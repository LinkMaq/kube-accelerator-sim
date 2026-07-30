// Package projection defines the backend-neutral desired and observed graphs
// shared by the maintained Kubernetes resource projections.
package projection

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/LinkMaq/kube-accelerator-sim/internal/catalog"
	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

const (
	ScenarioLabel     = "simulation.kasim.io/scenario"
	NodeGroupLabel    = "simulation.kasim.io/node-group"
	ReplicaIndexLabel = "simulation.kasim.io/replica-index"
)

// ResourceProjection is the internal behavior seam between backend-neutral
// desired state and one Kubernetes-visible resource representation.
type ResourceProjection interface {
	Support(cluster.TargetCapabilities, DesiredGraph) SupportReport
	Render(DesiredGraph, cluster.TargetCapabilities) (ProjectionFragment, error)
	Assess(ObservedGraph, ProjectionFragment) FidelityReport
}

// BuildInput binds one compiled Scenario to an accepted instance identity.
// Resolutions must be the receipt returned by the same compiler invocation.
type BuildInput struct {
	InstanceName domain.Name
	InstanceUID  domain.InstanceUID
	Generation   domain.Generation
	Scenario     domain.Scenario
	Resolutions  []catalog.ResolvedSelection
}

// DesiredGraph is the immutable backend-neutral realization of every
// Synthetic Node and Accelerator Pool in one accepted revision.
type DesiredGraph struct {
	instanceName domain.Name
	instanceUID  domain.InstanceUID
	fidelity     domain.FidelityMode
	generation   domain.Generation
	nodes        []DesiredNode
}

// DesiredNode keeps identity, portable Node intent, Accelerator Pools, and
// the scheduling gate together for one stable replica.
type DesiredNode struct {
	name                      string
	group                     string
	replica                   uint64
	labels                    map[string]string
	baseCapacity              map[string]string
	placement                 map[string]string
	taints                    []domain.Taint
	pools                     []DesiredPool
	requiresReady             bool
	requiresLease             bool
	schedulingInitiallyClosed bool
}

// DesiredPool is one source-resolved Accelerator Pool on a Synthetic Node.
type DesiredPool struct {
	name            string
	profileID       string
	modelID         string
	contractID      string
	resourceName    string
	capacity        uint64
	allocatable     uint64
	identitySignals []IdentitySignal
}

// IdentitySignal preserves a source-backed key. A projection may emit a
// vendor label only when the catalog also carries an exact evidenced value;
// model IDs are never substituted for missing vendor values.
type IdentitySignal struct {
	Kind  string
	Key   string
	Value string
}

