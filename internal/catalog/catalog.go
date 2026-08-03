// Package catalog loads, validates, and resolves immutable Vendor Profile
// records. Vendor ecosystems remain data; resolution contains no vendor switch.
package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
	"github.com/LinkMaq/kube-accelerator-sim/profiles"
)

const maximumCustomCatalogBytes = 1 << 20

var dnsSubdomainPattern = regexp.MustCompile(
	`^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?(?:\.[a-z0-9](?:[-a-z0-9]*[a-z0-9])?)*$`,
)
var qualifiedNamePartPattern = regexp.MustCompile(
	`^[A-Za-z0-9](?:[-A-Za-z0-9_.]*[A-Za-z0-9])?$`,
)

type catalogFile struct {
	SchemaVersion string          `json:"schemaVersion"`
	Revision      string          `json:"revision"`
	Profiles      []profileRecord `json:"profiles"`
}

type profileRecord struct {
	ID          string            `json:"id"`
	DisplayName string            `json:"displayName"`
	Class       string            `json:"class"`
	Evidence    []EvidenceReceipt `json:"evidence"`
	Contracts   []contractRecord  `json:"contracts"`
	Models      []modelRecord     `json:"models"`
}

type contractRecord struct {
	ID                 string            `json:"id"`
	Subject            string            `json:"subject,omitempty"`
	AuxiliaryCategory  string            `json:"auxiliaryCategory,omitempty"`
	ResourceNamePolicy string            `json:"resourceNamePolicy,omitempty"`
	Kind               string            `json:"kind"`
	ProviderScope      string            `json:"providerScope"`
	FidelityModes      []string          `json:"fidelityModes"`
	Resources          []resourceRecord  `json:"resources"`
	IdentitySignals    []identitySignal  `json:"identitySignals"`
	Capabilities       map[string]string `json:"capabilities"`
	EvidenceRefs       []string          `json:"evidenceRefs"`
}

type resourceRecord struct {
	Alias string `json:"alias"`
	Name  string `json:"name"`
	Unit  string `json:"unit"`
}

type identitySignal struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
}

type modelRecord struct {
	ID              string   `json:"id"`
	DisplayName     string   `json:"displayName"`
	Aliases         []string `json:"aliases"`
	Lifecycle       string   `json:"lifecycle"`
	Selectable      bool     `json:"selectable"`
	Contracts       []string `json:"contracts"`
	ResourceAliases []string `json:"resourceAliases"`
	EvidenceRefs    []string `json:"evidenceRefs"`
}

// EvidenceReceipt identifies the exact source and review date supporting a
// catalog claim.
type EvidenceReceipt struct {
	ID        string `json:"id"`
	Grade     string `json:"grade"`
	Source    string `json:"source"`
	Revision  string `json:"revision"`
	CheckedAt string `json:"checkedAt"`
}

// Snapshot is one immutable validated view of the bundled profile catalog.
type Snapshot struct {
	revision string
	digest   domain.Digest
	profiles map[string]resolvedProfile
}

type resolvedProfile struct {
	record profileRecord
	digest domain.Digest
}

// ProfileSummary is the stable offline list view of one bundled profile.
type ProfileSummary struct {
	id           string
	displayName  string
	profileClass string
	revision     string
	digest       domain.Digest
}

// ProfileView is the immutable offline detail view of one profile revision.
type ProfileView struct {
	id           string
	displayName  string
	profileClass string
	revision     string
	digest       domain.Digest
	evidence     []EvidenceReceipt
	contracts    []ContractSummary
	models       []ModelSummary
}

// ModelSummary is the immutable offline view of one Accelerator Model.
type ModelSummary struct {
	id              string
	displayName     string
	aliases         []string
	lifecycle       string
	selectable      bool
	contracts       []string
	resourceAliases []string
	evidenceRefs    []string
}

// ContractSummary is the immutable offline view of one Resource Contract.
type ContractSummary struct {
	id                 string
	subject            string
	auxiliaryCategory  string
	resourceNamePolicy string
	kind               string
	providerScope      string
	fidelityModes      []string
	resources          []ResourceSummary
	identitySignals    []IdentitySignalSummary
	capabilities       map[string]string
	evidenceRefs       []string
}

// ResourceSummary is one exact portable alias and Kubernetes-facing name.
type ResourceSummary struct {
	alias string
	name  string
	unit  string
}

// IdentitySignalSummary is one exact vendor label, annotation, or DRA key.
type IdentitySignalSummary struct {
	kind string
	key  string
}

// ResolveRequest selects one exact source-backed model and Kubernetes contract.
type ResolveRequest struct {
	ProfileID         string
	ModelID           string
	ContractID        string
	ResourceAlias     string
	Fidelity          domain.FidelityMode
	AcceptProvisional bool
}

type ResolveAuxiliaryRequest struct {
	ProfileID         string
	ContractID        string
	ResourceAlias     string
	ResourceName      string
	Fidelity          domain.FidelityMode
	AcceptProvisional bool
}

// ResolvedSelection is the immutable evidence-bearing result consumed by the
// Scenario Compiler.
type ResolvedSelection struct {
	profileClass       string
	profileDigest      domain.Digest
	subject            string
	auxiliaryCategory  string
	resourceNamePolicy string
	modelID            string
	contractID         string
	resourceAlias      string
	resourceName       string
	identity           []ResolvedIdentitySignal
	evidence           []EvidenceReceipt
}

