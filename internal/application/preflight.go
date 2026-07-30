// Package application contains the deep Scenario Runtime workflow. This file
// establishes its ordered zero-write apply preflight before the lifecycle
// entry points are connected to CLI delivery.
package application

import (
	"context"
	"fmt"

	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/controlplane"
)

const serverDryRunWarning = "server dry-run does not reserve cluster state; apply repeats all conflict checks"

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
	if request.Selection.KubeconfigPath == "" ||
		request.Selection.ContextName == "" {
		return PreflightApplyResult{}, cluster.NewError(
			cluster.ErrorInvalidIntent,
			"apply requires one explicit kubeconfig and matching context",
			false,
		)
	}
	if len(request.RequiredAccess) == 0 {
		return PreflightApplyResult{}, cluster.NewError(
			cluster.ErrorInvalidIntent,
			"apply preflight requires exact authorization operations",
			false,
		)
	}
	for _, requirement := range request.RequiredAccess {
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
	result.Authorization, err = connection.Cluster.Authorize(
		ctx,
		request.RequiredAccess,
	)
	if err != nil {
		return PreflightApplyResult{}, err
	}
	if len(result.Authorization.Decisions) != len(request.RequiredAccess) {
		return PreflightApplyResult{}, cluster.NewError(
			cluster.ErrorCapabilityUnavailable,
			"authorization response did not cover every exact operation",
			false,
		)
	}
	for index, decision := range result.Authorization.Decisions {
		if decision.Requirement != request.RequiredAccess[index] {
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
	result.Proposed, err = connection.ControlPlane.Submit(ctx, dryRunCommand)
	if err != nil {
		return PreflightApplyResult{}, err
	}
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