// Build creates stable replica identities and verifies that the compiler
// receipt still matches every pool before resource rendering can begin.
func Build(input BuildInput) (DesiredGraph, error) {
	if input.InstanceName.String() == "" ||
		input.InstanceUID.String() == "" ||
		input.Generation.Value() == 0 {
		return DesiredGraph{}, fmt.Errorf(
			"desired graph requires instance name, UID, and positive generation",
		)
	}
	if input.Scenario.Name() != input.InstanceName {
		return DesiredGraph{}, fmt.Errorf(
			"Scenario name %q does not match instance name %q",
			input.Scenario.Name(),
			input.InstanceName,
		)
	}

	poolCount := 0
	for _, group := range input.Scenario.NodeGroups() {
		poolCount += len(group.Pools())
	}
	if len(input.Resolutions) != poolCount {
		return DesiredGraph{}, fmt.Errorf(
			"compile receipt has %d resolutions for %d Accelerator Pools",
			len(input.Resolutions),
			poolCount,
		)
	}

	nodes := make([]DesiredNode, 0)
	resolutionIndex := 0
	for _, group := range input.Scenario.NodeGroups() {
		pools := make([]DesiredPool, 0, len(group.Pools()))
		resourceOwners := make(map[string]string, len(group.Pools()))
		for _, pool := range group.Pools() {
			resolved := input.Resolutions[resolutionIndex]
			resolutionIndex++
			if err := verifyResolution(pool, resolved); err != nil {
				return DesiredGraph{}, fmt.Errorf(
					"Node Group %q Accelerator Pool %q: %w",
					group.Name(),
					pool.Name(),
					err,
				)
			}
			if owner, collision := resourceOwners[resolved.ResourceName()]; collision {
				return DesiredGraph{}, fmt.Errorf(
					"Node Group %q resource %q collides between Accelerator Pools %q and %q",
					group.Name(),
					resolved.ResourceName(),
					owner,
					pool.Name(),
				)
			}
			resourceOwners[resolved.ResourceName()] = pool.Name().String()

			signals := make([]IdentitySignal, 0, len(resolved.IdentitySignals()))
			for _, signal := range resolved.IdentitySignals() {
				signals = append(signals, IdentitySignal{
					Kind: signal.Kind(),
					Key:  signal.Key(),
				})
			}
			pools = append(pools, DesiredPool{
				name:            pool.Name().String(),
				profileID:       pool.Profile().ID().String(),
				modelID:         resolved.ModelID(),
				contractID:      resolved.ContractID(),
				resourceName:    resolved.ResourceName(),
				capacity:        pool.Counts().Total(),
				allocatable:     pool.Counts().Healthy(),
				identitySignals: signals,
			})
		}

		for replica := uint64(0); replica < group.Replicas().Value(); replica++ {
			name, err := domain.SyntheticNodeName(
				input.InstanceName,
				input.InstanceUID,
				group.Name(),
				replica,
			)
			if err != nil {
				return DesiredGraph{}, err
			}
			labels := group.Node().Labels()
			if labels == nil {
				labels = make(map[string]string)
			}
			labels["kubernetes.io/hostname"] = name.String()
			labels[ScenarioLabel] = input.InstanceName.String()
			labels[NodeGroupLabel] = group.Name().String()
			labels[ReplicaIndexLabel] = strconv.FormatUint(replica, 10)
			if zone := group.Node().Placement()["zone"]; zone != "" {
				labels["topology.kubernetes.io/zone"] = zone
			}
			nodes = append(nodes, DesiredNode{
				name:                      name.String(),
				group:                     group.Name().String(),
				replica:                   replica,
				labels:                    labels,
				baseCapacity:              group.Node().Capacity(),
				placement:                 group.Node().Placement(),
				taints:                    group.Node().Taints(),
				pools:                     cloneDesiredPools(pools),
				requiresReady:             true,
				requiresLease:             true,
				schedulingInitiallyClosed: true,
			})
		}
	}
	slices.SortFunc(nodes, func(left, right DesiredNode) int {
		return strings.Compare(left.name, right.name)
	})
	return DesiredGraph{
		instanceName: input.InstanceName,
		instanceUID:  input.InstanceUID,
		fidelity:     input.Scenario.Fidelity(),
		generation:   input.Generation,
		nodes:        nodes,
	}, nil
}

func verifyResolution(
	pool domain.AcceleratorPool,
	resolved catalog.ResolvedSelection,
) error {
	switch {
	case pool.Profile().Digest() != resolved.ProfileDigest():
		return fmt.Errorf("profile digest does not match compile receipt")
	case pool.Model().String() != resolved.ModelID():
		return fmt.Errorf("model does not match compile receipt")
	case pool.Contract() != resolved.ContractID():
		return fmt.Errorf("Resource Contract does not match compile receipt")
	case pool.Resource() != resolved.ResourceAlias():
		return fmt.Errorf("resource alias does not match compile receipt")
	case resolved.ResourceName() == "":
		return fmt.Errorf("compile receipt has no source-backed resource name")
	}
	return nil
}

func (graph DesiredGraph) Fidelity() domain.FidelityMode {
	return graph.fidelity
}

func (graph DesiredGraph) InstanceName() domain.Name {
	return graph.instanceName
}

func (graph DesiredGraph) InstanceUID() domain.InstanceUID {
	return graph.instanceUID
}

func (graph DesiredGraph) Generation() domain.Generation {
	return graph.generation
}

func (graph DesiredGraph) Nodes() []DesiredNode {
	return cloneDesiredNodes(graph.nodes)
}

func (node DesiredNode) Name() string {
	return node.name
}

func (node DesiredNode) Group() string {
	return node.group
}

func (node DesiredNode) Replica() uint64 {
	return node.replica
}

func (node DesiredNode) Labels() map[string]string {
	return cloneStringMap(node.labels)
}

func (node DesiredNode) BaseCapacity() map[string]string {
	return cloneStringMap(node.baseCapacity)
}

func (node DesiredNode) Placement() map[string]string {
	return cloneStringMap(node.placement)
}

func (node DesiredNode) Taints() []domain.Taint {
	return append([]domain.Taint(nil), node.taints...)
}

func (node DesiredNode) Pools() []DesiredPool {
	return cloneDesiredPools(node.pools)
}

func (node DesiredNode) RequiresReady() bool {
	return node.requiresReady
}

func (node DesiredNode) RequiresLease() bool {
	return node.requiresLease
}

