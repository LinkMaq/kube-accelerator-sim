// Package kubernetes observes exactly owned Synthetic Nodes for the Simulated
// Vendor Telemetry Module. It performs read-only Kubernetes operations.
package kubernetes

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	simulationv1alpha1 "github.com/LinkMaq/kube-accelerator-sim/api/simulation/v1alpha1"
	"github.com/LinkMaq/kube-accelerator-sim/internal/catalog"
	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
	"github.com/LinkMaq/kube-accelerator-sim/internal/projection"
	"github.com/LinkMaq/kube-accelerator-sim/internal/scenario"
	"github.com/LinkMaq/kube-accelerator-sim/internal/telemetry"
)

const pageSize = 500

// Adapter joins Scenario Instances to exact owned Nodes and expands observed
// pool quantities into stable simulated device identities.
type Adapter struct {
	client  client.Client
	catalog catalog.Snapshot
}

func New(source client.Client, schedulingCatalog catalog.Snapshot) (*Adapter, error) {
	if source == nil {
		return nil, fmt.Errorf("telemetry Kubernetes Adapter requires a client")
	}
	if schedulingCatalog.Revision() == "" {
		return nil, fmt.Errorf("telemetry Kubernetes Adapter requires a scheduling catalog")
	}
	return &Adapter{client: source, catalog: schedulingCatalog}, nil
}

// Snapshot lists only product Scenario Instances and product-owned Nodes. It
// does not read Secrets, logs, environment variables, or host sensors.
func (adapter *Adapter) Snapshot(ctx context.Context) (telemetry.Observation, error) {
	instances, err := adapter.listInstances(ctx)
	if err != nil {
		return telemetry.Observation{}, fmt.Errorf("list Scenario Instances: %w", err)
	}
	nodes, err := adapter.listNodes(ctx)
	if err != nil {
		return telemetry.Observation{}, fmt.Errorf("list Synthetic Nodes: %w", err)
	}
	nodesByInstance := make(map[string]map[string]corev1.Node)
	for _, node := range nodes {
		uid := node.Labels[cluster.InstanceUIDLabel]
		if uid == "" || node.Labels[cluster.ManagedByLabel] != cluster.ManagedByValue {
			continue
		}
		if nodesByInstance[uid] == nil {
			nodesByInstance[uid] = make(map[string]corev1.Node)
		}
		nodesByInstance[uid][node.Name] = node
	}

	result := telemetry.Observation{}
	for index := range instances {
		instance := &instances[index]
		ownedNodes := nodesByInstance[string(instance.UID)]
		if len(ownedNodes) == 0 || instance.DeletionTimestamp != nil {
			continue
		}
		graph, err := adapter.graph(instance)
		if err != nil {
			return telemetry.Observation{}, fmt.Errorf("Scenario Instance %q: %w", instance.Name, err)
		}
		for _, desiredNode := range graph.Nodes() {
			observedNode, found := ownedNodes[desiredNode.Name()]
			if !found || !matchesDesiredGeneration(observedNode, instance.Spec.DesiredGeneration) {
				continue
			}
			node := telemetry.Node{
				InstanceName: instance.Name,
				InstanceUID:  string(instance.UID),
				Name:         desiredNode.Name(),
				Group:        desiredNode.Group(),
			}
			if len(result.Nodes) >= telemetry.MaximumNodes {
				return telemetry.Observation{}, fmt.Errorf(
					"owned Synthetic Nodes exceed maximum %d",
					telemetry.MaximumNodes,
				)
			}
			result.Nodes = append(result.Nodes, node)
			for _, pool := range desiredNode.Pools() {
				total := observedQuantity(observedNode.Status.Capacity, pool.ResourceName(), pool.Capacity())
				healthy := observedQuantity(observedNode.Status.Allocatable, pool.ResourceName(), pool.Allocatable())
				if err := appendDevices(&result, node, pool.Name(), pool.ProfileID(), pool.ModelID(), total, healthy); err != nil {
					return telemetry.Observation{}, err
				}
			}
			for _, pool := range desiredNode.AuxiliaryPools() {
				total := observedQuantity(observedNode.Status.Capacity, pool.ResourceName(), pool.Capacity())
				available := observedQuantity(observedNode.Status.Allocatable, pool.ResourceName(), pool.Allocatable())
				model := pool.Category()
				if model == "" {
					model = "auxiliary-device"
				}
				if err := appendDevices(&result, node, pool.Name(), pool.ProfileID(), model, total, available); err != nil {
					return telemetry.Observation{}, err
				}
			}
		}
	}
	slices.SortFunc(result.Nodes, func(left, right telemetry.Node) int {
		return compare(left.InstanceUID+"\x00"+left.Name, right.InstanceUID+"\x00"+right.Name)
	})
	slices.SortFunc(result.Devices, func(left, right telemetry.Device) int {
		leftKey := fmt.Sprintf("%s\x00%s\x00%s\x00%020d", left.InstanceUID, left.NodeName, left.Pool, left.Ordinal)
		rightKey := fmt.Sprintf("%s\x00%s\x00%s\x00%020d", right.InstanceUID, right.NodeName, right.Pool, right.Ordinal)
		return compare(leftKey, rightKey)
	})
	return result, nil
}

