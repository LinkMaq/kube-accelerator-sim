package contract_test

import (
	"os"
	"strings"
	"testing"
)

func TestReleasePipelineIsEvidenceGatedAndReproducible(t *testing.T) {
	t.Parallel()

	workflow := readReleaseContractFile(t, "../../.github/workflows/release.yml")
	for _, required := range []string{
		"workflow_dispatch:",
		"compatibility_run_id:",
		"protocol_run_id:",
		"scale_run_id:",
		"publish:",
		"recovery_run_id:",
		"release-evidence",
		"release-artifacts",
		"ubuntu-24.04-arm",
		"macos-15-intel",
		"macos-15",
		"windows-2025",
		"linux/amd64,linux/arm64",
		"sbom: true",
		"provenance: mode=max",
		"syft_1.50.0_linux_amd64.tar.gz",
		"--exclude ./.git",
		"--exclude ./release-staging",
		"cosign-release: v3.1.2",
		"subject-checksums:",
		"oci://ghcr.io/linkmaq/charts",
		"gh release",
		"refs/tags/v",
		"--notes-file",
		"Authenticate Cosign to GHCR",
		"chart_digest=",
		"kube-accelerator-sim-controller:${RELEASE_VERSION}",
		"kube-accelerator-sim-controller@${IMAGE_DIGEST}",
		"kasim-runtime@${chart_digest}",
		"Recover verified tag publication",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("release workflow is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"\n  pull_request:",
		"\n  push:",
		"\n  schedule:",
		"continue-on-error:",
		"--exclude .git",
		"--exclude release-staging",
		"--generate-notes",
		"latest",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("release-only workflow contains %q", forbidden)
		}
	}

	builder := readReleaseContractFile(
		t,
		"../../internal/tools/releasebuild/main.go",
	)
	for _, required := range []string{
		"linux",
		"darwin",
		"windows",
		"amd64",
		"arm64",
		"release-dependencies.json",
		"release-receipt.json",
		"kasim-runtime-",
		"SourceDateEpoch",
		"buildid=",
		"kasim_measure_no_ui",
		"compressedBinaryDeltaBytes",
		"maxUIBinaryDeltaBytes",
	} {
		if !strings.Contains(builder, required) {
			t.Errorf("release builder is missing %q", required)
		}
	}

	verifier := readReleaseContractFile(
		t,
		"../../internal/tools/releaseevidence/main.go",
	)
	for _, required := range []string{
		"kasim.io/compatibility-receipt/v1alpha1",
		"kasim.io/protocol-oracle-receipt/v1alpha1",
		"kasim.io/scale-receipt/v1alpha1",
		"v1.30.14",
		"v1.36.3",
		"two consecutive scale trials",
		"ownedLiveObjects",
		"sourceRevision",
	} {
		if !strings.Contains(verifier, required) {
			t.Errorf("release evidence verifier is missing %q", required)
		}
	}
}

func TestVersionedReleaseNotesAreBilingualAndNamePublishedPackages(t *testing.T) {
	t.Parallel()

	notes := readReleaseContractFile(t, "../../release/notes/v0.3.0.md")
	for _, required := range []string{
		"# 中文",
		"# English",
		"ghcr.io/linkmaq/kube-accelerator-sim-controller:0.3.0",
		"oci://ghcr.io/linkmaq/charts/kasim-runtime",
		"Linux amd64/arm64",
		"Windows amd64",
	} {
		if !strings.Contains(notes, required) {
			t.Errorf("v0.3.0 release notes are missing %q", required)
		}
	}
	if strings.Index(notes, "# 中文") > strings.Index(notes, "# English") {
		t.Error("complete Chinese release notes must precede English release notes")
	}
}

func TestReleaseInputsDeclareExplicitPublicSurfaceVersions(t *testing.T) {
	t.Parallel()

	inputs := readReleaseContractFile(t, "../../release/inputs.json")
	for _, required := range []string{
		`"cliBehavior": "v1"`,
		`"scenarioTransport": "v1alpha1"`,
		`"productKubernetesTransport": "simulation.kasim.io/v1alpha1"`,
		`"machineOutput": "v1alpha1"`,
		`"catalog": "2026-08-03"`,
		`"compatibilityMatrix": "2026-07-30"`,
		`"controllerImage": "v1"`,
		`"chart": "0.3.0"`,
	} {
		if !strings.Contains(inputs, required) {
			t.Errorf("release inputs are missing explicit surface %s", required)
		}
	}
}

func readReleaseContractFile(t *testing.T, path string) string {
	t.Helper()
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(encoded)
}
