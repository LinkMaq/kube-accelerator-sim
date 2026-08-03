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
	MaximumObservedClaims  = 65536
	MaximumDevicesPerSlice = 128
)

// TargetSelection identifies one context in a kubeconfig source. UseCurrent
// permits an interactive read-only caller to fill empty fields through the
// standard client-go loading rules; lifecycle callers leave it false and must
// supply both fields explicitly.
type TargetSelection struct {
	KubeconfigPath string
	ContextName    string
	UseCurrent     bool
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
	Verb          string
	Group         string
	Resource      string
	Subresource   string
	Namespace     string
	Name          string
	Namespaced    bool
	AllNamespaces bool
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
	if requirement.Namespaced &&
		requirement.Namespace == "" &&
		!requirement.AllNamespaces {
		return fmt.Errorf(
			"namespaced authorization requires an exact namespace or explicit all-namespace scope",
		)
	}
	if requirement.Namespaced &&
		requirement.Namespace != "" &&
		requirement.AllNamespaces {
		return fmt.Errorf(
			"namespaced authorization cannot combine an exact namespace with all-namespace scope",
		)
	}
	if !requirement.Namespaced &&
		(requirement.Namespace != "" || requirement.AllNamespaces) {
		return fmt.Errorf(
			"cluster-scoped authorization must not carry a namespace scope",
		)
	}
	if requirement.AllNamespaces && requirement.Name != "" {
		return fmt.Errorf(
			"all-namespace authorization cannot carry an ambiguous object name",
		)
	}
	return nil
}

