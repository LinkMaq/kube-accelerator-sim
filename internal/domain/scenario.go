package domain

import (
	"fmt"
	"regexp"
	"strings"
)

var qualifiedResourcePartPattern = regexp.MustCompile(
	`^[A-Za-z0-9](?:[-A-Za-z0-9_.]*[A-Za-z0-9])?$`,
)
var resourceDomainPattern = regexp.MustCompile(
	`^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?(?:\.[a-z0-9](?:[-a-z0-9]*[a-z0-9])?)*$`,
)

// PoolCounts keeps total capacity and requested healthy availability together.
type PoolCounts struct {
	total   uint64
	healthy uint64
}

// ReplicaCount is a non-negative Node Group replica target.
type ReplicaCount struct {
	value uint64
}

// NewReplicaCount preserves zero as a fully scaled-down desired state.
func NewReplicaCount(value int64) (ReplicaCount, error) {
	if value < 0 {
		return ReplicaCount{}, fmt.Errorf("replica count must be non-negative: %d", value)
	}
	return ReplicaCount{value: uint64(value)}, nil
}

func (count ReplicaCount) Value() uint64 {
	return count.value
}

// NewPoolCounts permits zero and partial health but rejects negative or
// impossible availability.
func NewPoolCounts(total, healthy int64) (PoolCounts, error) {
	if total < 0 {
		return PoolCounts{}, fmt.Errorf("accelerator count must be non-negative: %d", total)
	}
	if healthy < 0 {
		return PoolCounts{}, fmt.Errorf("healthy accelerator count must be non-negative: %d", healthy)
	}
	if healthy > total {
		return PoolCounts{}, fmt.Errorf(
			"healthy accelerator count %d exceeds total count %d",
			healthy,
			total,
		)
	}
	return PoolCounts{total: uint64(total), healthy: uint64(healthy)}, nil
}

func (counts PoolCounts) Total() uint64 {
	return counts.total
}

func (counts PoolCounts) Healthy() uint64 {
	return counts.healthy
}

// AuxiliaryCounts keeps the published quantity and schedulable availability
// of an Auxiliary Device Pool separate from physical device health.
type AuxiliaryCounts struct {
	total     uint64
	available uint64
}

func NewAuxiliaryCounts(total, available int64) (AuxiliaryCounts, error) {
	if total < 0 {
		return AuxiliaryCounts{}, fmt.Errorf("auxiliary count must be non-negative: %d", total)
	}
	if available < 0 {
		return AuxiliaryCounts{}, fmt.Errorf(
			"auxiliary available count must be non-negative: %d", available,
		)
	}
	if available > total {
		return AuxiliaryCounts{}, fmt.Errorf(
			"auxiliary available count %d exceeds total count %d", available, total,
		)
	}
	return AuxiliaryCounts{total: uint64(total), available: uint64(available)}, nil
}

func (counts AuxiliaryCounts) Total() uint64     { return counts.total }
func (counts AuxiliaryCounts) Available() uint64 { return counts.available }

// ProfileReference pins one immutable Vendor Profile revision and digest.
type ProfileReference struct {
	id       Name
	revision string
	digest   Digest
}

// NewProfileReference rejects floating or incomplete profile identity.
func NewProfileReference(
	id Name,
	revision string,
	digest Digest,
) (ProfileReference, error) {
	if id.value == "" {
		return ProfileReference{}, fmt.Errorf("profile reference requires an ID")
	}
	if len(revision) == 0 || len(revision) > 128 {
		return ProfileReference{}, fmt.Errorf("profile reference requires a bounded revision")
	}
	for _, character := range revision {
		if character <= ' ' || character > '~' {
			return ProfileReference{}, fmt.Errorf("profile revision contains unsupported characters")
		}
	}
	if digest.value == "" {
		return ProfileReference{}, fmt.Errorf("profile reference requires a digest")
	}
	return ProfileReference{id: id, revision: revision, digest: digest}, nil
}

func (reference ProfileReference) ID() Name {
	return reference.id
}

func (reference ProfileReference) Revision() string {
	return reference.revision
}

func (reference ProfileReference) Digest() Digest {
	return reference.digest
}

// AcceleratorPoolInput describes one homogeneous pool after profile
// references and portable aliases have been selected.
type AcceleratorPoolInput struct {
	Name     Name
	Profile  ProfileReference
	Model    Name
	Contract string
	Resource string
	Variant  map[string]string
	Counts   PoolCounts
}

// AcceleratorPool is one immutable homogeneous accelerator desired state.
type AcceleratorPool struct {
	name     Name
	profile  ProfileReference
	model    Name
	contract string
	resource string
	variant  map[string]string
	counts   PoolCounts
}

