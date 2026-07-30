package dra_test

import (
	"slices"
	"testing"

	"github.com/LinkMaq/kube-accelerator-sim/internal/catalog"
	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
	"github.com/LinkMaq/kube-accelerator-sim/internal/projection"
	"github.com/LinkMaq/kube-accelerator-sim/internal/projection/dra"
	"github.com/LinkMaq/kube-accelerator-sim/internal/scenario"
)

const fixtureInstanceUID = "6cb2dd6f-c608-4e79-aaf6-e3fa1287f73c"

func TestSupportRequiresThePortableStableDRAV1Contract(t *testing.T) {
	t.Parallel()

	graph := buildGraph(t, 1, 1)
	tests := []struct {
		name         string
		capabilities cluster.TargetCapabilities
		wantCode     string
	}{
		{
			name:         "supported",
			capabilities: draCapabilities(34),
		},
		{
			name:         "minor below stable floor",
			capabilities: draCapabilities(33),
			wantCode:     "KubernetesVersionUnsupported",
		},
		{
			name: "beta endpoints never substitute for v1",
			capabilities: cluster.TargetCapabilities{
				ServerVersion:   "v1.34.0",
				KubernetesMinor: 34,
				Resources: []cluster.ResourceCapability{{
					GroupVersion: "resource.k8s.io/v1beta2",
					Resource:     "deviceclasses",
					Verbs:        []string{"get", "list", "watch", "create", "patch", "delete"},
				}},
			},
			wantCode: "StableDRAAPIUnavailable",
		},
		{
			name: "missing claim watch",
			capabilities: mutateCapability(
				draCapabilities(34),
				"resource.k8s.io/v1",
				"resourceclaims",
				func(capability *cluster.ResourceCapability) {
					capability.Verbs = []string{"get", "list"}
				},
			),
			wantCode: "StableDRACapabilityUnavailable",
		},
		{
			name: "wrong class scope",
			capabilities: mutateCapability(
				draCapabilities(34),
				"resource.k8s.io/v1",
				"deviceclasses",
				func(capability *cluster.ResourceCapability) {
					capability.Namespaced = true
				},
			),
			wantCode: "StableDRACapabilityUnavailable",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			report := dra.New().Support(test.capabilities, graph)
			if test.wantCode == "" {
				if !report.Supported() || len(report.Issues()) != 0 {
					t.Fatalf("Support() = %#v, want supported", report.Issues())
				}
				return
			}
			if report.Supported() || len(report.Issues()) == 0 ||
				report.Issues()[0].Code != test.wantCode {
				t.Fatalf("Support() issues = %#v, want first code %q", report.Issues(), test.wantCode)
			}
		})
	}
}

func TestSupportRejectsAContractFromAnotherFidelityMode(t *testing.T) {
	t.Parallel()

	report := dra.New().Support(draCapabilities(34), buildSchedulingGraph(t))
	if report.Supported() || len(report.Issues()) == 0 ||
		report.Issues()[0].Code != "UnsupportedFidelity" {
		t.Fatalf("Support() issues = %#v", report.Issues())
	}
}

