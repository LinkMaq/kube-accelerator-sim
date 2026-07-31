package catalog_test

import (
	"slices"
	"testing"

	"github.com/LinkMaq/kube-accelerator-sim/internal/catalog"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

func TestBundledCatalogResolvesNVIDIAH100WithoutDerivingAResourceName(t *testing.T) {
	t.Parallel()

	snapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	fidelity, err := domain.ParseFidelityMode("scheduling")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := snapshot.Resolve(catalog.ResolveRequest{
		ProfileID:     "nvidia",
		ModelID:       "nvidia-h100",
		ContractID:    "device-plugin",
		ResourceAlias: "gpu",
		Fidelity:      fidelity,
	})
	if err != nil {
		t.Fatal(err)
	}

	if resolved.ProfileClass() != "verified" {
		t.Errorf("profile class = %q", resolved.ProfileClass())
	}
	if resolved.ResourceName() != "nvidia.com/gpu" {
		t.Errorf("resource = %q, want exact source-backed nvidia.com/gpu", resolved.ResourceName())
	}
	if resolved.ModelID() != "nvidia-h100" {
		t.Errorf("model = %q", resolved.ModelID())
	}
	evidence := resolved.Evidence()
	if len(evidence) == 0 ||
		evidence[0].Grade != "A" ||
		evidence[0].Revision != "5f27eeeee7eb7f7a4c0581aa10abeda7e4604ed2" {
		t.Fatalf("unexpected evidence receipt: %#v", evidence)
	}
	if snapshot.Digest().String() == "" {
		t.Fatal("catalog digest is empty")
	}
}

func TestBundledCatalogCoversRequiredAcceleratorEcosystemsAtHonestClasses(t *testing.T) {
	t.Parallel()

	snapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"nvidia":                "verified",
		"amd":                   "verified",
		"intel-gpu":             "verified",
		"intel-gaudi":           "verified",
		"huawei-ascend":         "verified",
		"cambricon":             "verified",
		"biren":                 "verified",
		"iluvatar":              "verified",
		"enflame":               "verified",
		"moore-threads":         "verified",
		"furiosa":               "verified",
		"graphcore":             "verified",
		"aws-neuron":            "verified",
		"google-tpu":            "verified",
		"metax":                 "verified",
		"hygon":                 "verified",
		"kunlunxin-hami":        "provisional",
		"vastai-hami":           "provisional",
		"qualcomm-cloud-ai-100": "provisional",
	}

	summaries := snapshot.List()
	ids := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		ids = append(ids, summary.ID())
		if expectedClass, required := want[summary.ID()]; required {
			if summary.Class() != expectedClass {
				t.Errorf(
					"profile %q class = %q, want %q",
					summary.ID(),
					summary.Class(),
					expectedClass,
				)
			}
			delete(want, summary.ID())
		}
	}
	if len(want) != 0 {
		t.Fatalf("required profiles missing: %#v", want)
	}
	if !slices.IsSorted(ids) {
		t.Fatalf("profile list is not deterministic: %#v", ids)
	}
}

