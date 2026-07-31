// Package application contains the deep Scenario Runtime workflow. This file
// establishes its ordered zero-write apply preflight before the lifecycle
// entry points are connected to CLI delivery.
package application

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/controlplane"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

const serverDryRunWarning = "server dry-run does not reserve cluster state; apply repeats all conflict checks"

const revisionSubmitAttemptLimit = 8

// ConnectedTarget is one immutable explicit Simulation Target carrying the
// two already-accepted remote seams. Target loading stays a concrete delivery
// concern rather than becoming a fourth behavioral seam.
type ConnectedTarget struct {
	Receipt      cluster.ConnectionReceipt
	Target       controlplane.ExplicitTarget
	ControlPlane controlplane.ScenarioControlPlane
	Cluster      cluster.Port
}

type ConnectorFunc func(
	context.Context,
	cluster.TargetSelection,
) (ConnectedTarget, error)

// PreflightApplyRequest contains already compiled intention-level desired
// state plus the exact permissions required for this operation.
type PreflightApplyRequest struct {
	Selection      cluster.TargetSelection
	Intent         controlplane.RevisionIntent
	RequiredAccess []cluster.AccessRequirement
}

// PreflightApplyResult is non-persistent evidence only.
type PreflightApplyResult struct {
	Connection    cluster.ConnectionReceipt
	ControlPlane  controlplane.TargetCapabilities
	Cluster       cluster.TargetCapabilities
	Authorization cluster.AuthorizationReport
	Observed      cluster.ObservedGraph
	CurrentFound  bool
	Intent        controlplane.RevisionIntent
	Proposed      controlplane.SubmissionReceipt
	Warning       string
	retryBaseline *revisionRetryBaseline
}