func TestRenderProducesDeterministicStableDRAInventory(t *testing.T) {
	t.Parallel()

	adapter := dra.New()
	first, err := adapter.Render(buildGraph(t, 129, 128), draCapabilities(34))
	if err != nil {
		t.Fatal(err)
	}
	second, err := adapter.Render(buildGraph(t, 129, 128), draCapabilities(34))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(first.ObjectKinds(), []string{"DeviceClass", "Node", "ResourceSlice"}) {
		t.Fatalf("ObjectKinds() = %v", first.ObjectKinds())
	}
	if len(first.DeviceClasses()) != 1 {
		t.Fatalf("DeviceClasses() = %#v", first.DeviceClasses())
	}
	class := first.DeviceClasses()[0]
	if class.Driver() != "gpu.nvidia.com" ||
		class.Group() != "nodes" ||
		class.Pool() != "accelerators" ||
		len(class.Selectors()) != 1 {
		t.Fatalf("unexpected DeviceClass fragment: %#v", class)
	}
	for _, required := range []string{
		`device.driver == "gpu.nvidia.com"`,
		`device.attributes["simulation.kasim.io"].simulated == true`,
		`device.attributes["simulation.kasim.io"].allocatable == true`,
	} {
		if !contains(class.Selectors()[0], required) {
			t.Errorf("selector %q does not contain %q", class.Selectors()[0], required)
		}
	}

	slicesFirst := first.ResourceSlices()
	slicesSecond := second.ResourceSlices()
	if len(slicesFirst) != 2 ||
		len(slicesFirst[0].Devices()) != 128 ||
		len(slicesFirst[1].Devices()) != 1 {
		t.Fatalf("unexpected deterministic sharding: %#v", sliceSizes(slicesFirst))
	}
	if slicesFirst[0].ResourceSliceCount() != 2 ||
		slicesFirst[1].ResourceSliceCount() != 2 ||
		slicesFirst[0].PoolGeneration() != 1 {
		t.Fatalf("unexpected pool completeness contract: %#v", slicesFirst)
	}
	if slicesFirst[0].Name() != slicesSecond[0].Name() ||
		slicesFirst[0].Devices()[0].Name() != slicesSecond[0].Devices()[0].Name() {
		t.Fatal("render retry changed stable slice or device identity")
	}
	if slicesFirst[0].Devices()[0].Name() == slicesFirst[0].Devices()[1].Name() {
		t.Fatal("two device indices produced the same identity")
	}
	firstDevice := slicesFirst[0].Devices()[0]
	if value, ok := firstDevice.Attribute("simulation.kasim.io/simulated"); !ok ||
		value.Kind() != projection.DeviceAttributeBool || !value.Bool() {
		t.Fatalf("simulated attribute = %#v, %v", value, ok)
	}
	if value, ok := firstDevice.Attribute("simulation.kasim.io/allocatable"); !ok ||
		!value.Bool() {
		t.Fatalf("allocatable attribute = %#v, %v", value, ok)
	}
	lastDevice := slicesFirst[1].Devices()[0]
	if value, ok := lastDevice.Attribute("simulation.kasim.io/allocatable"); !ok ||
		value.Bool() {
		t.Fatalf("unhealthy device allocatable attribute = %#v, %v", value, ok)
	}
	if _, invented := firstDevice.Attribute("gpu.nvidia.com/productName"); invented {
		t.Fatal("renderer invented a source-backed vendor attribute value")
	}
}

func TestHealthOnlyRevisionPreservesDeviceIdentity(t *testing.T) {
	t.Parallel()

	adapter := dra.New()
	healthy, err := adapter.Render(buildGraph(t, 4, 4), draCapabilities(34))
	if err != nil {
		t.Fatal(err)
	}
	degraded, err := adapter.Render(buildGraph(t, 4, 2), draCapabilities(34))
	if err != nil {
		t.Fatal(err)
	}
	healthyDevices := healthy.ResourceSlices()[0].Devices()
	degradedDevices := degraded.ResourceSlices()[0].Devices()
	for index := range healthyDevices {
		if healthyDevices[index].Name() != degradedDevices[index].Name() {
			t.Fatalf("health revision changed device %d identity", index)
		}
		wantAllocatable := index < 2
		value, found := degradedDevices[index].Attribute(
			"simulation.kasim.io/allocatable",
		)
		if !found || value.Bool() != wantAllocatable {
			t.Fatalf("device %d allocatable = %#v, %v", index, value, found)
		}
	}
}

