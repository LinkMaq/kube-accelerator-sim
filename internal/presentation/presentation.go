// Package presentation owns the versioned CLI output envelope, maintained
// output-format variants, secret redaction, and terminal layouts.
package presentation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

const schemaVersion = "v1alpha1"

const (
	humanFormat = "human"
	jsonFormat  = "json"
	yamlFormat  = "yaml"
)

var bearerPattern = regexp.MustCompile(`(?i)\bBearer[ \t]+[A-Za-z0-9._~+/=-]+`)
var secretAssignmentPattern = regexp.MustCompile(
	`(?i)\b(token|password|passwd|secret|client[-_ ]?key|private[-_ ]?key|authorization|credential)\b[ \t]*[:=][ \t]*[^ \t\r\n,;]+`,
)
var privateKeyPattern = regexp.MustCompile(
	`(?s)-----BEGIN [^-\r\n]*PRIVATE KEY-----.*?-----END [^-\r\n]*PRIVATE KEY-----`,
)

// OutputFormat is the sealed set of maintained presentation variants.
type OutputFormat struct {
	value string
}

// ParseOutputFormat rejects formatter plugin names and unsupported aliases.
func ParseOutputFormat(value string) (OutputFormat, error) {
	switch value {
	case humanFormat, jsonFormat, yamlFormat:
		return OutputFormat{value: value}, nil
	default:
		return OutputFormat{}, fmt.Errorf("unsupported output format %q", value)
	}
}

// OutputEnvelope is the one versioned command result passed to Render.
type OutputEnvelope struct {
	APIVersion string      `json:"apiVersion" yaml:"apiVersion"`
	Kind       string      `json:"kind" yaml:"kind"`
	Command    string      `json:"command" yaml:"command"`
	Status     string      `json:"status" yaml:"status"`
	Result     Result      `json:"result,omitempty" yaml:"result,omitempty"`
	Diagnostic *Diagnostic `json:"diagnostic,omitempty" yaml:"diagnostic,omitempty"`
}

// Result is the sealed set of maintained presentation result schemas.
type Result interface {
	presentationResult()
}

// Diagnostic is the stable machine-facing error contract.
type Diagnostic struct {
	Code             string `json:"code" yaml:"code"`
	Message          string `json:"message" yaml:"message"`
	Retryable        bool   `json:"retryable" yaml:"retryable"`
	RevisionAccepted bool   `json:"revisionAccepted" yaml:"revisionAccepted"`
	ExitCategory     int    `json:"exitCategory" yaml:"exitCategory"`
}

// VersionResult is the stable version command result schema.
type VersionResult struct {
	Binary            string `json:"binary" yaml:"binary"`
	ProductVersion    string `json:"productVersion" yaml:"productVersion"`
	SourceRevision    string `json:"sourceRevision" yaml:"sourceRevision"`
	BuildDate         string `json:"buildDate" yaml:"buildDate"`
	SchemaVersion     string `json:"schemaVersion" yaml:"schemaVersion"`
	CatalogVersion    string `json:"catalogVersion" yaml:"catalogVersion"`
	KubernetesFloor   string `json:"kubernetesFloor" yaml:"kubernetesFloor"`
	KubernetesCeiling string `json:"kubernetesCeiling" yaml:"kubernetesCeiling"`
}

func (VersionResult) presentationResult() {}

// ProfileSummaryResult is one stable offline profile list row.
type ProfileSummaryResult struct {
	ID          string `json:"id" yaml:"id"`
	DisplayName string `json:"displayName" yaml:"displayName"`
	Class       string `json:"class" yaml:"class"`
	Revision    string `json:"revision" yaml:"revision"`
	Digest      string `json:"digest" yaml:"digest"`
}

// ProfileListResult is the complete immutable bundled catalog list receipt.
type ProfileListResult struct {
	CatalogRevision string                 `json:"catalogRevision" yaml:"catalogRevision"`
	CatalogDigest   string                 `json:"catalogDigest" yaml:"catalogDigest"`
	Profiles        []ProfileSummaryResult `json:"profiles" yaml:"profiles"`
}

func (ProfileListResult) presentationResult() {}

// EvidenceResult is one source and exact reviewed revision.
type EvidenceResult struct {
	ID        string `json:"id" yaml:"id"`
	Grade     string `json:"grade" yaml:"grade"`
	Source    string `json:"source" yaml:"source"`
	Revision  string `json:"revision" yaml:"revision"`
	CheckedAt string `json:"checkedAt" yaml:"checkedAt"`
}

// ResourceResult is one portable alias and exact Kubernetes-facing name.
type ResourceResult struct {
	Alias string `json:"alias" yaml:"alias"`
	Name  string `json:"name" yaml:"name"`
	Unit  string `json:"unit" yaml:"unit"`
}

