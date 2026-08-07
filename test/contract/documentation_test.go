package contract_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
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
	"github.com/LinkMaq/kube-accelerator-sim/internal/scenario"
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
	byID := map[string]traceabilityRequirement{}
	for _, requirement := range document.Requirements {
		if _, duplicate := seen[requirement.ID]; duplicate {
			t.Errorf("requirement %s appears more than once", requirement.ID)
		}
		seen[requirement.ID] = struct{}{}
		byID[requirement.ID] = requirement
		got = append(got, requirement.ID)
		if len(requirement.Implemented) == 0 || len(requirement.Tests) == 0 ||
			len(requirement.Evidence) == 0 {
			t.Errorf("%s must link implementation, tests, and release evidence", requirement.ID)
		}
		for _, path := range traceabilityPaths(requirement) {
			assertRepositoryPath(t, root, requirement.ID, path)
		}
	}
	sort.Strings(got)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("traceability IDs do not exactly match the normative spec\nwant:\n%s\ngot:\n%s",
			strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
	for id, requiredPaths := range map[string][]string{
		"REL-001":   {"go.mod"},
		"SCOPE-008": {"test/e2e/compatibility_test.go"},
		"TEST-005":  {"internal/projection/contract_test.go"},
		"TEST-006":  {"internal/presentation/testdata/error-invocation.json.golden"},
		"TEST-007":  {"test/e2e/compatibility_test.go"},
		"TEST-009":  {"internal/tools/releaseevidence/main.go"},
	} {
		requirement, found := byID[id]
		if !found {
			t.Errorf("requirement-specific route %s is missing", id)
			continue
		}
		for _, requiredPath := range requiredPaths {
			if !containsDocumentationPath(
				traceabilityPaths(requirement),
				requiredPath,
			) {
				t.Errorf("%s does not link its specific contract %s", id, requiredPath)
			}
		}
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
		"docs/operators/simulated-vendor-telemetry.md",
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
		"execute accelerator compute",
		"install vendor drivers",
		"physical vendor telemetry",
		"simulate NUMA topology",
		"inject CDI devices",
		`kasim_simulated="true"`,
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
	for _, path := range documentedScenarioPaths(t, root) {
		path := path
		relative, err := filepath.Rel(filepath.Join(root, "examples"), path)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(relative, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			exit := cli.Run(
				[]string{
					"apply",
					"-f", path,
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

func TestExamplesCoverEverySelectableVendorResourceSignal(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	snapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	covered := make(map[string]struct{})
	coveredAuxiliary := make(map[string]struct{})
	for _, path := range documentedScenarioPaths(t, root) {
		encoded, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		input, err := scenario.Document(encoded)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		_, receipt, err := scenario.Compile(input, snapshot)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, resolution := range receipt.Resolutions() {
			covered[exampleSignalKey(
				resolution.ProfileDigest().String(),
				resolution.ContractID(),
				resolution.ResourceAlias(),
			)] = struct{}{}
		}
		for _, resolution := range receipt.AuxiliaryResolutions() {
			coveredAuxiliary[exampleSignalKey(
				resolution.ProfileDigest().String(),
				resolution.ContractID(),
				resolution.ResourceAlias(),
			)] = struct{}{}
		}
	}

	for _, summary := range snapshot.List() {
		profile, err := snapshot.Show(summary.ID())
		if err != nil {
			t.Fatal(err)
		}
		for _, contract := range profile.Contracts() {
			for _, resource := range contract.Resources() {
				if contract.Subject() == "auxiliary" {
					key := exampleSignalKey(
						profile.Digest().String(),
						contract.ID(),
						resource.Alias(),
					)
					if _, found := coveredAuxiliary[key]; !found {
						t.Errorf(
							"examples do not cover auxiliary signal %s %s/%s",
							profile.ID(), contract.ID(), resource.Alias(),
						)
					}
					continue
				}
				compatible := false
				for _, model := range profile.Models() {
					if model.Selectable() &&
						slices.Contains(model.Contracts(), contract.ID()) &&
						slices.Contains(model.ResourceAliases(), resource.Alias()) {
						compatible = true
						break
					}
				}
				if !compatible {
					continue
				}
				key := exampleSignalKey(
					profile.Digest().String(),
					contract.ID(),
					resource.Alias(),
				)
				if _, found := covered[key]; !found {
					t.Errorf(
						"examples do not cover selectable signal %s %s/%s (%s)",
						profile.ID(),
						contract.ID(),
						resource.Alias(),
						resource.Name(),
					)
				}
			}
		}
	}
}

func documentedScenarioPaths(t *testing.T, root string) []string {
	t.Helper()

	var paths []string
	examplesRoot := filepath.Join(root, "examples")
	err := filepath.WalkDir(
		examplesRoot,
		func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && filepath.Ext(path) == ".yaml" {
				paths = append(paths, path)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("list documented Scenarios: %v", err)
	}
	sort.Strings(paths)
	if len(paths) < 20 {
		t.Fatalf("expected broad documented Scenario coverage, got %d files", len(paths))
	}
	return paths
}

func exampleSignalKey(profileDigest, contractID, resourceAlias string) string {
	return strings.Join([]string{profileDigest, contractID, resourceAlias}, "\x00")
}

func TestDocumentedConnectedLifecycleCommandsExecute(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	dependencies := connectedDocumentationDependencies(t)
	created := runDocumentationCLI(t, dependencies, []string{
		"apply",
		"-f", filepath.Join(root, "examples/single-node-single-accelerator.yaml"),
		"--async",
		"--kubeconfig", "/explicit/config",
		"--context", "test-context",
		"-o", "json",
	})
	receipt := documentationReceipt(t, created)
	instanceUID := receipt["instanceUID"].(string)
	generation := jsonNumberString(t, receipt["desiredGeneration"])

	runDocumentationCLI(t, dependencies, []string{
		"status", "single-node-single-accelerator",
		"--kubeconfig", "/explicit/config",
		"--context", "test-context",
		"-o", "json",
	})
	health := runDocumentationCLI(t, dependencies, []string{
		"health", "single-node-single-accelerator",
		"--group", "workers",
		"--pool", "accelerator",
		"--healthy", "0",
		"--instance-uid", instanceUID,
		"--expected-generation", generation,
		"--async",
		"--kubeconfig", "/explicit/config",
		"--context", "test-context",
		"-o", "json",
	})
	generation = jsonNumberString(
		t,
		documentationReceipt(t, health)["desiredGeneration"],
	)
	scaled := runDocumentationCLI(t, dependencies, []string{
		"scale", "single-node-single-accelerator",
		"--group", "workers",
		"--replicas", "3",
		"--instance-uid", instanceUID,
		"--expected-generation", generation,
		"--async",
		"--kubeconfig", "/explicit/config",
		"--context", "test-context",
		"-o", "json",
	})
	generation = jsonNumberString(
		t,
		documentationReceipt(t, scaled)["desiredGeneration"],
	)
	runDocumentationCLI(t, dependencies, []string{
		"delete", "single-node-single-accelerator",
		"--instance-uid", instanceUID,
		"--expected-generation", generation,
		"--async",
		"--kubeconfig", "/explicit/config",
		"--context", "test-context",
		"-o", "json",
	})
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

func containsDocumentationPath(paths []string, expected string) bool {
	for _, path := range paths {
		if path == expected {
			return true
		}
	}
	return false
}

func traceabilityPaths(requirement traceabilityRequirement) []string {
	result := append([]string{}, requirement.Implemented...)
	result = append(result, requirement.Tests...)
	return append(result, requirement.Evidence...)
}

func connectedDocumentationDependencies(t *testing.T) cli.Dependencies {
	t.Helper()
	snapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := domain.ParseDigest("sha256:" + strings.Repeat("1", 64))
	if err != nil {
		t.Fatal(err)
	}
	target := controlplane.ExplicitTarget{
		ContextName: "test-context",
		Fingerprint: fingerprint,
	}
	connected := application.ConnectedTarget{
		Receipt: cluster.ConnectionReceipt{
			ContextName:             target.ContextName,
			CanonicalKubeconfigPath: "/explicit/config",
			APIServerURL:            "https://example.invalid",
			TargetFingerprint:       fingerprint,
			CADigest:                fingerprint,
		},
		Target:       target,
		ControlPlane: memory.New(memory.Options{HistoryLimit: 8}),
		Cluster: recording.New(recording.Options{
			Capabilities: cluster.TargetCapabilities{
				ServerVersion:   "v1.36.3",
				KubernetesMinor: 36,
			},
		}),
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

func runDocumentationCLI(
	t *testing.T,
	dependencies cli.Dependencies,
	args []string,
) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	exit := cli.RunWithDependencies(
		args,
		strings.NewReader(""),
		&stdout,
		&stderr,
		dependencies,
	)
	if exit != 0 {
		t.Fatalf("%s exited %d: %s", strings.Join(args, " "), exit, stderr.String())
	}
	return stdout.String()
}

func documentationReceipt(t *testing.T, encoded string) map[string]any {
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

func jsonNumberString(t *testing.T, value any) string {
	t.Helper()
	number, ok := value.(float64)
	if !ok || number < 1 || number != float64(uint64(number)) {
		t.Fatalf("unexpected positive generation %#v", value)
	}
	return strconv.FormatUint(uint64(number), 10)
}
