package projection_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/LinkMaq/kube-accelerator-sim/internal/catalog"
	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
	"github.com/LinkMaq/kube-accelerator-sim/internal/projection"
	"github.com/LinkMaq/kube-accelerator-sim/internal/projection/dra"
	"github.com/LinkMaq/kube-accelerator-sim/internal/projection/extended"
	"github.com/LinkMaq/kube-accelerator-sim/internal/scenario"
)

func TestMaintainedProjectionContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		adapter      projection.ResourceProjection
		graph        projection.DesiredGraph
		capabilities cluster.TargetCapabilities
		complete     func(
			t *testing.T,
			fragment projection.ProjectionFragment,
		) projection.ObservedGraph
	}{
		{
			name:         "extended-resource",
			adapter:      extended.New(),
			graph:        contractGraph(t, "scheduling", "device-plugin", "gpu"),
			capabilities: schedulingContractCapabilities(),
			complete:     completeExtendedObservation,
		},
		{
			name:         "stable-dra-v1",
			adapter:      dra.New(),
			graph:        contractGraph(t, "dra-control-plane", "dra", "device"),
			capabilities: draContractCapabilities(),
			complete:     completeDRAObservation,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			support := test.adapter.Support(test.capabilities, test.graph)
			if !support.Supported() || len(support.Issues()) != 0 {
				t.Fatalf("supported contract rejected: %#v", support.Issues())
			}
			if test.adapter.Support(
				cluster.TargetCapabilities{},
				test.graph,
			).Supported() {
				t.Fatal("capability-free target unexpectedly supported")
			}

			first, err := test.adapter.Render(test.graph, test.capabilities)
			if err != nil {
				t.Fatal(err)
			}
			second, err := test.adapter.Render(test.graph, test.capabilities)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(
				projectionContractSignature(first),
				projectionContractSignature(second),
			) {
				t.Fatal("retry changed deterministic projection identity")
			}
			if !slices.Contains(first.ObjectKinds(), "Node") ||
				slices.Contains(first.ObjectKinds(), "Pod") ||
				slices.Contains(first.ObjectKinds(), "ResourceClaim") {
				t.Fatalf(
					"projection escaped owned object boundary: %v",
					first.ObjectKinds(),
				)
			}

			empty, err := projection.NewObservedGraph(nil)
			if err != nil {
				t.Fatal(err)
			}
			if test.adapter.Assess(empty, first).FidelitySatisfied() {
				t.Fatal("unobserved projection satisfied fidelity")
			}
			complete := test.complete(t, first)
			report := test.adapter.Assess(complete, first)
			if !report.FidelitySatisfied() {
				t.Fatalf(
					"exact complete observation failed contract: %#v",
					report.Assessments(),
				)
			}
		})
	}
}

type contractSignature struct {
	kinds   []string
	nodes   []contractNode
	classes []contractClass
	slices  []contractSlice
}

type contractNode struct {
	name        string
	labels      map[string]string
	capacity    map[string]uint64
	allocatable map[string]uint64
}

type contractClass struct {
	name      string
	selectors []string
}

type contractSlice struct {
	name        string
	driver      string
	pool        string
	generation  int64
	count       int64
	node        string
	deviceNames []string
	deviceAttrs []map[string]string
}

func projectionContractSignature(
	fragment projection.ProjectionFragment,
) contractSignature {
	result := contractSignature{kinds: fragment.ObjectKinds()}
	for _, node := range fragment.Nodes() {
		result.nodes = append(result.nodes, contractNode{
			name:        node.Name(),
			labels:      node.IdentityLabels(),
			capacity:    node.Capacity(),
			allocatable: node.Allocatable(),
		})
	}
	for _, class := range fragment.DeviceClasses() {
		result.classes = append(result.classes, contractClass{
			name:      class.Name(),
			selectors: class.Selectors(),
		})
	}
	for _, resourceSlice := range fragment.ResourceSlices() {
		value := contractSlice{
			name:       resourceSlice.Name(),
			driver:     resourceSlice.Driver(),
			pool:       resourceSlice.PoolName(),
			generation: resourceSlice.PoolGeneration(),
			count:      resourceSlice.ResourceSliceCount(),
			node:       resourceSlice.NodeName(),
		}
		for _, device := range resourceSlice.Devices() {
			value.deviceNames = append(value.deviceNames, device.Name())
			attributes := make(map[string]string)
			for key, attribute := range device.Attributes() {
				switch attribute.Kind() {
				case projection.DeviceAttributeBool:
					if attribute.Bool() {
						attributes[key] = "true"
					} else {
						attributes[key] = "false"
					}
				case projection.DeviceAttributeString:
					attributes[key] = attribute.String()
				}
			}
			value.deviceAttrs = append(value.deviceAttrs, attributes)
		}
		result.slices = append(result.slices, value)
	}
	return result
}

