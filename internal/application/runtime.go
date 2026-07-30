package application

import (
	"context"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/LinkMaq/kube-accelerator-sim/internal/catalog"
	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/controlplane"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
	"github.com/LinkMaq/kube-accelerator-sim/internal/scenario"
)

type DryRunMode string

const (
	DryRunNone   DryRunMode = ""
	DryRunClient DryRunMode = "client"
	DryRunServer DryRunMode = "server"
)

// RuntimeOptions fixes target loading and watch reconnect behavior for one
// process. Connector configuration remains immutable after construction.
type RuntimeOptions struct {
	Connect        ConnectorFunc
	Catalog        catalog.Snapshot
	ReconnectDelay time.Duration
}

// ScenarioRuntime is the local deep lifecycle Module. Its only lifecycle
// Interface is Apply, Observe, and Delete.
type ScenarioRuntime struct {
	connect        ConnectorFunc
	catalog        catalog.Snapshot
	reconnectDelay time.Duration
}

type ApplyRequest struct {
	Selection     cluster.TargetSelection
	Intent        controlplane.RevisionIntent
	TypedRevision *TypedRevisionRequest
	Mode          DryRunMode
	Async         bool
	Timeout       time.Duration
}

type TypedRevisionRequest struct {
	Name               domain.Name
	InstanceUID        domain.InstanceUID
	ExpectedGeneration domain.Generation
	Change             scenario.TypedRevisionChange
}

type DeleteRequest struct {
	Selection          cluster.TargetSelection
	Name               domain.Name
	InstanceUID        domain.InstanceUID
	ExpectedGeneration domain.Generation
	Async              bool
	Timeout            time.Duration
}

type ObserveRequest struct {
	Selection cluster.TargetSelection
	Name      domain.Name
	Watch     bool
	Timeout   time.Duration
}

// LifecycleResult preserves target and revision identity whether work was
// proposed, accepted, completed, or returned as a no-op.
type LifecycleResult struct {
	Connection cluster.ConnectionReceipt
	Receipt    domain.RevisionReceipt
	Snapshot   *Snapshot
	Warning    string
}

// Snapshot is the bounded transport-independent status returned by the
// Scenario Runtime. Detail fields are added by the observation/wait slices.
type Snapshot struct {
	InstanceName         domain.Name
	InstanceUID          domain.InstanceUID
	TargetFingerprint    domain.Digest
	DesiredGeneration    domain.Generation
	ObservedGeneration   domain.Generation
	RevisionDigest       domain.Digest
	Profiles             []ProfileSnapshot
	Phase                string
	Pools                []PoolSnapshot
	PoolsTruncated       bool
	Inventory            []InventorySnapshot
	InventoryTruncated   bool
	Fidelity             []FidelitySurfaceSnapshot
	FidelityTruncated    bool
	Diagnostics          []DiagnosticSnapshot
	DiagnosticsTruncated bool
	Conditions           []ConditionSnapshot
	ConditionsTruncated  bool
}

type ProfileSnapshot struct {
	ID       string
	Revision string
	Digest   domain.Digest
	Class    string
}

type PoolSnapshot struct {
	Group            string
	Pool             string
	RequestedTotal   int64
	RequestedHealthy int64
	ObservedTotal    int64
	ObservedHealthy  int64
}

type InventorySnapshot struct {
	APIVersion string
	Kind       string
	Count      int32
}

type FidelitySurfaceSnapshot struct {
	Surface string
	State   string
}

type DiagnosticSnapshot struct {
	Code             string
	Message          string
	Retryable        bool
	RevisionAccepted bool
	ExitCategory     int32
}

type ConditionSnapshot struct {
	Type               string
	Status             string
	Reason             string
	Message            string
	ObservedGeneration int64
	LastTransitionTime time.Time
}

// RuntimeError preserves the accepted receipt and latest Snapshot alongside
// one stable automation diagnostic.
type RuntimeError struct {
	diagnostic domain.Diagnostic
	result     LifecycleResult
}

func (runtimeError *RuntimeError) Error() string {
	return runtimeError.diagnostic.Message()
}

func (runtimeError *RuntimeError) Diagnostic() domain.Diagnostic {
	return runtimeError.diagnostic
}

func (runtimeError *RuntimeError) Result() LifecycleResult {
	return runtimeError.result
}

func NewScenarioRuntime(options RuntimeOptions) (*ScenarioRuntime, error) {
	if options.Connect == nil {
		return nil, fmt.Errorf("Scenario Runtime requires an explicit target connector")
	}
	if options.ReconnectDelay <= 0 {
		options.ReconnectDelay = 100 * time.Millisecond
	}
	return &ScenarioRuntime{
		connect:        options.Connect,
		catalog:        options.Catalog,
		reconnectDelay: options.ReconnectDelay,
	}, nil
}