// OwnershipScope is the exact immutable root used for observation and every
// owned mutation.
type OwnershipScope struct {
	instanceName      domain.Name
	instanceUID       domain.InstanceUID
	desiredGeneration domain.Generation
	fidelity          domain.FidelityMode
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

// ForFidelity narrows observation to the object families required by one
// accepted Scenario Instance. In particular, scheduling-only instances do not
// require DRA list/watch permissions.
func (scope OwnershipScope) ForFidelity(
	fidelity domain.FidelityMode,
) (OwnershipScope, error) {
	switch fidelity.String() {
	case "scheduling", "dra-control-plane":
		scope.fidelity = fidelity
		return scope, nil
	default:
		return OwnershipScope{}, fmt.Errorf(
			"ownership observation requires an accepted Fidelity Mode",
		)
	}
}

func (scope OwnershipScope) Fidelity() domain.FidelityMode {
	return scope.fidelity
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
	ObjectKindNode          ObjectKind = "Node"
	ObjectKindNodeStatus    ObjectKind = "NodeStatus"
	ObjectKindLease         ObjectKind = "Lease"
	ObjectKindDeviceClass   ObjectKind = "DeviceClass"
	ObjectKindResourceSlice ObjectKind = "ResourceSlice"
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
	case ObjectKindNode,
		ObjectKindNodeStatus,
		ObjectKindDeviceClass,
		ObjectKindResourceSlice:
		if namespace != "" {
			return ObjectKey{}, fmt.Errorf("%s is cluster-scoped", kind)
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
	DeviceClass       *ObservedDeviceClassState
	ResourceSlice     *ObservedResourceSliceState
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

// DeviceAttributeKind is the stable DRA v1 subset the Cluster port can carry.
type DeviceAttributeKind string

const (
	DeviceAttributeBool   DeviceAttributeKind = "bool"
	DeviceAttributeString DeviceAttributeKind = "string"
)

// DeviceAttributeValue prevents optional and feature-gated DRA fields from
// leaking into the portable mutation contract.
type DeviceAttributeValue struct {
	kind        DeviceAttributeKind
	boolValue   bool
	stringValue string
}

func NewBoolDeviceAttribute(value bool) DeviceAttributeValue {
	return DeviceAttributeValue{kind: DeviceAttributeBool, boolValue: value}
}

func NewStringDeviceAttribute(value string) (DeviceAttributeValue, error) {
	if value == "" || len(value) > 64 {
		return DeviceAttributeValue{}, fmt.Errorf(
			"stable DRA string attribute must contain 1 to 64 bytes",
		)
	}
	return DeviceAttributeValue{
		kind:        DeviceAttributeString,
		stringValue: value,
	}, nil
}

func (value DeviceAttributeValue) Kind() DeviceAttributeKind {
	return value.kind
}

func (value DeviceAttributeValue) Bool() bool {
	return value.boolValue
}

func (value DeviceAttributeValue) String() string {
	return value.stringValue
}

// DRADevice is one deterministic device from a simulator-owned pool.
type DRADevice struct {
	Name       string
	Attributes map[string]DeviceAttributeValue
}

// ObservedDeviceClassState is the complete portable DeviceClass spec subset.
type ObservedDeviceClassState struct {
	Selectors []string
}

// ObservedResourceSliceState is the complete portable ResourceSlice spec
// subset. It deliberately has no gated per-device fields.
type ObservedResourceSliceState struct {
	Driver             string
	PoolName           string
	PoolGeneration     int64
	ResourceSliceCount int64
	NodeName           string
	Devices            []DRADevice
}

// DRAAllocationResult is the scheduler-owned tuple from ResourceClaim status.
type DRAAllocationResult struct {
	Request string
	Driver  string
	Pool    string
	Device  string
}

// DRAConsumerReference is one exact scheduler-owned reservation.
type DRAConsumerReference struct {
	APIGroup string
	Resource string
	Name     string
	UID      string
}

// ObservedResourceClaim is read-only allocation and cleanup evidence. Claims
// never enter ObjectKinds or an OwnedChangeSet.
type ObservedResourceClaim struct {
	Namespace        string
	Name             string
	UID              string
	ResourceVersion  string
	DeviceClassNames []string
	Allocations      []DRAAllocationResult
	ReservedFor      []DRAConsumerReference
}

// ObservedPod is read-only blocker and resource-accounting evidence for a Pod
// bound to an owned Synthetic Node. Pods are intentionally not ObjectKinds
// and can never enter an OwnedChangeSet.
type ObservedPod struct {
	Namespace      string
	Name           string
	UID            string
	NodeName       string
	Phase          string
	Requested      map[string]string
	ResourceClaims []string
}

// ObservedGraph is a bounded exact-UID observation.
type ObservedGraph struct {
	Objects        []ObservedObject
	Pods           []ObservedPod
	ResourceClaims []ObservedResourceClaim
}

// ChangeKind is a closed intention, not a Kubernetes verb or patch type.
type ChangeKind string

const (
	ChangeApplySyntheticNode        ChangeKind = "ApplySyntheticNode"
	ChangeUpdateSyntheticNodeStatus ChangeKind = "UpdateSyntheticNodeStatus"
	ChangeApplyLease                ChangeKind = "ApplyLease"
	ChangeApplyDeviceClass          ChangeKind = "ApplyDeviceClass"
	ChangeApplyResourceSlice        ChangeKind = "ApplyResourceSlice"
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
		(key.kind != ObjectKindNode &&
			key.kind != ObjectKindLease &&
			key.kind != ObjectKindDeviceClass &&
			key.kind != ObjectKindResourceSlice) {
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

// DeviceClassInput contains only the portable stable-v1 selector contract.
// Driver configuration and extendedResourceName are excluded.
type DeviceClassInput struct {
	Selectors []string
}

// ApplyDeviceClass is one exact server-side apply intention.
type ApplyDeviceClass struct {
	key           ObjectKey
	preconditions ObjectPreconditions
	selectors     []string
}

func NewApplyDeviceClass(
	key ObjectKey,
	preconditions ObjectPreconditions,
	input DeviceClassInput,
) (OwnedChange, error) {
	if key.kind != ObjectKindDeviceClass {
		return nil, fmt.Errorf("DeviceClass apply requires a DeviceClass key")
	}
	if (preconditions.UID == "") != (preconditions.ResourceVersion == "") {
		return nil, fmt.Errorf(
			"DeviceClass update requires both UID and resourceVersion",
		)
	}
	if len(input.Selectors) == 0 {
		return nil, fmt.Errorf("DeviceClass apply requires at least one CEL selector")
	}
	for _, selector := range input.Selectors {
		if selector == "" {
			return nil, fmt.Errorf("DeviceClass CEL selector must not be empty")
		}
	}
	return ApplyDeviceClass{
		key:           key,
		preconditions: preconditions,
		selectors:     append([]string(nil), input.Selectors...),
	}, nil
}

func (change ApplyDeviceClass) Kind() ChangeKind {
	return ChangeApplyDeviceClass
}

func (change ApplyDeviceClass) Key() ObjectKey {
	return change.key
}

func (change ApplyDeviceClass) Preconditions() ObjectPreconditions {
	return change.preconditions
}

func (change ApplyDeviceClass) Selectors() []string {
	return append([]string(nil), change.selectors...)
}

func (ApplyDeviceClass) isOwnedChange() {}

// ResourceSliceInput contains only fields present in the Kubernetes 1.34
// stable portable subset.
type ResourceSliceInput struct {
	Driver             string
	PoolName           string
	PoolGeneration     int64
	ResourceSliceCount int64
	NodeName           string
	Devices            []DRADevice
}

// ApplyResourceSlice is one exact server-side apply intention.
type ApplyResourceSlice struct {
	key                ObjectKey
	preconditions      ObjectPreconditions
	driver             string
	poolName           string
	poolGeneration     int64
	resourceSliceCount int64
	nodeName           string
	devices            []DRADevice
}

func NewApplyResourceSlice(
	key ObjectKey,
	preconditions ObjectPreconditions,
	input ResourceSliceInput,
) (OwnedChange, error) {
	if key.kind != ObjectKindResourceSlice {
		return nil, fmt.Errorf("ResourceSlice apply requires a ResourceSlice key")
	}
	if (preconditions.UID == "") != (preconditions.ResourceVersion == "") {
		return nil, fmt.Errorf(
			"ResourceSlice update requires both UID and resourceVersion",
		)
	}
	if input.Driver == "" || input.PoolName == "" ||
		input.PoolGeneration <= 0 || input.ResourceSliceCount <= 0 ||
		input.NodeName == "" {
		return nil, fmt.Errorf(
			"ResourceSlice apply requires exact driver, pool, generation, count, and Node",
		)
	}
	if len(input.Devices) > MaximumDevicesPerSlice {
		return nil, fmt.Errorf(
			"ResourceSlice exceeds the %d-device portable limit",
			MaximumDevicesPerSlice,
		)
	}
	devices := cloneDRADevices(input.Devices)
	names := make(map[string]struct{}, len(devices))
	for _, device := range devices {
		if device.Name == "" {
			return nil, fmt.Errorf("ResourceSlice device requires an exact name")
		}
		if _, duplicate := names[device.Name]; duplicate {
			return nil, fmt.Errorf(
				"ResourceSlice has duplicate device %q",
				device.Name,
			)
		}
		names[device.Name] = struct{}{}
		if len(device.Attributes) > 32 {
			return nil, fmt.Errorf(
				"device %q exceeds the portable attribute limit",
				device.Name,
			)
		}
		for key, value := range device.Attributes {
			if key == "" ||
				(value.kind != DeviceAttributeBool &&
					value.kind != DeviceAttributeString) ||
				(value.kind == DeviceAttributeString &&
					(value.stringValue == "" || len(value.stringValue) > 64)) {
				return nil, fmt.Errorf(
					"device %q has invalid stable DRA attribute %q",
					device.Name,
					key,
				)
			}
		}
	}
	return ApplyResourceSlice{
		key:                key,
		preconditions:      preconditions,
		driver:             input.Driver,
		poolName:           input.PoolName,
		poolGeneration:     input.PoolGeneration,
		resourceSliceCount: input.ResourceSliceCount,
		nodeName:           input.NodeName,
		devices:            devices,
	}, nil
}

func (change ApplyResourceSlice) Kind() ChangeKind {
	return ChangeApplyResourceSlice
}

func (change ApplyResourceSlice) Key() ObjectKey {
	return change.key
}

func (change ApplyResourceSlice) Preconditions() ObjectPreconditions {
	return change.preconditions
}

func (change ApplyResourceSlice) Driver() string {
	return change.driver
}

func (change ApplyResourceSlice) PoolName() string {
	return change.poolName
}

func (change ApplyResourceSlice) PoolGeneration() int64 {
	return change.poolGeneration
}

func (change ApplyResourceSlice) ResourceSliceCount() int64 {
	return change.resourceSliceCount
}

func (change ApplyResourceSlice) NodeName() string {
	return change.nodeName
}

func (change ApplyResourceSlice) Devices() []DRADevice {
	return cloneDRADevices(change.devices)
}

func (ApplyResourceSlice) isOwnedChange() {}

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

// OwnedChangeSet is one bounded homogeneous mutation stage under one exact
// ownership root. Every Kubernetes object may appear at most once so adapters
// can execute the validated intentions concurrently without crossing lifecycle
// ordering barriers.
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
	targets := make(map[ObjectKey]int, len(changes))
	var stage mutationStage
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
			ChangeApplyDeviceClass,
			ChangeApplyResourceSlice,
			ChangeDeleteOwnedObject:
		default:
			return OwnedChangeSet{}, fmt.Errorf(
				"change %d is not an allowlisted intention",
				index,
			)
		}
		currentStage := stageOf(change)
		if index == 0 {
			stage = currentStage
		} else if currentStage != stage {
			return OwnedChangeSet{}, fmt.Errorf(
				"changes %d and %d cross mutation stages",
				0,
				index,
			)
		}
		target := mutationTarget(change.Key())
		if previous, found := targets[target]; found {
			return OwnedChangeSet{}, fmt.Errorf(
				"changes %d and %d target the same Kubernetes object",
				previous,
				index,
			)
		}
		targets[target] = index
		copied[index] = change
	}
	return OwnedChangeSet{scope: scope, mode: mode, changes: copied}, nil
}

type mutationStage struct {
	changeKind ChangeKind
	objectKind ObjectKind
}

func stageOf(change OwnedChange) mutationStage {
	stage := mutationStage{changeKind: change.Kind()}
	if change.Kind() == ChangeDeleteOwnedObject {
		stage.objectKind = change.Key().Kind()
	}
	return stage
}

func mutationTarget(key ObjectKey) ObjectKey {
	if key.kind == ObjectKindNodeStatus {
		key.kind = ObjectKindNode
	}
	return key
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
	ErrorStaleObservation             ErrorCode = "StaleObservation"
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

func cloneDRADevices(input []DRADevice) []DRADevice {
	result := make([]DRADevice, len(input))
	for index, device := range input {
		result[index] = device
		if device.Attributes != nil {
			result[index].Attributes = make(
				map[string]DeviceAttributeValue,
				len(device.Attributes),
			)
			for key, value := range device.Attributes {
				result[index].Attributes[key] = value
			}
		}
	}
	return result
}
