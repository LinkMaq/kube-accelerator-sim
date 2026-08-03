// Package scenario implements strict, deterministic Scenario compilation and
// typed revisions. It is an in-process deep module, not a parser abstraction.
package scenario

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/LinkMaq/kube-accelerator-sim/internal/catalog"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

const maximumDocumentBytes = 1 << 20

// Input is the sealed set of compiler inputs.
type Input interface {
	scenarioInput()
}

type documentInput struct {
	encoded []byte
}

func (documentInput) scenarioInput() {}

// ShortcutInput is one homogeneous demo shape. Empty Fidelity, ContractID, and
// ResourceAlias values request scheduling defaults and unique catalog
// inference; ambiguity still fails closed during compilation.
type ShortcutInput struct {
	Name                       string
	ProfileID                  string
	ModelID                    string
	ContractID                 string
	ResourceAlias              string
	Fidelity                   string
	Nodes                      int64
	AcceleratorsPerNode        int64
	HealthyPerNode             *int64
	AcceptsProvisionalProfiles bool
	Variant                    map[string]string
}

type shortcutInput struct {
	value ShortcutInput
}

func (shortcutInput) scenarioInput() {}

// Document copies one bounded YAML document into the sealed input model.
func Document(encoded []byte) (Input, error) {
	if len(encoded) == 0 {
		return nil, fmt.Errorf("Scenario document must not be empty")
	}
	if len(encoded) > maximumDocumentBytes {
		return nil, fmt.Errorf("Scenario document exceeds %d bytes", maximumDocumentBytes)
	}
	return documentInput{encoded: append([]byte(nil), encoded...)}, nil
}

// Shortcut copies one homogeneous demo request into the sealed input model.
func Shortcut(input ShortcutInput) (Input, error) {
	if input.Name == "" {
		return nil, fmt.Errorf("Scenario shortcut requires a name")
	}
	if input.ProfileID == "" || input.ModelID == "" {
		return nil, fmt.Errorf("Scenario shortcut requires profile and model")
	}
	if input.HealthyPerNode != nil {
		healthy := *input.HealthyPerNode
		input.HealthyPerNode = &healthy
	}
	input.Variant = cloneMap(input.Variant)
	return shortcutInput{value: input}, nil
}

// CanonicalScenario is the immutable compiled domain aggregate and its stable
// canonical JSON identity.
type CanonicalScenario struct {
	scenario domain.Scenario
	encoded  []byte
	digest   domain.Digest
}

// Scenario returns the immutable domain aggregate.
func (compiled CanonicalScenario) Scenario() domain.Scenario {
	return compiled.scenario
}

// Bytes returns a copy of the canonical JSON representation.
func (compiled CanonicalScenario) Bytes() []byte {
	return append([]byte(nil), compiled.encoded...)
}

// Digest returns the canonical Scenario content digest.
func (compiled CanonicalScenario) Digest() domain.Digest {
	return compiled.digest
}

// CompileReceipt records the exact catalog snapshot and profile resolutions
// used by deterministic compilation.
type CompileReceipt struct {
	catalogDigest        domain.Digest
	resolutions          []catalog.ResolvedSelection
	auxiliaryResolutions []catalog.ResolvedSelection
}

// CatalogDigest returns the immutable catalog snapshot digest.
func (receipt CompileReceipt) CatalogDigest() domain.Digest {
	return receipt.catalogDigest
}

// Resolutions returns a copy of the source-backed selections.
func (receipt CompileReceipt) Resolutions() []catalog.ResolvedSelection {
	return append([]catalog.ResolvedSelection(nil), receipt.resolutions...)
}

// AuxiliaryResolutions returns source-backed selections for Auxiliary Device
// Pools in the same canonical Node Group and pool order as the Scenario.
func (receipt CompileReceipt) AuxiliaryResolutions() []catalog.ResolvedSelection {
	return append(
		[]catalog.ResolvedSelection(nil),
		receipt.auxiliaryResolutions...,
	)
}

// TypedRevisionChange is the sealed set of declarative Scenario field
// revisions. Callers construct values with Health or Scale.
type TypedRevisionChange interface {
	scenarioRevisionChange()
}

type healthChange struct {
	group   string
	pool    string
	healthy int64
}

func (healthChange) scenarioRevisionChange() {}

type scaleChange struct {
	group    string
	replicas int64
}

func (scaleChange) scenarioRevisionChange() {}

// Health selects exactly one Accelerator Pool healthy count.
func Health(group, pool string, healthy int64) (TypedRevisionChange, error) {
	if _, err := domain.ParseName(group); err != nil {
		return nil, fmt.Errorf("health Node Group: %w", err)
	}
	if _, err := domain.ParseName(pool); err != nil {
		return nil, fmt.Errorf("health Accelerator Pool: %w", err)
	}
	if healthy < 0 {
		return nil, fmt.Errorf("healthy accelerator count must be non-negative: %d", healthy)
	}
	return healthChange{group: group, pool: pool, healthy: healthy}, nil
}

