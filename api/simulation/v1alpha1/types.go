package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// ProfileReceipt pins one evidence-classified profile in a logical revision.
type ProfileReceipt struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
	Digest   string `json:"digest"`
	Class    string `json:"class"`
}

// ScenarioRevision is one append-only logical receipt. The current canonical
// Scenario is stored once on the enclosing spec to avoid unbounded document
// duplication.
type ScenarioRevision struct {
	Generation int64            `json:"generation"`
	Digest     string           `json:"digest"`
	Profiles   []ProfileReceipt `json:"profiles"`
}

// ScenarioInstanceSpec is the atomic desired-state and concurrency root.
type ScenarioInstanceSpec struct {
	TargetFingerprint string             `json:"targetFingerprint"`
	CreationIdentity  string             `json:"creationIdentity"`
	Fidelity          string             `json:"fidelity"`
	DesiredGeneration int64              `json:"desiredGeneration"`
	CanonicalScenario string             `json:"canonicalScenario"`
	Revisions         []ScenarioRevision `json:"revisions"`
}

// PoolStatus is one bounded requested/observed pool aggregate.
type PoolStatus struct {
	Group            string `json:"group"`
	Pool             string `json:"pool"`
	RequestedTotal   int64  `json:"requestedTotal"`
	RequestedHealthy int64  `json:"requestedHealthy"`
	ObservedTotal    int64  `json:"observedTotal"`
	ObservedHealthy  int64  `json:"observedHealthy"`
}

// DiagnosticStatus is one bounded stable automation diagnostic.
type DiagnosticStatus struct {
	Code             string `json:"code"`
	Message          string `json:"message"`
	Retryable        bool   `json:"retryable"`
	RevisionAccepted bool   `json:"revisionAccepted"`
	ExitCategory     int32  `json:"exitCategory"`
}

// InventoryEntry counts one exact allowlisted owned object kind.
type InventoryEntry struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Count      int32  `json:"count"`
}

// FidelitySurfaceStatus records one explicit simulation truth boundary.
type FidelitySurfaceStatus struct {
	Surface string `json:"surface"`
	State   string `json:"state"`
}

// ScenarioInstanceStatus is deliberately bounded independently of scale.
type ScenarioInstanceStatus struct {
	ObservedGeneration int64                   `json:"observedGeneration,omitempty"`
	RevisionDigest     string                  `json:"revisionDigest,omitempty"`
	Phase              string                  `json:"phase,omitempty"`
	Pools              []PoolStatus            `json:"pools,omitempty"`
	Inventory          []InventoryEntry        `json:"inventory,omitempty"`
	Fidelity           []FidelitySurfaceStatus `json:"fidelity,omitempty"`
	Diagnostics        []DiagnosticStatus      `json:"diagnostics,omitempty"`
	Conditions         []metav1.Condition      `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=ksi
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Fidelity",type=string,JSONPath=`.spec.fidelity`
// +kubebuilder:printcolumn:name="Desired",type=integer,JSONPath=`.spec.desiredGeneration`
// +kubebuilder:printcolumn:name="Observed",type=integer,JSONPath=`.status.observedGeneration`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`

// ScenarioInstance is the cluster-scoped durable identity and revision root.
type ScenarioInstance struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ScenarioInstanceSpec   `json:"spec"`
	Status ScenarioInstanceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ScenarioInstanceList is the list transport.
type ScenarioInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ScenarioInstance `json:"items"`
}