func (runtime *ScenarioRuntime) Apply(
	ctx context.Context,
	request ApplyRequest,
) (LifecycleResult, error) {
	if request.TypedRevision != nil {
		if request.Intent.Name.String() != "" {
			return LifecycleResult{}, fmt.Errorf(
				"Apply accepts either a compiled intent or one typed revision",
			)
		}
		return runtime.applyTyped(ctx, request)
	}
	switch request.Mode {
	case DryRunNone, DryRunServer:
	case DryRunClient:
		if err := controlplane.ValidateRevisionIntent(request.Intent); err != nil {
			return LifecycleResult{}, err
		}
		return LifecycleResult{}, fmt.Errorf(
			"client dry-run completes at the offline Scenario compiler",
		)
	default:
		return LifecycleResult{}, fmt.Errorf(
			"unsupported dry-run mode %q",
			request.Mode,
		)
	}

	var connected ConnectedTarget
	preflight, err := PreflightApply(
		ctx,
		PreflightApplyRequest{
			Selection:      request.Selection,
			Intent:         request.Intent,
			RequiredAccess: applyAccessRequirements(request.Intent),
		},
		func(
			ctx context.Context,
			selection cluster.TargetSelection,
		) (ConnectedTarget, error) {
			var connectErr error
			connected, connectErr = runtime.connect(ctx, selection)
			return connected, connectErr
		},
	)
	if err != nil {
		return LifecycleResult{}, err
	}
	if request.Mode == DryRunServer {
		receipt, err := revisionReceipt(
			connected,
			preflight.Intent,
			preflight.Proposed,
			zeroGeneration(),
		)
		if err != nil {
			return LifecycleResult{}, err
		}
		return LifecycleResult{
			Connection: preflight.Connection,
			Receipt:    receipt,
			Warning:    preflight.Warning,
		}, nil
	}

	submission, err := connected.ControlPlane.Submit(
		ctx,
		preflight.Intent.Bind(connected.Target),
	)
	if err != nil {
		return LifecycleResult{}, err
	}
	return runtime.finishApply(
		ctx,
		connected,
		preflight.Intent,
		submission,
		request.Async,
		request.Timeout,
	)
}

