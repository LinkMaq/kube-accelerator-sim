package e2e_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	chartKWOKAMD64Digest = "sha256:28ee38abba19bd0b89600b1b367c32480da45f1d954c80d14db5fc74feee83f2"
	chartKWOKImage       = "registry.k8s.io/kwok/kwok@" + chartKWOKAMD64Digest
	chartKWOKTestRepo    = "registry.k8s.io/kwok/kwok:kasim-test-amd64"
)

// TestRuntimeChartInstallUpgradeUninstall is opt-in because it builds and
// loads a product image and owns one disposable kind cluster. Cluster
// lifecycle remains test infrastructure and is not exposed by either product
// binary.
func TestRuntimeChartInstallUpgradeUninstall(t *testing.T) {
	if os.Getenv("KASIM_E2E_HELM") != "1" {
		t.Skip("set KASIM_E2E_HELM=1 to run the disposable chart smoke test")
	}

	kindBinary := requiredBinary(t, "KIND_BIN", "kind")
	kubectlBinary := requiredBinary(t, "KUBECTL_BIN", "kubectl")
	helmBinary := requiredBinary(t, "HELM_BIN", "helm")
	dockerBinary := requiredBinary(t, "DOCKER_BIN", "docker")
	image := os.Getenv("KASIM_CONTROLLER_IMAGE")
	if image == "" {
		image = "kasim-controller:0.3.0"
	}
	chartPath, err := filepath.Abs("../../charts/kasim-runtime")
	if err != nil {
		t.Fatal(err)
	}
	clusterName := fmt.Sprintf("kasim-chart-smoke-%d", os.Getpid())
	contextName := "kind-" + clusterName
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Minute)
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
			t.Logf("delete disposable chart cluster: %v\n%s", err, output)
		}
	})

	pullImage(t, ctx, dockerBinary, chartKWOKImage)
	run(t, ctx, dockerBinary, "tag", chartKWOKImage, chartKWOKTestRepo)
	run(t, ctx, kindBinary, "load", "docker-image", image, "--name", clusterName)
	run(
		t,
		ctx,
		kindBinary,
		"load",
		"docker-image",
		chartKWOKTestRepo,
		"--name",
		clusterName,
	)
	run(
		t,
		ctx,
		dockerBinary,
		"exec",
		clusterName+"-control-plane",
		"ctr",
		"--namespace=k8s.io",
		"images",
		"tag",
		chartKWOKTestRepo,
		chartKWOKTestRepo+"@"+chartKWOKAMD64Digest,
	)
	runKube(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"create",
		"namespace",
		"kasim-system",
	)
	helmRuntime(
		t,
		ctx,
		helmBinary,
		kubeconfig,
		contextName,
		"upgrade",
		"--install",
		"contract",
		chartPath,
		"--namespace",
		"kasim-system",
		"--set",
		"controller.image.repository=kasim-controller",
		"--set",
		"controller.image.tag=0.3.0",
		"--set",
		"controller.image.pullPolicy=Never",
		"--set",
		"kwok.image.repository="+chartKWOKTestRepo,
		"--set",
		"kwok.image.digest="+chartKWOKAMD64Digest,
		"--wait",
		"--timeout",
		"240s",
	)

	for _, deployment := range []string{
		"contract-kasim-runtime-controller",
		"contract-kasim-runtime-kwok-controller",
	} {
		runKube(
			t,
			ctx,
			kubectlBinary,
			kubeconfig,
			"rollout",
			"status",
			"--namespace=kasim-system",
			"deployment/"+deployment,
			"--timeout=180s",
		)
	}

	syntheticNode := `apiVersion: v1
kind: Node
metadata:
  name: kasim-chart-synthetic
  annotations:
    kwok.x-k8s.io/node: fake
  labels:
    app.kubernetes.io/managed-by: kube-accelerator-sim
    simulation.kasim.io/instance-uid: chart-smoke-uid
    kubernetes.io/hostname: kasim-chart-synthetic
`
	kubectlInput(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		[]byte(syntheticNode),
		"apply",
		"--server-side",
		"-f",
		"-",
	)
	runKube(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"wait",
		"--for=condition=Ready",
		"node/kasim-chart-synthetic",
		"--timeout=120s",
	)
	workload := `apiVersion: v1
kind: Pod
metadata:
  name: user-synthetic-workload
  namespace: default
spec:
  nodeName: kasim-chart-synthetic
  containers:
    - name: workload
      image: registry.k8s.io/pause:3.10
`
	kubectlInput(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		[]byte(workload),
		"apply",
		"--server-side",
		"-f",
		"-",
	)
	runKube(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"wait",
		"--for=condition=Ready",
		"pod/user-synthetic-workload",
		"--namespace=default",
		"--timeout=120s",
	)
	runKube(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"delete",
		"pods",
		"--namespace=kasim-system",
		"--selector=app.kubernetes.io/name=kasim-runtime",
	)
	for _, component := range []string{"controller", "kwok-controller"} {
		waitFor(t, ctx, component+" Pod restart on a real Node", func() bool {
			node := tryKubeValue(
				ctx,
				kubectlBinary,
				kubeconfig,
				"get",
				"pods",
				"--namespace=kasim-system",
				"--selector=app.kubernetes.io/component="+component,
				"-o=jsonpath={.items[0].spec.nodeName}",
			)
			return node != "" && node != "kasim-chart-synthetic"
		})
	}

	helmRuntime(
		t,
		ctx,
		helmBinary,
		kubeconfig,
		contextName,
		"upgrade",
		"contract",
		chartPath,
		"--namespace",
		"kasim-system",
		"--reuse-values",
		"--set",
		"controller.maxConcurrentReconciles=4",
		"--wait",
		"--timeout",
		"240s",
	)
	assertKubeOutput(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"--max-concurrent-reconciles=4",
		"get",
		"deployment/contract-kasim-runtime-controller",
		"--namespace=kasim-system",
		"-o=jsonpath={.spec.template.spec.containers[0].args[2]}",
	)

	runKube(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"annotate",
		"crd/stages.kwok.x-k8s.io",
		"simulation.kasim.io/ownership-root=incompatible/v9",
		"--overwrite",
	)
	command := exec.CommandContext(
		ctx,
		helmBinary,
		"--kubeconfig",
		kubeconfig,
		"--kube-context",
		contextName,
		"upgrade",
		"contract",
		chartPath,
		"--namespace",
		"kasim-system",
		"--reuse-values",
	)
	if output, err := command.CombinedOutput(); err == nil ||
		!strings.Contains(string(output), "refusing to adopt incompatible") {
		t.Fatalf(
			"incompatible ownership root did not fail closed: %v\n%s",
			err,
			output,
		)
	}
	runKube(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"annotate",
		"crd/stages.kwok.x-k8s.io",
		"simulation.kasim.io/ownership-root=kasim-runtime/v1alpha1",
		"--overwrite",
	)

	unrelated := `apiVersion: v1
kind: ConfigMap
metadata:
  name: user-owned
  namespace: default
data:
  keep: me
---
apiVersion: kwok.x-k8s.io/v1alpha1
kind: Stage
metadata:
  name: user-owned
spec:
  resourceRef:
    apiGroup: v1
    kind: Pod
`
	kubectlInput(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		[]byte(unrelated),
		"apply",
		"--server-side",
		"-f",
		"-",
	)
	helmRuntime(
		t,
		ctx,
		helmBinary,
		kubeconfig,
		contextName,
		"uninstall",
		"contract",
		"--namespace",
		"kasim-system",
		"--wait",
		"--timeout",
		"180s",
	)
	for _, object := range []string{
		"configmap/user-owned",
		"pod/user-synthetic-workload",
		"stage.kwok.x-k8s.io/user-owned",
		"crd/scenarioinstances.simulation.kasim.io",
		"crd/stages.kwok.x-k8s.io",
	} {
		runKube(t, ctx, kubectlBinary, kubeconfig, "get", object)
	}
	if tryKube(
		ctx,
		kubectlBinary,
		kubeconfig,
		"get",
		"deployment/contract-kasim-runtime-controller",
		"--namespace=kasim-system",
	) {
		t.Fatal("chart-owned controller Deployment survived uninstall")
	}
}