// IdentitySignalResult is one source-backed vendor identity key.
type IdentitySignalResult struct {
	Kind string `json:"kind" yaml:"kind"`
	Key  string `json:"key" yaml:"key"`
}

// ContractResult is one complete offline Resource Contract view.
type ContractResult struct {
	ID              string                 `json:"id" yaml:"id"`
	Kind            string                 `json:"kind" yaml:"kind"`
	ProviderScope   string                 `json:"providerScope" yaml:"providerScope"`
	FidelityModes   []string               `json:"fidelityModes" yaml:"fidelityModes"`
	Resources       []ResourceResult       `json:"resources" yaml:"resources"`
	IdentitySignals []IdentitySignalResult `json:"identitySignals" yaml:"identitySignals"`
	Capabilities    map[string]string      `json:"capabilities" yaml:"capabilities"`
	EvidenceRefs    []string               `json:"evidenceRefs" yaml:"evidenceRefs"`
}

// ModelResult is one complete offline Accelerator Model view.
type ModelResult struct {
	ID              string   `json:"id" yaml:"id"`
	DisplayName     string   `json:"displayName" yaml:"displayName"`
	Aliases         []string `json:"aliases" yaml:"aliases"`
	Lifecycle       string   `json:"lifecycle" yaml:"lifecycle"`
	Selectable      bool     `json:"selectable" yaml:"selectable"`
	Contracts       []string `json:"contracts" yaml:"contracts"`
	ResourceAliases []string `json:"resourceAliases" yaml:"resourceAliases"`
	EvidenceRefs    []string `json:"evidenceRefs" yaml:"evidenceRefs"`
}

// ProfileResult is one pinned profile, its evidence, contracts, and models.
type ProfileResult struct {
	ProfileSummaryResult `yaml:",inline"`
	Evidence             []EvidenceResult `json:"evidence" yaml:"evidence"`
	Contracts            []ContractResult `json:"contracts" yaml:"contracts"`
	Models               []ModelResult    `json:"models" yaml:"models"`
}

func (ProfileResult) presentationResult() {}

// ResolutionResult is one exact catalog selection used for compilation.
type ResolutionResult struct {
	ProfileClass  string           `json:"profileClass" yaml:"profileClass"`
	ProfileDigest string           `json:"profileDigest" yaml:"profileDigest"`
	ModelID       string           `json:"modelID" yaml:"modelID"`
	ContractID    string           `json:"contractID" yaml:"contractID"`
	ResourceAlias string           `json:"resourceAlias" yaml:"resourceAlias"`
	ResourceName  string           `json:"resourceName" yaml:"resourceName"`
	Evidence      []EvidenceResult `json:"evidence" yaml:"evidence"`
}

// ScenarioCompileResult is the complete client dry-run compile receipt.
type ScenarioCompileResult struct {
	ScenarioName      string             `json:"scenarioName" yaml:"scenarioName"`
	ScenarioDigest    string             `json:"scenarioDigest" yaml:"scenarioDigest"`
	CatalogDigest     string             `json:"catalogDigest" yaml:"catalogDigest"`
	Resolutions       []ResolutionResult `json:"resolutions" yaml:"resolutions"`
	CanonicalScenario any                `json:"canonicalScenario" yaml:"canonicalScenario"`
}

func (ScenarioCompileResult) presentationResult() {}

// Success constructs one successful versioned envelope.
func Success(kind, command string, result Result) OutputEnvelope {
	return OutputEnvelope{
		APIVersion: schemaVersion,
		Kind:       kind,
		Command:    command,
		Status:     "success",
		Result:     result,
	}
}

// Failure constructs one stable diagnostic envelope from a domain outcome.
func Failure(command string, diagnostic domain.Diagnostic) OutputEnvelope {
	return OutputEnvelope{
		APIVersion: schemaVersion,
		Kind:       "Diagnostic",
		Command:    command,
		Status:     "failure",
		Diagnostic: &Diagnostic{
			Code:             diagnostic.Code().String(),
			Message:          diagnostic.Message(),
			Retryable:        diagnostic.Retryable(),
			RevisionAccepted: diagnostic.RevisionAccepted(),
			ExitCategory:     diagnostic.ExitCategory().Code(),
		},
	}
}

// Render returns one complete newline-terminated output payload.
func Render(envelope OutputEnvelope, format OutputFormat) ([]byte, error) {
	envelope = redactEnvelope(envelope)
	switch format.value {
	case humanFormat:
		return renderHuman(envelope)
	case jsonFormat:
		encoded, err := json.MarshalIndent(envelope, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("render JSON output: %w", err)
		}
		return append(encoded, '\n'), nil
	case yamlFormat:
		var buffer bytes.Buffer
		encoder := yaml.NewEncoder(&buffer)
		encoder.SetIndent(2)
		if err := encoder.Encode(envelope); err != nil {
			return nil, fmt.Errorf("render YAML output: %w", err)
		}
		if err := encoder.Close(); err != nil {
			return nil, fmt.Errorf("close YAML output: %w", err)
		}
		return buffer.Bytes(), nil
	default:
		return nil, fmt.Errorf("invalid output format")
	}
}