func (runtime *ScenarioRuntime) applyTyped(
	ctx context.Context,
	request ApplyRequest,
) (LifecycleResult, error) {
	typed := request.TypedRevision
	if typed.Name.String() == "" ||
		typed.InstanceUID.String() == "" ||
		typed.ExpectedGeneration.Value() == 0 ||
		typed.Change == nil {
		return LifecycleResult{}, controlplane.NewError(
			controlplane.ErrorInvalidCommand,
			"typed revision requires exact name, instance UID, expected generation, and change",
			"",
		)
	}
	switch request.Mode {
	case DryRunNone, DryRunServer:
	case DryRunClient:
		return LifecycleResult{}, controlplane.NewError(
			controlplane.ErrorInvalidCommand,
			"typed revisions require current target state and do not support client dry-run",
			"",
		)
	default:
		return LifecycleResult{}, controlplane.NewError(
			controlplane.ErrorInvalidCommand,
			fmt.Sprintf("unsupported dry-run mode %q", request.Mode),
			"",
		)
	}
	if runtime.catalog.Digest().String() == "" {
		return LifecycleResult{}, controlplane.NewError(
			controlplane.ErrorInvalidCommand,
			"typed revisions require one immutable profile catalog",
			"",
		)
	}

	connected, err := runtime.connectAndPreflight(
		ctx,
		request.Selection,
		[]cluster.AccessRequirement{
			{
				Verb:     "get",
				Group:    "simulation.kasim.io",
				Resource: "scenarioinstances",
				Name:     typed.Name.String(),
			},
			{
				Verb:     "update",
				Group:    "simulation.kasim.io",
				Resource: "scenarioinstances",
				Name:     typed.Name.String(),
			},
		},
	)
	if err != nil {
		return LifecycleResult{}, err
	}
	key := controlplane.InstanceKey{
		TargetFingerprint: connected.Target.Fingerprint,
		Name:              typed.Name,
	}
	current, err := connected.ControlPlane.Read(ctx, key)
	if err != nil {
		return LifecycleResult{}, err
	}
	if current.InstanceUID != typed.InstanceUID {
		return LifecycleResult{}, controlplane.NewError(
			controlplane.ErrorUIDConflict,
			"Scenario Instance UID precondition failed",
			"",
		)
	}
	if current.DesiredGeneration != typed.ExpectedGeneration {
		return LifecycleResult{}, controlplane.NewError(
			controlplane.ErrorGenerationConflict,
			"Scenario Instance generation precondition failed",
			"",
		)
	}
	currentInput, err := scenario.Document(current.Revision.CanonicalScenario)
	if err != nil {
		return LifecycleResult{}, controlplane.NewError(
			controlplane.ErrorInvalidCommand,
			fmt.Sprintf("stored canonical Scenario is invalid: %v", err),
			"",
		)
	}
	currentCanonical, _, err := scenario.Compile(currentInput, runtime.catalog)
	if err != nil {
		return LifecycleResult{}, controlplane.NewError(
			controlplane.ErrorInvalidCommand,
			fmt.Sprintf("stored canonical Scenario no longer compiles: %v", err),
			"",
		)
	}
	if currentCanonical.Digest() != current.Revision.Digest {
		return LifecycleResult{}, controlplane.NewError(
			controlplane.ErrorResourceVersionConflict,
			"stored canonical Scenario digest does not match its accepted revision",
			"",
		)
	}
	if currentCanonical.Scenario().Name() != current.Name {
		return LifecycleResult{}, controlplane.NewError(
			controlplane.ErrorResourceVersionConflict,
			"stored canonical Scenario name does not match its instance",
			"",
		)
	}
	if currentCanonical.Scenario().Fidelity() != current.Fidelity {
		return LifecycleResult{}, controlplane.NewError(
			controlplane.ErrorFidelityConflict,
			"stored canonical Scenario fidelity does not match its instance",
			"",
		)
	}
	if current.DesiredGeneration.Value() >= math.MaxInt64 {
		return LifecycleResult{}, controlplane.NewError(
			controlplane.ErrorGenerationConflict,
			"Scenario Instance generation cannot advance",
			"",
		)
	}
	nextGeneration, err := domain.NewGeneration(
		int64(current.DesiredGeneration.Value()) + 1,
	)
	if err != nil {
		return LifecycleResult{}, err
	}
	revised, compileReceipt, err := scenario.Revise(
		currentCanonical,
		typed.Change,
		runtime.catalog,
	)
	if err != nil {
		return LifecycleResult{}, controlplane.NewError(
			controlplane.ErrorInvalidCommand,
			err.Error(),
			"",
		)
	}
	profiles, err := profileReceipts(revised, compileReceipt)
	if err != nil {
		return LifecycleResult{}, err
	}
	intent := controlplane.RevisionIntent{
		Name:             current.Name,
		CreationIdentity: current.CreationIdentity,
		Fidelity:         current.Fidelity,
		Preconditions: controlplane.Preconditions{
			InstanceUID:        current.InstanceUID,
			ExpectedGeneration: current.DesiredGeneration,
			ResourceVersion:    current.ResourceVersion,
		},
		Revision: controlplane.ScenarioRevision{
			Generation:        nextGeneration,
			Digest:            revised.Digest(),
			CanonicalScenario: revised.Bytes(),
			Profiles:          profiles,
		},
	}
	if err := controlplane.ValidateRevisionIntent(intent); err != nil {
		return LifecycleResult{}, err
	}
	scope, err := cluster.NewInstanceOwnershipScope(
		current.Name,
		current.InstanceUID,
		current.DesiredGeneration,
	)
	if err != nil {
		return LifecycleResult{}, err
	}
	scope, err = scope.ForFidelity(current.Fidelity)
	if err != nil {
		return LifecycleResult{}, err
	}
	if _, err := connected.Cluster.Observe(ctx, scope); err != nil {
		return LifecycleResult{}, err
	}

	dryRunCommand := intent.Bind(connected.Target)
	dryRunCommand.ServerDryRun = true
	proposed, err := connected.ControlPlane.Submit(ctx, dryRunCommand)
	if err != nil {
		return LifecycleResult{}, err
	}
	if !proposed.DryRun || proposed.Accepted {
		return LifecycleResult{}, cluster.NewError(
			cluster.ErrorAdmissionRejected,
			"Scenario Control Plane did not preserve server dry-run semantics",
			false,
		)
	}
	if request.Mode == DryRunServer {
		receipt, err := revisionReceipt(
			connected,
			intent,
			proposed,
			current.ObservedGeneration,
		)
		if err != nil {
			return LifecycleResult{}, err
		}
		return LifecycleResult{
			Connection: connected.Receipt,
			Receipt:    receipt,
			Warning:    serverDryRunWarning,
		}, nil
	}

	submission, err := connected.ControlPlane.Submit(
		ctx,
		intent.Bind(connected.Target),
	)
	if err != nil {
		return LifecycleResult{}, err
	}
	return runtime.finishApply(
		ctx,
		connected,
		intent,
		submission,
		request.Async,
		request.Timeout,
	)
}

func (runtime *ScenarioRuntime) finishApply(
	ctx context.Context,
	connected ConnectedTarget,
	intent controlplane.RevisionIntent,
	submission controlplane.SubmissionReceipt,
	async bool,
	timeout time.Duration,
) (LifecycleResult, error) {
	observed := zeroGeneration()
	var snapshot *Snapshot
	if submission.NoOp {
		record, err := connected.ControlPlane.Read(ctx, controlplane.InstanceKey{
			TargetFingerprint: connected.Target.Fingerprint,
			Name:              intent.Name,
		})
		if err != nil {
			return LifecycleResult{}, err
		}
		observed = record.ObservedGeneration
		translated := snapshotOf(record)
		snapshot = &translated
	}
	receipt, err := revisionReceipt(
		connected,
		intent,
		submission,
		observed,
	)
	if err != nil {
		return LifecycleResult{}, err
	}
	result := LifecycleResult{
		Connection: connected.Receipt,
		Receipt:    receipt,
		Snapshot:   snapshot,
	}
	if async || submission.NoOp {
		return result, nil
	}
	record, waitErr := runtime.waitForReady(
		ctx,
		connected.ControlPlane,
		controlplane.InstanceKey{
			TargetFingerprint: connected.Target.Fingerprint,
			Name:              intent.Name,
		},
		submission.DesiredGeneration,
		timeout,
		false,
	)
	if record.InstanceUID.String() != "" {
		translated := snapshotOf(record)
		result.Snapshot = &translated
		result.Receipt, err = revisionReceipt(
			connected,
			intent,
			submission,
			record.ObservedGeneration,
		)
		if err != nil {
			return LifecycleResult{}, err
		}
	}
	if waitErr != nil {
		return result, acceptedRuntimeError(result, waitErr)
	}
	return result, nil
}