// Scale selects exactly one Node Group replica count.
func Scale(group string, replicas int64) (TypedRevisionChange, error) {
	if _, err := domain.ParseName(group); err != nil {
		return nil, fmt.Errorf("scale Node Group: %w", err)
	}
	if _, err := domain.NewReplicaCount(replicas); err != nil {
		return nil, err
	}
	return scaleChange{group: group, replicas: replicas}, nil
}

type rawScenario struct {
	Metadata rawMetadata `yaml:"metadata"`
	Spec     rawSpec     `yaml:"spec"`
}

type rawMetadata struct {
	Name string `yaml:"name"`
}

type rawSpec struct {
	Fidelity   string         `yaml:"fidelity"`
	Acceptance rawAcceptance  `yaml:"acceptance"`
	NodeGroups []rawNodeGroup `yaml:"nodeGroups"`
}

type rawAcceptance struct {
	ProvisionalProfiles bool `yaml:"provisionalProfiles"`
}

type rawNodeGroup struct {
	Name                 string                   `yaml:"name"`
	Replicas             int64                    `yaml:"replicas"`
	Node                 rawNode                  `yaml:"node"`
	AcceleratorPools     []rawAcceleratorPool     `yaml:"acceleratorPools"`
	AuxiliaryDevicePools []rawAuxiliaryDevicePool `yaml:"auxiliaryDevicePools"`
}

type rawNode struct {
	Capacity  map[string]string `yaml:"capacity"`
	Placement map[string]string `yaml:"placement"`
	Labels    map[string]string `yaml:"labels"`
	Taints    []rawTaint        `yaml:"taints"`
}

type rawTaint struct {
	Key    string `yaml:"key"`
	Value  string `yaml:"value"`
	Effect string `yaml:"effect"`
}

type rawAcceleratorPool struct {
	Name     string            `yaml:"name"`
	Profile  rawProfile        `yaml:"profile"`
	Model    string            `yaml:"model"`
	Contract string            `yaml:"contract"`
	Resource string            `yaml:"resource"`
	Variant  map[string]string `yaml:"variant"`
	Count    int64             `yaml:"count"`
	Healthy  *int64            `yaml:"healthy"`
}

type rawAuxiliaryDevicePool struct {
	Name                       string     `yaml:"name"`
	Profile                    rawProfile `yaml:"profile"`
	Contract                   string     `yaml:"contract"`
	Resource                   string     `yaml:"resource"`
	ResourceName               string     `yaml:"resourceName"`
	Count                      int64      `yaml:"count"`
	Available                  *int64     `yaml:"available"`
	AssociatedAcceleratorPools []string   `yaml:"associatedAcceleratorPools"`
}

type rawProfile struct {
	ID       string `yaml:"id"`
	Revision string `yaml:"revision"`
	Digest   string `yaml:"digest"`
}

type canonicalDocument struct {
	Metadata canonicalMetadata `json:"metadata"`
	Spec     canonicalSpec     `json:"spec"`
}

type canonicalMetadata struct {
	Name string `json:"name"`
}

type canonicalSpec struct {
	Fidelity   string               `json:"fidelity"`
	Acceptance canonicalAcceptance  `json:"acceptance"`
	NodeGroups []canonicalNodeGroup `json:"nodeGroups"`
}

type canonicalAcceptance struct {
	ProvisionalProfiles bool `json:"provisionalProfiles"`
}

type canonicalNodeGroup struct {
	Name                 string                         `json:"name"`
	Replicas             uint64                         `json:"replicas"`
	Node                 canonicalNode                  `json:"node"`
	AcceleratorPools     []canonicalAcceleratorPool     `json:"acceleratorPools"`
	AuxiliaryDevicePools []canonicalAuxiliaryDevicePool `json:"auxiliaryDevicePools,omitempty"`
}

type canonicalNode struct {
	Capacity  map[string]string `json:"capacity"`
	Placement map[string]string `json:"placement"`
	Labels    map[string]string `json:"labels"`
	Taints    []canonicalTaint  `json:"taints"`
}

type canonicalTaint struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Effect string `json:"effect"`
}

type canonicalAcceleratorPool struct {
	Name     string            `json:"name"`
	Profile  canonicalProfile  `json:"profile"`
	Model    string            `json:"model"`
	Contract string            `json:"contract"`
	Resource string            `json:"resource"`
	Variant  map[string]string `json:"variant"`
	Count    uint64            `json:"count"`
	Healthy  uint64            `json:"healthy"`
}

type canonicalAuxiliaryDevicePool struct {
	Name                       string           `json:"name"`
	Profile                    canonicalProfile `json:"profile"`
	Contract                   string           `json:"contract"`
	Resource                   string           `json:"resource"`
	ResourceName               string           `json:"resourceName"`
	Count                      uint64           `json:"count"`
	Available                  uint64           `json:"available"`
	AssociatedAcceleratorPools []string         `json:"associatedAcceleratorPools"`
}

