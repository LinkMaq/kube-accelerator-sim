package scenario_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/LinkMaq/kube-accelerator-sim/internal/catalog"
	"github.com/LinkMaq/kube-accelerator-sim/internal/scenario"
)

const validScenarioDocument = `
metadata:
  name: training-lab
spec:
  fidelity: scheduling
  acceptance:
    provisionalProfiles: false
  nodeGroups:
    - name: workers
      replicas: 1
      node:
        capacity:
          cpu: "64"
          memory: 256Gi
          pods: "110"
        placement:
          zone: lab-a
        labels:
          workload.example.com/class: training
        taints: []
      acceleratorPools:
        - name: training
          profile:
            id: nvidia
            revision: 2026-07-31
            digest: sha256:15fa27b98c21e0b3bc60661acd0b4835c7e16e5c8b5c949334048ca08f3731de
          model: nvidia-h100
          contract: device-plugin
          resource: gpu
          variant: {}
          count: 8
          healthy: 8
`

func TestCompileDocumentProducesCanonicalResolvedScenario(t *testing.T) {
	t.Parallel()

	catalogSnapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	input, err := scenario.Document([]byte(validScenarioDocument))
	if err != nil {
		t.Fatal(err)
	}

	compiled, receipt, err := scenario.Compile(input, catalogSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Scenario().Name().String() != "training-lab" {
		t.Fatalf("Scenario name = %q", compiled.Scenario().Name())
	}
	if compiled.Digest().String() == "" || len(compiled.Bytes()) == 0 {
		t.Fatal("canonical output or digest is empty")
	}
	if receipt.CatalogDigest() != catalogSnapshot.Digest() {
		t.Fatal("compile receipt lost catalog digest")
	}
	if len(receipt.Resolutions()) != 1 ||
		receipt.Resolutions()[0].ResourceName() != "nvidia.com/gpu" ||
		receipt.Resolutions()[0].ModelID() != "nvidia-h100" {
		t.Fatalf("unexpected resolution receipt: %#v", receipt.Resolutions())
	}
}

func TestCompileCanonicalGolden(t *testing.T) {
	t.Parallel()

	catalogSnapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	compiled := compileDocument(t, validScenarioDocument, catalogSnapshot)
	want, err := os.ReadFile("testdata/training-lab.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	want = bytes.TrimSpace(want)
	if !bytes.Equal(compiled.Bytes(), want) {
		t.Fatalf("canonical golden drifted:\n%s\n%s", compiled.Bytes(), want)
	}
	const wantDigest = "sha256:3b5cc90f218d720f5f0916f05b18c92805e7b88208ffaa32f7341e227e578972"
	if compiled.Digest().String() != wantDigest {
		t.Fatalf("digest = %s, want %s", compiled.Digest(), wantDigest)
	}
}

func TestCompileRejectsNonCanonicalOrStructurallyAmbiguousDocuments(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		document string
		want     string
	}{
		"unknown field": {
			document: strings.Replace(
				validScenarioDocument,
				"  fidelity: scheduling",
				"  fidelity: scheduling\n  backend: kwok",
				1,
			),
			want: "field backend not found",
		},
		"duplicate key": {
			document: strings.Replace(
				validScenarioDocument,
				"metadata:\n  name: training-lab",
				"metadata:\n  name: first\n  name: second",
				1,
			),
			want: "mapping key \"name\" already defined",
		},
		"multiple documents": {
			document: validScenarioDocument + "\n---\nmetadata: {}\n",
			want:     "exactly one document",
		},
		"non-canonical quantity": {
			document: strings.Replace(validScenarioDocument, `memory: 256Gi`, `memory: 262144Mi`, 1),
			want:     `quantity "262144Mi" is not canonical; use "256Gi"`,
		},
		"reserved label": {
			document: strings.Replace(
				validScenarioDocument,
				"workload.example.com/class: training",
				"kubernetes.io/hostname: forged",
				1,
			),
			want: `label "kubernetes.io/hostname" is reserved`,
		},
		"vendor identity label": {
			document: strings.Replace(
				validScenarioDocument,
				"workload.example.com/class: training",
				"nvidia.com/gpu.product: forged",
				1,
			),
			want: `label "nvidia.com/gpu.product" conflicts with vendor identity`,
		},
		"conflicting taints": {
			document: strings.Replace(
				validScenarioDocument,
				"        taints: []",
				`        taints:
          - key: accelerator
            value: required
            effect: NoSchedule
          - key: accelerator
            value: forbidden
            effect: NoSchedule`,
				1,
			),
			want: `conflicting taint "accelerator" with effect "NoSchedule"`,
		},
		"unsupported fidelity": {
			document: strings.Replace(
				validScenarioDocument,
				"fidelity: scheduling",
				"fidelity: device-plugin",
				1,
			),
			want: `unsupported Fidelity Mode "device-plugin"`,
		},
		"unpinned profile": {
			document: strings.Replace(
				validScenarioDocument,
				"sha256:15fa27b98c21e0b3bc60661acd0b4835c7e16e5c8b5c949334048ca08f3731de",
				"sha256:afd6878266ba81287632d5a0cc9d5fe8856d2839ac735a460929ba5d7f519705",
				1,
			),
			want: `revision or digest does not match`,
		},
		"invalid health": {
			document: strings.Replace(validScenarioDocument, "healthy: 8", "healthy: 9", 1),
			want:     "exceeds total count",
		},
	}

	catalogSnapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			input, err := scenario.Document([]byte(test.document))
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = scenario.Compile(input, catalogSnapshot)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Compile() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestCompileCanonicalOutputIgnoresSourceOrdering(t *testing.T) {
	t.Parallel()

	reordered := strings.Replace(
		validScenarioDocument,
		`        capacity:
          cpu: "64"
          memory: 256Gi
          pods: "110"
        placement:
          zone: lab-a
        labels:
          workload.example.com/class: training`,
		`        labels:
          workload.example.com/class: training
        placement:
          zone: lab-a
        capacity:
          pods: "110"
          memory: 256Gi
          cpu: "64"`,
		1,
	)
	catalogSnapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	first := compileDocument(t, validScenarioDocument, catalogSnapshot)
	second := compileDocument(t, reordered, catalogSnapshot)
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatalf("canonical bytes differ:\n%s\n%s", first.Bytes(), second.Bytes())
	}
	if first.Digest() != second.Digest() {
		t.Fatalf("canonical digests differ: %s != %s", first.Digest(), second.Digest())
	}
}

func TestCompileRejectsScalarResourceCollisionOnOneNode(t *testing.T) {
	t.Parallel()

	secondPool := `
        - name: inference
          profile:
            id: nvidia
            revision: 2026-07-31
            digest: sha256:15fa27b98c21e0b3bc60661acd0b4835c7e16e5c8b5c949334048ca08f3731de
          model: nvidia-h200
          contract: device-plugin
          resource: gpu
          variant: {}
          count: 2
          healthy: 2`
	document := validScenarioDocument + secondPool + "\n"
	catalogSnapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	input, err := scenario.Document([]byte(document))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = scenario.Compile(input, catalogSnapshot)
	if err == nil || !strings.Contains(err.Error(), `scalar resource "nvidia.com/gpu"`) {
		t.Fatalf("Compile() error = %v, want scalar resource collision", err)
	}
}

func TestCompileRejectsDuplicateNamesBeforeResourceResolution(t *testing.T) {
	t.Parallel()

	catalogSnapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	groupStart := strings.Index(validScenarioDocument, "    - name: workers")
	poolStart := strings.Index(validScenarioDocument, "        - name: training")
	tests := map[string]struct {
		document string
		want     string
	}{
		"group": {
			document: validScenarioDocument + validScenarioDocument[groupStart:],
			want:     `duplicate Node Group name "workers"`,
		},
		"pool": {
			document: validScenarioDocument + validScenarioDocument[poolStart:],
			want:     `duplicate Accelerator Pool name "training"`,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			input, err := scenario.Document([]byte(test.document))
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = scenario.Compile(input, catalogSnapshot)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Compile() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestCompileRejectsConflictingDRAIdentitySignalsOnOneNode(t *testing.T) {
	t.Parallel()

	document := strings.NewReplacer(
		"fidelity: scheduling",
		"fidelity: dra-control-plane",
		"contract: device-plugin",
		"contract: dra",
		"resource: gpu",
		"resource: device",
	).Replace(validScenarioDocument)
	document += `
        - name: inference
          profile:
            id: nvidia
            revision: 2026-07-31
            digest: sha256:15fa27b98c21e0b3bc60661acd0b4835c7e16e5c8b5c949334048ca08f3731de
          model: nvidia-h200
          contract: dra
          resource: device
          variant: {}
          count: 2
          healthy: 2
`
	catalogSnapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	input, err := scenario.Document([]byte(document))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = scenario.Compile(input, catalogSnapshot)
	if err == nil ||
		!strings.Contains(err.Error(), `DRA identity signal "gpu.nvidia.com/productName"`) {
		t.Fatalf("Compile() error = %v, want DRA identity signal conflict", err)
	}
}

func TestShortcutAndEquivalentDocumentCompileIdentically(t *testing.T) {
	t.Parallel()

	catalogSnapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	shortcut, err := scenario.Shortcut(scenario.ShortcutInput{
		Name:                "demo",
		ProfileID:           "nvidia",
		ModelID:             "nvidia-h100",
		Nodes:               3,
		AcceleratorsPerNode: 4,
		HealthyPerNode:      nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	fromShortcut, _, err := scenario.Compile(shortcut, catalogSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	equivalent := `
metadata:
  name: demo
spec:
  fidelity: scheduling
  acceptance:
    provisionalProfiles: false
  nodeGroups:
    - name: nodes
      replicas: 3
      node:
        capacity: {}
        placement: {}
        labels: {}
        taints: []
      acceleratorPools:
        - name: accelerators
          profile:
            id: nvidia
            revision: 2026-07-31
            digest: sha256:15fa27b98c21e0b3bc60661acd0b4835c7e16e5c8b5c949334048ca08f3731de
          model: nvidia-h100
          contract: device-plugin
          resource: gpu
          variant: {}
          count: 4
          healthy: 4
`
	fromDocument := compileDocument(t, equivalent, catalogSnapshot)
	if !bytes.Equal(fromShortcut.Bytes(), fromDocument.Bytes()) {
		t.Fatalf("shortcut and document bytes differ:\n%s\n%s", fromShortcut.Bytes(), fromDocument.Bytes())
	}
	if fromShortcut.Digest() != fromDocument.Digest() {
		t.Fatalf(
			"shortcut and document digests differ: %s != %s",
			fromShortcut.Digest(),
			fromDocument.Digest(),
		)
	}
}

func TestCompilationFailsClosedOnProvisionalAndAmbiguousCatalogChoices(t *testing.T) {
	t.Parallel()

	catalogSnapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	provisionalDocument := strings.NewReplacer(
		"id: nvidia",
		"id: kunlunxin-hami",
		"digest: sha256:15fa27b98c21e0b3bc60661acd0b4835c7e16e5c8b5c949334048ca08f3731de",
		"digest: sha256:5c5b606b7b3b84e37a816201869fbb24e558f15b28ff1a9b291772153cfe5e10",
		"model: nvidia-h100",
		"model: kunlunxin-p800",
		"contract: device-plugin",
		"contract: hami-device",
		"resource: gpu",
		"resource: xpu",
	).Replace(validScenarioDocument)
	provisional, err := scenario.Document([]byte(provisionalDocument))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := scenario.Compile(provisional, catalogSnapshot); err == nil ||
		!strings.Contains(err.Error(), "requires explicit acceptance") {
		t.Fatalf("provisional Compile() error = %v", err)
	}

	ambiguous, err := scenario.Shortcut(scenario.ShortcutInput{
		Name:                "ambiguous",
		ProfileID:           "amd",
		ModelID:             "amd-mi300x",
		Nodes:               1,
		AcceleratorsPerNode: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := scenario.Compile(ambiguous, catalogSnapshot); err == nil ||
		!strings.Contains(err.Error(), "resource alias is ambiguous") {
		t.Fatalf("ambiguous Compile() error = %v", err)
	}
}

func TestTypedRevisionsChangeOnlyTheSelectedField(t *testing.T) {
	t.Parallel()

	catalogSnapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	current := compileDocument(t, validScenarioDocument, catalogSnapshot)

	healthChange, err := scenario.Health("workers", "training", 3)
	if err != nil {
		t.Fatal(err)
	}
	healthRevision, healthReceipt, err := scenario.Revise(
		current,
		healthChange,
		catalogSnapshot,
	)
	if err != nil {
		t.Fatal(err)
	}
	wantHealth := bytes.Replace(current.Bytes(), []byte(`"healthy":8`), []byte(`"healthy":3`), 1)
	if !bytes.Equal(healthRevision.Bytes(), wantHealth) {
		t.Fatalf("health revision changed more than healthy:\n%s\n%s", healthRevision.Bytes(), wantHealth)
	}
	if healthRevision.Digest() == current.Digest() {
		t.Fatal("health revision retained the previous digest")
	}
	if healthReceipt.CatalogDigest() != catalogSnapshot.Digest() {
		t.Fatal("health revision receipt lost catalog identity")
	}

	scaleChange, err := scenario.Scale("workers", 5)
	if err != nil {
		t.Fatal(err)
	}
	scaleRevision, _, err := scenario.Revise(current, scaleChange, catalogSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	wantScale := bytes.Replace(current.Bytes(), []byte(`"replicas":1`), []byte(`"replicas":5`), 1)
	if !bytes.Equal(scaleRevision.Bytes(), wantScale) {
		t.Fatalf("scale revision changed more than replicas:\n%s\n%s", scaleRevision.Bytes(), wantScale)
	}
	if scaleRevision.Digest() == current.Digest() {
		t.Fatal("scale revision retained the previous digest")
	}
}

func TestTypedRevisionsRejectMissingTargetsAndInvalidCounts(t *testing.T) {
	t.Parallel()

	catalogSnapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	current := compileDocument(t, validScenarioDocument, catalogSnapshot)

	tests := map[string]struct {
		change scenario.TypedRevisionChange
		want   string
	}{
		"missing group": {
			change: mustHealth(t, "absent", "training", 1),
			want:   `Node Group "absent" was not found`,
		},
		"missing pool": {
			change: mustHealth(t, "workers", "absent", 1),
			want:   `Accelerator Pool "absent" was not found`,
		},
		"health exceeds count": {
			change: mustHealth(t, "workers", "training", 9),
			want:   "exceeds total count",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := scenario.Revise(current, test.change, catalogSnapshot)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Revise() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func mustHealth(
	t *testing.T,
	group string,
	pool string,
	healthy int64,
) scenario.TypedRevisionChange {
	t.Helper()
	change, err := scenario.Health(group, pool, healthy)
	if err != nil {
		t.Fatal(err)
	}
	return change
}

func compileDocument(
	t *testing.T,
	document string,
	catalogSnapshot catalog.Snapshot,
) scenario.CanonicalScenario {
	t.Helper()
	input, err := scenario.Document([]byte(document))
	if err != nil {
		t.Fatal(err)
	}
	compiled, _, err := scenario.Compile(input, catalogSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func FuzzCompileDocumentBoundaries(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(validScenarioDocument),
		[]byte("metadata:\n  name: first\n  name: second\n"),
		[]byte(validScenarioDocument + "\nunknown: true\n"),
		[]byte(strings.Replace(validScenarioDocument, "memory: 256Gi", "memory: 262144Mi", 1)),
	} {
		f.Add(seed)
	}
	catalogSnapshot, err := catalog.LoadBundled()
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, encoded []byte) {
		firstInput, firstInputErr := scenario.Document(encoded)
		secondInput, secondInputErr := scenario.Document(encoded)
		if errorText(firstInputErr) != errorText(secondInputErr) {
			t.Fatalf("Document() is non-deterministic: %v != %v", firstInputErr, secondInputErr)
		}
		if firstInputErr != nil {
			return
		}
		first, _, firstErr := scenario.Compile(firstInput, catalogSnapshot)
		second, _, secondErr := scenario.Compile(secondInput, catalogSnapshot)
		if errorText(firstErr) != errorText(secondErr) {
			t.Fatalf("Compile() errors are non-deterministic: %v != %v", firstErr, secondErr)
		}
		if firstErr == nil &&
			(!bytes.Equal(first.Bytes(), second.Bytes()) || first.Digest() != second.Digest()) {
			t.Fatal("Compile() output is non-deterministic")
		}
	})
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