// ResolvedIdentitySignal is one exact source-backed vendor identity key used
// by the selected Resource Contract. Values remain model/backend data.
type ResolvedIdentitySignal struct {
	kind string
	key  string
}

// LoadBundled validates the exact embedded catalog and computes stable profile
// and whole-catalog digests.
func LoadBundled() (Snapshot, error) {
	return load(bytes.NewReader(profiles.CatalogJSON), profiles.CatalogJSON)
}

// LoadCustom applies the same strict schema and digest path to a bounded custom
// catalog and rejects records that claim a bundled evidence class.
func LoadCustom(reader io.Reader) (Snapshot, error) {
	encoded, err := io.ReadAll(io.LimitReader(reader, maximumCustomCatalogBytes+1))
	if err != nil {
		return Snapshot{}, fmt.Errorf("read custom catalog: %w", err)
	}
	if len(encoded) > maximumCustomCatalogBytes {
		return Snapshot{}, fmt.Errorf("custom catalog exceeds %d bytes", maximumCustomCatalogBytes)
	}
	snapshot, err := load(bytes.NewReader(encoded), encoded)
	if err != nil {
		return Snapshot{}, err
	}
	for _, profile := range snapshot.profiles {
		if profile.record.Class != "custom" {
			return Snapshot{}, fmt.Errorf(
				"custom catalog profile %q must use class custom",
				profile.record.ID,
			)
		}
	}
	return snapshot, nil
}

func load(reader io.Reader, digestInput []byte) (Snapshot, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()

	var file catalogFile
	if err := decoder.Decode(&file); err != nil {
		return Snapshot{}, fmt.Errorf("decode catalog: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Snapshot{}, fmt.Errorf("catalog must contain exactly one JSON document")
	}
	if file.SchemaVersion != "v1alpha1" && file.SchemaVersion != "v1alpha2" {
		return Snapshot{}, fmt.Errorf("unsupported catalog schema %q", file.SchemaVersion)
	}
	if file.Revision == "" || len(file.Profiles) == 0 {
		return Snapshot{}, fmt.Errorf("catalog requires revision and profiles")
	}

	resolvedProfiles := make(map[string]resolvedProfile, len(file.Profiles))
	for _, profile := range file.Profiles {
		if err := validateProfile(profile); err != nil {
			return Snapshot{}, err
		}
		if _, duplicate := resolvedProfiles[profile.ID]; duplicate {
			return Snapshot{}, fmt.Errorf("duplicate profile ID %q", profile.ID)
		}
		encoded, err := json.Marshal(profile)
		if err != nil {
			return Snapshot{}, fmt.Errorf("encode profile %q: %w", profile.ID, err)
		}
		profileDigest, err := digest(encoded)
		if err != nil {
			return Snapshot{}, err
		}
		resolvedProfiles[profile.ID] = resolvedProfile{
			record: profile,
			digest: profileDigest,
		}
	}
	catalogDigest, err := digest(digestInput)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		revision: file.Revision,
		digest:   catalogDigest,
		profiles: resolvedProfiles,
	}, nil
}

func validateProfile(profile profileRecord) error {
	if _, err := domain.ParseName(profile.ID); err != nil {
		return fmt.Errorf("profile ID: %w", err)
	}
	if profile.DisplayName == "" {
		return fmt.Errorf("profile %q requires a display name", profile.ID)
	}
	switch profile.Class {
	case "verified", "provisional", "custom":
	default:
		return fmt.Errorf("profile %q has invalid class %q", profile.ID, profile.Class)
	}
	if len(profile.Evidence) == 0 {
		return fmt.Errorf("profile %q requires evidence", profile.ID)
	}
	evidenceIDs := make(map[string]struct{}, len(profile.Evidence))
	for _, evidence := range profile.Evidence {
		if err := validateEvidence(profile.ID, evidence); err != nil {
			return err
		}
		if _, duplicate := evidenceIDs[evidence.ID]; duplicate {
			return fmt.Errorf("profile %q has duplicate evidence ID %q", profile.ID, evidence.ID)
		}
		evidenceIDs[evidence.ID] = struct{}{}
	}
	if profile.Class == "verified" && len(profile.Contracts) == 0 {
		return fmt.Errorf("verified profile %q requires contracts", profile.ID)
	}

	contracts := make(map[string]contractRecord, len(profile.Contracts))
	hasAcceleratorContract := false
	for _, contract := range profile.Contracts {
		if _, err := domain.ParseName(contract.ID); err != nil {
			return fmt.Errorf("profile %q contract ID: %w", profile.ID, err)
		}
		if _, duplicate := contracts[contract.ID]; duplicate {
			return fmt.Errorf("profile %q has duplicate contract ID %q", profile.ID, contract.ID)
		}
		if err := validateContract(profile.ID, contract, evidenceIDs); err != nil {
			return err
		}
		if contractSubject(contract) == "accelerator" {
			hasAcceleratorContract = true
		}
		contracts[contract.ID] = contract
	}
	if profile.Class == "verified" && hasAcceleratorContract && len(profile.Models) == 0 {
		return fmt.Errorf("verified accelerator profile %q requires models", profile.ID)
	}

	modelIDs := make(map[string]struct{}, len(profile.Models))
	modelAliases := make(map[string]string)
	for _, model := range profile.Models {
		if _, err := domain.ParseName(model.ID); err != nil {
			return fmt.Errorf("profile %q model ID: %w", profile.ID, err)
		}
		if _, duplicate := modelIDs[model.ID]; duplicate {
			return fmt.Errorf("profile %q has duplicate model ID %q", profile.ID, model.ID)
		}
		modelIDs[model.ID] = struct{}{}
		modelAliases[strings.ToLower(model.ID)] = model.ID
	}
	for _, model := range profile.Models {
		if err := validateModel(profile.ID, model, contracts, evidenceIDs, modelAliases); err != nil {
			return err
		}
	}
	return nil
}

