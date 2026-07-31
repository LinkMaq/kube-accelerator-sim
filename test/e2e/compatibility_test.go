package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

const compatibilityReleaseName = "compat"

// TestCompatibilitySchedulingLifecycle owns one disposable kind cluster and
// exercises the product CLI against the installed runtime. Cluster lifecycle
// is deliberately confined to this opt-in End-to-End Test Harness.
func TestCompatibilitySchedulingLifecycle(t *testing.T) {
	if os.Getenv("KASIM_E2E_COMPATIBILITY") != "1" {
		t.Skip("set KASIM_E2E_COMPATIBILITY=1 to run the compatibility row")
	}

	startedAt := time.Now()
	kindBinary := requiredBinary(t, "KIND_BIN", "kind")
	kubectlBinary := requiredBinary(t, "KUBECTL_BIN", "kubectl")
	helmBinary := requiredBinary(t, "HELM_BIN", "helm")
	dockerBinary := requiredBinary(t, "DOCKER_BIN", "docker")
	cliBinary := requiredBinary(t, "KASIM_CLI_BIN", "kasim")
	nodeImage := os.Getenv("KASIM_KIND_NODE_IMAGE")
	if !strings.Contains(nodeImage, "@sha256:") {
		t.Fatalf("KASIM_KIND_NODE_IMAGE must be pinned by digest: %q", nodeImage)
	}
	expectedPatch := os.Getenv("KASIM_KUBERNETES_PATCH")
	if expectedPatch == "" {
		t.Fatal("KASIM_KUBERNETES_PATCH is required")
	}
	controllerImage := os.Getenv("KASIM_CONTROLLER_IMAGE")
	if controllerImage == "" {
		controllerImage = "kasim-controller:0.1.0"
	}
	chartPath := absolutePath(t, "../../charts/kasim-runtime")
	scenarioPath := absolutePath(t, "../../internal/cli/testdata/training-lab.yaml")
	clusterName := fmt.Sprintf(
		"kasim-compat-%s-%d",
		strings.ReplaceAll(expectedPatch, ".", "-"),
		os.Getpid(),
	)
	adminKubeconfig := filepath.Join(t.TempDir(), "admin-kubeconfig")
	adminContext := "kind-" + clusterName
	ctx, cancel := context.WithTimeout(context.Background(), 22*time.Minute)
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
		adminKubeconfig,
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
			adminKubeconfig,
		)
		if output, err := command.CombinedOutput(); err != nil {
			t.Logf("delete compatibility cluster: %v\n%s", err, output)
		}
	})

	serverVersion := kubeOutput(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		"get",
		"--raw=/version",
	)
	var versionDocument struct {
		GitVersion string `json:"gitVersion"`
	}
	if err := json.Unmarshal([]byte(serverVersion), &versionDocument); err != nil {
		t.Fatalf("decode Kubernetes version: %v\n%s", err, serverVersion)
	}
	if versionDocument.GitVersion != "v"+expectedPatch {
		t.Fatalf(
			"server version = %q, want v%s",
			versionDocument.GitVersion,
			expectedPatch,
		)
	}
	realNode := kubeOutput(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		"get",
		"nodes",
		"-l",
		"!app.kubernetes.io/managed-by,!simulation.kasim.io/instance-uid",
		"-o=jsonpath={.items[0].metadata.name}",
	)
	if realNode == "" {
		t.Fatal("disposable cluster returned no pre-existing real Node")
	}
	realNodeBefore := realNodeSafetySnapshot(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		realNode,
	)
	realLeaseBefore := realLeaseSafetySnapshot(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		realNode,
	)

	missingRuntime := runProductCLI(
		ctx,
		cliBinary,
		"apply",
		"-f",
		scenarioPath,
		"--async",
		"--kubeconfig",
		adminKubeconfig,
		"--context",
		adminContext,
		"-o",
		"json",
	)
	assertCLIError(t, missingRuntime, 3, "RuntimeUnavailable")

	installCompatibilityRuntime(
		t,
		ctx,
		kindBinary,
		kubectlBinary,
		helmBinary,
		dockerBinary,
		clusterName,
		adminKubeconfig,
		adminContext,
		chartPath,
		controllerImage,
	)
	assertScenarioCount(t, ctx, kubectlBinary, adminKubeconfig, 0)

	operatorKubeconfig, operatorContext := serviceAccountKubeconfig(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		adminContext,
		"compat-kasim-runtime-operator",
		"operator",
	)
	observerKubeconfig, observerContext := serviceAccountKubeconfig(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		adminContext,
		"compat-kasim-runtime-observer",
		"observer",
	)
	denied := runProductCLI(
		ctx,
		cliBinary,
		"apply",
		"-f",
		scenarioPath,
		"--async",
		"--kubeconfig",
		observerKubeconfig,
		"--context",
		observerContext,
		"-o",
		"json",
	)
	assertCLIError(t, denied, 3, "AuthorizationDenied")
	assertScenarioCount(t, ctx, kubectlBinary, adminKubeconfig, 0)

	serverDryRun := runProductCLI(
		ctx,
		cliBinary,
		"apply",
		"-f",
		scenarioPath,
		"--dry-run=server",
		"--kubeconfig",
		operatorKubeconfig,
		"--context",
		operatorContext,
		"-o",
		"json",
	)
	if serverDryRun.exitCode != 0 ||
		lifecycleReceiptField(t, serverDryRun.stdout, "revisionAccepted") != false {
		t.Fatalf("server dry-run failed: %#v", serverDryRun)
	}
	assertScenarioCount(t, ctx, kubectlBinary, adminKubeconfig, 0)

	admissionPolicy := `apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: kasim-compatibility-deny
spec:
  failurePolicy: Fail
  matchConstraints:
    resourceRules:
      - apiGroups: ["simulation.kasim.io"]
        apiVersions: ["v1alpha1"]
        operations: ["CREATE"]
        resources: ["scenarioinstances"]
  validations:
    - expression: "object.metadata.name != 'admission-denied'"
      message: "compatibility admission oracle denied this Scenario"
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicyBinding
metadata:
  name: kasim-compatibility-deny
spec:
  policyName: kasim-compatibility-deny
  validationActions: [Deny]
`
	kubectlInput(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		[]byte(admissionPolicy),
		"apply",
		"--server-side",
		"-f",
		"-",
	)
	admissionScenario := scenarioVariant(
		t,
		scenarioPath,
		t.TempDir(),
		"admission-denied",
	)
	waitFor(t, ctx, "ValidatingAdmissionPolicy enforcement", func() bool {
		result := runProductCLI(
			ctx,
			cliBinary,
			"apply",
			"-f",
			admissionScenario,
			"--dry-run=server",
			"--kubeconfig",
			operatorKubeconfig,
			"--context",
			operatorContext,
			"-o",
			"json",
		)
		return result.exitCode != 0 &&
			strings.Contains(result.stderr, "compatibility admission oracle")
	})
	assertScenarioCount(t, ctx, kubectlBinary, adminKubeconfig, 0)

	minor := parsePatchMinor(t, expectedPatch)
	if minor < 34 {
		draDenied := runProductCLI(
			ctx,
			cliBinary,
			"apply",
			"demo",
			"--profile",
			"nvidia",
			"--model",
			"nvidia-h100",
			"--contract",
			"dra",
			"--resource",
			"device",
			"--fidelity",
			"dra-control-plane",
			"--nodes",
			"1",
			"--accelerators-per-node",
			"1",
			"--async",
			"--kubeconfig",
			operatorKubeconfig,
			"--context",
			operatorContext,
			"-o",
			"json",
		)
		assertCLIError(t, draDenied, 3, "CapabilityUnavailable")
		assertScenarioCount(t, ctx, kubectlBinary, adminKubeconfig, 0)
	}

	exerciseOwnershipConflict(
		t,
		ctx,
		cliBinary,
		kubectlBinary,
		adminKubeconfig,
		operatorKubeconfig,
		operatorContext,
		scenarioPath,
	)
	assertScenarioCount(t, ctx, kubectlBinary, adminKubeconfig, 0)

	applied := runProductCLI(
		ctx,
		cliBinary,
		"apply",
		"-f",
		scenarioPath,
		"--kubeconfig",
		operatorKubeconfig,
		"--context",
		operatorContext,
		"--timeout=240s",
		"-o",
		"json",
	)
	if applied.exitCode != 0 || applied.stderr != "" {
		t.Fatalf("apply failed: %#v", applied)
	}
	instanceUID := lifecycleStringField(t, applied.stdout, "instanceUID")
	if lifecycleNumberField(t, applied.stdout, "desiredGeneration") != 1 ||
		lifecycleSnapshotString(t, applied.stdout, "phase") != "Ready" {
		t.Fatalf("apply returned an incomplete Ready receipt: %s", applied.stdout)
	}
	syntheticNode := kubeOutput(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		"get",
		"nodes",
		"-l",
		"simulation.kasim.io/instance-uid="+instanceUID,
		"-o=jsonpath={.items[0].metadata.name}",
	)
	if syntheticNode == "" || syntheticNode == realNode {
		t.Fatalf("invalid Synthetic Node identity %q", syntheticNode)
	}
	assertKubeOutput(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		"8",
		"get",
		"node/"+syntheticNode,
		"-o=jsonpath={.status.allocatable.nvidia\\.com/gpu}",
	)

	placementPod := schedulingPod(
		"compat-placement",
		instanceUID,
		"1",
	)
	kubectlInput(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		[]byte(placementPod),
		"apply",
		"--server-side",
		"-f",
		"-",
	)
	waitFor(t, ctx, "scheduler placement", func() bool {
		return tryKubeOutput(
			ctx,
			kubectlBinary,
			adminKubeconfig,
			syntheticNode,
			"get",
			"pod/compat-placement",
			"-o=jsonpath={.spec.nodeName}",
		) && tryKubeOutput(
			ctx,
			kubectlBinary,
			adminKubeconfig,
			"True",
			"get",
			"pod/compat-placement",
			"-o=jsonpath={.status.conditions[?(@.type==\"PodScheduled\")].status}",
		)
	})
	assertKubeOutput(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		syntheticNode,
		"get",
		"pod/compat-placement",
		"-o=jsonpath={.spec.nodeName}",
	)

	exhaustionPod := schedulingPod(
		"compat-exhaustion",
		instanceUID,
		"8",
	)
	kubectlInput(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		[]byte(exhaustionPod),
		"apply",
		"--server-side",
		"-f",
		"-",
	)
	waitFor(t, ctx, "scheduler resource exhaustion", func() bool {
		return tryKubeOutput(
			ctx,
			kubectlBinary,
			adminKubeconfig,
			"False",
			"get",
			"pod/compat-exhaustion",
			"-o=jsonpath={.status.conditions[?(@.type==\"PodScheduled\")].status}",
		) && tryKubeOutput(
			ctx,
			kubectlBinary,
			adminKubeconfig,
			"",
			"get",
			"pod/compat-exhaustion",
			"-o=jsonpath={.spec.nodeName}",
		)
	})

	health := runProductCLI(
		ctx,
		cliBinary,
		"health",
		"training-lab",
		"--group",
		"workers",
		"--pool",
		"training",
		"--healthy",
		"0",
		"--instance-uid",
		instanceUID,
		"--expected-generation",
		"1",
		"--kubeconfig",
		operatorKubeconfig,
		"--context",
		operatorContext,
		"--timeout=240s",
		"-o",
		"json",
	)
	if health.exitCode != 0 ||
		lifecycleNumberField(t, health.stdout, "desiredGeneration") != 2 ||
		!strings.Contains(health.stdout, `"type": "Overcommitted"`) {
		t.Fatalf("health overcommitment failed: %#v", health)
	}
	assertKubeOutput(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		"0",
		"get",
		"node/"+syntheticNode,
		"-o=jsonpath={.status.allocatable.nvidia\\.com/gpu}",
	)
	assertKubeOutput(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		"2",
		"get",
		"node/"+syntheticNode,
		"-o=jsonpath={.metadata.labels.simulation\\.kasim\\.io/desired-generation}",
	)
	assertKubeOutput(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		syntheticNode,
		"get",
		"pod/compat-placement",
		"-o=jsonpath={.spec.nodeName}",
	)

	scaled := runProductCLI(
		ctx,
		cliBinary,
		"scale",
		"training-lab",
		"--group",
		"workers",
		"--replicas",
		"2",
		"--instance-uid",
		instanceUID,
		"--expected-generation",
		"2",
		"--kubeconfig",
		operatorKubeconfig,
		"--context",
		operatorContext,
		"--timeout=240s",
		"-o",
		"json",
	)
	if scaled.exitCode != 0 ||
		lifecycleNumberField(t, scaled.stdout, "desiredGeneration") != 3 {
		t.Fatalf("scale failed: %#v", scaled)
	}
	if count := kubeListCount(
		ctx,
		kubectlBinary,
		adminKubeconfig,
		"nodes",
		"simulation.kasim.io/instance-uid="+instanceUID,
	); count != 2 {
		t.Fatalf("scaled Synthetic Node count = %d, want 2", count)
	}

	runKube(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		"delete",
		"pods",
		"--namespace=kasim-system",
		"--selector=app.kubernetes.io/component=controller",
	)
	runKube(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		"rollout",
		"status",
		"deployment/compat-kasim-runtime-controller",
		"--namespace=kasim-system",
		"--timeout=180s",
	)
	recovered := runProductCLI(
		ctx,
		cliBinary,
		"status",
		"training-lab",
		"--watch",
		"--kubeconfig",
		operatorKubeconfig,
		"--context",
		operatorContext,
		"--timeout=120s",
		"-o",
		"json",
	)
	if recovered.exitCode != 0 ||
		lifecycleStringField(t, recovered.stdout, "instanceUID") != instanceUID ||
		lifecycleNumberField(t, recovered.stdout, "desiredGeneration") != 3 {
		t.Fatalf("controller recovery lost identity: %#v", recovered)
	}

	repointedKubeconfig := filepath.Join(t.TempDir(), "repointed-kubeconfig")
	copyFile(t, operatorKubeconfig, repointedKubeconfig)
	run(
		t,
		ctx,
		kubectlBinary,
		"--kubeconfig",
		repointedKubeconfig,
		"config",
		"set-cluster",
		adminContext,
		"--server=https://127.0.0.1:1",
	)
	repointed := runProductCLI(
		ctx,
		cliBinary,
		"health",
		"training-lab",
		"--group",
		"workers",
		"--pool",
		"training",
		"--healthy",
		"1",
		"--instance-uid",
		instanceUID,
		"--expected-generation",
		"3",
		"--async",
		"--kubeconfig",
		repointedKubeconfig,
		"--context",
		operatorContext,
		"-o",
		"json",
	)
	assertCLIError(t, repointed, 3, "TargetUnavailable")
	assertKubeOutput(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		"3",
		"get",
		"scenarioinstance/training-lab",
		"-o=jsonpath={.spec.desiredGeneration}",
	)

	blockedDelete := runProductCLI(
		ctx,
		cliBinary,
		"delete",
		"training-lab",
		"--instance-uid",
		instanceUID,
		"--expected-generation",
		"3",
		"--kubeconfig",
		operatorKubeconfig,
		"--context",
		operatorContext,
		"--timeout=120s",
		"-o",
		"json",
	)
	if blockedDelete.exitCode != 5 ||
		!strings.Contains(blockedDelete.stderr, `"code": "CleanupBlocked"`) {
		t.Fatalf("foreign Pod did not block cleanup: %#v", blockedDelete)
	}
	runKube(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		"get",
		"pod/compat-placement",
	)
	runKube(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		"delete",
		"pod/compat-placement",
		"pod/compat-exhaustion",
		"--force",
		"--grace-period=0",
		"--wait=true",
		"--timeout=120s",
	)
	cleaned := runProductCLI(
		ctx,
		cliBinary,
		"delete",
		"training-lab",
		"--instance-uid",
		instanceUID,
		"--expected-generation",
		"3",
		"--kubeconfig",
		operatorKubeconfig,
		"--context",
		operatorContext,
		"--timeout=120s",
		"-o",
		"json",
	)
	if cleaned.exitCode != 0 {
		t.Fatalf("exact cleanup retry failed: %#v", cleaned)
	}
	waitFor(t, ctx, "exact owned object cleanup", func() bool {
		return !tryKube(
			ctx,
			kubectlBinary,
			adminKubeconfig,
			"get",
			"scenarioinstance/training-lab",
		) && kubeListCount(
			ctx,
			kubectlBinary,
			adminKubeconfig,
			"nodes",
			"simulation.kasim.io/instance-uid="+instanceUID,
		) == 0 && kubeListCount(
			ctx,
			kubectlBinary,
			adminKubeconfig,
			"leases",
			"simulation.kasim.io/instance-uid="+instanceUID,
			"--namespace=kube-node-lease",
		) == 0
	})

	realNodeAfter := realNodeSafetySnapshot(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		realNode,
	)
	realLeaseAfter := realLeaseSafetySnapshot(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		realNode,
	)
	if realNodeAfter != realNodeBefore {
		t.Fatalf(
			"pre-existing real Node safety fields changed:\nbefore=%s\nafter=%s",
			realNodeBefore,
			realNodeAfter,
		)
	}
	if realLeaseAfter != realLeaseBefore {
		t.Fatalf(
			"pre-existing real Node Lease safety fields changed:\nbefore=%s\nafter=%s",
			realLeaseBefore,
			realLeaseAfter,
		)
	}

	writeCompatibilityReceipt(
		t,
		ctx,
		kindBinary,
		kubectlBinary,
		helmBinary,
		dockerBinary,
		versionDocument.GitVersion,
		nodeImage,
		controllerImage,
		time.Since(startedAt),
	)
}