type canonicalProfile struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
	Digest   string `json:"digest"`
}

// Compile strictly decodes, resolves, validates, canonicalizes, and digests one
// sealed input without contacting Kubernetes.
func Compile(input Input, catalogSnapshot catalog.Snapshot) (
	CanonicalScenario,
	CompileReceipt,
	error,
) {
	var raw rawScenario
	var err error
	switch typed := input.(type) {
	case documentInput:
		raw, err = decodeDocument(typed.encoded)
	case shortcutInput:
		raw, err = compileShortcutInput(typed.value, catalogSnapshot)
	default:
		return CanonicalScenario{}, CompileReceipt{}, fmt.Errorf("unsupported Scenario input")
	}
	if err != nil {
		return CanonicalScenario{}, CompileReceipt{}, err
	}
	return compileRaw(raw, catalogSnapshot)
}

// Revise applies exactly one typed field change to the current canonical
// Scenario, then runs the same pure compiler and catalog resolution path.
func Revise(
	current CanonicalScenario,
	change TypedRevisionChange,
	catalogSnapshot catalog.Snapshot,
) (CanonicalScenario, CompileReceipt, error) {
	if len(current.encoded) == 0 {
		return CanonicalScenario{}, CompileReceipt{}, fmt.Errorf("current canonical Scenario is empty")
	}
	raw, err := decodeDocument(current.encoded)
	if err != nil {
		return CanonicalScenario{}, CompileReceipt{}, fmt.Errorf(
			"decode current canonical Scenario: %w",
			err,
		)
	}
	switch typed := change.(type) {
	case healthChange:
		foundGroup := false
		foundPool := false
		for groupIndex := range raw.Spec.NodeGroups {
			group := &raw.Spec.NodeGroups[groupIndex]
			if group.Name != typed.group {
				continue
			}
			foundGroup = true
			for poolIndex := range group.AcceleratorPools {
				pool := &group.AcceleratorPools[poolIndex]
				if pool.Name != typed.pool {
					continue
				}
				healthy := typed.healthy
				pool.Healthy = &healthy
				foundPool = true
				break
			}
			break
		}
		if !foundGroup {
			return CanonicalScenario{}, CompileReceipt{}, fmt.Errorf(
				"Node Group %q was not found",
				typed.group,
			)
		}
		if !foundPool {
			return CanonicalScenario{}, CompileReceipt{}, fmt.Errorf(
				"Accelerator Pool %q was not found in Node Group %q",
				typed.pool,
				typed.group,
			)
		}
	case scaleChange:
		found := false
		for groupIndex := range raw.Spec.NodeGroups {
			group := &raw.Spec.NodeGroups[groupIndex]
			if group.Name == typed.group {
				group.Replicas = typed.replicas
				found = true
				break
			}
		}
		if !found {
			return CanonicalScenario{}, CompileReceipt{}, fmt.Errorf(
				"Node Group %q was not found",
				typed.group,
			)
		}
	default:
		return CanonicalScenario{}, CompileReceipt{}, fmt.Errorf("unsupported typed Scenario revision")
	}
	return compileRaw(raw, catalogSnapshot)
}

func compileShortcutInput(input ShortcutInput, catalogSnapshot catalog.Snapshot) (rawScenario, error) {
	fidelityName := input.Fidelity
	if fidelityName == "" {
		fidelityName = "scheduling"
	}
	fidelity, err := domain.ParseFidelityMode(fidelityName)
	if err != nil {
		return rawScenario{}, err
	}
	profile, found := findProfile(catalogSnapshot, input.ProfileID)
	if !found {
		return rawScenario{}, fmt.Errorf("unknown profile %q", input.ProfileID)
	}
	resolved, err := catalogSnapshot.Resolve(catalog.ResolveRequest{
		ProfileID:         input.ProfileID,
		ModelID:           input.ModelID,
		ContractID:        input.ContractID,
		ResourceAlias:     input.ResourceAlias,
		Fidelity:          fidelity,
		AcceptProvisional: input.AcceptsProvisionalProfiles,
	})
	if err != nil {
		return rawScenario{}, err
	}
	healthy := input.AcceleratorsPerNode
	if input.HealthyPerNode != nil {
		healthy = *input.HealthyPerNode
	}
	return rawScenario{
		Metadata: rawMetadata{Name: input.Name},
		Spec: rawSpec{
			Fidelity: fidelityName,
			Acceptance: rawAcceptance{
				ProvisionalProfiles: input.AcceptsProvisionalProfiles,
			},
			NodeGroups: []rawNodeGroup{{
				Name:     "nodes",
				Replicas: input.Nodes,
				Node: rawNode{
					Capacity:  map[string]string{},
					Placement: map[string]string{},
					Labels:    map[string]string{},
					Taints:    []rawTaint{},
				},
				AcceleratorPools: []rawAcceleratorPool{{
					Name: "accelerators",
					Profile: rawProfile{
						ID:       input.ProfileID,
						Revision: profile.Revision(),
						Digest:   profile.Digest().String(),
					},
					Model:    resolved.ModelID(),
					Contract: resolved.ContractID(),
					Resource: resolved.ResourceAlias(),
					Variant:  cloneMap(input.Variant),
					Count:    input.AcceleratorsPerNode,
					Healthy:  &healthy,
				}},
			}},
		},
	}, nil
}

