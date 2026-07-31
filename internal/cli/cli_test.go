package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LinkMaq/kube-accelerator-sim/internal/application"
	"github.com/LinkMaq/kube-accelerator-sim/internal/catalog"
	"github.com/LinkMaq/kube-accelerator-sim/internal/cli"
	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster/recording"
	"github.com/LinkMaq/kube-accelerator-sim/internal/controlplane"
	"github.com/LinkMaq/kube-accelerator-sim/internal/controlplane/memory"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

func TestOfflineCommandsNeedNoClusterConfiguration(t *testing.T) {
	t.Setenv("KUBECONFIG", "/definitely/not/read")

	tests := map[string]struct {
		args []string
		want []string
	}{
		"version": {
			args: []string{"version", "-o", "json"},
			want: []string{`"kind": "Version"`, `"catalogVersion": "2026-07-31"`},
		},
		"profile list": {
			args: []string{"profile", "list", "-o", "json"},
			want: []string{`"kind": "ProfileList"`, `"id": "huawei-ascend"`, `"class": "verified"`},
		},
		"profile show": {
			args: []string{"profile", "show", "nvidia", "-o", "json"},
			want: []string{
				`"kind": "Profile"`,
				`"name": "nvidia.com/gpu"`,
				`"key": "nvidia.com/gpu.product"`,
				`"id": "nvidia-h100"`,
				`"evidence"`,
			},
		},
		"file client dry-run": {
			args: []string{
				"apply",
				"-f", "testdata/training-lab.yaml",
				"--dry-run=client",
				"-o", "json",
			},
			want: []string{
				`"kind": "ScenarioCompile"`,
				`"scenarioName": "training-lab"`,
				`"resourceName": "nvidia.com/gpu"`,
				`"canonicalScenario"`,
			},
		},
		"demo client dry-run": {
			args: []string{
				"apply", "demo",
				"--profile", "nvidia",
				"--model", "nvidia-h100",
				"--nodes", "2",
				"--accelerators-per-node", "4",
				"--dry-run", "client",
				"-o", "yaml",
			},
			want: []string{
				"kind: ScenarioCompile",
				"scenarioName: demo",
				"resourceName: nvidia.com/gpu",
				"replicas: 2",
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := cli.Run(test.args, strings.NewReader(""), &stdout, &stderr)
			if exitCode != 0 {
				t.Fatalf("Run() exit = %d, stderr:\n%s", exitCode, stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("success wrote stderr:\n%s", stderr.String())
			}
			for _, expected := range test.want {
				if !strings.Contains(stdout.String(), expected) {
					t.Errorf("stdout missing %q:\n%s", expected, stdout.String())
				}
			}
		})
	}
}

func TestOfflineFailuresUseCategoryTwoEnvelopeOnStderr(t *testing.T) {
	t.Parallel()

	tests := map[string][]string{
		"unknown command":       {"plan", "-o", "json"},
		"unsupported lifecycle": {"status", "-o", "json"},
		"missing input":         {"apply", "--dry-run=client", "-o", "json"},
		"not client dry-run": {
			"apply", "-f", "testdata/training-lab.yaml", "--dry-run=server", "-o", "json",
		},
		"mixed inputs": {
			"apply", "demo", "-f", "testdata/training-lab.yaml",
			"--profile", "nvidia", "--model", "nvidia-h100",
			"--nodes", "1", "--accelerators-per-node", "1",
			"--dry-run=client", "-o", "json",
		},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := cli.Run(args, strings.NewReader(""), &stdout, &stderr)
			if exitCode != 2 {
				t.Fatalf("Run() exit = %d, want 2", exitCode)
			}
			if stdout.Len() != 0 {
				t.Fatalf("failure wrote stdout:\n%s", stdout.String())
			}
			var envelope map[string]any
			if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
				t.Fatalf("stderr is not a JSON envelope: %v\n%s", err, stderr.String())
			}
			diagnostic := envelope["diagnostic"].(map[string]any)
			if envelope["status"] != "failure" ||
				diagnostic["exitCategory"] != float64(2) {
				t.Fatalf("unexpected failure envelope: %#v", envelope)
			}
		})
	}
}

