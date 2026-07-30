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
	fidelity       domain.FidelityMode
	nodes          []NodeFragment
	deviceClasses  []DeviceClassFragment
	resourceSlices []ResourceSliceFragment
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

// WithFidelity binds a rendered fragment to the explicit mode selected by the
// controller composition adapter. Concrete projection adapters remain usable
// independently in contract tests.
func (fragment ProjectionFragment) WithFidelity(
	fidelity domain.FidelityMode,
) ProjectionFragment {
	fragment.fidelity = fidelity
	return fragment
}

func (fragment ProjectionFragment) Fidelity() domain.FidelityMode {
	return fragment.fidelity
}

// DeviceAttributeKind is the portable stable DRA v1 attribute subset used by
// the simulator. Other DRA value types and independently gated fields are not
// part of the initial projection contract.
type DeviceAttributeKind string

const (
	DeviceAttributeBool   DeviceAttributeKind = "bool"
	DeviceAttributeString DeviceAttributeKind = "string"
)

// DeviceAttributeValue is one immutable, exactly typed stable DRA attribute.
type DeviceAttributeValue struct {
	kind        DeviceAttributeKind
	boolValue   bool
	stringValue string
}

func NewBoolDeviceAttribute(value bool) DeviceAttributeValue {
	return DeviceAttributeValue{kind: DeviceAttributeBool, boolValue: value}
}

func NewStringDeviceAttribute(value string) (DeviceAttributeValue, error) {
	if value == "" {
		return DeviceAttributeValue{}, fmt.Errorf(
			"string device attribute must not be empty",
		)
	}
	return DeviceAttributeValue{
		kind:        DeviceAttributeString,
		stringValue: value,
	}, nil
}

func (value DeviceAttributeValue) Kind() DeviceAttributeKind {
	return value.kind
}

func (value DeviceAttributeValue) Bool() bool {
	return value.boolValue
}

func (value DeviceAttributeValue) String() string {
	return value.stringValue
}

// DeviceFragmentInput is one simulator-owned device in a stable DRA pool.
type DeviceFragmentInput struct {
	Name       string
	Attributes map[string]DeviceAttributeValue
}

// DeviceClassFragmentInput is the stable v1 DeviceClass intent for one
// Scenario Accelerator Pool. Selectors are CEL expressions and configuration
// is deliberately absent because this projector has no node-local driver.
type DeviceClassFragmentInput struct {
	Name      string
	Driver    string
	Group     string
	Pool      string
	Selectors []string
}

// ResourceSliceFragmentInput is one complete stable-v1 inventory shard. It
// uses NodeName only and therefore cannot request a gated per-device selector.
type ResourceSliceFragmentInput struct {
	Name               string
	Driver             string
	Group              string
	Pool               string
	PoolName           string
	PoolGeneration     int64
	ResourceSliceCount int64
	NodeName           string
	Devices            []DeviceFragmentInput
}

// DeviceClassFragment is one immutable DeviceClass contribution.
type DeviceClassFragment struct {
	name      string
	driver    string
	group     string
	pool      string
	selectors []string
}

// ResourceSliceFragment is one immutable ResourceSlice contribution.
type ResourceSliceFragment struct {
	name               string
	driver             string
	group              string
	pool               string
	poolName           string
	poolGeneration     int64
	resourceSliceCount int64
	nodeName           string
	devices            []DeviceFragment
}

// DeviceFragment is one deterministic simulated device.
type DeviceFragment struct {
	name       string
	attributes map[string]DeviceAttributeValue
}