func (node DesiredNode) SchedulingInitiallyClosed() bool {
	return node.schedulingInitiallyClosed
}

func (pool DesiredPool) Name() string {
	return pool.name
}

func (pool DesiredPool) ProfileID() string {
	return pool.profileID
}

func (pool DesiredPool) ModelID() string {
	return pool.modelID
}

func (pool DesiredPool) ContractID() string {
	return pool.contractID
}

func (pool DesiredPool) ResourceName() string {
	return pool.resourceName
}

func (pool DesiredPool) Capacity() uint64 {
	return pool.capacity
}

func (pool DesiredPool) Allocatable() uint64 {
	return pool.allocatable
}

func (pool DesiredPool) IdentitySignals() []IdentitySignal {
	return append([]IdentitySignal(nil), pool.identitySignals...)
}

// SupportIssue is one stable reason a projection cannot represent a graph.
type SupportIssue struct {
	Code    string
	Message string
}

// SupportReport is a bounded fail-closed capability result.
type SupportReport struct {
	supported bool
	issues    []SupportIssue
}

func NewSupportReport(issues []SupportIssue) SupportReport {
	return SupportReport{
		supported: len(issues) == 0,
		issues:    append([]SupportIssue(nil), issues...),
	}
}

func (report SupportReport) Supported() bool {
	return report.supported
}

func (report SupportReport) Issues() []SupportIssue {
	return append([]SupportIssue(nil), report.issues...)
}

// NodeFragmentInput is the validated construction boundary used by projection
// Adapters before fragments are merged by the reconciler.
type NodeFragmentInput struct {
	Name           string
	IdentityLabels map[string]string
	Capacity       map[string]uint64
	Allocatable    map[string]uint64
	RequiresReady  bool
	RequiresLease  bool
}

// ProjectionFragment contains only declarative Node fields and fidelity
// assertions. It contains no Kubernetes object, patch, Pod, or write method.
type ProjectionFragment struct {
	nodes []NodeFragment
}

// NodeFragment is one immutable projection contribution for a Synthetic Node.
type NodeFragment struct {
	name           string
	identityLabels map[string]string
	capacity       map[string]uint64
	allocatable    map[string]uint64
	requiresReady  bool
	requiresLease  bool
}

func NewFragment(inputs []NodeFragmentInput) (ProjectionFragment, error) {
	nodes := make([]NodeFragment, 0, len(inputs))
	names := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		if input.Name == "" {
			return ProjectionFragment{}, fmt.Errorf("projection fragment requires a Node name")
		}
		if _, duplicate := names[input.Name]; duplicate {
			return ProjectionFragment{}, fmt.Errorf(
				"projection fragment has duplicate Node %q",
				input.Name,
			)
		}
		names[input.Name] = struct{}{}
		for resourceName, allocatable := range input.Allocatable {
			capacity, found := input.Capacity[resourceName]
			if !found {
				return ProjectionFragment{}, fmt.Errorf(
					"allocatable resource %q has no capacity",
					resourceName,
				)
			}
			if allocatable > capacity {
				return ProjectionFragment{}, fmt.Errorf(
					"allocatable resource %q exceeds capacity",
					resourceName,
				)
			}
		}
		nodes = append(nodes, NodeFragment{
			name:           input.Name,
			identityLabels: cloneStringMap(input.IdentityLabels),
			capacity:       cloneUint64Map(input.Capacity),
			allocatable:    cloneUint64Map(input.Allocatable),
			requiresReady:  input.RequiresReady,
			requiresLease:  input.RequiresLease,
		})
	}
	slices.SortFunc(nodes, func(left, right NodeFragment) int {
		return strings.Compare(left.name, right.name)
	})
	return ProjectionFragment{nodes: nodes}, nil
}

