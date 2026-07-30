package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	scaleAccelerators          = 8000
	scaleAcceleratorsPerNode   = 8
	scaleApplyReadyLimit       = 180 * time.Second
	scaleCleanupLimit          = 120 * time.Second
	scaleControllerLimit       = 120 * time.Second
	scaleHealthLimit           = 15 * time.Second
	scaleHealthSample          = 100
	scaleInstanceName          = "reference-scale"
	scaleMaximumMemory         = 2 << 30
	scaleNodeCount             = 1000
	scaleObservationLimit      = 2 * time.Second
	scaleObservationSamples    = 10
	scaleRepresentativePods    = 100
	scaleWorkloadLimit         = 60 * time.Second
	scaleWorkloadNamespace     = "kasim-scale-workloads"
	scaleWorkloadSelectorLabel = "simulation.kasim.io/scale-workload"
)

// TestReferenceScaleGate performs two consecutive release-only trials against
// the real product CLI, controller, chart, KWOK runtime, scheduler, and API
// server. Cluster lifecycle remains confined to this End-to-End Test Harness.
func TestReferenceScaleGate(t *testing.T) {
	if os.Getenv("KASIM_E2E_SCALE") != "1" {
		t.Skip("set KASIM_E2E_SCALE=1 to run the release reference scale gate")
	}
	trials, err := strconv.Atoi(os.Getenv("KASIM_SCALE_TRIALS"))
	if err != nil || trials != 2 {
		t.Fatalf("KASIM_SCALE_TRIALS must be exactly 2, got %q", os.Getenv("KASIM_SCALE_TRIALS"))
	}
	receiptDirectory := os.Getenv("KASIM_SCALE_RECEIPT_DIR")
	if receiptDirectory == "" {
		t.Fatal("KASIM_SCALE_RECEIPT_DIR is required")
	}

	tools := scaleTools{
		kind:    requiredBinary(t, "KIND_BIN", "kind"),
		kubectl: requiredBinary(t, "KUBECTL_BIN", "kubectl"),
		helm:    requiredBinary(t, "HELM_BIN", "helm"),
		docker:  requiredBinary(t, "DOCKER_BIN", "docker"),
		cli:     requiredBinary(t, "KASIM_CLI_BIN", "kasim"),
	}
	environment := inspectScaleEnvironment(t, tools.docker)
	controllerImage := os.Getenv("KASIM_CONTROLLER_IMAGE")
	if controllerImage == "" {
		t.Fatal("KASIM_CONTROLLER_IMAGE is required")
	}
	nodeImage := os.Getenv("KASIM_KIND_NODE_IMAGE")
	if !strings.Contains(nodeImage, "@sha256:") {
		t.Fatalf("KASIM_KIND_NODE_IMAGE must be pinned by digest: %q", nodeImage)
	}
	if expected := os.Getenv("KASIM_KUBERNETES_PATCH"); expected != "1.36.3" {
		t.Fatalf("scale gate requires the frozen 1.36.3 ceiling, got %q", expected)
	}

	for trial := 1; trial <= trials; trial++ {
		trial := trial
		t.Run(fmt.Sprintf("trial-%d", trial), func(t *testing.T) {
			runReferenceScaleTrial(
				t,
				trial,
				receiptDirectory,
				tools,
				environment,
				controllerImage,
				nodeImage,
			)
		})
	}
}

type scaleTools struct {
	kind    string
	kubectl string
	helm    string
	docker  string
	cli     string
}

type scaleEnvironment struct {
	DockerCPUs        int    `json:"dockerCPUs"`
	DockerMemoryBytes int64  `json:"dockerMemoryBytes"`
	DockerDriver      string `json:"dockerDriver"`
	DockerOS          string `json:"dockerOS"`
	DockerVersion     string `json:"dockerVersion"`
	Host              string `json:"host"`
}

