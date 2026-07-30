// Package dra renders and assesses the portable stable
// resource.k8s.io/v1 control-plane projection.
package dra

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
	"github.com/LinkMaq/kube-accelerator-sim/internal/projection"
)

const (
	minimumKubernetesMinor = 34
	maximumKubernetesMinor = 36
	maximumDevicesPerSlice = 128

	simulatorDomain      = "simulation.kasim.io"
	simulatedAttribute   = simulatorDomain + "/simulated"
	allocatableAttribute = simulatorDomain + "/allocatable"
	scenarioAttribute    = simulatorDomain + "/scenario"
	groupAttribute       = simulatorDomain + "/group"
	poolAttribute        = simulatorDomain + "/pool"
	profileAttribute     = simulatorDomain + "/profile"
	modelAttribute       = simulatorDomain + "/model"
)

// Adapter is the maintained stable DRA control-plane projection.
type Adapter struct{}

func New() Adapter {
	return Adapter{}
}

func (Adapter) Support(
	capabilities cluster.TargetCapabilities,
	graph projection.DesiredGraph,
) projection.SupportReport {
	issues := make([]projection.SupportIssue, 0, 8)
	if graph.Fidelity().String() != "dra-control-plane" {
		issues = append(issues, projection.SupportIssue{
			Code:    "UnsupportedFidelity",
			Message: "stable DRA requires dra-control-plane Fidelity Mode",
		})
		return projection.NewSupportReport(issues)
	}
	if capabilities.KubernetesMinor < minimumKubernetesMinor {
		issues = append(issues, projection.SupportIssue{
			Code: "KubernetesVersionUnsupported",
			Message: fmt.Sprintf(
				"stable DRA requires Kubernetes 1.%d or newer",
				minimumKubernetesMinor,
			),
		})
		return projection.NewSupportReport(issues)
	}
	if capabilities.KubernetesMinor > maximumKubernetesMinor {
		issues = append(issues, projection.SupportIssue{
			Code: "KubernetesVersionUntested",
			Message: fmt.Sprintf(
				"Kubernetes 1.%d is above the validated DRA ceiling 1.%d",
				capabilities.KubernetesMinor,
				maximumKubernetesMinor,
			),
		})
		return projection.NewSupportReport(issues)
	}

	required := []resourceRequirement{
		{
			groupVersion: "resource.k8s.io/v1",
			resource:     "deviceclasses",
			verbs:        []string{"get", "list", "watch", "create", "patch", "delete"},
		},
		{
			groupVersion: "resource.k8s.io/v1",
			resource:     "resourceslices",
			verbs:        []string{"get", "list", "watch", "create", "patch", "delete"},
		},
		{
			groupVersion: "resource.k8s.io/v1",
			resource:     "resourceclaims",
			namespaced:   true,
			verbs:        []string{"get", "list", "watch"},
		},
		{
			groupVersion: "v1",
			resource:     "pods",
			namespaced:   true,
			verbs:        []string{"get", "list", "watch"},
		},
	}
	for _, requirement := range required {
		capability, found := findResource(
			capabilities,
			requirement.groupVersion,
			requirement.resource,
		)
		if !found {
			code := "StableDRACapabilityUnavailable"
			if requirement.groupVersion == "resource.k8s.io/v1" {
				code = "StableDRAAPIUnavailable"
			}
			issues = append(issues, projection.SupportIssue{
				Code: code,
				Message: fmt.Sprintf(
					"required Kubernetes resource %s/%s is unavailable",
					requirement.groupVersion,
					requirement.resource,
				),
			})
			continue
		}
		if capability.Namespaced != requirement.namespaced ||
			!containsEvery(capability.Verbs, requirement.verbs) {
			issues = append(issues, projection.SupportIssue{
				Code: "StableDRACapabilityUnavailable",
				Message: fmt.Sprintf(
					"Kubernetes resource %s/%s lacks the required scope or verbs",
					requirement.groupVersion,
					requirement.resource,
				),
			})
		}
	}
	if _, found := findResource(capabilities, "v1", "nodes"); !found {
		issues = append(issues, projection.SupportIssue{
			Code:    "NodeAPIUnavailable",
			Message: "core/v1 Nodes are unavailable",
		})
	}
	if _, found := findResource(
		capabilities,
		"coordination.k8s.io/v1",
		"leases",
	); !found {
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
			"stable DRA projection is unsupported: %s",
			support.Issues()[0].Message,
		)
	}
	if graph.Generation().Value() > math.MaxInt64 {
		return projection.ProjectionFragment{}, fmt.Errorf(
			"Scenario generation exceeds stable DRA pool generation range",
		)
	}

	nodeInputs := make(
		[]projection.NodeFragmentInput,
		0,
		len(graph.Nodes()),
	)
	classInputsByIdentity := make(map[string]projection.DeviceClassFragmentInput)
	sliceInputs := make([]projection.ResourceSliceFragmentInput, 0)
	for _, node := range graph.Nodes() {
		nodeInputs = append(nodeInputs, projection.NodeFragmentInput{
			Name:          node.Name(),
			RequiresReady: node.RequiresReady(),
			RequiresLease: node.RequiresLease(),
		})
		groupName, err := domain.ParseName(node.Group())
		if err != nil {
			return projection.ProjectionFragment{}, err
		}
		for _, pool := range node.Pools() {
			poolName, err := domain.ParseName(pool.Name())
			if err != nil {
				return projection.ProjectionFragment{}, err
			}
			classIdentity := strings.Join(
				[]string{node.Group(), pool.Name(), pool.ResourceName()},
				"\x00",
			)
			className := deterministicName(
				"kasim-class",
				"kasim.dra.device-class.v1",
				graph.InstanceUID().String(),
				node.Group(),
				pool.Name(),
				pool.ResourceName(),
			)
			if existing, found := classInputsByIdentity[classIdentity]; found {
				if existing.Name != className {
					return projection.ProjectionFragment{}, fmt.Errorf(
						"DRA class identity collision for %s/%s",
						node.Group(),
						pool.Name(),
					)
				}
			} else {
				classInputsByIdentity[classIdentity] = projection.DeviceClassFragmentInput{
					Name:   className,
					Driver: pool.ResourceName(),
					Group:  node.Group(),
					Pool:   pool.Name(),
					Selectors: []string{selectorExpression(
						graph,
						node,
						pool,
					)},
				}
			}

			resourcePoolName := deterministicName(
				"kasim-pool",
				"kasim.dra.resource-pool.v1",
				graph.InstanceUID().String(),
				node.Group(),
				strconv.FormatUint(node.Replica(), 10),
				pool.Name(),
			)
			shardCount := int((pool.Capacity() + maximumDevicesPerSlice - 1) /
				maximumDevicesPerSlice)
			if shardCount == 0 {
				shardCount = 1
			}
			for shard := 0; shard < shardCount; shard++ {
				start := uint64(shard * maximumDevicesPerSlice)
				end := start + maximumDevicesPerSlice
				if end > pool.Capacity() {
					end = pool.Capacity()
				}
				devices := make(
					[]projection.DeviceFragmentInput,
					0,
					end-start,
				)
				for index := start; index < end; index++ {
					deviceName, err := domain.SimulatedDeviceID(
						graph.InstanceUID(),
						groupName,
						node.Replica(),
						poolName,
						index,
					)
					if err != nil {
						return projection.ProjectionFragment{}, err
					}
					attributes, err := deviceAttributes(
						graph,
						node,
						pool,
						index < pool.Allocatable(),
					)
					if err != nil {
						return projection.ProjectionFragment{}, err
					}
					devices = append(devices, projection.DeviceFragmentInput{
						Name:       deviceName,
						Attributes: attributes,
					})
				}
				sliceInputs = append(
					sliceInputs,
					projection.ResourceSliceFragmentInput{
						Name: deterministicName(
							"kasim-slice",
							"kasim.dra.resource-slice.v1",
							resourcePoolName,
							strconv.FormatUint(graph.Generation().Value(), 10),
							strconv.Itoa(shard),
						),
						Driver:             pool.ResourceName(),
						Group:              node.Group(),
						Pool:               pool.Name(),
						PoolName:           resourcePoolName,
						PoolGeneration:     int64(graph.Generation().Value()),
						ResourceSliceCount: int64(shardCount),
						NodeName:           node.Name(),
						Devices:            devices,
					},
				)
			}
		}
	}
	classInputs := make(
		[]projection.DeviceClassFragmentInput,
		0,
		len(classInputsByIdentity),
	)
	for _, input := range classInputsByIdentity {
		classInputs = append(classInputs, input)
	}
	return projection.NewDRAFragment(nodeInputs, classInputs, sliceInputs)
}