func profileReceipts(
	compiled scenario.CanonicalScenario,
	receipt scenario.CompileReceipt,
) ([]controlplane.ProfileReceipt, error) {
	resolutions := receipt.Resolutions()
	expectedResolutions := 0
	for _, group := range compiled.Scenario().NodeGroups() {
		expectedResolutions += len(group.Pools())
	}
	if len(resolutions) != expectedResolutions {
		return nil, controlplane.NewError(
			controlplane.ErrorInvalidCommand,
			"compiler receipt does not cover every Accelerator Pool",
			"",
		)
	}
	profiles := make([]controlplane.ProfileReceipt, 0, len(resolutions))
	seen := make(map[string]struct{}, len(resolutions))
	resolutionIndex := 0
	for _, group := range compiled.Scenario().NodeGroups() {
		for _, pool := range group.Pools() {
			resolution := resolutions[resolutionIndex]
			resolutionIndex++
			profileID := pool.Profile().ID().String()
			if _, found := seen[profileID]; found {
				continue
			}
			seen[profileID] = struct{}{}
			profiles = append(profiles, controlplane.ProfileReceipt{
				ID:       profileID,
				Revision: pool.Profile().Revision(),
				Digest:   resolution.ProfileDigest(),
				Class:    resolution.ProfileClass(),
			})
		}
	}
	return profiles, nil
}

func (runtime *ScenarioRuntime) Delete(
	ctx context.Context,
	request DeleteRequest,
) (LifecycleResult, error) {
	if request.Name.String() == "" ||
		request.InstanceUID.String() == "" ||
		request.ExpectedGeneration.Value() == 0 {
		return LifecycleResult{}, controlplane.NewError(
			controlplane.ErrorInvalidCommand,
			"delete requires exact name, instance UID, and expected generation",
			"",
		)
	}
	connected, err := runtime.connectAndPreflight(
		ctx,
		request.Selection,
		[]cluster.AccessRequirement{
			{
				Verb:     "get",
				Group:    "simulation.kasim.io",
				Resource: "scenarioinstances",
				Name:     request.Name.String(),
			},
			{
				Verb:     "delete",
				Group:    "simulation.kasim.io",
				Resource: "scenarioinstances",
				Name:     request.Name.String(),
			},
		},
	)
	if err != nil {
		return LifecycleResult{}, err
	}
	key := controlplane.InstanceKey{
		TargetFingerprint: connected.Target.Fingerprint,
		Name:              request.Name,
	}
	current, err := connected.ControlPlane.Read(ctx, key)
	if err != nil {
		return LifecycleResult{}, err
	}
	if current.InstanceUID != request.InstanceUID {
		return LifecycleResult{}, controlplane.NewError(
			controlplane.ErrorUIDConflict,
			"Scenario Instance UID precondition failed",
			"",
		)
	}
	if current.DesiredGeneration != request.ExpectedGeneration {
		return LifecycleResult{}, controlplane.NewError(
			controlplane.ErrorGenerationConflict,
			"Scenario Instance generation precondition failed",
			"",
		)
	}
	deletion, err := connected.ControlPlane.Delete(
		ctx,
		controlplane.DeletionCommand{
			Target: connected.Target,
			Name:   request.Name,
			Preconditions: controlplane.DeletionPreconditions{
				InstanceUID:        request.InstanceUID,
				ExpectedGeneration: request.ExpectedGeneration,
			},
		},
	)
	if err != nil {
		return LifecycleResult{}, err
	}
	receipt, err := deletionRevisionReceipt(connected, current, deletion)
	if err != nil {
		return LifecycleResult{}, err
	}
	snapshot := snapshotOf(current)
	result := LifecycleResult{
		Connection: connected.Receipt,
		Receipt:    receipt,
		Snapshot:   &snapshot,
	}
	if request.Async {
		return result, nil
	}
	latest, waitErr := runtime.waitForDeletion(
		ctx,
		connected.ControlPlane,
		key,
		current,
		request.Timeout,
	)
	if latest.InstanceUID.String() != "" {
		translated := snapshotOf(latest)
		result.Snapshot = &translated
		result.Receipt, err = deletionRevisionReceipt(
			connected,
			latest,
			deletion,
		)
		if err != nil {
			return LifecycleResult{}, err
		}
	}
	if waitErr != nil {
		return result, acceptedRuntimeError(result, waitErr)
	}
	return result, nil
}