func TestOfflineFailureKindsMapToStableDiagnosticCodes(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		args  []string
		stdin string
		code  string
	}{
		"invocation": {
			args: []string{"plan", "-o", "json"},
			code: "InvocationInvalid",
		},
		"catalog": {
			args: []string{"profile", "show", "absent", "-o", "json"},
			code: "CatalogInvalid",
		},
		"scenario": {
			args:  []string{"apply", "-f", "-", "--dry-run=client", "-o", "json"},
			stdin: "metadata:\n  name: first\n  name: duplicate\n",
			code:  "ScenarioInvalid",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := cli.Run(test.args, strings.NewReader(test.stdin), &stdout, &stderr)
			if exitCode != 2 || stdout.Len() != 0 {
				t.Fatalf("exit = %d, stdout = %q", exitCode, stdout.String())
			}
			var envelope struct {
				Diagnostic struct {
					Code         string `json:"code"`
					ExitCategory int    `json:"exitCategory"`
				} `json:"diagnostic"`
			}
			if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Diagnostic.Code != test.code ||
				envelope.Diagnostic.ExitCategory != 2 {
				t.Fatalf("unexpected diagnostic: %#v", envelope.Diagnostic)
			}
		})
	}
}

func TestConnectedCLIAsyncLifecycleNoOpTypedRevisionsStatusAndDeleteRetry(
	t *testing.T,
) {
	t.Parallel()

	dependencies, controlAdapter := connectedDependencies(t, nil)
	targetFlags := []string{
		"--kubeconfig", "/explicit/config",
		"--context", "test-context",
		"-o", "json",
	}
	serverDryRunArgs := append([]string{
		"apply",
		"-f", "testdata/training-lab.yaml",
		"--dry-run=server",
	}, targetFlags...)
	serverDryRun := runCLI(t, dependencies, serverDryRunArgs)
	if serverDryRun.exit != 0 ||
		lifecycleReceipt(t, serverDryRun.stdout)["revisionAccepted"] != false ||
		!strings.Contains(serverDryRun.stdout, `"warning"`) {
		t.Fatalf("server dry-run did not return a proposal: %#v", serverDryRun)
	}
	if _, err := controlAdapter.Read(
		context.Background(),
		controlplane.InstanceKey{
			TargetFingerprint: dependenciesTarget(t).Fingerprint,
			Name:              mustName(t, "training-lab"),
		},
	); controlplane.ErrorCodeOf(err) != controlplane.ErrorNotFound {
		t.Fatalf("server dry-run persisted state: %v", err)
	}
	applyArgs := append([]string{
		"apply",
		"-f", "testdata/training-lab.yaml",
		"--async",
	}, targetFlags...)
	apply := runCLI(t, dependencies, applyArgs)
	if apply.exit != 0 || apply.stderr != "" {
		t.Fatalf("apply exit=%d stderr=%s", apply.exit, apply.stderr)
	}
	receipt := lifecycleReceipt(t, apply.stdout)
	uid := receipt["instanceUID"].(string)
	if receipt["revisionAccepted"] != true ||
		receipt["desiredGeneration"] != float64(1) {
		t.Fatalf("unexpected apply receipt: %#v", receipt)
	}

	noOpArgs := append([]string{
		"apply",
		"-f", "testdata/training-lab.yaml",
	}, targetFlags...)
	noOp := runCLI(t, dependencies, noOpArgs)
	if noOp.exit != 0 ||
		lifecycleReceipt(t, noOp.stdout)["noOp"] != true {
		t.Fatalf("same digest was not a no-op: %#v", noOp)
	}

	healthArgs := append([]string{
		"health", "training-lab",
		"--group", "workers",
		"--pool", "training",
		"--healthy", "4",
		"--instance-uid", uid,
		"--expected-generation", "1",
		"--async",
	}, targetFlags...)
	health := runCLI(t, dependencies, healthArgs)
	if health.exit != 0 ||
		lifecycleReceipt(t, health.stdout)["desiredGeneration"] != float64(2) {
		t.Fatalf("health failed: %#v", health)
	}

	scaleArgs := append([]string{
		"scale", "training-lab",
		"--group", "workers",
		"--replicas", "3",
		"--instance-uid", uid,
		"--expected-generation", "2",
		"--async",
	}, targetFlags...)
	scale := runCLI(t, dependencies, scaleArgs)
	if scale.exit != 0 ||
		lifecycleReceipt(t, scale.stdout)["desiredGeneration"] != float64(3) {
		t.Fatalf("scale failed: %#v", scale)
	}

	statusArgs := append([]string{"status", "training-lab"}, targetFlags...)
	status := runCLI(t, dependencies, statusArgs)
	if status.exit != 0 ||
		lifecycleReceipt(t, status.stdout)["desiredGeneration"] != float64(3) ||
		!strings.Contains(status.stdout, `"phase": "Pending"`) {
		t.Fatalf("status failed: %#v", status)
	}

	updateArgs := append([]string{
		"apply",
		"-f", "testdata/training-lab.yaml",
		"--instance-uid", uid,
		"--expected-generation", "3",
		"--async",
	}, targetFlags...)
	update := runCLI(t, dependencies, updateArgs)
	if update.exit != 0 ||
		lifecycleReceipt(t, update.stdout)["desiredGeneration"] != float64(4) {
		t.Fatalf("file update did not bind current resourceVersion: %#v", update)
	}

	deleteArgs := append([]string{
		"delete", "training-lab",
		"--instance-uid", uid,
		"--expected-generation", "4",
		"--async",
	}, targetFlags...)
	deletion := runCLI(t, dependencies, deleteArgs)
	retry := runCLI(t, dependencies, deleteArgs)
	if deletion.exit != 0 ||
		lifecycleReceipt(t, deletion.stdout)["revisionAccepted"] != true ||
		retry.exit != 0 ||
		lifecycleReceipt(t, retry.stdout)["noOp"] != true {
		t.Fatalf("delete retry was not idempotent: first=%#v retry=%#v", deletion, retry)
	}
	record, err := controlAdapter.Read(
		context.Background(),
		controlplane.InstanceKey{
			TargetFingerprint: dependenciesTarget(t).Fingerprint,
			Name:              mustName(t, "training-lab"),
		},
	)
	if err != nil || !record.DeletionRequested {
		t.Fatalf("deletion was not durably requested: record=%#v err=%v", record, err)
	}
}

