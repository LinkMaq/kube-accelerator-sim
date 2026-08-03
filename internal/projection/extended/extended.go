// Package extended renders and assesses scalar Kubernetes extended resources.
package extended

import (
	"fmt"
	"slices"

	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/projection"
)

// Adapter is the maintained scalar extended-resource projection.
type Adapter struct{}

func New() Adapter {
	return Adapter{}
}

func (Adapter) Support(
	capabilities cluster.TargetCapabilities,
	graph projection.DesiredGraph,
) projection.SupportReport {
	issues := make([]projection.SupportIssue, 0, 3)
	if graph.Fidelity().String() != "scheduling" {
		issues = append(issues, projection.SupportIssue{
			Code:    "UnsupportedFidelity",
			Message: "extended resources require scheduling Fidelity Mode",
		})
	}
	if !hasResource(capabilities, "v1", "nodes") {
		issues = append(issues, projection.SupportIssue{
			Code:    "NodeAPIUnavailable",
			Message: "core/v1 Nodes are unavailable",
		})
	}
	if !hasResource(capabilities, "coordination.k8s.io/v1", "leases") {
		issues = append(issues, projection.SupportIssue{
			Code:    "LeaseAPIUnavailable",
			Message: "coordination.k8s.io/v1 Leases are unavailable",
		})
	}
	return projection.NewSupportReport(issues)
}

func (adapter Adapter) Render(
	graph projection.DesiredGraph,
	capabilities cluster.TargetCapabilities,
) (projection.ProjectionFragment, error) {
	support := adapter.Support(capabilities, graph)
	if !support.Supported() {
		return projection.ProjectionFragment{}, fmt.Errorf(
			"extended-resource projection is unsupported: %s",
			support.Issues()[0].Message,
		)
	}

	inputs := make([]projection.NodeFragmentInput, 0, len(graph.Nodes()))
	for _, node := range graph.Nodes() {
		labels := make(map[string]string)
		capacity := make(
			map[string]uint64,
			len(node.Pools())+len(node.AuxiliaryPools()),
		)
		allocatable := make(
			map[string]uint64,
			len(node.Pools())+len(node.AuxiliaryPools()),
		)
		for _, pool := range node.Pools() {
			if _, collision := capacity[pool.ResourceName()]; collision {
				return projection.ProjectionFragment{}, fmt.Errorf(
					"Node %q has duplicate extended resource %q",
					node.Name(),
					pool.ResourceName(),
				)
			}
			capacity[pool.ResourceName()] = pool.Capacity()
			allocatable[pool.ResourceName()] = pool.Allocatable()
			for _, signal := range pool.IdentitySignals() {
				if signal.Kind != "node-label" || signal.Value == "" {
					continue
				}
				if _, collision := labels[signal.Key]; collision {
					return projection.ProjectionFragment{}, fmt.Errorf(
						"Node %q has duplicate source-backed identity label %q",
						node.Name(),
						signal.Key,
					)
				}
				labels[signal.Key] = signal.Value
			}
		}
		for _, pool := range node.AuxiliaryPools() {
			if _, collision := capacity[pool.ResourceName()]; collision {
				return projection.ProjectionFragment{}, fmt.Errorf(
					"Node %q has duplicate extended resource %q",
					node.Name(),
					pool.ResourceName(),
				)
			}
			capacity[pool.ResourceName()] = pool.Capacity()
			allocatable[pool.ResourceName()] = pool.Allocatable()
		}
		inputs = append(inputs, projection.NodeFragmentInput{
			Name:           node.Name(),
			IdentityLabels: labels,
			Capacity:       capacity,
			Allocatable:    allocatable,
			RequiresReady:  node.RequiresReady(),
			RequiresLease:  node.RequiresLease(),
		})
	}
	return projection.NewFragment(inputs)
}

