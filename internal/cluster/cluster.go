// Package cluster defines the intention-level Kubernetes Cluster seam used by
// the lifecycle Modules. It contains no Kubernetes runtime objects, patches,
// kubeconfig data, or generic CRUD surface.
package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

const (
	ManagedByLabel         = "app.kubernetes.io/managed-by"
	ManagedByValue         = "kube-accelerator-sim"
	InstanceUIDLabel       = "simulation.kasim.io/instance-uid"
	DesiredGenerationLabel = "simulation.kasim.io/desired-generation"
	MaximumOwnedChanges    = 4096
	MaximumObservedObjects = 16384
	MaximumObservedPods    = 65536
)

// TargetSelection identifies exactly one context in exactly one kubeconfig.
// Neither field has an environment or current-context fallback.
type TargetSelection struct {
	KubeconfigPath string
	ContextName    string
}

// ConnectionReceipt is the redacted immutable identity of one authenticated
// Simulation Target connection.
type ConnectionReceipt struct {
	ContextName             string
	CanonicalKubeconfigPath string
	APIServerURL            string
	TargetFingerprint       domain.Digest
	CADigest                domain.Digest
}

// AccessRequirement is one exact SelfSubjectAccessReview request. Group is
// empty only for the Kubernetes core group.
type AccessRequirement struct {
	Verb        string
	Group       string
	Resource    string
	Subresource string
	Namespace   string
	Name        string
	Namespaced  bool
}

// Validate rejects wildcards and fields that would make an authorization
// result broader or more ambiguous than the actual operation.
func (requirement AccessRequirement) Validate() error {
	if requirement.Verb == "" || requirement.Resource == "" {
		return fmt.Errorf("authorization requirement needs verb and resource")
	}
	for field, value := range map[string]string{
		"verb":        requirement.Verb,
		"group":       requirement.Group,
		"resource":    requirement.Resource,
		"subresource": requirement.Subresource,
		"namespace":   requirement.Namespace,
		"name":        requirement.Name,
	} {
		if strings.Contains(value, "*") {
			return fmt.Errorf("authorization %s must not contain a wildcard", field)
		}
	}
	if strings.Contains(requirement.Resource, "/") ||
		strings.Contains(requirement.Subresource, "/") {
		return fmt.Errorf("resource and subresource must be separate exact fields")
	}
	if requirement.Namespaced && requirement.Namespace == "" {
		return fmt.Errorf("namespaced authorization requires an exact namespace")
	}
	if !requirement.Namespaced && requirement.Namespace != "" {
		return fmt.Errorf("cluster-scoped authorization must not carry a namespace")
	}
	return nil
}

// OwnershipScope is the exact immutable root used for observation and every
// owned mutation.
type OwnershipScope struct {
	instanceName      domain.Name
	instanceUID       domain.InstanceUID
	desiredGeneration domain.Generation
}

// NewOwnershipScope rejects a missing UID and the create-precondition
// generation because no owned object may exist before revision acceptance.
func NewOwnershipScope(
	instanceUID domain.InstanceUID,
	desiredGeneration domain.Generation,
) (OwnershipScope, error) {
	if instanceUID.String() == "" {
		return OwnershipScope{}, fmt.Errorf("ownership scope requires an instance UID")
	}
	if desiredGeneration.Value() == 0 {
		return OwnershipScope{}, fmt.Errorf(
			"ownership scope requires a positive desired generation",
		)
	}
	return OwnershipScope{
		instanceUID:       instanceUID,
		desiredGeneration: desiredGeneration,
	}, nil
}

// NewInstanceOwnershipScope adds the cluster-scoped Scenario Instance name
// needed for the strongest legal Kubernetes owner reference.
func NewInstanceOwnershipScope(
	instanceName domain.Name,
	instanceUID domain.InstanceUID,
	desiredGeneration domain.Generation,
) (OwnershipScope, error) {
	if instanceName.String() == "" {
		return OwnershipScope{}, fmt.Errorf("ownership scope requires an instance name")
	}
	scope, err := NewOwnershipScope(instanceUID, desiredGeneration)
	if err != nil {
		return OwnershipScope{}, err
	}
	scope.instanceName = instanceName
	return scope, nil
}

func (scope OwnershipScope) InstanceName() domain.Name {
	return scope.instanceName
}

func (scope OwnershipScope) InstanceUID() domain.InstanceUID {
	return scope.instanceUID
}

func (scope OwnershipScope) DesiredGeneration() domain.Generation {
	return scope.desiredGeneration
}

func (scope OwnershipScope) ManagedBy() string {
	return ManagedByValue
}

