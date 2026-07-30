package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	defaultProtocolOracleNodeImage = "ghcr.io/linkmaq/kube-accelerator-sim-kind-node:v1.36.3-kind-v0.32.0-amd64@sha256:91336f2737cf3ae7039c68945de957c66bad889e6db90bf5d3568293f1ab73db"
	protocolOracleNamespace        = "kasim-protocol-oracle"
	protocolOracleDaemonSet        = "kasim-device-plugin-oracle"
	protocolOracleResource         = "oracle.kasim.io/accelerator"
	protocolOracleSocket           = "/var/lib/kubelet/device-plugins/kasim-oracle.sock"
	protocolOracleWorkload         = "kasim-device-plugin-workload"
)

// TestKubeletDevicePluginProtocolOracle is a test-only real kubelet oracle.
// It does not extend product Fidelity Modes and never requires hardware,
// a vendor driver, or a privileged product controller.
func TestKubeletDevicePluginProtocolOracle(t *testing.T) {
	if os.Getenv("KASIM_E2E_PROTOCOL_ORACLE") != "1" {
		t.Skip("set KASIM_E2E_PROTOCOL_ORACLE=1 to run the disposable kubelet oracle")
	}

	startedAt := time.Now()
	kindBinary := requiredBinary(t, "KIND_BIN", "kind")
	kubectlBinary := requiredBinary(t, "KUBECTL_BIN", "kubectl")
	dockerBinary := requiredBinary(t, "DOCKER_BIN", "docker")
	oracleImage := os.Getenv("KASIM_ORACLE_IMAGE")
	if oracleImage == "" || strings.ContainsAny(oracleImage, " \t\r\n") {
		t.Fatalf("KASIM_ORACLE_IMAGE must name the exact locally built oracle image: %q", oracleImage)
	}
	oracleImageID := commandOutput(
		t,
		context.Background(),
		dockerBinary,
		"image",
		"inspect",
		"--format={{.Id}}",
		oracleImage,
	)
	if !strings.HasPrefix(oracleImageID, "sha256:") {
		t.Fatalf("oracle image ID = %q, want sha256 digest", oracleImageID)
	}
	if expected := os.Getenv("KASIM_ORACLE_IMAGE_ID"); expected != "" &&
		oracleImageID != expected {
		t.Fatalf("oracle image ID = %q, want %q", oracleImageID, expected)
	}
	nodeImage := os.Getenv("KASIM_KIND_NODE_IMAGE")
	if nodeImage == "" {
		nodeImage = defaultProtocolOracleNodeImage
	}
	if !strings.Contains(nodeImage, "@sha256:") {
		t.Fatalf("KASIM_KIND_NODE_IMAGE must be pinned by digest: %q", nodeImage)
	}

	clusterName := fmt.Sprintf("kasim-protocol-oracle-%d", os.Getpid())
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	run(
		t,
		ctx,
		kindBinary,
		"create",
		"cluster",
		"--name",
		clusterName,
		"--image",
		nodeImage,
		"--kubeconfig",
		kubeconfig,
		"--wait",
		"180s",
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
			"delete",
			"cluster",
			"--name",
			clusterName,
			"--kubeconfig",
			kubeconfig,
		)
		if output, err := command.CombinedOutput(); err != nil {
			t.Logf("delete protocol oracle kind cluster: %v\n%s", err, output)
		}
	})

	run(
		t,
		ctx,
		kindBinary,
		"load",
		"docker-image",
		"--name",
		clusterName,
		oracleImage,
	)
	serverVersionDocument := kubeOutput(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"get",
		"--raw=/version",
	)
	var serverVersion struct {
		GitVersion string `json:"gitVersion"`
	}
	if err := json.Unmarshal(
		[]byte(serverVersionDocument),
		&serverVersion,
	); err != nil {
		t.Fatalf("decode Kubernetes version: %v\n%s", err, serverVersionDocument)
	}
	if expected := os.Getenv("KASIM_KUBERNETES_PATCH"); expected != "" &&
		serverVersion.GitVersion != "v"+expected {
		t.Fatalf(
			"protocol oracle server version = %q, want v%s",
			serverVersion.GitVersion,
			expected,
		)
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
	kubeletVersion := kubeOutput(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"get",
		"node/"+nodeName,
		"-o=jsonpath={.status.nodeInfo.kubeletVersion}",
	)

	daemonSet := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
  labels:
    simulation.kasim.io/test-surface: protocol-oracle
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: %s
  namespace: %s
  labels:
    simulation.kasim.io/test-surface: protocol-oracle
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: kasim-device-plugin-oracle
  template:
    metadata:
      labels:
        app.kubernetes.io/name: kasim-device-plugin-oracle
        simulation.kasim.io/test-surface: protocol-oracle
    spec:
      terminationGracePeriodSeconds: 10
      tolerations:
      - key: node-role.kubernetes.io/control-plane
        operator: Exists
        effect: NoSchedule
      containers:
      - name: oracle
        image: %s
        imagePullPolicy: Never
        args:
        - serve
        - --devices=2
        securityContext:
          privileged: false
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          runAsUser: 0
          capabilities:
            drop:
            - ALL
        volumeMounts:
        - name: device-plugin
          mountPath: /var/lib/kubelet/device-plugins
      volumes:
      - name: device-plugin
        hostPath:
          path: /var/lib/kubelet/device-plugins
          type: Directory
`, protocolOracleNamespace, protocolOracleDaemonSet, protocolOracleNamespace, oracleImage)
	kubectlInput(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		[]byte(daemonSet),
		"apply",
		"--server-side",
		"--field-manager=kasim-protocol-oracle",
		"-f",
		"-",
	)
	runKube(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"rollout",
		"status",
		"--namespace="+protocolOracleNamespace,
		"daemonset/"+protocolOracleDaemonSet,
		"--timeout=120s",
	)
	pluginPod := waitForProtocolOraclePod(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"",
	)
	waitFor(t, ctx, "device-plugin registration", func() bool {
		return protocolOracleLogsContain(
			ctx,
			kubectlBinary,
			kubeconfig,
			pluginPod.name,
			`"event":"registration"`,
		)
	})
	waitForNodeResource(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		nodeName,
		"capacity",
		"2",
	)
	waitForNodeResource(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		nodeName,
		"allocatable",
		"2",
	)

	workload := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
  labels:
    simulation.kasim.io/test-surface: protocol-oracle
spec:
  nodeSelector:
    kubernetes.io/hostname: %s
  tolerations:
  - key: node-role.kubernetes.io/control-plane
    operator: Exists
    effect: NoSchedule
  containers:
  - name: workload
    image: %s
    imagePullPolicy: Never
    args:
    - hold
    resources:
      requests:
        %s: "1"
      limits:
        %s: "1"
`, protocolOracleWorkload, protocolOracleNamespace, nodeName, oracleImage, protocolOracleResource, protocolOracleResource)
	kubectlInput(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		[]byte(workload),
		"apply",
		"--server-side",
		"--field-manager=kasim-protocol-oracle",
		"-f",
		"-",
	)
	runKube(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"wait",
		"--namespace="+protocolOracleNamespace,
		"--for=condition=Ready",
		"pod/"+protocolOracleWorkload,
		"--timeout=120s",
	)
	assertKubeOutput(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		nodeName,
		"get",
		"--namespace="+protocolOracleNamespace,
		"pod/"+protocolOracleWorkload,
		"-o=jsonpath={.spec.nodeName}",
	)
	waitFor(t, ctx, "device-plugin allocation", func() bool {
		return protocolOracleLogsContain(
			ctx,
			kubectlBinary,
			kubeconfig,
			pluginPod.name,
			`"event":"allocation"`,
		)
	})

	runKube(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"exec",
		"--namespace="+protocolOracleNamespace,
		pluginPod.name,
		"--",
		"/kasim-device-plugin-oracle",
		"control",
		"--health=Unhealthy",
	)
	waitFor(t, ctx, "device-plugin health-transition log", func() bool {
		return protocolOracleLogsContain(
			ctx,
			kubectlBinary,
			kubeconfig,
			pluginPod.name,
			`"event":"health-transition","health":"Unhealthy"`,
		)
	})
	waitForNodeResource(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		nodeName,
		"capacity",
		"2",
	)
	waitForNodeResource(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		nodeName,
		"allocatable",
		"0",
	)
	runKube(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"exec",
		"--namespace="+protocolOracleNamespace,
		pluginPod.name,
		"--",
		"/kasim-device-plugin-oracle",
		"control",
		"--health=Healthy",
	)
	waitForNodeResource(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		nodeName,
		"allocatable",
		"2",
	)

	runKube(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"delete",
		"--namespace="+protocolOracleNamespace,
		"pod/"+pluginPod.name,
		"--wait=true",
		"--timeout=120s",
	)
	restartedPod := waitForProtocolOraclePod(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		pluginPod.uid,
	)
	waitFor(t, ctx, "device-plugin plugin-restart registration", func() bool {
		return protocolOracleLogsContain(
			ctx,
			kubectlBinary,
			kubeconfig,
			restartedPod.name,
			`"event":"registration"`,
		)
	})
	waitForNodeResource(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		nodeName,
		"capacity",
		"2",
	)
	waitForNodeResource(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		nodeName,
		"allocatable",
		"2",
	)

	runKube(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"delete",
		"--namespace="+protocolOracleNamespace,
		"pod/"+protocolOracleWorkload,
		"daemonset/"+protocolOracleDaemonSet,
		"--wait=true",
		"--timeout=120s",
	)
	if tryKube(
		ctx,
		kubectlBinary,
		kubeconfig,
		"get",
		"--namespace="+protocolOracleNamespace,
		"daemonset/"+protocolOracleDaemonSet,
	) {
		t.Fatal("daemonset-cleanup left the test-only DaemonSet")
	}
	if tryKube(
		ctx,
		kubectlBinary,
		kubeconfig,
		"get",
		"--namespace="+protocolOracleNamespace,
		"pod/"+protocolOracleWorkload,
	) {
		t.Fatal("pod-cleanup left the workload Pod")
	}
	waitFor(t, ctx, "device-plugin socket-cleanup", func() bool {
		command := exec.CommandContext(
			ctx,
			dockerBinary,
			"exec",
			clusterName+"-control-plane",
			"test",
			"!",
			"-S",
			protocolOracleSocket,
		)
		return command.Run() == nil
	})
	runKube(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"delete",
		"namespace/"+protocolOracleNamespace,
		"--wait=true",
		"--timeout=120s",
	)
	if tryKube(
		ctx,
		kubectlBinary,
		kubeconfig,
		"get",
		"namespace/"+protocolOracleNamespace,
	) {
		t.Fatal("test-owned resource cleanup left the oracle Namespace")
	}

	writeProtocolOracleReceipt(
		t,
		ctx,
		kindBinary,
		kubectlBinary,
		dockerBinary,
		serverVersion.GitVersion,
		kubeletVersion,
		nodeImage,
		oracleImage,
		oracleImageID,
		time.Since(startedAt),
	)
}

