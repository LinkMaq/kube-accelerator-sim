// Package telemetry implements source-backed synthetic Prometheus telemetry
// for Kasim-owned Synthetic Nodes. Vendor metric schemas are data, not code
// adapters; values are explicitly simulated and never claim physical sensors.
package telemetry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/LinkMaq/kube-accelerator-sim/telemetryprofiles"
)

const telemetryCatalogSchema = "v1alpha2"

var metricNamePattern = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
var labelNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

type catalogFile struct {
	SchemaVersion string          `json:"schemaVersion"`
	Revision      string          `json:"revision"`
	Profiles      []profileRecord `json:"profiles"`
}

type profileRecord struct {
	ID           string           `json:"id"`
	DisplayName  string           `json:"displayName"`
	State        string           `json:"state"`
	Exporter     string           `json:"exporter,omitempty"`
	Evidence     []evidenceRecord `json:"evidence,omitempty"`
	Models       []modelEnvelope  `json:"models,omitempty"`
	MetricFamily []metricFamily   `json:"metricFamilies,omitempty"`
	DeviceLabels []nativeLabel    `json:"deviceLabels,omitempty"`
	Defaults     simulationLimits `json:"defaults,omitempty"`
}

type evidenceRecord struct {
	Source    string `json:"source"`
	Revision  string `json:"revision"`
	CheckedAt string `json:"checkedAt"`
}

type modelEnvelope struct {
	ID         string  `json:"id"`
	NativeName string  `json:"nativeName,omitempty"`
	MemoryMiB  float64 `json:"memoryMiB,omitempty"`
}

type simulationLimits struct {
	MemoryMiB      float64 `json:"memoryMiB,omitempty"`
	IdlePowerW     float64 `json:"idlePowerW,omitempty"`
	MaxPowerW      float64 `json:"maxPowerW,omitempty"`
	IdleTempC      float64 `json:"idleTempC,omitempty"`
	MaxTempC       float64 `json:"maxTempC,omitempty"`
	CoreClockMHz   float64 `json:"coreClockMHz,omitempty"`
	MemoryClockMHz float64 `json:"memoryClockMHz,omitempty"`
}

type metricFamily struct {
	Name     string        `json:"name"`
	Type     string        `json:"type"`
	Help     string        `json:"help"`
	Semantic string        `json:"semantic"`
	Unit     string        `json:"unit"`
	Labels   []nativeLabel `json:"labels,omitempty"`
}

