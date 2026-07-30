package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LinkMaq/kube-accelerator-sim/internal/cli"
)

func TestOfflineCommandsNeedNoClusterConfiguration(t *testing.T) {
	t.Setenv("KUBECONFIG", "/definitely/not/read")

	tests := map[string]struct {
		args []string
		want []string
	}{
		"version": {
			args: []string{"version", "-o", "json"},
			want: []string{`"kind": "Version"`, `"catalogVersion": "2026-07-30"`},
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
