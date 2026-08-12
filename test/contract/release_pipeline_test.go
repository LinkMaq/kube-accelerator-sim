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

func TestDocumentationReleaseNavigationTracksPublishedReleases(t *testing.T) {
	t.Parallel()

	pagesWorkflow := readReleaseContractFile(t, "../../.github/workflows/pages.yml")
	for _, required := range []string{
		"release_version:",
		"fetch-depth: 0",
		"gh release view",
		"KASIM_DOCS_RELEASE_VERSION",
		"npm run docs:check-release",
	} {
		if !strings.Contains(pagesWorkflow, required) {
			t.Errorf("documentation workflow is missing %q", required)
		}
	}

	releaseWorkflow := readReleaseContractFile(t, "../../.github/workflows/release.yml")
	if refreshes := strings.Count(releaseWorkflow, "gh workflow run pages.yml"); refreshes != 2 {
		t.Errorf("release workflow dispatches documentation refresh %d times, want 2", refreshes)
	}

	config := readReleaseContractFile(t, "../../docs/.vitepress/config.mts")
	for _, required := range []string{
		"resolveDocumentationReleaseVersion",
		"releases/tag/${releaseVersion}",
	} {
		if !strings.Contains(config, required) {
			t.Errorf("VitePress config is missing %q", required)
		}
	}
	if strings.Contains(config, "releases/tag/v0.1.0") {
		t.Error("VitePress config still hard-codes the first release")
	}

	packageManifest := readReleaseContractFile(t, "../../package.json")
	if !strings.Contains(packageManifest, `"docs:check-release"`) {
		t.Error("package scripts do not expose the built-site release check")
	}
}

func TestVersionedReleaseNotesAreBilingualAndNamePublishedPackages(t *testing.T) {
	t.Parallel()

	notes := readReleaseContractFile(t, "../../release/notes/v0.5.3.md")
	for _, required := range []string{
		"# 中文",
		"# English",
		"ghcr.io/linkmaq/kube-accelerator-sim-controller:0.5.3",
		"oci://ghcr.io/linkmaq/charts/kasim-runtime",
		"Linux amd64/arm64",
		"Windows amd64",
	} {
		if !strings.Contains(notes, required) {
			t.Errorf("v0.5.3 release notes are missing %q", required)
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
		`"telemetry": "v1alpha5"`,
		`"chart": "0.5.3"`,
		`"telemetryCatalog"`,
		`"revision": "2026-08-12.1"`,
		`"sha256": "40e52b3a86f7df9be9ccd4bddca7cca1e35f819b05fcfa0aef2aa3a2e9df47b0"`,
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