// PreflightApply performs the normative checks in order and ends with
// Kubernetes admission using dryRun=All. It never accepts a revision or
// executes an owned persistent change set.
func PreflightApply(
	ctx context.Context,
	request PreflightApplyRequest,
	connect ConnectorFunc,
) (PreflightApplyResult, error) {
	intent := request.Intent
	intent.Revision = controlplane.CloneRevision(request.Intent.Revision)
	if err := controlplane.ValidateRevisionIntent(intent); err != nil {
		return PreflightApplyResult{}, err
	}
	requiredAccess := append(
		[]cluster.AccessRequirement(nil),
		request.RequiredAccess...,
	)
	requiredAccess = append(
		requiredAccess,
		ownershipObservationAccessRequirements(intent.Fidelity.String())...,
	)
	if request.Selection.KubeconfigPath == "" ||
		request.Selection.ContextName == "" {
		return PreflightApplyResult{}, cluster.NewError(
			cluster.ErrorInvalidIntent,
			"apply requires one explicit kubeconfig and matching context",
			false,
		)
	}
	if len(requiredAccess) == 0 {
		return PreflightApplyResult{}, cluster.NewError(
			cluster.ErrorInvalidIntent,
			"apply preflight requires exact authorization operations",
			false,
		)
	}
	for _, requirement := range requiredAccess {
		if err := requirement.Validate(); err != nil {
			return PreflightApplyResult{}, cluster.NewError(
				cluster.ErrorInvalidIntent,
				err.Error(),
				false,
			)
		}
	}
	if connect == nil {
		return PreflightApplyResult{}, cluster.NewError(
			cluster.ErrorInvalidIntent,
			"apply preflight has no explicit target connector",
			false,
		)
	}

	connection, err := connect(ctx, request.Selection)
	if err != nil {
		return PreflightApplyResult{}, err
	}
	if connection.ControlPlane == nil || connection.Cluster == nil {
		return PreflightApplyResult{}, cluster.NewError(
			cluster.ErrorTargetUnavailable,
			"explicit target connection is incomplete",
			false,
		)
	}
	target := connection.Target
	if target.ContextName != request.Selection.ContextName {
		return PreflightApplyResult{}, cluster.NewError(
			cluster.ErrorTargetUnavailable,
			"connected target context does not match explicit selection",
			false,
		)
	}
	command := intent.Bind(target)
	if err := controlplane.ValidateCommand(command); err != nil {
		return PreflightApplyResult{}, err
	}
	connectionReceipt := connection.Receipt
	if connectionReceipt.ContextName != target.ContextName ||
		connectionReceipt.CanonicalKubeconfigPath == "" ||
		connectionReceipt.APIServerURL == "" ||
		connectionReceipt.CADigest.String() == "" {
		return PreflightApplyResult{}, cluster.NewError(
			cluster.ErrorTargetUnavailable,
			"explicit target returned an incomplete connection receipt",
			false,
		)
	}
	if connectionReceipt.TargetFingerprint != target.Fingerprint {
		return PreflightApplyResult{}, controlplane.NewError(
			controlplane.ErrorTargetMismatch,
			"connection receipt does not match the connected target",
			"",
		)
	}
	result := PreflightApplyResult{Connection: connectionReceipt}

	result.ControlPlane, err = connection.ControlPlane.Probe(ctx, target)
	if err != nil {
		return PreflightApplyResult{}, err
	}
	if result.ControlPlane.TargetFingerprint != target.Fingerprint {
		return PreflightApplyResult{}, controlplane.NewError(
			controlplane.ErrorTargetMismatch,
			"Scenario Control Plane probe returned another target",
			"",
		)
	}
	result.Cluster, err = connection.Cluster.Discover(ctx)
	if err != nil {
		return PreflightApplyResult{}, err
	}
	if err := validateFidelityCapabilities(intent.Fidelity.String(), result.Cluster); err != nil {
		return PreflightApplyResult{}, err
	}
	result.Authorization, err = connection.Cluster.Authorize(
		ctx,
		requiredAccess,
	)
	if err != nil {
		return PreflightApplyResult{}, err
	}
	if len(result.Authorization.Decisions) != len(requiredAccess) {
		return PreflightApplyResult{}, cluster.NewError(
			cluster.ErrorCapabilityUnavailable,
			"authorization response did not cover every exact operation",
			false,
		)
	}
	for index, decision := range result.Authorization.Decisions {
		if decision.Requirement != requiredAccess[index] {
			return PreflightApplyResult{}, cluster.NewError(
				cluster.ErrorCapabilityUnavailable,
				"authorization response did not preserve exact operation identity",
				false,
			)
		}
		if !decision.Allowed {
			return PreflightApplyResult{}, cluster.NewError(
				cluster.ErrorAuthorizationDenied,
				fmt.Sprintf(
					"authorization denied %s on %s/%s",
					decision.Requirement.Verb,
					decision.Requirement.Group,
					decision.Requirement.Resource,
				),
				false,
			)
		}
	}

	current, readErr := connection.ControlPlane.Read(
		ctx,
		controlplane.InstanceKey{
			TargetFingerprint: target.Fingerprint,
			Name:              command.Name,
		},
	)
	switch controlplane.ErrorCodeOf(readErr) {
	case "":
		if readErr != nil {
			return PreflightApplyResult{}, readErr
		}
		result.CurrentFound = true
		result.retryBaseline = newRevisionRetryBaseline(current)
		if command.Preconditions.ResourceVersion == "" &&
			command.Preconditions.InstanceUID == current.InstanceUID &&
			command.Preconditions.ExpectedGeneration == current.DesiredGeneration {
			command.Preconditions.ResourceVersion = current.ResourceVersion
			intent.Preconditions.ResourceVersion = current.ResourceVersion
		}
		scope, err := cluster.NewInstanceOwnershipScope(
			current.Name,
			current.InstanceUID,
			current.DesiredGeneration,
		)
		if err != nil {
			return PreflightApplyResult{}, err
		}
		scope, err = scope.ForFidelity(current.Fidelity)
		if err != nil {
			return PreflightApplyResult{}, err
		}
		result.Observed, err = connection.Cluster.Observe(ctx, scope)
		if err != nil {
			return PreflightApplyResult{}, err
		}
	case controlplane.ErrorNotFound:
	default:
		return PreflightApplyResult{}, readErr
	}

	dryRunCommand := command
	dryRunCommand.ServerDryRun = true
	result.Proposed, dryRunCommand, err = submitRevisionWithStatusDriftRetry(
		ctx,
		connection.ControlPlane,
		dryRunCommand,
		result.retryBaseline,
	)
	if err != nil {
		return PreflightApplyResult{}, err
	}
	intent.Preconditions.ResourceVersion =
		dryRunCommand.Preconditions.ResourceVersion
	if !result.Proposed.DryRun || result.Proposed.Accepted {
		return PreflightApplyResult{}, cluster.NewError(
			cluster.ErrorAdmissionRejected,
			"Scenario Control Plane did not preserve server dry-run semantics",
			false,
		)
	}
	result.Intent = intent
	result.Warning = serverDryRunWarning
	return result, nil
}

type revisionRetryBaseline struct {
	target            controlplane.ExplicitTarget
	name              domain.Name
	instanceUID       domain.InstanceUID
	deletionRequested bool
	creationIdentity  string
	fidelity          domain.FidelityMode
	desiredGeneration domain.Generation
	revision          controlplane.ScenarioRevision
	revisions         []controlplane.ScenarioRevision
}

