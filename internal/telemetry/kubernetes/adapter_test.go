package kubernetes

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	simulationv1alpha1 "github.com/LinkMaq/kube-accelerator-sim/api/simulation/v1alpha1"
	"github.com/LinkMaq/kube-accelerator-sim/internal/catalog"
	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
	"github.com/LinkMaq/kube-accelerator-sim/internal/projection"
	"github.com/LinkMaq/kube-accelerator-sim/internal/scenario"
)

func TestSnapshotJoinsOnlyExactOwnedSyntheticNodes(t *testing.T) {
	t.Parallel()

	schedulingCatalog, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	healthy := int64(1)
	input, err := scenario.Shortcut(scenario.ShortcutInput{
		Name: "telemetry-lab", ProfileID: "nvidia", ModelID: "nvidia-h200",
		Nodes: 1, AcceleratorsPerNode: 2, HealthyPerNode: &healthy,
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, receipt, err := scenario.Compile(input, schedulingCatalog)
	if err != nil {
		t.Fatal(err)
	}
	name, _ := domain.ParseName("telemetry-lab")
	uid, _ := domain.ParseInstanceUID("instance-uid")
	generation, _ := domain.NewGeneration(1)
	graph, err := projection.Build(projection.BuildInput{
		InstanceName: name, InstanceUID: uid, Generation: generation,
		Scenario: compiled.Scenario(), Resolutions: receipt.Resolutions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	nodeName := graph.Nodes()[0].Name()
	instance := &simulationv1alpha1.ScenarioInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "telemetry-lab", UID: types.UID("instance-uid")},
		Spec: simulationv1alpha1.ScenarioInstanceSpec{
			DesiredGeneration: 1, CanonicalScenario: string(compiled.Bytes()),
		},
	}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName, Labels: map[string]string{
			cluster.ManagedByLabel:         cluster.ManagedByValue,
			cluster.InstanceUIDLabel:       "instance-uid",
			cluster.DesiredGenerationLabel: "1",
		}},
		Status: corev1.NodeStatus{
			Capacity:    corev1.ResourceList{"nvidia.com/gpu": resource.MustParse("2")},
			Allocatable: corev1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
		},
	}
	foreign := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "foreign", Labels: map[string]string{cluster.ManagedByLabel: "someone-else"},
	}}
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := simulationv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance, node, foreign).Build()
	adapter, err := New(client, schedulingCatalog)
	if err != nil {
		t.Fatal(err)
	}

	observation, err := adapter.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(observation.Nodes) != 1 || observation.Nodes[0].Name != nodeName {
		t.Fatalf("observed Nodes = %#v", observation.Nodes)
	}
	if len(observation.Devices) != 2 {
		t.Fatalf("observed devices = %#v", observation.Devices)
	}
	if !observation.Devices[0].Healthy || observation.Devices[1].Healthy {
		t.Fatalf("device health = %#v", observation.Devices)
	}
	for _, device := range observation.Devices {
		if device.ProfileID != "nvidia" || device.ModelID != "nvidia-h200" ||
			device.NodeName != nodeName {
			t.Errorf("unexpected device = %#v", device)
		}
	}
}

func TestSnapshotRejectsUnboundedObservedDeviceQuantityBeforeExpansion(t *testing.T) {
	t.Parallel()

	schedulingCatalog, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	input, err := scenario.Shortcut(scenario.ShortcutInput{
		Name: "oversized-telemetry", ProfileID: "nvidia", ModelID: "nvidia-h200",
		Nodes: 1, AcceleratorsPerNode: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, receipt, err := scenario.Compile(input, schedulingCatalog)
	if err != nil {
		t.Fatal(err)
	}
	name, _ := domain.ParseName("oversized-telemetry")
	uid, _ := domain.ParseInstanceUID("oversized-instance")
	generation, _ := domain.NewGeneration(1)
	graph, err := projection.Build(projection.BuildInput{
		InstanceName: name, InstanceUID: uid, Generation: generation,
		Scenario: compiled.Scenario(), Resolutions: receipt.Resolutions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	instance := &simulationv1alpha1.ScenarioInstance{
		ObjectMeta: metav1.ObjectMeta{Name: name.String(), UID: types.UID(uid.String())},
		Spec: simulationv1alpha1.ScenarioInstanceSpec{
			DesiredGeneration: 1, CanonicalScenario: string(compiled.Bytes()),
		},
	}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: graph.Nodes()[0].Name(), Labels: map[string]string{
			cluster.ManagedByLabel: cluster.ManagedByValue, cluster.InstanceUIDLabel: uid.String(),
			cluster.DesiredGenerationLabel: "1",
		}},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{"nvidia.com/gpu": resource.MustParse("9000000000")},
		},
	}
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := simulationv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance, node).Build()
	adapter, err := New(reader, schedulingCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Snapshot(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "exceed maximum 8000") {
		t.Fatalf("Snapshot() error = %v, want bounded-device failure", err)
	}
}
