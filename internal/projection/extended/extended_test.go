package extended_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/LinkMaq/kube-accelerator-sim/internal/catalog"
	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
	"github.com/LinkMaq/kube-accelerator-sim/internal/projection"
	"github.com/LinkMaq/kube-accelerator-sim/internal/projection/extended"
	"github.com/LinkMaq/kube-accelerator-sim/internal/scenario"
)

const instanceUIDValue = "6cb2dd6f-c608-4e79-aaf6-e3fa1287f73c"

func TestProjectionRendersAuxiliarySchedulingSignalsBesideAccelerators(t *testing.T) {
	t.Parallel()

	snapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	auxiliaryProfile, err := snapshot.Show("rdma-shared-device-plugin")
	if err != nil {
		t.Fatal(err)
	}
	document := fmt.Sprintf(`
metadata:
  name: auxiliary-lab
spec:
  fidelity: scheduling
  acceptance: {provisionalProfiles: false}
  nodeGroups:
    - name: nodes
      replicas: 1
      node: {capacity: {}, placement: {}, labels: {}, taints: []}
      acceleratorPools:
        - name: accelerators
          profile: {id: nvidia, revision: 2026-08-03, digest: sha256:15fa27b98c21e0b3bc60661acd0b4835c7e16e5c8b5c949334048ca08f3731de}
          model: nvidia-h100
          contract: device-plugin
          resource: gpu
          variant: {}
          count: 4
          healthy: 4
      auxiliaryDevicePools:
        - name: rdma-a
          profile: {id: rdma-shared-device-plugin, revision: %s, digest: %s}
          contract: shared-hca
          resource: shared-token
          resourceName: rdma/rdma_shared_device_a
          count: 8
          available: 6
          associatedAcceleratorPools: [accelerators]
`, auxiliaryProfile.Revision(), auxiliaryProfile.Digest())
	input, err := scenario.Document([]byte(document))
	if err != nil {
		t.Fatal(err)
	}
	compiled, receipt, err := scenario.Compile(input, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := domain.ParseInstanceUID(instanceUIDValue)
	generation, _ := domain.NewGeneration(1)
	graph, err := projection.Build(projection.BuildInput{
		InstanceName: compiled.Scenario().Name(), InstanceUID: uid,
		Generation: generation, Scenario: compiled.Scenario(),
		Resolutions:          receipt.Resolutions(),
		AuxiliaryResolutions: receipt.AuxiliaryResolutions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	fragment, err := extended.New().Render(graph, schedulingCapabilities())
	if err != nil {
		t.Fatal(err)
	}
	node := fragment.Nodes()[0]
	if node.Capacity()["nvidia.com/gpu"] != 4 ||
		node.Capacity()["rdma/rdma_shared_device_a"] != 8 ||
		node.Allocatable()["rdma/rdma_shared_device_a"] != 6 {
		t.Fatalf("projected resources = capacity %#v allocatable %#v", node.Capacity(), node.Allocatable())
	}
}

func TestProjectionRendersExactSourceBackedCapacityAndSchedulingGate(t *testing.T) {
	t.Parallel()

	graph := buildGraph(t, 8, 6)
	if graph.InstanceName().String() != "training-lab" ||
		graph.InstanceUID().String() != instanceUIDValue ||
		graph.Generation().Value() != 1 {
		t.Fatalf(
			"desired graph lost accepted identity: name=%q uid=%q generation=%d",
			graph.InstanceName(),
			graph.InstanceUID(),
			graph.Generation().Value(),
		)
	}
	desiredNodes := graph.Nodes()
	if len(desiredNodes) != 1 ||
		desiredNodes[0].Labels()[projection.NodeGroupLabel] != "nodes" ||
		desiredNodes[0].Labels()[projection.ReplicaIndexLabel] != "0" ||
		!desiredNodes[0].SchedulingInitiallyClosed() {
		t.Fatalf("desired graph lost Node identity or scheduling gate: %#v", desiredNodes)
	}
	adapter := extended.New()
	support := adapter.Support(schedulingCapabilities(), graph)
	if !support.Supported() || len(support.Issues()) != 0 {
		t.Fatalf("Support() = supported %t, issues %#v", support.Supported(), support.Issues())
	}

	fragment, err := adapter.Render(graph, schedulingCapabilities())
	if err != nil {
		t.Fatal(err)
	}
	nodes := fragment.Nodes()
	if len(nodes) != 1 {
		t.Fatalf("Render() nodes = %d, want 1", len(nodes))
	}
	node := nodes[0]
	if node.Name() != "kasim-node-dd261ef38fe9d85f08e42d49" {
		t.Fatalf("Node name = %q", node.Name())
	}
	if got := node.Capacity()["nvidia.com/gpu"]; got != 8 {
		t.Fatalf("nvidia.com/gpu capacity = %d, want 8", got)
	}
	if got := node.Allocatable()["nvidia.com/gpu"]; got != 6 {
		t.Fatalf("nvidia.com/gpu allocatable = %d, want 6", got)
	}
	if !node.RequiresReady() || !node.RequiresLease() {
		t.Fatal("rendered node lost Ready or Lease fidelity assertion")
	}

	missingLease, err := projection.NewObservedGraph([]projection.ObservedNodeInput{{
		Name:          node.Name(),
		Exists:        true,
		Labels:        node.IdentityLabels(),
		Capacity:      node.Capacity(),
		Allocatable:   node.Allocatable(),
		Ready:         true,
		LeaseObserved: false,
		Unschedulable: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	report := adapter.Assess(missingLease, fragment)
	if report.MayOpenScheduling() ||
		len(report.OpenNodes()) != 0 ||
		report.FidelitySatisfied() {
		t.Fatalf("missing Lease unexpectedly opened or satisfied scheduling: %#v", report)
	}

	complete, err := projection.NewObservedGraph([]projection.ObservedNodeInput{{
		Name:          node.Name(),
		Exists:        true,
		Labels:        node.IdentityLabels(),
		Capacity:      node.Capacity(),
		Allocatable:   node.Allocatable(),
		Ready:         true,
		LeaseObserved: true,
		Unschedulable: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	report = adapter.Assess(complete, fragment)
	if !report.MayOpenScheduling() ||
		len(report.OpenNodes()) != 1 ||
		report.OpenNodes()[0] != node.Name() ||
		report.FidelitySatisfied() {
		t.Fatalf("closed complete Node gate = open %t, satisfied %t, want true/false",
			report.MayOpenScheduling(), report.FidelitySatisfied())
	}
}

func TestProjectionFragmentGolden(t *testing.T) {
	t.Parallel()

	fragment, err := extended.New().Render(
		buildGraph(t, 8, 6),
		schedulingCapabilities(),
	)
	if err != nil {
		t.Fatal(err)
	}
	type goldenNode struct {
		Name           string            `json:"name"`
		IdentityLabels map[string]string `json:"identityLabels"`
		Capacity       map[string]uint64 `json:"capacity"`
		Allocatable    map[string]uint64 `json:"allocatable"`
		RequiresReady  bool              `json:"requiresReady"`
		RequiresLease  bool              `json:"requiresLease"`
	}
	nodes := make([]goldenNode, 0, len(fragment.Nodes()))
	for _, node := range fragment.Nodes() {
		nodes = append(nodes, goldenNode{
			Name:           node.Name(),
			IdentityLabels: node.IdentityLabels(),
			Capacity:       node.Capacity(),
			Allocatable:    node.Allocatable(),
			RequiresReady:  node.RequiresReady(),
			RequiresLease:  node.RequiresLease(),
		})
	}
	actual, err := json.MarshalIndent(struct {
		ObjectKinds []string     `json:"objectKinds"`
		Nodes       []goldenNode `json:"nodes"`
	}{
		ObjectKinds: fragment.ObjectKinds(),
		Nodes:       nodes,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	actual = append(actual, '\n')
	expected, err := os.ReadFile("testdata/nvidia-h100.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, expected) {
		t.Fatalf("projection golden drifted:\n%s", actual)
	}
}

func TestBundledSchedulingContractsRenderExactCatalogResources(t *testing.T) {
	t.Parallel()

	snapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	uid, err := domain.ParseInstanceUID(instanceUIDValue)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := domain.NewGeneration(1)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := 0
	for _, summary := range snapshot.List() {
		profile, err := snapshot.Show(summary.ID())
		if err != nil {
			t.Fatal(err)
		}
		for _, contract := range profile.Contracts() {
			if contract.Kind() != "extended-resource" ||
				!contains(contract.FidelityModes(), "scheduling") {
				continue
			}
			for _, model := range profile.Models() {
				if !model.Selectable() || !contains(model.Contracts(), contract.ID()) {
					continue
				}
				for _, resource := range contract.Resources() {
					if !contains(model.ResourceAliases(), resource.Alias()) {
						continue
					}
					fixtures++
					testName := strings.Join([]string{
						profile.ID(),
						model.ID(),
						contract.ID(),
						resource.Alias(),
					}, "/")
					t.Run(testName, func(t *testing.T) {
						input, err := scenario.Shortcut(scenario.ShortcutInput{
							Name:                       "catalog-contract",
							ProfileID:                  profile.ID(),
							ModelID:                    model.ID(),
							ContractID:                 contract.ID(),
							ResourceAlias:              resource.Alias(),
							Nodes:                      1,
							AcceleratorsPerNode:        2,
							AcceptsProvisionalProfiles: true,
						})
						if err != nil {
							t.Fatal(err)
						}
						compiled, receipt, err := scenario.Compile(input, snapshot)
						if err != nil {
							t.Fatal(err)
						}
						graph, err := projection.Build(projection.BuildInput{
							InstanceName: compiled.Scenario().Name(),
							InstanceUID:  uid,
							Generation:   generation,
							Scenario:     compiled.Scenario(),
							Resolutions:  receipt.Resolutions(),
						})
						if err != nil {
							t.Fatal(err)
						}
						fragment, err := extended.New().Render(
							graph,
							schedulingCapabilities(),
						)
						if err != nil {
							t.Fatal(err)
						}
						if len(fragment.Nodes()) != 1 ||
							fragment.Nodes()[0].Capacity()[resource.Name()] != 2 ||
							fragment.Nodes()[0].Allocatable()[resource.Name()] != 2 {
							t.Fatalf(
								"exact catalog resource %q was not projected: %#v",
								resource.Name(),
								fragment.Nodes(),
							)
						}
					})
				}
			}
		}
	}
	if fixtures < 40 {
		t.Fatalf("only %d bundled scheduling fixtures were exercised", fixtures)
	}
}

func TestHealthAndCapacityChangesNeverProducePodMutations(t *testing.T) {
	t.Parallel()

	adapter := extended.New()
	initial, err := adapter.Render(buildGraph(t, 8, 6), schedulingCapabilities())
	if err != nil {
		t.Fatal(err)
	}
	unhealthy, err := adapter.Render(buildGraph(t, 8, 3), schedulingCapabilities())
	if err != nil {
		t.Fatal(err)
	}
	if initial.Nodes()[0].Capacity()["nvidia.com/gpu"] !=
		unhealthy.Nodes()[0].Capacity()["nvidia.com/gpu"] {
		t.Fatal("health-only revision changed capacity")
	}
	if unhealthy.Nodes()[0].Allocatable()["nvidia.com/gpu"] != 3 {
		t.Fatal("health-only revision did not change allocatable")
	}
	if len(unhealthy.ObjectKinds()) != 1 || unhealthy.ObjectKinds()[0] != "Node" {
		t.Fatalf("projection can mutate unexpected kinds: %v", unhealthy.ObjectKinds())
	}

	reduced, err := adapter.Render(buildGraph(t, 4, 4), schedulingCapabilities())
	if err != nil {
		t.Fatal(err)
	}
	node := reduced.Nodes()[0]
	observed, err := projection.NewObservedGraph([]projection.ObservedNodeInput{{
		Name:          node.Name(),
		Exists:        true,
		Labels:        node.IdentityLabels(),
		Capacity:      node.Capacity(),
		Allocatable:   node.Allocatable(),
		Requested:     map[string]uint64{"nvidia.com/gpu": 6},
		Ready:         true,
		LeaseObserved: true,
		Unschedulable: false,
	}})
	if err != nil {
		t.Fatal(err)
	}
	report := adapter.Assess(observed, reduced)
	overcommitments := report.Overcommitments()
	if len(overcommitments) != 1 ||
		overcommitments[0].ResourceName != "nvidia.com/gpu" ||
		overcommitments[0].Requested != 6 ||
		overcommitments[0].Allocatable != 4 {
		t.Fatalf("unexpected overcommitment report: %#v", overcommitments)
	}
	if !report.FidelitySatisfied() {
		t.Fatal("an observed capacity reduction should remain faithful even when overcommitted")
	}
}

func TestFragmentMergeRejectsResourceAndIdentityCollisions(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		left  projection.NodeFragmentInput
		right projection.NodeFragmentInput
		want  string
	}{
		"resource": {
			left: projection.NodeFragmentInput{
				Name:        "synthetic-a",
				Capacity:    map[string]uint64{"vendor.example/gpu": 8},
				Allocatable: map[string]uint64{"vendor.example/gpu": 8},
			},
			right: projection.NodeFragmentInput{
				Name:        "synthetic-a",
				Capacity:    map[string]uint64{"vendor.example/gpu": 4},
				Allocatable: map[string]uint64{"vendor.example/gpu": 4},
			},
			want: `capacity "vendor.example/gpu"`,
		},
		"identity": {
			left: projection.NodeFragmentInput{
				Name:           "synthetic-a",
				IdentityLabels: map[string]string{"vendor.example/model": "model-a"},
			},
			right: projection.NodeFragmentInput{
				Name:           "synthetic-a",
				IdentityLabels: map[string]string{"vendor.example/model": "model-b"},
			},
			want: `identity label "vendor.example/model"`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			left, err := projection.NewFragment([]projection.NodeFragmentInput{test.left})
			if err != nil {
				t.Fatal(err)
			}
			right, err := projection.NewFragment([]projection.NodeFragmentInput{test.right})
			if err != nil {
				t.Fatal(err)
			}
			_, err = projection.Merge(left, right)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Merge() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func buildGraph(t *testing.T, count, healthy int64) projection.DesiredGraph {
	t.Helper()

	snapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	healthyCopy := healthy
	input, err := scenario.Shortcut(scenario.ShortcutInput{
		Name:                "training-lab",
		ProfileID:           "nvidia",
		ModelID:             "nvidia-h100",
		Nodes:               1,
		AcceleratorsPerNode: count,
		HealthyPerNode:      &healthyCopy,
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, receipt, err := scenario.Compile(input, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	uid, err := domain.ParseInstanceUID(instanceUIDValue)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := domain.NewGeneration(1)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := projection.Build(projection.BuildInput{
		InstanceName: compiled.Scenario().Name(),
		InstanceUID:  uid,
		Generation:   generation,
		Scenario:     compiled.Scenario(),
		Resolutions:  receipt.Resolutions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return graph
}

func schedulingCapabilities() cluster.TargetCapabilities {
	return cluster.TargetCapabilities{
		ServerVersion:   "v1.36.2",
		KubernetesMinor: 36,
		Resources: []cluster.ResourceCapability{
			{
				GroupVersion: "v1",
				Resource:     "nodes",
				Verbs:        []string{"create", "delete", "get", "list", "patch", "watch"},
			},
			{
				GroupVersion: "coordination.k8s.io/v1",
				Resource:     "leases",
				Namespaced:   true,
				Verbs:        []string{"create", "delete", "get", "list", "patch", "watch"},
			},
		},
	}
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