func TestConnectedCLICoversPreflightConflictAndAcceptedTimeoutCategories(t *testing.T) {
	t.Parallel()

	runtimeUnavailable := cluster.NewError(
		cluster.ErrorRuntimeUnavailable,
		"product runtime unavailable token=top-secret",
		false,
	)
	preflightDependencies, _ := connectedDependencies(t, runtimeUnavailable)
	baseApply := []string{
		"apply",
		"-f", "testdata/training-lab.yaml",
		"--kubeconfig", "/explicit/config",
		"--context", "test-context",
		"-o", "json",
	}
	preflight := runCLI(
		t,
		preflightDependencies,
		append(append([]string{}, baseApply...), "--async"),
	)
	if preflight.exit != 3 ||
		!strings.Contains(preflight.stderr, `"code": "RuntimeUnavailable"`) ||
		strings.Contains(preflight.stderr, "top-secret") {
		t.Fatalf("preflight category/redaction failed: %#v", preflight)
	}

	dependencies, _ := connectedDependencies(t, nil)
	created := runCLI(
		t,
		dependencies,
		append(append([]string{}, baseApply...), "--async"),
	)
	uid := lifecycleReceipt(t, created.stdout)["instanceUID"].(string)
	conflict := runCLI(t, dependencies, []string{
		"health", "training-lab",
		"--group", "workers",
		"--pool", "training",
		"--healthy", "4",
		"--instance-uid", "wrong-instance",
		"--expected-generation", "1",
		"--async",
		"--kubeconfig", "/explicit/config",
		"--context", "test-context",
		"-o", "json",
	})
	if conflict.exit != 4 ||
		!strings.Contains(conflict.stderr, `"code": "UIDConflict"`) {
		t.Fatalf("conflict category failed: %#v", conflict)
	}
	if uid == "" {
		t.Fatal("create returned no UID")
	}

	timeoutDependencies, _ := connectedDependencies(t, nil)
	timeout := runCLI(t, timeoutDependencies, append(
		append([]string{}, baseApply...),
		"--timeout=5ms",
	))
	if timeout.exit != 5 ||
		!strings.Contains(timeout.stderr, `"code": "ConvergenceTimeout"`) ||
		!strings.Contains(timeout.stderr, `"revisionAccepted": true`) ||
		!strings.Contains(timeout.stderr, `"snapshot"`) {
		t.Fatalf("accepted timeout lost receipt or Snapshot: %#v", timeout)
	}
}

