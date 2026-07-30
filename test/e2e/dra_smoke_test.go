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

const defaultDRANodeImage = "kindest/node:v1.36.1@sha256:3489c7674813ba5d8b1a9977baea8a6e553784dab7b84759d1014dbd78f7ebd5"

// TestStableDRASchedulerAllocation is opt-in because it owns one disposable
// kind cluster. It proves stable DRA control-plane allocation, reservation,
// and Pod binding only; it deliberately does not require or claim a node-local
// DRA driver, NodePrepareResources, CDI, or device access.
func TestStableDRASchedulerAllocation(t *testing.T) {
	if os.Getenv("KASIM_E2E_DRA") != "1" {
		t.Skip("set KASIM_E2E_DRA=1 to run the disposable kind DRA test")
	}

	kindBinary := requiredBinary(t, "KIND_BIN", "kind")
	kubectlBinary := requiredBinary(t, "KUBECTL_BIN", "kubectl")
	nodeImage := os.Getenv("KASIM_KIND_NODE_IMAGE")
	if nodeImage == "" {
		nodeImage = defaultDRANodeImage
	}
	if !strings.Contains(nodeImage, "@sha256:") {
		t.Fatalf("KASIM_KIND_NODE_IMAGE must be pinned by digest: %q", nodeImage)
	}
	clusterName := fmt.Sprintf("kasim-dra-smoke-%d", os.Getpid())
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	run(t, ctx, kindBinary,
		"create", "cluster",
		"--name", clusterName,
		"--image", nodeImage,
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
			t.Logf("delete disposable DRA kind cluster: %v\n%s", err, output)
		}
	})

	discovery := kubeOutput(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"get",
		"--raw=/apis/resource.k8s.io/v1",
	)
	if !strings.Contains(discovery, `"groupVersion":"resource.k8s.io/v1"`) {
		t.Fatalf("stable DRA discovery returned an incompatible document: %s", discovery)
	}
	nodeName := kubeOutput(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"get",
		"nodes",
		"-o=jsonpath={.items[0].metadata.name}",
	)
	if nodeName == "" {
		t.Fatal("kind cluster returned no schedulable Node identity")
	}

	manifest := fmt.Sprintf(`apiVersion: resource.k8s.io/v1
kind: DeviceClass
metadata:
  name: kasim-dra-smoke
spec:
  selectors:
  - cel:
      expression: 'device.driver == "simulation.kasim.io" && device.attributes["simulation.kasim.io"].simulated == true && device.attributes["simulation.kasim.io"].allocatable == true'
---
apiVersion: resource.k8s.io/v1
kind: ResourceSlice
metadata:
  name: kasim-dra-smoke
spec:
  driver: simulation.kasim.io
  pool:
    name: kasim-dra-smoke
    generation: 1
    resourceSliceCount: 1
  nodeName: %s
  devices:
  - name: kasim-device-0
    attributes:
      simulation.kasim.io/simulated:
        bool: true
      simulation.kasim.io/allocatable:
        bool: true
---
apiVersion: resource.k8s.io/v1
kind: ResourceClaim
metadata:
  name: kasim-dra-smoke
spec:
  devices:
    requests:
    - name: accelerator
      exactly:
        deviceClassName: kasim-dra-smoke
        allocationMode: ExactCount
        count: 1
---
apiVersion: v1
kind: Pod
metadata:
  name: kasim-dra-smoke
spec:
  nodeSelector:
    kubernetes.io/hostname: %s
  tolerations:
  - key: node-role.kubernetes.io/control-plane
    operator: Exists
    effect: NoSchedule
  resourceClaims:
  - name: accelerator
    resourceClaimName: kasim-dra-smoke
  containers:
  - name: workload
    image: registry.k8s.io/pause:3.10
    resources:
      claims:
      - name: accelerator
`, nodeName, nodeName)
	kubectlInput(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		[]byte(manifest),
		"apply",
		"--server-side",
		"-f",
		"-",
	)

	waitFor(t, ctx, "stable DRA allocation and Pod binding", func() bool {
		return tryKubeOutput(
			ctx,
			kubectlBinary,
			kubeconfig,
			nodeName,
			"get",
			"pod/kasim-dra-smoke",
			"-o=jsonpath={.spec.nodeName}",
		) &&
			tryKubeOutput(
				ctx,
				kubectlBinary,
				kubeconfig,
				"kasim-device-0",
				"get",
				"resourceclaim/kasim-dra-smoke",
				"-o=jsonpath={.status.allocation.devices.results[0].device}",
			)
	})
	assertKubeOutput(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"simulation.kasim.io",
		"get",
		"resourceclaim/kasim-dra-smoke",
		"-o=jsonpath={.status.allocation.devices.results[0].driver}",
	)
	assertKubeOutput(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"kasim-dra-smoke",
		"get",
		"resourceclaim/kasim-dra-smoke",
		"-o=jsonpath={.status.allocation.devices.results[0].pool}",
	)
	podUID := kubeOutput(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"get",
		"pod/kasim-dra-smoke",
		"-o=jsonpath={.metadata.uid}",
	)
	if podUID == "" {
		t.Fatal("bound DRA Pod has no API-server UID")
	}
	assertKubeOutput(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		podUID,
		"get",
		"resourceclaim/kasim-dra-smoke",
		"-o=jsonpath={.status.reservedFor[0].uid}",
	)
}

func kubeOutput(
	t *testing.T,
	ctx context.Context,
	kubectlBinary,
	kubeconfig string,
	arguments ...string,
) string {
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
	return strings.TrimSpace(string(output))
}

func tryKubeOutput(
	ctx context.Context,
	kubectlBinary,
	kubeconfig,
	expected string,
	arguments ...string,
) bool {
	allArguments := append([]string{"--kubeconfig", kubeconfig}, arguments...)
	command := exec.CommandContext(ctx, kubectlBinary, allArguments...)
	output, err := command.CombinedOutput()
	return err == nil && strings.TrimSpace(string(output)) == expected
}
