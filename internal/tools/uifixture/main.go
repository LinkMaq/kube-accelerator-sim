// Command uifixture serves a deterministic, read-only browser-test inventory.
// It is test infrastructure only and is never included in release artifacts.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/inventory"
	"github.com/LinkMaq/kube-accelerator-sim/internal/inventory/memory"
	"github.com/LinkMaq/kube-accelerator-sim/internal/ui"
)

func main() {
	port := flag.Int("port", 18080, "strict loopback port")
	flag.Parse()

	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()
	source := memory.New()
	server, err := ui.NewServer(ctx, ui.Options{
		Module: inventory.New(source),
		Target: cluster.TargetSelection{
			KubeconfigPath: "/browser-fixture/kubeconfig",
			ContextName:    "browser-fixture",
		},
		Port: *port,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	source.Publish(browserObservation())
	fmt.Println(server.AccessURL())
	if err := server.Serve(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func browserObservation() inventory.Observation {
	ready := true
	observedAt := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	sources := []inventory.SourceState{
		availableSource(inventory.SourceNodes, observedAt),
		availableSource(inventory.SourcePods, observedAt),
		availableSource(inventory.SourceScenarios, observedAt),
		availableSource(inventory.SourceResourceSlices, observedAt),
		availableSource(inventory.SourceResourceClaims, observedAt),
		{
			Name: inventory.SourceDeviceClasses, Availability: inventory.AvailabilityForbidden,
			Mode: inventory.SourceModeUnavailable, Freshness: inventory.FreshnessIncomplete,
			Diagnostic: "forbidden fixture source",
		},
	}
	scenarios := []inventory.ScenarioRecord{
		{
			Name: "nvidia-fabric", UID: "scenario-nvidia",
			Signals: []inventory.ScenarioSignalRecord{
				{NodeGroup: "workers", Pool: "h100", Role: inventory.SignalRoleAccelerator, ResourceName: "nvidia.com/gpu", Vendor: "NVIDIA", Model: "NVIDIA H100"},
				{NodeGroup: "workers", Pool: "h100-dra", Role: inventory.SignalRoleAccelerator, ResourceName: "gpu.nvidia.com", Vendor: "NVIDIA", Model: "NVIDIA H100"},
				{NodeGroup: "workers", Pool: "rdma-a", Role: inventory.SignalRoleAuxiliary, Category: "rdma", ResourceName: "rdma/rdma_shared_device_a", Vendor: "RDMA Shared Device Plugin", AssociatedAcceleratorPools: []string{"h100"}},
			},
		},
		{
			Name: "amd-training", UID: "scenario-amd",
			Signals: []inventory.ScenarioSignalRecord{{NodeGroup: "workers", Pool: "mi300x", Role: inventory.SignalRoleAccelerator, ResourceName: "amd.com/gpu", Vendor: "AMD", Model: "AMD Instinct MI300X"}},
		},
		{
			Name: "ascend-inference", UID: "scenario-ascend",
			Signals: []inventory.ScenarioSignalRecord{{NodeGroup: "workers", Pool: "ascend", Role: inventory.SignalRoleAccelerator, ResourceName: "huawei.com/Ascend910", Vendor: "Huawei Ascend", Model: "Ascend 910B"}},
		},
	}
	nodes := make([]inventory.NodeRecord, 0, 1_001)
	for index := range 1_000 {
		uid := "scenario-nvidia"
		resource := "nvidia.com/gpu"
		if index%3 == 1 {
			uid, resource = "scenario-amd", "amd.com/gpu"
		} else if index%3 == 2 {
			uid, resource = "scenario-ascend", "huawei.com/Ascend910"
		}
		capacity := map[string]int64{resource: 8}
		allocatable := map[string]int64{resource: 8}
		if index == 0 {
			capacity["rdma/rdma_shared_device_a"] = 16
			allocatable["rdma/rdma_shared_device_a"] = 12
		}
		nodes = append(nodes, inventory.NodeRecord{
			Name: fmt.Sprintf("kasim-%04d", index),
			Labels: map[string]string{
				cluster.ManagedByLabel:           cluster.ManagedByValue,
				cluster.InstanceUIDLabel:         uid,
				"simulation.kasim.io/node-group": "workers",
			},
			Capacity: capacity, Allocatable: allocatable, Ready: &ready,
		})
	}
	nodes = append(nodes, inventory.NodeRecord{
		Name:        "real-control-plane",
		Capacity:    map[string]int64{"gpu.intel.com/i915": 1},
		Allocatable: map[string]int64{"gpu.intel.com/i915": 1},
		Ready:       &ready,
	})
	return inventory.Observation{
		Target: inventory.Target{
			ContextName: "browser-fixture", Fingerprint: "sha256:browser-fixture",
			KubernetesVersion: "v1.36.3",
		},
		ObservedAt: observedAt,
		Sources:    sources,
		Scenarios:  scenarios,
		Nodes:      nodes,
		Pods: []inventory.PodRecord{{
			Namespace: "workloads", Name: "trainer", UID: "trainer-uid",
			NodeName: "kasim-0000", Requests: map[string]int64{
				"nvidia.com/gpu": 2, "rdma/rdma_shared_device_a": 4,
			}, Claims: []string{"h100-claim"},
		}},
		Devices: []inventory.DRADeviceRecord{{
			NodeName: "kasim-0000", Driver: "gpu.nvidia.com", Pool: "dra-pool", Device: "gpu-0",
			Attributes: map[string]string{"gpu.nvidia.com/productName": "NVIDIA H100"},
		}},
		Claims: []inventory.ClaimRecord{{
			Namespace: "workloads", Name: "h100-claim", ReservedFor: []string{"trainer-uid"},
			Allocations: []inventory.ClaimAllocationRecord{{Driver: "gpu.nvidia.com", Pool: "dra-pool", Device: "gpu-0"}},
		}},
		Diagnostics: []inventory.Diagnostic{{
			Code: "BrowserFixturePartialSource", Message: "one optional source is intentionally unavailable",
			Source: inventory.SourceDeviceClasses,
		}},
	}
}

func availableSource(name inventory.SourceName, observedAt time.Time) inventory.SourceState {
	return inventory.SourceState{
		Name: name, Availability: inventory.AvailabilityAvailable,
		Mode: inventory.SourceModeLive, Freshness: inventory.FreshnessFresh,
		LastSuccess: observedAt,
	}
}