func decodeDocument(encoded []byte) (rawScenario, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(encoded))
	decoder.KnownFields(true)

	var raw rawScenario
	if err := decoder.Decode(&raw); err != nil {
		return rawScenario{}, fmt.Errorf("decode Scenario: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return rawScenario{}, fmt.Errorf("Scenario input must contain exactly one document")
		}
		return rawScenario{}, fmt.Errorf("decode trailing Scenario document: %w", err)
	}
	return raw, nil
}

func compileRaw(raw rawScenario, catalogSnapshot catalog.Snapshot) (
	CanonicalScenario,
	CompileReceipt,
	error,
) {
	scenarioName, err := domain.ParseName(raw.Metadata.Name)
	if err != nil {
		return CanonicalScenario{}, CompileReceipt{}, fmt.Errorf("Scenario name: %w", err)
	}
	fidelity, err := domain.ParseFidelityMode(raw.Spec.Fidelity)
	if err != nil {
		return CanonicalScenario{}, CompileReceipt{}, err
	}
	if len(raw.Spec.NodeGroups) == 0 {
		return CanonicalScenario{}, CompileReceipt{}, fmt.Errorf("Scenario requires Node Groups")
	}

	rawGroups := append([]rawNodeGroup(nil), raw.Spec.NodeGroups...)
	slices.SortFunc(rawGroups, func(left, right rawNodeGroup) int {
		return strings.Compare(left.Name, right.Name)
	})
	for index := 1; index < len(rawGroups); index++ {
		if rawGroups[index-1].Name == rawGroups[index].Name {
			return CanonicalScenario{}, CompileReceipt{}, fmt.Errorf(
				"duplicate Node Group name %q",
				rawGroups[index].Name,
			)
		}
	}
	profileSummaries := make(map[string]catalog.ProfileSummary)
	for _, profile := range catalogSnapshot.List() {
		profileSummaries[profile.ID()] = profile
	}
	groups := make([]domain.NodeGroup, 0, len(rawGroups))
	canonicalGroups := make([]canonicalNodeGroup, 0, len(rawGroups))
	resolutions := make([]catalog.ResolvedSelection, 0)
	auxiliaryResolutions := make([]catalog.ResolvedSelection, 0)
	for _, rawGroup := range rawGroups {
		group, canonicalGroup, groupResolutions, groupAuxiliaryResolutions, err := compileGroup(
			rawGroup,
			fidelity,
			raw.Spec.Acceptance.ProvisionalProfiles,
			catalogSnapshot,
			profileSummaries,
		)
		if err != nil {
			return CanonicalScenario{}, CompileReceipt{}, err
		}
		groups = append(groups, group)
		canonicalGroups = append(canonicalGroups, canonicalGroup)
		resolutions = append(resolutions, groupResolutions...)
		auxiliaryResolutions = append(auxiliaryResolutions, groupAuxiliaryResolutions...)
	}

	aggregate, err := domain.NewScenario(domain.ScenarioInput{
		Name:                       scenarioName,
		Fidelity:                   fidelity,
		AcceptsProvisionalProfiles: raw.Spec.Acceptance.ProvisionalProfiles,
		NodeGroups:                 groups,
	})
	if err != nil {
		return CanonicalScenario{}, CompileReceipt{}, err
	}
	canonical := canonicalDocument{
		Metadata: canonicalMetadata{Name: scenarioName.String()},
		Spec: canonicalSpec{
			Fidelity: fidelity.String(),
			Acceptance: canonicalAcceptance{
				ProvisionalProfiles: raw.Spec.Acceptance.ProvisionalProfiles,
			},
			NodeGroups: canonicalGroups,
		},
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return CanonicalScenario{}, CompileReceipt{}, fmt.Errorf("encode canonical Scenario: %w", err)
	}
	digest, err := digest(encoded)
	if err != nil {
		return CanonicalScenario{}, CompileReceipt{}, err
	}
	return CanonicalScenario{
			scenario: aggregate,
			encoded:  encoded,
			digest:   digest,
		}, CompileReceipt{
			catalogDigest:        catalogSnapshot.Digest(),
			resolutions:          resolutions,
			auxiliaryResolutions: auxiliaryResolutions,
		}, nil
}