func (runtime *ScenarioRuntime) Observe(
	ctx context.Context,
	request ObserveRequest,
) (LifecycleResult, error) {
	if request.Name.String() == "" {
		return LifecycleResult{}, controlplane.NewError(
			controlplane.ErrorInvalidCommand,
			"status requires one exact Scenario Instance name",
			"",
		)
	}
	requirements := []cluster.AccessRequirement{{
		Verb:     "get",
		Group:    "simulation.kasim.io",
		Resource: "scenarioinstances",
		Name:     request.Name.String(),
	}}
	if request.Watch {
		requirements = append(requirements,
			cluster.AccessRequirement{
				Verb:     "list",
				Group:    "simulation.kasim.io",
				Resource: "scenarioinstances",
			},
			cluster.AccessRequirement{
				Verb:     "watch",
				Group:    "simulation.kasim.io",
				Resource: "scenarioinstances",
			},
		)
	}
	connected, err := runtime.connectAndPreflight(
		ctx,
		request.Selection,
		requirements,
	)
	if err != nil {
		return LifecycleResult{}, err
	}
	key := controlplane.InstanceKey{
		TargetFingerprint: connected.Target.Fingerprint,
		Name:              request.Name,
	}
	record, err := connected.ControlPlane.Read(ctx, key)
	if err != nil {
		return LifecycleResult{}, err
	}
	receipt, err := recordRevisionReceipt(connected, record)
	if err != nil {
		return LifecycleResult{}, err
	}
	snapshot := snapshotOf(record)
	result := LifecycleResult{
		Connection: connected.Receipt,
		Receipt:    receipt,
		Snapshot:   &snapshot,
	}
	if !request.Watch {
		return result, nil
	}
	latest, waitErr := runtime.waitForReady(
		ctx,
		connected.ControlPlane,
		key,
		record.DesiredGeneration,
		request.Timeout,
		true,
	)
	if latest.InstanceUID.String() != "" {
		translated := snapshotOf(latest)
		result.Snapshot = &translated
		result.Receipt, err = recordRevisionReceipt(connected, latest)
		if err != nil {
			return LifecycleResult{}, err
		}
	}
	if waitErr != nil {
		return result, acceptedRuntimeError(result, waitErr)
	}
	return result, nil
}

func (runtime *ScenarioRuntime) connectAndPreflight(
	ctx context.Context,
	selection cluster.TargetSelection,
	requirements []cluster.AccessRequirement,
) (ConnectedTarget, error) {
	if selection.KubeconfigPath == "" || selection.ContextName == "" {
		return ConnectedTarget{}, cluster.NewError(
			cluster.ErrorInvalidIntent,
			"operation requires one explicit kubeconfig and matching context",
			false,
		)
	}
	connected, err := runtime.connect(ctx, selection)
	if err != nil {
		return ConnectedTarget{}, err
	}
	if connected.ControlPlane == nil ||
		connected.Cluster == nil ||
		connected.Target.ContextName != selection.ContextName ||
		connected.Target.Fingerprint.String() == "" ||
		connected.Receipt.ContextName != connected.Target.ContextName ||
		connected.Receipt.TargetFingerprint != connected.Target.Fingerprint ||
		connected.Receipt.CanonicalKubeconfigPath == "" ||
		connected.Receipt.APIServerURL == "" ||
		connected.Receipt.CADigest.String() == "" {
		return ConnectedTarget{}, cluster.NewError(
			cluster.ErrorTargetUnavailable,
			"explicit target connection is incomplete",
			false,
		)
	}
	probe, err := connected.ControlPlane.Probe(ctx, connected.Target)
	if err != nil {
		return ConnectedTarget{}, err
	}
	if probe.TargetFingerprint != connected.Target.Fingerprint {
		return ConnectedTarget{}, controlplane.NewError(
			controlplane.ErrorTargetMismatch,
			"Scenario Control Plane probe returned another target",
			"",
		)
	}
	if _, err := connected.Cluster.Discover(ctx); err != nil {
		return ConnectedTarget{}, err
	}
	report, err := connected.Cluster.Authorize(ctx, requirements)
	if err != nil {
		return ConnectedTarget{}, err
	}
	if len(report.Decisions) != len(requirements) {
		return ConnectedTarget{}, cluster.NewError(
			cluster.ErrorCapabilityUnavailable,
			"authorization response did not cover every exact operation",
			false,
		)
	}
	for index, decision := range report.Decisions {
		if decision.Requirement != requirements[index] || !decision.Allowed {
			return ConnectedTarget{}, cluster.NewError(
				cluster.ErrorAuthorizationDenied,
				fmt.Sprintf(
					"authorization denied %s on %s/%s",
					requirements[index].Verb,
					requirements[index].Group,
					requirements[index].Resource,
				),
				false,
			)
		}
	}
	return connected, nil
}

func deletionRevisionReceipt(
	connected ConnectedTarget,
	record controlplane.InstanceRecord,
	deletion controlplane.DeletionReceipt,
) (domain.RevisionReceipt, error) {
	profileDigests := make([]domain.Digest, 0, len(record.Revision.Profiles))
	for _, profile := range record.Revision.Profiles {
		profileDigests = append(profileDigests, profile.Digest)
	}
	return domain.NewRevisionReceipt(domain.RevisionReceiptInput{
		ContextName:        connected.Target.ContextName,
		TargetFingerprint:  connected.Target.Fingerprint,
		InstanceName:       record.Name,
		InstanceUID:        deletion.InstanceUID,
		DesiredGeneration:  deletion.DesiredGeneration,
		ObservedGeneration: record.ObservedGeneration,
		RevisionDigest:     record.Revision.Digest,
		ProfileDigests:     profileDigests,
		RevisionAccepted:   deletion.Accepted,
		NoOp:               deletion.NoOp,
	})
}

