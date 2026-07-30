// Package v1alpha1 contains Kubernetes transport types for the product-owned
// Scenario Control Plane. These types are translated at adapter edges and are
// never reused as domain values.
//
// +kubebuilder:object:generate=true
//
//go:generate go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.21.0 object paths=.
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const GroupName = "simulation.kasim.io"

// GroupVersion identifies the initial product transport.
var GroupVersion = schema.GroupVersion{Group: GroupName, Version: "v1alpha1"}

// SchemeBuilder registers all product transport objects.
var SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

// AddToScheme adds product transport objects to one runtime Scheme.
var AddToScheme = SchemeBuilder.AddToScheme

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(
		GroupVersion,
		&ScenarioInstance{},
		&ScenarioInstanceList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