func compileGroup(
	raw rawNodeGroup,
	fidelity domain.FidelityMode,
	acceptsProvisional bool,
	catalogSnapshot catalog.Snapshot,
	profileSummaries map[string]catalog.ProfileSummary,
) (
	domain.NodeGroup,
	canonicalNodeGroup,
	[]catalog.ResolvedSelection,
	[]catalog.ResolvedSelection,
	error,
) {
	name, err := domain.ParseName(raw.Name)
	if err != nil {
		return domain.NodeGroup{}, canonicalNodeGroup{}, nil, nil, fmt.Errorf("Node Group name: %w", err)
	}
	replicas, err := domain.NewReplicaCount(raw.Replicas)
	if err != nil {
		return domain.NodeGroup{}, canonicalNodeGroup{}, nil, nil, err
	}
	node, canonicalNode, err := compileNode(raw.Node)
	if err != nil {
		return domain.NodeGroup{}, canonicalNodeGroup{}, nil, nil, err
	}
	if len(raw.AcceleratorPools) == 0 {
		return domain.NodeGroup{}, canonicalNodeGroup{}, nil, nil, fmt.Errorf(
			"Node Group %q requires Accelerator Pools",
			raw.Name,
		)
	}
	rawPools := append([]rawAcceleratorPool(nil), raw.AcceleratorPools...)
	slices.SortFunc(rawPools, func(left, right rawAcceleratorPool) int {
		return strings.Compare(left.Name, right.Name)
	})
	for index := 1; index < len(rawPools); index++ {
		if rawPools[index-1].Name == rawPools[index].Name {
			return domain.NodeGroup{}, canonicalNodeGroup{}, nil, nil, fmt.Errorf(
				"duplicate Accelerator Pool name %q",
				rawPools[index].Name,
			)
		}
	}
	pools := make([]domain.AcceleratorPool, 0, len(rawPools))
	canonicalPools := make([]canonicalAcceleratorPool, 0, len(rawPools))
	resolutions := make([]catalog.ResolvedSelection, 0, len(rawPools))
	scalarResources := make(map[string]string, len(rawPools))
	type identityOwner struct {
		model string
		pool  string
	}
	identitySignals := make(map[string]identityOwner)
	for _, rawPool := range rawPools {
		pool, canonicalPool, resolved, err := compilePool(
			rawPool,
			fidelity,
			acceptsProvisional,
			catalogSnapshot,
			profileSummaries,
		)
		if err != nil {
			return domain.NodeGroup{}, canonicalNodeGroup{}, nil, nil, err
		}
		if fidelity.String() == "scheduling" {
			if owner, conflict := scalarResources[resolved.ResourceName()]; conflict {
				return domain.NodeGroup{}, canonicalNodeGroup{}, nil, nil, fmt.Errorf(
					"Node Group %q scalar resource %q conflicts between Accelerator Pools %q and %q",
					raw.Name,
					resolved.ResourceName(),
					owner,
					rawPool.Name,
				)
			}
			scalarResources[resolved.ResourceName()] = rawPool.Name
		}
		for _, signal := range resolved.IdentitySignals() {
			identity := signal.Kind() + "\x00" + signal.Key()
			owner, conflict := identitySignals[identity]
			if conflict && owner.model != resolved.ModelID() {
				signalKind := "vendor"
				if signal.Kind() == "dra-attribute" {
					signalKind = "DRA"
				}
				return domain.NodeGroup{}, canonicalNodeGroup{}, nil, nil, fmt.Errorf(
					"Node Group %q %s identity signal %q conflicts between models %q and %q in Accelerator Pools %q and %q",
					raw.Name,
					signalKind,
					signal.Key(),
					owner.model,
					resolved.ModelID(),
					owner.pool,
					rawPool.Name,
				)
			}
			identitySignals[identity] = identityOwner{
				model: resolved.ModelID(),
				pool:  rawPool.Name,
			}
			if signal.Kind() == "node-label" {
				if _, claimed := raw.Node.Labels[signal.Key()]; claimed {
					return domain.NodeGroup{}, canonicalNodeGroup{}, nil, nil, fmt.Errorf(
						"Node label %q conflicts with vendor identity owned by Accelerator Pool %q",
						signal.Key(),
						rawPool.Name,
					)
				}
			}
		}
		pools = append(pools, pool)
		canonicalPools = append(canonicalPools, canonicalPool)
		resolutions = append(resolutions, resolved)
	}

	rawAuxiliaryPools := append(
		[]rawAuxiliaryDevicePool(nil),
		raw.AuxiliaryDevicePools...,
	)
	slices.SortFunc(rawAuxiliaryPools, func(left, right rawAuxiliaryDevicePool) int {
		return strings.Compare(left.Name, right.Name)
	})
	for index := 1; index < len(rawAuxiliaryPools); index++ {
		if rawAuxiliaryPools[index-1].Name == rawAuxiliaryPools[index].Name {
			return domain.NodeGroup{}, canonicalNodeGroup{}, nil, nil, fmt.Errorf(
				"duplicate Auxiliary Device Pool name %q",
				rawAuxiliaryPools[index].Name,
			)
		}
	}
	auxiliaryPools := make([]domain.AuxiliaryDevicePool, 0, len(rawAuxiliaryPools))
	canonicalAuxiliaryPools := make(
		[]canonicalAuxiliaryDevicePool,
		0,
		len(rawAuxiliaryPools),
	)
	auxiliaryResolutions := make(
		[]catalog.ResolvedSelection,
		0,
		len(rawAuxiliaryPools),
	)
	for _, rawPool := range rawAuxiliaryPools {
		pool, canonicalPool, resolved, err := compileAuxiliaryPool(
			rawPool,
			fidelity,
			acceptsProvisional,
			catalogSnapshot,
			profileSummaries,
		)
		if err != nil {
			return domain.NodeGroup{}, canonicalNodeGroup{}, nil, nil, err
		}
		if owner, conflict := scalarResources[resolved.ResourceName()]; conflict {
			return domain.NodeGroup{}, canonicalNodeGroup{}, nil, nil, fmt.Errorf(
				"Node Group %q scalar resource %q conflicts between pool %q and Auxiliary Device Pool %q",
				raw.Name,
				resolved.ResourceName(),
				owner,
				rawPool.Name,
			)
		}
		scalarResources[resolved.ResourceName()] = rawPool.Name
		auxiliaryPools = append(auxiliaryPools, pool)
		canonicalAuxiliaryPools = append(canonicalAuxiliaryPools, canonicalPool)
		auxiliaryResolutions = append(auxiliaryResolutions, resolved)
	}
	group, err := domain.NewNodeGroup(domain.NodeGroupInput{
		Name:           name,
		Replicas:       replicas,
		Node:           node,
		Pools:          pools,
		AuxiliaryPools: auxiliaryPools,
	})
	if err != nil {
		return domain.NodeGroup{}, canonicalNodeGroup{}, nil, nil, err
	}
	return group, canonicalNodeGroup{
		Name:                 name.String(),
		Replicas:             replicas.Value(),
		Node:                 canonicalNode,
		AcceleratorPools:     canonicalPools,
		AuxiliaryDevicePools: canonicalAuxiliaryPools,
	}, resolutions, auxiliaryResolutions, nil
}

