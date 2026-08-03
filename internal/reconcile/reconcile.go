// Package reconcile converges one accepted Scenario Instance revision through
// the three accepted behavior seams: Scenario Control Plane, Kubernetes
// Cluster, and Resource Projection.
package reconcile

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/LinkMaq/kube-accelerator-sim/internal/catalog"
	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/controlplane"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
	"github.com/LinkMaq/kube-accelerator-sim/internal/projection"
	"github.com/LinkMaq/kube-accelerator-sim/internal/runtime/kwok"
	"github.com/LinkMaq/kube-accelerator-sim/internal/scenario"
)

// FinalizationAction is the controller-runtime edge instruction for the
// product ownership finalizer. It is not a generic Kubernetes mutation.
type FinalizationAction string

const (
	FinalizationEnsure FinalizationAction = "ensure"
	FinalizationRetain FinalizationAction = "retain"
	FinalizationRemove FinalizationAction = "remove"
)

// StatusIntent is the bounded product-resource update emitted by one complete
// reconciliation call.
type StatusIntent struct {
	Key                controlplane.InstanceKey
	ResourceVersion    string
	ObservedGeneration domain.Generation
	Status             controlplane.InstanceStatus
	Finalization       FinalizationAction
}

// CommitFunc is concrete controller delivery plumbing, not an extension
// registry. The production controller writes status/finalizer state; tests
// record the same bounded intent.
type CommitFunc func(context.Context, StatusIntent) error

// Options provides the accepted seams and deterministic system boundaries.
type Options struct {
	ControlPlane controlplane.ScenarioControlPlane
	Cluster      cluster.Port
	Catalog      catalog.Snapshot
	Projection   projection.ResourceProjection
	Now          func() time.Time
	Commit       CommitFunc
}

// InstanceReconciler is the single deep lifecycle Module.
type InstanceReconciler struct {
	controlPlane controlplane.ScenarioControlPlane
	cluster      cluster.Port
	catalog      catalog.Snapshot
	projection   projection.ResourceProjection
	runtime      kwok.Runtime
	resourceKeys map[string]struct{}
	identityKeys map[string]struct{}
	now          func() time.Time
	commit       CommitFunc
}

// Result reports whether another bounded convergence pass is needed.
type Result struct {
	requeue bool
	phase   string
}

func New(options Options) (*InstanceReconciler, error) {
	if options.ControlPlane == nil ||
		options.Cluster == nil ||
		options.Projection == nil ||
		options.Now == nil ||
		options.Commit == nil {
		return nil, fmt.Errorf(
			"Instance Reconciler requires control plane, Cluster, projection, clock, and commit",
		)
	}
	if options.Catalog.Digest().String() == "" {
		return nil, fmt.Errorf("Instance Reconciler requires an immutable catalog")
	}
	return &InstanceReconciler{
		controlPlane: options.ControlPlane,
		cluster:      options.Cluster,
		catalog:      options.Catalog,
		projection:   options.Projection,
		runtime:      kwok.Pinned(),
		resourceKeys: catalogExtendedResourceNames(options.Catalog),
		identityKeys: catalogIdentityLabelKeys(options.Catalog),
		now:          options.Now,
		commit:       options.Commit,
	}, nil
}

func (result Result) Requeue() bool {
	return result.requeue
}

func (result Result) Phase() string {
	return result.phase
}

// Reconcile advances at most one ordered lifecycle stage. A later queue pass
// observes durable Kubernetes state, so retries resume without hidden memory.
func (reconciler *InstanceReconciler) Reconcile(
	ctx context.Context,
	key controlplane.InstanceKey,
) (Result, error) {
	record, err := reconciler.controlPlane.Read(ctx, key)
	if err != nil {
		return Result{}, err
	}
	if record.DeletionRequested {
		scope, err := cluster.NewInstanceOwnershipScope(
			record.Name,
			record.InstanceUID,
			record.DesiredGeneration,
		)
		if err != nil {
			return reconciler.fail(ctx, key, record, "ConvergenceFailed", err, false)
		}
		scope, err = scope.ForFidelity(record.Fidelity)
		if err != nil {
			return reconciler.fail(ctx, key, record, "ConvergenceFailed", err, false)
		}
		observed, err := reconciler.cluster.Observe(ctx, scope)
		if err != nil {
			return reconciler.fail(
				ctx,
				key,
				record,
				diagnosticCode(err),
				err,
				retryable(err),
			)
		}
		return reconciler.reconcileDeletion(
			ctx,
			key,
			record,
			projection.DesiredGraph{},
			observed,
			scope,
		)
	}
	compiled, receipt, err := reconciler.compileAndVerify(record)
	if err != nil {
		return reconciler.fail(ctx, key, record, "CatalogInvalid", err, false)
	}
	capabilities, err := reconciler.cluster.Discover(ctx)
	if err != nil {
		return reconciler.fail(ctx, key, record, diagnosticCode(err), err, retryable(err))
	}
	graph, err := projection.Build(projection.BuildInput{
		InstanceName:         record.Name,
		InstanceUID:          record.InstanceUID,
		Generation:           record.DesiredGeneration,
		Scenario:             compiled.Scenario(),
		Resolutions:          receipt.Resolutions(),
		AuxiliaryResolutions: receipt.AuxiliaryResolutions(),
	})
	if err != nil {
		return reconciler.fail(ctx, key, record, "ConvergenceFailed", err, false)
	}
	support := reconciler.projection.Support(capabilities, graph)
	if !support.Supported() {
		return reconciler.fail(
			ctx,
			key,
			record,
			"CapabilityUnavailable",
			fmt.Errorf("%s", support.Issues()[0].Message),
			false,
		)
	}
	fragment, err := reconciler.projection.Render(graph, capabilities)
	if err != nil {
		return reconciler.fail(ctx, key, record, "ConvergenceFailed", err, false)
	}
	scope, err := cluster.NewInstanceOwnershipScope(
		record.Name,
		record.InstanceUID,
		record.DesiredGeneration,
	)
	if err != nil {
		return reconciler.fail(ctx, key, record, "ConvergenceFailed", err, false)
	}
	scope, err = scope.ForFidelity(record.Fidelity)
	if err != nil {
		return reconciler.fail(ctx, key, record, "ConvergenceFailed", err, false)
	}
	observed, err := reconciler.cluster.Observe(ctx, scope)
	if err != nil {
		return reconciler.fail(ctx, key, record, diagnosticCode(err), err, retryable(err))
	}
	changes, err := reconciler.missingIdentityChanges(graph, fragment, observed)
	if err != nil {
		return reconciler.fail(ctx, key, record, "ConvergenceFailed", err, false)
	}
	if len(changes) != 0 {
		return reconciler.executeStage(
			ctx,
			key,
			record,
			graph,
			observed,
			scope,
			changes,
		)
	}
	changes, err = reconciler.metadataChanges(graph, fragment, observed)
	if err != nil {
		return reconciler.fail(ctx, key, record, "OwnershipConflict", err, false)
	}
	if len(changes) != 0 {
		return reconciler.executeStage(
			ctx,
			key,
			record,
			graph,
			observed,
			scope,
			changes,
		)
	}
	changes, err = reconciler.replacementCloseChanges(graph, fragment, observed)
	if err != nil {
		return reconciler.fail(ctx, key, record, "ConvergenceFailed", err, false)
	}
	if len(changes) != 0 {
		return reconciler.executeStage(
			ctx,
			key,
			record,
			graph,
			observed,
			scope,
			changes,
		)
	}
	changes, err = reconciler.statusChanges(graph, fragment, observed)
	if err != nil {
		return reconciler.fail(ctx, key, record, "ConvergenceFailed", err, false)
	}
	if len(changes) != 0 {
		return reconciler.executeStage(
			ctx,
			key,
			record,
			graph,
			observed,
			scope,
			changes,
		)
	}
	changes, err = reconciler.draClassChanges(graph, fragment, observed)
	if err != nil {
		return reconciler.fail(ctx, key, record, "ConvergenceFailed", err, false)
	}
	if len(changes) != 0 {
		return reconciler.executeStage(
			ctx,
			key,
			record,
			graph,
			observed,
			scope,
			changes,
		)
	}
	changes, err = reconciler.draSliceChanges(graph, fragment, observed)
	if err != nil {
		return reconciler.fail(ctx, key, record, "ConvergenceFailed", err, false)
	}
	if len(changes) != 0 {
		return reconciler.executeStage(
			ctx,
			key,
			record,
			graph,
			observed,
			scope,
			changes,
		)
	}
	if result, handled, err := reconciler.reconcileStaleInventory(
		ctx,
		key,
		record,
		graph,
		fragment,
		observed,
		scope,
	); handled || err != nil {
		return result, err
	}

	projectionObserved, err := observedProjection(fragment, observed)
	if err != nil {
		return reconciler.fail(ctx, key, record, "ConvergenceFailed", err, false)
	}
	fidelity := reconciler.projection.Assess(projectionObserved, fragment)
	if closeNodes := fidelity.MustCloseNodes(); len(closeNodes) != 0 {
		changes, err := reconciler.schedulingChanges(
			graph,
			fragment,
			observed,
			closeNodes,
			true,
		)
		if err != nil {
			return reconciler.fail(ctx, key, record, "ConvergenceFailed", err, false)
		}
		return reconciler.executeStage(
			ctx,
			key,
			record,
			graph,
			observed,
			scope,
			changes,
		)
	}
	if openNodes := fidelity.OpenNodes(); len(openNodes) != 0 {
		changes, err := reconciler.schedulingChanges(
			graph,
			fragment,
			observed,
			openNodes,
			false,
		)
		if err != nil {
			return reconciler.fail(ctx, key, record, "ConvergenceFailed", err, false)
		}
		return reconciler.executeStage(
			ctx,
			key,
			record,
			graph,
			observed,
			scope,
			changes,
		)
	}
	if fidelity.FidelitySatisfied() {
		intent := reconciler.statusIntent(
			key,
			record,
			graph,
			observed,
			"Ready",
			FinalizationEnsure,
			nil,
		)
		intent.ObservedGeneration = record.DesiredGeneration
		intent.Status.Fidelity = fidelitySurfaceStatus(fidelity)
		conditions := []controlplane.ConditionStatus{{
			Type:               "FidelitySatisfied",
			Status:             "True",
			Reason:             "FidelitySatisfied",
			Message:            "Every required scheduling surface is observed",
			ObservedGeneration: int64(record.DesiredGeneration.Value()),
			LastTransitionTime: reconciler.now().UTC(),
		}}
		if overcommitments := fidelity.Overcommitments(); len(overcommitments) != 0 {
			first := overcommitments[0]
			nodeWord := "nodes"
			if len(overcommitments) == 1 {
				nodeWord = "node"
			}
			conditions = append(conditions, controlplane.ConditionStatus{
				Type:   "Overcommitted",
				Status: "True",
				Reason: "Overcommitted",
				Message: fmt.Sprintf(
					"%s requested %d exceeds allocatable %d on %d %s",
					first.ResourceName,
					first.Requested,
					first.Allocatable,
					len(overcommitments),
					nodeWord,
				),
				ObservedGeneration: int64(record.DesiredGeneration.Value()),
				LastTransitionTime: reconciler.now().UTC(),
			})
		}
		intent.Status.Conditions = conditions
		if err := reconciler.commit(ctx, intent); err != nil {
			return Result{}, err
		}
		return Result{phase: "Ready"}, nil
	}

	intent := reconciler.statusIntent(
		key,
		record,
		graph,
		observed,
		"Reconciling",
		FinalizationEnsure,
		nil,
	)
	intent.Status.Fidelity = fidelitySurfaceStatus(fidelity)
	if err := reconciler.commit(ctx, intent); err != nil {
		return Result{}, err
	}
	return Result{requeue: true, phase: "Reconciling"}, nil
}