func TestZeroDevicePoolStillHasACompleteObservableGeneration(t *testing.T) {
	t.Parallel()

	fragment, err := dra.New().Render(buildGraph(t, 0, 0), draCapabilities(34))
	if err != nil {
		t.Fatal(err)
	}
	resourceSlices := fragment.ResourceSlices()
	if len(resourceSlices) != 1 ||
		resourceSlices[0].ResourceSliceCount() != 1 ||
		len(resourceSlices[0].Devices()) != 0 {
		t.Fatalf("zero-device pool = %#v", resourceSlices)
	}
}

func TestAssessRequiresExactClassAndCompleteHighestPoolGeneration(t *testing.T) {
	t.Parallel()

	adapter := dra.New()
	fragment, err := adapter.Render(buildGraph(t, 129, 129), draCapabilities(34))
	if err != nil {
		t.Fatal(err)
	}
	observed, err := projection.NewObservedGraph(
		observedNodes(fragment),
		projection.DRAObservedInput{
			DeviceClasses:  observedClasses(fragment),
			ResourceSlices: observedSlices(fragment),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	report := adapter.Assess(observed, fragment)
	if !report.FidelitySatisfied() {
		t.Fatalf("complete DRA inventory was not satisfied: %#v", report.Assessments())
	}
	assertSurface(t, report, "device-class", projection.SurfaceAchieved)
	assertSurface(t, report, "resource-slice-inventory", projection.SurfaceAchieved)
	assertSurface(t, report, "node-prepare-resources", projection.SurfaceExcluded)
	assertSurface(t, report, "cdi", projection.SurfaceOutOfScope)
	assertSurface(t, report, "device-access", projection.SurfaceOutOfScope)
	assertSurface(t, report, "accelerator-compute", projection.SurfaceOutOfScope)

	incompleteInputs := observedSlices(fragment)
	incompleteInputs = incompleteInputs[:1]
	incomplete, err := projection.NewObservedGraph(
		observedNodes(fragment),
		projection.DRAObservedInput{
			DeviceClasses:  observedClasses(fragment),
			ResourceSlices: incompleteInputs,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	incompleteReport := adapter.Assess(incomplete, fragment)
	if incompleteReport.FidelitySatisfied() {
		t.Fatal("incomplete highest ResourceSlice generation satisfied fidelity")
	}
	assertSurface(
		t,
		incompleteReport,
		"resource-slice-inventory",
		projection.SurfaceUnavailable,
	)
}

func TestAssessRequiresExactSchedulerEvidenceForASelectedClaimAndPod(t *testing.T) {
	t.Parallel()

	adapter := dra.New()
	fragment, err := adapter.Render(buildGraph(t, 1, 1), draCapabilities(34))
	if err != nil {
		t.Fatal(err)
	}
	deviceClass := fragment.DeviceClasses()[0]
	resourceSlice := fragment.ResourceSlices()[0]
	device := resourceSlice.Devices()[0]
	base := projection.DRAObservedInput{
		DeviceClasses:  observedClasses(fragment),
		ResourceSlices: observedSlices(fragment),
	}
	withoutWorkload, err := projection.NewObservedGraph(
		observedNodes(fragment),
		base,
	)
	if err != nil {
		t.Fatal(err)
	}
	withoutWorkloadReport := adapter.Assess(withoutWorkload, fragment)
	for _, surface := range []string{
		"resource-claim-allocation",
		"resource-claim-reservation",
		"pod-scheduling",
	} {
		assertSurface(
			t,
			withoutWorkloadReport,
			surface,
			projection.SurfaceOutOfScope,
		)
	}

	selected := base
	selected.ResourceClaims = []projection.ObservedResourceClaimInput{{
		Namespace:        "default",
		Name:             "probe-claim",
		DeviceClassNames: []string{deviceClass.Name()},
		Allocations: []projection.ObservedAllocationInput{{
			Request: "accelerator",
			Driver:  resourceSlice.Driver(),
			Pool:    resourceSlice.PoolName(),
			Device:  device.Name(),
		}},
		ReservedFor: []projection.ObservedConsumerReferenceInput{{
			Resource: "pods",
			Name:     "probe-pod",
			UID:      "probe-pod-uid",
		}},
	}}
	selected.Pods = []projection.ObservedPodInput{{
		Namespace:      "default",
		Name:           "probe-pod",
		UID:            "probe-pod-uid",
		NodeName:       resourceSlice.NodeName(),
		ResourceClaims: []string{"probe-claim"},
	}}
	observed, err := projection.NewObservedGraph(
		observedNodes(fragment),
		selected,
	)
	if err != nil {
		t.Fatal(err)
	}
	report := adapter.Assess(observed, fragment)
	if !report.FidelitySatisfied() {
		t.Fatalf("exact scheduler evidence did not satisfy fidelity: %#v", report.Assessments())
	}
	for _, surface := range []string{
		"resource-claim-allocation",
		"resource-claim-reservation",
		"pod-scheduling",
	} {
		assertSurface(t, report, surface, projection.SurfaceAchieved)
	}

	wrongDevice := selected
	wrongDevice.ResourceClaims = append(
		[]projection.ObservedResourceClaimInput(nil),
		selected.ResourceClaims...,
	)
	wrongDevice.ResourceClaims[0].Allocations = append(
		[]projection.ObservedAllocationInput(nil),
		selected.ResourceClaims[0].Allocations...,
	)
	wrongDevice.ResourceClaims[0].Allocations[0].Device = "vendor-hardware-id"
	invalid, err := projection.NewObservedGraph(
		observedNodes(fragment),
		wrongDevice,
	)
	if err != nil {
		t.Fatal(err)
	}
	invalidReport := adapter.Assess(invalid, fragment)
	if invalidReport.FidelitySatisfied() {
		t.Fatal("allocation outside exact owned inventory satisfied fidelity")
	}
	assertSurface(
		t,
		invalidReport,
		"resource-claim-allocation",
		projection.SurfaceUnavailable,
	)
}

func buildGraph(t *testing.T, count, healthy int64) projection.DesiredGraph {
	t.Helper()
	return buildGraphFor(t, "dra-control-plane", "dra", "device", count, healthy)
}

func buildSchedulingGraph(t *testing.T) projection.DesiredGraph {
	t.Helper()
	return buildGraphFor(t, "scheduling", "device-plugin", "gpu", 1, 1)
}

func buildGraphFor(
	t *testing.T,
	fidelity,
	contract,
	resourceAlias string,
	count,
	healthy int64,
) projection.DesiredGraph {
	t.Helper()
	snapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	healthyCopy := healthy
	input, err := scenario.Shortcut(scenario.ShortcutInput{
		Name:                "training-lab",
		ProfileID:           "nvidia",
		ModelID:             "nvidia-h100",
		ContractID:          contract,
		ResourceAlias:       resourceAlias,
		Fidelity:            fidelity,
		Nodes:               1,
		AcceleratorsPerNode: count,
		HealthyPerNode:      &healthyCopy,
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, receipt, err := scenario.Compile(input, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	uid, err := domain.ParseInstanceUID(fixtureInstanceUID)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := domain.NewGeneration(1)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := projection.Build(projection.BuildInput{
		InstanceName: compiled.Scenario().Name(),
		InstanceUID:  uid,
		Generation:   generation,
		Scenario:     compiled.Scenario(),
		Resolutions:  receipt.Resolutions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return graph
}

func draCapabilities(minor int) cluster.TargetCapabilities {
	verbs := []string{"get", "list", "watch", "create", "patch", "delete"}
	return cluster.TargetCapabilities{
		ServerVersion:   "v1.34.0",
		KubernetesMinor: minor,
		Resources: []cluster.ResourceCapability{
			{GroupVersion: "v1", Resource: "nodes", Verbs: verbs},
			{GroupVersion: "v1", Resource: "pods", Namespaced: true, Verbs: []string{"get", "list", "watch"}},
			{GroupVersion: "coordination.k8s.io/v1", Resource: "leases", Namespaced: true, Verbs: verbs},
			{GroupVersion: "resource.k8s.io/v1", Resource: "deviceclasses", Verbs: verbs},
			{GroupVersion: "resource.k8s.io/v1", Resource: "resourceslices", Verbs: verbs},
			{
				GroupVersion: "resource.k8s.io/v1",
				Resource:     "resourceclaims",
				Namespaced:   true,
				Verbs:        []string{"get", "list", "watch"},
			},
		},
	}
}

func mutateCapability(
	capabilities cluster.TargetCapabilities,
	groupVersion,
	resource string,
	mutate func(*cluster.ResourceCapability),
) cluster.TargetCapabilities {
	result := capabilities
	result.Resources = append([]cluster.ResourceCapability(nil), capabilities.Resources...)
	for index := range result.Resources {
		result.Resources[index].Verbs = append([]string(nil), result.Resources[index].Verbs...)
		if result.Resources[index].GroupVersion == groupVersion &&
			result.Resources[index].Resource == resource {
			mutate(&result.Resources[index])
		}
	}
	return result
}

func observedNodes(fragment projection.ProjectionFragment) []projection.ObservedNodeInput {
	result := make([]projection.ObservedNodeInput, 0, len(fragment.Nodes()))
	for _, node := range fragment.Nodes() {
		result = append(result, projection.ObservedNodeInput{
			Name:          node.Name(),
			Exists:        true,
			Labels:        node.IdentityLabels(),
			Capacity:      node.Capacity(),
			Allocatable:   node.Allocatable(),
			Ready:         true,
			LeaseObserved: true,
		})
	}
	return result
}

func observedClasses(
	fragment projection.ProjectionFragment,
) []projection.ObservedDeviceClassInput {
	result := make(
		[]projection.ObservedDeviceClassInput,
		0,
		len(fragment.DeviceClasses()),
	)
	for _, class := range fragment.DeviceClasses() {
		result = append(result, projection.ObservedDeviceClassInput{
			Name:      class.Name(),
			Exists:    true,
			Selectors: class.Selectors(),
		})
	}
	return result
}

func observedSlices(
	fragment projection.ProjectionFragment,
) []projection.ObservedResourceSliceInput {
	result := make(
		[]projection.ObservedResourceSliceInput,
		0,
		len(fragment.ResourceSlices()),
	)
	for _, resourceSlice := range fragment.ResourceSlices() {
		devices := make([]projection.ObservedDeviceInput, 0, len(resourceSlice.Devices()))
		for _, device := range resourceSlice.Devices() {
			devices = append(devices, projection.ObservedDeviceInput{
				Name:       device.Name(),
				Attributes: device.Attributes(),
			})
		}
		result = append(result, projection.ObservedResourceSliceInput{
			Name:               resourceSlice.Name(),
			Exists:             true,
			Driver:             resourceSlice.Driver(),
			PoolName:           resourceSlice.PoolName(),
			PoolGeneration:     resourceSlice.PoolGeneration(),
			ResourceSliceCount: resourceSlice.ResourceSliceCount(),
			NodeName:           resourceSlice.NodeName(),
			Devices:            devices,
		})
	}
	return result
}

func assertSurface(
	t *testing.T,
	report projection.FidelityReport,
	surface string,
	state projection.SurfaceState,
) {
	t.Helper()
	if !slices.ContainsFunc(report.Assessments(), func(value projection.SurfaceAssessment) bool {
		return value.Surface == surface && value.State == state
	}) {
		t.Fatalf("surface %q=%q not found in %#v", surface, state, report.Assessments())
	}
}

func sliceSizes(values []projection.ResourceSliceFragment) []int {
	result := make([]int, 0, len(values))
	for _, value := range values {
		result = append(result, len(value.Devices()))
	}
	return result
}

func contains(value, substring string) bool {
	for index := 0; index+len(substring) <= len(value); index++ {
		if value[index:index+len(substring)] == substring {
			return true
		}
	}
	return false
}
