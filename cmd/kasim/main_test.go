package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

func TestBuiltCLISeparatesSuccessfulOutputAndFailureDiagnostics(t *testing.T) {
	binaryName := "kasim"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binary := filepath.Join(t.TempDir(), binaryName)
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build kasim: %v\n%s", err, output)
	}

	scenarioDocument, err := os.ReadFile("../../internal/cli/testdata/training-lab.yaml")
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]struct {
		args       []string
		stdin      []byte
		exitCode   int
		wantStdout string
		wantStderr string
	}{
		"version": {
			args:       []string{"version", "-o", "json"},
			exitCode:   0,
			wantStdout: `"kind": "Version"`,
		},
		"profile list": {
			args:       []string{"profile", "list"},
			exitCode:   0,
			wantStdout: "huawei-ascend",
		},
		"profile show": {
			args:       []string{"profile", "show", "nvidia", "-o", "yaml"},
			exitCode:   0,
			wantStdout: "nvidia.com/gpu",
		},
		"stdin client dry-run": {
			args: []string{
				"apply", "-f", "-", "--dry-run=client", "-o", "json",
			},
			stdin:      scenarioDocument,
			exitCode:   0,
			wantStdout: `"kind": "ScenarioCompile"`,
		},
		"invalid invocation": {
			args:       []string{"plan", "-o", "json"},
			exitCode:   2,
			wantStderr: `"code": "InvocationInvalid"`,
		},
		"forbidden backend flag": {
			args: []string{
				"apply", "-f", "-", "--dry-run=client", "--backend", "kwok", "-o", "json",
			},
			stdin:      scenarioDocument,
			exitCode:   2,
			wantStderr: `"code": "InvocationInvalid"`,
		},
		"target preflight": {
			args: []string{
				"status", "training-lab",
				"--kubeconfig", "/definitely/not/a/kubeconfig",
				"--context", "missing",
				"-o", "json",
			},
			exitCode:   3,
			wantStderr: `"code": "TargetUnavailable"`,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			command := exec.Command(binary, test.args...)
			command.Stdin = bytes.NewReader(test.stdin)
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			err := command.Run()
			gotExit := 0
			if err != nil {
				var exitError *exec.ExitError
				if !errors.As(err, &exitError) {
					t.Fatal(err)
				}
				gotExit = exitError.ExitCode()
			}
			if gotExit != test.exitCode {
				t.Fatalf(
					"exit = %d, want %d\nstdout:\n%s\nstderr:\n%s",
					gotExit,
					test.exitCode,
					stdout.String(),
					stderr.String(),
				)
			}
			if test.wantStdout != "" && !strings.Contains(stdout.String(), test.wantStdout) {
				t.Errorf("stdout missing %q:\n%s", test.wantStdout, stdout.String())
			}
			if test.wantStderr != "" && !strings.Contains(stderr.String(), test.wantStderr) {
				t.Errorf("stderr missing %q:\n%s", test.wantStderr, stderr.String())
			}
			if test.exitCode == 0 && stderr.Len() != 0 {
				t.Errorf("success wrote stderr:\n%s", stderr.String())
			}
			if test.exitCode != 0 && stdout.Len() != 0 {
				t.Errorf("failure wrote stdout:\n%s", stdout.String())
			}
		})
	}
}