type productCLIResult struct {
	exitCode int
	stdout   string
	stderr   string
}

func runProductCLI(
	ctx context.Context,
	binary string,
	arguments ...string,
) productCLIResult {
	command := exec.CommandContext(ctx, binary, arguments...)
	var stdout strings.Builder
	var stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	exitCode := 0
	if err != nil {
		exitCode = -1
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		}
	}
	return productCLIResult{
		exitCode: exitCode,
		stdout:   stdout.String(),
		stderr:   stderr.String(),
	}
}

func assertCLIError(
	t *testing.T,
	result productCLIResult,
	exitCode int,
	code string,
) {
	t.Helper()
	if result.exitCode != exitCode ||
		!strings.Contains(result.stderr, `"code": "`+code+`"`) ||
		result.stdout != "" {
		t.Fatalf(
			"CLI error = exit %d stdout=%q stderr=%q, want exit %d code %s",
			result.exitCode,
			result.stdout,
			result.stderr,
			exitCode,
			code,
		)
	}
}

func lifecycleDocument(t *testing.T, encoded string) map[string]any {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal([]byte(encoded), &envelope); err != nil {
		t.Fatalf("decode lifecycle envelope: %v\n%s", err, encoded)
	}
	result, ok := envelope["result"].(map[string]any)
	if !ok {
		t.Fatalf("lifecycle result missing: %#v", envelope)
	}
	return result
}

