package v1alpha1_test

import (
	"os"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

func TestPublishedCRDIsStructuralBoundedAndHasStatusSubresource(t *testing.T) {
	t.Parallel()

	encoded, err := os.ReadFile(
		"../../../config/crd/bases/simulation.kasim.io_scenarioinstances.yaml",
	)
	if err != nil {
		t.Fatal(err)
	}
	var definition apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(encoded, &definition); err != nil {
		t.Fatal(err)
	}
	if definition.Spec.Group != "simulation.kasim.io" ||
		definition.Spec.Scope != apiextensionsv1.ClusterScoped ||
		definition.Spec.Names.Kind != "ScenarioInstance" {
		t.Fatalf("unexpected CRD identity: %#v", definition.Spec)
	}
	if len(definition.Spec.Versions) != 1 ||
		!definition.Spec.Versions[0].Served ||
		!definition.Spec.Versions[0].Storage ||
		definition.Spec.Versions[0].Subresources == nil ||
		definition.Spec.Versions[0].Subresources.Status == nil {
		t.Fatal("v1alpha1 storage version or status subresource is missing")
	}
	root := definition.Spec.Versions[0].Schema.OpenAPIV3Schema
	spec := root.Properties["spec"]
	status := root.Properties["status"]
	if len(spec.XValidations) < 5 {
		t.Fatalf("immutable/append-only validations missing: %#v", spec.XValidations)
	}
	if status.Properties["diagnostics"].MaxItems == nil ||
		*status.Properties["diagnostics"].MaxItems != 32 ||
		status.Properties["conditions"].MaxItems == nil ||
		status.Properties["inventory"].MaxItems == nil {
		t.Fatal("status collections are not structurally bounded")
	}
	fidelity := status.Properties["fidelity"]
	if fidelity.MaxItems == nil || *fidelity.MaxItems != 32 ||
		fidelity.XListType == nil || *fidelity.XListType != "map" ||
		len(fidelity.XListMapKeys) != 1 ||
		fidelity.XListMapKeys[0] != "surface" {
		t.Fatal("fidelity surfaces are not a bounded map keyed by surface")
	}
	revisions := spec.Properties["revisions"]
	canonical := spec.Properties["canonicalScenario"]
	if canonical.MaxLength == nil || *canonical.MaxLength != 1<<20 {
		t.Fatal("canonical Scenario transport is not bounded to one MiB")
	}
	if revisions.MinItems == nil || *revisions.MinItems != 1 ||
		revisions.MaxItems == nil || *revisions.MaxItems != 4096 ||
		revisions.XListType == nil || *revisions.XListType != "map" {
		t.Fatal("accepted revision receipts are not bounded append-only map entries")
	}
	if root.XPreserveUnknownFields != nil && *root.XPreserveUnknownFields {
		t.Fatal("CRD preserves arbitrary unknown fields")
	}
}