func validateEvidence(profileID string, evidence EvidenceReceipt) error {
	if evidence.ID == "" || evidence.Source == "" ||
		evidence.Revision == "" || evidence.CheckedAt == "" {
		return fmt.Errorf("profile %q contains incomplete evidence", profileID)
	}
	switch evidence.Grade {
	case "A", "B", "C", "D":
	default:
		return fmt.Errorf("profile %q evidence %q has invalid grade", profileID, evidence.ID)
	}
	parsedURL, err := url.ParseRequestURI(evidence.Source)
	if err != nil || (parsedURL.Scheme != "https" && parsedURL.Scheme != "http") {
		return fmt.Errorf("profile %q evidence %q has invalid source", profileID, evidence.ID)
	}
	if len(evidence.Revision) > 128 {
		return fmt.Errorf("profile %q evidence %q revision is unbounded", profileID, evidence.ID)
	}
	if _, err := time.Parse("2006-01-02", evidence.CheckedAt); err != nil {
		return fmt.Errorf("profile %q evidence %q has invalid checked date", profileID, evidence.ID)
	}
	return nil
}

func validateContract(
	profileID string,
	contract contractRecord,
	evidenceIDs map[string]struct{},
) error {
	subject := contractSubject(contract)
	switch subject {
	case "accelerator":
		if contract.AuxiliaryCategory != "" || contract.ResourceNamePolicy != "" {
			return fmt.Errorf(
				"profile %q accelerator contract %q carries auxiliary fields",
				profileID, contract.ID,
			)
		}
	case "auxiliary":
		if contract.AuxiliaryCategory == "" {
			return fmt.Errorf(
				"profile %q auxiliary contract %q requires a category",
				profileID, contract.ID,
			)
		}
		switch contract.ResourceNamePolicy {
		case "fixed", "scenario-required":
		default:
			return fmt.Errorf(
				"profile %q auxiliary contract %q has invalid resource-name policy %q",
				profileID, contract.ID, contract.ResourceNamePolicy,
			)
		}
		if contract.Kind != "extended-resource" ||
			!slices.Equal(contract.FidelityModes, []string{"scheduling"}) {
			return fmt.Errorf(
				"profile %q auxiliary contract %q must be a scheduling-only extended resource",
				profileID, contract.ID,
			)
		}
	default:
		return fmt.Errorf(
			"profile %q contract %q has invalid subject %q",
			profileID, contract.ID, subject,
		)
	}
	switch contract.Kind {
	case "extended-resource", "dra":
	default:
		return fmt.Errorf(
			"profile %q contract %q has invalid kind %q",
			profileID,
			contract.ID,
			contract.Kind,
		)
	}
	if contract.ProviderScope == "" || len(contract.ProviderScope) > 128 {
		return fmt.Errorf("profile %q contract %q requires provider scope", profileID, contract.ID)
	}
	if len(contract.FidelityModes) == 0 || len(contract.Resources) == 0 {
		return fmt.Errorf("profile %q contract %q is incomplete", profileID, contract.ID)
	}
	fidelityModes := make(map[string]struct{}, len(contract.FidelityModes))
	for _, fidelity := range contract.FidelityModes {
		if _, err := domain.ParseFidelityMode(fidelity); err != nil {
			return fmt.Errorf("profile %q contract %q: %w", profileID, contract.ID, err)
		}
		if _, duplicate := fidelityModes[fidelity]; duplicate {
			return fmt.Errorf(
				"profile %q contract %q has duplicate Fidelity Mode %q",
				profileID,
				contract.ID,
				fidelity,
			)
		}
		fidelityModes[fidelity] = struct{}{}
	}
	resourceAliases := make(map[string]struct{}, len(contract.Resources))
	resourceNames := make(map[string]struct{}, len(contract.Resources))
	for _, resource := range contract.Resources {
		if _, err := domain.ParseName(resource.Alias); err != nil {
			return fmt.Errorf("profile %q contract %q resource alias: %w", profileID, contract.ID, err)
		}
		if resource.Unit == "" {
			return fmt.Errorf("profile %q contract %q resource %q has no unit", profileID, contract.ID, resource.Alias)
		}
		nameRequired := subject != "auxiliary" || contract.ResourceNamePolicy == "fixed"
		if (nameRequired && !validContractResourceName(contract.Kind, resource.Name)) ||
			(!nameRequired && resource.Name != "") {
			return fmt.Errorf(
				"profile %q contract %q has invalid resource name %q",
				profileID,
				contract.ID,
				resource.Name,
			)
		}
		if _, duplicate := resourceAliases[resource.Alias]; duplicate {
			return fmt.Errorf(
				"profile %q contract %q has duplicate resource alias %q",
				profileID,
				contract.ID,
				resource.Alias,
			)
		}
		if _, duplicate := resourceNames[resource.Name]; resource.Name != "" && duplicate {
			return fmt.Errorf(
				"profile %q contract %q has duplicate resource name %q",
				profileID,
				contract.ID,
				resource.Name,
			)
		}
		resourceAliases[resource.Alias] = struct{}{}
		if resource.Name != "" {
			resourceNames[resource.Name] = struct{}{}
		}
	}
	identitySignals := make(map[string]struct{}, len(contract.IdentitySignals))
	for _, signal := range contract.IdentitySignals {
		switch signal.Kind {
		case "node-label", "annotation", "dra-attribute":
		default:
			return fmt.Errorf(
				"profile %q contract %q has invalid identity signal kind %q",
				profileID,
				contract.ID,
				signal.Kind,
			)
		}
		if !validQualifiedName(signal.Key) {
			return fmt.Errorf(
				"profile %q contract %q has invalid identity signal key %q",
				profileID,
				contract.ID,
				signal.Key,
			)
		}
		identity := signal.Kind + "\x00" + signal.Key
		if _, duplicate := identitySignals[identity]; duplicate {
			return fmt.Errorf(
				"profile %q contract %q repeats identity signal %q",
				profileID,
				contract.ID,
				signal.Key,
			)
		}
		identitySignals[identity] = struct{}{}
	}
	requiredCapabilities := map[string]struct{}{
		"health": {}, "topology": {}, "sharing": {}, "partitioning": {},
	}
	if len(contract.Capabilities) != len(requiredCapabilities) {
		return fmt.Errorf("profile %q contract %q has incomplete capabilities", profileID, contract.ID)
	}
	for capability, state := range contract.Capabilities {
		if _, known := requiredCapabilities[capability]; !known {
			return fmt.Errorf(
				"profile %q contract %q has unknown capability %q",
				profileID,
				contract.ID,
				capability,
			)
		}
		switch state {
		case "verified", "not-public", "not-applicable":
		default:
			return fmt.Errorf(
				"profile %q contract %q capability %q has invalid evidence state %q",
				profileID,
				contract.ID,
				capability,
				state,
			)
		}
	}
	return validateEvidenceReferences(profileID, "contract "+contract.ID, contract.EvidenceRefs, evidenceIDs)
}