type protocolOraclePod struct {
	name string
	uid  string
}

func waitForProtocolOraclePod(
	t *testing.T,
	ctx context.Context,
	kubectlBinary,
	kubeconfig,
	previousUID string,
) protocolOraclePod {
	t.Helper()

	var found protocolOraclePod
	waitFor(t, ctx, "ready protocol oracle Pod", func() bool {
		document, err := protocolOracleCommandOutput(
			ctx,
			kubectlBinary,
			"--kubeconfig",
			kubeconfig,
			"get",
			"pods",
			"--namespace="+protocolOracleNamespace,
			"-l",
			"app.kubernetes.io/name=kasim-device-plugin-oracle",
			"-o=json",
		)
		if err != nil {
			return false
		}
		var list struct {
			Items []struct {
				Metadata struct {
					Name string `json:"name"`
					UID  string `json:"uid"`
				} `json:"metadata"`
				Status struct {
					Conditions []struct {
						Type   string `json:"type"`
						Status string `json:"status"`
					} `json:"conditions"`
				} `json:"status"`
			} `json:"items"`
		}
		if json.Unmarshal([]byte(document), &list) != nil {
			return false
		}
		for _, item := range list.Items {
			if item.Metadata.UID == previousUID {
				continue
			}
			for _, condition := range item.Status.Conditions {
				if condition.Type == "Ready" && condition.Status == "True" {
					found = protocolOraclePod{
						name: item.Metadata.Name,
						uid:  item.Metadata.UID,
					}
					return found.name != "" && found.uid != ""
				}
			}
		}
		return false
	})
	return found
}