func lifecycleReceiptField(t *testing.T, encoded, field string) any {
	t.Helper()
	result := lifecycleDocument(t, encoded)
	receipt, ok := result["receipt"].(map[string]any)
	if !ok {
		t.Fatalf("lifecycle receipt missing: %#v", result)
	}
	return receipt[field]
}

func lifecycleStringField(t *testing.T, encoded, field string) string {
	t.Helper()
	value, ok := lifecycleReceiptField(t, encoded, field).(string)
	if !ok || value == "" {
		t.Fatalf("lifecycle receipt field %s is not a non-empty string", field)
	}
	return value
}

func lifecycleNumberField(t *testing.T, encoded, field string) int {
	t.Helper()
	value, ok := lifecycleReceiptField(t, encoded, field).(float64)
	if !ok {
		t.Fatalf("lifecycle receipt field %s is not numeric", field)
	}
	return int(value)
}

func lifecycleSnapshotString(t *testing.T, encoded, field string) string {
	t.Helper()
	result := lifecycleDocument(t, encoded)
	snapshot, ok := result["snapshot"].(map[string]any)
	if !ok {
		t.Fatalf("lifecycle snapshot missing: %#v", result)
	}
	value, ok := snapshot[field].(string)
	if !ok {
		t.Fatalf("lifecycle snapshot field %s is not a string", field)
	}
	return value
}