func fidelitySurfaceStatus(
	report projection.FidelityReport,
) []controlplane.FidelitySurfaceStatus {
	states := make(map[string]projection.SurfaceState)
	for _, assessment := range report.Assessments() {
		if assessment.Surface == "" {
			continue
		}
		current, found := states[assessment.Surface]
		if !found || surfaceStatePriority(assessment.State) >
			surfaceStatePriority(current) {
			states[assessment.Surface] = assessment.State
		}
	}
	surfaces := make([]string, 0, len(states))
	for surface := range states {
		surfaces = append(surfaces, surface)
	}
	slices.Sort(surfaces)
	if len(surfaces) > controlplane.MaximumStatusFidelity {
		surfaces = surfaces[:controlplane.MaximumStatusFidelity]
	}
	result := make(
		[]controlplane.FidelitySurfaceStatus,
		0,
		len(surfaces),
	)
	for _, surface := range surfaces {
		result = append(result, controlplane.FidelitySurfaceStatus{
			Surface: surface,
			State:   string(states[surface]),
		})
	}
	return result
}

func surfaceStatePriority(state projection.SurfaceState) int {
	switch state {
	case projection.SurfaceUnavailable:
		return 4
	case projection.SurfaceExcluded:
		return 3
	case projection.SurfaceOutOfScope:
		return 2
	case projection.SurfaceAchieved:
		return 1
	default:
		return 5
	}
}

func (reconciler *InstanceReconciler) reconcileStaleInventory(
	ctx context.Context,
	key controlplane.InstanceKey,
	record controlplane.InstanceRecord,
	graph projection.DesiredGraph,
	fragment projection.ProjectionFragment,
	observed cluster.ObservedGraph,
	scope cluster.OwnershipScope,
) (Result, bool, error) {
	desiredNames := make(map[string]struct{}, len(graph.Nodes()))
	for _, node := range graph.Nodes() {
		desiredNames[node.Name()] = struct{}{}
	}
	desiredClassNames := make(map[string]struct{}, len(fragment.DeviceClasses()))
	desiredSliceNames := make(map[string]struct{})
	for _, deviceClass := range fragment.DeviceClasses() {
		desiredClassNames[deviceClass.Name()] = struct{}{}
	}
	for _, resourceSlice := range fragment.ResourceSlices() {
		desiredSliceNames[resourceSlice.Name()] = struct{}{}
	}
	staleNodes := make([]cluster.ObservedObject, 0)
	staleLeases := make([]cluster.ObservedObject, 0)
	staleClasses := make([]cluster.ObservedObject, 0)
	staleSlices := make([]cluster.ObservedObject, 0)
	staleNodeNames := make(map[string]struct{})
	for _, object := range observed.Objects {
		if _, desired := desiredNames[object.Key.Name()]; desired {
			continue
		}
		switch object.Key.Kind() {
		case cluster.ObjectKindNode:
			staleNodes = append(staleNodes, object)
			staleNodeNames[object.Key.Name()] = struct{}{}
		case cluster.ObjectKindLease:
			staleLeases = append(staleLeases, object)
		case cluster.ObjectKindDeviceClass:
			if _, desired := desiredClassNames[object.Key.Name()]; !desired {
				staleClasses = append(staleClasses, object)
			}
		case cluster.ObjectKindResourceSlice:
			if _, desired := desiredSliceNames[object.Key.Name()]; !desired {
				staleSlices = append(staleSlices, object)
			}
		}
	}
	if len(staleNodes) == 0 &&
		len(staleLeases) == 0 &&
		len(staleClasses) == 0 &&
		len(staleSlices) == 0 {
		return Result{}, false, nil
	}

	closeChanges := make([]cluster.OwnedChange, 0, len(staleNodes))
	for _, object := range staleNodes {
		if object.Node == nil {
			result, err := reconciler.fail(
				ctx,
				key,
				record,
				"ConvergenceFailed",
				fmt.Errorf("stale owned Node %q has no observable Node state", object.Key.Name()),
				false,
			)
			return result, true, err
		}
		if object.Node.Unschedulable {
			continue
		}
		change, err := closeObservedNode(object)
		if err != nil {
			result, failure := reconciler.fail(
				ctx,
				key,
				record,
				"ConvergenceFailed",
				err,
				false,
			)
			return result, true, failure
		}
		closeChanges = append(closeChanges, change)
	}
	if len(closeChanges) != 0 {
		result, err := reconciler.executeStage(
			ctx,
			key,
			record,
			graph,
			observed,
			scope,
			closeChanges,
		)
		return result, true, err
	}

	blockers := activePodBlockers(observed.Pods, staleNodeNames)
	if len(blockers) != 0 {
		diagnostic := controlplane.DiagnosticStatus{
			Code:             "CleanupBlocked",
			Message:          boundedMessage("scale-down is blocked by bound Pods: " + blockers[0]),
			Retryable:        true,
			RevisionAccepted: true,
			ExitCategory:     5,
		}
		intent := reconciler.statusIntent(
			key,
			record,
			graph,
			observed,
			"Reconciling",
			FinalizationEnsure,
			[]controlplane.DiagnosticStatus{diagnostic},
		)
		intent.Status.Conditions = []controlplane.ConditionStatus{{
			Type:               "CleanupBlocked",
			Status:             "True",
			Reason:             "CleanupBlocked",
			Message:            diagnostic.Message,
			ObservedGeneration: int64(record.DesiredGeneration.Value()),
			LastTransitionTime: reconciler.now().UTC(),
		}}
		if err := reconciler.commit(ctx, intent); err != nil {
			return Result{}, true, err
		}
		return Result{requeue: true, phase: "Reconciling"}, true, nil
	}
	if blockers := draClaimBlockers(
		observed.ResourceClaims,
		staleClasses,
		staleSlices,
	); len(blockers) != 0 {
		diagnostic := controlplane.DiagnosticStatus{
			Code:             "CleanupBlocked",
			Message:          boundedMessage("scale-down is blocked by DRA claim: " + blockers[0]),
			Retryable:        true,
			RevisionAccepted: true,
			ExitCategory:     5,
		}
		intent := reconciler.statusIntent(
			key,
			record,
			graph,
			observed,
			"Reconciling",
			FinalizationEnsure,
			[]controlplane.DiagnosticStatus{diagnostic},
		)
		intent.Status.Conditions = []controlplane.ConditionStatus{{
			Type:               "CleanupBlocked",
			Status:             "True",
			Reason:             "CleanupBlocked",
			Message:            diagnostic.Message,
			ObservedGeneration: int64(record.DesiredGeneration.Value()),
			LastTransitionTime: reconciler.now().UTC(),
		}}
		if err := reconciler.commit(ctx, intent); err != nil {
			return Result{}, true, err
		}
		return Result{requeue: true, phase: "Reconciling"}, true, nil
	}

	staleObjects := staleSlices
	if len(staleObjects) == 0 {
		staleObjects = staleClasses
	}
	if len(staleObjects) == 0 {
		staleObjects = staleNodes
	}
	if len(staleObjects) == 0 {
		staleObjects = staleLeases
	}
	changes := deleteObjectChanges(staleObjects)
	if len(changes) == 0 {
		result, err := reconciler.fail(
			ctx,
			key,
			record,
			"ConvergenceFailed",
			fmt.Errorf("stale inventory contained no deletable allowlisted objects"),
			false,
		)
		return result, true, err
	}
	result, err := reconciler.executeStage(
		ctx,
		key,
		record,
		graph,
		observed,
		scope,
		changes,
	)
	return result, true, err
}