func waitForNodeResource(
	t *testing.T,
	ctx context.Context,
	kubectlBinary,
	kubeconfig,
	nodeName,
	surface,
	expected string,
) {
	t.Helper()
	jsonPath := fmt.Sprintf(
		`-o=jsonpath={.status.%s.oracle\.kasim\.io/accelerator}`,
		surface,
	)
	waitFor(
		t,
		ctx,
		"node "+surface+" "+expected,
		func() bool {
			return tryKubeOutput(
				ctx,
				kubectlBinary,
				kubeconfig,
				expected,
				"get",
				"node/"+nodeName,
				jsonPath,
			)
		},
	)
}

func protocolOracleLogsContain(
	ctx context.Context,
	kubectlBinary,
	kubeconfig,
	podName,
	expected string,
) bool {
	output, err := protocolOracleCommandOutput(
		ctx,
		kubectlBinary,
		"--kubeconfig",
		kubeconfig,
		"logs",
		"--namespace="+protocolOracleNamespace,
		podName,
	)
	return err == nil && strings.Contains(output, expected)
}

func protocolOracleCommandOutput(
	ctx context.Context,
	binary string,
	arguments ...string,
) (string, error) {
	command := exec.CommandContext(ctx, binary, arguments...)
	output, err := command.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

func writeProtocolOracleReceipt(
	t *testing.T,
	ctx context.Context,
	kindBinary,
	kubectlBinary,
	dockerBinary,
	serverVersion,
	kubeletVersion,
	nodeImage,
	oracleImage,
	oracleImageID string,
	duration time.Duration,
) {
	t.Helper()

	path := os.Getenv("KASIM_PROTOCOL_ORACLE_RECEIPT")
	if path == "" {
		t.Log("KASIM_PROTOCOL_ORACLE_RECEIPT is unset; receipt was validated but not retained")
		return
	}
	dockerfile, err := os.ReadFile("../oracle/deviceplugin/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	dockerfileSum := sha256.Sum256(dockerfile)
	receipt := map[string]any{
		"schemaVersion":  "kasim.io/protocol-oracle-receipt/v1alpha1",
		"checkedAt":      time.Now().UTC().Format(time.RFC3339),
		"sourceRevision": commandOutput(t, ctx, "git", "rev-parse", "HEAD"),
		"evidenceClass":  "test-only-protocol-oracle",
		"protocol": map[string]any{
			"api":      "kubelet.deviceplugin.v1beta1",
			"resource": protocolOracleResource,
			"devices":  2,
		},
		"kubernetes": map[string]any{
			"serverVersion":       serverVersion,
			"kubeletVersion":      kubeletVersion,
			"nodeImage":           nodeImage,
			"imageClassification": os.Getenv("KASIM_NODE_IMAGE_CLASSIFICATION"),
		},
		"oracleImage": map[string]any{
			"name":             oracleImage,
			"imageID":          oracleImageID,
			"dockerfileSHA256": hex.EncodeToString(dockerfileSum[:]),
		},
		"harness": map[string]any{
			"kind":    commandOutput(t, ctx, kindBinary, "version"),
			"kubectl": commandOutput(t, ctx, kubectlBinary, "version", "--client=true"),
			"docker": commandOutput(
				t,
				ctx,
				dockerBinary,
				"version",
				"--format={{.Server.Version}}",
			),
			"host": runtime.GOOS + "/" + runtime.GOARCH,
		},
		"productClaims": map[string]any{
			"fidelityModes": []string{"scheduling", "dra-control-plane"},
			"oracleIsMode":  false,
			"excluded": []string{
				"physical accelerator hardware",
				"vendor driver",
				"accelerator computation",
				"host device mount",
				"CDI injection",
				"production node agent",
			},
		},
		"outcomes": map[string]any{
			"registration":      true,
			"capacity":          2,
			"allocatable":       2,
			"health-transition": true,
			"allocation":        true,
			"plugin-restart":    true,
			"socket-cleanup":    true,
			"daemonset-cleanup": true,
			"pod-cleanup":       true,
			"ownedLiveObjects":  0,
		},
		"durationSeconds": duration.Seconds(),
	}
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}