func installCompatibilityRuntime(
	t *testing.T,
	ctx context.Context,
	kindBinary,
	kubectlBinary,
	helmBinary,
	dockerBinary,
	clusterName,
	kubeconfig,
	contextName,
	chartPath,
	controllerImage string,
	extraHelmArguments ...string,
) {
	t.Helper()
	pullImage(t, ctx, dockerBinary, chartKWOKImage)
	run(t, ctx, dockerBinary, "tag", chartKWOKImage, chartKWOKTestRepo)
	run(t, ctx, kindBinary, "load", "docker-image", controllerImage, "--name", clusterName)
	run(t, ctx, kindBinary, "load", "docker-image", chartKWOKTestRepo, "--name", clusterName)
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
	runKube(t, ctx, kubectlBinary, kubeconfig, "create", "namespace", "kasim-system")
	helmArguments := []string{
		"upgrade",
		"--install",
		compatibilityReleaseName,
		chartPath,
		"--namespace",
		"kasim-system",
		"--set",
		"controller.image.repository=kasim-controller",
		"--set",
		"controller.image.tag=0.1.0",
		"--set",
		"controller.image.pullPolicy=Never",
		"--set",
		"kwok.image.repository=" + chartKWOKTestRepo,
		"--set",
		"kwok.image.digest=" + chartKWOKAMD64Digest,
	}
	helmArguments = append(helmArguments, extraHelmArguments...)
	helmArguments = append(
		helmArguments,
		"--wait",
		"--timeout",
		"240s",
	)
	helmRuntime(
		t,
		ctx,
		helmBinary,
		kubeconfig,
		contextName,
		helmArguments...,
	)
	for _, deployment := range []string{
		"compat-kasim-runtime-controller",
		"compat-kasim-runtime-kwok-controller",
	} {
		runKube(
			t,
			ctx,
			kubectlBinary,
			kubeconfig,
			"rollout",
			"status",
			"deployment/"+deployment,
			"--namespace=kasim-system",
			"--timeout=180s",
		)
	}
}