func newRevisionRetryBaseline(
	record controlplane.InstanceRecord,
) *revisionRetryBaseline {
	revisions := make(
		[]controlplane.ScenarioRevision,
		len(record.Revisions),
	)
	for index := range record.Revisions {
		revisions[index] = controlplane.CloneRevision(record.Revisions[index])
	}
	return &revisionRetryBaseline{
		target:            record.Target,
		name:              record.Name,
		instanceUID:       record.InstanceUID,
		deletionRequested: record.DeletionRequested,
		creationIdentity:  record.CreationIdentity,
		fidelity:          record.Fidelity,
		desiredGeneration: record.DesiredGeneration,
		revision:          controlplane.CloneRevision(record.Revision),
		revisions:         revisions,
	}
}

func submitRevisionWithStatusDriftRetry(
	ctx context.Context,
	controlPlane controlplane.ScenarioControlPlane,
	command controlplane.RevisionCommand,
	baseline *revisionRetryBaseline,
) (
	controlplane.SubmissionReceipt,
	controlplane.RevisionCommand,
	error,
) {
	for attempt := 0; attempt < revisionSubmitAttemptLimit; attempt++ {
		submission, err := controlPlane.Submit(ctx, command)
		if controlplane.ErrorCodeOf(err) !=
			controlplane.ErrorResourceVersionConflict {
			return submission, command, err
		}
		if baseline == nil ||
			command.Preconditions.InstanceUID.String() == "" ||
			command.Preconditions.ExpectedGeneration.Value() == 0 {
			return controlplane.SubmissionReceipt{}, command, err
		}
		if attempt == revisionSubmitAttemptLimit-1 {
			return controlplane.SubmissionReceipt{}, command, controlplane.NewError(
				controlplane.ErrorResourceVersionConflict,
				"Scenario Instance kept changing during revision submission",
				"",
			)
		}
		if waitErr := waitForRevisionRetry(ctx, attempt); waitErr != nil {
			return controlplane.SubmissionReceipt{}, command, waitErr
		}
		current, readErr := controlPlane.Read(ctx, controlplane.InstanceKey{
			TargetFingerprint: command.Target.Fingerprint,
			Name:              command.Name,
		})
		if readErr != nil {
			return controlplane.SubmissionReceipt{}, command, readErr
		}
		if retryErr := validateRevisionRetry(
			current,
			command,
			baseline,
		); retryErr != nil {
			return controlplane.SubmissionReceipt{}, command, retryErr
		}
		command.Preconditions.ResourceVersion = current.ResourceVersion
	}
	panic("unreachable revision submission loop")
}

func validateRevisionRetry(
	current controlplane.InstanceRecord,
	command controlplane.RevisionCommand,
	baseline *revisionRetryBaseline,
) error {
	switch {
	case baseline == nil:
		return controlplane.NewError(
			controlplane.ErrorResourceVersionConflict,
			"Scenario Instance has no retry baseline",
			"",
		)
	case current.Target.Fingerprint != command.Target.Fingerprint:
		return controlplane.NewError(
			controlplane.ErrorTargetMismatch,
			"Scenario Instance target fingerprint changed during revision submission",
			"",
		)
	case current.InstanceUID != command.Preconditions.InstanceUID:
		return controlplane.NewError(
			controlplane.ErrorUIDConflict,
			"Scenario Instance UID changed during revision submission",
			"",
		)
	case current.DesiredGeneration != command.Preconditions.ExpectedGeneration:
		return controlplane.NewError(
			controlplane.ErrorGenerationConflict,
			"Scenario Instance generation changed during revision submission",
			"",
		)
	case current.CreationIdentity != command.CreationIdentity:
		return controlplane.NewError(
			controlplane.ErrorCreationIdentityConflict,
			"Scenario Instance creation identity changed during revision submission",
			"",
		)
	case current.Fidelity != command.Fidelity:
		return controlplane.NewError(
			controlplane.ErrorFidelityConflict,
			"Scenario Instance Fidelity Mode changed during revision submission",
			"",
		)
	case current.DeletionRequested:
		return controlplane.NewError(
			controlplane.ErrorResourceVersionConflict,
			"Scenario Instance deletion started during revision submission",
			"",
		)
	case current.ResourceVersion == "":
		return controlplane.NewError(
			controlplane.ErrorResourceVersionConflict,
			"Scenario Instance returned no resourceVersion during revision submission",
			"",
		)
	case current.Target != baseline.target ||
		current.Name != baseline.name ||
		current.InstanceUID != baseline.instanceUID ||
		current.DeletionRequested != baseline.deletionRequested ||
		current.CreationIdentity != baseline.creationIdentity ||
		current.Fidelity != baseline.fidelity ||
		current.DesiredGeneration != baseline.desiredGeneration ||
		!scenarioRevisionEqual(current.Revision, baseline.revision) ||
		!scenarioRevisionsEqual(current.Revisions, baseline.revisions):
		return controlplane.NewError(
			controlplane.ErrorResourceVersionConflict,
			"Scenario Instance logical spec or revision history changed during submission",
			"",
		)
	default:
		return nil
	}
}