func TestBundledCatalogContainsRequiredCommonModelSeeds(t *testing.T) {
	t.Parallel()

	snapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	required := map[string][]string{
		"nvidia": {
			"nvidia-a100-40gb", "nvidia-a100-80gb", "nvidia-h100",
			"nvidia-h200", "nvidia-l40s", "nvidia-b200", "nvidia-b300",
			"nvidia-a800", "nvidia-h800", "nvidia-h20",
		},
		"amd": {
			"amd-mi210", "amd-mi250x", "amd-mi300a", "amd-mi300x",
			"amd-mi325x", "amd-mi350x", "amd-mi355x",
		},
		"intel-gpu": {
			"intel-flex-140", "intel-flex-170", "intel-flex-170v",
			"intel-max-1100", "intel-max-1550",
		},
		"intel-gaudi": {"intel-gaudi2", "intel-gaudi3"},
		"huawei-ascend": {
			"huawei-ascend-310", "huawei-ascend-310p", "huawei-ascend-910",
			"huawei-atlas-a2", "huawei-atlas-a3",
		},
		"cambricon": {
			"cambricon-mlu270", "cambricon-mlu290", "cambricon-mlu370",
			"cambricon-mlu590",
		},
		"biren":    {"biren-br100"},
		"iluvatar": {"iluvatar-bi-v150", "iluvatar-bi-v150s"},
		"enflame":  {"enflame-s60", "enflame-s60g"},
		"moore-threads": {
			"moore-threads-mtt-s3000", "moore-threads-mtt-s80",
			"moore-threads-mtt-s2000", "moore-threads-mtt-s4000",
		},
		"furiosa":   {"furiosa-rngd"},
		"graphcore": {"graphcore-gc200", "graphcore-m2000", "graphcore-ipu-pod"},
		"aws-neuron": {
			"aws-inferentia1", "aws-inferentia2", "aws-trainium1",
			"aws-trainium2", "aws-trainium3",
		},
		"google-tpu": {
			"google-tpu-v4", "google-tpu-v5e", "google-tpu-v5p",
			"google-tpu-v6e", "google-tpu7x",
		},
		"metax": {
			"metax-c500", "metax-c500-p", "metax-c500x", "metax-c280",
			"metax-c290", "metax-c550", "metax-c600", "metax-n260",
		},
		"hygon": {
			"hygon-k100-ai", "hygon-bw200", "hygon-bw1000", "hygon-z100l",
			"hygon-bw1100",
		},
		"kunlunxin-hami": {"kunlunxin-p800", "kunlunxin-r480"},
	}

	for profileID, requiredModels := range required {
		profile, err := snapshot.Show(profileID)
		if err != nil {
			t.Fatalf("Show(%q): %v", profileID, err)
		}
		models := make(map[string]bool, len(profile.Models()))
		for _, model := range profile.Models() {
			models[model.ID()] = model.Selectable()
		}
		for _, modelID := range requiredModels {
			if !models[modelID] {
				t.Errorf("profile %q is missing selectable model %q", profileID, modelID)
			}
		}
	}

	vastai, err := snapshot.Show("vastai-hami")
	if err != nil {
		t.Fatal(err)
	}
	if len(vastai.Models()) != 0 {
		t.Fatalf("Vastai unexpectedly has built-in model seeds: %#v", vastai.Models())
	}
	qualcomm, err := snapshot.Show("qualcomm-cloud-ai-100")
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range qualcomm.Models() {
		if model.Selectable() {
			t.Errorf("Qualcomm catalog-only model %q is selectable", model.ID())
		}
	}
}

func TestBundledCatalogResolvesSourceBackedResourceSignalVariants(t *testing.T) {
	t.Parallel()

	snapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	fidelity, err := domain.ParseFidelityMode("scheduling")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name               string
		profileID          string
		modelID            string
		contractID         string
		resourceAlias      string
		wantResource       string
		acceptsProvisional bool
	}{
		{"nvidia-mig", "nvidia", "nvidia-a100-80gb", "device-plugin", "mig-1g-10gb", "nvidia.com/mig-1g.10gb", false},
		{"nvidia-shared", "nvidia", "nvidia-l40s", "device-plugin", "gpu-shared", "nvidia.com/gpu.shared", false},
		{"amd-partition", "amd", "amd-mi300x", "device-plugin", "cpx-nps1", "amd.com/cpx_nps1", false},
		{"ascend-vnpu", "huawei-ascend", "huawei-ascend-310p", "device-plugin", "npu-core", "huawei.com/npu-core", false},
		{"cambricon-shared", "cambricon", "cambricon-mlu370", "device-plugin", "mlu370-share", "cambricon.com/mlu370.share", false},
		{"metax-sgpu", "metax", "metax-c500", "device-plugin", "sgpu", "metax-tech.com/sgpu", false},
		{"metax-vfio", "metax", "metax-c500", "device-plugin", "vfio-gpu", "metax-tech.com/vfio-gpu", false},
		{"hygon-vdcu", "hygon", "hygon-k100-ai", "device-plugin", "dcu-share-30c-16g", "hygon.com/dcu-share-30c-16g", false},
		{"kunlunxin-hami", "kunlunxin-hami", "kunlunxin-p800", "hami-device", "xpu", "kunlunxin.com/xpu", true},
	}
	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			resolved, err := snapshot.Resolve(catalog.ResolveRequest{
				ProfileID:         test.profileID,
				ModelID:           test.modelID,
				ContractID:        test.contractID,
				ResourceAlias:     test.resourceAlias,
				Fidelity:          fidelity,
				AcceptProvisional: test.acceptsProvisional,
			})
			if err != nil {
				t.Fatal(err)
			}
			if resolved.ResourceName() != test.wantResource {
				t.Errorf(
					"resource = %q, want %q",
					resolved.ResourceName(),
					test.wantResource,
				)
			}
		})
	}
}