// TargetCapabilities is the versioned, bounded discovery result consumed by
// lifecycle Modules.
type TargetCapabilities struct {
	ServerVersion   string
	KubernetesMinor int
	Resources       []ResourceCapability
}

// ResourceCapability records one exact discovered Kubernetes resource.
type ResourceCapability struct {
	GroupVersion string
	Resource     string
	Namespaced   bool
	Verbs        []string
}

// AuthorizationDecision preserves the exact reviewed attributes and result.
type AuthorizationDecision struct {
	Requirement     AccessRequirement
	Allowed         bool
	Reason          string
	EvaluationError string
}

// AuthorizationReport returns one decision for every requested operation.
type AuthorizationReport struct {
	Decisions []AuthorizationDecision
}

// ObjectKind is the closed set of Kubernetes object families that the Cluster
// port can observe or mutate.
type ObjectKind string

const (
	ObjectKindNode       ObjectKind = "Node"
	ObjectKindNodeStatus ObjectKind = "NodeStatus"
	ObjectKindLease      ObjectKind = "Lease"
)

// ObjectKey identifies one allowlisted Kubernetes object.
type ObjectKey struct {
	kind      ObjectKind
	namespace string
	name      string
}

// NewObjectKey enforces kind scope before a key can enter a change set.
func NewObjectKey(kind ObjectKind, namespace, name string) (ObjectKey, error) {
	if name == "" || strings.ContainsAny(name, "*?[]") {
		return ObjectKey{}, fmt.Errorf("owned object requires one exact name")
	}
	switch kind {
	case ObjectKindNode, ObjectKindNodeStatus:
		if namespace != "" {
			return ObjectKey{}, fmt.Errorf("Node is cluster-scoped")
		}
	case ObjectKindLease:
		if namespace == "" {
			return ObjectKey{}, fmt.Errorf("Lease requires one exact namespace")
		}
	default:
		return ObjectKey{}, fmt.Errorf("unsupported owned object kind %q", kind)
	}
	return ObjectKey{kind: kind, namespace: namespace, name: name}, nil
}

func (key ObjectKey) Kind() ObjectKind {
	return key.kind
}

func (key ObjectKey) Namespace() string {
	return key.namespace
}

func (key ObjectKey) Name() string {
	return key.name
}

// ObjectPreconditions are repeated immediately before mutation.
type ObjectPreconditions struct {
	UID             string
	ResourceVersion string
}

// ObservedObject is the ownership and concurrency identity of one allowlisted
// object. Kind-specific desired and observed state stays in later graph
// fragments rather than generic maps.
type ObservedObject struct {
	Key               ObjectKey
	UID               string
	ResourceVersion   string
	DesiredGeneration domain.Generation
	Node              *ObservedNodeState
	Lease             *ObservedLeaseState
}

// ObservedNodeState is the bounded scheduler-visible state of an exactly
// owned Synthetic Node.
type ObservedNodeState struct {
	Labels        map[string]string
	Annotations   map[string]string
	Taints        []NodeTaint
	Unschedulable bool
	Capacity      map[string]string
	Allocatable   map[string]string
	Ready         bool
}

// ObservedLeaseState is the bounded heartbeat state of one exact Node Lease.
type ObservedLeaseState struct {
	HolderIdentity       string
	LeaseDurationSeconds int32
	RenewTime            time.Time
}

// ObservedPod is read-only blocker and resource-accounting evidence for a Pod
// bound to an owned Synthetic Node. Pods are intentionally not ObjectKinds
// and can never enter an OwnedChangeSet.
type ObservedPod struct {
	Namespace string
	Name      string
	UID       string
	NodeName  string
	Phase     string
	Requested map[string]string
}

// ObservedGraph is a bounded exact-UID observation.
type ObservedGraph struct {
	Objects []ObservedObject
	Pods    []ObservedPod
}

// ChangeKind is a closed intention, not a Kubernetes verb or patch type.
type ChangeKind string

const (
	ChangeApplySyntheticNode        ChangeKind = "ApplySyntheticNode"
	ChangeUpdateSyntheticNodeStatus ChangeKind = "UpdateSyntheticNodeStatus"
	ChangeApplyLease                ChangeKind = "ApplyLease"
	ChangeDeleteOwnedObject         ChangeKind = "DeleteOwnedObject"
)

// OwnedChange is sealed to this package so callers cannot inject generic
// Kubernetes objects or patch bytes.
type OwnedChange interface {
	Kind() ChangeKind
	Key() ObjectKey
	Preconditions() ObjectPreconditions
	isOwnedChange()
}