// NewDRAFragment validates all stable DRA inventory before it can cross into
// reconciliation. Nodes remain in the same fragment because DRA scheduling
// still depends on the synthetic Node lifecycle and scheduling gate.
func NewDRAFragment(
	nodeInputs []NodeFragmentInput,
	classInputs []DeviceClassFragmentInput,
	sliceInputs []ResourceSliceFragmentInput,
) (ProjectionFragment, error) {
	fragment, err := NewFragment(nodeInputs)
	if err != nil {
		return ProjectionFragment{}, err
	}
	classNames := make(map[string]struct{}, len(classInputs))
	classes := make([]DeviceClassFragment, 0, len(classInputs))
	for _, input := range classInputs {
		if input.Name == "" || input.Driver == "" ||
			input.Group == "" || input.Pool == "" ||
			len(input.Selectors) == 0 {
			return ProjectionFragment{}, fmt.Errorf(
				"DeviceClass fragment requires name, driver, pool identity, and selectors",
			)
		}
		if _, duplicate := classNames[input.Name]; duplicate {
			return ProjectionFragment{}, fmt.Errorf(
				"projection fragment has duplicate DeviceClass %q",
				input.Name,
			)
		}
		classNames[input.Name] = struct{}{}
		for _, selector := range input.Selectors {
			if selector == "" {
				return ProjectionFragment{}, fmt.Errorf(
					"DeviceClass %q has an empty selector",
					input.Name,
				)
			}
		}
		classes = append(classes, DeviceClassFragment{
			name:      input.Name,
			driver:    input.Driver,
			group:     input.Group,
			pool:      input.Pool,
			selectors: append([]string(nil), input.Selectors...),
		})
	}

	sliceNames := make(map[string]struct{}, len(sliceInputs))
	deviceNamesByPool := make(map[string]map[string]struct{})
	resourceSlices := make([]ResourceSliceFragment, 0, len(sliceInputs))
	for _, input := range sliceInputs {
		if input.Name == "" || input.Driver == "" ||
			input.Group == "" || input.Pool == "" ||
			input.PoolName == "" || input.PoolGeneration <= 0 ||
			input.ResourceSliceCount <= 0 || input.NodeName == "" {
			return ProjectionFragment{}, fmt.Errorf(
				"ResourceSlice fragment requires exact stable pool and Node identity",
			)
		}
		if len(input.Devices) > 128 {
			return ProjectionFragment{}, fmt.Errorf(
				"ResourceSlice %q exceeds the stable 128-device limit",
				input.Name,
			)
		}
		if _, duplicate := sliceNames[input.Name]; duplicate {
			return ProjectionFragment{}, fmt.Errorf(
				"projection fragment has duplicate ResourceSlice %q",
				input.Name,
			)
		}
		sliceNames[input.Name] = struct{}{}
		poolIdentity := input.Driver + "\x00" + input.PoolName
		deviceNames := deviceNamesByPool[poolIdentity]
		if deviceNames == nil {
			deviceNames = make(map[string]struct{})
			deviceNamesByPool[poolIdentity] = deviceNames
		}
		devices := make([]DeviceFragment, 0, len(input.Devices))
		for _, device := range input.Devices {
			if device.Name == "" {
				return ProjectionFragment{}, fmt.Errorf(
					"ResourceSlice %q has a device without a name",
					input.Name,
				)
			}
			if _, duplicate := deviceNames[device.Name]; duplicate {
				return ProjectionFragment{}, fmt.Errorf(
					"stable DRA pool %q has duplicate device %q",
					input.PoolName,
					device.Name,
				)
			}
			deviceNames[device.Name] = struct{}{}
			attributes := cloneDeviceAttributes(device.Attributes)
			for key, value := range attributes {
				if key == "" ||
					(value.kind != DeviceAttributeBool &&
						value.kind != DeviceAttributeString) ||
					(value.kind == DeviceAttributeString && value.stringValue == "") {
					return ProjectionFragment{}, fmt.Errorf(
						"device %q has invalid attribute %q",
						device.Name,
						key,
					)
				}
			}
			devices = append(devices, DeviceFragment{
				name:       device.Name,
				attributes: attributes,
			})
		}
		slices.SortFunc(devices, func(left, right DeviceFragment) int {
			return strings.Compare(left.name, right.name)
		})
		resourceSlices = append(resourceSlices, ResourceSliceFragment{
			name:               input.Name,
			driver:             input.Driver,
			group:              input.Group,
			pool:               input.Pool,
			poolName:           input.PoolName,
			poolGeneration:     input.PoolGeneration,
			resourceSliceCount: input.ResourceSliceCount,
			nodeName:           input.NodeName,
			devices:            devices,
		})
	}
	slices.SortFunc(classes, func(left, right DeviceClassFragment) int {
		return strings.Compare(left.name, right.name)
	})
	slices.SortFunc(resourceSlices, func(left, right ResourceSliceFragment) int {
		return strings.Compare(left.name, right.name)
	})
	fragment.deviceClasses = classes
	fragment.resourceSlices = resourceSlices
	return fragment, nil
}