func TestProfileViewExposesImmutableOfflineContractEvidence(t *testing.T) {
	t.Parallel()

	snapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	profile, err := snapshot.Show("nvidia")
	if err != nil {
		t.Fatal(err)
	}
	if profile.ID() != "nvidia" ||
		profile.DisplayName() != "NVIDIA" ||
		profile.Class() != "verified" ||
		profile.Revision() != "2026-07-31" ||
		profile.Digest().String() == "" {
		t.Fatalf("incomplete profile identity: %#v", profile)
	}
	if len(profile.Evidence()) != 2 {
		t.Fatalf("evidence count = %d, want 2", len(profile.Evidence()))
	}
	contracts := profile.Contracts()
	if len(contracts) != 2 ||
		contracts[0].ID() != "device-plugin" ||
		contracts[1].ID() != "dra" {
		t.Fatalf("unexpected contracts: %#v", contracts)
	}
	if contracts[0].Kind() != "extended-resource" ||
		contracts[0].ProviderScope() != "any-kubernetes" ||
		contracts[0].Resources()[0].Name() != "nvidia.com/gpu" ||
		contracts[0].IdentitySignals()[0].Key() != "nvidia.com/gpu.product" ||
		contracts[0].Capabilities()["health"] != "verified" {
		t.Fatalf("incomplete contract view: %#v", contracts[0])
	}
	models := profile.Models()
	if models[0].DisplayName() == "" ||
		len(models[0].Aliases()) == 0 ||
		models[0].Lifecycle() == "" ||
		len(models[0].Contracts()) == 0 ||
		len(models[0].ResourceAliases()) == 0 ||
		len(models[0].EvidenceRefs()) == 0 {
		t.Fatalf("incomplete model view: %#v", models[0])
	}

	capabilities := contracts[0].Capabilities()
	capabilities["health"] = "forged"
	if contracts[0].Capabilities()["health"] != "verified" {
		t.Fatal("contract capabilities escaped immutability")
	}
}

func TestResolutionFailsClosedOnEvidenceClassAndModelResourceCompatibility(t *testing.T) {
	t.Parallel()

	snapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	scheduling, err := domain.ParseFidelityMode("scheduling")
	if err != nil {
		t.Fatal(err)
	}

	metax := catalog.ResolveRequest{
		ProfileID:     "metax",
		ModelID:       "metax-c500",
		ContractID:    "device-plugin",
		ResourceAlias: "gpu",
		Fidelity:      scheduling,
	}
	if _, err := snapshot.Resolve(metax); err == nil {
		t.Fatal("provisional MetaX profile resolved without explicit acceptance")
	}
	metax.AcceptProvisional = true
	resolved, err := snapshot.Resolve(metax)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ResourceName() != "metax-tech.com/gpu" {
		t.Fatalf("MetaX resource = %q", resolved.ResourceName())
	}

	if _, err := snapshot.Resolve(catalog.ResolveRequest{
		ProfileID:         "huawei-ascend",
		ModelID:           "huawei-ascend-910",
		ContractID:        "device-plugin",
		ResourceAlias:     "ascend310",
		Fidelity:          scheduling,
		AcceptProvisional: false,
	}); err == nil {
		t.Fatal("Ascend 910 resolved through the Ascend 310 resource")
	}

	if _, err := snapshot.Resolve(catalog.ResolveRequest{
		ProfileID:         "qualcomm-cloud-ai-100",
		ModelID:           "qualcomm-cloud-ai-100-pro",
		ContractID:        "device-plugin",
		ResourceAlias:     "gpu",
		Fidelity:          scheduling,
		AcceptProvisional: true,
	}); err == nil {
		t.Fatal("catalog-only Qualcomm model resolved through an invented resource")
	}
}

