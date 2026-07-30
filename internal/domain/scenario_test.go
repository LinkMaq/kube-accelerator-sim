package domain_test

import (
	"testing"

	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

func TestPoolCountsAllowUnavailableAndScaledDownDesiredState(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		total   int64
		healthy int64
	}{
		{total: 0, healthy: 0},
		{total: 8, healthy: 8},
		{total: 8, healthy: 3},
		{total: 8, healthy: 0},
	} {
		counts, err := domain.NewPoolCounts(test.total, test.healthy)
		if err != nil {
			t.Fatalf("NewPoolCounts(%d, %d): %v", test.total, test.healthy, err)
		}
		if counts.Total() != uint64(test.total) || counts.Healthy() != uint64(test.healthy) {
			t.Errorf(
				"NewPoolCounts(%d, %d) = (%d, %d)",
				test.total,
				test.healthy,
				counts.Total(),
				counts.Healthy(),
			)
		}
	}

	for _, test := range []struct {
		total   int64
		healthy int64
	}{
		{total: -1, healthy: 0},
		{total: 1, healthy: -1},
		{total: 1, healthy: 2},
	} {
		if _, err := domain.NewPoolCounts(test.total, test.healthy); err == nil {
			t.Errorf("NewPoolCounts(%d, %d) unexpectedly succeeded", test.total, test.healthy)
		}
	}
}

func TestReplicaCountAllowsZeroAndRejectsNegativeValues(t *testing.T) {
	t.Parallel()

	for _, input := range []int64{0, 1, 1000} {
		count, err := domain.NewReplicaCount(input)
		if err != nil {
			t.Fatalf("NewReplicaCount(%d): %v", input, err)
		}
		if count.Value() != uint64(input) {
			t.Errorf("NewReplicaCount(%d) = %d", input, count.Value())
		}
	}

	if _, err := domain.NewReplicaCount(-1); err == nil {
		t.Fatal("NewReplicaCount(-1) unexpectedly succeeded")
	}
}

