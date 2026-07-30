package v1alpha1_test

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"

	simulationv1alpha1 "github.com/LinkMaq/kube-accelerator-sim/api/simulation/v1alpha1"
)

func TestScenarioInstanceRegistersAndDeepCopiesMutableTransportData(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := simulationv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	object, err := scheme.New(simulationv1alpha1.GroupVersion.WithKind("ScenarioInstance"))
	if err != nil {
		t.Fatal(err)
	}
	instance := object.(*simulationv1alpha1.ScenarioInstance)
	instance.Spec.Revisions = []simulationv1alpha1.ScenarioRevision{{
		Generation: 1,
		Digest:     "sha256:test",
		Profiles: []simulationv1alpha1.ProfileReceipt{{
			ID: "nvidia",
		}},
	}}
	instance.Spec.CanonicalScenario = `{"metadata":{"name":"demo"}}`
	instance.Status.Inventory = []simulationv1alpha1.InventoryEntry{{
		Kind:  "Node",
		Count: 2,
	}}

	cloned := instance.DeepCopy()
	cloned.Spec.Revisions[0].Profiles[0].ID = "forged"
	cloned.Status.Inventory[0].Count = 99
	if instance.Spec.Revisions[0].Profiles[0].ID != "nvidia" ||
		instance.Status.Inventory[0].Count != 2 {
		t.Fatal("DeepCopy shared mutable transport storage")
	}
}

func TestRevisionTransportCannotCarryArbitraryKubernetesObjectsOrPatches(t *testing.T) {
	t.Parallel()

	forbidden := []reflect.Type{
		reflect.TypeFor[runtime.RawExtension](),
		reflect.TypeFor[runtime.Object](),
	}
	assertNoForbiddenField(
		t,
		reflect.TypeFor[simulationv1alpha1.ScenarioInstanceSpec](),
		forbidden,
		map[reflect.Type]bool{},
	)
}

func assertNoForbiddenField(
	t *testing.T,
	value reflect.Type,
	forbidden []reflect.Type,
	visited map[reflect.Type]bool,
) {
	t.Helper()
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Slice ||
		value.Kind() == reflect.Array {
		value = value.Elem()
	}
	if visited[value] {
		return
	}
	visited[value] = true
	for _, rejected := range forbidden {
		if value == rejected ||
			(rejected.Kind() == reflect.Interface && value.Implements(rejected)) {
			t.Fatalf("transport contains forbidden Kubernetes object field %s", value)
		}
	}
	if value.PkgPath() == "k8s.io/apimachinery/pkg/apis/meta/v1" {
		return
	}
	if value.Kind() != reflect.Struct {
		return
	}
	for index := 0; index < value.NumField(); index++ {
		assertNoForbiddenField(t, value.Field(index).Type, forbidden, visited)
	}
}
