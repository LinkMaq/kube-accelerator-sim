package inventory_test

import (
	"context"
	"testing"
	"time"

	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/inventory"
	"github.com/LinkMaq/kube-accelerator-sim/internal/inventory/memory"
)

func TestInventoryPublishesEvidenceFirstFullSnapshots(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := memory.New()
	module := inventory.New(source)
	stream, err := module.Open(ctx, inventory.OpenRequest{
		Target: cluster.TargetSelection{
			KubeconfigPath: "/explicit/config",
			ContextName:    "lab",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	loading, err := stream.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loading.Revision != 1 || loading.Completeness != inventory.CompletenessLoading {
		t.Fatalf("first snapshot = %#v, want revision 1 loading", loading)
	}

	ready := true
	observedAt := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	source.Publish(inventory.Observation{
		Target: inventory.Target{
			ContextName:       "lab",
			Fingerprint:       "sha256:target",
			KubernetesVersion: "v1.30.14",
		},
		ObservedAt: observedAt,
		Sources: []inventory.SourceState{
			{
				Name:         inventory.SourceNodes,
				Availability: inventory.AvailabilityAvailable,
				Mode:         inventory.SourceModeLive,
				Freshness:    inventory.FreshnessFresh,
			},
			{
				Name:         inventory.SourcePods,
				Availability: inventory.AvailabilityAvailable,
				Mode:         inventory.SourceModeLive,
				Freshness:    inventory.FreshnessFresh,
			},
			{
				Name:         inventory.SourceResourceClaims,
				Availability: inventory.AvailabilityAvailable,
				Mode:         inventory.SourceModeLive,
				Freshness:    inventory.FreshnessFresh,
			},
			{
				Name:         inventory.SourceResourceSlices,
				Availability: inventory.AvailabilityAvailable,
				Mode:         inventory.SourceModeLive,
				Freshness:    inventory.FreshnessFresh,
			},
		},
		Scenarios: []inventory.ScenarioRecord{
			{
				Name: "mixed", UID: "instance-uid",
				Signals: []inventory.ScenarioSignalRecord{
					{
						NodeGroup: "workers", Pool: "gpu", Role: inventory.SignalRoleAccelerator,
						ResourceName: "nvidia.com/gpu", Vendor: "NVIDIA", Model: "NVIDIA H100",
					},
					{
						NodeGroup: "workers", Pool: "rdma-a", Role: inventory.SignalRoleAuxiliary,
						Category: "rdma", ResourceName: "rdma/rdma_shared_device_a",
						Vendor:                     "RDMA Shared Device Plugin",
						AssociatedAcceleratorPools: []string{"gpu"},
					},
				},
			},
		},
		Nodes: []inventory.NodeRecord{
			{
				Name: "kasim-worker-0",
				Labels: map[string]string{
					cluster.ManagedByLabel:           cluster.ManagedByValue,
					cluster.InstanceUIDLabel:         "instance-uid",
					"simulation.kasim.io/node-group": "workers",
				},
				Capacity: map[string]int64{
					"nvidia.com/gpu": 8, "rdma/rdma_shared_device_a": 16,
				},
				Allocatable: map[string]int64{
					"nvidia.com/gpu": 6, "rdma/rdma_shared_device_a": 12,
				},
				Ready: &ready,
			},
			{
				Name:        "real-worker-0",
				Capacity:    map[string]int64{"amd.com/gpu": 4},
				Allocatable: map[string]int64{"amd.com/gpu": 4},
			},
		},
		Pods: []inventory.PodRecord{
			{
				Namespace: "workloads",
				Name:      "trainer",
				UID:       "pod-uid",
				NodeName:  "kasim-worker-0",
				Requests:  map[string]int64{"nvidia.com/gpu": 2},
				Claims:    []string{"gpu-claim"},
			},
		},
		Devices: []inventory.DRADeviceRecord{{
			NodeName: "real-worker-0", Driver: "gpu.nvidia.com",
			Pool: "real-pool", Device: "gpu-0",
			Attributes: map[string]string{"gpu.nvidia.com/productName": "NVIDIA H200"},
		}},
		Claims: []inventory.ClaimRecord{{
			Namespace: "workloads", Name: "gpu-claim",
			Allocations: []inventory.ClaimAllocationRecord{{
				Driver: "gpu.nvidia.com", Pool: "real-pool", Device: "gpu-0",
			}},
			ReservedFor: []string{"pod-uid"},
		}},
	})

	snapshot, err := stream.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision != 2 || snapshot.Completeness != inventory.CompletenessComplete {
		t.Fatalf("snapshot state = revision %d completeness %q", snapshot.Revision, snapshot.Completeness)
	}
	if !snapshot.GeneratedAt.Equal(observedAt) {
		t.Fatalf("generated at = %v, want %v", snapshot.GeneratedAt, observedAt)
	}
	if len(snapshot.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(snapshot.Nodes))
	}
	kasimNode := snapshot.Nodes[0]
	if kasimNode.Name != "kasim-worker-0" || kasimNode.Ownership != inventory.OwnershipKasim {
		t.Fatalf("first node = %#v, want Kasim node first", kasimNode)
	}
	if kasimNode.Scenario.State != inventory.FactKnown || kasimNode.Scenario.Value != "mixed" {
		t.Fatalf("scenario fact = %#v", kasimNode.Scenario)
	}
	if len(kasimNode.Signals) != 2 {
		t.Fatalf("signals = %#v", kasimNode.Signals)
	}
	signal := kasimNode.Signals[0]
	if signal.Representation != inventory.RepresentationScalar || signal.Device != nil {
		t.Fatalf("scalar signal invented a device: %#v", signal)
	}
	if signal.Capacity.Value != 8 || signal.Allocatable.Value != 6 || signal.Requested.Value != 2 {
		t.Fatalf("signal quantities = %#v", signal)
	}
	if signal.Health.State != inventory.FactUnknown {
		t.Fatalf("health = %#v, want unknown", signal.Health)
	}
	if signal.Role != inventory.SignalRoleAccelerator || signal.Vendor.Value != "NVIDIA" ||
		signal.Model.Value != "NVIDIA H100" {
		t.Fatalf("accelerator classification = %#v", signal)
	}
	auxiliary := kasimNode.Signals[1]
	if auxiliary.Role != inventory.SignalRoleAuxiliary || auxiliary.Category != "rdma" ||
		len(auxiliary.Associations) != 1 || auxiliary.Associations[0] != "gpu" {
		t.Fatalf("auxiliary classification = %#v", auxiliary)
	}
	if snapshot.Nodes[1].Ownership != inventory.OwnershipNonKasim {
		t.Fatalf("second node ownership = %q", snapshot.Nodes[1].Ownership)
	}
	dra := snapshot.Nodes[1].Signals[1]
	if dra.Device == nil || dra.Device.Driver != "gpu.nvidia.com" ||
		dra.Allocation.Value != "scheduled-consumer" ||
		dra.Model.Value != "NVIDIA H200" {
		t.Fatalf("DRA evidence join = %#v", dra)
	}
}

func TestInventoryStreamCloseIsConcurrentSafe(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := memory.New()
	stream, err := inventory.New(source).Open(ctx, inventory.OpenRequest{
		Target: cluster.TargetSelection{
			KubeconfigPath: "/explicit/config",
			ContextName:    "lab",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Next(ctx); err != nil {
		t.Fatal(err)
	}

	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			if _, err := stream.Next(ctx); err != nil {
				return
			}
		}
	}()
	for range 1_000 {
		source.Publish(inventory.Observation{ObservedAt: time.Now().UTC()})
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-readerDone:
	case <-time.After(time.Second):
		t.Fatal("concurrent inventory reader did not stop after Close")
	}
}

func TestInventoryCoalescesSlowConsumerToLatestFullReplacement(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	source := memory.New()
	stream, err := inventory.New(source).Open(ctx, inventory.OpenRequest{
		Target: cluster.TargetSelection{KubeconfigPath: "/explicit/config", ContextName: "lab"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Next(ctx); err != nil {
		t.Fatal(err)
	}
	source.Publish(inventory.Observation{ObservedAt: time.Unix(1, 0).UTC()})
	source.Publish(inventory.Observation{ObservedAt: time.Unix(2, 0).UTC()})

	snapshot, err := stream.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision != 2 || !snapshot.GeneratedAt.Equal(time.Unix(2, 0).UTC()) {
		t.Fatalf("coalesced snapshot = revision %d at %s", snapshot.Revision, snapshot.GeneratedAt)
	}
}

func TestInventoryAggregatesMaximumBoundedPodAndClaimFixture(t *testing.T) {
	ctx := context.Background()
	source := memory.New()
	stream, err := inventory.New(source).Open(ctx, inventory.OpenRequest{
		Target: cluster.TargetSelection{KubeconfigPath: "/explicit/config", ContextName: "scale"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Next(ctx); err != nil {
		t.Fatal(err)
	}

	request := map[string]int64{"nvidia.com/gpu": 1}
	pods := make([]inventory.PodRecord, cluster.MaximumObservedPods)
	for index := range pods {
		pods[index] = inventory.PodRecord{NodeName: "scale-node", Requests: request}
	}
	claims := make([]inventory.ClaimRecord, cluster.MaximumObservedClaims)
	source.Publish(inventory.Observation{
		ObservedAt: time.Now().UTC(),
		Sources: []inventory.SourceState{
			{Name: inventory.SourceNodes, Availability: inventory.AvailabilityAvailable, Freshness: inventory.FreshnessFresh},
			{Name: inventory.SourcePods, Availability: inventory.AvailabilityAvailable, Freshness: inventory.FreshnessFresh},
			{Name: inventory.SourceResourceClaims, Availability: inventory.AvailabilityAvailable, Freshness: inventory.FreshnessFresh},
		},
		Nodes: []inventory.NodeRecord{{
			Name: "scale-node", Capacity: map[string]int64{"nvidia.com/gpu": 8},
			Allocatable: map[string]int64{"nvidia.com/gpu": 8},
		}},
		Pods: pods, Claims: claims,
	})
	snapshot, err := stream.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Nodes[0].Signals[0].Requested.Value; got != cluster.MaximumObservedPods {
		t.Fatalf("aggregated Pod requests = %d, want %d", got, cluster.MaximumObservedPods)
	}
}
