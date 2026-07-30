package architecture_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestArchitectureGuardRejectsInfrastructureImportsFromDomain(t *testing.T) {
	t.Parallel()

	for _, importPath := range []string{
		"github.com/LinkMaq/kube-accelerator-sim/cmd/kasim",
		"gopkg.in/yaml.v3",
		"helm.sh/helm/v3/pkg/action",
		"sigs.k8s.io/controller-runtime/pkg/client",
		"k8s.io/client-go/kubernetes",
		"github.com/kubernetes-sigs/kwok/pkg/kwokctl",
		"sigs.k8s.io/kind/pkg/cluster",
		"github.com/vendor/device-plugin",
	} {
		importPath := importPath
		t.Run(importPath, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			domainDirectory := filepath.Join(root, "internal", "domain")
			if err := os.MkdirAll(domainDirectory, 0o755); err != nil {
				t.Fatal(err)
			}

			source := []byte("package domain\n\nimport _ " + strconv.Quote(importPath) + "\n")
			if err := os.WriteFile(filepath.Join(domainDirectory, "scenario.go"), source, 0o600); err != nil {
				t.Fatal(err)
			}

			command := exec.Command(
				"go",
				"run",
				"../../internal/tools/archcheck",
				"--root",
				root,
			)
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("%v unexpectedly succeeded\n%s", command.Args, output)
			}
			want := "internal/domain cannot import " + importPath
			if !strings.Contains(string(output), want) {
				t.Fatalf("unexpected diagnostic:\n%s\nwant %q", output, want)
			}
		})
	}
}

func TestArchitectureGuardRejectsShallowCatchAllPackages(t *testing.T) {
	t.Parallel()

	for _, packageName := range []string{"backend", "common", "providers", "utils"} {
		packageName := packageName
		t.Run(packageName, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			packageDirectory := filepath.Join(root, "internal", packageName)
			if err := os.MkdirAll(packageDirectory, 0o755); err != nil {
				t.Fatal(err)
			}
			source := []byte("package " + packageName + "\n")
			if err := os.WriteFile(filepath.Join(packageDirectory, "package.go"), source, 0o600); err != nil {
				t.Fatal(err)
			}

			command := exec.Command(
				"go",
				"run",
				"../../internal/tools/archcheck",
				"--root",
				root,
			)
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("%v unexpectedly succeeded\n%s", command.Args, output)
			}
			want := "forbidden catch-all package internal/" + packageName
			if !strings.Contains(string(output), want) {
				t.Fatalf("unexpected diagnostic:\n%s\nwant %q", output, want)
			}
		})
	}
}
