// Package kubernetes is the thin controller-runtime delivery edge for the
// deep Instance Reconciler Module. Lifecycle ordering stays in the parent
// reconcile package.
package kubernetes

import (
	"context"
	"fmt"
	"slices"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	simulationv1alpha1 "github.com/LinkMaq/kube-accelerator-sim/api/simulation/v1alpha1"
	"github.com/LinkMaq/kube-accelerator-sim/internal/controlplane"
	"github.com/LinkMaq/kube-accelerator-sim/internal/reconcile"
)

const InstanceFinalizer = "simulation.kasim.io/owned-resources"

// StatusWriter persists one bounded product status and its explicit ownership
// finalization instruction.
type StatusWriter struct {
	client client.Client
}

func NewStatusWriter(kubernetesClient client.Client) *StatusWriter {
	return &StatusWriter{client: kubernetesClient}
}

func (writer *StatusWriter) Commit(
	ctx context.Context,
	intent reconcile.StatusIntent,
) error {
	if writer == nil || writer.client == nil {
		return fmt.Errorf("status writer requires a Kubernetes client")
	}
	if intent.Key.Name.String() == "" {
		return fmt.Errorf("status intent requires an exact instance name")
	}
	switch intent.Finalization {
	case reconcile.FinalizationEnsure, reconcile.FinalizationRetain,
		reconcile.FinalizationRemove:
	default:
		return fmt.Errorf(
			"status intent has unsupported finalization %q",
			intent.Finalization,
		)
	}

	current := &simulationv1alpha1.ScenarioInstance{}
	key := client.ObjectKey{Name: intent.Key.Name.String()}
	if err := writer.client.Get(ctx, key, current); err != nil {
		return fmt.Errorf("read Scenario Instance before status commit: %w", err)
	}
	if intent.ResourceVersion != "" &&
		current.ResourceVersion != intent.ResourceVersion {
		return apierrors.NewConflict(
			schema.GroupResource{
				Group:    simulationv1alpha1.GroupVersion.Group,
				Resource: "scenarioinstances",
			},
			current.Name,
			fmt.Errorf("status intent resourceVersion precondition failed"),
		)
	}

	if intent.Finalization != reconcile.FinalizationRemove &&
		!slices.Contains(current.Finalizers, InstanceFinalizer) {
		current.Finalizers = append(current.Finalizers, InstanceFinalizer)
		if err := writer.client.Update(ctx, current); err != nil {
			return fmt.Errorf("ensure Scenario Instance ownership finalizer: %w", err)
		}
	}

	current.Status = transportStatus(intent)
	if err := writer.client.Status().Update(ctx, current); err != nil {
		return fmt.Errorf("update Scenario Instance status: %w", err)
	}

	if intent.Finalization == reconcile.FinalizationRemove &&
		slices.Contains(current.Finalizers, InstanceFinalizer) {
		latest := &simulationv1alpha1.ScenarioInstance{}
		if err := writer.client.Get(ctx, key, latest); err != nil {
			return fmt.Errorf(
				"read Scenario Instance before finalizer removal: %w",
				err,
			)
		}
		latest.Finalizers = slices.DeleteFunc(
			latest.Finalizers,
			func(value string) bool { return value == InstanceFinalizer },
		)
		if err := writer.client.Update(ctx, latest); err != nil {
			return fmt.Errorf("remove Scenario Instance ownership finalizer: %w", err)
		}
	}
	return nil
}

func transportStatus(
	intent reconcile.StatusIntent,
) simulationv1alpha1.ScenarioInstanceStatus {
	pools := bounded(intent.Status.Pools, controlplane.MaximumStatusPools)
	transportPools := make([]simulationv1alpha1.PoolStatus, 0, len(pools))
	for _, pool := range pools {
		transportPools = append(transportPools, simulationv1alpha1.PoolStatus{
			Group:            pool.Group,
			Pool:             pool.Pool,
			RequestedTotal:   pool.RequestedTotal,
			RequestedHealthy: pool.RequestedHealthy,
			ObservedTotal:    pool.ObservedTotal,
			ObservedHealthy:  pool.ObservedHealthy,
		})
	}
	inventory := bounded(
		intent.Status.Inventory,
		controlplane.MaximumStatusInventory,
	)
	transportInventory := make(
		[]simulationv1alpha1.InventoryEntry,
		0,
		len(inventory),
	)
	for _, entry := range inventory {
		transportInventory = append(
			transportInventory,
			simulationv1alpha1.InventoryEntry{
				APIVersion: entry.APIVersion,
				Kind:       entry.Kind,
				Count:      entry.Count,
			},
		)
	}
	diagnostics := bounded(
		intent.Status.Diagnostics,
		controlplane.MaximumStatusDiagnostics,
	)
	transportDiagnostics := make(
		[]simulationv1alpha1.DiagnosticStatus,
		0,
		len(diagnostics),
	)
	for _, diagnostic := range diagnostics {
		transportDiagnostics = append(
			transportDiagnostics,
			simulationv1alpha1.DiagnosticStatus{
				Code:             diagnostic.Code,
				Message:          diagnostic.Message,
				Retryable:        diagnostic.Retryable,
				RevisionAccepted: diagnostic.RevisionAccepted,
				ExitCategory:     diagnostic.ExitCategory,
			},
		)
	}
	conditions := bounded(
		intent.Status.Conditions,
		controlplane.MaximumStatusConditions,
	)
	transportConditions := make([]metav1.Condition, 0, len(conditions))
	for _, condition := range conditions {
		transportConditions = append(transportConditions, metav1.Condition{
			Type:               condition.Type,
			Status:             metav1.ConditionStatus(condition.Status),
			Reason:             condition.Reason,
			Message:            condition.Message,
			ObservedGeneration: condition.ObservedGeneration,
			LastTransitionTime: metav1.NewTime(condition.LastTransitionTime),
		})
	}
	return simulationv1alpha1.ScenarioInstanceStatus{
		ObservedGeneration: int64(intent.ObservedGeneration.Value()),
		RevisionDigest:     intent.Status.RevisionDigest.String(),
		Phase:              intent.Status.Phase,
		Pools:              transportPools,
		Inventory:          transportInventory,
		Diagnostics:        transportDiagnostics,
		Conditions:         transportConditions,
	}
}

func bounded[T any](values []T, limit int) []T {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}
