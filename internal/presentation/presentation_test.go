package presentation_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
	"github.com/LinkMaq/kube-accelerator-sim/internal/presentation"
)

func TestVersionGoldenInEveryOutputFormat(t *testing.T) {
	t.Parallel()

	envelope := presentation.Success(
		"Version",
		"version",
		presentation.VersionResult{
			Binary:            "kasim",
			ProductVersion:    "dev",
			SourceRevision:    "unknown",
			BuildDate:         "unknown",
			SchemaVersion:     "v1alpha1",
			CatalogVersion:    "2026-07-30",
			KubernetesFloor:   "1.30",
			KubernetesCeiling: "1.36",
		},
	)
	for _, formatName := range []string{"human", "json", "yaml"} {
		formatName := formatName
		t.Run(formatName, func(t *testing.T) {
			t.Parallel()
			format, err := presentation.ParseOutputFormat(formatName)
			if err != nil {
				t.Fatal(err)
			}
			got, err := presentation.Render(envelope, format)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join("testdata", "version."+formatName+".golden"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("%s output drifted:\n%s\n%s", formatName, got, want)
			}
		})
	}
}

func TestOfflineFailureClassesAreRedactedInEveryOutputFormat(t *testing.T) {
	t.Parallel()

	exitCategory, err := domain.ParseExitCategory(2)
	if err != nil {
		t.Fatal(err)
	}
	for _, codeName := range []string{
		"InvocationInvalid",
		"ScenarioInvalid",
		"CatalogInvalid",
	} {
		codeName := codeName
		t.Run(codeName, func(t *testing.T) {
			t.Parallel()
			code, err := domain.ParseDiagnosticCode(codeName)
			if err != nil {
				t.Fatal(err)
			}
			diagnostic, err := domain.NewDiagnostic(
				code,
				"token=top-secret",
				false,
				false,
				exitCategory,
			)
			if err != nil {
				t.Fatal(err)
			}
			envelope := presentation.Failure("test", diagnostic)
			for _, formatName := range []string{"human", "json", "yaml"} {
				format, err := presentation.ParseOutputFormat(formatName)
				if err != nil {
					t.Fatal(err)
				}
				got, err := presentation.Render(envelope, format)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(got, []byte("top-secret")) ||
					!bytes.Contains(got, []byte("[REDACTED]")) ||
					!bytes.Contains(got, []byte(codeName)) {
					t.Fatalf("%s %s output was not safely rendered:\n%s", codeName, formatName, got)
				}
				if codeName == "InvocationInvalid" {
					want, err := os.ReadFile(filepath.Join(
						"testdata",
						"error-invocation."+formatName+".golden",
					))
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(got, want) {
						t.Fatalf("%s error output drifted:\n%s\n%s", formatName, got, want)
					}
				}
			}
		})
	}
}

func TestDiagnosticRedactionRemovesKubeconfigAndPrivateKeyBodies(t *testing.T) {
	t.Parallel()

	for _, secret := range []string{
		"apiVersion: v1\nclusters:\n- cluster: {}\nusers:\n- user: {}\n",
		"-----BEGIN PRIVATE KEY-----\nprivate-material\n-----END PRIVATE KEY-----",
		"Authorization: Bearer abc.def.ghi",
	} {
		redacted := renderFailureMessage(t, secret)
		if strings.Contains(redacted, "private-material") ||
			strings.Contains(redacted, "abc.def.ghi") ||
			strings.Contains(redacted, "clusters:") {
			t.Fatalf("secret remained in output: %s", redacted)
		}
	}
}

