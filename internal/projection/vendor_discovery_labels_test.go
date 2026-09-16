package projection_test

import (
	"fmt"
	"testing"

	"github.com/LinkMaq/kube-accelerator-sim/internal/catalog"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
	"github.com/LinkMaq/kube-accelerator-sim/internal/projection"
	"github.com/LinkMaq/kube-accelerator-sim/internal/scenario"
)

// vendorDiscoveryCase pins one vendor's source-backed node label set for one
// exact model. Vendors inject those labels through different upstream
// mechanisms (NFD feature files, an NFD hook, or the device plugin itself);
// the catalog stores only the resulting Kubernetes-visible keys and values.
type vendorDiscoveryCase struct {
	name     string
	profile  string
	model    string
	contract string
	resource string
	// want holds every key whose value is backed by catalog evidence.
	want map[string]string
}

var vendorDiscoveryCases = []vendorDiscoveryCase{
	{
		name:     "amd-mi300x-whole-device",
		profile:  "amd",
		model:    "amd-mi300x",
		contract: "device-plugin",
		resource: "gpu",
		want: map[string]string{
			"amd.com/gpu.cu-count":     "304",
			"amd.com/gpu.device-id":    "74a1",
			"amd.com/gpu.product-name": "AMD_Instinct_MI300X_OAM",
			"amd.com/gpu.simd-count":   "1216",
		},
	},
	{
		name:     "intel-max-1550-whole-device",
		profile:  "intel-gpu",
		model:    "intel-max-1550",
		contract: "device-plugin",
		resource: "xe",
		want: map[string]string{
			"gpu.intel.com/device.count": "8",
			"gpu.intel.com/family":       "Max_Series",
			"gpu.intel.com/product":      "Max_1550",
		},
	},
	{
		name:     "intel-flex-170-whole-device",
		profile:  "intel-gpu",
		model:    "intel-flex-170",
		contract: "device-plugin",
		resource: "xe",
		want: map[string]string{
			"gpu.intel.com/device.count": "8",
			"gpu.intel.com/family":       "Flex_Series",
			"gpu.intel.com/product":      "Flex_170",
		},
	},
	{
		name:     "huawei-ascend-910-whole-device",
		profile:  "huawei-ascend",
		model:    "huawei-ascend-910",
		contract: "device-plugin",
		resource: "ascend910",
		want: map[string]string{
			"accelerator":                      "huawei-Ascend910",
			"node.kubernetes.io/npu.chip.name": "910A",
			"servertype":                       "Ascend910-32",
		},
	},
	{
		name:     "huawei-atlas-a2-whole-device",
		profile:  "huawei-ascend",
		model:    "huawei-atlas-a2",
		contract: "device-plugin",
		resource: "ascend910",
		want: map[string]string{
			"node.kubernetes.io/npu.chip.name": "910B",
			"servertype":                       "Ascend910B-20",
		},
	},
	{
		name:     "alibaba-ppu-zw810e-whole-device",
		profile:  "alibaba-ppu",
		model:    "alibaba-ppu-zw810e",
		contract: "device-plugin",
		resource: "ppu",
		want: map[string]string{
			"aliyun.accelerator/ppu_count": "8",
			"aliyun.accelerator/ppu_mem":   "98304MiB",
			"aliyun.accelerator/ppu_name":  "PPU-ZW810E",
			"aliyun.accelerator/xpu_type":  "ppu",
		},
	},
}

// vendorDiscoveryScenario renders one Node Group per case. Omitting the
// discoveryLabels field is the pre-existing canonical shape and must keep
// every vendor value out of the graph.
func vendorDiscoveryScenario(
	t *testing.T,
	snapshot catalog.Snapshot,
	cases []vendorDiscoveryCase,
	discoveryLabels bool,
) []byte {
	t.Helper()

	discovery := ""
	if discoveryLabels {
		discovery = "\n        discoveryLabels: true"
	}

	groups := ""
	for _, item := range cases {
		profile, err := snapshot.Show(item.profile)
		if err != nil {
			t.Fatalf("show %s: %v", item.profile, err)
		}
		groups += fmt.Sprintf(`
    - name: %s
      replicas: 1
      node:
        capacity: {}
        placement: {}
        labels: {}
        taints: []%s
      acceleratorPools:
        - name: accelerator
          profile:
            id: %s
            revision: %s
            digest: %s
          model: %s
          contract: %s
          resource: %s
          variant: {}
          count: 8
          healthy: 8
`, item.name, discovery, item.profile, profile.Revision(), profile.Digest(),
			item.model, item.contract, item.resource)
	}

	return []byte(fmt.Sprintf(`metadata:
  name: vendor-discovery-labels
spec:
  fidelity: scheduling
  acceptance:
    provisionalProfiles: false
  nodeGroups:%s`, groups))
}

