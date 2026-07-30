package contract_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

func TestProtocolOracleIsSeparateReleaseEvidence(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		"device-plugin",
		"node-runtime",
		"protocol-oracle",
	} {
		if _, err := domain.ParseFidelityMode(input); err == nil {
			t.Errorf("test-only protocol surface %q became a product Fidelity Mode", input)
		}
	}

	workflow := readProtocolOracleFile(
		t,
		"../../.github/workflows/protocol-oracle.yml",
	)
	for _, required := range []string{
		"schedule:",
		"workflow_dispatch:",
		"ubuntu-24.04",
		"1.30.14",
		"1.36.3",
		"ghcr.io/linkmaq/kube-accelerator-sim-kind-node:v1.30.14-kind-v0.32.0-amd64@sha256:a73107ec2a139b8ced09922eb0c129731eb0f23d0c37f741d038a6def0e39b25",
		"ghcr.io/linkmaq/kube-accelerator-sim-kind-node:v1.36.3-kind-v0.32.0-amd64@sha256:91336f2737cf3ae7039c68945de957c66bad889e6db90bf5d3568293f1ab73db",
		"TestKubeletDevicePluginProtocolOracle",
		"protocol-oracle-receipt-",
		"if-no-files-found: error",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("protocol oracle workflow is missing %q", required)
		}
	}

	e2e := readProtocolOracleFile(t, "../e2e/device_plugin_oracle_test.go")
	for _, required := range []string{
		"registration",
		"capacity",
		"allocatable",
		"health-transition",
		"allocation",
		"plugin-restart",
		"socket-cleanup",
		"daemonset-cleanup",
		"pod-cleanup",
		"kasim.io/protocol-oracle-receipt/v1alpha1",
	} {
		if !strings.Contains(e2e, required) {
			t.Errorf("protocol oracle E2E is missing evidence surface %q", required)
		}
	}

	dockerfile := readProtocolOracleFile(
		t,
		"../oracle/deviceplugin/Dockerfile",
	)
	for _, required := range []string{
		"golang:1.26.0-alpine3.23@sha256:d4c4845f5d60c6a974c6000ce58ae079328d03ab7f721a0734277e69905473e5",
		"FROM scratch",
		"CGO_ENABLED=0",
	} {
		if !strings.Contains(dockerfile, required) {
			t.Errorf("test oracle Dockerfile is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"nvidia",
		"amd",
		"vendor driver",
		"privileged",
	} {
		if strings.Contains(strings.ToLower(dockerfile), forbidden) {
			t.Errorf("test oracle Dockerfile contains forbidden dependency %q", forbidden)
		}
	}
}

func TestKubeletProtocolDependencyCannotEnterProductPackages(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	forbiddenImport := `"k8s.io/` + `kubelet/`
	err = filepath.WalkDir(root, func(
		path string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if relative == ".git" || relative == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" ||
			strings.HasPrefix(
				filepath.ToSlash(relative),
				"test/oracle/deviceplugin/",
			) {
			return nil
		}
		encoded, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(encoded), forbiddenImport) {
			t.Errorf("product-adjacent source %s imports the test-only kubelet API", relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"../../charts",
		"../../cmd",
		"../../internal",
	} {
		err := filepath.WalkDir(path, func(
			nestedPath string,
			entry fs.DirEntry,
			walkErr error,
		) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			encoded, err := os.ReadFile(nestedPath)
			if err != nil {
				return err
			}
			for _, forbidden := range []string{
				"oracle.kasim.io/accelerator",
				"kasim-oracle.sock",
				"kasim-device-plugin-oracle",
			} {
				if strings.Contains(string(encoded), forbidden) {
					t.Errorf(
						"product surface %s contains test oracle token %q",
						nestedPath,
						forbidden,
					)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func readProtocolOracleFile(t *testing.T, path string) string {
	t.Helper()

	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(encoded)
}