func renderHuman(envelope OutputEnvelope) ([]byte, error) {
	if envelope.Diagnostic != nil {
		return []byte(fmt.Sprintf(
			"Error [%s]: %s\n",
			envelope.Diagnostic.Code,
			envelope.Diagnostic.Message,
		)), nil
	}
	switch result := envelope.Result.(type) {
	case VersionResult:
		return []byte(fmt.Sprintf(
			"%s %s (commit=%s, built=%s)\nschema=%s catalog=%s kubernetes=%s-%s\n",
			result.Binary,
			result.ProductVersion,
			result.SourceRevision,
			result.BuildDate,
			result.SchemaVersion,
			result.CatalogVersion,
			result.KubernetesFloor,
			result.KubernetesCeiling,
		)), nil
	case ProfileListResult:
		var output strings.Builder
		fmt.Fprintf(
			&output,
			"Catalog %s (%s)\nID\tCLASS\tDISPLAY NAME\n",
			result.CatalogRevision,
			result.CatalogDigest,
		)
		for _, profile := range result.Profiles {
			fmt.Fprintf(
				&output,
				"%s\t%s\t%s\n",
				profile.ID,
				profile.Class,
				profile.DisplayName,
			)
		}
		return []byte(output.String()), nil
	case ProfileResult:
		var output strings.Builder
		fmt.Fprintf(
			&output,
			"%s (%s)\nID: %s\nRevision: %s\nDigest: %s\n",
			result.DisplayName,
			result.Class,
			result.ID,
			result.Revision,
			result.Digest,
		)
		fmt.Fprintf(
			&output,
			"Evidence: %d  Contracts: %d  Models: %d\n",
			len(result.Evidence),
			len(result.Contracts),
			len(result.Models),
		)
		for _, contract := range result.Contracts {
			fmt.Fprintf(&output, "Contract %s (%s):", contract.ID, contract.Kind)
			for _, resource := range contract.Resources {
				fmt.Fprintf(&output, " %s=%s", resource.Alias, resource.Name)
			}
			output.WriteByte('\n')
		}
		return []byte(output.String()), nil
	case ScenarioCompileResult:
		var output strings.Builder
		fmt.Fprintf(
			&output,
			"Scenario %s compiled (client dry-run)\nDigest: %s\nCatalog: %s\n",
			result.ScenarioName,
			result.ScenarioDigest,
			result.CatalogDigest,
		)
		for _, resolution := range result.Resolutions {
			fmt.Fprintf(
				&output,
				"Resolution: %s/%s %s/%s -> %s\n",
				resolution.ProfileClass,
				resolution.ModelID,
				resolution.ContractID,
				resolution.ResourceAlias,
				resolution.ResourceName,
			)
		}
		return []byte(output.String()), nil
	default:
		return nil, fmt.Errorf("unsupported human output kind %q", envelope.Kind)
	}
}

func redactEnvelope(envelope OutputEnvelope) OutputEnvelope {
	if envelope.Diagnostic != nil {
		diagnostic := *envelope.Diagnostic
		diagnostic.Message = redactString(diagnostic.Message)
		envelope.Diagnostic = &diagnostic
	}
	switch result := envelope.Result.(type) {
	case VersionResult:
		result.Binary = redactString(result.Binary)
		result.ProductVersion = redactString(result.ProductVersion)
		result.SourceRevision = redactString(result.SourceRevision)
		result.BuildDate = redactString(result.BuildDate)
		result.SchemaVersion = redactString(result.SchemaVersion)
		result.CatalogVersion = redactString(result.CatalogVersion)
		result.KubernetesFloor = redactString(result.KubernetesFloor)
		result.KubernetesCeiling = redactString(result.KubernetesCeiling)
		envelope.Result = result
	case ProfileListResult:
		result.CatalogRevision = redactString(result.CatalogRevision)
		result.CatalogDigest = redactString(result.CatalogDigest)
		for index := range result.Profiles {
			redactProfileSummary(&result.Profiles[index])
		}
		envelope.Result = result
	case ProfileResult:
		redactProfileSummary(&result.ProfileSummaryResult)
		for index := range result.Evidence {
			redactEvidence(&result.Evidence[index])
		}
		for index := range result.Contracts {
			redactContract(&result.Contracts[index])
		}
		for index := range result.Models {
			redactModel(&result.Models[index])
		}
		envelope.Result = result
	case ScenarioCompileResult:
		result.ScenarioName = redactString(result.ScenarioName)
		result.ScenarioDigest = redactString(result.ScenarioDigest)
		result.CatalogDigest = redactString(result.CatalogDigest)
		for index := range result.Resolutions {
			resolution := &result.Resolutions[index]
			resolution.ProfileClass = redactString(resolution.ProfileClass)
			resolution.ProfileDigest = redactString(resolution.ProfileDigest)
			resolution.ModelID = redactString(resolution.ModelID)
			resolution.ContractID = redactString(resolution.ContractID)
			resolution.ResourceAlias = redactString(resolution.ResourceAlias)
			resolution.ResourceName = redactString(resolution.ResourceName)
			for evidenceIndex := range resolution.Evidence {
				redactEvidence(&resolution.Evidence[evidenceIndex])
			}
		}
		result.CanonicalScenario = redactValue(result.CanonicalScenario, "")
		envelope.Result = result
	}
	return envelope
}