type nativeLabel struct {
	Name      string `json:"name"`
	ValueFrom string `json:"valueFrom"`
	Value     string `json:"value,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
}

type validatedFamily struct {
	metric metricFamily
	labels []nativeLabel
}

// Catalog is one immutable validated telemetry-contract snapshot.
type Catalog struct {
	revision string
	digest   string
	profiles map[string]profileRecord
}

// LoadBundled validates the exact embedded telemetry catalog.
func LoadBundled() (Catalog, error) {
	return loadCatalog(telemetryprofiles.CatalogJSON)
}

func loadCatalog(encoded []byte) (Catalog, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var file catalogFile
	if err := decoder.Decode(&file); err != nil {
		return Catalog{}, fmt.Errorf("decode telemetry catalog: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Catalog{}, fmt.Errorf("telemetry catalog must contain exactly one JSON document")
	}
	if file.SchemaVersion != telemetryCatalogSchema {
		return Catalog{}, fmt.Errorf("unsupported telemetry catalog schema %q", file.SchemaVersion)
	}
	if file.Revision == "" || len(file.Profiles) == 0 {
		return Catalog{}, fmt.Errorf("telemetry catalog requires revision and profiles")
	}
	profiles := make(map[string]profileRecord, len(file.Profiles))
	families := make(map[string]validatedFamily)
	for _, profile := range file.Profiles {
		if err := validateProfileRecord(profile, families); err != nil {
			return Catalog{}, fmt.Errorf("telemetry profile %q: %w", profile.ID, err)
		}
		if _, duplicate := profiles[profile.ID]; duplicate {
			return Catalog{}, fmt.Errorf("duplicate telemetry profile %q", profile.ID)
		}
		profiles[profile.ID] = cloneProfileRecord(profile)
	}
	sum := sha256.Sum256(encoded)
	return Catalog{
		revision: file.Revision,
		digest:   "sha256:" + hex.EncodeToString(sum[:]),
		profiles: profiles,
	}, nil
}

func validateProfileRecord(
	profile profileRecord,
	families map[string]validatedFamily,
) error {
	if profile.ID == "" || profile.DisplayName == "" {
		return fmt.Errorf("id and displayName are required")
	}
	switch profile.State {
	case "verified":
		if profile.Exporter == "" || len(profile.Evidence) == 0 ||
			len(profile.MetricFamily) == 0 || len(profile.DeviceLabels) == 0 {
			return fmt.Errorf("verified profile requires exporter, evidence, metrics, and labels")
		}
	case "provisional", "unavailable":
		if len(profile.MetricFamily) != 0 || len(profile.DeviceLabels) != 0 {
			return fmt.Errorf("%s profile must not enable metric families", profile.State)
		}
	default:
		return fmt.Errorf("unsupported state %q", profile.State)
	}
	for _, evidence := range profile.Evidence {
		if evidence.Source == "" || evidence.Revision == "" || evidence.CheckedAt == "" {
			return fmt.Errorf("evidence requires source, revision, and checkedAt")
		}
	}
	seenLabels := make(map[string]struct{}, len(profile.DeviceLabels))
	if err := validateNativeLabels(profile.DeviceLabels, seenLabels); err != nil {
		return err
	}
	seenFamilies := make(map[string]struct{}, len(profile.MetricFamily))
	for _, family := range profile.MetricFamily {
		if !metricNamePattern.MatchString(family.Name) {
			return fmt.Errorf("invalid metric name %q", family.Name)
		}
		if _, duplicate := seenFamilies[family.Name]; duplicate {
			return fmt.Errorf("duplicate metric family %q", family.Name)
		}
		seenFamilies[family.Name] = struct{}{}
		if family.Type != "gauge" && family.Type != "counter" {
			return fmt.Errorf("metric %q has unsupported type %q", family.Name, family.Type)
		}
		if strings.TrimSpace(family.Help) == "" || strings.ContainsAny(family.Help, "\r\n") {
			return fmt.Errorf("metric %q requires one exact single-line HELP string", family.Name)
		}
		if !supportedSemantic(family.Semantic) {
			return fmt.Errorf("metric %q has unsupported semantic %q", family.Name, family.Semantic)
		}
		familyLabels := make(map[string]struct{}, len(seenLabels)+len(family.Labels))
		for name := range seenLabels {
			familyLabels[name] = struct{}{}
		}
		if err := validateNativeLabels(family.Labels, familyLabels); err != nil {
			return fmt.Errorf("metric %q: %w", family.Name, err)
		}
		completeLabels := make([]nativeLabel, 0, len(profile.DeviceLabels)+len(family.Labels))
		completeLabels = append(completeLabels, profile.DeviceLabels...)
		completeLabels = append(completeLabels, family.Labels...)
		if previous, exists := families[family.Name]; exists &&
			(previous.metric.Type != family.Type || previous.metric.Help != family.Help ||
				previous.metric.Semantic != family.Semantic || previous.metric.Unit != family.Unit ||
				!slices.Equal(previous.labels, completeLabels)) {
			return fmt.Errorf("metric family %q conflicts across profiles", family.Name)
		}
		families[family.Name] = validatedFamily{metric: family, labels: completeLabels}
	}
	modelIDs := make(map[string]struct{}, len(profile.Models))
	for _, model := range profile.Models {
		if model.ID == "" || model.MemoryMiB < 0 {
			return fmt.Errorf("invalid model envelope %q", model.ID)
		}
		if _, duplicate := modelIDs[model.ID]; duplicate {
			return fmt.Errorf("duplicate model envelope %q", model.ID)
		}
		modelIDs[model.ID] = struct{}{}
	}
	if profile.Defaults.MemoryMiB < 0 || profile.Defaults.IdlePowerW < 0 ||
		profile.Defaults.MaxPowerW < profile.Defaults.IdlePowerW ||
		profile.Defaults.MaxTempC < profile.Defaults.IdleTempC {
		return fmt.Errorf("invalid simulation envelope")
	}
	return nil
}

func validateNativeLabels(labels []nativeLabel, seen map[string]struct{}) error {
	for _, label := range labels {
		if !labelNamePattern.MatchString(label.Name) || strings.HasPrefix(label.Name, "kasim_") {
			return fmt.Errorf("invalid or reserved native label %q", label.Name)
		}
		if _, duplicate := seen[label.Name]; duplicate {
			return fmt.Errorf("duplicate native label %q", label.Name)
		}
		seen[label.Name] = struct{}{}
		if !supportedLabelSource(label.ValueFrom) {
			return fmt.Errorf("native label %q has unsupported value source %q", label.Name, label.ValueFrom)
		}
		if label.ValueFrom == "fixed" && label.Value == "" {
			return fmt.Errorf("fixed native label %q requires value", label.Name)
		}
		if label.Prefix != "" && label.ValueFrom != "device-name" &&
			label.ValueFrom != "device-name-node-hash" && label.ValueFrom != "device-uuid" &&
			label.ValueFrom != "serial" {
			return fmt.Errorf("native label %q cannot prefix value source %q", label.Name, label.ValueFrom)
		}
	}
	return nil
}

func supportedLabelSource(source string) bool {
	return slices.Contains([]string{
		"device-index", "device-name", "device-name-node-hash", "device-uuid", "empty", "fixed",
		"health-error-code", "health-error-message", "health-message", "model-name",
		"node-name", "pci-bdf", "pci-bdf-domain8", "profile-name", "serial",
	}, source)
}

func supportedSemantic(semantic string) bool {
	return slices.Contains([]string{
		"clock-core", "clock-memory", "constant-zero", "correctable-error-count", "cycle-counter", "energy",
		"health-binary", "health-enflame", "health-one-healthy",
		"ib-physical-state", "ib-state", "info", "link-rate", "memory-free",
		"memory-free-reserved", "memory-ratio", "memory-reserved", "memory-total", "memory-used",
		"memory-utilization-percent", "packet-rx-counter",
		"packet-tx-counter", "power", "temperature",
		"traffic-rx-counter", "traffic-tx-counter", "throughput-rx", "throughput-tx",
		"uncorrectable-error-count", "utilization", "utilization-ratio", "utilization-sparse", "last-error",
		"voltage",
	}, semantic)
}

func cloneProfileRecord(input profileRecord) profileRecord {
	input.Evidence = append([]evidenceRecord(nil), input.Evidence...)
	input.Models = append([]modelEnvelope(nil), input.Models...)
	input.MetricFamily = append([]metricFamily(nil), input.MetricFamily...)
	for index := range input.MetricFamily {
		input.MetricFamily[index].Labels = append([]nativeLabel(nil), input.MetricFamily[index].Labels...)
	}
	input.DeviceLabels = append([]nativeLabel(nil), input.DeviceLabels...)
	return input
}

func (catalog Catalog) Revision() string { return catalog.revision }
func (catalog Catalog) Digest() string   { return catalog.digest }

func (catalog Catalog) profile(id string) (profileRecord, bool) {
	profile, found := catalog.profiles[id]
	return cloneProfileRecord(profile), found
}

// ProfileStates returns a stable profile-to-telemetry-state view for receipts
// and self-observation without exposing mutable catalog records.
func (catalog Catalog) ProfileStates() map[string]string {
	result := make(map[string]string, len(catalog.profiles))
	for id, profile := range catalog.profiles {
		result[id] = profile.State
	}
	return result
}