type DeleteOwnedObject struct {
	key           ObjectKey
	preconditions ObjectPreconditions
}

// NewDeleteOwnedObject requires the exact server identity observed by the
// reconciler. There is no selector, wildcard, or force-delete variant.
func NewDeleteOwnedObject(
	key ObjectKey,
	preconditions ObjectPreconditions,
) (OwnedChange, error) {
	if key.name == "" ||
		(key.kind != ObjectKindNode && key.kind != ObjectKindLease) {
		return nil, fmt.Errorf("delete requires an allowlisted object key")
	}
	if preconditions.UID == "" || preconditions.ResourceVersion == "" {
		return nil, fmt.Errorf(
			"delete requires exact UID and resourceVersion preconditions",
		)
	}
	return DeleteOwnedObject{
		key:           key,
		preconditions: preconditions,
	}, nil
}

func (change DeleteOwnedObject) Kind() ChangeKind {
	return ChangeDeleteOwnedObject
}

func (change DeleteOwnedObject) Key() ObjectKey {
	return change.key
}

func (change DeleteOwnedObject) Preconditions() ObjectPreconditions {
	return change.preconditions
}

func (DeleteOwnedObject) isOwnedChange() {}

// NodeTaint is the transport-neutral subset applied to a Synthetic Node.
type NodeTaint struct {
	Key    string
	Value  string
	Effect string
}

// SyntheticNodeInput is the declarative allowlisted Node intent.
type SyntheticNodeInput struct {
	Labels        map[string]string
	Annotations   map[string]string
	Taints        []NodeTaint
	Unschedulable bool
}

// ApplySyntheticNode is one server-side apply intention.
type ApplySyntheticNode struct {
	key           ObjectKey
	preconditions ObjectPreconditions
	labels        map[string]string
	annotations   map[string]string
	taints        []NodeTaint
	unschedulable bool
}

func NewApplySyntheticNode(
	key ObjectKey,
	preconditions ObjectPreconditions,
	input SyntheticNodeInput,
) (OwnedChange, error) {
	if key.kind != ObjectKindNode {
		return nil, fmt.Errorf("Synthetic Node apply requires a Node key")
	}
	if (preconditions.UID == "") != (preconditions.ResourceVersion == "") {
		return nil, fmt.Errorf(
			"Synthetic Node update requires both UID and resourceVersion",
		)
	}
	for _, reserved := range []string{
		ManagedByLabel,
		InstanceUIDLabel,
		DesiredGenerationLabel,
	} {
		if _, found := input.Labels[reserved]; found {
			return nil, fmt.Errorf(
				"Synthetic Node input must not override reserved label %q",
				reserved,
			)
		}
	}
	for key, value := range input.Labels {
		if key == "" || value == "" {
			return nil, fmt.Errorf("Synthetic Node labels must be non-empty")
		}
	}
	for key := range input.Annotations {
		if key == "" {
			return nil, fmt.Errorf("Synthetic Node annotation key must be non-empty")
		}
	}
	for _, taint := range input.Taints {
		if taint.Key == "" || taint.Effect == "" {
			return nil, fmt.Errorf("Synthetic Node taint requires key and effect")
		}
	}
	return ApplySyntheticNode{
		key:           key,
		preconditions: preconditions,
		labels:        cloneStringMap(input.Labels),
		annotations:   cloneStringMap(input.Annotations),
		taints:        append([]NodeTaint(nil), input.Taints...),
		unschedulable: input.Unschedulable,
	}, nil
}

func (change ApplySyntheticNode) Kind() ChangeKind {
	return ChangeApplySyntheticNode
}

func (change ApplySyntheticNode) Key() ObjectKey {
	return change.key
}

func (change ApplySyntheticNode) Preconditions() ObjectPreconditions {
	return change.preconditions
}

func (change ApplySyntheticNode) Labels() map[string]string {
	return cloneStringMap(change.labels)
}

func (change ApplySyntheticNode) Annotations() map[string]string {
	return cloneStringMap(change.annotations)
}

func (change ApplySyntheticNode) Taints() []NodeTaint {
	return append([]NodeTaint(nil), change.taints...)
}

func (change ApplySyntheticNode) Unschedulable() bool {
	return change.unschedulable
}

func (ApplySyntheticNode) isOwnedChange() {}

// SyntheticNodeStatusInput contains only scheduler-visible status fields.
type SyntheticNodeStatusInput struct {
	Capacity    map[string]string
	Allocatable map[string]string
	ManageReady bool
	Ready       bool
	ObservedAt  time.Time
}