func contractSubject(contract contractRecord) string {
	if contract.Subject == "" {
		return "accelerator"
	}
	return contract.Subject
}

func validQualifiedName(value string) bool {
	prefix, name, found := strings.Cut(value, "/")
	if !found {
		return len(value) <= 63 && qualifiedNamePartPattern.MatchString(value)
	}
	return len(prefix) <= 253 &&
		len(name) <= 63 &&
		dnsSubdomainPattern.MatchString(prefix) &&
		qualifiedNamePartPattern.MatchString(name)
}

func validContractResourceName(kind, value string) bool {
	if kind == "dra" {
		return len(value) <= 253 && dnsSubdomainPattern.MatchString(value)
	}
	prefix, name, found := strings.Cut(value, "/")
	if !found || len(prefix) > 253 || len(name) > 63 {
		return false
	}
	if !dnsSubdomainPattern.MatchString(prefix) || !qualifiedNamePartPattern.MatchString(name) {
		return false
	}
	return prefix != "kubernetes.io" && !strings.HasSuffix(prefix, ".kubernetes.io")
}

func validateModel(
	profileID string,
	model modelRecord,
	contracts map[string]contractRecord,
	evidenceIDs map[string]struct{},
	aliases map[string]string,
) error {
	if model.DisplayName == "" {
		return fmt.Errorf("profile %q model %q requires display name", profileID, model.ID)
	}
	switch model.Lifecycle {
	case "k8s-identified", "current-product", "deployed-retention", "catalog-only":
	default:
		return fmt.Errorf(
			"profile %q model %q has invalid lifecycle %q",
			profileID,
			model.ID,
			model.Lifecycle,
		)
	}
	if model.Selectable && (len(model.Contracts) == 0 || len(model.ResourceAliases) == 0) {
		return fmt.Errorf("profile %q selectable model %q has no contract", profileID, model.ID)
	}
	if model.Lifecycle == "catalog-only" && model.Selectable {
		return fmt.Errorf("profile %q catalog-only model %q is selectable", profileID, model.ID)
	}
	for _, alias := range model.Aliases {
		normalized := strings.ToLower(strings.TrimSpace(alias))
		if normalized == "" {
			return fmt.Errorf("profile %q model %q has an empty alias", profileID, model.ID)
		}
		if owner, duplicate := aliases[normalized]; duplicate {
			return fmt.Errorf(
				"profile %q model alias %q is ambiguous between %q and %q",
				profileID,
				alias,
				owner,
				model.ID,
			)
		}
		aliases[normalized] = model.ID
	}
	availableAliases := make(map[string]struct{})
	for _, contractID := range model.Contracts {
		contract, found := contracts[contractID]
		if !found {
			return fmt.Errorf(
				"profile %q model %q references unknown contract %q",
				profileID,
				model.ID,
				contractID,
			)
		}
		for _, resource := range contract.Resources {
			availableAliases[resource.Alias] = struct{}{}
		}
	}
	for _, resourceAlias := range model.ResourceAliases {
		if _, found := availableAliases[resourceAlias]; !found {
			return fmt.Errorf(
				"profile %q model %q references unknown resource alias %q",
				profileID,
				model.ID,
				resourceAlias,
			)
		}
	}
	return validateEvidenceReferences(profileID, "model "+model.ID, model.EvidenceRefs, evidenceIDs)
}