func TestCanonicalScenarioSecretLookingFieldsAreRedacted(t *testing.T) {
	t.Parallel()

	envelope := presentation.Success(
		"ScenarioCompile",
		"apply",
		presentation.ScenarioCompileResult{
			ScenarioName:   "safe-name",
			ScenarioDigest: "sha256:0000",
			CatalogDigest:  "sha256:1111",
			Resolutions:    []presentation.ResolutionResult{},
			CanonicalScenario: map[string]any{
				"spec": map[string]any{
					"labels": map[string]any{
						"example.com/password": "top-secret",
						"safe":                 "Bearer abc.def.ghi",
					},
				},
			},
		},
	)
	for _, formatName := range []string{"json", "yaml"} {
		format, err := presentation.ParseOutputFormat(formatName)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := presentation.Render(envelope, format)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(encoded, []byte("top-secret")) ||
			bytes.Contains(encoded, []byte("abc.def.ghi")) ||
			bytes.Count(encoded, []byte("[REDACTED]")) != 2 {
			t.Fatalf("%s canonical output was not redacted:\n%s", formatName, encoded)
		}
	}
}

func TestLifecycleFailurePreservesReceiptSnapshotAndRedactsDetail(t *testing.T) {
	t.Parallel()

	code, err := domain.ParseDiagnosticCode("ConvergenceTimeout")
	if err != nil {
		t.Fatal(err)
	}
	exitCategory, err := domain.ParseExitCategory(5)
	if err != nil {
		t.Fatal(err)
	}
	diagnostic, err := domain.NewDiagnostic(
		code,
		"accepted revision timed out token=top-secret",
		true,
		true,
		exitCategory,
	)
	if err != nil {
		t.Fatal(err)
	}
	result := presentation.LifecycleResult{
		Connection: presentation.ConnectionResult{
			ContextName:       "simulation",
			APIServerURL:      "https://cluster.example",
			TargetFingerprint: "sha256:target",
			CADigest:          "sha256:ca",
		},
		Receipt: presentation.RevisionReceiptResult{
			InstanceName:       "training-lab",
			InstanceUID:        "instance-1",
			DesiredGeneration:  2,
			ObservedGeneration: 1,
			RevisionDigest:     "sha256:revision",
			ProfileDigests:     []string{"sha256:profile"},
			RevisionAccepted:   true,
		},
		Snapshot: &presentation.SnapshotResult{
			Phase:                "Reconciling",
			DiagnosticsTruncated: true,
			Diagnostics: []presentation.SnapshotDiagnosticResult{{
				Code:    "ConvergenceFailed",
				Message: "Bearer abc.def.ghi",
			}},
			Conditions: []presentation.ConditionResult{{
				Type:    "Progressing",
				Status:  "True",
				Reason:  "ConvergenceFailed",
				Message: "password=still-secret",
			}},
		},
	}
	envelope := presentation.FailureWithResult("apply", diagnostic, result)
	for _, formatName := range []string{"human", "json", "yaml"} {
		format, err := presentation.ParseOutputFormat(formatName)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := presentation.Render(envelope, format)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"top-secret", "abc.def.ghi", "still-secret"} {
			if bytes.Contains(encoded, []byte(secret)) {
				t.Fatalf("%s lifecycle output exposed %q:\n%s", formatName, secret, encoded)
			}
		}
		for _, expected := range []string{
			"ConvergenceTimeout",
			"training-lab",
			"instance-1",
			"Reconciling",
		} {
			if !bytes.Contains(encoded, []byte(expected)) {
				t.Fatalf("%s lifecycle output omitted %q:\n%s", formatName, expected, encoded)
			}
		}
	}
}

func renderFailureMessage(t *testing.T, message string) string {
	t.Helper()
	code, err := domain.ParseDiagnosticCode("InvocationInvalid")
	if err != nil {
		t.Fatal(err)
	}
	exitCategory, err := domain.ParseExitCategory(2)
	if err != nil {
		t.Fatal(err)
	}
	diagnostic, err := domain.NewDiagnostic(code, message, false, false, exitCategory)
	if err != nil {
		t.Fatal(err)
	}
	format, err := presentation.ParseOutputFormat("human")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := presentation.Render(presentation.Failure("test", diagnostic), format)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
