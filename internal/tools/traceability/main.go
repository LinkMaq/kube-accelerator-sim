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

var routes = buildRoutes()

func buildRoutes() map[string]route {
	result := map[string]route{}
	add := func(ids []string, value route) {
		for _, id := range ids {
			if _, duplicate := result[id]; duplicate {
				panic("duplicate requirement route " + id)
			}
			result[id] = value
		}
	}
	ci := []string{".github/workflows/ci.yml"}
	compatibility := []string{".github/workflows/compatibility.yml"}
	scale := []string{".github/workflows/scale.yml"}
	release := []string{".github/workflows/release.yml"}

	add(requirementRange("ARCH", 1, 8), route{
		implementation: []string{"docs/adr/0007-deep-modules-and-extension-seams.md", "internal/tools/archcheck/main.go"},
		tests:          []string{"test/architecture/architecture_test.go"},
		evidence:       ci,
	})
	add(requirementRange("CAT", 1, 5), route{
		implementation: []string{"profiles/catalog.json", "internal/catalog/catalog.go", "docs/research/vendor-model-evidence-catalog.md"},
		tests:          []string{"internal/catalog/catalog_test.go", "internal/catalog/schema_test.go"},
		evidence:       ci,
	})
	add([]string{"CAT-006"}, route{
		implementation: []string{"profiles/schema.json", "internal/catalog/catalog.go"},
		tests:          []string{"internal/catalog/schema_test.go", "internal/catalog/catalog_test.go"},
		evidence:       ci,
	})
	add([]string{"CAT-007"}, route{
		implementation: []string{"profiles/catalog.json", "profiles/embed.go"},
		tests:          []string{"internal/catalog/golden_test.go"},
		evidence:       ci,
	})
	add([]string{"CLI-001"}, route{
		implementation: []string{"internal/cli/cli.go", "internal/catalog/catalog.go", "internal/scenario/compiler.go"},
		tests:          []string{"internal/cli/cli_test.go", "test/contract/documentation_test.go"},
		evidence:       ci,
	})
	add(requirementRange("CLI", 2, 4), route{
		implementation: []string{"internal/cli/cli.go", "internal/scenario/compiler.go", "examples/heterogeneous.yaml"},
		tests:          []string{"internal/cli/cli_test.go", "internal/scenario/compile_test.go", "test/contract/documentation_test.go"},
		evidence:       ci,
	})
	add(requirementRange("CLI", 5, 9), route{
		implementation: []string{"internal/cli/cli.go", "internal/application/runtime.go", "internal/presentation/presentation.go"},
		tests:          []string{"internal/cli/cli_test.go", "internal/application/runtime_test.go", "internal/presentation/presentation_test.go"},
		evidence:       append(append([]string{}, compatibility...), scale...),
	})
	add(requirementRange("CLI", 10, 12), route{
		implementation: []string{"internal/cli/cli.go", "internal/application/runtime.go", "internal/reconcile/reconcile.go"},
		tests:          []string{"internal/cli/cli_test.go", "internal/application/runtime_test.go", "test/e2e/compatibility_test.go"},
		evidence:       compatibility,
	})
	add(requirementRange("COMPAT", 1, 6), route{
		implementation: []string{"release/compatibility-lock.json", "docs/operators/kubernetes-compatibility.md"},
		tests:          []string{"test/contract/compatibility_matrix_test.go", "test/e2e/compatibility_test.go", "test/e2e/dra_smoke_test.go"},
		evidence:       compatibility,
	})
	add(requirementRange("COMPAT", 7, 10), route{
		implementation: []string{".github/workflows/ci.yml", ".github/workflows/compatibility.yml", "release/compatibility-lock.json"},
		tests:          []string{"test/contract/compatibility_matrix_test.go"},
		evidence:       compatibility,
	})
	add(requirementRange("DOM", 1, 3), route{
		implementation: []string{"internal/domain/fidelity.go", "internal/domain/status.go", "internal/projection/projection.go"},
		tests:          []string{"internal/domain/fidelity_test.go", "internal/domain/status_test.go", "internal/projection/contract_test.go"},
		evidence:       ci,
	})
	add(requirementRange("DOM", 4, 9), route{
		implementation: []string{"internal/domain/scenario.go", "internal/catalog/catalog.go", "profiles/catalog.json"},
		tests:          []string{"internal/domain/scenario_test.go", "internal/catalog/catalog_test.go", "internal/catalog/custom_test.go"},
		evidence:       ci,
	})
	add(requirementRange("DOM", 10, 14), route{
		implementation: []string{"internal/catalog/catalog.go", "profiles/catalog.json"},
		tests:          []string{"internal/catalog/catalog_test.go", "internal/catalog/schema_test.go"},
		evidence:       ci,
	})
	add(requirementRange("DOM", 15, 18), route{
		implementation: []string{"internal/catalog/catalog.go", "profiles/catalog.json"},
		tests:          []string{"internal/catalog/catalog_test.go", "internal/catalog/golden_test.go"},
		evidence:       ci,
	})
	add(requirementRange("DRA", 1, 6), route{
		implementation: []string{"internal/projection/dra/dra.go", "internal/application/preflight.go"},
		tests:          []string{"internal/projection/dra/dra_test.go", "internal/projection/contract_test.go", "test/e2e/dra_smoke_test.go"},
		evidence:       []string{".github/workflows/compatibility.yml", ".github/workflows/_compatibility-dra-row.yml"},
	})
	add(requirementRange("LIFE", 1, 7), route{
		implementation: []string{"internal/application/runtime.go", "internal/controlplane/kubernetes/kubernetes.go", "api/simulation/v1alpha1/types.go"},
		tests:          []string{"internal/application/runtime_test.go", "internal/controlplane/controlplane_test.go", "api/simulation/v1alpha1/types_test.go"},
		evidence:       compatibility,
	})
	add(requirementRange("LIFE", 8, 10), route{
		implementation: []string{"internal/reconcile/reconcile.go", "internal/reconcile/kubernetes/delivery.go"},
		tests:          []string{"internal/reconcile/reconcile_test.go", "test/e2e/compatibility_test.go", "test/e2e/scale_test.go"},
		evidence:       append(append([]string{}, compatibility...), scale...),
	})
	add(requirementRange("OUT", 1, 5), route{
		implementation: []string{"internal/presentation/presentation.go", "internal/domain/diagnostic.go"},
		tests:          []string{"internal/presentation/presentation_test.go", "internal/cli/cli_test.go", "internal/presentation/testdata/error-invocation.json.golden"},
		evidence:       ci,
	})
	add(requirementRange("PACK", 1, 4), route{
		implementation: []string{"charts/kasim-runtime/Chart.yaml", "charts/kasim-runtime/templates/controller-deployment.yaml", "release/inputs.json"},
		tests:          []string{"test/packaging/chart_test.go", "test/e2e/chart_smoke_test.go"},
		evidence:       append(append([]string{}, ci...), release...),
	})
	add(requirementRange("PERF", 1, 9), route{
		implementation: []string{"test/e2e/testdata/reference-scale.yaml", "test/e2e/scale_test.go"},
		tests:          []string{"test/e2e/scale_test.go", "test/contract/scale_gate_test.go"},
		evidence:       scale,
	})
	add(requirementRange("PREFLIGHT", 1, 6), route{
		implementation: []string{"internal/application/preflight.go", "internal/application/runtime.go", "internal/cluster/kubernetes/adapter.go"},
		tests:          []string{"internal/application/preflight_test.go", "internal/cli/cli_test.go", "test/e2e/compatibility_test.go"},
		evidence:       compatibility,
	})
	add(requirementRange("PROTO", 1, 3), route{
		implementation: []string{"test/oracle/deviceplugin/plugin.go", "test/e2e/device_plugin_oracle_test.go", "docs/operators/kubelet-protocol-oracle.md"},
		tests:          []string{"test/oracle/deviceplugin/plugin_test.go", "test/e2e/device_plugin_oracle_test.go"},
		evidence:       []string{".github/workflows/protocol-oracle.yml"},
	})
	add([]string{"RBAC-001"}, route{
		implementation: []string{"charts/kasim-runtime/templates/controller-rbac.yaml", "charts/kasim-runtime/templates/kwok-rbac.yaml"},
		tests:          []string{"test/packaging/chart_test.go", "test/e2e/chart_smoke_test.go"},
		evidence:       ci,
	})
	add([]string{"RBAC-002"}, route{
		implementation: []string{"charts/kasim-runtime/templates/persona-rbac.yaml", "docs/operators/runtime-installation.md"},
		tests:          []string{"test/packaging/chart_test.go", "test/contract/documentation_test.go"},
		evidence:       ci,
	})
	add([]string{"RBAC-003"}, route{
		implementation: []string{"internal/application/preflight.go", "charts/kasim-runtime/templates/persona-rbac.yaml"},
		tests:          []string{"test/e2e/compatibility_test.go"},
		evidence:       compatibility,
	})
	add([]string{"RBAC-004"}, route{
		implementation: []string{"Dockerfile", "charts/kasim-runtime/templates/controller-deployment.yaml", "charts/kasim-runtime/templates/kwok-deployment.yaml"},
		tests:          []string{"test/packaging/chart_test.go", "test/e2e/chart_smoke_test.go"},
		evidence:       ci,
	})
	add(requirementRange("REL", 1, 2), route{
		implementation: []string{"go.mod", "go.sum", "release/inputs.json"},
		tests:          []string{"test/contract/version_test.go", "test/contract/compatibility_matrix_test.go"},
		evidence:       ci,
	})
	add([]string{"REL-003"}, route{
		implementation: []string{"cmd/kasim/main.go", "cmd/kasim-controller/main.go", "Makefile"},
		tests:          []string{"cmd/kasim/main_test.go", "cmd/kasim-controller/main_test.go"},
		evidence:       ci,
	})
	add([]string{"REL-004"}, route{
		implementation: []string{"internal/tools/releasebuild/main.go"},
		tests:          []string{"internal/tools/releasebuild/main_test.go", "test/release/smoke_test.go"},
		evidence:       release,
	})
	add([]string{"REL-005"}, route{
		implementation: []string{"Dockerfile", "charts/kasim-runtime/Chart.yaml", "internal/tools/releasebuild/main.go"},
		tests:          []string{"test/e2e/chart_smoke_test.go", "test/release/smoke_test.go"},
		evidence:       release,
	})
	add([]string{"REL-006"}, route{
		implementation: []string{"release/inputs.json", "internal/tools/releasebuild/main.go", "internal/tools/releaseevidence/main.go"},
		tests:          []string{"internal/tools/releasebuild/main_test.go", "internal/tools/releaseevidence/main_test.go"},
		evidence:       release,
	})
	add([]string{"REL-007"}, route{
		implementation: []string{"release/inputs.json", "charts/kasim-runtime/Chart.yaml", "docs/research/kwok-v0.8.0-runtime-lock.md"},
		tests:          []string{"test/packaging/chart_test.go", "internal/runtime/kwok/kwok_test.go"},
		evidence:       release,
	})
	add(requirementRange("SAFE", 1, 8), route{
		implementation: []string{"internal/reconcile/reconcile.go", "internal/cluster/kubernetes/adapter.go", "internal/application/runtime.go"},
		tests:          []string{"internal/reconcile/reconcile_test.go", "internal/cluster/kubernetes/adapter_test.go", "test/e2e/compatibility_test.go"},
		evidence:       append(append([]string{}, compatibility...), scale...),
	})
	add(requirementRange("SCHED", 1, 5), route{
		implementation: []string{"internal/projection/extended/extended.go", "internal/reconcile/reconcile.go"},
		tests:          []string{"internal/projection/extended/extended_test.go", "internal/projection/contract_test.go", "test/e2e/compatibility_test.go"},
		evidence:       compatibility,
	})
	add(requirementRange("SCN", 1, 8), route{
		implementation: []string{"internal/scenario/compiler.go", "internal/domain/scenario.go"},
		tests:          []string{"internal/scenario/compile_test.go", "test/contract/documentation_test.go"},
		evidence:       ci,
	})
	add(requirementRange("SCN", 9, 13), route{
		implementation: []string{"internal/scenario/compiler.go", "internal/projection/projection.go", "internal/cli/cli.go"},
		tests:          []string{"internal/scenario/compile_test.go", "internal/projection/contract_test.go", "internal/cli/cli_test.go"},
		evidence:       append(append([]string{}, ci...), compatibility...),
	})
	add([]string{"SCOPE-001"}, route{
		implementation: []string{"internal/scenario/compiler.go", "examples/heterogeneous.yaml", "test/e2e/testdata/reference-scale.yaml"},
		tests:          []string{"test/contract/documentation_test.go", "test/e2e/compatibility_test.go", "test/e2e/scale_test.go"},
		evidence:       append(append([]string{}, compatibility...), scale...),
	})
	add([]string{"SCOPE-002"}, route{
		implementation: []string{"internal/projection/extended/extended.go", "release/compatibility-lock.json"},
		tests:          []string{"internal/projection/extended/extended_test.go", "test/e2e/compatibility_test.go"},
		evidence:       compatibility,
	})
	add([]string{"SCOPE-003"}, route{
		implementation: []string{"internal/projection/dra/dra.go", "internal/application/preflight.go"},
		tests:          []string{"internal/projection/dra/dra_test.go", "test/e2e/dra_smoke_test.go"},
		evidence:       compatibility,
	})
	add(requirementRange("SCOPE", 4, 5), route{
		implementation: []string{"internal/domain/fidelity.go", "internal/domain/status.go", "README.md"},
		tests:          []string{"internal/domain/fidelity_test.go", "test/contract/documentation_test.go"},
		evidence:       ci,
	})
	add(requirementRange("SCOPE", 6, 7), route{
		implementation: []string{"internal/cli/cli.go", "internal/cluster/kubernetes/connect.go"},
		tests:          []string{"internal/cli/cli_test.go", "internal/cluster/kubernetes/connect_test.go"},
		evidence:       compatibility,
	})
	add([]string{"SCOPE-008"}, route{
		implementation: []string{"test/e2e/compatibility_test.go", "test/e2e/scale_test.go"},
		tests:          []string{"test/contract/compatibility_matrix_test.go", "test/contract/scale_gate_test.go"},
		evidence:       append(append([]string{}, compatibility...), scale...),
	})
	add([]string{"SCOPE-009"}, route{
		implementation: []string{"internal/cli/cli.go", "charts/kasim-runtime/Chart.yaml", "docs/operators/runtime-installation.md"},
		tests:          []string{"internal/cli/cli_test.go", "test/e2e/chart_smoke_test.go"},
		evidence:       ci,
	})
	add(requirementRange("STAT", 1, 5), route{
		implementation: []string{"internal/domain/status.go", "internal/presentation/presentation.go", "internal/reconcile/reconcile.go"},
		tests:          []string{"internal/domain/status_test.go", "internal/presentation/presentation_test.go", "internal/reconcile/reconcile_test.go"},
		evidence:       append(append([]string{}, compatibility...), scale...),
	})
	add(requirementRange("TARGET", 1, 5), route{
		implementation: []string{"internal/cluster/kubernetes/connect.go", "internal/cli/cli.go", "internal/cluster/cluster.go"},
		tests:          []string{"internal/cluster/kubernetes/connect_test.go", "internal/cli/cli_test.go", "test/e2e/compatibility_test.go"},
		evidence:       compatibility,
	})
	add([]string{"TEST-001"}, route{
		implementation: []string{"internal/scenario/compiler.go"},
		tests:          []string{"internal/scenario/compile_test.go"},
		evidence:       ci,
	})
	add([]string{"TEST-002"}, route{
		implementation: []string{"internal/catalog/catalog.go", "profiles/catalog.json"},
		tests:          []string{"internal/catalog/catalog_test.go", "internal/catalog/schema_test.go", "internal/catalog/golden_test.go"},
		evidence:       ci,
	})
	add([]string{"TEST-003"}, route{
		implementation: []string{"internal/application/runtime.go", "internal/controlplane/memory/memory.go"},
		tests:          []string{"internal/application/runtime_test.go"},
		evidence:       ci,
	})
	add([]string{"TEST-004"}, route{
		implementation: []string{"internal/reconcile/reconcile.go", "internal/cluster/recording/recording.go"},
		tests:          []string{"internal/reconcile/reconcile_test.go"},
		evidence:       ci,
	})
	add([]string{"TEST-005"}, route{
		implementation: []string{"internal/projection/projection.go", "internal/projection/extended/extended.go", "internal/projection/dra/dra.go"},
		tests:          []string{"internal/projection/contract_test.go"},
		evidence:       ci,
	})
	add([]string{"TEST-006"}, route{
		implementation: []string{"internal/presentation/presentation.go"},
		tests:          []string{"internal/presentation/presentation_test.go", "internal/presentation/testdata/error-invocation.human.golden", "internal/presentation/testdata/error-invocation.json.golden"},
		evidence:       ci,
	})
	add([]string{"TEST-007"}, route{
		implementation: []string{"test/e2e/compatibility_test.go", "test/e2e/chart_smoke_test.go"},
		tests:          []string{"test/e2e/compatibility_test.go", "test/e2e/chart_smoke_test.go"},
		evidence:       compatibility,
	})
	add([]string{"TEST-008"}, route{
		implementation: []string{"docs/adr/0007-deep-modules-and-extension-seams.md", "test/e2e/compatibility_test.go"},
		tests:          []string{"test/e2e/compatibility_test.go", "test/e2e/scale_test.go"},
		evidence:       append(append([]string{}, compatibility...), scale...),
	})
	add([]string{"TEST-009"}, route{
		implementation: []string{"internal/tools/releaseevidence/main.go", "release/traceability.json"},
		tests:          []string{"internal/tools/releaseevidence/main_test.go", "test/contract/documentation_test.go"},
		evidence:       release,
	})
	return result
}

func requirementRange(prefix string, first, last int) []string {
	result := make([]string, 0, last-first+1)
	for number := first; number <= last; number++ {
		result = append(result, fmt.Sprintf("%s-%03d", prefix, number))
	}
	return result
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
		mapping, ok := routes[id]
		if !ok {
			return nil, fmt.Errorf(
				"normative requirement %s has no requirement-specific traceability route",
				id,
			)
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