func redactProfileSummary(profile *ProfileSummaryResult) {
	profile.ID = redactString(profile.ID)
	profile.DisplayName = redactString(profile.DisplayName)
	profile.Class = redactString(profile.Class)
	profile.Revision = redactString(profile.Revision)
	profile.Digest = redactString(profile.Digest)
}

func redactEvidence(evidence *EvidenceResult) {
	evidence.ID = redactString(evidence.ID)
	evidence.Grade = redactString(evidence.Grade)
	evidence.Source = redactString(evidence.Source)
	evidence.Revision = redactString(evidence.Revision)
	evidence.CheckedAt = redactString(evidence.CheckedAt)
}

func redactContract(contract *ContractResult) {
	contract.ID = redactString(contract.ID)
	contract.Kind = redactString(contract.Kind)
	contract.ProviderScope = redactString(contract.ProviderScope)
	contract.FidelityModes = redactStrings(contract.FidelityModes)
	for index := range contract.Resources {
		resource := &contract.Resources[index]
		resource.Alias = redactString(resource.Alias)
		resource.Name = redactString(resource.Name)
		resource.Unit = redactString(resource.Unit)
	}
	for index := range contract.IdentitySignals {
		signal := &contract.IdentitySignals[index]
		signal.Kind = redactString(signal.Kind)
		signal.Key = redactString(signal.Key)
	}
	contract.Capabilities = redactStringMap(contract.Capabilities)
	contract.EvidenceRefs = redactStrings(contract.EvidenceRefs)
}

func redactModel(model *ModelResult) {
	model.ID = redactString(model.ID)
	model.DisplayName = redactString(model.DisplayName)
	model.Aliases = redactStrings(model.Aliases)
	model.Lifecycle = redactString(model.Lifecycle)
	model.Contracts = redactStrings(model.Contracts)
	model.ResourceAliases = redactStrings(model.ResourceAliases)
	model.EvidenceRefs = redactStrings(model.EvidenceRefs)
}

func redactStrings(values []string) []string {
	redacted := append([]string(nil), values...)
	for index := range redacted {
		redacted[index] = redactString(redacted[index])
	}
	return redacted
}

func redactStringMap(values map[string]string) map[string]string {
	redacted := make(map[string]string, len(values))
	for key, value := range values {
		if secretField(key) {
			redacted[key] = "[REDACTED]"
			continue
		}
		redacted[redactString(key)] = redactString(value)
	}
	return redacted
}

func redactValue(value any, field string) any {
	if secretField(field) {
		return "[REDACTED]"
	}
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, child := range typed {
			redacted[key] = redactValue(child, key)
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for index, child := range typed {
			redacted[index] = redactValue(child, field)
		}
		return redacted
	case string:
		return redactString(typed)
	default:
		return value
	}
}

func secretField(field string) bool {
	normalized := strings.NewReplacer(
		"-", "",
		"_", "",
		".", "",
		" ", "",
	).Replace(strings.ToLower(field))
	for _, marker := range []string{
		"token",
		"password",
		"passwd",
		"secret",
		"clientkey",
		"privatekey",
		"kubeconfig",
		"authorization",
		"credential",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func redactString(value string) string {
	lower := strings.ToLower(value)
	if strings.Contains(lower, "apiversion:") &&
		strings.Contains(lower, "clusters:") &&
		strings.Contains(lower, "users:") {
		return "[REDACTED kubeconfig]"
	}
	value = privateKeyPattern.ReplaceAllString(value, "[REDACTED private key]")
	value = bearerPattern.ReplaceAllString(value, "Bearer [REDACTED]")
	return secretAssignmentPattern.ReplaceAllStringFunc(value, func(match string) string {
		separator := strings.IndexAny(match, ":=")
		if separator == -1 {
			return "[REDACTED]"
		}
		return strings.TrimSpace(match[:separator]) + "=[REDACTED]"
	})
}