func buildVendorDiscoveryGraph(
	t *testing.T,
	cases []vendorDiscoveryCase,
	discoveryLabels bool,
) projection.DesiredGraph {
	t.Helper()

	snapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	input, err := scenario.Document(
		vendorDiscoveryScenario(t, snapshot, cases, discoveryLabels),
	)
	if err != nil {
		t.Fatal(err)
	}
	compiled, receipt, err := scenario.Compile(input, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	uid, err := domain.ParseInstanceUID("11111111-2222-3333-4444-555555555555")
	if err != nil {
		t.Fatal(err)
	}
	generation, err := domain.NewGeneration(1)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := projection.Build(projection.BuildInput{
		InstanceName:         compiled.Scenario().Name(),
		InstanceUID:          uid,
		Generation:           generation,
		Scenario:             compiled.Scenario(),
		Resolutions:          receipt.Resolutions(),
		AuxiliaryResolutions: receipt.AuxiliaryResolutions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return graph
}

// signalsByGroup indexes every projected vendor label by Node Group and key.
func signalsByGroup(
	t *testing.T,
	graph projection.DesiredGraph,
) map[string]map[string]string {
	t.Helper()

	result := make(map[string]map[string]string, len(graph.Nodes()))
	for _, node := range graph.Nodes() {
		labels := make(map[string]string, len(node.Pools()))
		for _, pool := range node.Pools() {
			for _, signal := range pool.IdentitySignals() {
				if signal.Kind != "node-label" {
					continue
				}
				if existing, duplicate := labels[signal.Key]; duplicate &&
					existing != signal.Value {
					t.Fatalf(
						"Node Group %q key %q projected twice with %q and %q",
						node.Group(),
						signal.Key,
						existing,
						signal.Value,
					)
				}
				labels[signal.Key] = signal.Value
			}
		}
		result[node.Group()] = labels
	}
	return result
}

func TestVendorDiscoveryLabelsProjectEveryEvidencedValue(t *testing.T) {
	t.Parallel()

	projected := signalsByGroup(t, buildVendorDiscoveryGraph(t, vendorDiscoveryCases, true))

	for _, item := range vendorDiscoveryCases {
		labels, ok := projected[item.name]
		if !ok {
			t.Fatalf("Node Group %q missing from graph", item.name)
		}
		for key, want := range item.want {
			if got := labels[key]; got != want {
				t.Errorf(
					"%s: label %q = %q, want %q",
					item.name,
					key,
					got,
					want,
				)
			}
		}
	}
}

func TestVendorDiscoveryLabelsStayOptInAndNeverInventValues(t *testing.T) {
	t.Parallel()

	off := signalsByGroup(t, buildVendorDiscoveryGraph(t, vendorDiscoveryCases, false))
	on := signalsByGroup(t, buildVendorDiscoveryGraph(t, vendorDiscoveryCases, true))

	for _, item := range vendorDiscoveryCases {
		for key := range item.want {
			if value := off[item.name][key]; value != "" {
				t.Errorf(
					"%s: label %q = %q without discoveryLabels, want empty",
					item.name,
					key,
					value,
				)
			}
		}
	}

	// A model whose sub-fields are not publicly evidenced keeps the declared
	// key with an empty value. The catalog never substitutes a model ID or a
	// resource name for a missing vendor value.
	atlasA2Group := ""
	for _, item := range vendorDiscoveryCases {
		if item.model == "huawei-atlas-a2" {
			atlasA2Group = item.name
		}
	}
	if atlasA2Group == "" {
		t.Fatal("vendor discovery cases lost the huawei-atlas-a2 model")
	}
	value, declared := on[atlasA2Group]["accelerator"]
	if !declared {
		t.Error("huawei-atlas-a2 dropped the declared accelerator key")
	}
	if value != "" {
		t.Errorf("huawei-atlas-a2 accelerator = %q, want empty", value)
	}
}