func compileNode(raw rawNode) (domain.NodeTemplate, canonicalNode, error) {
	capacity := make(map[string]string, len(raw.Capacity))
	for name, value := range raw.Capacity {
		switch name {
		case "cpu", "memory", "pods", "ephemeral-storage":
		default:
			return domain.NodeTemplate{}, canonicalNode{}, fmt.Errorf(
				"Node base capacity resource %q is unsupported",
				name,
			)
		}
		quantity, err := resource.ParseQuantity(value)
		if err != nil {
			return domain.NodeTemplate{}, canonicalNode{}, fmt.Errorf(
				"Node capacity %q: %w",
				name,
				err,
			)
		}
		if quantity.String() != value {
			return domain.NodeTemplate{}, canonicalNode{}, fmt.Errorf(
				"Node capacity %q quantity %q is not canonical; use %q",
				name,
				value,
				quantity.String(),
			)
		}
		capacity[name] = value
	}
	if _, conflict := raw.Labels["topology.kubernetes.io/zone"]; conflict {
		return domain.NodeTemplate{}, canonicalNode{}, fmt.Errorf(
			"topology.kubernetes.io/zone is owned by node placement",
		)
	}
	for key := range raw.Labels {
		if key == "kubernetes.io/hostname" ||
			strings.HasPrefix(key, "kwok.x-k8s.io/") {
			return domain.NodeTemplate{}, canonicalNode{}, fmt.Errorf(
				"Node label %q is reserved",
				key,
			)
		}
	}
	rawTaints := append([]rawTaint(nil), raw.Taints...)
	slices.SortFunc(rawTaints, func(left, right rawTaint) int {
		if compared := strings.Compare(left.Key, right.Key); compared != 0 {
			return compared
		}
		if compared := strings.Compare(left.Effect, right.Effect); compared != 0 {
			return compared
		}
		return strings.Compare(left.Value, right.Value)
	})
	taints := make([]domain.Taint, 0, len(rawTaints))
	canonicalTaints := make([]canonicalTaint, 0, len(rawTaints))
	taintValues := make(map[string]string, len(rawTaints))
	for _, rawTaint := range rawTaints {
		taint, err := domain.NewTaint(rawTaint.Key, rawTaint.Value, rawTaint.Effect)
		if err != nil {
			return domain.NodeTemplate{}, canonicalNode{}, err
		}
		identity := rawTaint.Key + "\x00" + rawTaint.Effect
		if previous, duplicate := taintValues[identity]; duplicate {
			if previous != rawTaint.Value {
				return domain.NodeTemplate{}, canonicalNode{}, fmt.Errorf(
					"conflicting taint %q with effect %q",
					rawTaint.Key,
					rawTaint.Effect,
				)
			}
			return domain.NodeTemplate{}, canonicalNode{}, fmt.Errorf(
				"duplicate taint %q with effect %q",
				rawTaint.Key,
				rawTaint.Effect,
			)
		}
		taintValues[identity] = rawTaint.Value
		taints = append(taints, taint)
		canonicalTaints = append(canonicalTaints, canonicalTaint(rawTaint))
	}
	template, err := domain.NewNodeTemplate(domain.NodeTemplateInput{
		Capacity:  capacity,
		Placement: raw.Placement,
		Labels:    raw.Labels,
		Taints:    taints,
	})
	if err != nil {
		return domain.NodeTemplate{}, canonicalNode{}, err
	}
	return template, canonicalNode{
		Capacity:  nonNilMap(capacity),
		Placement: nonNilMap(raw.Placement),
		Labels:    nonNilMap(raw.Labels),
		Taints:    nonNilTaints(canonicalTaints),
	}, nil
}

