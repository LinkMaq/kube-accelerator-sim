// Command traceability generates the reviewable normative requirement index.
//
//go:generate go run . --root ../../.. --write
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const schemaVersion = "kasim.io/requirement-traceability/v1alpha1"

var requirementPattern = regexp.MustCompile(`\*\*([A-Z]+-[0-9]{3})\*\*`)

type route struct {
	implementation []string
	tests          []string
	evidence       []string
}

type document struct {
	SchemaVersion string        `json:"schemaVersion"`
	Spec          string        `json:"spec"`
	Requirements  []requirement `json:"requirements"`
}

type requirement struct {
	ID             string   `json:"id"`
	Implementation []string `json:"implementation"`
	Tests          []string `json:"tests"`
	Evidence       []string `json:"evidence"`
}

var routes = map[string]route{
	"ARCH": {
		implementation: []string{"docs/adr/0007-deep-modules-and-extension-seams.md", "internal/tools/archcheck/main.go"},
		tests:          []string{"test/architecture/architecture_test.go"},
		evidence:       []string{".github/workflows/ci.yml"},
	},
	"CAT": {
		implementation: []string{"profiles/catalog.json", "internal/catalog/catalog.go"},
		tests:          []string{"internal/catalog/catalog_test.go", "internal/catalog/golden_test.go", "internal/catalog/schema_test.go"},
		evidence:       []string{"docs/research/vendor-model-evidence-catalog.md", ".github/workflows/ci.yml"},
	},
	"CLI": {
		implementation: []string{"internal/cli/cli.go", "internal/presentation/presentation.go"},
		tests:          []string{"internal/cli/cli_test.go", "internal/presentation/presentation_test.go"},
		evidence:       []string{".github/workflows/compatibility.yml", ".github/workflows/scale.yml"},
	},
	"COMPAT": {
		implementation: []string{"release/compatibility-lock.json", "docs/operators/kubernetes-compatibility.md"},
		tests:          []string{"test/contract/compatibility_matrix_test.go", "test/e2e/compatibility_test.go", "test/e2e/dra_smoke_test.go"},
		evidence:       []string{".github/workflows/compatibility.yml"},
	},
	"DOM": {
		implementation: []string{"internal/domain/doc.go", "internal/domain/scenario.go", "internal/domain/receipt.go", "internal/domain/status.go"},
		tests:          []string{"internal/domain/scenario_test.go", "internal/domain/receipt_test.go", "internal/domain/status_test.go"},
		evidence:       []string{".github/workflows/ci.yml"},
	},
	"DRA": {
		implementation: []string{"internal/projection/dra/dra.go"},
		tests:          []string{"internal/projection/dra/dra_test.go", "test/e2e/dra_smoke_test.go"},
		evidence:       []string{".github/workflows/compatibility.yml", ".github/workflows/_compatibility-dra-row.yml"},
	},
	"LIFE": {
		implementation: []string{"internal/application/runtime.go", "internal/reconcile/reconcile.go", "internal/reconcile/kubernetes/adapter.go"},
		tests:          []string{"internal/application/runtime_test.go", "internal/reconcile/reconcile_test.go", "internal/reconcile/kubernetes/adapter_test.go"},
		evidence:       []string{".github/workflows/compatibility.yml", ".github/workflows/scale.yml"},
	},
	"OUT": {
		implementation: []string{"internal/presentation/presentation.go"},
		tests:          []string{"internal/presentation/presentation_test.go", "internal/presentation/testdata/version.json.golden"},
		evidence:       []string{".github/workflows/ci.yml"},
	},
	"PACK": {
		implementation: []string{"charts/kasim-runtime/Chart.yaml", "charts/kasim-runtime/values.schema.json", "release/inputs.json"},
		tests:          []string{"test/packaging/chart_test.go", "test/e2e/chart_smoke_test.go"},
		evidence:       []string{".github/workflows/ci.yml", ".github/workflows/release.yml"},
	},
	"PERF": {
		implementation: []string{"test/e2e/testdata/reference-scale.yaml", "internal/application/runtime.go"},
		tests:          []string{"test/e2e/scale_test.go", "test/contract/scale_gate_test.go"},
		evidence:       []string{".github/workflows/scale.yml"},
	},
	"PREFLIGHT": {
		implementation: []string{"internal/application/preflight.go", "internal/application/runtime.go"},
		tests:          []string{"internal/application/preflight_test.go", "test/e2e/compatibility_test.go"},
		evidence:       []string{".github/workflows/compatibility.yml"},
	},
	"PROTO": {
		implementation: []string{"test/oracle/deviceplugin/plugin.go", "test/oracle/deviceplugin/main.go"},
		tests:          []string{"test/oracle/deviceplugin/plugin_test.go", "test/e2e/device_plugin_oracle_test.go"},
		evidence:       []string{".github/workflows/protocol-oracle.yml"},
	},
	"RBAC": {
		implementation: []string{"charts/kasim-runtime/templates/controller-rbac.yaml", "charts/kasim-runtime/templates/persona-rbac.yaml", "charts/kasim-runtime/templates/kwok-rbac.yaml"},
		tests:          []string{"test/packaging/chart_test.go", "test/e2e/chart_smoke_test.go"},
		evidence:       []string{".github/workflows/ci.yml"},
	},
	"REL": {
		implementation: []string{"internal/tools/releasebuild/main.go", "internal/tools/releaseevidence/main.go", "release/inputs.json"},
		tests:          []string{"internal/tools/releasebuild/main_test.go", "internal/tools/releaseevidence/main_test.go", "test/release/smoke_test.go"},
		evidence:       []string{".github/workflows/release.yml"},
	},
	"SAFE": {
		implementation: []string{"internal/reconcile/kubernetes/adapter.go", "internal/cluster/kubernetes/adapter.go"},
		tests:          []string{"internal/reconcile/kubernetes/adapter_test.go", "internal/cluster/kubernetes/adapter_test.go", "test/e2e/compatibility_test.go"},
		evidence:       []string{".github/workflows/compatibility.yml", ".github/workflows/scale.yml"},
	},
	"SCHED": {
		implementation: []string{"internal/projection/extended/extended.go", "internal/reconcile/reconcile.go"},
		tests:          []string{"internal/projection/extended/extended_test.go", "test/e2e/compatibility_test.go"},
		evidence:       []string{".github/workflows/compatibility.yml"},
	},
	"SCN": {
		implementation: []string{"internal/scenario/compiler.go", "internal/domain/scenario.go"},
		tests:          []string{"internal/scenario/compile_test.go", "test/contract/documentation_test.go"},
		evidence:       []string{".github/workflows/ci.yml", ".github/workflows/scale.yml"},
	},
	"SCOPE": {
		implementation: []string{"docs/adr/0001-fidelity-modes-and-simulation-backends.md", "docs/adr/0005-explicit-target-receipt-driven-cli.md", "internal/cli/cli.go"},
		tests:          []string{"test/architecture/architecture_test.go", "internal/cli/cli_test.go", "test/contract/documentation_test.go"},
		evidence:       []string{".github/workflows/compatibility.yml", ".github/workflows/protocol-oracle.yml"},
	},
	"STAT": {
		implementation: []string{"internal/domain/status.go", "internal/presentation/presentation.go"},
		tests:          []string{"internal/domain/status_test.go", "internal/presentation/presentation_test.go"},
		evidence:       []string{".github/workflows/compatibility.yml", ".github/workflows/scale.yml"},
	},
	"TARGET": {
		implementation: []string{"internal/cluster/kubernetes/connect.go", "internal/cli/cli.go"},
		tests:          []string{"internal/cluster/kubernetes/connect_test.go", "internal/cli/cli_test.go"},
		evidence:       []string{".github/workflows/compatibility.yml"},
	},
	"TEST": {
		implementation: []string{"Makefile", "release/compatibility-lock.json"},
		tests:          []string{"test/architecture/architecture_test.go", "test/contract/documentation_test.go", "test/e2e/compatibility_test.go"},
		evidence:       []string{".github/workflows/ci.yml", ".github/workflows/compatibility.yml", ".github/workflows/protocol-oracle.yml", ".github/workflows/scale.yml"},
	},
}