func serviceAccountKubeconfig(
	t *testing.T,
	ctx context.Context,
	kubectlBinary,
	adminKubeconfig,
	clusterName,
	serviceAccount,
	persona string,
) (string, string) {
	t.Helper()
	token := kubeOutput(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		"create",
		"token",
		serviceAccount,
		"--namespace=kasim-system",
		"--duration=30m",
	)
	path := filepath.Join(t.TempDir(), persona+"-kubeconfig")
	copyFile(t, adminKubeconfig, path)
	contextName := "kasim-" + persona
	run(
		t,
		ctx,
		kubectlBinary,
		"--kubeconfig",
		path,
		"config",
		"set-credentials",
		contextName,
		"--token="+token,
	)
	run(
		t,
		ctx,
		kubectlBinary,
		"--kubeconfig",
		path,
		"config",
		"set-context",
		contextName,
		"--cluster="+clusterName,
		"--user="+contextName,
	)
	return path, contextName
}

func exerciseOwnershipConflict(
	t *testing.T,
	ctx context.Context,
	cliBinary,
	kubectlBinary,
	adminKubeconfig,
	operatorKubeconfig,
	operatorContext,
	scenarioPath string,
) {
	t.Helper()
	runKube(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		"scale",
		"deployment/compat-kasim-runtime-controller",
		"--namespace=kasim-system",
		"--replicas=0",
	)
	runKube(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		"rollout",
		"status",
		"deployment/compat-kasim-runtime-controller",
		"--namespace=kasim-system",
		"--timeout=120s",
	)
	variant := scenarioVariant(
		t,
		scenarioPath,
		t.TempDir(),
		"ownership-conflict",
	)
	accepted := runProductCLI(
		ctx,
		cliBinary,
		"apply",
		"-f",
		variant,
		"--async",
		"--kubeconfig",
		operatorKubeconfig,
		"--context",
		operatorContext,
		"-o",
		"json",
	)
	if accepted.exitCode != 0 {
		t.Fatalf("accept ownership-conflict fixture: %#v", accepted)
	}
	instanceUIDValue := lifecycleStringField(t, accepted.stdout, "instanceUID")
	instanceName, _ := domain.ParseName("ownership-conflict")
	instanceUID, _ := domain.ParseInstanceUID(instanceUIDValue)
	group, _ := domain.ParseName("workers")
	nodeName, err := domain.SyntheticNodeName(instanceName, instanceUID, group, 0)
	if err != nil {
		t.Fatal(err)
	}
	unownedNode := fmt.Sprintf(`apiVersion: v1
kind: Node
metadata:
  name: %s
  labels:
    user.example.com/owner: compatibility-oracle
`, nodeName.String())
	kubectlInput(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		[]byte(unownedNode),
		"create",
		"-f",
		"-",
	)
	unownedUID := kubeOutput(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		"get",
		"node/"+nodeName.String(),
		"-o=jsonpath={.metadata.uid}",
	)
	runKube(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		"scale",
		"deployment/compat-kasim-runtime-controller",
		"--namespace=kasim-system",
		"--replicas=1",
	)
	runKube(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		"rollout",
		"status",
		"deployment/compat-kasim-runtime-controller",
		"--namespace=kasim-system",
		"--timeout=180s",
	)
	waitFor(t, ctx, "unowned Node conflict", func() bool {
		return tryKubeOutput(
			ctx,
			kubectlBinary,
			adminKubeconfig,
			"OwnershipConflict",
			"get",
			"scenarioinstance/ownership-conflict",
			"-o=jsonpath={.status.diagnostics[0].code}",
		)
	})
	assertKubeOutput(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		unownedUID,
		"get",
		"node/"+nodeName.String(),
		"-o=jsonpath={.metadata.uid}",
	)
	assertKubeOutput(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		"",
		"get",
		"node/"+nodeName.String(),
		"-o=jsonpath={.metadata.labels.app\\.kubernetes\\.io/managed-by}",
	)
	runKube(
		t,
		ctx,
		kubectlBinary,
		adminKubeconfig,
		"delete",
		"node/"+nodeName.String(),
		"--wait=true",
	)
	revision := runProductCLI(
		ctx,
		cliBinary,
		"health",
		"ownership-conflict",
		"--group",
		"workers",
		"--pool",
		"training",
		"--healthy",
		"7",
		"--instance-uid",
		instanceUIDValue,
		"--expected-generation",
		"1",
		"--async",
		"--kubeconfig",
		operatorKubeconfig,
		"--context",
		operatorContext,
		"-o",
		"json",
	)
	if revision.exitCode != 0 ||
		lifecycleNumberField(t, revision.stdout, "desiredGeneration") != 2 {
		t.Fatalf("ownership recovery revision failed: %#v", revision)
	}
	waitFor(t, ctx, "ownership conflict recovery generation", func() bool {
		return tryKubeOutput(
			ctx,
			kubectlBinary,
			adminKubeconfig,
			"Ready|2",
			"get",
			"scenarioinstance/ownership-conflict",
			"-o=jsonpath={.status.phase}|{.status.observedGeneration}",
		)
	})
	ready := runProductCLI(
		ctx,
		cliBinary,
		"status",
		"ownership-conflict",
		"--kubeconfig",
		operatorKubeconfig,
		"--context",
		operatorContext,
		"--timeout=180s",
		"-o",
		"json",
	)
	if ready.exitCode != 0 ||
		lifecycleSnapshotString(t, ready.stdout, "phase") != "Ready" {
		t.Fatalf("ownership fixture did not recover: %#v", ready)
	}
	deleted := runProductCLI(
		ctx,
		cliBinary,
		"delete",
		"ownership-conflict",
		"--instance-uid",
		instanceUIDValue,
		"--expected-generation",
		"2",
		"--kubeconfig",
		operatorKubeconfig,
		"--context",
		operatorContext,
		"--timeout=120s",
		"-o",
		"json",
	)
	if deleted.exitCode != 0 {
		t.Fatalf("delete ownership fixture: %#v", deleted)
	}
}