func compilePool(
	raw rawAcceleratorPool,
	fidelity domain.FidelityMode,
	acceptsProvisional bool,
	catalogSnapshot catalog.Snapshot,
	profileSummaries map[string]catalog.ProfileSummary,
) (
	domain.AcceleratorPool,
	canonicalAcceleratorPool,
	catalog.ResolvedSelection,
	error,
) {
	name, err := domain.ParseName(raw.Name)
	if err != nil {
		return domain.AcceleratorPool{}, canonicalAcceleratorPool{}, catalog.ResolvedSelection{}, err
	}
	profileID, err := domain.ParseName(raw.Profile.ID)
	if err != nil {
		return domain.AcceleratorPool{}, canonicalAcceleratorPool{}, catalog.ResolvedSelection{}, err
	}
	profileDigest, err := domain.ParseDigest(raw.Profile.Digest)
	if err != nil {
		return domain.AcceleratorPool{}, canonicalAcceleratorPool{}, catalog.ResolvedSelection{}, err
	}
	profileSummary, found := profileSummaries[raw.Profile.ID]
	if !found {
		return domain.AcceleratorPool{}, canonicalAcceleratorPool{}, catalog.ResolvedSelection{}, fmt.Errorf(
			"unknown profile %q",
			raw.Profile.ID,
		)
	}
	if raw.Profile.Revision != profileSummary.Revision() ||
		profileDigest != profileSummary.Digest() {
		return domain.AcceleratorPool{}, canonicalAcceleratorPool{}, catalog.ResolvedSelection{}, fmt.Errorf(
			"profile %q revision or digest does not match the catalog snapshot",
			raw.Profile.ID,
		)
	}
	resolved, err := catalogSnapshot.Resolve(catalog.ResolveRequest{
		ProfileID:         raw.Profile.ID,
		ModelID:           raw.Model,
		ContractID:        raw.Contract,
		ResourceAlias:     raw.Resource,
		Fidelity:          fidelity,
		AcceptProvisional: acceptsProvisional,
	})
	if err != nil {
		return domain.AcceleratorPool{}, canonicalAcceleratorPool{}, catalog.ResolvedSelection{}, err
	}
	modelID, err := domain.ParseName(resolved.ModelID())
	if err != nil {
		return domain.AcceleratorPool{}, canonicalAcceleratorPool{}, catalog.ResolvedSelection{}, err
	}
	profile, err := domain.NewProfileReference(
		profileID,
		profileSummary.Revision(),
		profileSummary.Digest(),
	)
	if err != nil {
		return domain.AcceleratorPool{}, canonicalAcceleratorPool{}, catalog.ResolvedSelection{}, err
	}
	healthy := raw.Count
	if raw.Healthy != nil {
		healthy = *raw.Healthy
	}
	counts, err := domain.NewPoolCounts(raw.Count, healthy)
	if err != nil {
		return domain.AcceleratorPool{}, canonicalAcceleratorPool{}, catalog.ResolvedSelection{}, err
	}
	pool, err := domain.NewAcceleratorPool(domain.AcceleratorPoolInput{
		Name:     name,
		Profile:  profile,
		Model:    modelID,
		Contract: resolved.ContractID(),
		Resource: resolved.ResourceAlias(),
		Variant:  raw.Variant,
		Counts:   counts,
	})
	if err != nil {
		return domain.AcceleratorPool{}, canonicalAcceleratorPool{}, catalog.ResolvedSelection{}, err
	}
	return pool, canonicalAcceleratorPool{
		Name: name.String(),
		Profile: canonicalProfile{
			ID:       profileID.String(),
			Revision: profileSummary.Revision(),
			Digest:   profileSummary.Digest().String(),
		},
		Model:    resolved.ModelID(),
		Contract: resolved.ContractID(),
		Resource: resolved.ResourceAlias(),
		Variant:  nonNilMap(raw.Variant),
		Count:    counts.Total(),
		Healthy:  counts.Healthy(),
	}, resolved, nil
}