func (Adapter) Assess(
	observedGraph projection.ObservedGraph,
	fragment projection.ProjectionFragment,
) projection.FidelityReport {
	observed := make(map[string]projection.ObservedNode)
	for _, node := range observedGraph.Nodes() {
		observed[node.Name()] = node
	}
	desiredNames := make(map[string]struct{}, len(fragment.Nodes()))
	assessments := make([]projection.SurfaceAssessment, 0, len(fragment.Nodes())*4)
	overcommitments := make([]projection.Overcommitment, 0)
	openNodes := make([]string, 0)
	mustClose := make([]string, 0)
	allAchieved := true
	allOpenable := len(fragment.Nodes()) > 0
	allOpen := true

	for _, desired := range fragment.Nodes() {
		desiredNames[desired.Name()] = struct{}{}
		actual, found := observed[desired.Name()]
		nodeAchieved := found && actual.Exists()
		resourceAchieved := nodeAchieved &&
			containsStrings(actual.Labels(), desired.IdentityLabels()) &&
			containsQuantities(actual.Capacity(), desired.Capacity()) &&
			containsQuantities(actual.Allocatable(), desired.Allocatable())
		readyAchieved := !desired.RequiresReady() ||
			(nodeAchieved && actual.Ready())
		leaseAchieved := !desired.RequiresLease() ||
			(nodeAchieved && actual.LeaseObserved())
		appendAssessment := func(surface string, achieved bool) {
			state := projection.SurfaceUnavailable
			if achieved {
				state = projection.SurfaceAchieved
			}
			assessments = append(assessments, projection.SurfaceAssessment{
				Node:    desired.Name(),
				Surface: surface,
				State:   state,
			})
		}
		appendAssessment("node", nodeAchieved)
		appendAssessment("extended-resources", resourceAchieved)
		appendAssessment("ready", readyAchieved)
		appendAssessment("lease", leaseAchieved)

		complete := nodeAchieved && resourceAchieved &&
			readyAchieved && leaseAchieved
		if !complete {
			allAchieved = false
			allOpenable = false
			if found && actual.Exists() && !actual.Unschedulable() {
				mustClose = append(mustClose, desired.Name())
			}
		}
		if complete && actual.Unschedulable() {
			openNodes = append(openNodes, desired.Name())
		}
		if !found || actual.Unschedulable() {
			allOpen = false
		}
		for resourceName, requested := range actual.Requested() {
			allocatable, projected := desired.Allocatable()[resourceName]
			if projected && requested > allocatable {
				overcommitments = append(overcommitments, projection.Overcommitment{
					Node:         desired.Name(),
					ResourceName: resourceName,
					Requested:    requested,
					Allocatable:  allocatable,
				})
			}
		}
	}

	for name := range observed {
		if _, desired := desiredNames[name]; desired {
			continue
		}
		allAchieved = false
		allOpenable = false
		assessments = append(assessments, projection.SurfaceAssessment{
			Node:    name,
			Surface: "stale-node",
			State:   projection.SurfaceUnavailable,
		})
	}
	slices.Sort(mustClose)
	slices.Sort(openNodes)
	slices.SortFunc(overcommitments, func(left, right projection.Overcommitment) int {
		if left.Node != right.Node {
			if left.Node < right.Node {
				return -1
			}
			return 1
		}
		if left.ResourceName < right.ResourceName {
			return -1
		}
		if left.ResourceName > right.ResourceName {
			return 1
		}
		return 0
	})
	if len(fragment.Nodes()) == 0 && len(observed) == 0 {
		allAchieved = true
		allOpen = true
	}
	if !allOpenable {
		openNodes = nil
	}
	return projection.NewFidelityReport(
		assessments,
		overcommitments,
		openNodes,
		mustClose,
		allAchieved && allOpen,
	)
}

func hasResource(
	capabilities cluster.TargetCapabilities,
	groupVersion,
	resourceName string,
) bool {
	for _, resource := range capabilities.Resources {
		if resource.GroupVersion == groupVersion &&
			resource.Resource == resourceName {
			return true
		}
	}
	return false
}

func containsStrings(actual, desired map[string]string) bool {
	for key, value := range desired {
		if actual[key] != value {
			return false
		}
	}
	return true
}

func containsQuantities(actual, desired map[string]uint64) bool {
	for key, value := range desired {
		if actualValue, found := actual[key]; !found || actualValue != value {
			return false
		}
	}
	return true
}