func scenarioVariant(
	t *testing.T,
	sourcePath,
	directory,
	name string,
) string {
	t.Helper()
	encoded, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(
		string(encoded),
		"name: training-lab",
		"name: "+name,
		1,
	)
	if updated == string(encoded) {
		t.Fatal("Scenario fixture name marker not found")
	}
	target := filepath.Join(directory, name+".yaml")
	if err := os.WriteFile(target, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	return target
}

func schedulingPod(name, instanceUID, accelerators string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
spec:
  nodeSelector:
    simulation.kasim.io/instance-uid: %s
  containers:
    - name: workload
      image: registry.k8s.io/pause:3.10
      resources:
        requests:
          nvidia.com/gpu: %q
        limits:
          nvidia.com/gpu: %q
`, name, instanceUID, accelerators, accelerators)
}

func realNodeSafetySnapshot(
	t *testing.T,
	ctx context.Context,
	kubectlBinary,
	kubeconfig,
	name string,
) string {
	t.Helper()
	document := kubeOutput(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"get",
		"node/"+name,
		"-o=json",
	)
	var node struct {
		Metadata struct {
			UID    string            `json:"uid"`
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
		Spec struct {
			Unschedulable bool            `json:"unschedulable"`
			Taints        []realNodeTaint `json:"taints"`
		} `json:"spec"`
		Status struct {
			Capacity    map[string]string `json:"capacity"`
			Allocatable map[string]string `json:"allocatable"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(document), &node); err != nil {
		t.Fatalf("decode real Node safety snapshot: %v", err)
	}
	snapshot := struct {
		UID                    string          `json:"uid"`
		ManagedBy              string          `json:"managedBy"`
		InstanceUID            string          `json:"instanceUID"`
		Unschedulable          bool            `json:"unschedulable"`
		Taints                 []realNodeTaint `json:"taints"`
		AcceleratorCapacity    string          `json:"acceleratorCapacity"`
		AcceleratorAllocatable string          `json:"acceleratorAllocatable"`
	}{
		UID:                    node.Metadata.UID,
		ManagedBy:              node.Metadata.Labels["app.kubernetes.io/managed-by"],
		InstanceUID:            node.Metadata.Labels["simulation.kasim.io/instance-uid"],
		Unschedulable:          node.Spec.Unschedulable,
		Taints:                 stableRealNodeTaints(node.Spec.Taints),
		AcceleratorCapacity:    node.Status.Capacity["nvidia.com/gpu"],
		AcceleratorAllocatable: node.Status.Allocatable["nvidia.com/gpu"],
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("encode real Node safety snapshot: %v", err)
	}
	return string(encoded)
}