func compileAuxiliaryPool(
	raw rawAuxiliaryDevicePool,
	fidelity domain.FidelityMode,
	acceptsProvisional bool,
	catalogSnapshot catalog.Snapshot,
	profileSummaries map[string]catalog.ProfileSummary,
) (
	domain.AuxiliaryDevicePool,
	canonicalAuxiliaryDevicePool,
	catalog.ResolvedSelection,
	error,
) {
	emptyPool := domain.AuxiliaryDevicePool{}
	emptyCanonical := canonicalAuxiliaryDevicePool{}
	emptyResolution := catalog.ResolvedSelection{}
	name, err := domain.ParseName(raw.Name)
	if err != nil {
		return emptyPool, emptyCanonical, emptyResolution, err
	}
	profileID, err := domain.ParseName(raw.Profile.ID)
	if err != nil {
		return emptyPool, emptyCanonical, emptyResolution, err
	}
	profileDigest, err := domain.ParseDigest(raw.Profile.Digest)
	if err != nil {
		return emptyPool, emptyCanonical, emptyResolution, err
	}
	profileSummary, found := profileSummaries[raw.Profile.ID]
	if !found {
		return emptyPool, emptyCanonical, emptyResolution, fmt.Errorf(
			"unknown auxiliary profile %q", raw.Profile.ID,
		)
	}
	if raw.Profile.Revision != profileSummary.Revision() ||
		profileDigest != profileSummary.Digest() {
		return emptyPool, emptyCanonical, emptyResolution, fmt.Errorf(
			"auxiliary profile %q revision or digest does not match the catalog snapshot",
			raw.Profile.ID,
		)
	}
	resolved, err := catalogSnapshot.ResolveAuxiliary(catalog.ResolveAuxiliaryRequest{
		ProfileID:         raw.Profile.ID,
		ContractID:        raw.Contract,
		ResourceAlias:     raw.Resource,
		ResourceName:      raw.ResourceName,
		Fidelity:          fidelity,
		AcceptProvisional: acceptsProvisional,
	})
	if err != nil {
		return emptyPool, emptyCanonical, emptyResolution, err
	}
	profile, err := domain.NewProfileReference(
		profileID,
		profileSummary.Revision(),
		profileSummary.Digest(),
	)
	if err != nil {
		return emptyPool, emptyCanonical, emptyResolution, err
	}
	available := raw.Count
	if raw.Available != nil {
		available = *raw.Available
	}
	counts, err := domain.NewAuxiliaryCounts(raw.Count, available)
	if err != nil {
		return emptyPool, emptyCanonical, emptyResolution, err
	}
	associationNames := append([]string(nil), raw.AssociatedAcceleratorPools...)
	slices.Sort(associationNames)
	associations := make([]domain.Name, 0, len(associationNames))
	for _, association := range associationNames {
		parsed, err := domain.ParseName(association)
		if err != nil {
			return emptyPool, emptyCanonical, emptyResolution, fmt.Errorf(
				"Auxiliary Device Pool %q association: %w", raw.Name, err,
			)
		}
		associations = append(associations, parsed)
	}
	pool, err := domain.NewAuxiliaryDevicePool(domain.AuxiliaryDevicePoolInput{
		Name:                       name,
		Profile:                    profile,
		Contract:                   resolved.ContractID(),
		Resource:                   resolved.ResourceAlias(),
		ResourceName:               resolved.ResourceName(),
		Counts:                     counts,
		AssociatedAcceleratorPools: associations,
	})
	if err != nil {
		return emptyPool, emptyCanonical, emptyResolution, err
	}
	return pool, canonicalAuxiliaryDevicePool{
		Name: name.String(),
		Profile: canonicalProfile{
			ID:       profileID.String(),
			Revision: profileSummary.Revision(),
			Digest:   profileSummary.Digest().String(),
		},
		Contract:                   resolved.ContractID(),
		Resource:                   resolved.ResourceAlias(),
		ResourceName:               resolved.ResourceName(),
		Count:                      counts.Total(),
		Available:                  counts.Available(),
		AssociatedAcceleratorPools: associationNames,
	}, resolved, nil
}

func findProfile(snapshot catalog.Snapshot, profileID string) (catalog.ProfileSummary, bool) {
	for _, profile := range snapshot.List() {
		if profile.ID() == profileID {
			return profile, true
		}
	}
	return catalog.ProfileSummary{}, false
}

func digest(encoded []byte) (domain.Digest, error) {
	sum := sha256.Sum256(encoded)
	return domain.ParseDigest("sha256:" + hex.EncodeToString(sum[:]))
}

func nonNilMap(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	return values
}

func cloneMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func nonNilTaints(values []canonicalTaint) []canonicalTaint {
	if values == nil {
		return []canonicalTaint{}
	}
	return values
}