func (Adapter) Assess(
	observedGraph projection.ObservedGraph,
	fragment projection.ProjectionFragment,
) projection.FidelityReport {
	assessments := make([]projection.SurfaceAssessment, 0)
	allAchieved := true

	observedClasses := make(map[string]projection.ObservedDeviceClass)
	for _, value := range observedGraph.DeviceClasses() {
		observedClasses[value.Name()] = value
	}
	desiredClassNames := make(map[string]struct{})
	classByPool := make(map[string]bool)
	for _, desired := range fragment.DeviceClasses() {
		desiredClassNames[desired.Name()] = struct{}{}
		actual, found := observedClasses[desired.Name()]
		achieved := found && actual.Exists() &&
			slices.Equal(actual.Selectors(), desired.Selectors())
		classByPool[desired.Group()+"\x00"+desired.Pool()] = achieved
		assessments = append(assessments, projection.SurfaceAssessment{
			Node:    desired.Name(),
			Surface: "device-class",
			State:   state(achieved),
		})
		if !achieved {
			allAchieved = false
		}
	}
	for name := range observedClasses {
		if _, desired := desiredClassNames[name]; desired {
			continue
		}
		allAchieved = false
		assessments = append(assessments, projection.SurfaceAssessment{
			Node:    name,
			Surface: "stale-device-class",
			State:   projection.SurfaceUnavailable,
		})
	}

	desiredSlicesByPool := make(map[string][]projection.ResourceSliceFragment)
	desiredDeviceNodes := make(map[string]string)
	for _, desired := range fragment.ResourceSlices() {
		identity := desired.Driver() + "\x00" + desired.PoolName()
		desiredSlicesByPool[identity] = append(desiredSlicesByPool[identity], desired)
		for _, device := range desired.Devices() {
			desiredDeviceNodes[draTuple(
				desired.Driver(),
				desired.PoolName(),
				device.Name(),
			)] = desired.NodeName()
		}
	}
	observedSlicesByPool := make(map[string][]projection.ObservedResourceSlice)
	for _, actual := range observedGraph.ResourceSlices() {
		identity := actual.Driver() + "\x00" + actual.PoolName()
		observedSlicesByPool[identity] = append(observedSlicesByPool[identity], actual)
	}
	poolByNode := make(map[string]bool)
	for identity, desiredSlices := range desiredSlicesByPool {
		actualSlices := observedSlicesByPool[identity]
		achieved := exactCompletePool(desiredSlices, actualSlices)
		for _, desired := range desiredSlices {
			poolByNode[desired.NodeName()] =
				poolByNode[desired.NodeName()] || achieved
		}
		first := desiredSlices[0]
		assessments = append(assessments, projection.SurfaceAssessment{
			Node:    first.NodeName(),
			Surface: "resource-slice-inventory",
			State:   state(achieved),
		})
		if !achieved {
			allAchieved = false
		}
	}
	for identity, values := range observedSlicesByPool {
		if _, desired := desiredSlicesByPool[identity]; desired {
			continue
		}
		allAchieved = false
		for _, value := range values {
			assessments = append(assessments, projection.SurfaceAssessment{
				Node:    value.Name(),
				Surface: "stale-resource-slice",
				State:   projection.SurfaceUnavailable,
			})
		}
	}

	observedNodes := make(map[string]projection.ObservedNode)
	for _, node := range observedGraph.Nodes() {
		observedNodes[node.Name()] = node
	}
	openNodes := make([]string, 0)
	mustClose := make([]string, 0)
	allOpen := true
	for _, desired := range fragment.Nodes() {
		actual, found := observedNodes[desired.Name()]
		nodeAchieved := found && actual.Exists()
		readyAchieved := !desired.RequiresReady() ||
			(nodeAchieved && actual.Ready())
		leaseAchieved := !desired.RequiresLease() ||
			(nodeAchieved && actual.LeaseObserved())
		inventoryAchieved := poolByNode[desired.Name()]
		complete := nodeAchieved && readyAchieved && leaseAchieved &&
			inventoryAchieved
		for surface, achieved := range map[string]bool{
			"node":  nodeAchieved,
			"ready": readyAchieved,
			"lease": leaseAchieved,
		} {
			assessments = append(assessments, projection.SurfaceAssessment{
				Node:    desired.Name(),
				Surface: surface,
				State:   state(achieved),
			})
		}
		if !complete {
			allAchieved = false
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
	}

	workloadAssessments, workloadSelected, workloadAchieved :=
		assessSelectedWorkloads(
			observedGraph,
			desiredClassNames,
			desiredDeviceNodes,
		)
	assessments = append(assessments, workloadAssessments...)
	if workloadSelected && !workloadAchieved {
		allAchieved = false
	}

	for _, excluded := range []projection.SurfaceAssessment{
		{Surface: "node-prepare-resources", State: projection.SurfaceExcluded},
		{Surface: "node-unprepare-resources", State: projection.SurfaceExcluded},
		{Surface: "cdi", State: projection.SurfaceOutOfScope},
		{Surface: "device-access", State: projection.SurfaceOutOfScope},
		{Surface: "node-local-health", State: projection.SurfaceOutOfScope},
		{Surface: "accelerator-compute", State: projection.SurfaceOutOfScope},
	} {
		assessments = append(assessments, excluded)
	}
	slices.Sort(openNodes)
	slices.Sort(mustClose)
	if !allAchieved {
		openNodes = nil
	}
	return projection.NewFidelityReport(
		assessments,
		nil,
		openNodes,
		mustClose,
		allAchieved && allOpen,
	)
}

func assessSelectedWorkloads(
	observed projection.ObservedGraph,
	desiredClasses map[string]struct{},
	desiredDeviceNodes map[string]string,
) ([]projection.SurfaceAssessment, bool, bool) {
	podsByClaim := make(map[string][]projection.ObservedPod)
	for _, pod := range observed.Pods() {
		for _, claim := range pod.ResourceClaims() {
			key := pod.Namespace() + "\x00" + claim
			podsByClaim[key] = append(podsByClaim[key], pod)
		}
	}
	assessments := make([]projection.SurfaceAssessment, 0)
	selected := false
	allAchieved := true
	for _, claim := range observed.ResourceClaims() {
		pods := podsByClaim[claim.Namespace()+"\x00"+claim.Name()]
		if len(pods) == 0 {
			continue
		}
		selected = true
		allocationAchieved := len(claim.DeviceClassNames()) != 0 &&
			len(claim.Allocations()) != 0
		for _, className := range claim.DeviceClassNames() {
			if _, desired := desiredClasses[className]; !desired {
				allocationAchieved = false
			}
		}
		allocationNodes := make(map[string]struct{})
		for _, allocation := range claim.Allocations() {
			nodeName, desired := desiredDeviceNodes[draTuple(
				allocation.Driver(),
				allocation.Pool(),
				allocation.Device(),
			)]
			if !desired {
				allocationAchieved = false
				continue
			}
			allocationNodes[nodeName] = struct{}{}
		}
		claimIdentity := claim.Namespace() + "/" + claim.Name()
		assessments = append(assessments, projection.SurfaceAssessment{
			Node:    claimIdentity,
			Surface: "resource-claim-allocation",
			State:   state(allocationAchieved),
		})
		if !allocationAchieved {
			allAchieved = false
		}
		for _, pod := range pods {
			reservationAchieved := hasExactReservation(claim, pod)
			schedulingAchieved := allocationAchieved &&
				reservationAchieved &&
				pod.NodeName() != "" &&
				len(allocationNodes) == 1
			if schedulingAchieved {
				_, schedulingAchieved = allocationNodes[pod.NodeName()]
			}
			podIdentity := pod.Namespace() + "/" + pod.Name()
			assessments = append(
				assessments,
				projection.SurfaceAssessment{
					Node:    podIdentity,
					Surface: "resource-claim-reservation",
					State:   state(reservationAchieved),
				},
				projection.SurfaceAssessment{
					Node:    podIdentity,
					Surface: "pod-scheduling",
					State:   state(schedulingAchieved),
				},
			)
			if !reservationAchieved || !schedulingAchieved {
				allAchieved = false
			}
		}
	}
	if !selected {
		for _, surface := range []string{
			"resource-claim-allocation",
			"resource-claim-reservation",
			"pod-scheduling",
		} {
			assessments = append(assessments, projection.SurfaceAssessment{
				Surface: surface,
				State:   projection.SurfaceOutOfScope,
			})
		}
	}
	return assessments, selected, allAchieved
}

func hasExactReservation(
	claim projection.ObservedResourceClaim,
	pod projection.ObservedPod,
) bool {
	for _, reservation := range claim.ReservedFor() {
		if reservation.APIGroup() == "" &&
			reservation.Resource() == "pods" &&
			reservation.Name() == pod.Name() &&
			reservation.UID() == pod.UID() {
			return true
		}
	}
	return false
}

func draTuple(driver, pool, device string) string {
	return driver + "\x00" + pool + "\x00" + device
}

type resourceRequirement struct {
	groupVersion string
	resource     string
	namespaced   bool
	verbs        []string
}

func findResource(
	capabilities cluster.TargetCapabilities,
	groupVersion,
	resource string,
) (cluster.ResourceCapability, bool) {
	for _, capability := range capabilities.Resources {
		if capability.GroupVersion == groupVersion &&
			capability.Resource == resource {
			return capability, true
		}
	}
	return cluster.ResourceCapability{}, false
}

func containsEvery(actual, required []string) bool {
	for _, value := range required {
		if !slices.Contains(actual, value) {
			return false
		}
	}
	return true
}

func selectorExpression(
	graph projection.DesiredGraph,
	node projection.DesiredNode,
	pool projection.DesiredPool,
) string {
	stringAttribute := func(name, value string) string {
		return fmt.Sprintf(
			`device.attributes[%q].%s == %s`,
			simulatorDomain,
			name,
			strconv.Quote(value),
		)
	}
	return strings.Join([]string{
		`device.driver == ` + strconv.Quote(pool.ResourceName()),
		`device.attributes["simulation.kasim.io"].simulated == true`,
		`device.attributes["simulation.kasim.io"].allocatable == true`,
		stringAttribute("scenario", graph.InstanceName().String()),
		stringAttribute("group", node.Group()),
		stringAttribute("pool", pool.Name()),
		stringAttribute("profile", pool.ProfileID()),
		stringAttribute("model", pool.ModelID()),
	}, " && ")
}

func deviceAttributes(
	graph projection.DesiredGraph,
	node projection.DesiredNode,
	pool projection.DesiredPool,
	allocatable bool,
) (map[string]projection.DeviceAttributeValue, error) {
	values := map[string]string{
		scenarioAttribute: graph.InstanceName().String(),
		groupAttribute:    node.Group(),
		poolAttribute:     pool.Name(),
		profileAttribute:  pool.ProfileID(),
		modelAttribute:    pool.ModelID(),
	}
	result := map[string]projection.DeviceAttributeValue{
		simulatedAttribute:   projection.NewBoolDeviceAttribute(true),
		allocatableAttribute: projection.NewBoolDeviceAttribute(allocatable),
	}
	for key, value := range values {
		attribute, err := projection.NewStringDeviceAttribute(value)
		if err != nil {
			return nil, err
		}
		result[key] = attribute
	}
	return result, nil
}

func deterministicName(prefix, identityDomain string, values ...string) string {
	digester := sha256.New()
	_, _ = digester.Write([]byte(identityDomain))
	for _, value := range values {
		_, _ = digester.Write([]byte{0})
		_, _ = digester.Write([]byte(value))
	}
	return prefix + "-" + hex.EncodeToString(digester.Sum(nil))[:32]
}

func exactCompletePool(
	desired []projection.ResourceSliceFragment,
	actual []projection.ObservedResourceSlice,
) bool {
	if len(desired) == 0 || len(actual) != len(desired) {
		return false
	}
	expected := make(map[string]projection.ResourceSliceFragment, len(desired))
	for _, value := range desired {
		expected[value.Name()] = value
	}
	for _, value := range actual {
		wanted, found := expected[value.Name()]
		if !found || !value.Exists() ||
			value.Driver() != wanted.Driver() ||
			value.PoolName() != wanted.PoolName() ||
			value.PoolGeneration() != wanted.PoolGeneration() ||
			value.ResourceSliceCount() != wanted.ResourceSliceCount() ||
			value.NodeName() != wanted.NodeName() ||
			!equalDevices(value.Devices(), wanted.Devices()) {
			return false
		}
	}
	return int64(len(actual)) == desired[0].ResourceSliceCount()
}

func equalDevices(
	actual []projection.ObservedDevice,
	desired []projection.DeviceFragment,
) bool {
	if len(actual) != len(desired) {
		return false
	}
	for index := range actual {
		if actual[index].Name() != desired[index].Name() ||
			!equalAttributes(
				actual[index].Attributes(),
				desired[index].Attributes(),
			) {
			return false
		}
	}
	return true
}

func equalAttributes(
	actual,
	desired map[string]projection.DeviceAttributeValue,
) bool {
	if len(actual) != len(desired) {
		return false
	}
	for key, wanted := range desired {
		value, found := actual[key]
		if !found || value.Kind() != wanted.Kind() ||
			value.Bool() != wanted.Bool() ||
			value.String() != wanted.String() {
			return false
		}
	}
	return true
}

func state(achieved bool) projection.SurfaceState {
	if achieved {
		return projection.SurfaceAchieved
	}
	return projection.SurfaceUnavailable
}