func recordRevisionReceipt(
	connected ConnectedTarget,
	record controlplane.InstanceRecord,
) (domain.RevisionReceipt, error) {
	profileDigests := make([]domain.Digest, 0, len(record.Revision.Profiles))
	for _, profile := range record.Revision.Profiles {
		profileDigests = append(profileDigests, profile.Digest)
	}
	return domain.NewRevisionReceipt(domain.RevisionReceiptInput{
		ContextName:        connected.Target.ContextName,
		TargetFingerprint:  connected.Target.Fingerprint,
		InstanceName:       record.Name,
		InstanceUID:        record.InstanceUID,
		DesiredGeneration:  record.DesiredGeneration,
		ObservedGeneration: record.ObservedGeneration,
		RevisionDigest:     record.Revision.Digest,
		ProfileDigests:     profileDigests,
		RevisionAccepted:   true,
	})
}

const watchBatchLimit = 64

type waitFailure struct {
	code      string
	message   string
	retryable bool
}

func (failure *waitFailure) Error() string {
	return failure.message
}

func (runtime *ScenarioRuntime) waitForReady(
	ctx context.Context,
	controlPlane controlplane.ScenarioControlPlane,
	key controlplane.InstanceKey,
	desiredGeneration domain.Generation,
	timeout time.Duration,
	followLatest bool,
) (controlplane.InstanceRecord, error) {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	latest, err := controlPlane.Read(waitCtx, key)
	if err != nil {
		return controlplane.InstanceRecord{}, &waitFailure{
			code:      "ConvergenceFailed",
			message:   "read accepted Scenario Instance status failed",
			retryable: true,
		}
	}
	if followLatest {
		desiredGeneration = latest.DesiredGeneration
	}
	if failure := readyState(latest, desiredGeneration); failure != nil ||
		isReady(latest, desiredGeneration) {
		return latest, failure
	}
	cursor := latest.ResourceVersion

	for {
		stream, err := controlPlane.Watch(waitCtx, controlplane.WatchCursor{
			Key:                  key,
			AfterResourceVersion: cursor,
			Limit:                watchBatchLimit,
		})
		if err != nil {
			if controlplane.ErrorCodeOf(err) == controlplane.ErrorCursorExpired {
				latest, err = controlPlane.Read(waitCtx, key)
				if err == nil {
					cursor = latest.ResourceVersion
					if followLatest {
						desiredGeneration = latest.DesiredGeneration
					}
					if failure := readyState(latest, desiredGeneration); failure != nil ||
						isReady(latest, desiredGeneration) {
						return latest, failure
					}
					continue
				}
			}
			if waitCtx.Err() != nil {
				return latest, convergenceTimeout()
			}
			if err := runtime.waitBeforeReconnect(waitCtx); err != nil {
				return latest, convergenceTimeout()
			}
			continue
		}

		for {
			event, nextErr := stream.Next(waitCtx)
			if nextErr == nil {
				latest = event.Record
				cursor = event.Cursor
				if followLatest {
					desiredGeneration = latest.DesiredGeneration
				}
				if failure := readyState(
					latest,
					desiredGeneration,
				); failure != nil || isReady(latest, desiredGeneration) {
					_ = stream.Close()
					return latest, failure
				}
				continue
			}
			_ = stream.Close()
			if waitCtx.Err() != nil {
				if current, readErr := readLatestAfterWait(
					ctx,
					controlPlane,
					key,
				); readErr == nil {
					latest = current
				}
				return latest, convergenceTimeout()
			}
			if nextErr != io.EOF &&
				controlplane.ErrorCodeOf(nextErr) ==
					controlplane.ErrorCursorExpired {
				if resume := controlplane.ResumeCursorOf(nextErr); resume != "" {
					cursor = resume
				}
			}
			if err := runtime.waitBeforeReconnect(waitCtx); err != nil {
				return latest, convergenceTimeout()
			}
			break
		}
	}
}

func (runtime *ScenarioRuntime) waitForDeletion(
	ctx context.Context,
	controlPlane controlplane.ScenarioControlPlane,
	key controlplane.InstanceKey,
	latest controlplane.InstanceRecord,
	timeout time.Duration,
) (controlplane.InstanceRecord, error) {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cursor := latest.ResourceVersion

	for {
		stream, err := controlPlane.Watch(waitCtx, controlplane.WatchCursor{
			Key:                  key,
			AfterResourceVersion: cursor,
			Limit:                watchBatchLimit,
		})
		if err != nil {
			current, readErr := controlPlane.Read(waitCtx, key)
			if controlplane.ErrorCodeOf(readErr) == controlplane.ErrorNotFound {
				return latest, nil
			}
			if readErr == nil {
				latest = current
				cursor = current.ResourceVersion
				if failure := deletionState(current); failure != nil {
					return latest, failure
				}
			}
			if waitCtx.Err() != nil {
				return latest, deletionTimeout()
			}
			if err := runtime.waitBeforeReconnect(waitCtx); err != nil {
				return latest, deletionTimeout()
			}
			continue
		}

		for {
			event, nextErr := stream.Next(waitCtx)
			if nextErr == nil {
				latest = event.Record
				cursor = event.Cursor
				if failure := deletionState(latest); failure != nil {
					_ = stream.Close()
					return latest, failure
				}
				continue
			}
			_ = stream.Close()
			current, readErr := readLatestAfterWait(
				ctx,
				controlPlane,
				key,
			)
			if controlplane.ErrorCodeOf(readErr) == controlplane.ErrorNotFound {
				return latest, nil
			}
			if readErr == nil {
				latest = current
				cursor = current.ResourceVersion
				if failure := deletionState(current); failure != nil {
					return latest, failure
				}
			}
			if waitCtx.Err() != nil {
				return latest, deletionTimeout()
			}
			if err := runtime.waitBeforeReconnect(waitCtx); err != nil {
				return latest, deletionTimeout()
			}
			break
		}
	}
}

