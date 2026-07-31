package contract_test

import (
	"os"
	"strings"
	"testing"
)

func TestReferenceScaleGateIsReleaseOnlyAndExact(t *testing.T) {
	t.Parallel()

	workflow := readScaleGateFile(t, "../../.github/workflows/scale.yml")
	for _, required := range []string{
		"workflow_dispatch:",
		"ubuntu-24.04",
		"timeout-minutes: 40",
		"1.36.3",
		"ghcr.io/linkmaq/kube-accelerator-sim-kind-node:v1.36.3-kind-v0.32.0-amd64@sha256:91336f2737cf3ae7039c68945de957c66bad889e6db90bf5d3568293f1ab73db",
		"TestReferenceScaleGate",
		"KASIM_SCALE_TRIALS: \"2\"",
		"scale-receipts",
		"if-no-files-found: error",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("scale workflow is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"pull_request:",
		"push:",
		"schedule:",
		"continue-on-error:",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("release-only scale workflow contains %q", forbidden)
		}
	}

	e2e := readScaleGateFile(t, "../e2e/scale_test.go")
	for _, required := range []string{
		"kasim.io/scale-receipt/v1alpha1",
		"syntheticNodes",
		"1000",
		"accelerators",
		"8000",
		"representativePods",
		"100",
		"applyReady",
		"180",
		"observationP95",
		"2",
		"healthLoss",
		"15",
		"healthRecovery",
		"workload",
		"60",
		"controllerRecovery",
		"120",
		"cleanup",
		"controlPlanePeakBytes",
		"2 << 30",
		"identityDrift",
		"ownedLiveObjects",
		"etcdFileShrinkClaimed",
	} {
		if !strings.Contains(e2e, required) {
			t.Errorf("scale E2E is missing contract token %q", required)
		}
	}

	scenario := readScaleGateFile(
		t,
		"../e2e/testdata/reference-scale.yaml",
	)
	for _, required := range []string{
		"name: reference-scale",
		"fidelity: scheduling",
		"replicas: 900",
		"replicas: 100",
		"id: nvidia",
		"model: nvidia-h100",
		"contract: device-plugin",
		"resource: gpu",
		"digest: sha256:15fa27b98c21e0b3bc60661acd0b4835c7e16e5c8b5c949334048ca08f3731de",
	} {
		if !strings.Contains(scenario, required) {
			t.Errorf("reference scale Scenario is missing %q", required)
		}
	}
	if strings.Count(scenario, "count: 8") != 2 ||
		strings.Count(scenario, "healthy: 8") != 2 {
		t.Error("reference scale Scenario does not keep eight accelerators on both groups")
	}
}

func readScaleGateFile(t *testing.T, path string) string {
	t.Helper()

	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(encoded)
}