func validateEvidenceReferences(
	profileID string,
	subject string,
	references []string,
	evidenceIDs map[string]struct{},
) error {
	if len(references) == 0 {
		return fmt.Errorf("profile %q %s requires evidence", profileID, subject)
	}
	seen := make(map[string]struct{}, len(references))
	for _, reference := range references {
		if _, found := evidenceIDs[reference]; !found {
			return fmt.Errorf(
				"profile %q %s references unknown evidence %q",
				profileID,
				subject,
				reference,
			)
		}
		if _, duplicate := seen[reference]; duplicate {
			return fmt.Errorf(
				"profile %q %s repeats evidence %q",
				profileID,
				subject,
				reference,
			)
		}
		seen[reference] = struct{}{}
	}
	return nil
}

func digest(value []byte) (domain.Digest, error) {
	sum := sha256.Sum256(value)
	return domain.ParseDigest("sha256:" + hex.EncodeToString(sum[:]))
}

// Resolve validates profile class, model selection, contract compatibility,
// Fidelity Mode, and exact resource alias without deriving a resource name.
func (snapshot Snapshot) Resolve(request ResolveRequest) (ResolvedSelection, error) {
	profile, found := snapshot.profiles[request.ProfileID]
	if !found {
		return ResolvedSelection{}, fmt.Errorf("unknown profile %q", request.ProfileID)
	}
	if profile.record.Class == "provisional" && !request.AcceptProvisional {
		return ResolvedSelection{}, fmt.Errorf(
			"provisional profile %q requires explicit acceptance",
			request.ProfileID,
		)
	}

	var model *modelRecord
	for index := range profile.record.Models {
		candidate := &profile.record.Models[index]
		if candidate.ID == request.ModelID || slices.Contains(candidate.Aliases, request.ModelID) {
			model = candidate
			break
		}
	}
	if model == nil || !model.Selectable {
		return ResolvedSelection{}, fmt.Errorf(
			"model %q is not selectable in profile %q",
			request.ModelID,
			request.ProfileID,
		)
	}

	var contract *contractRecord
	if request.ContractID != "" {
		for index := range profile.record.Contracts {
			candidate := &profile.record.Contracts[index]
			if candidate.ID == request.ContractID {
				contract = candidate
				break
			}
		}
		if contract == nil || contractSubject(*contract) != "accelerator" ||
			!slices.Contains(model.Contracts, contract.ID) {
			return ResolvedSelection{}, fmt.Errorf(
				"contract %q is not supported by model %q",
				request.ContractID,
				model.ID,
			)
		}
		if !slices.Contains(contract.FidelityModes, request.Fidelity.String()) {
			return ResolvedSelection{}, fmt.Errorf(
				"contract %q does not support Fidelity Mode %q",
				contract.ID,
				request.Fidelity,
			)
		}
	} else {
		compatibleContracts := make([]*contractRecord, 0, len(profile.record.Contracts))
		for index := range profile.record.Contracts {
			candidate := &profile.record.Contracts[index]
			if contractSubject(*candidate) == "accelerator" &&
				slices.Contains(model.Contracts, candidate.ID) &&
				slices.Contains(candidate.FidelityModes, request.Fidelity.String()) {
				compatibleContracts = append(compatibleContracts, candidate)
			}
		}
		if len(compatibleContracts) == 0 {
			return ResolvedSelection{}, fmt.Errorf(
				"no Resource Contract supports model %q and Fidelity Mode %q",
				model.ID,
				request.Fidelity.String(),
			)
		}
		if len(compatibleContracts) > 1 {
			return ResolvedSelection{}, fmt.Errorf(
				"contract is ambiguous for model %q and Fidelity Mode %q",
				model.ID,
				request.Fidelity.String(),
			)
		}
		contract = compatibleContracts[0]
	}

	var resource resourceRecord
	if request.ResourceAlias != "" {
		found := false
		for _, candidate := range contract.Resources {
			if candidate.Alias == request.ResourceAlias {
				resource = candidate
				found = true
				break
			}
		}
		if !found {
			return ResolvedSelection{}, fmt.Errorf(
				"resource alias %q is not defined by contract %q",
				request.ResourceAlias,
				contract.ID,
			)
		}
		if len(model.ResourceAliases) != 0 &&
			!slices.Contains(model.ResourceAliases, request.ResourceAlias) {
			return ResolvedSelection{}, fmt.Errorf(
				"resource alias %q is not compatible with model %q",
				request.ResourceAlias,
				model.ID,
			)
		}
	} else {
		compatibleResources := make([]resourceRecord, 0, len(contract.Resources))
		for _, candidate := range contract.Resources {
			if len(model.ResourceAliases) == 0 ||
				slices.Contains(model.ResourceAliases, candidate.Alias) {
				compatibleResources = append(compatibleResources, candidate)
			}
		}
		if len(compatibleResources) == 0 {
			return ResolvedSelection{}, fmt.Errorf(
				"no resource alias is compatible with model %q and contract %q",
				model.ID,
				contract.ID,
			)
		}
		if len(compatibleResources) > 1 {
			return ResolvedSelection{}, fmt.Errorf(
				"resource alias is ambiguous for model %q and contract %q",
				model.ID,
				contract.ID,
			)
		}
		resource = compatibleResources[0]
	}
	evidence := append([]EvidenceReceipt(nil), profile.record.Evidence...)
	identity := make([]ResolvedIdentitySignal, 0, len(contract.IdentitySignals))
	for _, signal := range contract.IdentitySignals {
		identity = append(identity, ResolvedIdentitySignal{
			kind: signal.Kind,
			key:  signal.Key,
		})
	}
	return ResolvedSelection{
		profileClass: profile.record.Class, profileDigest: profile.digest,
		subject: "accelerator", modelID: model.ID, contractID: contract.ID,
		resourceAlias: resource.Alias, resourceName: resource.Name,
		identity: identity, evidence: evidence,
	}, nil
}