func (reconciler *InstanceReconciler) reconcileDeletion(
	ctx context.Context,
	key controlplane.InstanceKey,
	record controlplane.InstanceRecord,
	graph projection.DesiredGraph,
	observed cluster.ObservedGraph,
	scope cluster.OwnershipScope,
) (Result, error) {
	closeChanges := make([]cluster.OwnedChange, 0)
	ownedNodeNames := make(map[string]struct{})
	for _, object := range observed.Objects {
		if object.Key.Kind() != cluster.ObjectKindNode {
			continue
		}
		ownedNodeNames[object.Key.Name()] = struct{}{}
		if object.Node == nil {
			return reconciler.fail(
				ctx,
				key,
				record,
				"ConvergenceFailed",
				fmt.Errorf("owned Node %q has no observable Node state", object.Key.Name()),
				false,
			)
		}
		if object.Node.Unschedulable {
			continue
		}
		change, err := closeObservedNode(object)
		if err != nil {
			return reconciler.fail(ctx, key, record, "ConvergenceFailed", err, false)
		}
		closeChanges = append(closeChanges, change)
	}
	if len(closeChanges) != 0 {
		return reconciler.executeDeletionStage(
			ctx,
			key,
			record,
			graph,
			observed,
			scope,
			closeChanges,
		)
	}

	blockers := activePodBlockers(observed.Pods, ownedNodeNames)
	if len(blockers) != 0 {
		diagnostic := controlplane.DiagnosticStatus{
			Code:             "CleanupBlocked",
			Message:          boundedMessage("cleanup is blocked by bound Pods: " + blockers[0]),
			Retryable:        true,
			RevisionAccepted: true,
			ExitCategory:     5,
		}
		intent := reconciler.statusIntent(
			key,
			record,
			graph,
			observed,
			"Deleting",
			FinalizationRetain,
			[]controlplane.DiagnosticStatus{diagnostic},
		)
		intent.Status.Conditions = []controlplane.ConditionStatus{{
			Type:               "CleanupBlocked",
			Status:             "True",
			Reason:             "CleanupBlocked",
			Message:            diagnostic.Message,
			ObservedGeneration: int64(record.DesiredGeneration.Value()),
			LastTransitionTime: reconciler.now().UTC(),
		}}
		if err := reconciler.commit(ctx, intent); err != nil {
			return Result{}, err
		}
		return Result{requeue: true, phase: "Deleting"}, nil
	}
	draObjects := make([]cluster.ObservedObject, 0)
	for _, object := range observed.Objects {
		if object.Key.Kind() == cluster.ObjectKindDeviceClass ||
			object.Key.Kind() == cluster.ObjectKindResourceSlice {
			draObjects = append(draObjects, object)
		}
	}
	if blockers := draClaimBlockers(
		observed.ResourceClaims,
		draObjects,
		draObjects,
	); len(blockers) != 0 {
		diagnostic := controlplane.DiagnosticStatus{
			Code:             "CleanupBlocked",
			Message:          boundedMessage("cleanup is blocked by DRA claim: " + blockers[0]),
			Retryable:        true,
			RevisionAccepted: true,
			ExitCategory:     5,
		}
		intent := reconciler.statusIntent(
			key,
			record,
			graph,
			observed,
			"Deleting",
			FinalizationRetain,
			[]controlplane.DiagnosticStatus{diagnostic},
		)
		intent.Status.Conditions = []controlplane.ConditionStatus{{
			Type:               "CleanupBlocked",
			Status:             "True",
			Reason:             "CleanupBlocked",
			Message:            diagnostic.Message,
			ObservedGeneration: int64(record.DesiredGeneration.Value()),
			LastTransitionTime: reconciler.now().UTC(),
		}}
		if err := reconciler.commit(ctx, intent); err != nil {
			return Result{}, err
		}
		return Result{requeue: true, phase: "Deleting"}, nil
	}

	deleteChanges := deletionChanges(
		observed.Objects,
		cluster.ObjectKindResourceSlice,
	)
	if len(deleteChanges) == 0 {
		deleteChanges = deletionChanges(
			observed.Objects,
			cluster.ObjectKindDeviceClass,
		)
	}
	if len(deleteChanges) == 0 {
		deleteChanges = deletionChanges(observed.Objects, cluster.ObjectKindNode)
	}
	if len(deleteChanges) == 0 {
		deleteChanges = deletionChanges(observed.Objects, cluster.ObjectKindLease)
	}
	if len(deleteChanges) != 0 {
		return reconciler.executeDeletionStage(
			ctx,
			key,
			record,
			graph,
			observed,
			scope,
			deleteChanges,
		)
	}

	intent := reconciler.statusIntent(
		key,
		record,
		graph,
		observed,
		"Deleting",
		FinalizationRemove,
		nil,
	)
	if err := reconciler.commit(ctx, intent); err != nil {
		return Result{}, err
	}
	return Result{phase: "Deleting"}, nil
}

func (reconciler *InstanceReconciler) executeDeletionStage(
	ctx context.Context,
	key controlplane.InstanceKey,
	record controlplane.InstanceRecord,
	graph projection.DesiredGraph,
	observed cluster.ObservedGraph,
	scope cluster.OwnershipScope,
	changes []cluster.OwnedChange,
) (Result, error) {
	changeSet, err := cluster.NewOwnedChangeSet(
		scope,
		cluster.ExecutionPersistent,
		changes,
	)
	if err != nil {
		return reconciler.fail(ctx, key, record, "ConvergenceFailed", err, false)
	}
	if _, err := reconciler.cluster.Execute(ctx, changeSet); err != nil {
		if result, ok := staleObservationResult(err, "Deleting"); ok {
			return result, nil
		}
		return reconciler.fail(
			ctx,
			key,
			record,
			diagnosticCode(err),
			err,
			retryable(err),
		)
	}
	intent := reconciler.statusIntent(
		key,
		record,
		graph,
		observed,
		"Deleting",
		FinalizationRetain,
		nil,
	)
	if err := reconciler.commit(ctx, intent); err != nil {
		return Result{}, err
	}
	return Result{requeue: true, phase: "Deleting"}, nil
}

func (reconciler *InstanceReconciler) executeStage(
	ctx context.Context,
	key controlplane.InstanceKey,
	record controlplane.InstanceRecord,
	graph projection.DesiredGraph,
	observed cluster.ObservedGraph,
	scope cluster.OwnershipScope,
	changes []cluster.OwnedChange,
) (Result, error) {
	changeSet, err := cluster.NewOwnedChangeSet(
		scope,
		cluster.ExecutionPersistent,
		changes,
	)
	if err != nil {
		return reconciler.fail(ctx, key, record, "ConvergenceFailed", err, false)
	}
	if _, err := reconciler.cluster.Execute(ctx, changeSet); err != nil {
		if result, ok := staleObservationResult(err, "Reconciling"); ok {
			return result, nil
		}
		return reconciler.fail(
			ctx,
			key,
			record,
			diagnosticCode(err),
			err,
			retryable(err),
		)
	}
	intent := reconciler.statusIntent(
		key,
		record,
		graph,
		observed,
		"Reconciling",
		FinalizationEnsure,
		nil,
	)
	if err := reconciler.commit(ctx, intent); err != nil {
		return Result{}, err
	}
	return Result{requeue: true, phase: "Reconciling"}, nil
}

func staleObservationResult(err error, phase string) (Result, bool) {
	if cluster.ErrorCodeOf(err) != cluster.ErrorStaleObservation {
		return Result{}, false
	}
	// A concurrent runtime or API mutation invalidated the exact snapshot used
	// to build this change set. Re-observe before producing another intent;
	// ownership and UID conflicts remain explicit failures.
	return Result{requeue: true, phase: phase}, true
}