// NewAcceleratorPool validates required identity and copies variant data.
func NewAcceleratorPool(input AcceleratorPoolInput) (AcceleratorPool, error) {
	if input.Name.value == "" {
		return AcceleratorPool{}, fmt.Errorf("Accelerator Pool requires a name")
	}
	if input.Profile.id.value == "" {
		return AcceleratorPool{}, fmt.Errorf("Accelerator Pool requires a profile")
	}
	if input.Model.value == "" {
		return AcceleratorPool{}, fmt.Errorf("Accelerator Pool requires a model")
	}
	if !dnsLabelPattern.MatchString(input.Contract) {
		return AcceleratorPool{}, fmt.Errorf("invalid Resource Contract %q", input.Contract)
	}
	if !dnsLabelPattern.MatchString(input.Resource) {
		return AcceleratorPool{}, fmt.Errorf("invalid resource alias %q", input.Resource)
	}
	for key, value := range input.Variant {
		if len(key) == 0 || len(key) > 128 || len(value) > 256 {
			return AcceleratorPool{}, fmt.Errorf("invalid source-backed variant %q", key)
		}
	}
	return AcceleratorPool{
		name:     input.Name,
		profile:  input.Profile,
		model:    input.Model,
		contract: input.Contract,
		resource: input.Resource,
		variant:  cloneStringMap(input.Variant),
		counts:   input.Counts,
	}, nil
}

func (pool AcceleratorPool) Name() Name {
	return pool.name
}

func (pool AcceleratorPool) Profile() ProfileReference {
	return pool.profile
}

func (pool AcceleratorPool) Model() Name {
	return pool.model
}

func (pool AcceleratorPool) Contract() string {
	return pool.contract
}

func (pool AcceleratorPool) Resource() string {
	return pool.resource
}

func (pool AcceleratorPool) Variant() map[string]string {
	return cloneStringMap(pool.variant)
}

func (pool AcceleratorPool) Counts() PoolCounts {
	return pool.counts
}

type AuxiliaryDevicePoolInput struct {
	Name                       Name
	Profile                    ProfileReference
	Contract                   string
	Resource                   string
	ResourceName               string
	Counts                     AuxiliaryCounts
	AssociatedAcceleratorPools []Name
}

// AuxiliaryDevicePool is one immutable, scheduling-only auxiliary resource
// surface. It does not represent physical NIC or data-plane inventory.
type AuxiliaryDevicePool struct {
	name                       Name
	profile                    ProfileReference
	contract                   string
	resource                   string
	resourceName               string
	counts                     AuxiliaryCounts
	associatedAcceleratorPools []Name
}

func NewAuxiliaryDevicePool(input AuxiliaryDevicePoolInput) (AuxiliaryDevicePool, error) {
	if input.Name.value == "" {
		return AuxiliaryDevicePool{}, fmt.Errorf("Auxiliary Device Pool requires a name")
	}
	if input.Profile.id.value == "" {
		return AuxiliaryDevicePool{}, fmt.Errorf("Auxiliary Device Pool requires a profile")
	}
	if !dnsLabelPattern.MatchString(input.Contract) {
		return AuxiliaryDevicePool{}, fmt.Errorf("invalid auxiliary Resource Contract %q", input.Contract)
	}
	if !dnsLabelPattern.MatchString(input.Resource) {
		return AuxiliaryDevicePool{}, fmt.Errorf("invalid auxiliary resource alias %q", input.Resource)
	}
	if !validExtendedResourceName(input.ResourceName) {
		return AuxiliaryDevicePool{}, fmt.Errorf(
			"invalid auxiliary extended resource name %q", input.ResourceName,
		)
	}
	if len(input.AssociatedAcceleratorPools) == 0 {
		return AuxiliaryDevicePool{}, fmt.Errorf(
			"Auxiliary Device Pool requires an Accelerator Pool association",
		)
	}
	seen := make(map[string]struct{}, len(input.AssociatedAcceleratorPools))
	for _, association := range input.AssociatedAcceleratorPools {
		if association.value == "" {
			return AuxiliaryDevicePool{}, fmt.Errorf("invalid Accelerator Pool association")
		}
		if _, duplicate := seen[association.value]; duplicate {
			return AuxiliaryDevicePool{}, fmt.Errorf(
				"duplicate Accelerator Pool association %q", association.value,
			)
		}
		seen[association.value] = struct{}{}
	}
	return AuxiliaryDevicePool{
		name: input.Name, profile: input.Profile, contract: input.Contract,
		resource: input.Resource, resourceName: input.ResourceName,
		counts:                     input.Counts,
		associatedAcceleratorPools: append([]Name(nil), input.AssociatedAcceleratorPools...),
	}, nil
}