func TestVerifiedProfilesResolveExactPublishedResourceNames(t *testing.T) {
	t.Parallel()

	snapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	scheduling, err := domain.ParseFidelityMode("scheduling")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		profile  string
		model    string
		contract string
		alias    string
		resource string
	}{
		{"nvidia", "nvidia-h100", "device-plugin", "gpu", "nvidia.com/gpu"},
		{"amd", "amd-mi300x", "device-plugin", "gpu", "amd.com/gpu"},
		{"intel-gpu", "intel-flex-170", "device-plugin", "i915", "gpu.intel.com/i915"},
		{"intel-gaudi", "intel-gaudi3", "device-plugin", "gaudi", "habana.ai/gaudi"},
		{"huawei-ascend", "huawei-ascend-910", "device-plugin", "ascend910", "huawei.com/Ascend910"},
		{"cambricon", "cambricon-mlu370", "device-plugin", "mlu", "cambricon.com/mlu"},
		{"biren", "biren-br100", "device-plugin", "gpu", "birentech.com/gpu"},
		{"iluvatar", "iluvatar-bi-v150", "device-plugin", "gpu", "iluvatar.com/gpu"},
		{"enflame", "enflame-s60", "device-plugin", "gcu", "enflame.com/gcu"},
		{"moore-threads", "moore-threads-mtt-s3000", "device-plugin", "gpu", "mthreads.com/gpu"},
		{"furiosa", "furiosa-rngd", "device-plugin", "rngd", "furiosa.ai/rngd"},
		{"graphcore", "graphcore-gc200", "device-plugin", "ipu", "c600.graphcore.ai/ipu"},
		{"aws-neuron", "aws-inferentia2", "device-plugin", "neuron", "aws.amazon.com/neuron"},
		{"google-tpu", "google-tpu-v6e", "gke", "tpu", "google.com/tpu"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.profile, func(t *testing.T) {
			t.Parallel()
			resolved, err := snapshot.Resolve(catalog.ResolveRequest{
				ProfileID:     test.profile,
				ModelID:       test.model,
				ContractID:    test.contract,
				ResourceAlias: test.alias,
				Fidelity:      scheduling,
			})
			if err != nil {
				t.Fatal(err)
			}
			if resolved.ResourceName() != test.resource {
				t.Errorf("resource = %q, want %q", resolved.ResourceName(), test.resource)
			}
		})
	}
}

func TestDRAContractsRemainDistinctAndUsePublishedDriverNames(t *testing.T) {
	t.Parallel()

	snapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	dra, err := domain.ParseFidelityMode("dra-control-plane")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		profile string
		model   string
		driver  string
	}{
		{"nvidia", "nvidia-h100", "gpu.nvidia.com"},
		{"amd", "amd-mi300x", "gpu.amd.com"},
		{"aws-neuron", "aws-inferentia2", "neuron.aws.com"},
	}
	for _, test := range tests {
		resolved, err := snapshot.Resolve(catalog.ResolveRequest{
			ProfileID:     test.profile,
			ModelID:       test.model,
			ContractID:    "dra",
			ResourceAlias: "device",
			Fidelity:      dra,
		})
		if err != nil {
			t.Fatalf("%s: %v", test.profile, err)
		}
		if resolved.ContractID() != "dra" || resolved.ResourceName() != test.driver {
			t.Errorf(
				"%s DRA = (%q, %q), want (dra, %q)",
				test.profile,
				resolved.ContractID(),
				resolved.ResourceName(),
				test.driver,
			)
		}
	}
}