// Merge combines distinct projection contributions and fails closed when two
// Adapters claim the same label or resource identity on one Node.
func Merge(fragments ...ProjectionFragment) (ProjectionFragment, error) {
	merged := make(map[string]NodeFragment)
	classInputs := make([]DeviceClassFragmentInput, 0)
	sliceInputs := make([]ResourceSliceFragmentInput, 0)
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
		for _, class := range fragment.deviceClasses {
			classInputs = append(classInputs, DeviceClassFragmentInput{
				Name:      class.name,
				Driver:    class.driver,
				Group:     class.group,
				Pool:      class.pool,
				Selectors: append([]string(nil), class.selectors...),
			})
		}
		for _, resourceSlice := range fragment.resourceSlices {
			devices := make([]DeviceFragmentInput, 0, len(resourceSlice.devices))
			for _, device := range resourceSlice.devices {
				devices = append(devices, DeviceFragmentInput{
					Name:       device.name,
					Attributes: cloneDeviceAttributes(device.attributes),
				})
			}
			sliceInputs = append(sliceInputs, ResourceSliceFragmentInput{
				Name:               resourceSlice.name,
				Driver:             resourceSlice.driver,
				Group:              resourceSlice.group,
				Pool:               resourceSlice.pool,
				PoolName:           resourceSlice.poolName,
				PoolGeneration:     resourceSlice.poolGeneration,
				ResourceSliceCount: resourceSlice.resourceSliceCount,
				NodeName:           resourceSlice.nodeName,
				Devices:            devices,
			})
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
	if len(classInputs) == 0 && len(sliceInputs) == 0 {
		return NewFragment(inputs)
	}
	return NewDRAFragment(inputs, classInputs, sliceInputs)
}

func (fragment ProjectionFragment) Nodes() []NodeFragment {
	nodes := make([]NodeFragment, 0, len(fragment.nodes))
	for _, node := range fragment.nodes {
		nodes = append(nodes, cloneNodeFragment(node))
	}
	return nodes
}

func (fragment ProjectionFragment) DeviceClasses() []DeviceClassFragment {
	result := make([]DeviceClassFragment, 0, len(fragment.deviceClasses))
	for _, value := range fragment.deviceClasses {
		value.selectors = append([]string(nil), value.selectors...)
		result = append(result, value)
	}
	return result
}

func (fragment ProjectionFragment) ResourceSlices() []ResourceSliceFragment {
	result := make([]ResourceSliceFragment, 0, len(fragment.resourceSlices))
	for _, value := range fragment.resourceSlices {
		value.devices = cloneDevices(value.devices)
		result = append(result, value)
	}
	return result
}

// ObjectKinds is deliberately closed: resource projection never owns or
// mutates Pods.
func (fragment ProjectionFragment) ObjectKinds() []string {
	if len(fragment.nodes) == 0 &&
		len(fragment.deviceClasses) == 0 &&
		len(fragment.resourceSlices) == 0 {
		return nil
	}
	kinds := make([]string, 0, 3)
	if len(fragment.deviceClasses) != 0 {
		kinds = append(kinds, "DeviceClass")
	}
	if len(fragment.nodes) != 0 {
		kinds = append(kinds, "Node")
	}
	if len(fragment.resourceSlices) != 0 {
		kinds = append(kinds, "ResourceSlice")
	}
	return kinds
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

func (fragment DeviceClassFragment) Name() string {
	return fragment.name
}

func (fragment DeviceClassFragment) Driver() string {
	return fragment.driver
}

func (fragment DeviceClassFragment) Group() string {
	return fragment.group
}

func (fragment DeviceClassFragment) Pool() string {
	return fragment.pool
}

func (fragment DeviceClassFragment) Selectors() []string {
	return append([]string(nil), fragment.selectors...)
}

func (fragment ResourceSliceFragment) Name() string {
	return fragment.name
}

func (fragment ResourceSliceFragment) Driver() string {
	return fragment.driver
}

func (fragment ResourceSliceFragment) Group() string {
	return fragment.group
}

func (fragment ResourceSliceFragment) Pool() string {
	return fragment.pool
}

func (fragment ResourceSliceFragment) PoolName() string {
	return fragment.poolName
}

func (fragment ResourceSliceFragment) PoolGeneration() int64 {
	return fragment.poolGeneration
}

func (fragment ResourceSliceFragment) ResourceSliceCount() int64 {
	return fragment.resourceSliceCount
}

func (fragment ResourceSliceFragment) NodeName() string {
	return fragment.nodeName
}

func (fragment ResourceSliceFragment) Devices() []DeviceFragment {
	return cloneDevices(fragment.devices)
}

func (device DeviceFragment) Name() string {
	return device.name
}

func (device DeviceFragment) Attributes() map[string]DeviceAttributeValue {
	return cloneDeviceAttributes(device.attributes)
}

func (device DeviceFragment) Attribute(
	key string,
) (DeviceAttributeValue, bool) {
	value, found := device.attributes[key]
	return value, found
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
	nodes          []ObservedNode
	deviceClasses  []ObservedDeviceClass
	resourceSlices []ObservedResourceSlice
	resourceClaims []ObservedResourceClaim
	pods           []ObservedPod
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

// DRAObservedInput adds the stable DRA objects that the projection owns and
// assesses. ResourceClaims and Pods remain read-only workload evidence on the
// Cluster port and are never projection-owned objects.
type DRAObservedInput struct {
	DeviceClasses  []ObservedDeviceClassInput
	ResourceSlices []ObservedResourceSliceInput
	ResourceClaims []ObservedResourceClaimInput
	Pods           []ObservedPodInput
}

type ObservedDeviceClassInput struct {
	Name      string
	Exists    bool
	Selectors []string
}

type ObservedResourceSliceInput struct {
	Name               string
	Exists             bool
	Driver             string
	PoolName           string
	PoolGeneration     int64
	ResourceSliceCount int64
	NodeName           string
	Devices            []ObservedDeviceInput
}

type ObservedDeviceInput struct {
	Name       string
	Attributes map[string]DeviceAttributeValue
}

type ObservedResourceClaimInput struct {
	Namespace        string
	Name             string
	DeviceClassNames []string
	Allocations      []ObservedAllocationInput
	ReservedFor      []ObservedConsumerReferenceInput
}

type ObservedAllocationInput struct {
	Request string
	Driver  string
	Pool    string
	Device  string
}

type ObservedConsumerReferenceInput struct {
	APIGroup string
	Resource string
	Name     string
	UID      string
}

type ObservedPodInput struct {
	Namespace      string
	Name           string
	UID            string
	NodeName       string
	ResourceClaims []string
}

type ObservedDeviceClass struct {
	name      string
	exists    bool
	selectors []string
}

type ObservedResourceSlice struct {
	name               string
	exists             bool
	driver             string
	poolName           string
	poolGeneration     int64
	resourceSliceCount int64
	nodeName           string
	devices            []ObservedDevice
}

type ObservedDevice struct {
	name       string
	attributes map[string]DeviceAttributeValue
}

type ObservedResourceClaim struct {
	namespace        string
	name             string
	deviceClassNames []string
	allocations      []ObservedAllocation
	reservedFor      []ObservedConsumerReference
}

type ObservedAllocation struct {
	request string
	driver  string
	pool    string
	device  string
}

type ObservedConsumerReference struct {
	apiGroup string
	resource string
	name     string
	uid      string
}

type ObservedPod struct {
	namespace      string
	name           string
	uid            string
	nodeName       string
	resourceClaims []string
}

func NewObservedGraph(
	inputs []ObservedNodeInput,
	draInputs ...DRAObservedInput,
) (ObservedGraph, error) {
	if len(draInputs) > 1 {
		return ObservedGraph{}, fmt.Errorf(
			"observed graph accepts at most one stable DRA observation",
		)
	}
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
	var draInput DRAObservedInput
	if len(draInputs) == 1 {
		draInput = draInputs[0]
	}
	classNames := make(map[string]struct{}, len(draInput.DeviceClasses))
	deviceClasses := make(
		[]ObservedDeviceClass,
		0,
		len(draInput.DeviceClasses),
	)
	for _, input := range draInput.DeviceClasses {
		if input.Name == "" {
			return ObservedGraph{}, fmt.Errorf(
				"observed DeviceClass requires an exact name",
			)
		}
		if _, duplicate := classNames[input.Name]; duplicate {
			return ObservedGraph{}, fmt.Errorf(
				"duplicate observed DeviceClass %q",
				input.Name,
			)
		}
		classNames[input.Name] = struct{}{}
		deviceClasses = append(deviceClasses, ObservedDeviceClass{
			name:      input.Name,
			exists:    input.Exists,
			selectors: append([]string(nil), input.Selectors...),
		})
	}
	resourceSliceNames := make(map[string]struct{}, len(draInput.ResourceSlices))
	resourceSlices := make(
		[]ObservedResourceSlice,
		0,
		len(draInput.ResourceSlices),
	)
	for _, input := range draInput.ResourceSlices {
		if input.Name == "" {
			return ObservedGraph{}, fmt.Errorf(
				"observed ResourceSlice requires an exact name",
			)
		}
		if _, duplicate := resourceSliceNames[input.Name]; duplicate {
			return ObservedGraph{}, fmt.Errorf(
				"duplicate observed ResourceSlice %q",
				input.Name,
			)
		}
		resourceSliceNames[input.Name] = struct{}{}
		devices := make([]ObservedDevice, 0, len(input.Devices))
		deviceNames := make(map[string]struct{}, len(input.Devices))
		for _, device := range input.Devices {
			if device.Name == "" {
				return ObservedGraph{}, fmt.Errorf(
					"observed ResourceSlice %q has an unnamed device",
					input.Name,
				)
			}
			if _, duplicate := deviceNames[device.Name]; duplicate {
				return ObservedGraph{}, fmt.Errorf(
					"observed ResourceSlice %q has duplicate device %q",
					input.Name,
					device.Name,
				)
			}
			deviceNames[device.Name] = struct{}{}
			devices = append(devices, ObservedDevice{
				name:       device.Name,
				attributes: cloneDeviceAttributes(device.Attributes),
			})
		}
		slices.SortFunc(devices, func(left, right ObservedDevice) int {
			return strings.Compare(left.name, right.name)
		})
		resourceSlices = append(resourceSlices, ObservedResourceSlice{
			name:               input.Name,
			exists:             input.Exists,
			driver:             input.Driver,
			poolName:           input.PoolName,
			poolGeneration:     input.PoolGeneration,
			resourceSliceCount: input.ResourceSliceCount,
			nodeName:           input.NodeName,
			devices:            devices,
		})
	}
	slices.SortFunc(deviceClasses, func(left, right ObservedDeviceClass) int {
		return strings.Compare(left.name, right.name)
	})
	slices.SortFunc(resourceSlices, func(left, right ObservedResourceSlice) int {
		return strings.Compare(left.name, right.name)
	})
	resourceClaims, err := observedResourceClaims(draInput.ResourceClaims)
	if err != nil {
		return ObservedGraph{}, err
	}
	pods, err := observedPods(draInput.Pods)
	if err != nil {
		return ObservedGraph{}, err
	}
	return ObservedGraph{
		nodes:          nodes,
		deviceClasses:  deviceClasses,
		resourceSlices: resourceSlices,
		resourceClaims: resourceClaims,
		pods:           pods,
	}, nil
}

func (graph ObservedGraph) Nodes() []ObservedNode {
	nodes := make([]ObservedNode, 0, len(graph.nodes))
	for _, node := range graph.nodes {
		nodes = append(nodes, cloneObservedNode(node))
	}
	return nodes
}

func (graph ObservedGraph) DeviceClasses() []ObservedDeviceClass {
	result := make([]ObservedDeviceClass, 0, len(graph.deviceClasses))
	for _, value := range graph.deviceClasses {
		value.selectors = append([]string(nil), value.selectors...)
		result = append(result, value)
	}
	return result
}

func (graph ObservedGraph) ResourceSlices() []ObservedResourceSlice {
	result := make([]ObservedResourceSlice, 0, len(graph.resourceSlices))
	for _, value := range graph.resourceSlices {
		value.devices = cloneObservedDevices(value.devices)
		result = append(result, value)
	}
	return result
}

func (graph ObservedGraph) ResourceClaims() []ObservedResourceClaim {
	result := make([]ObservedResourceClaim, 0, len(graph.resourceClaims))
	for _, value := range graph.resourceClaims {
		result = append(result, cloneObservedResourceClaim(value))
	}
	return result
}

func (graph ObservedGraph) Pods() []ObservedPod {
	result := make([]ObservedPod, 0, len(graph.pods))
	for _, value := range graph.pods {
		value.resourceClaims = append([]string(nil), value.resourceClaims...)
		result = append(result, value)
	}
	return result
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

func (value ObservedDeviceClass) Name() string {
	return value.name
}

func (value ObservedDeviceClass) Exists() bool {
	return value.exists
}

func (value ObservedDeviceClass) Selectors() []string {
	return append([]string(nil), value.selectors...)
}

func (value ObservedResourceSlice) Name() string {
	return value.name
}

func (value ObservedResourceSlice) Exists() bool {
	return value.exists
}

func (value ObservedResourceSlice) Driver() string {
	return value.driver
}

func (value ObservedResourceSlice) PoolName() string {
	return value.poolName
}

func (value ObservedResourceSlice) PoolGeneration() int64 {
	return value.poolGeneration
}

func (value ObservedResourceSlice) ResourceSliceCount() int64 {
	return value.resourceSliceCount
}

func (value ObservedResourceSlice) NodeName() string {
	return value.nodeName
}

func (value ObservedResourceSlice) Devices() []ObservedDevice {
	return cloneObservedDevices(value.devices)
}

func (value ObservedDevice) Name() string {
	return value.name
}

func (value ObservedDevice) Attributes() map[string]DeviceAttributeValue {
	return cloneDeviceAttributes(value.attributes)
}

func (value ObservedResourceClaim) Namespace() string {
	return value.namespace
}

func (value ObservedResourceClaim) Name() string {
	return value.name
}

func (value ObservedResourceClaim) DeviceClassNames() []string {
	return append([]string(nil), value.deviceClassNames...)
}

func (value ObservedResourceClaim) Allocations() []ObservedAllocation {
	return append([]ObservedAllocation(nil), value.allocations...)
}

func (value ObservedResourceClaim) ReservedFor() []ObservedConsumerReference {
	return append([]ObservedConsumerReference(nil), value.reservedFor...)
}

func (value ObservedAllocation) Request() string {
	return value.request
}

func (value ObservedAllocation) Driver() string {
	return value.driver
}

func (value ObservedAllocation) Pool() string {
	return value.pool
}

func (value ObservedAllocation) Device() string {
	return value.device
}

func (value ObservedConsumerReference) APIGroup() string {
	return value.apiGroup
}

func (value ObservedConsumerReference) Resource() string {
	return value.resource
}

func (value ObservedConsumerReference) Name() string {
	return value.name
}

func (value ObservedConsumerReference) UID() string {
	return value.uid
}

func (value ObservedPod) Namespace() string {
	return value.namespace
}

func (value ObservedPod) Name() string {
	return value.name
}

func (value ObservedPod) UID() string {
	return value.uid
}

func (value ObservedPod) NodeName() string {
	return value.nodeName
}

func (value ObservedPod) ResourceClaims() []string {
	return append([]string(nil), value.resourceClaims...)
}

// SurfaceState follows the product's four-state fidelity vocabulary.
type SurfaceState string

const (
	SurfaceAchieved    SurfaceState = "achieved"
	SurfaceExcluded    SurfaceState = "excluded"
	SurfaceUnavailable SurfaceState = "unavailable"
	SurfaceOutOfScope  SurfaceState = "out-of-scope"
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

func observedResourceClaims(
	inputs []ObservedResourceClaimInput,
) ([]ObservedResourceClaim, error) {
	if len(inputs) > cluster.MaximumObservedClaims {
		return nil, fmt.Errorf("observed ResourceClaim limit exceeded")
	}
	result := make([]ObservedResourceClaim, 0, len(inputs))
	names := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		if input.Namespace == "" || input.Name == "" {
			return nil, fmt.Errorf(
				"observed ResourceClaim requires an exact namespace and name",
			)
		}
		key := input.Namespace + "\x00" + input.Name
		if _, duplicate := names[key]; duplicate {
			return nil, fmt.Errorf(
				"duplicate observed ResourceClaim %s/%s",
				input.Namespace,
				input.Name,
			)
		}
		names[key] = struct{}{}
		classes := append([]string(nil), input.DeviceClassNames...)
		slices.Sort(classes)
		for index, name := range classes {
			if name == "" {
				return nil, fmt.Errorf(
					"observed ResourceClaim %s/%s has an empty DeviceClass",
					input.Namespace,
					input.Name,
				)
			}
			if index != 0 && classes[index-1] == name {
				return nil, fmt.Errorf(
					"observed ResourceClaim %s/%s repeats DeviceClass %q",
					input.Namespace,
					input.Name,
					name,
				)
			}
		}
		allocations := make([]ObservedAllocation, 0, len(input.Allocations))
		for _, allocation := range input.Allocations {
			if allocation.Request == "" || allocation.Driver == "" ||
				allocation.Pool == "" || allocation.Device == "" {
				return nil, fmt.Errorf(
					"observed ResourceClaim %s/%s has an incomplete allocation",
					input.Namespace,
					input.Name,
				)
			}
			allocations = append(allocations, ObservedAllocation{
				request: allocation.Request,
				driver:  allocation.Driver,
				pool:    allocation.Pool,
				device:  allocation.Device,
			})
		}
		slices.SortFunc(
			allocations,
			func(left, right ObservedAllocation) int {
				return strings.Compare(
					left.request+"\x00"+left.driver+"\x00"+
						left.pool+"\x00"+left.device,
					right.request+"\x00"+right.driver+"\x00"+
						right.pool+"\x00"+right.device,
				)
			},
		)
		reservations := make(
			[]ObservedConsumerReference,
			0,
			len(input.ReservedFor),
		)
		for _, reservation := range input.ReservedFor {
			if reservation.Resource == "" || reservation.Name == "" ||
				reservation.UID == "" {
				return nil, fmt.Errorf(
					"observed ResourceClaim %s/%s has an incomplete reservation",
					input.Namespace,
					input.Name,
				)
			}
			reservations = append(reservations, ObservedConsumerReference{
				apiGroup: reservation.APIGroup,
				resource: reservation.Resource,
				name:     reservation.Name,
				uid:      reservation.UID,
			})
		}
		slices.SortFunc(
			reservations,
			func(left, right ObservedConsumerReference) int {
				return strings.Compare(
					left.apiGroup+"\x00"+left.resource+"\x00"+
						left.name+"\x00"+left.uid,
					right.apiGroup+"\x00"+right.resource+"\x00"+
						right.name+"\x00"+right.uid,
				)
			},
		)
		result = append(result, ObservedResourceClaim{
			namespace:        input.Namespace,
			name:             input.Name,
			deviceClassNames: classes,
			allocations:      allocations,
			reservedFor:      reservations,
		})
	}
	slices.SortFunc(result, func(left, right ObservedResourceClaim) int {
		return strings.Compare(
			left.namespace+"\x00"+left.name,
			right.namespace+"\x00"+right.name,
		)
	})
	return result, nil
}

func observedPods(inputs []ObservedPodInput) ([]ObservedPod, error) {
	if len(inputs) > cluster.MaximumObservedPods {
		return nil, fmt.Errorf("observed Pod limit exceeded")
	}
	result := make([]ObservedPod, 0, len(inputs))
	names := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		if input.Namespace == "" || input.Name == "" || input.UID == "" {
			return nil, fmt.Errorf(
				"observed Pod requires an exact namespace, name, and UID",
			)
		}
		key := input.Namespace + "\x00" + input.Name
		if _, duplicate := names[key]; duplicate {
			return nil, fmt.Errorf(
				"duplicate observed Pod %s/%s",
				input.Namespace,
				input.Name,
			)
		}
		names[key] = struct{}{}
		claims := append([]string(nil), input.ResourceClaims...)
		slices.Sort(claims)
		for index, claim := range claims {
			if claim == "" {
				return nil, fmt.Errorf(
					"observed Pod %s/%s has an empty ResourceClaim reference",
					input.Namespace,
					input.Name,
				)
			}
			if index != 0 && claims[index-1] == claim {
				return nil, fmt.Errorf(
					"observed Pod %s/%s repeats ResourceClaim %q",
					input.Namespace,
					input.Name,
					claim,
				)
			}
		}
		result = append(result, ObservedPod{
			namespace:      input.Namespace,
			name:           input.Name,
			uid:            input.UID,
			nodeName:       input.NodeName,
			resourceClaims: claims,
		})
	}
	slices.SortFunc(result, func(left, right ObservedPod) int {
		return strings.Compare(
			left.namespace+"\x00"+left.name,
			right.namespace+"\x00"+right.name,
		)
	})
	return result, nil
}

func cloneObservedResourceClaim(
	value ObservedResourceClaim,
) ObservedResourceClaim {
	value.deviceClassNames = append([]string(nil), value.deviceClassNames...)
	value.allocations = append([]ObservedAllocation(nil), value.allocations...)
	value.reservedFor = append(
		[]ObservedConsumerReference(nil),
		value.reservedFor...,
	)
	return value
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

func cloneDevices(values []DeviceFragment) []DeviceFragment {
	result := make([]DeviceFragment, 0, len(values))
	for _, value := range values {
		value.attributes = cloneDeviceAttributes(value.attributes)
		result = append(result, value)
	}
	return result
}

func cloneObservedDevices(values []ObservedDevice) []ObservedDevice {
	result := make([]ObservedDevice, 0, len(values))
	for _, value := range values {
		value.attributes = cloneDeviceAttributes(value.attributes)
		result = append(result, value)
	}
	return result
}

func cloneDeviceAttributes(
	values map[string]DeviceAttributeValue,
) map[string]DeviceAttributeValue {
	if values == nil {
		return make(map[string]DeviceAttributeValue)
	}
	cloned := make(map[string]DeviceAttributeValue, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
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