func main() {
	root := flag.String("root", ".", "repository root")
	write := flag.Bool("write", false, "write generated files")
	check := flag.Bool("check", false, "verify generated files are current")
	flag.Parse()
	if *write && *check {
		fail("--write and --check are mutually exclusive")
	}
	if !*write {
		*check = true
	}

	generated, err := generate(*root)
	if err != nil {
		fail(err.Error())
	}
	for path, content := range generated {
		fullPath := filepath.Join(*root, filepath.FromSlash(path))
		if *write {
			if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
				fail(fmt.Sprintf("create %s parent: %v", path, err))
			}
			if err := os.WriteFile(fullPath, content, 0o644); err != nil {
				fail(fmt.Sprintf("write %s: %v", path, err))
			}
			continue
		}
		existing, err := os.ReadFile(fullPath)
		if err != nil {
			fail(fmt.Sprintf("read generated %s: %v", path, err))
		}
		if !bytes.Equal(existing, content) {
			fail(fmt.Sprintf("%s is stale; run go generate ./internal/tools/traceability", path))
		}
	}
}

func generate(root string) (map[string][]byte, error) {
	specPath := filepath.Join(root, "docs/spec/v1.md")
	spec, err := os.ReadFile(specPath)
	if err != nil {
		return nil, fmt.Errorf("read governing specification: %w", err)
	}
	matches := requirementPattern.FindAllSubmatch(spec, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("governing specification contains no requirement IDs")
	}

	seen := map[string]struct{}{}
	requirements := make([]requirement, 0, len(matches))
	for _, match := range matches {
		id := string(match[1])
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("duplicate normative requirement %s", id)
		}
		seen[id] = struct{}{}
		prefix := strings.SplitN(id, "-", 2)[0]
		mapping, ok := routes[prefix]
		if !ok {
			return nil, fmt.Errorf("normative requirement %s has no traceability route", id)
		}
		for _, path := range append(
			append(append([]string{}, mapping.implementation...), mapping.tests...),
			mapping.evidence...,
		) {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
				return nil, fmt.Errorf("%s traceability path %s: %w", id, path, err)
			}
		}
		requirements = append(requirements, requirement{
			ID:             id,
			Implementation: append([]string(nil), mapping.implementation...),
			Tests:          append([]string(nil), mapping.tests...),
			Evidence:       append([]string(nil), mapping.evidence...),
		})
	}
	sort.Slice(requirements, func(left, right int) bool {
		return requirements[left].ID < requirements[right].ID
	})

	traceability := document{
		SchemaVersion: schemaVersion,
		Spec:          "docs/spec/v1.md",
		Requirements:  requirements,
	}
	encoded, err := json.MarshalIndent(traceability, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode traceability JSON: %w", err)
	}
	encoded = append(encoded, '\n')
	return map[string][]byte{
		"release/traceability.json":                  encoded,
		"docs/operators/requirement-traceability.md": renderMarkdown(requirements),
	}, nil
}