func (reconciler *InstanceReconciler) compileAndVerify(
	record controlplane.InstanceRecord,
) (scenario.CanonicalScenario, scenario.CompileReceipt, error) {
	if record.Name.String() == "" ||
		record.InstanceUID.String() == "" ||
		record.DesiredGeneration.Value() == 0 ||
		record.Revision.Generation != record.DesiredGeneration {
		return scenario.CanonicalScenario{}, scenario.CompileReceipt{}, fmt.Errorf(
			"accepted Scenario Instance identity is incomplete",
		)
	}
	input, err := scenario.Document(record.Revision.CanonicalScenario)
	if err != nil {
		return scenario.CanonicalScenario{}, scenario.CompileReceipt{}, err
	}
	compiled, receipt, err := scenario.Compile(input, reconciler.catalog)
	if err != nil {
		return scenario.CanonicalScenario{}, scenario.CompileReceipt{}, err
	}
	if compiled.Digest() != record.Revision.Digest {
		return scenario.CanonicalScenario{}, scenario.CompileReceipt{}, fmt.Errorf(
			"canonical Scenario digest does not match the accepted revision",
		)
	}
	if compiled.Scenario().Fidelity() != record.Fidelity {
		return scenario.CanonicalScenario{}, scenario.CompileReceipt{}, fmt.Errorf(
			"compiled Fidelity Mode does not match the accepted instance",
		)
	}
	if err := verifyProfileReceipts(
		compiled.Scenario(),
		receipt.Resolutions(),
		receipt.AuxiliaryResolutions(),
		record.Revision.Profiles,
	); err != nil {
		return scenario.CanonicalScenario{}, scenario.CompileReceipt{}, err
	}
	return compiled, receipt, nil
}

func verifyProfileReceipts(
	compiled domain.Scenario,
	resolutions []catalog.ResolvedSelection,
	auxiliaryResolutions []catalog.ResolvedSelection,
	accepted []controlplane.ProfileReceipt,
) error {
	acceptedByID := make(map[string]controlplane.ProfileReceipt, len(accepted))
	for _, profile := range accepted {
		if profile.ID == "" {
			return fmt.Errorf("accepted profile receipt has no ID")
		}
		if _, duplicate := acceptedByID[profile.ID]; duplicate {
			return fmt.Errorf("accepted profile receipt %q is duplicated", profile.ID)
		}
		acceptedByID[profile.ID] = profile
	}
	resolutionIndex := 0
	auxiliaryResolutionIndex := 0
	used := make(map[string]struct{})
	verify := func(
		profile domain.ProfileReference,
		resolved catalog.ResolvedSelection,
	) error {
		profileID := profile.ID().String()
		acceptedProfile, found := acceptedByID[profileID]
		if !found ||
			acceptedProfile.Revision != profile.Revision() ||
			acceptedProfile.Digest != resolved.ProfileDigest() ||
			acceptedProfile.Class != resolved.ProfileClass() {
			return fmt.Errorf(
				"accepted profile receipt %q does not match the catalog resolution",
				profileID,
			)
		}
		used[profileID] = struct{}{}
		return nil
	}
	for _, group := range compiled.NodeGroups() {
		for _, pool := range group.Pools() {
			if resolutionIndex >= len(resolutions) {
				return fmt.Errorf("compile receipt has too few pool resolutions")
			}
			resolved := resolutions[resolutionIndex]
			resolutionIndex++
			if err := verify(pool.Profile(), resolved); err != nil {
				return err
			}
		}
		for _, pool := range group.AuxiliaryPools() {
			if auxiliaryResolutionIndex >= len(auxiliaryResolutions) {
				return fmt.Errorf("compile receipt has too few auxiliary pool resolutions")
			}
			resolved := auxiliaryResolutions[auxiliaryResolutionIndex]
			auxiliaryResolutionIndex++
			if err := verify(pool.Profile(), resolved); err != nil {
				return err
			}
		}
	}
	if resolutionIndex != len(resolutions) ||
		auxiliaryResolutionIndex != len(auxiliaryResolutions) ||
		len(used) != len(acceptedByID) {
		return fmt.Errorf("accepted profile receipts do not exactly match the Scenario")
	}
	return nil
}

func (reconciler *InstanceReconciler) replacementCloseChanges(
	graph projection.DesiredGraph,
	fragment projection.ProjectionFragment,
	observed cluster.ObservedGraph,
) ([]cluster.OwnedChange, error) {
	actualByName := make(map[string]cluster.ObservedObject)
	for _, object := range observed.Objects {
		if object.Key.Kind() == cluster.ObjectKindNode {
			actualByName[object.Key.Name()] = object
		}
	}
	fragmentByName := make(map[string]projection.NodeFragment)
	for _, node := range fragment.Nodes() {
		fragmentByName[node.Name()] = node
	}
	closeNames := make([]string, 0)
	for _, desired := range graph.Nodes() {
		actual, found := actualByName[desired.Name()]
		if !found || actual.Node == nil || actual.Node.Unschedulable {
			continue
		}
		capacity, allocatable := desiredNodeResources(
			desired,
			fragmentByName[desired.Name()],
		)
		if hasStaleManagedResource(
			actual.Node.Capacity,
			capacity,
			reconciler.resourceKeys,
		) || hasStaleManagedResource(
			actual.Node.Allocatable,
			allocatable,
			reconciler.resourceKeys,
		) {
			closeNames = append(closeNames, desired.Name())
		}
	}
	if len(closeNames) == 0 {
		return nil, nil
	}
	slices.Sort(closeNames)
	return reconciler.schedulingChanges(
		graph,
		fragment,
		observed,
		closeNames,
		true,
	)
}

func (reconciler *InstanceReconciler) missingIdentityChanges(
	graph projection.DesiredGraph,
	fragment projection.ProjectionFragment,
	observed cluster.ObservedGraph,
) ([]cluster.OwnedChange, error) {
	observedKeys := make(map[string]cluster.ObservedObject, len(observed.Objects))
	for _, object := range observed.Objects {
		observedKeys[objectIdentity(object.Key)] = object
	}
	fragments := make(map[string]projection.NodeFragment, len(fragment.Nodes()))
	for _, node := range fragment.Nodes() {
		fragments[node.Name()] = node
	}
	contribution := reconciler.runtime.NodeContribution()
	inactiveAnnotations := contribution.InactiveAnnotations()
	repairNodeChanges := make([]cluster.OwnedChange, 0, len(graph.Nodes()))
	createNodeChanges := make([]cluster.OwnedChange, 0, len(graph.Nodes()))
	leaseChanges := make([]cluster.OwnedChange, 0, len(graph.Nodes()))
	now := reconciler.now().UTC()
	for _, node := range graph.Nodes() {
		leaseKey, err := cluster.NewObjectKey(
			cluster.ObjectKindLease,
			"kube-node-lease",
			node.Name(),
		)
		if err != nil {
			return nil, err
		}
		_, leaseFound := observedKeys[objectIdentity(leaseKey)]
		if !leaseFound {
			change, err := cluster.NewApplyLease(
				leaseKey,
				cluster.ObjectPreconditions{},
				cluster.LeaseInput{
					HolderIdentity:       node.Name(),
					LeaseDurationSeconds: contribution.LeaseDurationSeconds(),
					RenewTime:            now,
				},
			)
			if err != nil {
				return nil, err
			}
			leaseChanges = append(leaseChanges, change)
		}
		nodeKey, err := cluster.NewObjectKey(
			cluster.ObjectKindNode,
			"",
			node.Name(),
		)
		if err != nil {
			return nil, err
		}
		actualNode, nodeFound := observedKeys[objectIdentity(nodeKey)]
		nodeAnnotations := contribution.Annotations()
		if nodeFound {
			if actualNode.Node == nil {
				return nil, fmt.Errorf(
					"owned Node %q has no observable Node state",
					node.Name(),
				)
			}
			if leaseFound ||
				(actualNode.Node.Unschedulable &&
					containsStringMap(
						actualNode.Node.Annotations,
						inactiveAnnotations,
					)) {
				continue
			}
			nodeAnnotations = inactiveAnnotations
		} else if !leaseFound {
			continue
		}
		labels := node.Labels()
		for key, value := range fragments[node.Name()].IdentityLabels() {
			labels[key] = value
		}
		taints := make([]cluster.NodeTaint, 0, len(node.Taints()))
		for _, taint := range node.Taints() {
			taints = append(taints, cluster.NodeTaint{
				Key:    taint.Key(),
				Value:  taint.Value(),
				Effect: taint.Effect(),
			})
		}
		change, err := cluster.NewApplySyntheticNode(
			nodeKey,
			preconditions(actualNode),
			cluster.SyntheticNodeInput{
				Labels:        labels,
				Annotations:   nodeAnnotations,
				Taints:        taints,
				Unschedulable: true,
			},
		)
		if err != nil {
			return nil, err
		}
		if nodeFound {
			repairNodeChanges = append(repairNodeChanges, change)
		} else {
			createNodeChanges = append(createNodeChanges, change)
		}
	}
	if len(repairNodeChanges) != 0 {
		// A later Reconcile must observe every repaired Node as closed and
		// runtime-inactive before recreating a missing Lease.
		return repairNodeChanges, nil
	}
	if len(leaseChanges) != 0 {
		// KWOK independently renews Node Leases. Establish exact ownership
		// before creating an active Synthetic Node so the runtime can never
		// win an unowned create race.
		return leaseChanges, nil
	}
	return createNodeChanges, nil
}