func completeExtendedObservation(
	t *testing.T,
	fragment projection.ProjectionFragment,
) projection.ObservedGraph {
	t.Helper()
	inputs := make([]projection.ObservedNodeInput, 0, len(fragment.Nodes()))
	for _, node := range fragment.Nodes() {
		inputs = append(inputs, projection.ObservedNodeInput{
			Name:          node.Name(),
			Exists:        true,
			Labels:        node.IdentityLabels(),
			Capacity:      node.Capacity(),
			Allocatable:   node.Allocatable(),
			Ready:         true,
			LeaseObserved: true,
			Unschedulable: false,
		})
	}
	observed, err := projection.NewObservedGraph(inputs)
	if err != nil {
		t.Fatal(err)
	}
	return observed
}

func completeDRAObservation(
	t *testing.T,
	fragment projection.ProjectionFragment,
) projection.ObservedGraph {
	t.Helper()
	nodes := make([]projection.ObservedNodeInput, 0, len(fragment.Nodes()))
	for _, node := range fragment.Nodes() {
		nodes = append(nodes, projection.ObservedNodeInput{
			Name:          node.Name(),
			Exists:        true,
			Ready:         true,
			LeaseObserved: true,
			Unschedulable: false,
		})
	}
	classes := make(
		[]projection.ObservedDeviceClassInput,
		0,
		len(fragment.DeviceClasses()),
	)
	for _, class := range fragment.DeviceClasses() {
		classes = append(classes, projection.ObservedDeviceClassInput{
			Name: class.Name(), Exists: true, Selectors: class.Selectors(),
		})
	}
	resourceSlices := make(
		[]projection.ObservedResourceSliceInput,
		0,
		len(fragment.ResourceSlices()),
	)
	for _, value := range fragment.ResourceSlices() {
		devices := make(
			[]projection.ObservedDeviceInput,
			0,
			len(value.Devices()),
		)
		for _, device := range value.Devices() {
			devices = append(devices, projection.ObservedDeviceInput{
				Name: device.Name(), Attributes: device.Attributes(),
			})
		}
		resourceSlices = append(
			resourceSlices,
			projection.ObservedResourceSliceInput{
				Name:               value.Name(),
				Exists:             true,
				Driver:             value.Driver(),
				PoolName:           value.PoolName(),
				PoolGeneration:     value.PoolGeneration(),
				ResourceSliceCount: value.ResourceSliceCount(),
				NodeName:           value.NodeName(),
				Devices:            devices,
			},
		)
	}
	observed, err := projection.NewObservedGraph(
		nodes,
		projection.DRAObservedInput{
			DeviceClasses:  classes,
			ResourceSlices: resourceSlices,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return observed
}

func contractGraph(
	t *testing.T,
	fidelity,
	contract,
	resourceAlias string,
) projection.DesiredGraph {
	t.Helper()
	snapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	input, err := scenario.Shortcut(scenario.ShortcutInput{
		Name:                "projection-contract",
		ProfileID:           "nvidia",
		ModelID:             "nvidia-h100",
		ContractID:          contract,
		ResourceAlias:       resourceAlias,
		Fidelity:            fidelity,
		Nodes:               1,
		AcceleratorsPerNode: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, receipt, err := scenario.Compile(input, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	uid, err := domain.ParseInstanceUID(
		"8c97163e-d6ac-4e53-86eb-8453072ccaca",
	)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := domain.NewGeneration(7)
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

func schedulingContractCapabilities() cluster.TargetCapabilities {
	return cluster.TargetCapabilities{
		ServerVersion:   "v1.30.14",
		KubernetesMinor: 30,
		Resources: []cluster.ResourceCapability{
			{GroupVersion: "v1", Resource: "nodes"},
			{GroupVersion: "coordination.k8s.io/v1", Resource: "leases"},
		},
	}
}

func draContractCapabilities() cluster.TargetCapabilities {
	verbs := []string{"get", "list", "watch", "create", "patch", "delete"}
	return cluster.TargetCapabilities{
		ServerVersion:   "v1.34.10",
		KubernetesMinor: 34,
		Resources: []cluster.ResourceCapability{
			{GroupVersion: "v1", Resource: "nodes", Verbs: verbs},
			{
				GroupVersion: "coordination.k8s.io/v1",
				Resource:     "leases",
				Verbs:        verbs,
			},
			{
				GroupVersion: "resource.k8s.io/v1",
				Resource:     "deviceclasses",
				Verbs:        verbs,
			},
			{
				GroupVersion: "resource.k8s.io/v1",
				Resource:     "resourceslices",
				Verbs:        verbs,
			},
			{
				GroupVersion: "resource.k8s.io/v1",
				Resource:     "resourceclaims",
				Namespaced:   true,
				Verbs:        []string{"get", "list", "watch"},
			},
			{
				GroupVersion: "v1",
				Resource:     "pods",
				Namespaced:   true,
				Verbs:        []string{"get", "list", "watch"},
			},
		},
	}
}