// Merge combines distinct projection contributions and fails closed when two
// Adapters claim the same label or resource identity on one Node.
func Merge(fragments ...ProjectionFragment) (ProjectionFragment, error) {
	merged := make(map[string]NodeFragment)
	for _, fragment := range fragments {
		for _, node := range fragment.nodes {
			current, found := merged[node.name]
			if !found {
				merged[node.name] = cloneNodeFragment(node)
				continue
			}
			for key := range node.identityLabels {
				if _, collision := current.identityLabels[key]; collision {
					return ProjectionFragment{}, fmt.Errorf(
						"Node %q identity label %q is claimed by multiple projections",
						node.name,
						key,
					)
				}
				current.identityLabels[key] = node.identityLabels[key]
			}
			for resourceName := range node.capacity {
				if _, collision := current.capacity[resourceName]; collision {
					return ProjectionFragment{}, fmt.Errorf(
						"Node %q capacity %q is claimed by multiple projections",
						node.name,
						resourceName,
					)
				}
				current.capacity[resourceName] = node.capacity[resourceName]
			}
			for resourceName := range node.allocatable {
				if _, collision := current.allocatable[resourceName]; collision {
					return ProjectionFragment{}, fmt.Errorf(
						"Node %q allocatable %q is claimed by multiple projections",
						node.name,
						resourceName,
					)
				}
				current.allocatable[resourceName] = node.allocatable[resourceName]
			}
			current.requiresReady = current.requiresReady || node.requiresReady
			current.requiresLease = current.requiresLease || node.requiresLease
			merged[node.name] = current
		}
	}
	inputs := make([]NodeFragmentInput, 0, len(merged))
	for _, node := range merged {
		inputs = append(inputs, NodeFragmentInput{
			Name:           node.name,
			IdentityLabels: node.identityLabels,
			Capacity:       node.capacity,
			Allocatable:    node.allocatable,
			RequiresReady:  node.requiresReady,
			RequiresLease:  node.requiresLease,
		})
	}
	return NewFragment(inputs)
}

func (fragment ProjectionFragment) Nodes() []NodeFragment {
	nodes := make([]NodeFragment, 0, len(fragment.nodes))
	for _, node := range fragment.nodes {
		nodes = append(nodes, cloneNodeFragment(node))
	}
	return nodes
}

// ObjectKinds is deliberately closed: resource projection never owns or
// mutates Pods.
func (fragment ProjectionFragment) ObjectKinds() []string {
	if len(fragment.nodes) == 0 {
		return nil
	}
	return []string{"Node"}
}

func (node NodeFragment) Name() string {
	return node.name
}

func (node NodeFragment) IdentityLabels() map[string]string {
	return cloneStringMap(node.identityLabels)
}

func (node NodeFragment) Capacity() map[string]uint64 {
	return cloneUint64Map(node.capacity)
}

func (node NodeFragment) Allocatable() map[string]uint64 {
	return cloneUint64Map(node.allocatable)
}

func (node NodeFragment) RequiresReady() bool {
	return node.requiresReady
}

func (node NodeFragment) RequiresLease() bool {
	return node.requiresLease
}

// ObservedNodeInput contains already-aggregated Kubernetes observations. Pod
// requests are read-only accounting; no projection operation can mutate Pods.
type ObservedNodeInput struct {
	Name          string
	Exists        bool
	Labels        map[string]string
	Capacity      map[string]uint64
	Allocatable   map[string]uint64
	Requested     map[string]uint64
	Ready         bool
	LeaseObserved bool
	Unschedulable bool
}

// ObservedGraph is the immutable scheduler-visible state assessed by a
// projection Adapter.
type ObservedGraph struct {
	nodes []ObservedNode
}

type ObservedNode struct {
	name          string
	exists        bool
	labels        map[string]string
	capacity      map[string]uint64
	allocatable   map[string]uint64
	requested     map[string]uint64
	ready         bool
	leaseObserved bool
	unschedulable bool
}

func NewObservedGraph(inputs []ObservedNodeInput) (ObservedGraph, error) {
	nodes := make([]ObservedNode, 0, len(inputs))
	names := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		if input.Name == "" {
			return ObservedGraph{}, fmt.Errorf("observed graph requires exact Node names")
		}
		if _, duplicate := names[input.Name]; duplicate {
			return ObservedGraph{}, fmt.Errorf("duplicate observed Node %q", input.Name)
		}
		names[input.Name] = struct{}{}
		nodes = append(nodes, ObservedNode{
			name:          input.Name,
			exists:        input.Exists,
			labels:        cloneStringMap(input.Labels),
			capacity:      cloneUint64Map(input.Capacity),
			allocatable:   cloneUint64Map(input.Allocatable),
			requested:     cloneUint64Map(input.Requested),
			ready:         input.Ready,
			leaseObserved: input.LeaseObserved,
			unschedulable: input.Unschedulable,
		})
	}
	return ObservedGraph{nodes: nodes}, nil
}

func (graph ObservedGraph) Nodes() []ObservedNode {
	nodes := make([]ObservedNode, 0, len(graph.nodes))
	for _, node := range graph.nodes {
		nodes = append(nodes, cloneObservedNode(node))
	}
	return nodes
}

func (node ObservedNode) Name() string {
	return node.name
}