func TestScenarioRepresentsOneHomogeneousNodeGroupAndPool(t *testing.T) {
	t.Parallel()

	scenarioName, err := domain.ParseName("single-node-eight-accelerators")
	if err != nil {
		t.Fatal(err)
	}
	groupName, err := domain.ParseName("workers")
	if err != nil {
		t.Fatal(err)
	}
	poolName, err := domain.ParseName("training")
	if err != nil {
		t.Fatal(err)
	}
	profileID, err := domain.ParseName("nvidia")
	if err != nil {
		t.Fatal(err)
	}
	model, err := domain.ParseName("nvidia-h100")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := domain.ParseDigest(
		"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := domain.NewProfileReference(profileID, "2026-07-29", digest)
	if err != nil {
		t.Fatal(err)
	}
	counts, err := domain.NewPoolCounts(8, 8)
	if err != nil {
		t.Fatal(err)
	}
	variant := map[string]string{"sharing": "disabled"}
	pool, err := domain.NewAcceleratorPool(domain.AcceleratorPoolInput{
		Name:     poolName,
		Profile:  profile,
		Model:    model,
		Contract: "device-plugin",
		Resource: "gpu",
		Variant:  variant,
		Counts:   counts,
	})
	if err != nil {
		t.Fatal(err)
	}
	replicas, err := domain.NewReplicaCount(1)
	if err != nil {
		t.Fatal(err)
	}
	taint, err := domain.NewTaint("accelerator", "simulated", "NoSchedule")
	if err != nil {
		t.Fatal(err)
	}
	if taint.Key() != "accelerator" ||
		taint.Value() != "simulated" ||
		taint.Effect() != "NoSchedule" {
		t.Fatalf("Taint accessors lost portable intent: %#v", taint)
	}
	capacity := map[string]string{"cpu": "64", "memory": "256Gi", "pods": "110"}
	labels := map[string]string{"workload.example.com/class": "training"}
	node, err := domain.NewNodeTemplate(domain.NodeTemplateInput{
		Capacity:  capacity,
		Placement: map[string]string{"zone": "lab-a"},
		Labels:    labels,
		Taints:    []domain.Taint{taint},
	})
	if err != nil {
		t.Fatal(err)
	}
	group, err := domain.NewNodeGroup(domain.NodeGroupInput{
		Name:     groupName,
		Replicas: replicas,
		Node:     node,
		Pools:    []domain.AcceleratorPool{pool},
	})
	if err != nil {
		t.Fatal(err)
	}
	fidelity, err := domain.ParseFidelityMode("scheduling")
	if err != nil {
		t.Fatal(err)
	}

	scenario, err := domain.NewScenario(domain.ScenarioInput{
		Name:                       scenarioName,
		Fidelity:                   fidelity,
		AcceptsProvisionalProfiles: false,
		NodeGroups:                 []domain.NodeGroup{group},
	})
	if err != nil {
		t.Fatal(err)
	}

	if scenario.Name() != scenarioName || scenario.Fidelity() != fidelity {
		t.Fatalf("Scenario identity was not preserved")
	}
	if scenario.AcceptsProvisionalProfiles() {
		t.Fatal("Scenario unexpectedly accepts provisional profiles")
	}
	if len(scenario.NodeGroups()) != 1 ||
		scenario.NodeGroups()[0].Replicas() != replicas {
		t.Fatalf("Scenario topology was not preserved: %#v", scenario.NodeGroups())
	}
	capacity["cpu"] = "1"
	labels["workload.example.com/class"] = "mutated"
	actualNode := scenario.NodeGroups()[0].Node()
	if actualNode.Capacity()["cpu"] != "64" ||
		actualNode.Labels()["workload.example.com/class"] != "training" ||
		actualNode.Placement()["zone"] != "lab-a" ||
		actualNode.Taints()[0] != taint {
		t.Fatalf("Node template was not preserved immutably: %#v", actualNode)
	}
	actualPool := scenario.NodeGroups()[0].Pools()[0]
	if actualPool.Profile() != profile ||
		actualPool.Model() != model ||
		actualPool.Contract() != "device-plugin" ||
		actualPool.Resource() != "gpu" ||
		actualPool.Variant()["sharing"] != "disabled" ||
		actualPool.Counts().Total() != 8 {
		t.Fatalf("Accelerator Pool contract was not preserved: %#v", actualPool)
	}
	variant["sharing"] = "mutated"
	returnedVariant := actualPool.Variant()
	returnedVariant["sharing"] = "also-mutated"
	if scenario.NodeGroups()[0].Pools()[0].Variant()["sharing"] != "disabled" {
		t.Fatal("Accelerator Pool exposed mutable variant storage")
	}
}

func TestScenarioAggregateRejectsDuplicateGroupAndPoolNames(t *testing.T) {
	t.Parallel()

	pool := newTestAcceleratorPool(t, "training")
	groupName, err := domain.ParseName("workers")
	if err != nil {
		t.Fatal(err)
	}
	replicas, err := domain.NewReplicaCount(1)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("pool", func(t *testing.T) {
		t.Parallel()
		if _, err := domain.NewNodeGroup(domain.NodeGroupInput{
			Name:     groupName,
			Replicas: replicas,
			Pools:    []domain.AcceleratorPool{pool, pool},
		}); err == nil {
			t.Fatal("duplicate Accelerator Pool names unexpectedly succeeded")
		}
	})

	t.Run("group", func(t *testing.T) {
		t.Parallel()
		group, err := domain.NewNodeGroup(domain.NodeGroupInput{
			Name:     groupName,
			Replicas: replicas,
			Pools:    []domain.AcceleratorPool{pool},
		})
		if err != nil {
			t.Fatal(err)
		}
		scenarioName, err := domain.ParseName("duplicate-groups")
		if err != nil {
			t.Fatal(err)
		}
		fidelity, err := domain.ParseFidelityMode("scheduling")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := domain.NewScenario(domain.ScenarioInput{
			Name:       scenarioName,
			Fidelity:   fidelity,
			NodeGroups: []domain.NodeGroup{group, group},
		}); err == nil {
			t.Fatal("duplicate Node Group names unexpectedly succeeded")
		}
	})
}

func newTestAcceleratorPool(t *testing.T, value string) domain.AcceleratorPool {
	t.Helper()

	poolName, err := domain.ParseName(value)
	if err != nil {
		t.Fatal(err)
	}
	profileID, err := domain.ParseName("nvidia")
	if err != nil {
		t.Fatal(err)
	}
	model, err := domain.ParseName("nvidia-h100")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := domain.ParseDigest(
		"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := domain.NewProfileReference(profileID, "2026-07-29", digest)
	if err != nil {
		t.Fatal(err)
	}
	counts, err := domain.NewPoolCounts(8, 8)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := domain.NewAcceleratorPool(domain.AcceleratorPoolInput{
		Name:     poolName,
		Profile:  profile,
		Model:    model,
		Contract: "device-plugin",
		Resource: "gpu",
		Counts:   counts,
	})
	if err != nil {
		t.Fatal(err)
	}
	return pool
}