func (reconciler *InstanceReconciler) metadataChanges(
	graph projection.DesiredGraph,
	fragment projection.ProjectionFragment,
	observed cluster.ObservedGraph,
) ([]cluster.OwnedChange, error) {
	objects := make(map[string]cluster.ObservedObject, len(observed.Objects))
	for _, object := range observed.Objects {
		objects[objectIdentity(object.Key)] = object
	}
	fragments := make(map[string]projection.NodeFragment, len(fragment.Nodes()))
	for _, node := range fragment.Nodes() {
		fragments[node.Name()] = node
	}
	contribution := reconciler.runtime.NodeContribution()
	now := reconciler.now().UTC()
	nodeChanges := make([]cluster.OwnedChange, 0, len(graph.Nodes()))
	leaseChanges := make([]cluster.OwnedChange, 0, len(graph.Nodes()))
	for _, desired := range graph.Nodes() {
		nodeKey, _ := cluster.NewObjectKey(
			cluster.ObjectKindNode,
			"",
			desired.Name(),
		)
		actual, found := objects[objectIdentity(nodeKey)]
		if !found {
			continue
		}
		if actual.Node == nil {
			return nil, fmt.Errorf(
				"owned Node %q has no observable Node state",
				desired.Name(),
			)
		}
		labels := desired.Labels()
		for key, value := range fragments[desired.Name()].IdentityLabels() {
			labels[key] = value
		}
		taints := make([]cluster.NodeTaint, 0, len(desired.Taints()))
		for _, taint := range desired.Taints() {
			taints = append(taints, cluster.NodeTaint{
				Key:    taint.Key(),
				Value:  taint.Value(),
				Effect: taint.Effect(),
			})
		}
		if actual.DesiredGeneration.Value() > graph.Generation().Value() ||
			!containsStringMap(actual.Node.Labels, labels) ||
			hasStaleManagedKey(
				actual.Node.Labels,
				labels,
				reconciler.identityKeys,
			) ||
			!containsStringMap(actual.Node.Annotations, contribution.Annotations()) ||
			!containsTaints(actual.Node.Taints, taints) {
			change, err := cluster.NewApplySyntheticNode(
				nodeKey,
				preconditions(actual),
				cluster.SyntheticNodeInput{
					Labels:        labels,
					Annotations:   contribution.Annotations(),
					Taints:        taints,
					Unschedulable: true,
				},
			)
			if err != nil {
				return nil, err
			}
			nodeChanges = append(nodeChanges, change)
		}

		leaseKey, _ := cluster.NewObjectKey(
			cluster.ObjectKindLease,
			"kube-node-lease",
			desired.Name(),
		)
		lease, found := objects[objectIdentity(leaseKey)]
		if !found {
			continue
		}
		if lease.Lease == nil {
			return nil, fmt.Errorf(
				"owned Lease %q has no observable Lease state",
				desired.Name(),
			)
		}
		if lease.DesiredGeneration.Value() > graph.Generation().Value() ||
			lease.Lease.LeaseDurationSeconds != contribution.LeaseDurationSeconds() {
			change, err := cluster.NewApplyLease(
				leaseKey,
				preconditions(lease),
				cluster.LeaseInput{
					HolderIdentity:       desired.Name(),
					LeaseDurationSeconds: contribution.LeaseDurationSeconds(),
					RenewTime:            now,
				},
			)
			if err != nil {
				return nil, err
			}
			leaseChanges = append(leaseChanges, change)
		}
	}
	if len(nodeChanges) != 0 {
		// Preserve the replacement barrier: observe the closed Node at the new
		// generation before publishing the matching Lease metadata.
		return nodeChanges, nil
	}
	return leaseChanges, nil
}