// ResolveAuxiliary validates one source-backed Auxiliary Device Signal
// contract without inventing an Accelerator Model or a configurable resource
// name.
func (snapshot Snapshot) ResolveAuxiliary(
	request ResolveAuxiliaryRequest,
) (ResolvedSelection, error) {
	profile, found := snapshot.profiles[request.ProfileID]
	if !found {
		return ResolvedSelection{}, fmt.Errorf("unknown profile %q", request.ProfileID)
	}
	if profile.record.Class == "provisional" && !request.AcceptProvisional {
		return ResolvedSelection{}, fmt.Errorf(
			"provisional profile %q requires explicit acceptance", request.ProfileID,
		)
	}
	var contract *contractRecord
	for index := range profile.record.Contracts {
		candidate := &profile.record.Contracts[index]
		if candidate.ID == request.ContractID {
			contract = candidate
			break
		}
	}
	if contract == nil || contractSubject(*contract) != "auxiliary" {
		return ResolvedSelection{}, fmt.Errorf(
			"auxiliary contract %q is not defined by profile %q",
			request.ContractID, request.ProfileID,
		)
	}
	if !slices.Contains(contract.FidelityModes, request.Fidelity.String()) {
		return ResolvedSelection{}, fmt.Errorf(
			"auxiliary contract %q does not support Fidelity Mode %q",
			contract.ID, request.Fidelity,
		)
	}
	var resource resourceRecord
	found = false
	for _, candidate := range contract.Resources {
		if candidate.Alias == request.ResourceAlias {
			resource = candidate
			found = true
			break
		}
	}
	if !found {
		return ResolvedSelection{}, fmt.Errorf(
			"resource alias %q is not defined by auxiliary contract %q",
			request.ResourceAlias, contract.ID,
		)
	}
	resourceName := resource.Name
	switch contract.ResourceNamePolicy {
	case "fixed":
		if request.ResourceName != "" && request.ResourceName != resource.Name {
			return ResolvedSelection{}, fmt.Errorf(
				"fixed auxiliary contract %q does not permit resource-name override",
				contract.ID,
			)
		}
	case "scenario-required":
		if !validContractResourceName("extended-resource", request.ResourceName) {
			return ResolvedSelection{}, fmt.Errorf(
				"auxiliary contract %q requires an exact resource name",
				contract.ID,
			)
		}
		resourceName = request.ResourceName
	}
	identity := make([]ResolvedIdentitySignal, 0, len(contract.IdentitySignals))
	for _, signal := range contract.IdentitySignals {
		identity = append(identity, ResolvedIdentitySignal{kind: signal.Kind, key: signal.Key})
	}
	return ResolvedSelection{
		profileClass: profile.record.Class, profileDigest: profile.digest,
		subject: "auxiliary", auxiliaryCategory: contract.AuxiliaryCategory,
		resourceNamePolicy: contract.ResourceNamePolicy,
		contractID:         contract.ID, resourceAlias: resource.Alias,
		resourceName: resourceName, identity: identity,
		evidence: append([]EvidenceReceipt(nil), profile.record.Evidence...),
	}, nil
}

// Digest returns the immutable whole-catalog content digest.
func (snapshot Snapshot) Digest() domain.Digest {
	return snapshot.digest
}

// Revision returns the release-frozen catalog revision.
func (snapshot Snapshot) Revision() string {
	return snapshot.revision
}

// List returns profile summaries in canonical ID order.
func (snapshot Snapshot) List() []ProfileSummary {
	summaries := make([]ProfileSummary, 0, len(snapshot.profiles))
	for _, profile := range snapshot.profiles {
		summaries = append(summaries, ProfileSummary{
			id:           profile.record.ID,
			displayName:  profile.record.DisplayName,
			profileClass: profile.record.Class,
			revision:     snapshot.revision,
			digest:       profile.digest,
		})
	}
	sort.Slice(summaries, func(left, right int) bool {
		return summaries[left].id < summaries[right].id
	})
	return summaries
}

