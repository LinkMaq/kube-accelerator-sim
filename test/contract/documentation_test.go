package contract_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/LinkMaq/kube-accelerator-sim/internal/cli"
)

type traceabilityDocument struct {
	SchemaVersion string                    `json:"schemaVersion"`
	Spec          string                    `json:"spec"`
	Requirements  []traceabilityRequirement `json:"requirements"`
}

type traceabilityRequirement struct {
	ID          string   `json:"id"`
	Implemented []string `json:"implementation"`
	Tests       []string `json:"tests"`
	Evidence    []string `json:"evidence"`
	DeferralADR string   `json:"deferralADR,omitempty"`
}

func TestEveryNormativeRequirementHasReviewableTraceability(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	spec := readDocumentationFile(t, filepath.Join(root, "docs/spec/v1.md"))
	requirementPattern := regexp.MustCompile(`\*\*([A-Z]+-[0-9]{3})\*\*`)
	matches := requirementPattern.FindAllStringSubmatch(spec, -1)
	if len(matches) == 0 {
		t.Fatal("normative specification contains no requirement IDs")
	}
	want := make([]string, 0, len(matches))
	for _, match := range matches {
		want = append(want, match[1])
	}
	sort.Strings(want)

	encoded := readDocumentationFile(t, filepath.Join(root, "release/traceability.json"))
	var document traceabilityDocument
	if err := json.Unmarshal([]byte(encoded), &document); err != nil {
		t.Fatalf("decode release/traceability.json: %v", err)
	}
	if document.SchemaVersion != "kasim.io/requirement-traceability/v1alpha1" {
		t.Errorf("unexpected traceability schema %q", document.SchemaVersion)
	}
	if document.Spec != "docs/spec/v1.md" {
		t.Errorf("unexpected governing spec %q", document.Spec)
	}

	got := make([]string, 0, len(document.Requirements))
	seen := map[string]struct{}{}
	for _, requirement := range document.Requirements {
		if _, duplicate := seen[requirement.ID]; duplicate {
			t.Errorf("requirement %s appears more than once", requirement.ID)
		}
		seen[requirement.ID] = struct{}{}
		got = append(got, requirement.ID)
		if requirement.DeferralADR != "" {
			assertRepositoryPath(t, root, requirement.ID, requirement.DeferralADR)
			if !strings.HasPrefix(requirement.DeferralADR, "docs/adr/") {
				t.Errorf("%s deferral is not an accepted ADR: %s", requirement.ID, requirement.DeferralADR)
			}
			continue
		}
		if len(requirement.Implemented) == 0 || len(requirement.Tests) == 0 ||
			len(requirement.Evidence) == 0 {
			t.Errorf("%s must link implementation, tests, and release evidence", requirement.ID)
		}
		for _, path := range append(
			append(append([]string{}, requirement.Implemented...), requirement.Tests...),
			requirement.Evidence...,
		) {
			assertRepositoryPath(t, root, requirement.ID, path)
		}
	}
	sort.Strings(got)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("traceability IDs do not exactly match the normative spec\nwant:\n%s\ngot:\n%s",
			strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
}

func TestOperatorDocumentationIsExplicitAndCapabilityBounded(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	required := []string{
		"README.md",
		"docs/operators/quickstart.md",
		"docs/operators/scenario-examples.md",
		"docs/operators/upgrade-rollback.md",
		"docs/operators/troubleshooting-security.md",
		"docs/operators/profile-evidence.md",
		"docs/operators/requirement-traceability.md",
		"docs/operators/final-audit.md",
	}
	var combined strings.Builder
	for _, path := range required {
		combined.WriteString(readDocumentationFile(t, filepath.Join(root, path)))
		combined.WriteByte('\n')
	}
	docs := combined.String()
	for _, requiredText := range []string{
		"--kubeconfig ./target.kubeconfig",
		"--context target",
		"--kube-context target",
		"1.30–1.36",
		"1.34–1.36",
		"does not provide device access",
		"does not execute accelerator compute",
		"does not install vendor drivers",
		"does not provide vendor telemetry",
		"does not simulate NUMA topology",
		"does not inject CDI devices",
	} {
		if !strings.Contains(docs, requiredText) {
			t.Errorf("operator documentation is missing boundary or explicit target text %q", requiredText)
		}
	}
	for _, forbidden := range []string{
		"kasim cluster",
		"kind create cluster",
		"minikube start",
		"kwokctl create cluster",
		"supports Kubernetes 1.30+",
	} {
		if strings.Contains(docs, forbidden) {
			t.Errorf("operator documentation contains forbidden product/lifecycle claim %q", forbidden)
		}
	}
}

func TestDocumentedScenariosCompileThroughTheProductCLI(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	for _, path := range []string{
		"examples/single-node-single-accelerator.yaml",
		"examples/single-node-multi-accelerator.yaml",
		"examples/multi-node-multi-accelerator.yaml",
		"examples/heterogeneous.yaml",
		"examples/dra-control-plane.yaml",
	} {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			exit := cli.Run(
				[]string{
					"apply",
					"-f", filepath.Join(root, path),
					"--dry-run=client",
					"-o", "json",
				},
				strings.NewReader(""),
				&stdout,
				&stderr,
			)
			if exit != 0 {
				t.Fatalf("client dry-run failed with exit %d: %s", exit, stderr.String())
			}
			if !strings.Contains(stdout.String(), `"kind": "ScenarioCompile"`) {
				t.Errorf("client dry-run did not emit a ScenarioCompile receipt: %s", stdout.String())
			}
		})
	}
}

func TestLocalMarkdownLinksResolve(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	linkPattern := regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)
	paths := []string{"README.md", "charts/kasim-runtime/README.md"}
	operatorDocs, err := filepath.Glob(filepath.Join(root, "docs/operators/*.md"))
	if err != nil {
		t.Fatalf("list operator documentation: %v", err)
	}
	for _, path := range operatorDocs {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatalf("make %s relative: %v", path, err)
		}
		paths = append(paths, relative)
	}
	for _, relative := range paths {
		encoded := readDocumentationFile(t, filepath.Join(root, relative))
		for _, match := range linkPattern.FindAllStringSubmatch(encoded, -1) {
			target := strings.Trim(match[1], "<>")
			target = strings.SplitN(target, "#", 2)[0]
			if target == "" || strings.Contains(target, "://") ||
				strings.HasPrefix(target, "mailto:") {
				continue
			}
			if fields := strings.Fields(target); len(fields) != 0 {
				target = fields[0]
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(filepath.Join(root, relative)), target))
			if _, err := os.Stat(resolved); err != nil {
				t.Errorf("%s links to missing local target %s: %v", relative, target, err)
			}
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return root
}

func readDocumentationFile(t *testing.T, path string) string {
	t.Helper()
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(encoded)
}

func assertRepositoryPath(t *testing.T, root, requirement, path string) {
	t.Helper()
	if path == "" || filepath.IsAbs(path) || strings.Contains(path, "..") {
		t.Errorf("%s has unsafe repository path %q", requirement, path)
		return
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
		t.Errorf("%s links missing repository path %s: %v", requirement, path, err)
	}
}
