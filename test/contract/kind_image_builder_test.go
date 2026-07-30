package contract_test

import (
	"os"
	"strings"
	"testing"
)

func TestExactPatchKindImageBuilderIsManualAndPinned(t *testing.T) {
	t.Parallel()

	encoded, err := os.ReadFile(
		"../../.github/workflows/build-kind-node-images.yml",
	)
	if err != nil {
		t.Fatalf("read exact-patch image builder: %v", err)
	}
	source := string(encoded)
	for _, required := range []string{
		"workflow_dispatch:",
		"packages: write",
		"kind/releases/download/v0.32.0/kind-linux-amd64",
		"50030de23cf40a18505f20426f6a8506bedf13c6e509244bd1fa9463721b0f54",
		"kind build node-image",
		"ghcr.io/linkmaq/kube-accelerator-sim-kind-node",
		"1.30.14",
		"1.32.13",
		"1.33.13",
		"1.34.10",
		"1.35.7",
		"1.36.3",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("exact-patch image builder lacks %q", required)
		}
	}
	for _, forbidden := range []string{
		"pull_request:",
		"schedule:",
		"continue-on-error:",
		":latest",
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("exact-patch image builder contains forbidden %q", forbidden)
		}
	}
}