func renderMarkdown(requirements []requirement) []byte {
	var output strings.Builder
	output.WriteString("# Normative requirement traceability\n\n")
	output.WriteString("This file is generated from `docs/spec/v1.md` by ")
	output.WriteString("`internal/tools/traceability`. Do not edit it by hand. ")
	output.WriteString("Each row links one normative requirement to its implementation, ")
	output.WriteString("tests, and release-evidence entry points. A workflow link identifies ")
	output.WriteString("the receipt-producing gate; a release decision still uses receipts ")
	output.WriteString("from the exact source revision being released.\n\n")
	output.WriteString("| Requirement | Implementation | Tests | Evidence gate |\n")
	output.WriteString("| --- | --- | --- | --- |\n")
	for _, requirement := range requirements {
		fmt.Fprintf(
			&output,
			"| `%s` | %s | %s | %s |\n",
			requirement.ID,
			renderLinks(requirement.Implementation),
			renderLinks(requirement.Tests),
			renderLinks(requirement.Evidence),
		)
	}
	return []byte(output.String())
}

func renderLinks(paths []string) string {
	links := make([]string, 0, len(paths))
	for _, path := range paths {
		links = append(
			links,
			fmt.Sprintf("[%s](../../%s)", filepath.Base(path), path),
		)
	}
	return strings.Join(links, "<br>")
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
