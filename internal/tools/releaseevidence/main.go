// Command releaseevidence validates and normalizes the retained release-gate
// receipts before any release artifact can be built or published.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

const (
	compatibilityReceiptSchema = "kasim.io/compatibility-receipt/v1alpha1"
	protocolReceiptSchema      = "kasim.io/protocol-oracle-receipt/v1alpha1"
	scaleReceiptSchema         = "kasim.io/scale-receipt/v1alpha1"
	evidenceSummarySchema      = "kasim.io/release-evidence/v1alpha1"
)

var fullSourceRevision = regexp.MustCompile(`^[0-9a-f]{40}$`)

type options struct {
	Revision         string
	CompatibilityDir string
	ProtocolDir      string
	ScaleDir         string
	OutputDir        string
}

type receipt struct {
	Path     string
	Document map[string]any
	Encoded  []byte
}

type normalizedReceipt struct {
	Category      string `json:"category"`
	File          string `json:"file"`
	SHA256        string `json:"sha256"`
	SchemaVersion string `json:"schemaVersion"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "releaseevidence:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("releaseevidence", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	var configured options
	flags.StringVar(&configured.Revision, "revision", "", "exact source revision")
	flags.StringVar(
		&configured.CompatibilityDir,
		"compatibility-dir",
		"",
		"full compatibility receipts",
	)
	flags.StringVar(&configured.ProtocolDir, "protocol-dir", "", "protocol receipts")
	flags.StringVar(&configured.ScaleDir, "scale-dir", "", "scale receipts")
	flags.StringVar(&configured.OutputDir, "output", "", "normalized output")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %q", flags.Args())
	}
	if !fullSourceRevision.MatchString(configured.Revision) {
		return fmt.Errorf("sourceRevision %q must be a full Git SHA", configured.Revision)
	}
	for label, path := range map[string]string{
		"compatibility-dir": configured.CompatibilityDir,
		"protocol-dir":      configured.ProtocolDir,
		"scale-dir":         configured.ScaleDir,
		"output":            configured.OutputDir,
	} {
		if path == "" {
			return fmt.Errorf("%s is required", label)
		}
	}
	if err := requireEmptyOutput(configured.OutputDir); err != nil {
		return err
	}

	compatibility, err := readReceipts(configured.CompatibilityDir)
	if err != nil {
		return fmt.Errorf("compatibility receipts: %w", err)
	}
	protocol, err := readReceipts(configured.ProtocolDir)
	if err != nil {
		return fmt.Errorf("protocol receipts: %w", err)
	}
	scale, err := readReceipts(configured.ScaleDir)
	if err != nil {
		return fmt.Errorf("scale receipts: %w", err)
	}
	if err := validateCompatibility(compatibility, configured.Revision); err != nil {
		return err
	}
	if err := validateProtocol(protocol, configured.Revision); err != nil {
		return err
	}
	if err := validateScale(scale, configured.Revision); err != nil {
		return err
	}

	normalized := make([]normalizedReceipt, 0, len(compatibility)+len(protocol)+len(scale))
	for _, category := range []struct {
		name     string
		receipts []receipt
	}{
		{name: "compatibility", receipts: compatibility},
		{name: "protocol", receipts: protocol},
		{name: "scale", receipts: scale},
	} {
		for _, item := range category.receipts {
			name := category.name + "-" + filepath.Base(item.Path)
			target := filepath.Join(configured.OutputDir, name)
			if err := os.WriteFile(target, item.Encoded, 0o644); err != nil {
				return err
			}
			sum := sha256.Sum256(item.Encoded)
			normalized = append(normalized, normalizedReceipt{
				Category:      category.name,
				File:          name,
				SHA256:        hex.EncodeToString(sum[:]),
				SchemaVersion: stringField(item.Document, "schemaVersion"),
			})
		}
	}
	sort.Slice(normalized, func(left, right int) bool {
		return normalized[left].File < normalized[right].File
	})
	summary := map[string]any{
		"schemaVersion":  evidenceSummarySchema,
		"sourceRevision": configured.Revision,
		"requirements": []string{
			"full Kubernetes 1.30-1.36 scheduling matrix",
			"stable DRA Kubernetes 1.34-1.36 matrix",
			"kubelet protocol floor and ceiling",
			"two consecutive scale trials",
		},
		"counts": map[string]int{
			"compatibility": len(compatibility),
			"protocol":      len(protocol),
			"scale":         len(scale),
		},
		"receipts": normalized,
		"result":   "passed",
	}
	encoded, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return os.WriteFile(
		filepath.Join(configured.OutputDir, "release-evidence.json"),
		encoded,
		0o644,
	)
}

func requireEmptyOutput(path string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("output directory %q must be empty", path)
	}
	return nil
}

func readReceipts(root string) ([]receipt, error) {
	var paths []string
	if err := filepath.WalkDir(root, func(
		path string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && filepath.Ext(path) == ".json" {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil, errors.New("no JSON receipts found")
	}
	result := make([]receipt, 0, len(paths))
	for _, path := range paths {
		encoded, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var document map[string]any
		if err := json.Unmarshal(encoded, &document); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		result = append(result, receipt{
			Path:     path,
			Document: document,
			Encoded:  encoded,
		})
	}
	return result, nil
}

func validateCompatibility(receipts []receipt, revision string) error {
	expected := map[string]map[string]bool{
		"v1.30.14": {"scheduling": false},
		"v1.31.14": {"scheduling": false},
		"v1.32.13": {"scheduling": false},
		"v1.33.13": {"scheduling": false},
		"v1.34.10": {"scheduling": false, "dra-control-plane": false},
		"v1.35.7":  {"scheduling": false, "dra-control-plane": false},
		"v1.36.3":  {"scheduling": false, "dra-control-plane": false},
	}
	if len(receipts) != 10 {
		return fmt.Errorf("full compatibility gate requires 10 receipts, found %d", len(receipts))
	}
	for _, item := range receipts {
		if err := validateCommon(item, compatibilityReceiptSchema, revision); err != nil {
			return err
		}
		version := nestedString(item.Document, "kubernetes", "serverVersion")
		modes := nestedStrings(item.Document, "fidelity", "tested")
		if len(modes) != 1 {
			return fmt.Errorf("%s: expected exactly one tested fidelity", item.Path)
		}
		mode := modes[0]
		modeState, known := expected[version]
		if !known {
			return fmt.Errorf("%s: unexpected Kubernetes version %q", item.Path, version)
		}
		if _, known := modeState[mode]; !known {
			return fmt.Errorf("%s: unexpected fidelity %q for %s", item.Path, mode, version)
		}
		if modeState[mode] {
			return fmt.Errorf("%s: duplicate %s/%s receipt", item.Path, version, mode)
		}
		modeState[mode] = true
		if numberField(nestedMap(item.Document, "outcomes"), "ownedLiveObjects") != 0 {
			return fmt.Errorf("%s: ownedLiveObjects is not zero", item.Path)
		}
		required := []string{
			"placement",
			"exhaustion",
			"healthReduction",
			"controllerRecovery",
			"realNodeUnchanged",
			"realLeaseUnchanged",
		}
		if mode == "dra-control-plane" {
			required = []string{
				"stableDiscovery",
				"classSelection",
				"sliceInventory",
				"allocation",
				"reservation",
				"podBinding",
				"deviceReuse",
			}
		}
		if err := requireTrueOutcomes(item, required...); err != nil {
			return err
		}
	}
	for version, modes := range expected {
		for mode, present := range modes {
			if !present {
				return fmt.Errorf("missing compatibility receipt for %s/%s", version, mode)
			}
		}
	}
	return nil
}

func validateProtocol(receipts []receipt, revision string) error {
	expected := map[string]bool{"v1.30.14": false, "v1.36.3": false}
	if len(receipts) != len(expected) {
		return fmt.Errorf("protocol gate requires two receipts, found %d", len(receipts))
	}
	for _, item := range receipts {
		if err := validateCommon(item, protocolReceiptSchema, revision); err != nil {
			return err
		}
		version := nestedString(item.Document, "kubernetes", "serverVersion")
		if _, known := expected[version]; !known || expected[version] {
			return fmt.Errorf("%s: unexpected or duplicate protocol version %q", item.Path, version)
		}
		expected[version] = true
		if err := requireTrueOutcomes(
			item,
			"registration",
			"health-transition",
			"allocation",
			"plugin-restart",
			"socket-cleanup",
			"daemonset-cleanup",
			"pod-cleanup",
		); err != nil {
			return err
		}
		outcomes := nestedMap(item.Document, "outcomes")
		if numberField(outcomes, "capacity") != 2 ||
			numberField(outcomes, "allocatable") != 2 ||
			numberField(outcomes, "ownedLiveObjects") != 0 {
			return fmt.Errorf("%s: protocol counts are not exact", item.Path)
		}
	}
	return nil
}

func validateScale(receipts []receipt, revision string) error {
	if len(receipts) != 2 {
		return fmt.Errorf(
			"two consecutive scale trials require two receipts, found %d",
			len(receipts),
		)
	}
	trials := map[int]bool{1: false, 2: false}
	for _, item := range receipts {
		if err := validateCommon(item, scaleReceiptSchema, revision); err != nil {
			return err
		}
		trial := int(numberField(item.Document, "trial"))
		if _, known := trials[trial]; !known || trials[trial] {
			return fmt.Errorf("%s: unexpected or duplicate scale trial %d", item.Path, trial)
		}
		trials[trial] = true
		if numberField(item.Document, "trialsRequired") != 2 ||
			stringField(item.Document, "result") != "passed" {
			return fmt.Errorf("%s: scale trial did not pass the two-trial gate", item.Path)
		}
		scenario := nestedMap(item.Document, "scenario")
		if numberField(scenario, "syntheticNodes") != 1000 ||
			numberField(scenario, "accelerators") != 8000 ||
			numberField(scenario, "representativePods") != 100 {
			return fmt.Errorf("%s: scale topology counts are not exact", item.Path)
		}
		if err := requireTrueOutcomes(
			item,
			"applyReady",
			"observationP95",
			"healthLoss",
			"healthRecovery",
			"workload",
			"controllerRecovery",
			"cleanup",
		); err != nil {
			return err
		}
		outcomes := nestedMap(item.Document, "outcomes")
		if numberField(outcomes, "apiErrors") != 0 ||
			numberField(outcomes, "controllerErrors") != 0 ||
			numberField(outcomes, "controllerCrashes") != 0 ||
			numberField(outcomes, "ownedLiveObjects") != 0 ||
			boolField(outcomes, "identityDrift") ||
			boolField(outcomes, "observedCountReduction") {
			return fmt.Errorf("%s: scale safety outcome failed", item.Path)
		}
		thresholds := nestedMap(item.Document, "thresholds")
		measurements := nestedMap(item.Document, "measurements")
		for _, field := range []string{
			"applyReadySeconds",
			"observationP95Seconds",
			"healthLossSeconds",
			"healthRecoverySeconds",
			"workloadSeconds",
			"controllerRecoverySeconds",
			"cleanupSeconds",
			"controlPlanePeakBytes",
		} {
			threshold, thresholdPresent := numberValue(thresholds, field)
			measurement, measurementPresent := numberValue(measurements, field)
			if !thresholdPresent || threshold <= 0 ||
				!measurementPresent || measurement < 0 {
				return fmt.Errorf("%s: %s evidence is missing", item.Path, field)
			}
			if measurement > threshold {
				return fmt.Errorf("%s: %s exceeds threshold", item.Path, field)
			}
		}
	}
	return nil
}

func validateCommon(item receipt, schema, revision string) error {
	if stringField(item.Document, "schemaVersion") != schema {
		return fmt.Errorf("%s: wrong schemaVersion", item.Path)
	}
	if stringField(item.Document, "sourceRevision") != revision {
		return fmt.Errorf("%s: sourceRevision does not match release commit", item.Path)
	}
	checkedAt := stringField(item.Document, "checkedAt")
	if _, err := time.Parse(time.RFC3339, checkedAt); err != nil {
		return fmt.Errorf("%s: invalid checkedAt %q", item.Path, checkedAt)
	}
	return nil
}

func requireTrueOutcomes(item receipt, fields ...string) error {
	outcomes := nestedMap(item.Document, "outcomes")
	for _, field := range fields {
		if !boolField(outcomes, field) {
			return fmt.Errorf("%s: outcome %s is not true", item.Path, field)
		}
	}
	return nil
}

func nestedMap(document map[string]any, field string) map[string]any {
	value, _ := document[field].(map[string]any)
	return value
}

func nestedString(document map[string]any, object, field string) string {
	return stringField(nestedMap(document, object), field)
}

func nestedStrings(document map[string]any, object, field string) []string {
	values, _ := nestedMap(document, object)[field].([]any)
	result := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func stringField(document map[string]any, field string) string {
	value, _ := document[field].(string)
	return value
}

func numberField(document map[string]any, field string) float64 {
	value, _ := document[field].(float64)
	return value
}

func numberValue(document map[string]any, field string) (float64, bool) {
	value, present := document[field].(float64)
	return value, present
}

func boolField(document map[string]any, field string) bool {
	value, _ := document[field].(bool)
	return value
}

func fileSHA256(path string) (string, error) {
	input, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer input.Close()
	digester := sha256.New()
	if _, err := io.Copy(digester, input); err != nil {
		return "", err
	}
	return hex.EncodeToString(digester.Sum(nil)), nil
}