func inspectScaleEnvironment(t *testing.T, dockerBinary string) scaleEnvironment {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cpus, err := strconv.Atoi(commandOutput(
		t,
		ctx,
		dockerBinary,
		"info",
		"--format={{.NCPU}}",
	))
	if err != nil {
		t.Fatal(err)
	}
	memoryBytes, err := strconv.ParseInt(commandOutput(
		t,
		ctx,
		dockerBinary,
		"info",
		"--format={{.MemTotal}}",
	), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if cpus < 4 {
		t.Fatalf("scale gate requires at least four Docker CPUs, found %d", cpus)
	}
	if memoryBytes < 8<<30 {
		t.Fatalf(
			"scale gate requires at least eight GiB Docker memory, found %d bytes",
			memoryBytes,
		)
	}
	return scaleEnvironment{
		DockerCPUs:        cpus,
		DockerMemoryBytes: memoryBytes,
		DockerDriver: commandOutput(
			t,
			ctx,
			dockerBinary,
			"info",
			"--format={{.Driver}}",
		),
		DockerOS: commandOutput(
			t,
			ctx,
			dockerBinary,
			"info",
			"--format={{.OperatingSystem}}",
		),
		DockerVersion: commandOutput(
			t,
			ctx,
			dockerBinary,
			"version",
			"--format={{.Server.Version}}",
		),
		Host: runtime.GOOS + "/" + runtime.GOARCH,
	}
}

type scaleMeasurements struct {
	ApplyReadySeconds              float64 `json:"applyReadySeconds"`
	ObservationP95Seconds          float64 `json:"observationP95Seconds"`
	HealthLossSeconds              float64 `json:"healthLossSeconds"`
	HealthRecoverySeconds          float64 `json:"healthRecoverySeconds"`
	WorkloadSeconds                float64 `json:"workloadSeconds"`
	ControllerRecoverySeconds      float64 `json:"controllerRecoverySeconds"`
	ControllerHealthRestoreSeconds float64 `json:"controllerHealthRestoreSeconds"`
	CleanupSeconds                 float64 `json:"cleanupSeconds"`
	ControlPlanePeakBytes          uint64  `json:"controlPlanePeakBytes"`
	ControlPlaneMemorySamples      int     `json:"controlPlaneMemorySamples"`
	EtcdBytesBefore                int64   `json:"etcdBytesBefore"`
	EtcdBytesAfter                 int64   `json:"etcdBytesAfter"`
}

func runReferenceScaleTrial(
	t *testing.T,
	trial int,
	receiptDirectory string,
	tools scaleTools,
	environment scaleEnvironment,
	controllerImage,
	nodeImage string,
) {
	t.Helper()
	startedAt := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	clusterName := fmt.Sprintf("kasim-scale-%d-%d", trial, os.Getpid())
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	contextName := "kind-" + clusterName

	run(
		t,
		ctx,
		tools.kind,
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
			tools.kind,
			"delete",
			"cluster",
			"--name",
			clusterName,
			"--kubeconfig",
			kubeconfig,
		)
		if output, err := command.CombinedOutput(); err != nil {
			t.Logf("delete reference scale cluster: %v\n%s", err, output)
		}
	})

	serverVersion := scaleServerVersion(t, ctx, tools.kubectl, kubeconfig)
	if serverVersion != "v1.36.3" {
		t.Fatalf("scale Kubernetes server = %q, want v1.36.3", serverVersion)
	}
	nodeName := kubeOutput(
		t,
		ctx,
		tools.kubectl,
		kubeconfig,
		"get",
		"nodes",
		"-o=jsonpath={.items[0].metadata.name}",
	)
	kubeletVersion := kubeOutput(
		t,
		ctx,
		tools.kubectl,
		kubeconfig,
		"get",
		"node/"+nodeName,
		"-o=jsonpath={.status.nodeInfo.kubeletVersion}",
	)
	sampler := startScaleMemorySampler(
		ctx,
		tools.docker,
		clusterName+"-control-plane",
	)
	t.Cleanup(func() {
		sampler.stop()
	})
	etcdBefore := scaleEtcdBytes(t, ctx, tools.docker, clusterName)

	installCompatibilityRuntime(
		t,
		ctx,
		tools.kind,
		tools.kubectl,
		tools.helm,
		tools.docker,
		clusterName,
		kubeconfig,
		contextName,
		absolutePath(t, "../../charts/kasim-runtime"),
		controllerImage,
		"--set",
		"controller.resources.requests.cpu=100m",
		"--set",
		"controller.resources.requests.memory=128Mi",
		"--set",
		"controller.resources.limits.cpu=2",
		"--set",
		"controller.resources.limits.memory=2Gi",
		"--set",
		"kwok.resources.limits.cpu=2",
		"--set",
		"kwok.resources.limits.memory=2Gi",
	)
	operatorKubeconfig, operatorContext := serviceAccountKubeconfig(
		t,
		ctx,
		tools.kubectl,
		kubeconfig,
		contextName,
		"compat-kasim-runtime-operator",
		"scale-operator",
	)
	scenarioPath := absolutePath(t, "testdata/reference-scale.yaml")
	measurements := scaleMeasurements{EtcdBytesBefore: etcdBefore}

	applyStarted := time.Now()
	applied := runProductCLI(
		ctx,
		tools.cli,
		"apply",
		"-f",
		scenarioPath,
		"--kubeconfig",
		operatorKubeconfig,
		"--context",
		operatorContext,
		"--timeout=180s",
		"-o",
		"json",
	)
	measurements.ApplyReadySeconds = time.Since(applyStarted).Seconds()
	requireScaleCLI(
		t,
		applied,
		"applyReady",
		1,
		scaleInstanceName,
	)
	requireScaleDuration(
		t,
		"applyReady",
		measurements.ApplyReadySeconds,
		scaleApplyReadyLimit,
	)
	instanceUID := lifecycleStringField(t, applied.stdout, "instanceUID")

	inventory := observeScaleInventory(
		t,
		ctx,
		tools.kubectl,
		kubeconfig,
		instanceUID,
	)
	requireScaleInventory(t, inventory, scaleNodeCount, scaleAccelerators, scaleAccelerators)
	identityBefore := inventory.IdentityDigest

	statusDurations := make([]time.Duration, 0, scaleObservationSamples)
	for sample := 0; sample < scaleObservationSamples; sample++ {
		statusStarted := time.Now()
		status := runProductCLI(
			ctx,
			tools.cli,
			"status",
			scaleInstanceName,
			"--kubeconfig",
			operatorKubeconfig,
			"--context",
			operatorContext,
			"-o",
			"json",
		)
		statusDurations = append(statusDurations, time.Since(statusStarted))
		requireScaleCLI(t, status, "observation", 1, scaleInstanceName)
		if lifecycleStringField(t, status.stdout, "instanceUID") != instanceUID {
			t.Fatal("status observation returned identity drift")
		}
	}
	sort.Slice(statusDurations, func(i, j int) bool {
		return statusDurations[i] < statusDurations[j]
	})
	p95Index := int(math.Ceil(0.95*float64(len(statusDurations)))) - 1
	measurements.ObservationP95Seconds = statusDurations[p95Index].Seconds()
	requireScaleDuration(
		t,
		"observationP95",
		measurements.ObservationP95Seconds,
		scaleObservationLimit,
	)

	healthLossStarted := time.Now()
	healthLoss := runScaleHealth(
		ctx,
		tools.cli,
		operatorKubeconfig,
		operatorContext,
		instanceUID,
		1,
		0,
		scaleHealthLimit,
		false,
	)
	measurements.HealthLossSeconds = time.Since(healthLossStarted).Seconds()
	requireScaleCLI(t, healthLoss, "healthLoss", 2, scaleInstanceName)
	requireScaleDuration(
		t,
		"healthLoss",
		measurements.HealthLossSeconds,
		scaleHealthLimit,
	)
	inventory = observeScaleInventory(
		t,
		ctx,
		tools.kubectl,
		kubeconfig,
		instanceUID,
	)
	requireScaleInventory(
		t,
		inventory,
		scaleNodeCount,
		scaleAccelerators,
		scaleAccelerators-(scaleHealthSample*scaleAcceleratorsPerNode),
	)
	if inventory.UnhealthySampleNodes != scaleHealthSample {
		t.Fatalf(
			"healthLoss changed %d sample Nodes, want %d",
			inventory.UnhealthySampleNodes,
			scaleHealthSample,
		)
	}

	healthRecoveryStarted := time.Now()
	healthRecovery := runScaleHealth(
		ctx,
		tools.cli,
		operatorKubeconfig,
		operatorContext,
		instanceUID,
		2,
		scaleAcceleratorsPerNode,
		scaleHealthLimit,
		false,
	)
	measurements.HealthRecoverySeconds = time.Since(healthRecoveryStarted).Seconds()
	requireScaleCLI(t, healthRecovery, "healthRecovery", 3, scaleInstanceName)
	requireScaleDuration(
		t,
		"healthRecovery",
		measurements.HealthRecoverySeconds,
		scaleHealthLimit,
	)

	workloadStarted := time.Now()
	runKube(
		t,
		ctx,
		tools.kubectl,
		kubeconfig,
		"create",
		"namespace",
		scaleWorkloadNamespace,
	)
	kubectlInput(
		t,
		ctx,
		tools.kubectl,
		kubeconfig,
		[]byte(scaleWorkloadManifest(instanceUID)),
		"apply",
		"--server-side",
		"--field-manager=kasim-scale-gate",
		"-f",
		"-",
	)
	waitForScaleCondition(
		t,
		ctx,
		scaleWorkloadLimit,
		"100-Pod workload",
		func() bool {
			pods := observeScalePods(
				ctx,
				tools.kubectl,
				kubeconfig,
			)
			return pods.Total == scaleRepresentativePods &&
				pods.Running == scaleRepresentativePods &&
				pods.ScheduledNodes == scaleRepresentativePods
		},
	)
	measurements.WorkloadSeconds = time.Since(workloadStarted).Seconds()
	requireScaleDuration(
		t,
		"workload",
		measurements.WorkloadSeconds,
		scaleWorkloadLimit,
	)
	runKube(
		t,
		ctx,
		tools.kubectl,
		kubeconfig,
		"delete",
		"pods",
		"--namespace="+scaleWorkloadNamespace,
		"--selector="+scaleWorkloadSelectorLabel+"=true",
		"--force",
		"--grace-period=0",
		"--wait=true",
		"--timeout=120s",
	)

	controllerLogsBefore := scaleControllerLogs(
		t,
		ctx,
		tools.kubectl,
		kubeconfig,
	)
	assertNoScaleControllerErrors(t, controllerLogsBefore)
	runKube(
		t,
		ctx,
		tools.kubectl,
		kubeconfig,
		"scale",
		"deployment/compat-kasim-runtime-controller",
		"--namespace=kasim-system",
		"--replicas=0",
	)
	runKube(
		t,
		ctx,
		tools.kubectl,
		kubeconfig,
		"wait",
		"--namespace=kasim-system",
		"--for=delete",
		"pod",
		"--selector=app.kubernetes.io/component=controller",
		"--timeout=120s",
	)
	queuedHealth := runScaleHealth(
		ctx,
		tools.cli,
		operatorKubeconfig,
		operatorContext,
		instanceUID,
		3,
		7,
		scaleControllerLimit,
		true,
	)
	requireScaleCLI(t, queuedHealth, "controllerRecoveryQueuedRevision", 4, "")

	controllerStarted := time.Now()
	runKube(
		t,
		ctx,
		tools.kubectl,
		kubeconfig,
		"scale",
		"deployment/compat-kasim-runtime-controller",
		"--namespace=kasim-system",
		"--replicas=1",
	)
	runKube(
		t,
		ctx,
		tools.kubectl,
		kubeconfig,
		"rollout",
		"status",
		"deployment/compat-kasim-runtime-controller",
		"--namespace=kasim-system",
		"--timeout=120s",
	)
	recovered := runProductCLI(
		ctx,
		tools.cli,
		"status",
		scaleInstanceName,
		"--watch",
		"--kubeconfig",
		operatorKubeconfig,
		"--context",
		operatorContext,
		"--timeout=120s",
		"-o",
		"json",
	)
	measurements.ControllerRecoverySeconds = time.Since(controllerStarted).Seconds()
	requireScaleCLI(t, recovered, "controllerRecovery", 4, scaleInstanceName)
	requireScaleDuration(
		t,
		"controllerRecovery",
		measurements.ControllerRecoverySeconds,
		scaleControllerLimit,
	)

	controllerRestoreStarted := time.Now()
	controllerRestore := runScaleHealth(
		ctx,
		tools.cli,
		operatorKubeconfig,
		operatorContext,
		instanceUID,
		4,
		scaleAcceleratorsPerNode,
		scaleHealthLimit,
		false,
	)
	measurements.ControllerHealthRestoreSeconds = time.Since(
		controllerRestoreStarted,
	).Seconds()
	requireScaleCLI(
		t,
		controllerRestore,
		"controllerHealthRestore",
		5,
		scaleInstanceName,
	)
	requireScaleDuration(
		t,
		"controllerHealthRestore",
		measurements.ControllerHealthRestoreSeconds,
		scaleHealthLimit,
	)
	inventory = observeScaleInventory(
		t,
		ctx,
		tools.kubectl,
		kubeconfig,
		instanceUID,
	)
	requireScaleInventory(t, inventory, scaleNodeCount, scaleAccelerators, scaleAccelerators)
	if inventory.IdentityDigest != identityBefore {
		t.Fatalf(
			"identityDrift after controller recovery: before=%s after=%s",
			identityBefore,
			inventory.IdentityDigest,
		)
	}
	assertScaleControllerHealthy(
		t,
		ctx,
		tools.kubectl,
		kubeconfig,
	)

	cleanupStarted := time.Now()
	deleted := runProductCLI(
		ctx,
		tools.cli,
		"delete",
		scaleInstanceName,
		"--instance-uid",
		instanceUID,
		"--expected-generation",
		"5",
		"--kubeconfig",
		operatorKubeconfig,
		"--context",
		operatorContext,
		"--timeout=120s",
		"-o",
		"json",
	)
	measurements.CleanupSeconds = time.Since(cleanupStarted).Seconds()
	if deleted.exitCode != 0 || deleted.stderr != "" {
		t.Fatalf("cleanup failed: %#v", deleted)
	}
	requireScaleDuration(
		t,
		"cleanup",
		measurements.CleanupSeconds,
		scaleCleanupLimit,
	)
	waitForScaleCondition(
		t,
		ctx,
		30*time.Second,
		"zero exact-owned live objects",
		func() bool {
			return kubeListCount(
				ctx,
				tools.kubectl,
				kubeconfig,
				"nodes",
				"simulation.kasim.io/instance-uid="+instanceUID,
			) == 0 && kubeListCount(
				ctx,
				tools.kubectl,
				kubeconfig,
				"leases",
				"simulation.kasim.io/instance-uid="+instanceUID,
				"--namespace=kube-node-lease",
			) == 0 && !tryKube(
				ctx,
				tools.kubectl,
				kubeconfig,
				"get",
				"scenarioinstance/"+scaleInstanceName,
			)
		},
	)
	runKube(
		t,
		ctx,
		tools.kubectl,
		kubeconfig,
		"delete",
		"namespace/"+scaleWorkloadNamespace,
		"--wait=true",
		"--timeout=120s",
	)
	measurements.EtcdBytesAfter = scaleEtcdBytes(
		t,
		ctx,
		tools.docker,
		clusterName,
	)
	memory := sampler.stop()
	if memory.Error != "" {
		t.Fatalf("control-plane memory sampling failed: %s", memory.Error)
	}
	if memory.Samples == 0 {
		t.Fatal("control-plane memory sampler collected no samples")
	}
	measurements.ControlPlanePeakBytes = memory.PeakBytes
	measurements.ControlPlaneMemorySamples = memory.Samples
	if memory.PeakBytes > scaleMaximumMemory {
		t.Fatalf(
			"controlPlanePeakBytes = %d, want <= %d",
			memory.PeakBytes,
			scaleMaximumMemory,
		)
	}

	writeScaleReceipt(
		t,
		ctx,
		trial,
		receiptDirectory,
		tools,
		environment,
		serverVersion,
		kubeletVersion,
		nodeImage,
		controllerImage,
		instanceUID,
		identityBefore,
		measurements,
		time.Since(startedAt),
	)
}