// UpdateSyntheticNodeStatus is one status-subresource apply intention.
type UpdateSyntheticNodeStatus struct {
	key           ObjectKey
	preconditions ObjectPreconditions
	capacity      map[string]string
	allocatable   map[string]string
	manageReady   bool
	ready         bool
	observedAt    time.Time
}

func NewUpdateSyntheticNodeStatus(
	key ObjectKey,
	preconditions ObjectPreconditions,
	input SyntheticNodeStatusInput,
) (OwnedChange, error) {
	if key.kind != ObjectKindNodeStatus {
		return nil, fmt.Errorf("Synthetic Node status requires a NodeStatus key")
	}
	if preconditions.UID == "" || preconditions.ResourceVersion == "" {
		return nil, fmt.Errorf(
			"Synthetic Node status requires UID and resourceVersion",
		)
	}
	if input.ObservedAt.IsZero() {
		return nil, fmt.Errorf("Synthetic Node status requires an observation time")
	}
	if len(input.Capacity) == 0 || len(input.Allocatable) == 0 {
		return nil, fmt.Errorf(
			"Synthetic Node status requires capacity and allocatable",
		)
	}
	return UpdateSyntheticNodeStatus{
		key:           key,
		preconditions: preconditions,
		capacity:      cloneStringMap(input.Capacity),
		allocatable:   cloneStringMap(input.Allocatable),
		manageReady:   input.ManageReady,
		ready:         input.Ready,
		observedAt:    input.ObservedAt.UTC(),
	}, nil
}

func (change UpdateSyntheticNodeStatus) Kind() ChangeKind {
	return ChangeUpdateSyntheticNodeStatus
}

func (change UpdateSyntheticNodeStatus) Key() ObjectKey {
	return change.key
}

func (change UpdateSyntheticNodeStatus) Preconditions() ObjectPreconditions {
	return change.preconditions
}

func (change UpdateSyntheticNodeStatus) Capacity() map[string]string {
	return cloneStringMap(change.capacity)
}

func (change UpdateSyntheticNodeStatus) Allocatable() map[string]string {
	return cloneStringMap(change.allocatable)
}

func (change UpdateSyntheticNodeStatus) Ready() bool {
	return change.ready
}

func (change UpdateSyntheticNodeStatus) ManagesReady() bool {
	return change.manageReady
}

func (change UpdateSyntheticNodeStatus) ObservedAt() time.Time {
	return change.observedAt
}

func (UpdateSyntheticNodeStatus) isOwnedChange() {}

// LeaseInput is the bounded Node heartbeat intent.
type LeaseInput struct {
	HolderIdentity       string
	LeaseDurationSeconds int32
	RenewTime            time.Time
}

// ApplyLease is one server-side apply intention for an exact owned Lease.
type ApplyLease struct {
	key                  ObjectKey
	preconditions        ObjectPreconditions
	holderIdentity       string
	leaseDurationSeconds int32
	renewTime            time.Time
}

func NewApplyLease(
	key ObjectKey,
	preconditions ObjectPreconditions,
	input LeaseInput,
) (OwnedChange, error) {
	if key.kind != ObjectKindLease {
		return nil, fmt.Errorf("Lease apply requires a Lease key")
	}
	if (preconditions.UID == "") != (preconditions.ResourceVersion == "") {
		return nil, fmt.Errorf("Lease update requires both UID and resourceVersion")
	}
	if input.HolderIdentity == "" || input.LeaseDurationSeconds <= 0 ||
		input.RenewTime.IsZero() {
		return nil, fmt.Errorf(
			"Lease apply requires holder, positive duration, and renew time",
		)
	}
	return ApplyLease{
		key:                  key,
		preconditions:        preconditions,
		holderIdentity:       input.HolderIdentity,
		leaseDurationSeconds: input.LeaseDurationSeconds,
		renewTime:            input.RenewTime.UTC(),
	}, nil
}

func (change ApplyLease) Kind() ChangeKind {
	return ChangeApplyLease
}

func (change ApplyLease) Key() ObjectKey {
	return change.key
}

func (change ApplyLease) Preconditions() ObjectPreconditions {
	return change.preconditions
}

func (change ApplyLease) HolderIdentity() string {
	return change.holderIdentity
}

func (change ApplyLease) LeaseDurationSeconds() int32 {
	return change.leaseDurationSeconds
}

func (change ApplyLease) RenewTime() time.Time {
	return change.renewTime
}

func (ApplyLease) isOwnedChange() {}

// ExecutionMode distinguishes admission-only execution from persistence.
type ExecutionMode string