func readLatestAfterWait(
	ctx context.Context,
	controlPlane controlplane.ScenarioControlPlane,
	key controlplane.InstanceKey,
) (controlplane.InstanceRecord, error) {
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	return controlPlane.Read(readCtx, key)
}

func deletionState(record controlplane.InstanceRecord) error {
	if failure := readyState(record, record.DesiredGeneration); failure != nil {
		return failure
	}
	return nil
}

func (runtime *ScenarioRuntime) waitBeforeReconnect(ctx context.Context) error {
	timer := time.NewTimer(runtime.reconnectDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isReady(
	record controlplane.InstanceRecord,
	desiredGeneration domain.Generation,
) bool {
	return record.Status.Phase == "Ready" &&
		record.ObservedGeneration == desiredGeneration
}

func readyState(
	record controlplane.InstanceRecord,
	desiredGeneration domain.Generation,
) error {
	if record.Status.Phase == "Failed" {
		code := "ConvergenceFailed"
		message := "accepted revision failed to converge"
		retryable := false
		if len(record.Status.Diagnostics) != 0 {
			diagnostic := record.Status.Diagnostics[0]
			code = diagnostic.Code
			message = diagnostic.Message
			retryable = diagnostic.Retryable
		}
		return &waitFailure{
			code:      code,
			message:   message,
			retryable: retryable,
		}
	}
	for _, condition := range record.Status.Conditions {
		if condition.Type == "CleanupBlocked" && condition.Status == "True" {
			return &waitFailure{
				code:      "CleanupBlocked",
				message:   condition.Message,
				retryable: true,
			}
		}
	}
	if record.ObservedGeneration.Value() > desiredGeneration.Value() {
		return &waitFailure{
			code:      "ConvergenceFailed",
			message:   "observed generation advanced beyond the accepted revision",
			retryable: false,
		}
	}
	return nil
}

func convergenceTimeout() error {
	return &waitFailure{
		code:      "ConvergenceTimeout",
		message:   "accepted revision did not reach its terminal condition before timeout",
		retryable: true,
	}
}

func deletionTimeout() error {
	return &waitFailure{
		code:      "ConvergenceTimeout",
		message:   "accepted deletion did not complete before timeout",
		retryable: true,
	}
}

func acceptedRuntimeError(
	result LifecycleResult,
	cause error,
) error {
	failure, ok := cause.(*waitFailure)
	if !ok {
		failure = &waitFailure{
			code:      "ConvergenceFailed",
			message:   cause.Error(),
			retryable: true,
		}
	}
	code, err := domain.ParseDiagnosticCode(failure.code)
	if err != nil {
		code, _ = domain.ParseDiagnosticCode("ConvergenceFailed")
	}
	category, _ := domain.ParseExitCategory(5)
	message := failure.message
	if len(message) > domain.MaximumDiagnosticMessageBytes {
		message = message[:domain.MaximumDiagnosticMessageBytes]
	}
	diagnostic, err := domain.NewDiagnostic(
		code,
		message,
		failure.retryable,
		true,
		category,
	)
	if err != nil {
		return cause
	}
	return &RuntimeError{diagnostic: diagnostic, result: result}
}

func applyAccessRequirements(
	intent controlplane.RevisionIntent,
) []cluster.AccessRequirement {
	mutationVerb := "create"
	if intent.Preconditions.ExpectedGeneration.Value() != 0 {
		mutationVerb = "update"
	}
	return []cluster.AccessRequirement{
		{
			Verb:     "get",
			Group:    "simulation.kasim.io",
			Resource: "scenarioinstances",
			Name:     intent.Name.String(),
		},
		{
			Verb:     mutationVerb,
			Group:    "simulation.kasim.io",
			Resource: "scenarioinstances",
			Name:     intent.Name.String(),
		},
	}
}

func revisionReceipt(
	connected ConnectedTarget,
	intent controlplane.RevisionIntent,
	submission controlplane.SubmissionReceipt,
	observed domain.Generation,
) (domain.RevisionReceipt, error) {
	profileDigests := make(
		[]domain.Digest,
		0,
		len(intent.Revision.Profiles),
	)
	for _, profile := range intent.Revision.Profiles {
		profileDigests = append(profileDigests, profile.Digest)
	}
	return domain.NewRevisionReceipt(domain.RevisionReceiptInput{
		ContextName:        connected.Target.ContextName,
		TargetFingerprint:  connected.Target.Fingerprint,
		InstanceName:       intent.Name,
		InstanceUID:        submission.InstanceUID,
		DesiredGeneration:  submission.DesiredGeneration,
		ObservedGeneration: observed,
		RevisionDigest:     submission.RevisionDigest,
		ProfileDigests:     profileDigests,
		RevisionAccepted:   submission.Accepted,
		NoOp:               submission.NoOp,
	})
}

func snapshotOf(record controlplane.InstanceRecord) Snapshot {
	revisionDigest := record.Status.RevisionDigest
	if revisionDigest.String() == "" {
		revisionDigest = record.Revision.Digest
	}
	phase := record.Status.Phase
	if phase == "" {
		phase = "Pending"
	}
	profiles := make([]ProfileSnapshot, 0, len(record.Revision.Profiles))
	for _, profile := range record.Revision.Profiles {
		profiles = append(profiles, ProfileSnapshot{
			ID:       profile.ID,
			Revision: profile.Revision,
			Digest:   profile.Digest,
			Class:    profile.Class,
		})
	}
	statusPools := record.Status.Pools
	poolsTruncated := record.Status.PoolsTruncated
	if len(statusPools) > controlplane.MaximumStatusPools {
		statusPools = statusPools[:controlplane.MaximumStatusPools]
		poolsTruncated = true
	}
	pools := make([]PoolSnapshot, 0, len(statusPools))
	for _, pool := range statusPools {
		pools = append(pools, PoolSnapshot{
			Group:            pool.Group,
			Pool:             pool.Pool,
			RequestedTotal:   pool.RequestedTotal,
			RequestedHealthy: pool.RequestedHealthy,
			ObservedTotal:    pool.ObservedTotal,
			ObservedHealthy:  pool.ObservedHealthy,
		})
	}
	statusInventory := record.Status.Inventory
	inventoryTruncated := record.Status.InventoryTruncated
	if len(statusInventory) > controlplane.MaximumStatusInventory {
		statusInventory = statusInventory[:controlplane.MaximumStatusInventory]
		inventoryTruncated = true
	}
	inventory := make([]InventorySnapshot, 0, len(statusInventory))
	for _, entry := range statusInventory {
		inventory = append(inventory, InventorySnapshot{
			APIVersion: entry.APIVersion,
			Kind:       entry.Kind,
			Count:      entry.Count,
		})
	}
	statusFidelity := record.Status.Fidelity
	fidelityTruncated := record.Status.FidelityTruncated
	if len(statusFidelity) > controlplane.MaximumStatusFidelity {
		statusFidelity = statusFidelity[:controlplane.MaximumStatusFidelity]
		fidelityTruncated = true
	}
	fidelity := make(
		[]FidelitySurfaceSnapshot,
		0,
		len(statusFidelity),
	)
	for _, surface := range statusFidelity {
		fidelity = append(fidelity, FidelitySurfaceSnapshot{
			Surface: surface.Surface,
			State:   surface.State,
		})
	}
	statusDiagnostics := record.Status.Diagnostics
	diagnosticsTruncated := record.Status.DiagnosticsTruncated
	if len(statusDiagnostics) > controlplane.MaximumStatusDiagnostics {
		statusDiagnostics = statusDiagnostics[:controlplane.MaximumStatusDiagnostics]
		diagnosticsTruncated = true
	}
	diagnostics := make(
		[]DiagnosticSnapshot,
		0,
		len(statusDiagnostics),
	)
	for _, diagnostic := range statusDiagnostics {
		diagnostics = append(diagnostics, DiagnosticSnapshot{
			Code:             diagnostic.Code,
			Message:          diagnostic.Message,
			Retryable:        diagnostic.Retryable,
			RevisionAccepted: diagnostic.RevisionAccepted,
			ExitCategory:     diagnostic.ExitCategory,
		})
	}
	statusConditions := record.Status.Conditions
	conditionsTruncated := record.Status.ConditionsTruncated
	if len(statusConditions) > controlplane.MaximumStatusConditions {
		statusConditions = statusConditions[:controlplane.MaximumStatusConditions]
		conditionsTruncated = true
	}
	conditions := make([]ConditionSnapshot, 0, len(statusConditions))
	for _, condition := range statusConditions {
		conditions = append(conditions, ConditionSnapshot{
			Type:               condition.Type,
			Status:             condition.Status,
			Reason:             condition.Reason,
			Message:            condition.Message,
			ObservedGeneration: condition.ObservedGeneration,
			LastTransitionTime: condition.LastTransitionTime,
		})
	}
	return Snapshot{
		InstanceName:         record.Name,
		InstanceUID:          record.InstanceUID,
		TargetFingerprint:    record.Target.Fingerprint,
		DesiredGeneration:    record.DesiredGeneration,
		ObservedGeneration:   record.ObservedGeneration,
		RevisionDigest:       revisionDigest,
		Profiles:             profiles,
		Phase:                phase,
		Pools:                pools,
		PoolsTruncated:       poolsTruncated,
		Inventory:            inventory,
		InventoryTruncated:   inventoryTruncated,
		Fidelity:             fidelity,
		FidelityTruncated:    fidelityTruncated,
		Diagnostics:          diagnostics,
		DiagnosticsTruncated: diagnosticsTruncated,
		Conditions:           conditions,
		ConditionsTruncated:  conditionsTruncated,
	}
}

func zeroGeneration() domain.Generation {
	generation, _ := domain.NewGeneration(0)
	return generation
}