func (node ObservedNode) Exists() bool {
	return node.exists
}

func (node ObservedNode) Labels() map[string]string {
	return cloneStringMap(node.labels)
}

func (node ObservedNode) Capacity() map[string]uint64 {
	return cloneUint64Map(node.capacity)
}

func (node ObservedNode) Allocatable() map[string]uint64 {
	return cloneUint64Map(node.allocatable)
}

func (node ObservedNode) Requested() map[string]uint64 {
	return cloneUint64Map(node.requested)
}

func (node ObservedNode) Ready() bool {
	return node.ready
}

func (node ObservedNode) LeaseObserved() bool {
	return node.leaseObserved
}

func (node ObservedNode) Unschedulable() bool {
	return node.unschedulable
}

// SurfaceState follows the product's four-state fidelity vocabulary.
type SurfaceState string

const (
	SurfaceAchieved    SurfaceState = "achieved"
	SurfaceUnavailable SurfaceState = "unavailable"
)

// SurfaceAssessment is one Node/resource/runtime fact checked from observed
// Kubernetes state.
type SurfaceAssessment struct {
	Node    string
	Surface string
	State   SurfaceState
}

// Overcommitment reports read-only scheduler accounting after a capacity
// reduction. It carries no eviction or Pod mutation instruction.
type Overcommitment struct {
	Node         string
	ResourceName string
	Requested    uint64
	Allocatable  uint64
}

// FidelityReport is the complete bounded result of observed-state assessment.
type FidelityReport struct {
	assessments       []SurfaceAssessment
	overcommitments   []Overcommitment
	openNodes         []string
	mustCloseNodes    []string
	fidelitySatisfied bool
}

func NewFidelityReport(
	assessments []SurfaceAssessment,
	overcommitments []Overcommitment,
	openNodes []string,
	mustCloseNodes []string,
	fidelitySatisfied bool,
) FidelityReport {
	return FidelityReport{
		assessments:       append([]SurfaceAssessment(nil), assessments...),
		overcommitments:   append([]Overcommitment(nil), overcommitments...),
		openNodes:         append([]string(nil), openNodes...),
		mustCloseNodes:    append([]string(nil), mustCloseNodes...),
		fidelitySatisfied: fidelitySatisfied,
	}
}

func (report FidelityReport) Assessments() []SurfaceAssessment {
	return append([]SurfaceAssessment(nil), report.assessments...)
}

func (report FidelityReport) Overcommitments() []Overcommitment {
	return append([]Overcommitment(nil), report.overcommitments...)
}

func (report FidelityReport) MayOpenScheduling() bool {
	return len(report.openNodes) != 0
}

func (report FidelityReport) OpenNodes() []string {
	return append([]string(nil), report.openNodes...)
}

func (report FidelityReport) MustCloseNodes() []string {
	return append([]string(nil), report.mustCloseNodes...)
}

func (report FidelityReport) FidelitySatisfied() bool {
	return report.fidelitySatisfied
}

func cloneDesiredNodes(values []DesiredNode) []DesiredNode {
	cloned := make([]DesiredNode, 0, len(values))
	for _, value := range values {
		value.labels = cloneStringMap(value.labels)
		value.baseCapacity = cloneStringMap(value.baseCapacity)
		value.placement = cloneStringMap(value.placement)
		value.taints = append([]domain.Taint(nil), value.taints...)
		value.pools = cloneDesiredPools(value.pools)
		cloned = append(cloned, value)
	}
	return cloned
}

func cloneDesiredPools(values []DesiredPool) []DesiredPool {
	cloned := make([]DesiredPool, 0, len(values))
	for _, value := range values {
		value.identitySignals = append([]IdentitySignal(nil), value.identitySignals...)
		cloned = append(cloned, value)
	}
	return cloned
}

func cloneNodeFragment(value NodeFragment) NodeFragment {
	value.identityLabels = cloneStringMap(value.identityLabels)
	value.capacity = cloneUint64Map(value.capacity)
	value.allocatable = cloneUint64Map(value.allocatable)
	return value
}

func cloneObservedNode(value ObservedNode) ObservedNode {
	value.labels = cloneStringMap(value.labels)
	value.capacity = cloneUint64Map(value.capacity)
	value.allocatable = cloneUint64Map(value.allocatable)
	value.requested = cloneUint64Map(value.requested)
	return value
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return make(map[string]string)
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneUint64Map(values map[string]uint64) map[string]uint64 {
	if values == nil {
		return make(map[string]uint64)
	}
	cloned := make(map[string]uint64, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