func (adapter *Adapter) graph(
	instance *simulationv1alpha1.ScenarioInstance,
) (projection.DesiredGraph, error) {
	name, err := domain.ParseName(instance.Name)
	if err != nil {
		return projection.DesiredGraph{}, err
	}
	uid, err := domain.ParseInstanceUID(string(instance.UID))
	if err != nil {
		return projection.DesiredGraph{}, err
	}
	generation, err := domain.NewGeneration(instance.Spec.DesiredGeneration)
	if err != nil || generation.Value() == 0 {
		return projection.DesiredGraph{}, fmt.Errorf("invalid desired generation %d", instance.Spec.DesiredGeneration)
	}
	input, err := scenario.Document([]byte(instance.Spec.CanonicalScenario))
	if err != nil {
		return projection.DesiredGraph{}, err
	}
	compiled, receipt, err := scenario.Compile(input, adapter.catalog)
	if err != nil {
		return projection.DesiredGraph{}, err
	}
	return projection.Build(projection.BuildInput{
		InstanceName:         name,
		InstanceUID:          uid,
		Generation:           generation,
		Scenario:             compiled.Scenario(),
		Resolutions:          receipt.Resolutions(),
		AuxiliaryResolutions: receipt.AuxiliaryResolutions(),
	})
}

func (adapter *Adapter) listInstances(
	ctx context.Context,
) ([]simulationv1alpha1.ScenarioInstance, error) {
	result := make([]simulationv1alpha1.ScenarioInstance, 0)
	continuation := ""
	for {
		var page simulationv1alpha1.ScenarioInstanceList
		if err := adapter.client.List(
			ctx,
			&page,
			client.Limit(pageSize),
			client.Continue(continuation),
		); err != nil {
			return nil, err
		}
		result = append(result, page.Items...)
		continuation = page.Continue
		if continuation == "" {
			return result, nil
		}
	}
}

func (adapter *Adapter) listNodes(ctx context.Context) ([]corev1.Node, error) {
	result := make([]corev1.Node, 0)
	continuation := ""
	for {
		var page corev1.NodeList
		if err := adapter.client.List(
			ctx,
			&page,
			client.MatchingLabels{cluster.ManagedByLabel: cluster.ManagedByValue},
			client.Limit(pageSize),
			client.Continue(continuation),
		); err != nil {
			return nil, err
		}
		result = append(result, page.Items...)
		continuation = page.Continue
		if continuation == "" {
			return result, nil
		}
	}
}

func matchesDesiredGeneration(node corev1.Node, generation int64) bool {
	return node.Labels[cluster.DesiredGenerationLabel] == strconv.FormatInt(generation, 10)
}

func observedQuantity(
	values corev1.ResourceList,
	name string,
	fallback uint64,
) uint64 {
	quantity, found := values[corev1.ResourceName(name)]
	if !found {
		return fallback
	}
	value := quantity.Value()
	if value < 0 {
		return 0
	}
	return uint64(value)
}

func appendDevices(
	observation *telemetry.Observation,
	node telemetry.Node,
	pool,
	profile,
	model string,
	total,
	healthy uint64,
) error {
	remaining := telemetry.MaximumDevices - len(observation.Devices)
	if total > uint64(remaining) {
		return fmt.Errorf(
			"owned simulated devices exceed maximum %d while expanding Node %q pool %q",
			telemetry.MaximumDevices,
			node.Name,
			pool,
		)
	}
	if healthy > total {
		healthy = total
	}
	for ordinal := uint64(0); ordinal < total; ordinal++ {
		observation.Devices = append(observation.Devices, telemetry.Device{
			InstanceName: node.InstanceName,
			InstanceUID:  node.InstanceUID,
			NodeName:     node.Name,
			NodeGroup:    node.Group,
			Pool:         pool,
			ProfileID:    profile,
			ModelID:      model,
			Ordinal:      ordinal,
			Healthy:      ordinal < healthy,
		})
	}
	return nil
}

func compare(left, right string) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}