type realNodeTaint struct {
	Key       string `json:"key"`
	Value     string `json:"value,omitempty"`
	Effect    string `json:"effect"`
	TimeAdded string `json:"timeAdded,omitempty"`
}

func stableRealNodeTaints(taints []realNodeTaint) []realNodeTaint {
	kubernetesLifecycleTaints := map[string]struct{}{
		"node.kubernetes.io/not-ready":           {},
		"node.kubernetes.io/unreachable":         {},
		"node.kubernetes.io/memory-pressure":     {},
		"node.kubernetes.io/disk-pressure":       {},
		"node.kubernetes.io/pid-pressure":        {},
		"node.kubernetes.io/network-unavailable": {},
		"node.kubernetes.io/unschedulable":       {},
	}
	stable := make([]realNodeTaint, 0, len(taints))
	for _, taint := range taints {
		if _, dynamic := kubernetesLifecycleTaints[taint.Key]; dynamic {
			continue
		}
		stable = append(stable, taint)
	}
	sort.Slice(stable, func(left, right int) bool {
		if stable[left].Key != stable[right].Key {
			return stable[left].Key < stable[right].Key
		}
		if stable[left].Effect != stable[right].Effect {
			return stable[left].Effect < stable[right].Effect
		}
		return stable[left].Value < stable[right].Value
	})
	return stable
}

func TestStableRealNodeTaintsExcludeNodeLifecycleNoise(t *testing.T) {
	t.Parallel()

	taints := stableRealNodeTaints([]realNodeTaint{
		{
			Key:    "node.kubernetes.io/not-ready",
			Effect: "NoExecute",
		},
		{
			Key:    "node.kubernetes.io/unreachable",
			Effect: "NoExecute",
		},
		{
			Key:    "dedicated",
			Value:  "control-plane",
			Effect: "NoSchedule",
		},
	})

	if len(taints) != 1 {
		t.Fatalf("stable taints = %#v, want one user-managed taint", taints)
	}
	if taints[0].Key != "dedicated" ||
		taints[0].Value != "control-plane" ||
		taints[0].Effect != "NoSchedule" {
		t.Fatalf("stable taint = %#v, want dedicated control-plane", taints[0])
	}
}

func realLeaseSafetySnapshot(
	t *testing.T,
	ctx context.Context,
	kubectlBinary,
	kubeconfig,
	name string,
) string {
	t.Helper()
	return kubeOutput(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"get",
		"lease/"+name,
		"--namespace=kube-node-lease",
		"-o=jsonpath={.metadata.uid}|{.metadata.labels.app\\.kubernetes\\.io/managed-by}|{.metadata.labels.simulation\\.kasim\\.io/instance-uid}|{.spec.holderIdentity}",
	)
}

func assertScenarioCount(
	t *testing.T,
	ctx context.Context,
	kubectlBinary,
	kubeconfig string,
	expected int,
) {
	t.Helper()
	if actual := kubeListCount(
		ctx,
		kubectlBinary,
		kubeconfig,
		"scenarioinstances.simulation.kasim.io",
		"",
	); actual != expected {
		t.Fatalf("Scenario Instance count = %d, want %d", actual, expected)
	}
}

func kubeListCount(
	ctx context.Context,
	kubectlBinary,
	kubeconfig,
	resource,
	selector string,
	extra ...string,
) int {
	arguments := []string{
		"--kubeconfig",
		kubeconfig,
		"get",
		resource,
	}
	arguments = append(arguments, extra...)
	if selector != "" {
		arguments = append(arguments, "--selector="+selector)
	}
	arguments = append(arguments, "-o=json")
	command := exec.CommandContext(ctx, kubectlBinary, arguments...)
	output, err := command.Output()
	if err != nil {
		return -1
	}
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	if json.Unmarshal(output, &list) != nil {
		return -1
	}
	return len(list.Items)
}