const (
	ExecutionServerDryRun ExecutionMode = "server-dry-run"
	ExecutionPersistent   ExecutionMode = "persistent"
)

// OwnedChangeSet is one bounded ordered mutation batch under one exact
// ownership root.
type OwnedChangeSet struct {
	scope   OwnershipScope
	mode    ExecutionMode
	changes []OwnedChange
}

// NewOwnedChangeSet validates the complete batch before an adapter can make a
// request.
func NewOwnedChangeSet(
	scope OwnershipScope,
	mode ExecutionMode,
	changes []OwnedChange,
) (OwnedChangeSet, error) {
	if scope.instanceUID.String() == "" || scope.desiredGeneration.Value() == 0 {
		return OwnedChangeSet{}, fmt.Errorf("change set requires exact ownership")
	}
	if mode != ExecutionServerDryRun && mode != ExecutionPersistent {
		return OwnedChangeSet{}, fmt.Errorf("unsupported execution mode %q", mode)
	}
	if len(changes) == 0 || len(changes) > MaximumOwnedChanges {
		return OwnedChangeSet{}, fmt.Errorf(
			"change set must contain 1 to %d changes",
			MaximumOwnedChanges,
		)
	}
	copied := make([]OwnedChange, len(changes))
	for index, change := range changes {
		if change == nil || change.Key().name == "" {
			return OwnedChangeSet{}, fmt.Errorf(
				"change %d is not an allowlisted intention",
				index,
			)
		}
		switch change.Kind() {
		case ChangeApplySyntheticNode,
			ChangeUpdateSyntheticNodeStatus,
			ChangeApplyLease,
			ChangeDeleteOwnedObject:
		default:
			return OwnedChangeSet{}, fmt.Errorf(
				"change %d is not an allowlisted intention",
				index,
			)
		}
		copied[index] = change
	}
	return OwnedChangeSet{scope: scope, mode: mode, changes: copied}, nil
}

func (changeSet OwnedChangeSet) Scope() OwnershipScope {
	return changeSet.scope
}

func (changeSet OwnedChangeSet) Mode() ExecutionMode {
	return changeSet.mode
}

func (changeSet OwnedChangeSet) Changes() []OwnedChange {
	return append([]OwnedChange(nil), changeSet.changes...)
}

// MutationReceipt is bounded execution evidence.
type MutationReceipt struct {
	DryRun    bool
	Attempted int
	Persisted int
}

// ErrorCode is a stable external failure classification.
type ErrorCode string

const (
	ErrorTargetUnavailable            ErrorCode = "TargetUnavailable"
	ErrorAuthenticationFailed         ErrorCode = "AuthenticationFailed"
	ErrorAuthorizationDenied          ErrorCode = "AuthorizationDenied"
	ErrorCapabilityUnavailable        ErrorCode = "CapabilityUnavailable"
	ErrorRuntimeUnavailable           ErrorCode = "RuntimeUnavailable"
	ErrorKubernetesVersionUnsupported ErrorCode = "KubernetesVersionUnsupported"
	ErrorKubernetesVersionUntested    ErrorCode = "KubernetesVersionUntested"
	ErrorOwnershipConflict            ErrorCode = "OwnershipConflict"
	ErrorUIDConflict                  ErrorCode = "UIDConflict"
	ErrorResourceVersionConflict      ErrorCode = "ResourceVersionConflict"
	ErrorAdmissionRejected            ErrorCode = "AdmissionRejected"
	ErrorRateLimited                  ErrorCode = "RateLimited"
	ErrorTransient                    ErrorCode = "Transient"
	ErrorInvalidIntent                ErrorCode = "InvalidIntent"
)

// Error preserves a stable code without exposing credentials or raw server
// response bodies.
type Error struct {
	Code      ErrorCode
	Message   string
	Retryable bool
}

func (clusterError *Error) Error() string {
	return clusterError.Message
}

func NewError(code ErrorCode, message string, retryable bool) error {
	return &Error{Code: code, Message: message, Retryable: retryable}
}

func ErrorCodeOf(err error) ErrorCode {
	var clusterError *Error
	if errors.As(err, &clusterError) {
		return clusterError.Code
	}
	return ""
}

// Port is the small intention-level Interface at the true external
// Kubernetes seam.
type Port interface {
	Discover(context.Context) (TargetCapabilities, error)
	Authorize(context.Context, []AccessRequirement) (AuthorizationReport, error)
	Observe(context.Context, OwnershipScope) (ObservedGraph, error)
	Execute(context.Context, OwnedChangeSet) (MutationReceipt, error)
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