func runScaleHealth(
	ctx context.Context,
	cliBinary,
	kubeconfig,
	contextName,
	instanceUID string,
	expectedGeneration,
	healthy int,
	timeout time.Duration,
	async bool,
) productCLIResult {
	arguments := []string{
		"health",
		scaleInstanceName,
		"--group",
		"health-sample",
		"--pool",
		"accelerator",
		"--healthy",
		strconv.Itoa(healthy),
		"--instance-uid",
		instanceUID,
		"--expected-generation",
		strconv.Itoa(expectedGeneration),
		"--kubeconfig",
		kubeconfig,
		"--context",
		contextName,
		"--timeout=" + timeout.String(),
		"-o",
		"json",
	}
	if async {
		arguments = append(arguments, "--async")
	}
	return runProductCLI(ctx, cliBinary, arguments...)
}

func requireScaleCLI(
	t *testing.T,
	result productCLIResult,
	operation string,
	expectedGeneration int,
	expectedName string,
) {
	t.Helper()
	if result.exitCode != 0 || result.stderr != "" {
		t.Fatalf("%s failed: %#v", operation, result)
	}
	if lifecycleNumberField(t, result.stdout, "desiredGeneration") !=
		expectedGeneration {
		t.Fatalf("%s returned wrong generation: %s", operation, result.stdout)
	}
	if expectedName != "" &&
		lifecycleSnapshotString(t, result.stdout, "phase") != "Ready" {
		t.Fatalf("%s did not return Ready: %s", operation, result.stdout)
	}
}