func (pool AuxiliaryDevicePool) Name() Name                { return pool.name }
func (pool AuxiliaryDevicePool) Profile() ProfileReference { return pool.profile }
func (pool AuxiliaryDevicePool) Contract() string          { return pool.contract }
func (pool AuxiliaryDevicePool) Resource() string          { return pool.resource }
func (pool AuxiliaryDevicePool) ResourceName() string      { return pool.resourceName }
func (pool AuxiliaryDevicePool) Counts() AuxiliaryCounts   { return pool.counts }
func (pool AuxiliaryDevicePool) AssociatedAcceleratorPools() []Name {
	return append([]Name(nil), pool.associatedAcceleratorPools...)
}

func validExtendedResourceName(value string) bool {
	prefix, name, found := strings.Cut(value, "/")
	return found && len(prefix) <= 253 && len(name) <= 63 &&
		resourceDomainPattern.MatchString(prefix) &&
		qualifiedResourcePartPattern.MatchString(name) &&
		prefix != "kubernetes.io" && !strings.HasSuffix(prefix, ".kubernetes.io")
}

// Taint is portable scheduling intent for one Node template.
type Taint struct {
	key    string
	value  string
	effect string
}

// NewTaint accepts only the three stable Kubernetes taint effects.
func NewTaint(key, value, effect string) (Taint, error) {
	if len(key) == 0 || len(key) > 253 {
		return Taint{}, fmt.Errorf("taint key must contain 1 to 253 bytes")
	}
	if len(value) > 63 {
		return Taint{}, fmt.Errorf("taint value exceeds 63 bytes")
	}
	switch effect {
	case "NoSchedule", "PreferNoSchedule", "NoExecute":
	default:
		return Taint{}, fmt.Errorf("unsupported taint effect %q", effect)
	}
	return Taint{key: key, value: value, effect: effect}, nil
}

func (taint Taint) Key() string {
	return taint.key
}

func (taint Taint) Value() string {
	return taint.value
}

func (taint Taint) Effect() string {
	return taint.effect
}

// NodeTemplateInput is the portable base capacity and placement intent shared
// by every replica of a Node Group.
type NodeTemplateInput struct {
	Capacity  map[string]string
	Placement map[string]string
	Labels    map[string]string
	Taints    []Taint
}

// NodeTemplate is an immutable homogeneous Synthetic Node template.
type NodeTemplate struct {
	capacity  map[string]string
	placement map[string]string
	labels    map[string]string
	taints    []Taint
}

// NewNodeTemplate validates required map entries and copies mutable inputs.
func NewNodeTemplate(input NodeTemplateInput) (NodeTemplate, error) {
	for resource, quantity := range input.Capacity {
		if resource == "" || quantity == "" {
			return NodeTemplate{}, fmt.Errorf("Node capacity requires resource and quantity")
		}
	}
	for key := range input.Placement {
		if key == "" {
			return NodeTemplate{}, fmt.Errorf("Node placement key must not be empty")
		}
	}
	for key := range input.Labels {
		if key == "" {
			return NodeTemplate{}, fmt.Errorf("Node label key must not be empty")
		}
	}
	for _, taint := range input.Taints {
		if taint.key == "" || taint.effect == "" {
			return NodeTemplate{}, fmt.Errorf("Node template contains an invalid taint")
		}
	}
	return NodeTemplate{
		capacity:  cloneStringMap(input.Capacity),
		placement: cloneStringMap(input.Placement),
		labels:    cloneStringMap(input.Labels),
		taints:    append([]Taint(nil), input.Taints...),
	}, nil
}

func (node NodeTemplate) Capacity() map[string]string {
	return cloneStringMap(node.capacity)
}

func (node NodeTemplate) Placement() map[string]string {
	return cloneStringMap(node.placement)
}

func (node NodeTemplate) Labels() map[string]string {
	return cloneStringMap(node.labels)
}

func (node NodeTemplate) Taints() []Taint {
	return append([]Taint(nil), node.taints...)
}

// NodeGroupInput combines one homogeneous template, replica target, and pools.
type NodeGroupInput struct {
	Name           Name
	Replicas       ReplicaCount
	Node           NodeTemplate
	Pools          []AcceleratorPool
	AuxiliaryPools []AuxiliaryDevicePool
}

// NodeGroup repeats one immutable Node template with stable replica indices.
type NodeGroup struct {
	name           Name
	replicas       ReplicaCount
	node           NodeTemplate
	pools          []AcceleratorPool
	auxiliaryPools []AuxiliaryDevicePool
}