type cliResult struct {
	exit   int
	stdout string
	stderr string
}

func runCLI(
	t *testing.T,
	dependencies cli.Dependencies,
	args []string,
) cliResult {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exit := cli.RunWithDependencies(
		args,
		strings.NewReader(""),
		&stdout,
		&stderr,
		dependencies,
	)
	return cliResult{exit: exit, stdout: stdout.String(), stderr: stderr.String()}
}

func lifecycleReceipt(t *testing.T, encoded string) map[string]any {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal([]byte(encoded), &envelope); err != nil {
		t.Fatalf("decode lifecycle envelope: %v\n%s", err, encoded)
	}
	result, ok := envelope["result"].(map[string]any)
	if !ok {
		t.Fatalf("lifecycle result missing: %#v", envelope)
	}
	receipt, ok := result["receipt"].(map[string]any)
	if !ok {
		t.Fatalf("lifecycle receipt missing: %#v", result)
	}
	return receipt
}

func connectedDependencies(
	t *testing.T,
	discoveryError error,
) (cli.Dependencies, *memory.Adapter) {
	t.Helper()
	snapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	target := dependenciesTarget(t)
	controlAdapter := memory.New(memory.Options{HistoryLimit: 8})
	errorsByCall := map[recording.Call]error{}
	if discoveryError != nil {
		errorsByCall[recording.CallDiscover] = discoveryError
	}
	clusterAdapter := recording.New(recording.Options{
		Capabilities: cluster.TargetCapabilities{
			ServerVersion: "v1.36.3", KubernetesMinor: 36,
		},
		Errors: errorsByCall,
	})
	connected := application.ConnectedTarget{
		Receipt: cluster.ConnectionReceipt{
			ContextName:             target.ContextName,
			CanonicalKubeconfigPath: "/explicit/config",
			APIServerURL:            "https://example.invalid",
			TargetFingerprint:       target.Fingerprint,
			CADigest:                target.Fingerprint,
		},
		Target:       target,
		ControlPlane: controlAdapter,
		Cluster:      clusterAdapter,
	}
	return cli.Dependencies{
		Catalog: snapshot,
		Connect: func(
			context.Context,
			cluster.TargetSelection,
		) (application.ConnectedTarget, error) {
			return connected, nil
		},
	}, controlAdapter
}

func dependenciesTarget(t *testing.T) controlplane.ExplicitTarget {
	t.Helper()
	digest, err := domain.ParseDigest("sha256:" + strings.Repeat("1", 64))
	if err != nil {
		t.Fatal(err)
	}
	return controlplane.ExplicitTarget{
		ContextName: "test-context",
		Fingerprint: digest,
	}
}

func mustName(t *testing.T, value string) domain.Name {
	t.Helper()
	name, err := domain.ParseName(value)
	if err != nil {
		t.Fatal(err)
	}
	return name
}
