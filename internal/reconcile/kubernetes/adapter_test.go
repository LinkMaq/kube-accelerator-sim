package kubernetes_test

import (
	"context"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	simulationv1alpha1 "github.com/LinkMaq/kube-accelerator-sim/api/simulation/v1alpha1"
	"github.com/LinkMaq/kube-accelerator-sim/internal/controlplane"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
	"github.com/LinkMaq/kube-accelerator-sim/internal/reconcile"
	reconcilekubernetes "github.com/LinkMaq/kube-accelerator-sim/internal/reconcile/kubernetes"
)

func TestStatusWriterPersistsBoundedSnapshotAndEnsuresFinalizer(t *testing.T) {
	t.Parallel()

	kubernetesClient, instance := statusFixture(t, nil)
	writer := reconcilekubernetes.NewStatusWriter(kubernetesClient)
	generation, err := domain.NewGeneration(1)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := domain.ParseDigest(
		"sha256:6b925dcb04e5cd20a65597ab0e53ce09e68e195129a19c0a696a7007a34deade",
	)
	if err != nil {
		t.Fatal(err)
	}
	intent := reconcile.StatusIntent{
		Key: controlplane.InstanceKey{
			Name: mustName(t, instance.Name),
		},
		ResourceVersion:    instance.ResourceVersion,
		ObservedGeneration: generation,
		Status: controlplane.InstanceStatus{
			RevisionDigest: digest,
			Phase:          "Ready",
			Pools: []controlplane.PoolStatus{{
				Group: "nodes", Pool: "accelerators",
				RequestedTotal: 8, RequestedHealthy: 6,
				ObservedTotal: 8, ObservedHealthy: 6,
			}},
			Inventory: []controlplane.InventoryEntry{{
				APIVersion: "v1", Kind: "Node", Count: 1,
			}},
			Conditions: []controlplane.ConditionStatus{{
				Type:               "FidelitySatisfied",
				Status:             "True",
				Reason:             "FidelitySatisfied",
				Message:            "Every required scheduling surface is observed",
				ObservedGeneration: 1,
				LastTransitionTime: time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC),
			}},
		},
		Finalization: reconcile.FinalizationEnsure,
	}
	if err := writer.Commit(context.Background(), intent); err != nil {
		t.Fatal(err)
	}

	current := &simulationv1alpha1.ScenarioInstance{}
	if err := kubernetesClient.Get(
		context.Background(),
		client.ObjectKey{Name: instance.Name},
		current,
	); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(
		current.Finalizers,
		"simulation.kasim.io/owned-resources",
	) ||
		current.Status.ObservedGeneration != 1 ||
		current.Status.RevisionDigest != digest.String() ||
		current.Status.Phase != "Ready" ||
		len(current.Status.Pools) != 1 ||
		len(current.Status.Inventory) != 1 ||
		len(current.Status.Conditions) != 1 ||
		current.Status.Conditions[0].Type != "FidelitySatisfied" {
		t.Fatalf("status/finalizer intent was not persisted: %#v", current)
	}
}

func TestStatusWriterRemovesFinalizerOnlyForExplicitCleanupProof(t *testing.T) {
	t.Parallel()

	kubernetesClient, instance := statusFixture(
		t,
		[]string{"simulation.kasim.io/owned-resources"},
	)
	writer := reconcilekubernetes.NewStatusWriter(kubernetesClient)
	if err := writer.Commit(context.Background(), reconcile.StatusIntent{
		Key: controlplane.InstanceKey{
			Name: mustName(t, instance.Name),
		},
		ResourceVersion: instance.ResourceVersion,
		Status: controlplane.InstanceStatus{
			Phase: "Deleting",
		},
		Finalization: reconcile.FinalizationRemove,
	}); err != nil {
		t.Fatal(err)
	}
	current := &simulationv1alpha1.ScenarioInstance{}
	if err := kubernetesClient.Get(
		context.Background(),
		client.ObjectKey{Name: instance.Name},
		current,
	); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(
		current.Finalizers,
		"simulation.kasim.io/owned-resources",
	) {
		t.Fatalf("ownership finalizer was retained: %#v", current.Finalizers)
	}
}

func TestDeliveryRejectsUnboundedConcurrency(t *testing.T) {
	t.Parallel()

	if _, err := reconcilekubernetes.NewDelivery(
		reconcilekubernetes.DeliveryOptions{
			MaxConcurrentReconciles: reconcilekubernetes.MaximumConcurrentReconciles + 1,
		},
	); err == nil {
		t.Fatal("delivery accepted unbounded reconcile concurrency")
	}
}

func statusFixture(
	t *testing.T,
	finalizers []string,
) (client.Client, *simulationv1alpha1.ScenarioInstance) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := simulationv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	instance := &simulationv1alpha1.ScenarioInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "training-lab",
			UID:             "instance-uid",
			ResourceVersion: "1",
			Finalizers:      finalizers,
		},
	}
	kubernetesClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&simulationv1alpha1.ScenarioInstance{}).
		WithObjects(instance).
		Build()
	return kubernetesClient, instance
}

func mustName(t *testing.T, value string) domain.Name {
	t.Helper()
	name, err := domain.ParseName(value)
	if err != nil {
		t.Fatal(err)
	}
	return name
}