func requireScaleDuration(
	t *testing.T,
	operation string,
	seconds float64,
	limit time.Duration,
) {
	t.Helper()
	if seconds > limit.Seconds() {
		t.Fatalf(
			"%s took %.3fs, want <= %.3fs",
			operation,
			seconds,
			limit.Seconds(),
		)
	}
}

type scaleInventory struct {
	Nodes                int
	ReadyNodes           int
	Capacity             int
	Allocatable          int
	Leases               int
	UnhealthySampleNodes int
	IdentityDigest       string
}

func observeScaleInventory(
	t *testing.T,
	ctx context.Context,
	kubectlBinary,
	kubeconfig,
	instanceUID string,
) scaleInventory {
	t.Helper()
	document := kubeOutput(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"get",
		"nodes",
		"-l",
		"simulation.kasim.io/instance-uid="+instanceUID,
		"-o=json",
	)
	var list struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				UID    string            `json:"uid"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Status struct {
				Capacity    map[string]string `json:"capacity"`
				Allocatable map[string]string `json:"allocatable"`
				Conditions  []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(document), &list); err != nil {
		t.Fatal(err)
	}
	inventory := scaleInventory{Nodes: len(list.Items)}
	identities := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		inventory.Capacity += scaleQuantity(item.Status.Capacity["nvidia.com/gpu"])
		allocatable := scaleQuantity(item.Status.Allocatable["nvidia.com/gpu"])
		inventory.Allocatable += allocatable
		if item.Metadata.Labels["simulation.kasim.io/reference-role"] ==
			"health-sample" && allocatable == 0 {
			inventory.UnhealthySampleNodes++
		}
		for _, condition := range item.Status.Conditions {
			if condition.Type == "Ready" && condition.Status == "True" {
				inventory.ReadyNodes++
				break
			}
		}
		identities = append(
			identities,
			item.Metadata.Name+"\x00"+item.Metadata.UID,
		)
	}
	sort.Strings(identities)
	digester := sha256.New()
	for _, identity := range identities {
		_, _ = digester.Write([]byte(identity))
		_, _ = digester.Write([]byte{0})
	}
	inventory.IdentityDigest = "sha256:" + hex.EncodeToString(digester.Sum(nil))
	inventory.Leases = kubeListCount(
		ctx,
		kubectlBinary,
		kubeconfig,
		"leases",
		"simulation.kasim.io/instance-uid="+instanceUID,
		"--namespace=kube-node-lease",
	)
	return inventory
}

func requireScaleInventory(
	t *testing.T,
	inventory scaleInventory,
	nodes,
	capacity,
	allocatable int,
) {
	t.Helper()
	if inventory.Nodes != nodes ||
		inventory.ReadyNodes != nodes ||
		inventory.Leases != nodes ||
		inventory.Capacity != capacity ||
		inventory.Allocatable != allocatable {
		t.Fatalf(
			"scale inventory = %#v, want nodes/ready/leases=%d capacity=%d allocatable=%d",
			inventory,
			nodes,
			capacity,
			allocatable,
		)
	}
}

func scaleQuantity(value string) int {
	quantity, _ := strconv.Atoi(value)
	return quantity
}

type scalePodInventory struct {
	Total          int
	Running        int
	ScheduledNodes int
}

func observeScalePods(
	ctx context.Context,
	kubectlBinary,
	kubeconfig string,
) scalePodInventory {
	output, err := protocolOracleCommandOutput(
		ctx,
		kubectlBinary,
		"--kubeconfig",
		kubeconfig,
		"get",
		"pods",
		"--namespace="+scaleWorkloadNamespace,
		"--selector="+scaleWorkloadSelectorLabel+"=true",
		"-o=json",
	)
	if err != nil {
		return scalePodInventory{}
	}
	var list struct {
		Items []struct {
			Spec struct {
				NodeName string `json:"nodeName"`
			} `json:"spec"`
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal([]byte(output), &list) != nil {
		return scalePodInventory{}
	}
	inventory := scalePodInventory{Total: len(list.Items)}
	nodes := make(map[string]struct{}, len(list.Items))
	for _, item := range list.Items {
		if item.Status.Phase == "Running" {
			inventory.Running++
		}
		if item.Spec.NodeName != "" {
			nodes[item.Spec.NodeName] = struct{}{}
		}
	}
	inventory.ScheduledNodes = len(nodes)
	return inventory
}

func scaleWorkloadManifest(instanceUID string) string {
	var manifest strings.Builder
	for index := 0; index < scaleRepresentativePods; index++ {
		fmt.Fprintf(&manifest, `apiVersion: v1
kind: Pod
metadata:
  name: kasim-scale-%03d
  namespace: %s
  labels:
    %s: "true"
spec:
  restartPolicy: Never
  nodeSelector:
    simulation.kasim.io/instance-uid: %s
  containers:
  - name: workload
    image: registry.k8s.io/pause:3.10
    resources:
      requests:
        nvidia.com/gpu: "8"
      limits:
        nvidia.com/gpu: "8"
---
`, index, scaleWorkloadNamespace, scaleWorkloadSelectorLabel, instanceUID)
	}
	return manifest.String()
}

func waitForScaleCondition(
	t *testing.T,
	ctx context.Context,
	limit time.Duration,
	description string,
	probe func() bool,
) {
	t.Helper()
	deadline := time.NewTimer(limit)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if probe() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for %s: %v", description, ctx.Err())
		case <-deadline.C:
			t.Fatalf("wait for %s exceeded %s", description, limit)
		case <-ticker.C:
		}
	}
}

func scaleControllerLogs(
	t *testing.T,
	ctx context.Context,
	kubectlBinary,
	kubeconfig string,
) string {
	t.Helper()
	return kubeOutput(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"logs",
		"--namespace=kasim-system",
		"--selector=app.kubernetes.io/component=controller",
		"--tail=-1",
	)
}

func assertNoScaleControllerErrors(t *testing.T, logs string) {
	t.Helper()
	for _, line := range strings.Split(logs, "\n") {
		var event map[string]any
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		if event["level"] == "error" {
			t.Fatalf("controller emitted reconciliation error: %s", line)
		}
	}
}

func assertScaleControllerHealthy(
	t *testing.T,
	ctx context.Context,
	kubectlBinary,
	kubeconfig string,
) {
	t.Helper()
	logs := scaleControllerLogs(t, ctx, kubectlBinary, kubeconfig)
	assertNoScaleControllerErrors(t, logs)
	restarts := kubeOutput(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"get",
		"pods",
		"--namespace=kasim-system",
		"--selector=app.kubernetes.io/component=controller",
		"-o=jsonpath={.items[0].status.containerStatuses[0].restartCount}",
	)
	if restarts != "0" {
		t.Fatalf("controller crash count = %s, want 0", restarts)
	}
}

type scaleMemoryResult struct {
	PeakBytes uint64
	Samples   int
	Error     string
}

type scaleMemorySampler struct {
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	result scaleMemoryResult
}

func startScaleMemorySampler(
	parent context.Context,
	dockerBinary,
	container string,
) *scaleMemorySampler {
	ctx, cancel := context.WithCancel(parent)
	sampler := &scaleMemorySampler{
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go func() {
		defer close(sampler.done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			output, err := protocolOracleCommandOutput(
				ctx,
				dockerBinary,
				"stats",
				"--no-stream",
				"--format={{.MemUsage}}",
				container,
			)
			sampler.mu.Lock()
			if err != nil {
				if ctx.Err() == nil && sampler.result.Error == "" {
					sampler.result.Error = err.Error() + ": " + output
				}
			} else {
				bytes, parseErr := parseDockerMemory(output)
				if parseErr != nil {
					if sampler.result.Error == "" {
						sampler.result.Error = parseErr.Error()
					}
				} else {
					sampler.result.Samples++
					if bytes > sampler.result.PeakBytes {
						sampler.result.PeakBytes = bytes
					}
				}
			}
			sampler.mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return sampler
}

func (sampler *scaleMemorySampler) stop() scaleMemoryResult {
	sampler.once.Do(func() {
		sampler.cancel()
		<-sampler.done
	})
	sampler.mu.Lock()
	defer sampler.mu.Unlock()
	return sampler.result
}

func parseDockerMemory(value string) (uint64, error) {
	used := strings.TrimSpace(strings.SplitN(value, "/", 2)[0])
	fields := strings.Fields(used)
	if len(fields) != 1 {
		return 0, fmt.Errorf("invalid Docker memory value %q", value)
	}
	for _, unit := range []struct {
		suffix     string
		multiplier float64
	}{
		{"GiB", 1 << 30},
		{"MiB", 1 << 20},
		{"KiB", 1 << 10},
		{"GB", 1_000_000_000},
		{"MB", 1_000_000},
		{"kB", 1_000},
		{"B", 1},
	} {
		if !strings.HasSuffix(fields[0], unit.suffix) {
			continue
		}
		number := strings.TrimSuffix(fields[0], unit.suffix)
		parsed, err := strconv.ParseFloat(number, 64)
		if err != nil {
			return 0, err
		}
		return uint64(parsed * unit.multiplier), nil
	}
	return 0, fmt.Errorf("unsupported Docker memory value %q", value)
}

func TestParseDockerMemory(t *testing.T) {
	t.Parallel()
	for input, expected := range map[string]uint64{
		"512MiB / 8GiB": 512 << 20,
		"1.5GiB / 8GiB": 3 << 29,
		"900kB / 8GB":   900_000,
	} {
		actual, err := parseDockerMemory(input)
		if err != nil {
			t.Errorf("parseDockerMemory(%q): %v", input, err)
		} else if actual != expected {
			t.Errorf("parseDockerMemory(%q) = %d, want %d", input, actual, expected)
		}
	}
}

func scaleEtcdBytes(
	t *testing.T,
	ctx context.Context,
	dockerBinary,
	clusterName string,
) int64 {
	t.Helper()
	value := commandOutput(
		t,
		ctx,
		dockerBinary,
		"exec",
		clusterName+"-control-plane",
		"stat",
		"-c",
		"%s",
		"/var/lib/etcd/member/snap/db",
	)
	bytes, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return bytes
}

func scaleServerVersion(
	t *testing.T,
	ctx context.Context,
	kubectlBinary,
	kubeconfig string,
) string {
	t.Helper()
	document := kubeOutput(
		t,
		ctx,
		kubectlBinary,
		kubeconfig,
		"get",
		"--raw=/version",
	)
	var version struct {
		GitVersion string `json:"gitVersion"`
	}
	if err := json.Unmarshal([]byte(document), &version); err != nil {
		t.Fatal(err)
	}
	return version.GitVersion
}

func writeScaleReceipt(
	t *testing.T,
	ctx context.Context,
	trial int,
	receiptDirectory string,
	tools scaleTools,
	environment scaleEnvironment,
	serverVersion,
	kubeletVersion,
	nodeImage,
	controllerImage,
	instanceUID,
	identityDigest string,
	measurements scaleMeasurements,
	duration time.Duration,
) {
	t.Helper()
	controllerImageID := commandOutput(
		t,
		ctx,
		tools.docker,
		"image",
		"inspect",
		"--format={{.Id}}",
		controllerImage,
	)
	receipt := map[string]any{
		"schemaVersion":  "kasim.io/scale-receipt/v1alpha1",
		"checkedAt":      time.Now().UTC().Format(time.RFC3339),
		"sourceRevision": commandOutput(t, ctx, "git", "rev-parse", "HEAD"),
		"trial":          trial,
		"trialsRequired": 2,
		"environment":    environment,
		"kubernetes": map[string]any{
			"serverVersion":       serverVersion,
			"kubeletVersion":      kubeletVersion,
			"nodeImage":           nodeImage,
			"imageClassification": os.Getenv("KASIM_NODE_IMAGE_CLASSIFICATION"),
		},
		"runtime": map[string]any{
			"controllerImage":   controllerImage,
			"controllerImageID": controllerImageID,
			"chartTreeSHA256": scaleTreeDigest(
				t,
				"../../charts/kasim-runtime",
			),
			"releaseInputs": loadJSONEvidence(t, "../../release/inputs.json"),
			"compatibilityLock": loadJSONEvidence(
				t,
				"../../release/compatibility-lock.json",
			),
		},
		"tools": map[string]any{
			"kind":    commandOutput(t, ctx, tools.kind, "version"),
			"kubectl": commandOutput(t, ctx, tools.kubectl, "version", "--client=true"),
			"helm":    commandOutput(t, ctx, tools.helm, "version", "--short"),
			"cli":     commandOutput(t, ctx, tools.cli, "version", "-o", "json"),
		},
		"scenario": map[string]any{
			"name":                scaleInstanceName,
			"instanceUID":         instanceUID,
			"fidelity":            "scheduling",
			"syntheticNodes":      scaleNodeCount,
			"accelerators":        scaleAccelerators,
			"acceleratorsPerNode": scaleAcceleratorsPerNode,
			"healthUpdateNodes":   scaleHealthSample,
			"representativePods":  scaleRepresentativePods,
			"identityDigest":      identityDigest,
		},
		"thresholds": map[string]any{
			"applyReadySeconds":         scaleApplyReadyLimit.Seconds(),
			"observationP95Seconds":     scaleObservationLimit.Seconds(),
			"healthLossSeconds":         scaleHealthLimit.Seconds(),
			"healthRecoverySeconds":     scaleHealthLimit.Seconds(),
			"workloadSeconds":           scaleWorkloadLimit.Seconds(),
			"controllerRecoverySeconds": scaleControllerLimit.Seconds(),
			"cleanupSeconds":            scaleCleanupLimit.Seconds(),
			"controlPlanePeakBytes":     scaleMaximumMemory,
		},
		"measurements": measurements,
		"outcomes": map[string]any{
			"applyReady":             true,
			"observationP95":         true,
			"healthLoss":             true,
			"healthRecovery":         true,
			"workload":               true,
			"controllerRecovery":     true,
			"cleanup":                true,
			"apiErrors":              0,
			"controllerErrors":       0,
			"controllerCrashes":      0,
			"identityDrift":          false,
			"observedCountReduction": false,
			"ownedLiveObjects":       0,
			"etcdFileShrinkClaimed":  false,
		},
		"excluded": []string{
			"physical accelerator hardware",
			"accelerator computation",
			"device-plugin gRPC",
			"CDI injection",
			"etcd compaction",
			"etcd defragmentation",
		},
		"durationSeconds": duration.Seconds(),
		"result":          "passed",
	}
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	if err := os.MkdirAll(receiptDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(
		receiptDirectory,
		fmt.Sprintf("scale-receipt-trial-%d.json", trial),
	)
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func scaleTreeDigest(t *testing.T, root string) string {
	t.Helper()
	var paths []string
	if err := filepath.WalkDir(root, func(
		path string,
		entry fs.DirEntry,
		err error,
	) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	digester := sha256.New()
	for _, path := range paths {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = digester.Write([]byte(filepath.ToSlash(relative)))
		_, _ = digester.Write([]byte{0})
		_, _ = digester.Write(encoded)
		_, _ = digester.Write([]byte{0})
	}
	return hex.EncodeToString(digester.Sum(nil))
}