func TestCLISubprocessCoversEveryStableExitCategory(t *testing.T) {
	if helperCase := os.Getenv("KASIM_CLI_HELPER_CASE"); helperCase != "" {
		os.Exit(runCLIHelperCase(helperCase))
	}

	tests := map[string]struct {
		exitCode int
		stream   string
		want     string
	}{
		"success": {
			exitCode: 0,
			stream:   "stdout",
			want:     `"status": "success"`,
		},
		"invocation": {
			exitCode: 2,
			stream:   "stderr",
			want:     `"code": "InvocationInvalid"`,
		},
		"preflight": {
			exitCode: 3,
			stream:   "stderr",
			want:     `"code": "RuntimeUnavailable"`,
		},
		"conflict": {
			exitCode: 4,
			stream:   "stderr",
			want:     `"code": "UIDConflict"`,
		},
		"accepted-timeout": {
			exitCode: 5,
			stream:   "stderr",
			want:     `"revisionAccepted": true`,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			command := exec.Command(
				os.Args[0],
				"-test.run=^TestCLISubprocessCoversEveryStableExitCategory$",
			)
			command.Env = append(
				os.Environ(),
				"KASIM_CLI_HELPER_CASE="+name,
			)
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			err := command.Run()
			exitCode := 0
			if err != nil {
				var exitError *exec.ExitError
				if !errors.As(err, &exitError) {
					t.Fatal(err)
				}
				exitCode = exitError.ExitCode()
			}
			if exitCode != test.exitCode {
				t.Fatalf(
					"exit=%d want=%d\nstdout:\n%s\nstderr:\n%s",
					exitCode,
					test.exitCode,
					stdout.String(),
					stderr.String(),
				)
			}
			got := stdout.String()
			if test.stream == "stderr" {
				got = stderr.String()
			}
			if !strings.Contains(got, test.want) {
				t.Fatalf("%s missing %q:\n%s", test.stream, test.want, got)
			}
			if test.exitCode == 0 && stderr.Len() != 0 {
				t.Fatalf("success wrote stderr:\n%s", stderr.String())
			}
			if test.exitCode != 0 && stdout.Len() != 0 {
				t.Fatalf("failure wrote stdout:\n%s", stdout.String())
			}
		})
	}
}

func runCLIHelperCase(helperCase string) int {
	dependencies := helperDependencies(helperCase == "preflight")
	baseApply := []string{
		"apply",
		"-f", "../../internal/cli/testdata/training-lab.yaml",
		"--kubeconfig", "/explicit/config",
		"--context", "test-context",
		"-o", "json",
	}
	switch helperCase {
	case "success":
		return cli.RunWithDependencies(
			append(baseApply, "--async"),
			strings.NewReader(""),
			os.Stdout,
			os.Stderr,
			dependencies,
		)
	case "invocation":
		return cli.RunWithDependencies(
			[]string{"unknown", "-o", "json"},
			strings.NewReader(""),
			os.Stdout,
			os.Stderr,
			dependencies,
		)
	case "preflight":
		return cli.RunWithDependencies(
			append(baseApply, "--async"),
			strings.NewReader(""),
			os.Stdout,
			os.Stderr,
			dependencies,
		)
	case "conflict":
		exit := cli.RunWithDependencies(
			append(baseApply, "--async"),
			strings.NewReader(""),
			io.Discard,
			io.Discard,
			dependencies,
		)
		if exit != 0 {
			return exit
		}
		return cli.RunWithDependencies(
			[]string{
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
			},
			strings.NewReader(""),
			os.Stdout,
			os.Stderr,
			dependencies,
		)
	case "accepted-timeout":
		return cli.RunWithDependencies(
			append(baseApply, "--timeout=5ms"),
			strings.NewReader(""),
			os.Stdout,
			os.Stderr,
			dependencies,
		)
	default:
		return 2
	}
}

func helperDependencies(runtimeUnavailable bool) cli.Dependencies {
	snapshot, err := catalog.LoadBundled()
	if err != nil {
		panic(err)
	}
	targetDigest, err := domain.ParseDigest(
		"sha256:" + strings.Repeat("1", 64),
	)
	if err != nil {
		panic(err)
	}
	target := controlplane.ExplicitTarget{
		ContextName: "test-context",
		Fingerprint: targetDigest,
	}
	errorsByCall := map[recording.Call]error{}
	if runtimeUnavailable {
		errorsByCall[recording.CallDiscover] = cluster.NewError(
			cluster.ErrorRuntimeUnavailable,
			"product runtime unavailable",
			false,
		)
	}
	controlAdapter := memory.New(memory.Options{HistoryLimit: 8})
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
	}
}