// NewNodeGroup rejects invalid or duplicate Accelerator Pool names.
func NewNodeGroup(input NodeGroupInput) (NodeGroup, error) {
	if input.Name.value == "" {
		return NodeGroup{}, fmt.Errorf("Node Group requires a name")
	}
	if len(input.Pools) == 0 {
		return NodeGroup{}, fmt.Errorf("Node Group requires at least one Accelerator Pool")
	}
	poolNames := make(map[string]struct{}, len(input.Pools))
	for _, pool := range input.Pools {
		if pool.name.value == "" {
			return NodeGroup{}, fmt.Errorf("Node Group contains an invalid Accelerator Pool")
		}
		if _, duplicate := poolNames[pool.name.value]; duplicate {
			return NodeGroup{}, fmt.Errorf("duplicate Accelerator Pool name %q", pool.name.value)
		}
		poolNames[pool.name.value] = struct{}{}
	}
	auxiliaryNames := make(map[string]struct{}, len(input.AuxiliaryPools))
	for _, pool := range input.AuxiliaryPools {
		if pool.name.value == "" {
			return NodeGroup{}, fmt.Errorf("Node Group contains an invalid Auxiliary Device Pool")
		}
		if _, duplicate := auxiliaryNames[pool.name.value]; duplicate {
			return NodeGroup{}, fmt.Errorf("duplicate Auxiliary Device Pool name %q", pool.name.value)
		}
		auxiliaryNames[pool.name.value] = struct{}{}
		for _, association := range pool.associatedAcceleratorPools {
			if _, found := poolNames[association.value]; !found {
				return NodeGroup{}, fmt.Errorf(
					"Auxiliary Device Pool %q references unknown Accelerator Pool %q",
					pool.name.value, association.value,
				)
			}
		}
	}
	return NodeGroup{
		name:           input.Name,
		replicas:       input.Replicas,
		node:           input.Node,
		pools:          append([]AcceleratorPool(nil), input.Pools...),
		auxiliaryPools: append([]AuxiliaryDevicePool(nil), input.AuxiliaryPools...),
	}, nil
}

func (group NodeGroup) Name() Name {
	return group.name
}

func (group NodeGroup) Replicas() ReplicaCount {
	return group.replicas
}

func (group NodeGroup) Node() NodeTemplate {
	return group.node
}

func (group NodeGroup) Pools() []AcceleratorPool {
	return append([]AcceleratorPool(nil), group.pools...)
}

func (group NodeGroup) AuxiliaryPools() []AuxiliaryDevicePool {
	return append([]AuxiliaryDevicePool(nil), group.auxiliaryPools...)
}

// ScenarioInput is target-independent desired state for one Scenario.
type ScenarioInput struct {
	Name                       Name
	Fidelity                   FidelityMode
	AcceptsProvisionalProfiles bool
	NodeGroups                 []NodeGroup
}

// Scenario is the immutable vendor-neutral aggregate submitted to the
// lifecycle Module after compilation.
type Scenario struct {
	name                       Name
	fidelity                   FidelityMode
	acceptsProvisionalProfiles bool
	nodeGroups                 []NodeGroup
}

// NewScenario rejects missing identity, unsupported fidelity, and duplicate
// Node Group names.
func NewScenario(input ScenarioInput) (Scenario, error) {
	if input.Name.value == "" {
		return Scenario{}, fmt.Errorf("Scenario requires a name")
	}
	if input.Fidelity.value == "" {
		return Scenario{}, fmt.Errorf("Scenario requires a Fidelity Mode")
	}
	if len(input.NodeGroups) == 0 {
		return Scenario{}, fmt.Errorf("Scenario requires at least one Node Group")
	}
	groupNames := make(map[string]struct{}, len(input.NodeGroups))
	for _, group := range input.NodeGroups {
		if group.name.value == "" {
			return Scenario{}, fmt.Errorf("Scenario contains an invalid Node Group")
		}
		if _, duplicate := groupNames[group.name.value]; duplicate {
			return Scenario{}, fmt.Errorf("duplicate Node Group name %q", group.name.value)
		}
		groupNames[group.name.value] = struct{}{}
	}
	return Scenario{
		name:                       input.Name,
		fidelity:                   input.Fidelity,
		acceptsProvisionalProfiles: input.AcceptsProvisionalProfiles,
		nodeGroups:                 append([]NodeGroup(nil), input.NodeGroups...),
	}, nil
}

func (scenario Scenario) Name() Name {
	return scenario.name
}

func (scenario Scenario) Fidelity() FidelityMode {
	return scenario.fidelity
}

func (scenario Scenario) AcceptsProvisionalProfiles() bool {
	return scenario.acceptsProvisionalProfiles
}

func (scenario Scenario) NodeGroups() []NodeGroup {
	return append([]NodeGroup(nil), scenario.nodeGroups...)
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
