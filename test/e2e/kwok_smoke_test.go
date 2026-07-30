package e2e_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LinkMaq/kube-accelerator-sim/internal/runtime/kwok"
)

const (
	kindNodeImage = "kindest/node:v1.36.1@sha256:3489c7674813ba5d8b1a9977baea8a6e553784dab7b84759d1014dbd78f7ebd5"
	smokeNodeName = "kasim-kwok-smoke-node"
	smokePodName  = "kasim-kwok-smoke-pod"
)

// TestPinnedKWOKSchedulesAndStartsPod is opt-in because it owns one disposable
// kind cluster. Product binaries never inherit this lifecycle behavior.
func TestPinnedKWOKSchedulesAndStartsPod(t *testing.T) {
	if os.Getenv("KASIM_E2E_KWOK") != "1" {
		t.Skip("set KASIM_E2E_KWOK=1 to run the disposable kind smoke test")
	}

	kindBinary := requiredBinary(t, "KIND_BIN", "kind")
	kubectlBinary := requiredBinary(t, "KUBECTL_BIN", "kubectl")
	clusterName := fmt.Sprintf("kasim-kwok-smoke-%d", os.Getpid())
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	run(t, ctx, kindBinary,
		"create", "cluster",
		"--name", clusterName,
		"--image", kindNodeImage,
		"--kubeconfig", kubeconfig,
		"--wait", "180s",
	)
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(
			context.Background(),
			2*time.Minute,
		)
		defer cleanupCancel()
		command := exec.CommandContext(
			cleanupContext,
			kindBinary,
			"delete", "cluster",
			"--name", clusterName,
			"--kubeconfig", kubeconfig,
		)
		if output, err := command.CombinedOutput(); err != nil {
			t.Logf("delete disposable kind cluster: %v\n%s", err, output)
		}
	})

	runtime := kwok.Pinned()
	lock := runtime.Lock()
	manifest := download(t, ctx, lock.ManifestURL, 4<<20)
	if err := kwok.VerifyAsset("kwok.yaml", manifest, lock.ManifestSHA256); err != nil {
		t.Fatal(err)
	}
	const taggedImage = "registry.k8s.io/kwok/kwok:v0.8.0"
	if bytes.Count(manifest, []byte(taggedImage)) != 1 {
		t.Fatal("pinned KWOK manifest no longer contains exactly one expected image tag")
	}
	manifest = bytes.ReplaceAll(manifest, []byte(taggedImage), []byte(lock.Image))
	kubectlInput(
		t, ctx, kubectlBinary, kubeconfig, manifest,
		"apply", "--server-side", "-f", "-",
	)
	runKube(
		t, ctx, kubectlBinary, kubeconfig,
		"wait", "--for=condition=Established",
		"crd/stages.kwok.x-k8s.io",
		"--timeout=120s",
	)
	stages, err := runtime.EmbeddedStages()
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.VerifyEmbeddedAssets(); err != nil {
		t.Fatal(err)
	}
	kubectlInput(
		t, ctx, kubectlBinary, kubeconfig, stages,
		"apply", "--server-side", "-f", "-",
	)
	runKube(
		t, ctx, kubectlBinary, kubeconfig,
		"wait", "--namespace=kube-system", "--for=condition=Available",
		"deployment/kwok-controller", "--timeout=180s",
	)

	contribution := runtime.NodeContribution()
	node := fmt.Sprintf(`apiVersion: v1
kind: Node
metadata:
  name: %s
  annotations:
    kwok.x-k8s.io/node: %s
  labels:
    kubernetes.io/hostname: %s
    simulation.kasim.io/scenario: kwok-smoke
spec:
  unschedulable: true
`, smokeNodeName, contribution.Annotations()["kwok.x-k8s.io/node"], smokeNodeName)
	kubectlInput(
		t, ctx, kubectlBinary, kubeconfig, []byte(node),
		"apply", "--server-side", "-f", "-",
	)
	runKube(
		t, ctx, kubectlBinary, kubeconfig,
		"wait", "--for=condition=Ready", "node/"+smokeNodeName, "--timeout=120s",
	)
	waitFor(
		t,
		ctx,
		"KWOK Node Lease",
		func() bool {
			return tryKube(
				ctx, kubectlBinary, kubeconfig,
				"get", "--namespace=kube-node-lease",
				"lease/"+smokeNodeName,
			)
		},
	)

	statusPatch := `{
  "status": {
    "capacity": {
      "cpu": "4",
      "memory": "16Gi",
      "pods": "110",
      "nvidia.com/gpu": "2"
    },
    "allocatable": {
      "cpu": "4",
      "memory": "16Gi",
      "pods": "110",
      "nvidia.com/gpu": "2"
    }
  }
}`
	runKube(
		t, ctx, kubectlBinary, kubeconfig,
		"patch", "node/"+smokeNodeName,
		"--subresource=status",
		"--type=merge",
		"--patch", statusPatch,
	)
	assertKubeOutput(
		t, ctx, kubectlBinary, kubeconfig,
		"2",
		"get", "node/"+smokeNodeName,
		"-o=jsonpath={.status.allocatable.nvidia\\.com/gpu}",
	)
	assertKubeOutput(
		t, ctx, kubectlBinary, kubeconfig,
		"true",
		"get", "node/"+smokeNodeName,
		"-o=jsonpath={.spec.unschedulable}",
	)

	runKube(
		t, ctx, kubectlBinary, kubeconfig,
		"patch", "node/"+smokeNodeName,
		"--type=merge",
		"--patch", `{"spec":{"unschedulable":false}}`,
	)
	pod := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