func (reconciler *InstanceReconciler) schedulingChanges(
	graph projection.DesiredGraph,
	fragment projection.ProjectionFragment,
	observed cluster.ObservedGraph,
	names []string,
	unschedulable bool,
) ([]cluster.OwnedChange, error) {
	desiredByName := make(map[string]projection.DesiredNode, len(graph.Nodes()))
	for _, node := range graph.Nodes() {
		desiredByName[node.Name()] = node
	}
	fragmentByName := make(map[string]projection.NodeFragment, len(fragment.Nodes()))
	for _, node := range fragment.Nodes() {
		fragmentByName[node.Name()] = node
	}
	actualByName := make(map[string]cluster.ObservedObject)
	for _, object := range observed.Objects {
		if object.Key.Kind() == cluster.ObjectKindNode {
			actualByName[object.Key.Name()] = object
		}
	}
	contribution := reconciler.runtime.NodeContribution()
	changes := make([]cluster.OwnedChange, 0, len(names))
	for _, name := range names {
		desired, found := desiredByName[name]
		actual, observed := actualByName[name]
		if !found || !observed || actual.Node == nil {
			return nil, fmt.Errorf(
				"scheduling decision references unknown owned Node %q",
				name,
			)
		}
		labels := desired.Labels()
		for key, value := range fragmentByName[name].IdentityLabels() {
			labels[key] = value
		}
		taints := make([]cluster.NodeTaint, 0, len(desired.Taints()))
		for _, taint := range desired.Taints() {
			taints = append(taints, cluster.NodeTaint{
				Key:    taint.Key(),
				Value:  taint.Value(),
				Effect: taint.Effect(),
			})
		}
		nodeKey, _ := cluster.NewObjectKey(cluster.ObjectKindNode, "", name)
		change, err := cluster.NewApplySyntheticNode(
			nodeKey,
			preconditions(actual),
			cluster.SyntheticNodeInput{
				Labels:        labels,
				Annotations:   contribution.Annotations(),
				Taints:        taints,
				Unschedulable: unschedulable,
			},
		)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func (reconciler *InstanceReconciler) statusChanges(
	graph projection.DesiredGraph,
	fragment projection.ProjectionFragment,
	observed cluster.ObservedGraph,
) ([]cluster.OwnedChange, error) {
	objects := make(map[string]cluster.ObservedObject, len(observed.Objects))
	for _, object := range observed.Objects {
		objects[objectIdentity(object.Key)] = object
	}
	fragments := make(map[string]projection.NodeFragment, len(fragment.Nodes()))
	for _, node := range fragment.Nodes() {
		fragments[node.Name()] = node
	}
	changes := make([]cluster.OwnedChange, 0)
	for _, desired := range graph.Nodes() {
		nodeKey, _ := cluster.NewObjectKey(
			cluster.ObjectKindNode,
			"",
			desired.Name(),
		)
		actual, found := objects[objectIdentity(nodeKey)]
		if !found {
			continue
		}
		if actual.Node == nil {
			return nil, fmt.Errorf(
				"owned Node %q has no observable Node state",
				desired.Name(),
			)
		}
		capacity, allocatable := desiredNodeResources(
			desired,
			fragments[desired.Name()],
		)
		if nodeResourcesConverged(
			actual.Node,
			capacity,
			allocatable,
			reconciler.resourceKeys,
		) {
			continue
		}
		statusKey, _ := cluster.NewObjectKey(
			cluster.ObjectKindNodeStatus,
			"",
			desired.Name(),
		)
		change, err := cluster.NewUpdateSyntheticNodeStatus(
			statusKey,
			preconditions(actual),
			cluster.SyntheticNodeStatusInput{
				Capacity:    capacity,
				Allocatable: allocatable,
				ManageReady: false,
				ObservedAt:  reconciler.now().UTC(),
			},
		)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func (reconciler *InstanceReconciler) draClassChanges(
	graph projection.DesiredGraph,
	fragment projection.ProjectionFragment,
	observed cluster.ObservedGraph,
) ([]cluster.OwnedChange, error) {
	observedByName := make(map[string]cluster.ObservedObject)
	for _, object := range observed.Objects {
		if object.Key.Kind() == cluster.ObjectKindDeviceClass {
			observedByName[object.Key.Name()] = object
		}
	}
	changes := make([]cluster.OwnedChange, 0)
	for _, desired := range fragment.DeviceClasses() {
		key, err := cluster.NewObjectKey(
			cluster.ObjectKindDeviceClass,
			"",
			desired.Name(),
		)
		if err != nil {
			return nil, err
		}
		actual, found := observedByName[desired.Name()]
		objectPreconditions := cluster.ObjectPreconditions{}
		if found {
			if actual.DeviceClass == nil {
				return nil, fmt.Errorf(
					"owned DeviceClass %q has no observable stable DRA state",
					desired.Name(),
				)
			}
			if actual.DesiredGeneration == graph.Generation() &&
				slices.Equal(
					actual.DeviceClass.Selectors,
					desired.Selectors(),
				) {
				continue
			}
			objectPreconditions = preconditions(actual)
		}
		change, err := cluster.NewApplyDeviceClass(
			key,
			objectPreconditions,
			cluster.DeviceClassInput{Selectors: desired.Selectors()},
		)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func (reconciler *InstanceReconciler) draSliceChanges(
	graph projection.DesiredGraph,
	fragment projection.ProjectionFragment,
	observed cluster.ObservedGraph,
) ([]cluster.OwnedChange, error) {
	observedByName := make(map[string]cluster.ObservedObject)
	for _, object := range observed.Objects {
		if object.Key.Kind() == cluster.ObjectKindResourceSlice {
			observedByName[object.Key.Name()] = object
		}
	}
	changes := make([]cluster.OwnedChange, 0)
	for _, desired := range fragment.ResourceSlices() {
		key, err := cluster.NewObjectKey(
			cluster.ObjectKindResourceSlice,
			"",
			desired.Name(),
		)
		if err != nil {
			return nil, err
		}
		actual, found := observedByName[desired.Name()]
		objectPreconditions := cluster.ObjectPreconditions{}
		if found {
			if actual.ResourceSlice == nil {
				return nil, fmt.Errorf(
					"owned ResourceSlice %q has no observable stable DRA state",
					desired.Name(),
				)
			}
			if actual.DesiredGeneration == graph.Generation() &&
				resourceSliceStateEqual(
					actual.ResourceSlice,
					desired,
				) {
				continue
			}
			objectPreconditions = preconditions(actual)
		}
		devices, err := clusterDevices(desired.Devices())
		if err != nil {
			return nil, err
		}
		change, err := cluster.NewApplyResourceSlice(
			key,
			objectPreconditions,
			cluster.ResourceSliceInput{
				Driver:             desired.Driver(),
				PoolName:           desired.PoolName(),
				PoolGeneration:     desired.PoolGeneration(),
				ResourceSliceCount: desired.ResourceSliceCount(),
				NodeName:           desired.NodeName(),
				Devices:            devices,
			},
		)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func resourceSliceStateEqual(
	actual *cluster.ObservedResourceSliceState,
	desired projection.ResourceSliceFragment,
) bool {
	if actual.Driver != desired.Driver() ||
		actual.PoolName != desired.PoolName() ||
		actual.PoolGeneration != desired.PoolGeneration() ||
		actual.ResourceSliceCount != desired.ResourceSliceCount() ||
		actual.NodeName != desired.NodeName() ||
		len(actual.Devices) != len(desired.Devices()) {
		return false
	}
	actualDevices := append([]cluster.DRADevice(nil), actual.Devices...)
	slices.SortFunc(actualDevices, func(left, right cluster.DRADevice) int {
		return compareStrings(left.Name, right.Name)
	})
	desiredDevices := desired.Devices()
	for index := range actualDevices {
		if actualDevices[index].Name != desiredDevices[index].Name() ||
			!deviceAttributesEqual(
				actualDevices[index].Attributes,
				desiredDevices[index].Attributes(),
			) {
			return false
		}
	}
	return true
}

func clusterDevices(
	devices []projection.DeviceFragment,
) ([]cluster.DRADevice, error) {
	result := make([]cluster.DRADevice, 0, len(devices))
	for _, device := range devices {
		attributes := make(
			map[string]cluster.DeviceAttributeValue,
			len(device.Attributes()),
		)
		for key, value := range device.Attributes() {
			switch value.Kind() {
			case projection.DeviceAttributeBool:
				attributes[key] =
					cluster.NewBoolDeviceAttribute(value.Bool())
			case projection.DeviceAttributeString:
				attribute, err := cluster.NewStringDeviceAttribute(value.String())
				if err != nil {
					return nil, err
				}
				attributes[key] = attribute
			default:
				return nil, fmt.Errorf(
					"device %q has unsupported portable DRA attribute %q",
					device.Name(),
					key,
				)
			}
		}
		result = append(result, cluster.DRADevice{
			Name:       device.Name(),
			Attributes: attributes,
		})
	}
	return result, nil
}

func deviceAttributesEqual(
	actual map[string]cluster.DeviceAttributeValue,
	desired map[string]projection.DeviceAttributeValue,
) bool {
	if len(actual) != len(desired) {
		return false
	}
	for key, wanted := range desired {
		value, found := actual[key]
		if !found || string(value.Kind()) != string(wanted.Kind()) ||
			value.Bool() != wanted.Bool() ||
			value.String() != wanted.String() {
			return false
		}
	}
	return true
}

func desiredNodeResources(
	desired projection.DesiredNode,
	fragment projection.NodeFragment,
) (map[string]string, map[string]string) {
	capacity := desired.BaseCapacity()
	allocatable := desired.BaseCapacity()
	for resourceName, value := range fragment.Capacity() {
		capacity[resourceName] = strconv.FormatUint(value, 10)
	}
	for resourceName, value := range fragment.Allocatable() {
		allocatable[resourceName] = strconv.FormatUint(value, 10)
	}
	return capacity, allocatable
}

func nodeResourcesConverged(
	actual *cluster.ObservedNodeState,
	capacity,
	allocatable map[string]string,
	managed map[string]struct{},
) bool {
	return actual != nil &&
		containsStringMap(actual.Capacity, capacity) &&
		containsStringMap(actual.Allocatable, allocatable) &&
		!hasStaleManagedResource(actual.Capacity, capacity, managed) &&
		!hasStaleManagedResource(actual.Allocatable, allocatable, managed)
}

func hasStaleManagedResource(
	actual,
	desired map[string]string,
	managed map[string]struct{},
) bool {
	return hasStaleManagedKey(actual, desired, managed)
}

func hasStaleManagedKey(
	actual,
	desired map[string]string,
	managed map[string]struct{},
) bool {
	for resourceName := range actual {
		if _, known := managed[resourceName]; !known {
			continue
		}
		if _, wanted := desired[resourceName]; !wanted {
			return true
		}
	}
	return false
}

func catalogExtendedResourceNames(snapshot catalog.Snapshot) map[string]struct{} {
	result := make(map[string]struct{})
	for _, summary := range snapshot.List() {
		profile, err := snapshot.Show(summary.ID())
		if err != nil {
			continue
		}
		for _, contract := range profile.Contracts() {
			if contract.Kind() != "extended-resource" {
				continue
			}
			for _, resource := range contract.Resources() {
				result[resource.Name()] = struct{}{}
			}
		}
	}
	return result
}

func catalogIdentityLabelKeys(snapshot catalog.Snapshot) map[string]struct{} {
	result := make(map[string]struct{})
	for _, summary := range snapshot.List() {
		profile, err := snapshot.Show(summary.ID())
		if err != nil {
			continue
		}
		for _, contract := range profile.Contracts() {
			for _, signal := range contract.IdentitySignals() {
				if signal.Kind() == "node-label" {
					result[signal.Key()] = struct{}{}
				}
			}
		}
	}
	return result
}

func (reconciler *InstanceReconciler) statusIntent(
	key controlplane.InstanceKey,
	record controlplane.InstanceRecord,
	graph projection.DesiredGraph,
	observed cluster.ObservedGraph,
	phase string,
	finalization FinalizationAction,
	diagnostics []controlplane.DiagnosticStatus,
) StatusIntent {
	zero, _ := domain.NewGeneration(0)
	type poolKey struct {
		group string
		pool  string
		role  string
	}
	observedNodes := make(map[string]*cluster.ObservedNodeState)
	observedSlices := make([]*cluster.ObservedResourceSliceState, 0)
	for _, object := range observed.Objects {
		if object.Key.Kind() == cluster.ObjectKindNode && object.Node != nil {
			observedNodes[object.Key.Name()] = object.Node
		}
		if object.Key.Kind() == cluster.ObjectKindResourceSlice &&
			object.ResourceSlice != nil {
			observedSlices = append(observedSlices, object.ResourceSlice)
		}
	}
	poolsByKey := make(map[poolKey]controlplane.PoolStatus)
	for _, node := range graph.Nodes() {
		for _, pool := range node.Pools() {
			key := poolKey{group: node.Group(), pool: pool.Name(), role: "accelerator"}
			current := poolsByKey[key]
			current.Group = key.group
			current.Pool = key.pool
			current.Role = key.role
			current.ResourceName = pool.ResourceName()
			current.RequestedTotal = saturatingAdd(
				current.RequestedTotal,
				pool.Capacity(),
			)
			current.RequestedHealthy = saturatingAdd(
				current.RequestedHealthy,
				pool.Allocatable(),
			)
			if actual := observedNodes[node.Name()]; actual != nil {
				if value, ok := parseObservedCount(
					actual.Capacity[pool.ResourceName()],
				); ok {
					current.ObservedTotal = saturatingAdd(
						current.ObservedTotal,
						value,
					)
				}
				if value, ok := parseObservedCount(
					actual.Allocatable[pool.ResourceName()],
				); ok {
					current.ObservedHealthy = saturatingAdd(
						current.ObservedHealthy,
						value,
					)
				}
			}
			if graph.Fidelity().String() == "dra-control-plane" {
				for _, resourceSlice := range observedSlices {
					if resourceSlice.Driver != pool.ResourceName() ||
						resourceSlice.NodeName != node.Name() ||
						resourceSlice.PoolGeneration !=
							int64(graph.Generation().Value()) {
						continue
					}
					current.ObservedTotal = saturatingAdd(
						current.ObservedTotal,
						uint64(len(resourceSlice.Devices)),
					)
					for _, device := range resourceSlice.Devices {
						allocatable, found := device.Attributes["simulation.kasim.io/allocatable"]
						if found &&
							allocatable.Kind() == cluster.DeviceAttributeBool &&
							allocatable.Bool() {
							current.ObservedHealthy = saturatingAdd(
								current.ObservedHealthy,
								1,
							)
						}
					}
				}
			}
			poolsByKey[key] = current
		}
		for _, pool := range node.AuxiliaryPools() {
			key := poolKey{group: node.Group(), pool: pool.Name(), role: "auxiliary"}
			current := poolsByKey[key]
			current.Group = key.group
			current.Pool = key.pool
			current.Role = key.role
			current.Category = pool.Category()
			current.ResourceName = pool.ResourceName()
			current.RequestedTotal = saturatingAdd(
				current.RequestedTotal,
				pool.Capacity(),
			)
			current.RequestedHealthy = saturatingAdd(
				current.RequestedHealthy,
				pool.Allocatable(),
			)
			if actual := observedNodes[node.Name()]; actual != nil {
				if value, ok := parseObservedCount(actual.Capacity[pool.ResourceName()]); ok {
					current.ObservedTotal = saturatingAdd(current.ObservedTotal, value)
				}
				if value, ok := parseObservedCount(actual.Allocatable[pool.ResourceName()]); ok {
					current.ObservedHealthy = saturatingAdd(current.ObservedHealthy, value)
				}
			}
			poolsByKey[key] = current
		}
	}
	keys := make([]poolKey, 0, len(poolsByKey))
	for key := range poolsByKey {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(left, right poolKey) int {
		if compared := compareStrings(left.group, right.group); compared != 0 {
			return compared
		}
		if compared := compareStrings(left.role, right.role); compared != 0 {
			return compared
		}
		return compareStrings(left.pool, right.pool)
	})
	if len(keys) > controlplane.MaximumStatusPools {
		keys = keys[:controlplane.MaximumStatusPools]
	}
	pools := make([]controlplane.PoolStatus, 0, len(keys))
	for _, key := range keys {
		pools = append(pools, poolsByKey[key])
	}
	inventoryCounts := make(map[cluster.ObjectKind]int32)
	for _, object := range observed.Objects {
		inventoryCounts[object.Key.Kind()]++
	}
	inventory := make([]controlplane.InventoryEntry, 0, len(inventoryCounts))
	for kind, count := range inventoryCounts {
		apiVersion := "v1"
		switch kind {
		case cluster.ObjectKindLease:
			apiVersion = "coordination.k8s.io/v1"
		case cluster.ObjectKindDeviceClass, cluster.ObjectKindResourceSlice:
			apiVersion = "resource.k8s.io/v1"
		}
		inventory = append(inventory, controlplane.InventoryEntry{
			APIVersion: apiVersion,
			Kind:       string(kind),
			Count:      count,
		})
	}
	slices.SortFunc(inventory, func(left, right controlplane.InventoryEntry) int {
		return compareStrings(left.Kind, right.Kind)
	})
	if len(inventory) > controlplane.MaximumStatusInventory {
		inventory = inventory[:controlplane.MaximumStatusInventory]
	}
	conditions := []controlplane.ConditionStatus{{
		Type:               "Progressing",
		Status:             "True",
		Reason:             "ConvergenceFailed",
		Message:            "Synthetic Node convergence is in progress",
		ObservedGeneration: int64(record.DesiredGeneration.Value()),
		LastTransitionTime: reconciler.now().UTC(),
	}}
	return StatusIntent{
		Key:                key,
		ResourceVersion:    record.ResourceVersion,
		ObservedGeneration: zero,
		Status: controlplane.InstanceStatus{
			RevisionDigest: record.Revision.Digest,
			Phase:          phase,
			Pools:          pools,
			Inventory:      inventory,
			Diagnostics:    diagnostics,
			Conditions:     conditions,
		},
		Finalization: finalization,
	}
}

func (reconciler *InstanceReconciler) fail(
	ctx context.Context,
	key controlplane.InstanceKey,
	record controlplane.InstanceRecord,
	code string,
	cause error,
	retry bool,
) (Result, error) {
	if code == "" {
		code = "ConvergenceFailed"
	}
	finalization := FinalizationEnsure
	if record.DeletionRequested {
		finalization = FinalizationRetain
	}
	intent := StatusIntent{
		Key:                key,
		ResourceVersion:    record.ResourceVersion,
		ObservedGeneration: record.ObservedGeneration,
		Status: controlplane.InstanceStatus{
			RevisionDigest: record.Revision.Digest,
			Phase:          "Failed",
			Diagnostics: []controlplane.DiagnosticStatus{{
				Code:             code,
				Message:          boundedMessage(cause.Error()),
				Retryable:        retry,
				RevisionAccepted: true,
				ExitCategory:     5,
			}},
			Conditions: []controlplane.ConditionStatus{{
				Type:               conditionType(code, retry),
				Status:             "True",
				Reason:             code,
				Message:            boundedMessage(cause.Error()),
				ObservedGeneration: int64(record.DesiredGeneration.Value()),
				LastTransitionTime: reconciler.now().UTC(),
			}},
		},
		Finalization: finalization,
	}
	if err := reconciler.commit(ctx, intent); err != nil {
		return Result{}, err
	}
	return Result{requeue: retry, phase: "Failed"}, cause
}

func diagnosticCode(err error) string {
	switch cluster.ErrorCodeOf(err) {
	case cluster.ErrorOwnershipConflict:
		return "OwnershipConflict"
	case cluster.ErrorRuntimeUnavailable:
		return "RuntimeUnavailable"
	case cluster.ErrorCapabilityUnavailable,
		cluster.ErrorKubernetesVersionUnsupported,
		cluster.ErrorKubernetesVersionUntested:
		return "CapabilityUnavailable"
	case cluster.ErrorAuthorizationDenied:
		return "AuthorizationDenied"
	case cluster.ErrorAuthenticationFailed, cluster.ErrorTargetUnavailable:
		return "TargetUnavailable"
	default:
		return "ConvergenceFailed"
	}
}

func retryable(err error) bool {
	var clusterError *cluster.Error
	if errors.As(err, &clusterError) {
		return clusterError.Retryable
	}
	return false
}

func conditionType(code string, retry bool) string {
	switch code {
	case "OwnershipConflict":
		return "OwnershipConflict"
	case "CleanupBlocked":
		return "CleanupBlocked"
	default:
		if retry {
			return "Retrying"
		}
		return "Progressing"
	}
}

func boundedMessage(message string) string {
	if len(message) <= domain.MaximumDiagnosticMessageBytes {
		return message
	}
	return message[:domain.MaximumDiagnosticMessageBytes]
}

func parseObservedCount(encoded string) (uint64, bool) {
	value, err := strconv.ParseUint(encoded, 10, 64)
	return value, err == nil
}

func saturatingAdd(current int64, increment uint64) int64 {
	const maximumInt64 = int64(^uint64(0) >> 1)
	if increment >= uint64(maximumInt64) ||
		current > maximumInt64-int64(increment) {
		return maximumInt64
	}
	return current + int64(increment)
}

func objectIdentity(key cluster.ObjectKey) string {
	return string(key.Kind()) + "\x00" + key.Namespace() + "\x00" + key.Name()
}

func preconditions(object cluster.ObservedObject) cluster.ObjectPreconditions {
	return cluster.ObjectPreconditions{
		UID:             object.UID,
		ResourceVersion: object.ResourceVersion,
	}
}

func closeObservedNode(object cluster.ObservedObject) (cluster.OwnedChange, error) {
	if object.Node == nil {
		return nil, fmt.Errorf(
			"owned Node %q has no observable Node state",
			object.Key.Name(),
		)
	}
	labels := make(map[string]string, len(object.Node.Labels))
	for key, value := range object.Node.Labels {
		switch key {
		case cluster.ManagedByLabel,
			cluster.InstanceUIDLabel,
			cluster.DesiredGenerationLabel:
			continue
		default:
			labels[key] = value
		}
	}
	return cluster.NewApplySyntheticNode(
		object.Key,
		preconditions(object),
		cluster.SyntheticNodeInput{
			Labels:        labels,
			Annotations:   object.Node.Annotations,
			Taints:        object.Node.Taints,
			Unschedulable: true,
		},
	)
}

func activePodBlockers(
	pods []cluster.ObservedPod,
	ownedNodeNames map[string]struct{},
) []string {
	blockers := make([]string, 0)
	for _, pod := range pods {
		if _, owned := ownedNodeNames[pod.NodeName]; !owned ||
			terminalPodPhase(pod.Phase) {
			continue
		}
		blockers = append(blockers, pod.Namespace+"/"+pod.Name)
	}
	slices.Sort(blockers)
	return blockers
}

func draClaimBlockers(
	claims []cluster.ObservedResourceClaim,
	classObjects,
	sliceObjects []cluster.ObservedObject,
) []string {
	classNames := make(map[string]struct{})
	for _, object := range classObjects {
		if object.Key.Kind() == cluster.ObjectKindDeviceClass {
			classNames[object.Key.Name()] = struct{}{}
		}
	}
	deviceTuples := make(map[string]struct{})
	for _, object := range sliceObjects {
		if object.Key.Kind() != cluster.ObjectKindResourceSlice ||
			object.ResourceSlice == nil {
			continue
		}
		for _, device := range object.ResourceSlice.Devices {
			deviceTuples[draAllocationIdentity(
				object.ResourceSlice.Driver,
				object.ResourceSlice.PoolName,
				device.Name,
			)] = struct{}{}
		}
	}
	blockers := make([]string, 0)
	for _, claim := range claims {
		blocked := false
		for _, className := range claim.DeviceClassNames {
			if _, found := classNames[className]; found {
				blocked = true
				break
			}
		}
		if !blocked {
			for _, allocation := range claim.Allocations {
				if _, found := deviceTuples[draAllocationIdentity(
					allocation.Driver,
					allocation.Pool,
					allocation.Device,
				)]; found {
					blocked = true
					break
				}
			}
		}
		if blocked {
			blockers = append(blockers, claim.Namespace+"/"+claim.Name)
		}
	}
	slices.Sort(blockers)
	return blockers
}

func draAllocationIdentity(driver, pool, device string) string {
	return driver + "\x00" + pool + "\x00" + device
}

func deletionChanges(
	objects []cluster.ObservedObject,
	kind cluster.ObjectKind,
) []cluster.OwnedChange {
	filtered := make([]cluster.ObservedObject, 0)
	for _, object := range objects {
		if object.Key.Kind() != kind {
			continue
		}
		filtered = append(filtered, object)
	}
	return deleteObjectChanges(filtered)
}

func deleteObjectChanges(
	objects []cluster.ObservedObject,
) []cluster.OwnedChange {
	changes := make([]cluster.OwnedChange, 0, len(objects))
	for _, object := range objects {
		change, err := cluster.NewDeleteOwnedObject(
			object.Key,
			preconditions(object),
		)
		if err != nil {
			continue
		}
		changes = append(changes, change)
	}
	slices.SortFunc(changes, func(left, right cluster.OwnedChange) int {
		return compareStrings(left.Key().Name(), right.Key().Name())
	})
	return changes
}

func containsStringMap(actual, desired map[string]string) bool {
	for key, value := range desired {
		if actual[key] != value {
			return false
		}
	}
	return true
}

func containsTaints(actual, desired []cluster.NodeTaint) bool {
	for _, wanted := range desired {
		found := false
		for _, current := range actual {
			if current == wanted {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func observedProjection(
	fragment projection.ProjectionFragment,
	observed cluster.ObservedGraph,
) (projection.ObservedGraph, error) {
	nodes := make(map[string]cluster.ObservedObject)
	leases := make(map[string]cluster.ObservedObject)
	for _, object := range observed.Objects {
		switch object.Key.Kind() {
		case cluster.ObjectKindNode:
			nodes[object.Key.Name()] = object
		case cluster.ObjectKindLease:
			leases[object.Key.Name()] = object
		}
	}
	requested := make(map[string]map[string]uint64)
	for _, pod := range observed.Pods {
		if pod.NodeName == "" || terminalPodPhase(pod.Phase) {
			continue
		}
		if requested[pod.NodeName] == nil {
			requested[pod.NodeName] = make(map[string]uint64)
		}
		for resourceName, encoded := range pod.Requested {
			value, err := strconv.ParseUint(encoded, 10, 64)
			if err != nil {
				return projection.ObservedGraph{}, fmt.Errorf(
					"Pod %s/%s resource request %q is not an integer",
					pod.Namespace,
					pod.Name,
					resourceName,
				)
			}
			requested[pod.NodeName][resourceName] += value
		}
	}
	inputs := make([]projection.ObservedNodeInput, 0, len(nodes))
	for _, desired := range fragment.Nodes() {
		actual, found := nodes[desired.Name()]
		input := projection.ObservedNodeInput{
			Name:      desired.Name(),
			Exists:    found,
			Requested: requested[desired.Name()],
		}
		if found {
			if actual.Node == nil {
				return projection.ObservedGraph{}, fmt.Errorf(
					"owned Node %q has no observable Node state",
					desired.Name(),
				)
			}
			input.Labels = actual.Node.Labels
			input.Ready = actual.Node.Ready
			input.Unschedulable = actual.Node.Unschedulable
			input.LeaseObserved = leases[desired.Name()].Lease != nil
			var err error
			input.Capacity, err = projectedQuantities(
				actual.Node.Capacity,
				desired.Capacity(),
			)
			if err != nil {
				return projection.ObservedGraph{}, err
			}
			input.Allocatable, err = projectedQuantities(
				actual.Node.Allocatable,
				desired.Allocatable(),
			)
			if err != nil {
				return projection.ObservedGraph{}, err
			}
		}
		inputs = append(inputs, input)
	}
	for name, actual := range nodes {
		found := false
		for _, desired := range fragment.Nodes() {
			if desired.Name() == name {
				found = true
				break
			}
		}
		if found {
			continue
		}
		inputs = append(inputs, projection.ObservedNodeInput{
			Name:          name,
			Exists:        true,
			Labels:        actual.Node.Labels,
			Ready:         actual.Node.Ready,
			Unschedulable: actual.Node.Unschedulable,
			Requested:     requested[name],
		})
	}
	deviceClasses := make([]projection.ObservedDeviceClassInput, 0)
	resourceSlices := make([]projection.ObservedResourceSliceInput, 0)
	for _, object := range observed.Objects {
		switch object.Key.Kind() {
		case cluster.ObjectKindDeviceClass:
			if object.DeviceClass == nil {
				return projection.ObservedGraph{}, fmt.Errorf(
					"owned DeviceClass %q has no observable stable DRA state",
					object.Key.Name(),
				)
			}
			deviceClasses = append(
				deviceClasses,
				projection.ObservedDeviceClassInput{
					Name:      object.Key.Name(),
					Exists:    true,
					Selectors: object.DeviceClass.Selectors,
				},
			)
		case cluster.ObjectKindResourceSlice:
			if object.ResourceSlice == nil {
				return projection.ObservedGraph{}, fmt.Errorf(
					"owned ResourceSlice %q has no observable stable DRA state",
					object.Key.Name(),
				)
			}
			devices := make(
				[]projection.ObservedDeviceInput,
				0,
				len(object.ResourceSlice.Devices),
			)
			for _, device := range object.ResourceSlice.Devices {
				attributes := make(
					map[string]projection.DeviceAttributeValue,
					len(device.Attributes),
				)
				for key, value := range device.Attributes {
					switch value.Kind() {
					case cluster.DeviceAttributeBool:
						attributes[key] =
							projection.NewBoolDeviceAttribute(value.Bool())
					case cluster.DeviceAttributeString:
						attribute, err := projection.NewStringDeviceAttribute(
							value.String(),
						)
						if err != nil {
							return projection.ObservedGraph{}, err
						}
						attributes[key] = attribute
					default:
						return projection.ObservedGraph{}, fmt.Errorf(
							"observed device %q has an unsupported DRA attribute",
							device.Name,
						)
					}
				}
				devices = append(devices, projection.ObservedDeviceInput{
					Name:       device.Name,
					Attributes: attributes,
				})
			}
			resourceSlices = append(
				resourceSlices,
				projection.ObservedResourceSliceInput{
					Name:               object.Key.Name(),
					Exists:             true,
					Driver:             object.ResourceSlice.Driver,
					PoolName:           object.ResourceSlice.PoolName,
					PoolGeneration:     object.ResourceSlice.PoolGeneration,
					ResourceSliceCount: object.ResourceSlice.ResourceSliceCount,
					NodeName:           object.ResourceSlice.NodeName,
					Devices:            devices,
				},
			)
		}
	}
	resourceClaims := make(
		[]projection.ObservedResourceClaimInput,
		0,
		len(observed.ResourceClaims),
	)
	for _, claim := range observed.ResourceClaims {
		allocations := make(
			[]projection.ObservedAllocationInput,
			0,
			len(claim.Allocations),
		)
		for _, allocation := range claim.Allocations {
			allocations = append(
				allocations,
				projection.ObservedAllocationInput{
					Request: allocation.Request,
					Driver:  allocation.Driver,
					Pool:    allocation.Pool,
					Device:  allocation.Device,
				},
			)
		}
		reservations := make(
			[]projection.ObservedConsumerReferenceInput,
			0,
			len(claim.ReservedFor),
		)
		for _, reservation := range claim.ReservedFor {
			reservations = append(
				reservations,
				projection.ObservedConsumerReferenceInput{
					APIGroup: reservation.APIGroup,
					Resource: reservation.Resource,
					Name:     reservation.Name,
					UID:      reservation.UID,
				},
			)
		}
		resourceClaims = append(
			resourceClaims,
			projection.ObservedResourceClaimInput{
				Namespace:        claim.Namespace,
				Name:             claim.Name,
				DeviceClassNames: claim.DeviceClassNames,
				Allocations:      allocations,
				ReservedFor:      reservations,
			},
		)
	}
	pods := make([]projection.ObservedPodInput, 0, len(observed.Pods))
	for _, pod := range observed.Pods {
		pods = append(pods, projection.ObservedPodInput{
			Namespace:      pod.Namespace,
			Name:           pod.Name,
			UID:            pod.UID,
			NodeName:       pod.NodeName,
			ResourceClaims: pod.ResourceClaims,
		})
	}
	return projection.NewObservedGraph(
		inputs,
		projection.DRAObservedInput{
			DeviceClasses:  deviceClasses,
			ResourceSlices: resourceSlices,
			ResourceClaims: resourceClaims,
			Pods:           pods,
		},
	)
}

func projectedQuantities(
	actual map[string]string,
	desired map[string]uint64,
) (map[string]uint64, error) {
	result := make(map[string]uint64, len(desired))
	for resourceName := range desired {
		encoded, found := actual[resourceName]
		if !found {
			continue
		}
		value, err := strconv.ParseUint(encoded, 10, 64)
		if err != nil {
			return nil, fmt.Errorf(
				"observed extended resource %q is not an integer",
				resourceName,
			)
		}
		result[resourceName] = value
	}
	return result, nil
}

func terminalPodPhase(phase string) bool {
	return phase == "Succeeded" || phase == "Failed"
}

func compareStrings(left, right string) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}