func parsePatchMinor(t *testing.T, patch string) int {
	t.Helper()
	parts := strings.Split(patch, ".")
	if len(parts) != 3 || parts[0] != "1" {
		t.Fatalf("invalid Kubernetes patch %q", patch)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	return minor
}

func absolutePath(t *testing.T, path string) string {
	t.Helper()
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return absolute
}

func copyFile(t *testing.T, source, target string) {
	t.Helper()
	encoded, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeCompatibilityReceipt(
	t *testing.T,
	ctx context.Context,
	kindBinary,
	kubectlBinary,
	helmBinary,
	dockerBinary,
	serverVersion,
	nodeImage,
	controllerImage string,
	duration time.Duration,
) {
	t.Helper()
	path := os.Getenv("KASIM_COMPATIBILITY_RECEIPT")
	if path == "" {
		t.Log("KASIM_COMPATIBILITY_RECEIPT is unset; receipt was validated but not retained")
		return
	}
	inputs := loadJSONEvidence(t, "../../release/inputs.json")
	compatibilityLock := loadJSONEvidence(
		t,
		"../../release/compatibility-lock.json",
	)
	sourceRevision := commandOutput(t, ctx, "git", "rev-parse", "HEAD")
	receipt := map[string]any{
		"schemaVersion":     "kasim.io/compatibility-receipt/v1alpha1",
		"checkedAt":         time.Now().UTC().Format(time.RFC3339),
		"sourceRevision":    sourceRevision,
		"compatibilityLock": compatibilityLock,
		"kubernetes": map[string]any{
			"serverVersion":       serverVersion,
			"nodeImage":           nodeImage,
			"imageClassification": os.Getenv("KASIM_NODE_IMAGE_CLASSIFICATION"),
		},
		"fidelity": map[string]any{
			"tested": []string{"scheduling"},
			"excluded": []string{
				"physical hardware",
				"accelerator computation",
				"device-plugin gRPC",
				"CDI injection",
				"DRA node preparation",
			},
		},
		"harness": map[string]any{
			"kind":              commandOutput(t, ctx, kindBinary, "version"),
			"kubectl":           commandOutput(t, ctx, kubectlBinary, "version", "--client=true"),
			"helm":              commandOutput(t, ctx, helmBinary, "version", "--short"),
			"containerProvider": "docker",
			"containerVersion": commandOutput(
				t,
				ctx,
				dockerBinary,
				"version",
				"--format",
				"{{.Server.Version}}",
			),
			"host": runtime.GOOS + "/" + runtime.GOARCH,
		},
		"runtime": map[string]any{
			"controllerImage": controllerImage,
			"chart":           "kasim-runtime-0.1.0",
			"kwokImage":       chartKWOKTestRepo + "@" + chartKWOKAMD64Digest,
		},
		"releaseInputs": inputs,
		"outcomes": map[string]any{
			"runtimeMissingZeroWrite": true,
			"rbacDenialZeroWrite":     true,
			"serverDryRunZeroWrite":   true,
			"admissionZeroWrite":      true,
			"ownershipConflictSafe":   true,
			"placement":               true,
			"exhaustion":              true,
			"healthReduction":         true,
			"overcommitment":          true,
			"scale":                   true,
			"controllerRecovery":      true,
			"contextRepointClosed":    true,
			"foreignPodBlocked":       true,
			"realNodeUnchanged":       true,
			"realLeaseUnchanged":      true,
			"ownedLiveObjects":        0,
			"etcdFileShrinkClaimed":   false,
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

func writeDRACompatibilityReceipt(
	t *testing.T,
	ctx context.Context,
	kindBinary,
	kubectlBinary,
	serverVersion,
	nodeImage string,
) {
	t.Helper()
	path := os.Getenv("KASIM_DRA_RECEIPT")
	if path == "" {
		t.Log("KASIM_DRA_RECEIPT is unset; DRA receipt was not retained")
		return
	}
	receipt := map[string]any{
		"schemaVersion":     "kasim.io/compatibility-receipt/v1alpha1",
		"checkedAt":         time.Now().UTC().Format(time.RFC3339),
		"sourceRevision":    commandOutput(t, ctx, "git", "rev-parse", "HEAD"),
		"compatibilityLock": loadJSONEvidence(t, "../../release/compatibility-lock.json"),
		"kubernetes": map[string]any{
			"serverVersion":       serverVersion,
			"nodeImage":           nodeImage,
			"imageClassification": os.Getenv("KASIM_NODE_IMAGE_CLASSIFICATION"),
		},
		"fidelity": map[string]any{
			"tested":      []string{"dra-control-plane"},
			"resourceAPI": "resource.k8s.io/v1",
			"excluded": []string{
				"physical hardware",
				"accelerator computation",
				"NodePrepareResources",
				"NodeUnprepareResources",
				"CDI injection",
				"container device access",
			},
		},
		"harness": map[string]any{
			"kind":    commandOutput(t, ctx, kindBinary, "version"),
			"kubectl": commandOutput(t, ctx, kubectlBinary, "version", "--client=true"),
			"host":    runtime.GOOS + "/" + runtime.GOARCH,
		},
		"releaseInputs": loadJSONEvidence(t, "../../release/inputs.json"),
		"outcomes": map[string]any{
			"stableDiscovery":  true,
			"classSelection":   true,
			"sliceInventory":   true,
			"allocation":       true,
			"reservation":      true,
			"podBinding":       true,
			"deviceReuse":      true,
			"ownedLiveObjects": 0,
		},
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

func loadJSONEvidence(t *testing.T, path string) any {
	t.Helper()
	encoded, err := os.ReadFile(absolutePath(t, path))
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func commandOutput(
	t *testing.T,
	ctx context.Context,
	binary string,
	arguments ...string,
) string {
	t.Helper()
	command := exec.CommandContext(ctx, binary, arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", binary, strings.Join(arguments, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}