func pullImage(
	t *testing.T,
	ctx context.Context,
	dockerBinary,
	image string,
) {
	t.Helper()
	var lastOutput []byte
	var lastError error
	for attempt := 1; attempt <= 3; attempt++ {
		command := exec.CommandContext(ctx, dockerBinary, "pull", image)
		lastOutput, lastError = command.CombinedOutput()
		if lastError == nil {
			return
		}
		t.Logf(
			"pull pinned image attempt %d failed: %v\n%s",
			attempt,
			lastError,
			lastOutput,
		)
	}
	t.Fatalf("pull pinned image %s: %v\n%s", image, lastError, lastOutput)
}

func helmRuntime(
	t *testing.T,
	ctx context.Context,
	helmBinary,
	kubeconfig,
	contextName string,
	arguments ...string,
) {
	t.Helper()
	allArguments := append(
		[]string{
			"--kubeconfig",
			kubeconfig,
			"--kube-context",
			contextName,
		},
		arguments...,
	)
	run(t, ctx, helmBinary, allArguments...)
}

func tryKubeValue(
	ctx context.Context,
	kubectlBinary,
	kubeconfig string,
	arguments ...string,
) string {
	allArguments := append([]string{"--kubeconfig", kubeconfig}, arguments...)
	command := exec.CommandContext(ctx, kubectlBinary, allArguments...)
	output, err := command.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