spec:
  nodeSelector:
    kubernetes.io/hostname: %s
  containers:
  - name: workload
    image: registry.k8s.io/pause:3.10
    resources:
      requests:
        nvidia.com/gpu: "1"
      limits:
        nvidia.com/gpu: "1"
`, smokePodName, smokeNodeName)
	kubectlInput(
		t, ctx, kubectlBinary, kubeconfig, []byte(pod),
		"apply", "--server-side", "-f", "-",
	)
	runKube(
		t, ctx, kubectlBinary, kubeconfig,
		"wait", "--for=condition=Ready", "pod/"+smokePodName, "--timeout=120s",
	)
	assertKubeOutput(
		t, ctx, kubectlBinary, kubeconfig,
		smokeNodeName,
		"get", "pod/"+smokePodName,
		"-o=jsonpath={.spec.nodeName}",
	)
	assertKubeOutput(
		t, ctx, kubectlBinary, kubeconfig,
		"Running",
		"get", "pod/"+smokePodName,
		"-o=jsonpath={.status.phase}",
	)
}

func requiredBinary(t *testing.T, environmentName, defaultName string) string {
	t.Helper()
	if configured := os.Getenv(environmentName); configured != "" {
		if info, err := os.Stat(configured); err != nil || info.IsDir() {
			t.Fatalf("%s does not name an executable file: %q", environmentName, configured)
		}
		return configured
	}
	path, err := exec.LookPath(defaultName)
	if err != nil {
		t.Fatalf("%s is required: %v", defaultName, err)
	}
	return path
}

func download(
	t *testing.T,
	ctx context.Context,
	url string,
	maximumBytes int64,
) []byte {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("download %s: HTTP %s", url, response.Status)
	}
	encoded, err := io.ReadAll(io.LimitReader(response.Body, maximumBytes+1))
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(encoded)) > maximumBytes {
		t.Fatalf("download %s exceeds %d bytes", url, maximumBytes)
	}
	return encoded
}

func run(t *testing.T, ctx context.Context, binary string, arguments ...string) {
	t.Helper()
	command := exec.CommandContext(ctx, binary, arguments...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", binary, strings.Join(arguments, " "), err, output)
	}
}

func runKube(
	t *testing.T,
	ctx context.Context,
	kubectlBinary,
	kubeconfig string,
	arguments ...string,
) {
	t.Helper()
	allArguments := append([]string{"--kubeconfig", kubeconfig}, arguments...)
	run(t, ctx, kubectlBinary, allArguments...)
}

func kubectlInput(
	t *testing.T,
	ctx context.Context,
	kubectlBinary,
	kubeconfig string,
	input []byte,
	arguments ...string,
) {
	t.Helper()
	allArguments := append([]string{"--kubeconfig", kubeconfig}, arguments...)
	command := exec.CommandContext(ctx, kubectlBinary, allArguments...)
	command.Stdin = bytes.NewReader(input)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf(
			"%s %s: %v\n%s",
			kubectlBinary,
			strings.Join(allArguments, " "),
			err,
			output,
		)
	}
}

func tryKube(
	ctx context.Context,
	kubectlBinary,
	kubeconfig string,
	arguments ...string,
) bool {
	allArguments := append([]string{"--kubeconfig", kubeconfig}, arguments...)
	command := exec.CommandContext(ctx, kubectlBinary, allArguments...)
	return command.Run() == nil
}

func waitFor(
	t *testing.T,
	ctx context.Context,
	description string,
	probe func() bool,
) {
	t.Helper()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if probe() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for %s: %v", description, ctx.Err())
		case <-ticker.C:
		}
	}
}

func assertKubeOutput(
	t *testing.T,
	ctx context.Context,
	kubectlBinary,
	kubeconfig,
	expected string,
	arguments ...string,
) {
	t.Helper()
	allArguments := append([]string{"--kubeconfig", kubeconfig}, arguments...)
	command := exec.CommandContext(ctx, kubectlBinary, allArguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf(
			"%s %s: %v\n%s",
			kubectlBinary,
			strings.Join(allArguments, " "),
			err,
			output,
		)
	}
	if strings.TrimSpace(string(output)) != expected {
		t.Fatalf(
			"%s output = %q, want %q",
			strings.Join(arguments, " "),
			strings.TrimSpace(string(output)),
			expected,
		)
	}
}