// Show returns one immutable profile detail view.
func (snapshot Snapshot) Show(profileID string) (ProfileView, error) {
	profile, found := snapshot.profiles[profileID]
	if !found {
		return ProfileView{}, fmt.Errorf("unknown profile %q", profileID)
	}
	models := make([]ModelSummary, 0, len(profile.record.Models))
	for _, model := range profile.record.Models {
		models = append(models, ModelSummary{
			id:              model.ID,
			displayName:     model.DisplayName,
			aliases:         append([]string(nil), model.Aliases...),
			lifecycle:       model.Lifecycle,
			selectable:      model.Selectable,
			contracts:       append([]string(nil), model.Contracts...),
			resourceAliases: append([]string(nil), model.ResourceAliases...),
			evidenceRefs:    append([]string(nil), model.EvidenceRefs...),
		})
	}
	sort.Slice(models, func(left, right int) bool {
		return models[left].id < models[right].id
	})
	contracts := make([]ContractSummary, 0, len(profile.record.Contracts))
	for _, contract := range profile.record.Contracts {
		resources := make([]ResourceSummary, 0, len(contract.Resources))
		for _, resource := range contract.Resources {
			resources = append(resources, ResourceSummary{
				alias: resource.Alias,
				name:  resource.Name,
				unit:  resource.Unit,
			})
		}
		sort.Slice(resources, func(left, right int) bool {
			return resources[left].alias < resources[right].alias
		})
		signals := make([]IdentitySignalSummary, 0, len(contract.IdentitySignals))
		for _, signal := range contract.IdentitySignals {
			signals = append(signals, IdentitySignalSummary{
				kind: signal.Kind,
				key:  signal.Key,
			})
		}
		sort.Slice(signals, func(left, right int) bool {
			if signals[left].kind != signals[right].kind {
				return signals[left].kind < signals[right].kind
			}
			return signals[left].key < signals[right].key
		})
		contracts = append(contracts, ContractSummary{
			id:                 contract.ID,
			subject:            contractSubject(contract),
			auxiliaryCategory:  contract.AuxiliaryCategory,
			resourceNamePolicy: contract.ResourceNamePolicy,
			kind:               contract.Kind,
			providerScope:      contract.ProviderScope,
			fidelityModes:      append([]string(nil), contract.FidelityModes...),
			resources:          resources,
			identitySignals:    signals,
			capabilities:       cloneStringMap(contract.Capabilities),
			evidenceRefs:       append([]string(nil), contract.EvidenceRefs...),
		})
	}
	sort.Slice(contracts, func(left, right int) bool {
		return contracts[left].id < contracts[right].id
	})
	return ProfileView{
		id:           profile.record.ID,
		displayName:  profile.record.DisplayName,
		profileClass: profile.record.Class,
		revision:     snapshot.revision,
		digest:       profile.digest,
		evidence:     append([]EvidenceReceipt(nil), profile.record.Evidence...),
		contracts:    contracts,
		models:       models,
	}, nil
}

// ID returns the stable profile identifier.
func (summary ProfileSummary) ID() string {
	return summary.id
}

// DisplayName returns the human-facing Vendor Profile name.
func (summary ProfileSummary) DisplayName() string {
	return summary.displayName
}

// Class returns verified, provisional, or custom.
func (summary ProfileSummary) Class() string {
	return summary.profileClass
}

// Revision returns the catalog revision containing this immutable profile.
func (summary ProfileSummary) Revision() string {
	return summary.revision
}

// Digest returns the immutable content digest of the profile record.
func (summary ProfileSummary) Digest() domain.Digest {
	return summary.digest
}

// Models returns model summaries in canonical ID order.
func (profile ProfileView) Models() []ModelSummary {
	models := append([]ModelSummary(nil), profile.models...)
	for index := range models {
		models[index].aliases = append([]string(nil), models[index].aliases...)
		models[index].contracts = append([]string(nil), models[index].contracts...)
		models[index].resourceAliases = append(
			[]string(nil),
			models[index].resourceAliases...,
		)
		models[index].evidenceRefs = append([]string(nil), models[index].evidenceRefs...)
	}
	return models
}

// ID returns the exact Vendor Profile identifier.
func (profile ProfileView) ID() string {
	return profile.id
}

// DisplayName returns the human-facing Vendor Profile name.
func (profile ProfileView) DisplayName() string {
	return profile.displayName
}

// Class returns verified, provisional, or custom.
func (profile ProfileView) Class() string {
	return profile.profileClass
}

// Revision returns the catalog revision pinning this view.
func (profile ProfileView) Revision() string {
	return profile.revision
}

// Digest returns the exact immutable Vendor Profile digest.
func (profile ProfileView) Digest() domain.Digest {
	return profile.digest
}

// Evidence returns a copy of the receipts supporting this profile.
func (profile ProfileView) Evidence() []EvidenceReceipt {
	return append([]EvidenceReceipt(nil), profile.evidence...)
}

// Contracts returns deep copies in canonical contract ID order.
func (profile ProfileView) Contracts() []ContractSummary {
	contracts := append([]ContractSummary(nil), profile.contracts...)
	for index := range contracts {
		contracts[index].fidelityModes = append(
			[]string(nil),
			contracts[index].fidelityModes...,
		)
		contracts[index].resources = append(
			[]ResourceSummary(nil),
			contracts[index].resources...,
		)
		contracts[index].identitySignals = append(
			[]IdentitySignalSummary(nil),
			contracts[index].identitySignals...,
		)
		contracts[index].capabilities = cloneStringMap(contracts[index].capabilities)
		contracts[index].evidenceRefs = append(
			[]string(nil),
			contracts[index].evidenceRefs...,
		)
	}
	return contracts
}

// ID returns the canonical model ID.
func (model ModelSummary) ID() string {
	return model.id
}

// DisplayName returns the human-facing model name.
func (model ModelSummary) DisplayName() string {
	return model.displayName
}

// Aliases returns exact accepted alternate model names.
func (model ModelSummary) Aliases() []string {
	return append([]string(nil), model.aliases...)
}

// Lifecycle returns the evidence lifecycle classification.
func (model ModelSummary) Lifecycle() string {
	return model.lifecycle
}