func scenarioRevisionsEqual(
	left, right []controlplane.ScenarioRevision,
) bool {
	return slices.EqualFunc(left, right, scenarioRevisionEqual)
}

func scenarioRevisionEqual(
	left, right controlplane.ScenarioRevision,
) bool {
	return left.Generation == right.Generation &&
		left.Digest == right.Digest &&
		bytes.Equal(left.CanonicalScenario, right.CanonicalScenario) &&
		slices.Equal(left.Profiles, right.Profiles)
}

func waitForRevisionRetry(ctx context.Context, attempt int) error {
	delay := 5 * time.Millisecond * time.Duration(1<<attempt)
	if delay > 250*time.Millisecond {
		delay = 250 * time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func ownershipObservationAccessRequirements(
	fidelity string,
) []cluster.AccessRequirement {
	requirements := []cluster.AccessRequirement{
		{
			Verb:     "list",
			Resource: "nodes",
		},
		{
			Verb:       "list",
			Group:      "coordination.k8s.io",
			Resource:   "leases",
			Namespace:  "kube-node-lease",
			Namespaced: true,
		},
		{
			Verb:          "list",
			Resource:      "pods",
			Namespaced:    true,
			AllNamespaces: true,
		},
	}
	if fidelity != "dra-control-plane" {
		return requirements
	}
	return append(requirements, draObservationAccessRequirements()...)
}

func draObservationAccessRequirements() []cluster.AccessRequirement {
	return []cluster.AccessRequirement{
		{
			Verb:     "list",
			Group:    "resource.k8s.io",
			Resource: "deviceclasses",
		},
		{
			Verb:     "list",
			Group:    "resource.k8s.io",
			Resource: "resourceslices",
		},
		{
			Verb:          "list",
			Group:         "resource.k8s.io",
			Resource:      "resourceclaims",
			Namespaced:    true,
			AllNamespaces: true,
		},
	}
}

func validateFidelityCapabilities(
	fidelity string,
	capabilities cluster.TargetCapabilities,
) error {
	if fidelity != "dra-control-plane" {
		return nil
	}
	switch {
	case capabilities.KubernetesMinor < 34:
		return cluster.NewError(
			cluster.ErrorKubernetesVersionUnsupported,
			"stable DRA control-plane projection requires Kubernetes 1.34 or newer",
			false,
		)
	case capabilities.KubernetesMinor > 36:
		return cluster.NewError(
			cluster.ErrorKubernetesVersionUntested,
			"stable DRA control-plane projection is validated only through Kubernetes 1.36",
			false,
		)
	}
	type requirement struct {
		groupVersion string
		resource     string
		namespaced   bool
		verbs        []string
	}
	requirements := []requirement{
		{
			groupVersion: "resource.k8s.io/v1",
			resource:     "deviceclasses",
			verbs:        []string{"get", "list", "watch", "create", "patch", "delete"},
		},
		{
			groupVersion: "resource.k8s.io/v1",
			resource:     "resourceslices",
			verbs:        []string{"get", "list", "watch", "create", "patch", "delete"},
		},
		{
			groupVersion: "resource.k8s.io/v1",
			resource:     "resourceclaims",
			namespaced:   true,
			verbs:        []string{"get", "list", "watch"},
		},
		{
			groupVersion: "v1",
			resource:     "pods",
			namespaced:   true,
			verbs:        []string{"get", "list", "watch"},
		},
	}
	for _, required := range requirements {
		var found *cluster.ResourceCapability
		for index := range capabilities.Resources {
			capability := &capabilities.Resources[index]
			if capability.GroupVersion == required.groupVersion &&
				capability.Resource == required.resource {
				found = capability
				break
			}
		}
		if found == nil {
			return cluster.NewError(
				cluster.ErrorCapabilityUnavailable,
				fmt.Sprintf(
					"stable DRA requires %s/%s; alpha and beta APIs are not accepted",
					required.groupVersion,
					required.resource,
				),
				false,
			)
		}
		if found.Namespaced != required.namespaced {
			return cluster.NewError(
				cluster.ErrorCapabilityUnavailable,
				fmt.Sprintf(
					"stable DRA resource %s/%s has an incompatible scope",
					required.groupVersion,
					required.resource,
				),
				false,
			)
		}
		for _, verb := range required.verbs {
			present := false
			for _, actual := range found.Verbs {
				if actual == verb {
					present = true
					break
				}
			}
			if !present {
				return cluster.NewError(
					cluster.ErrorCapabilityUnavailable,
					fmt.Sprintf(
						"stable DRA resource %s/%s lacks required verb %s",
						required.groupVersion,
						required.resource,
						verb,
					),
					false,
				)
			}
		}
	}
	return nil
}
