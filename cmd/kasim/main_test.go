package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