// Selectable reports whether the model has an applicable source-backed
// Resource Contract.
func (model ModelSummary) Selectable() bool {
	return model.selectable
}

// Contracts returns compatible Resource Contract IDs.
func (model ModelSummary) Contracts() []string {
	return append([]string(nil), model.contracts...)
}

// ResourceAliases returns compatible portable resource aliases.
func (model ModelSummary) ResourceAliases() []string {
	return append([]string(nil), model.resourceAliases...)
}

// EvidenceRefs returns profile-local evidence receipt IDs.
func (model ModelSummary) EvidenceRefs() []string {
	return append([]string(nil), model.evidenceRefs...)
}

// ID returns the exact Resource Contract identifier.
func (contract ContractSummary) ID() string {
	return contract.id
}

// Subject returns accelerator or auxiliary.
func (contract ContractSummary) Subject() string {
	return contract.subject
}

// AuxiliaryCategory returns the scheduling-signal category for auxiliary
// contracts and an empty string for Accelerator Device Pool contracts.
func (contract ContractSummary) AuxiliaryCategory() string {
	return contract.auxiliaryCategory
}

// ResourceNamePolicy returns fixed or scenario-required for auxiliary
// contracts and an empty string for Accelerator Device Pool contracts.
func (contract ContractSummary) ResourceNamePolicy() string {
	return contract.resourceNamePolicy
}

// Kind returns extended-resource or dra.
func (contract ContractSummary) Kind() string {
	return contract.kind
}

// ProviderScope returns the exact catalog provider scope.
func (contract ContractSummary) ProviderScope() string {
	return contract.providerScope
}

// FidelityModes returns supported product Fidelity Modes.
func (contract ContractSummary) FidelityModes() []string {
	return append([]string(nil), contract.fidelityModes...)
}

// Resources returns exact source-backed resources in canonical alias order.
func (contract ContractSummary) Resources() []ResourceSummary {
	return append([]ResourceSummary(nil), contract.resources...)
}

// IdentitySignals returns exact source-backed identity keys.
func (contract ContractSummary) IdentitySignals() []IdentitySignalSummary {
	return append([]IdentitySignalSummary(nil), contract.identitySignals...)
}

// Capabilities returns an immutable copy of evidence states by capability.
func (contract ContractSummary) Capabilities() map[string]string {
	return cloneStringMap(contract.capabilities)
}

// EvidenceRefs returns profile-local evidence receipt IDs.
func (contract ContractSummary) EvidenceRefs() []string {
	return append([]string(nil), contract.evidenceRefs...)
}

// Alias returns the portable Scenario resource alias.
func (resource ResourceSummary) Alias() string {
	return resource.alias
}

// Name returns the exact Kubernetes-facing resource or driver name.
func (resource ResourceSummary) Name() string {
	return resource.name
}

// Unit returns the catalog unit.
func (resource ResourceSummary) Unit() string {
	return resource.unit
}

// Kind returns node-label, annotation, or dra-attribute.
func (signal IdentitySignalSummary) Kind() string {
	return signal.kind
}

// Key returns the exact signal key.
func (signal IdentitySignalSummary) Key() string {
	return signal.key
}

// ProfileClass returns verified, provisional, or custom.
func (selection ResolvedSelection) ProfileClass() string {
	return selection.profileClass
}

// ProfileDigest returns the immutable digest of the resolved profile record.
func (selection ResolvedSelection) ProfileDigest() domain.Digest {
	return selection.profileDigest
}

// Subject returns accelerator or auxiliary.
func (selection ResolvedSelection) Subject() string {
	return selection.subject
}

// AuxiliaryCategory returns the exact catalog category for an auxiliary
// selection and an empty string for an accelerator selection.
func (selection ResolvedSelection) AuxiliaryCategory() string {
	return selection.auxiliaryCategory
}

// ResourceNamePolicy returns the catalog policy that governed an auxiliary
// resource name and an empty string for an accelerator selection.
func (selection ResolvedSelection) ResourceNamePolicy() string {
	return selection.resourceNamePolicy
}

// ModelID returns the canonical model ID after alias resolution.
func (selection ResolvedSelection) ModelID() string {
	return selection.modelID
}

// ContractID returns the exact selected Resource Contract.
func (selection ResolvedSelection) ContractID() string {
	return selection.contractID
}

// ResourceAlias returns the exact portable alias selected within the contract.
func (selection ResolvedSelection) ResourceAlias() string {
	return selection.resourceAlias
}

// ResourceName returns the exact evidence-backed Kubernetes resource name.
func (selection ResolvedSelection) ResourceName() string {
	return selection.resourceName
}

// IdentitySignals returns a copy of the selected contract's exact identity
// signal keys.
func (selection ResolvedSelection) IdentitySignals() []ResolvedIdentitySignal {
	return append([]ResolvedIdentitySignal(nil), selection.identity...)
}

// Kind returns node-label, annotation, or dra-attribute.
func (signal ResolvedIdentitySignal) Kind() string {
	return signal.kind
}

// Key returns the exact source-backed signal key.
func (signal ResolvedIdentitySignal) Key() string {
	return signal.key
}

// Evidence returns a copy of the evidence supporting the selection.
func (selection ResolvedSelection) Evidence() []EvidenceReceipt {
	return append([]EvidenceReceipt(nil), selection.evidence...)
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
