package contract_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

type compatibilityLock struct {
	SchemaVersion              string             `json:"schemaVersion"`
	CheckedAt                  string             `json:"checkedAt"`
	KindVersion                string             `json:"kindVersion"`
	KindBinarySHA256           string             `json:"kindBinarySHA256"`
	ContainerProvider          string             `json:"containerProvider"`
	HostArchitecture           string             `json:"hostArchitecture"`
	ProjectBuiltSourceRevision string             `json:"projectBuiltSourceRevision"`
	ProjectBuiltWorkflowRun    string             `json:"projectBuiltWorkflowRun"`
	Rows                       []compatibilityRow `json:"rows"`
}

type compatibilityRow struct {
	Kubernetes             string   `json:"kubernetes"`
	UpstreamState          string   `json:"upstreamState"`
	Classification         string   `json:"classification"`
	NodeImage              string   `json:"nodeImage"`
	KubernetesSourceSHA256 string   `json:"kubernetesSourceSHA256"`
	Modes                  []string `json:"modes"`
}

func TestCompatibilityLockPinsTheFrozenExactPatchMatrix(t *testing.T) {
	t.Parallel()

	encoded, err := os.ReadFile("../../release/compatibility-lock.json")
	if err != nil {
		t.Fatalf("read compatibility lock: %v", err)
	}
	var lock compatibilityLock
	if err := json.Unmarshal(encoded, &lock); err != nil {
		t.Fatalf("decode compatibility lock: %v", err)
	}
	if lock.SchemaVersion != "kasim.io/compatibility-lock/v1alpha1" ||
		lock.CheckedAt != "2026-07-30" ||
		lock.KindVersion != "v0.32.0" ||
		lock.KindBinarySHA256 !=
			"50030de23cf40a18505f20426f6a8506bedf13c6e509244bd1fa9463721b0f54" ||
		lock.ContainerProvider != "docker" ||
		lock.HostArchitecture != "linux/amd64" ||
		!regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(
			lock.ProjectBuiltSourceRevision,
		) ||
		lock.ProjectBuiltWorkflowRun !=
			"https://github.com/LinkMaq/kube-accelerator-sim/actions/runs/30561224966" {
		t.Fatalf("compatibility lock header drifted: %#v", lock)
	}

	expected := []struct {
		patch          string
		upstreamState  string
		classification string
		modes          []string
	}{
		{"v1.30.14", "eol", "project-built", []string{"scheduling"}},
		{"v1.31.14", "eol", "kind-release-paired", []string{"scheduling"}},
		{"v1.32.13", "eol", "project-built", []string{"scheduling"}},
		{"v1.33.13", "eol", "project-built", []string{"scheduling"}},
		{
			"v1.34.10",
			"active",
			"project-built",
			[]string{"scheduling", "dra-control-plane"},
		},
		{
			"v1.35.7",
			"active",
			"project-built",
			[]string{"scheduling", "dra-control-plane"},
		},
		{
			"v1.36.3",
			"active",
			"project-built",
			[]string{"scheduling", "dra-control-plane"},
		},
	}
	if len(lock.Rows) != len(expected) {
		t.Fatalf("compatibility rows = %d, want %d", len(lock.Rows), len(expected))
	}
	pinnedImage := regexp.MustCompile(
		`^[a-z0-9./_-]+:v1\.[0-9]+\.[0-9]+[^@]*@sha256:[0-9a-f]{64}$`,
	)
	images := make(map[string]struct{}, len(lock.Rows))
	for index, want := range expected {
		row := lock.Rows[index]
		if row.Kubernetes != want.patch ||
			row.UpstreamState != want.upstreamState ||
			row.Classification != want.classification ||
			!reflect.DeepEqual(row.Modes, want.modes) {
			t.Errorf("compatibility row %d = %#v, want %#v", index, row, want)
		}
		if !pinnedImage.MatchString(row.NodeImage) {
			t.Errorf("compatibility row %s has unpinned image %q", row.Kubernetes, row.NodeImage)
		}
		if row.Classification == "project-built" &&
			!regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(
				row.KubernetesSourceSHA256,
			) {
			t.Errorf(
				"project-built row %s has invalid Kubernetes source checksum %q",
				row.Kubernetes,
				row.KubernetesSourceSHA256,
			)
		}
		if row.Classification == "kind-release-paired" &&
			row.KubernetesSourceSHA256 != "" {
			t.Errorf(
				"release-paired row %s unexpectedly claims a project source checksum",
				row.Kubernetes,
			)
		}
		if _, duplicate := images[row.NodeImage]; duplicate {
			t.Errorf("compatibility row repeats image %q", row.NodeImage)
		}
		images[row.NodeImage] = struct{}{}
	}

	inputsEncoded, err := os.ReadFile("../../release/inputs.json")
	if err != nil {
		t.Fatal(err)
	}
	var inputs struct {
		Compatibility struct {
			MatrixLock struct {
				SchemaVersion string `json:"schemaVersion"`
				CheckedAt     string `json:"checkedAt"`
				SHA256        string `json:"sha256"`
			} `json:"matrixLock"`
		} `json:"compatibility"`
	}
	if err := json.Unmarshal(inputsEncoded, &inputs); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	if inputs.Compatibility.MatrixLock.SchemaVersion != lock.SchemaVersion ||
		inputs.Compatibility.MatrixLock.CheckedAt != lock.CheckedAt ||
		inputs.Compatibility.MatrixLock.SHA256 != fmt.Sprintf("%x", sum[:]) {
		t.Fatalf(
			"release input compatibility lock receipt drifted: %#v",
			inputs.Compatibility.MatrixLock,
		)
	}
}

func TestCompatibilityWorkflowSeparatesFastAndFullMatrices(t *testing.T) {
	t.Parallel()

	var source strings.Builder
	for _, path := range []string{
		"../../.github/workflows/compatibility.yml",
		"../../.github/workflows/_compatibility-scheduling-row.yml",
		"../../.github/workflows/_compatibility-dra-row.yml",
	} {
		encoded, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read compatibility workflow %s: %v", path, err)
		}
		source.Write(encoded)
	}
	for _, required := range []string{
		"pull_request:",
		"schedule:",
		"workflow_dispatch:",
		"1.30.14",
		"1.31.14",
		"1.32.13",
		"1.33.13",
		"1.34.10",
		"1.35.7",
		"1.36.3",
		"KASIM_E2E_COMPATIBILITY",
		"KASIM_COMPATIBILITY_RECEIPT",
		"compatibility-receipt-",
		"TestCompatibilitySchedulingLifecycle",
		"TestStableDRASchedulerAllocation",
	} {
		if !strings.Contains(source.String(), required) {
			t.Errorf("compatibility workflow lacks %q", required)
		}
	}
	for _, forbidden := range []string{
		"continue-on-error:",
		":latest",
		"kindest/node:v1.34.8",
		"kindest/node:v1.35.5",
		"kindest/node:v1.36.1",
	} {
		if strings.Contains(source.String(), forbidden) {
			t.Errorf("compatibility workflow contains forbidden %q", forbidden)
		}
	}
}
