// Package inventory implements the read-only Cluster Simulation Inventory
// Module. Kubernetes collection and UI transport remain behind its small
// Open/Next/Close interface.
package inventory

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/LinkMaq/kube-accelerator-sim/internal/catalog"
	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
)

type Completeness string

const (
	CompletenessLoading         Completeness = "loading"
	CompletenessComplete        Completeness = "complete"
	CompletenessPartial         Completeness = "partial"
	CompletenessDiagnosticsOnly Completeness = "diagnostics-only"
)

type Freshness string

const (
	FreshnessLoading      Freshness = "loading"
	FreshnessFresh        Freshness = "fresh"
	FreshnessReconnecting Freshness = "reconnecting"
	FreshnessStale        Freshness = "stale"
	FreshnessResyncing    Freshness = "resyncing"
	FreshnessIncomplete   Freshness = "incomplete"
)

type Availability string

const (
	AvailabilityAvailable         Availability = "available"
	AvailabilityForbidden         Availability = "forbidden"
	AvailabilityUnsupported       Availability = "unsupported"
	AvailabilityUnsupportedSchema Availability = "unsupported-schema"
	AvailabilityFailed            Availability = "failed"
)

type SourceMode string

const (
	SourceModeInitializing SourceMode = "initializing"
	SourceModeLive         SourceMode = "live"
	SourceModePolling      SourceMode = "polling"
	SourceModeSnapshotOnly SourceMode = "snapshot-only"
	SourceModeUnavailable  SourceMode = "unavailable"
)

type SourceName string

const (
	SourceNodes          SourceName = "nodes"
	SourcePods           SourceName = "pods"
	SourceScenarios      SourceName = "scenario-instances"
	SourceResourceSlices SourceName = "resource-slices"
	SourceResourceClaims SourceName = "resource-claims"
	SourceDeviceClasses  SourceName = "device-classes"
)

type SourceState struct {
	Name         SourceName   `json:"name"`
	Availability Availability `json:"availability"`
	Mode         SourceMode   `json:"mode"`
	Freshness    Freshness    `json:"freshness"`
	LastSuccess  time.Time    `json:"lastSuccess,omitempty"`
	Diagnostic   string       `json:"diagnostic,omitempty"`
}

type FactState string

const (
	FactKnown       FactState = "known"
	FactUnknown     FactState = "unknown"
	FactUnavailable FactState = "unavailable"
	FactIncomplete  FactState = "incomplete"
)

type EvidenceKind string

const (
	EvidenceObserved       EvidenceKind = "observed"
	EvidenceDerived        EvidenceKind = "derived"
	EvidenceKasimSimulated EvidenceKind = "kasim-simulated"
	EvidenceUnavailable    EvidenceKind = "unavailable"
)

type Fact[T any] struct {
	State    FactState    `json:"state"`
	Value    T            `json:"value,omitempty"`
	Evidence EvidenceKind `json:"evidence"`
	Source   SourceName   `json:"source,omitempty"`
	Reason   string       `json:"reason,omitempty"`
}

type Ownership string

const (
	OwnershipKasim    Ownership = "kasim"
	OwnershipNonKasim Ownership = "non-kasim"
)

type SignalRole string

const (
	SignalRoleAccelerator  SignalRole = "accelerator"
	SignalRoleAuxiliary    SignalRole = "auxiliary"
	SignalRoleUnclassified SignalRole = "unclassified"
)

type Representation string

const (
	RepresentationScalar Representation = "scalar-extended-resource"
	RepresentationDRA    Representation = "dra-device"
)

type DeviceIdentity struct {
	Driver string `json:"driver"`
	Pool   string `json:"pool"`
	Device string `json:"device"`
}

type Signal struct {
	Role           SignalRole        `json:"role"`
	Representation Representation    `json:"representation"`
	Pool           string            `json:"pool,omitempty"`
	Category       string            `json:"category,omitempty"`
	Associations   []string          `json:"associations,omitempty"`
	ResourceName   string            `json:"resourceName,omitempty"`
	Device         *DeviceIdentity   `json:"device,omitempty"`
	Attributes     map[string]string `json:"attributes,omitempty"`
	Capacity       Fact[int64]       `json:"capacity"`
	Allocatable    Fact[int64]       `json:"allocatable"`
	Requested      Fact[int64]       `json:"requested"`
	Allocation     Fact[string]      `json:"allocation"`
	Health         Fact[string]      `json:"health"`
	Vendor         Fact[string]      `json:"vendor"`
	Model          Fact[string]      `json:"model"`
	Source         SourceName        `json:"source"`
}

type Node struct {
	Name      string       `json:"name"`
	Ownership Ownership    `json:"ownership"`
	Scenario  Fact[string] `json:"scenario"`
	Ready     Fact[bool]   `json:"ready"`
	Signals   []Signal     `json:"signals"`
}

type Target struct {
	ContextName       string `json:"contextName"`
	Fingerprint       string `json:"fingerprint"`
	KubernetesVersion string `json:"kubernetesVersion"`
}

type Diagnostic struct {
	Code    string     `json:"code"`
	Message string     `json:"message"`
	Source  SourceName `json:"source,omitempty"`
}

type Summary struct {
	Nodes         int `json:"nodes"`
	KasimNodes    int `json:"kasimNodes"`
	NonKasimNodes int `json:"nonKasimNodes"`
	ScalarSignals int `json:"scalarSignals"`
	DRADevices    int `json:"draDevices"`
}

type Snapshot struct {
	Revision     uint64        `json:"revision"`
	GeneratedAt  time.Time     `json:"generatedAt"`
	Completeness Completeness  `json:"completeness"`
	Freshness    Freshness     `json:"freshness"`
	Target       Target        `json:"target"`
	Sources      []SourceState `json:"sources"`
	Nodes        []Node        `json:"nodes"`
	Diagnostics  []Diagnostic  `json:"diagnostics"`
	Summary      Summary       `json:"summary"`
}

type ScenarioRecord struct {
	Name    string
	UID     string
	Signals []ScenarioSignalRecord
}

type ScenarioSignalRecord struct {
	NodeGroup                  string
	Pool                       string
	Role                       SignalRole
	Category                   string
	ResourceName               string
	Vendor                     string
	Model                      string
	AssociatedAcceleratorPools []string
}

type NodeRecord struct {
	Name        string
	Labels      map[string]string
	Capacity    map[string]int64
	Allocatable map[string]int64
	Ready       *bool
}

type PodRecord struct {
	Namespace string
	Name      string
	UID       string
	NodeName  string
	Requests  map[string]int64
	Claims    []string
}

type DRADeviceRecord struct {
	NodeName   string
	Driver     string
	Pool       string
	Device     string
	Attributes map[string]string
}

type ClaimAllocationRecord struct {
	Driver string
	Pool   string
	Device string
}

type ClaimRecord struct {
	Namespace   string
	Name        string
	Allocations []ClaimAllocationRecord
	ReservedFor []string
}

type Observation struct {
	Target      Target
	ObservedAt  time.Time
	Sources     []SourceState
	Scenarios   []ScenarioRecord
	Nodes       []NodeRecord
	Pods        []PodRecord
	Devices     []DRADeviceRecord
	Claims      []ClaimRecord
	Diagnostics []Diagnostic
}

// Source is the internal collection seam. Production Kubernetes and
// deterministic memory implementations are the maintained Adapters.
type Source interface {
	Open(context.Context, cluster.TargetSelection) (SourceStream, error)
}

type SourceStream interface {
	Target() Target
	Next(context.Context) (Observation, error)
	Close() error
}

type OpenRequest struct {
	Target cluster.TargetSelection
}

type SnapshotStream interface {
	Next(context.Context) (Snapshot, error)
	Close() error
}

type Module struct {
	source Source
}

func New(source Source) *Module {
	return &Module{source: source}
}

func (module *Module) Open(
	ctx context.Context,
	request OpenRequest,
) (SnapshotStream, error) {
	if module == nil || module.source == nil {
		return nil, fmt.Errorf("Cluster Simulation Inventory source is required")
	}
	if request.Target.KubeconfigPath == "" || request.Target.ContextName == "" {
		return nil, fmt.Errorf("explicit kubeconfig path and context name are both required")
	}
	source, err := module.source.Open(ctx, request.Target)
	if err != nil {
		return nil, err
	}
	return &snapshotStream{
		source: source,
		target: source.Target(),
	}, nil
}

type snapshotStream struct {
	source   SourceStream
	target   Target
	mu       sync.Mutex
	revision uint64
	loading  bool
	closed   bool
}

func (stream *snapshotStream) Next(ctx context.Context) (Snapshot, error) {
	stream.mu.Lock()
	if stream.closed {
		stream.mu.Unlock()
		return Snapshot{}, errors.New("Cluster Simulation Inventory stream is closed")
	}
	if !stream.loading {
		stream.loading = true
		stream.revision = 1
		snapshot := Snapshot{
			Revision:     stream.revision,
			GeneratedAt:  time.Now().UTC(),
			Completeness: CompletenessLoading,
			Freshness:    FreshnessLoading,
			Target:       stream.target,
		}
		stream.mu.Unlock()
		return snapshot, nil
	}
	stream.mu.Unlock()

	observation, err := stream.source.Next(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.closed {
		return Snapshot{}, errors.New("Cluster Simulation Inventory stream is closed")
	}
	stream.revision++
	return buildSnapshot(stream.revision, observation), nil
}

func (stream *snapshotStream) Close() error {
	stream.mu.Lock()
	if stream.closed {
		stream.mu.Unlock()
		return nil
	}
	stream.closed = true
	stream.mu.Unlock()
	return stream.source.Close()
}

func buildSnapshot(revision uint64, observation Observation) Snapshot {
	scenarioByUID := make(map[string]ScenarioRecord, len(observation.Scenarios))
	for _, scenario := range observation.Scenarios {
		if scenario.UID != "" && scenario.Name != "" {
			scenarioByUID[scenario.UID] = scenario
		}
	}
	requests := make(map[string]map[string]int64)
	for _, pod := range observation.Pods {
		if pod.NodeName == "" {
			continue
		}
		if requests[pod.NodeName] == nil {
			requests[pod.NodeName] = make(map[string]int64)
		}
		for resource, quantity := range pod.Requests {
			requests[pod.NodeName][resource] += quantity
		}
	}
	podsKnown := sourceAvailable(observation.Sources, SourcePods)
	claimsKnown := sourceAvailable(observation.Sources, SourceResourceClaims)
	nodeRecords := make(map[string]NodeRecord, len(observation.Nodes))
	for _, record := range observation.Nodes {
		nodeRecords[record.Name] = record
	}
	scheduledClaims := make(map[string]struct{})
	for _, pod := range observation.Pods {
		if pod.NodeName == "" {
			continue
		}
		for _, claim := range pod.Claims {
			scheduledClaims[pod.Namespace+"/"+claim] = struct{}{}
		}
	}
	allocationByDevice := make(map[string]string)
	for _, claim := range observation.Claims {
		phase := "allocated"
		if len(claim.ReservedFor) > 0 {
			phase = "reserved"
		}
		if _, found := scheduledClaims[claim.Namespace+"/"+claim.Name]; found {
			phase = "scheduled-consumer"
		}
		for _, allocation := range claim.Allocations {
			allocationByDevice[deviceKey(
				allocation.Driver,
				allocation.Pool,
				allocation.Device,
			)] = phase
		}
	}
	nodes := make([]Node, 0, len(observation.Nodes))
	for _, record := range observation.Nodes {
		ownership := OwnershipNonKasim
		scenario := Fact[string]{
			State: FactUnknown, Evidence: EvidenceUnavailable,
			Source: SourceScenarios, Reason: "no exact Kasim ownership join",
		}
		var ownedScenario ScenarioRecord
		if record.Labels[cluster.ManagedByLabel] == cluster.ManagedByValue {
			uid := record.Labels[cluster.InstanceUIDLabel]
			if joined, found := scenarioByUID[uid]; found {
				ownership = OwnershipKasim
				ownedScenario = joined
				scenario = Fact[string]{
					State: FactKnown, Value: joined.Name, Evidence: EvidenceDerived,
					Source: SourceScenarios,
				}
			}
		}
		ready := Fact[bool]{
			State: FactUnknown, Evidence: EvidenceUnavailable,
			Source: SourceNodes, Reason: "Node Ready was not reported",
		}
		if record.Ready != nil {
			ready = Fact[bool]{
				State: FactKnown, Value: *record.Ready,
				Evidence: EvidenceObserved, Source: SourceNodes,
			}
		}
		resourceNames := make(map[string]struct{})
		for resource := range record.Capacity {
			if isExtendedResource(resource) {
				resourceNames[resource] = struct{}{}
			}
		}
		for resource := range record.Allocatable {
			if isExtendedResource(resource) {
				resourceNames[resource] = struct{}{}
			}
		}
		names := make([]string, 0, len(resourceNames))
		for resource := range resourceNames {
			names = append(names, resource)
		}
		slices.Sort(names)
		signals := make([]Signal, 0, len(names))
		for _, resource := range names {
			metadata := classifyExact(RepresentationScalar, resource)
			if ownership == OwnershipKasim {
				metadata = scenarioSignal(
					ownedScenario,
					record.Labels["simulation.kasim.io/node-group"],
					resource,
				)
			}
			requested := Fact[int64]{
				State: FactUnknown, Evidence: EvidenceUnavailable,
				Source: SourcePods, Reason: "Pod requests are unavailable",
			}
			if podsKnown {
				requested = Fact[int64]{
					State: FactKnown, Value: requests[record.Name][resource],
					Evidence: EvidenceDerived, Source: SourcePods,
				}
			}
			signals = append(signals, Signal{
				Role: metadata.role, Representation: RepresentationScalar,
				Pool: metadata.pool, Category: metadata.category,
				Associations: append([]string(nil), metadata.associations...),
				ResourceName: resource, Source: SourceNodes,
				Capacity: Fact[int64]{
					State: FactKnown, Value: record.Capacity[resource],
					Evidence: EvidenceObserved, Source: SourceNodes,
				},
				Allocatable: Fact[int64]{
					State: FactKnown, Value: record.Allocatable[resource],
					Evidence: EvidenceObserved, Source: SourceNodes,
				},
				Requested: requested,
				Allocation: Fact[string]{
					State: FactUnknown, Evidence: EvidenceUnavailable,
					Source: SourceResourceClaims,
					Reason: "scalar extended resources have no native allocation identity",
				},
				Health: Fact[string]{
					State: FactUnknown, Evidence: EvidenceUnavailable,
					Source: SourceNodes, Reason: "health is not inferred from capacity or readiness",
				},
				Vendor: metadataFact(metadata.vendor, ownership, SourceNodes),
				Model:  metadataFact(metadata.model, ownership, SourceNodes),
			})
		}
		nodes = append(nodes, Node{
			Name: record.Name, Ownership: ownership, Scenario: scenario,
			Ready: ready, Signals: signals,
		})
	}
	slices.SortFunc(nodes, func(left, right Node) int {
		if left.Ownership != right.Ownership {
			if left.Ownership == OwnershipKasim {
				return -1
			}
			return 1
		}
		return strings.Compare(left.Name, right.Name)
	})
	for _, device := range observation.Devices {
		index := slices.IndexFunc(nodes, func(node Node) bool { return node.Name == device.NodeName })
		if index < 0 {
			continue
		}
		metadata := classifyExact(RepresentationDRA, device.Driver)
		if nodes[index].Ownership == OwnershipKasim {
			record := nodeRecords[device.NodeName]
			joined := scenarioByUID[record.Labels[cluster.InstanceUIDLabel]]
			nodeGroup := record.Labels["simulation.kasim.io/node-group"]
			metadata = scenarioSignal(joined, nodeGroup, device.Driver)
		}
		allocation := Fact[string]{
			State: FactUnknown, Evidence: EvidenceUnavailable,
			Source: SourceResourceClaims, Reason: "ResourceClaims are unavailable",
		}
		if claimsKnown {
			phase := allocationByDevice[deviceKey(device.Driver, device.Pool, device.Device)]
			if phase == "" {
				phase = "unallocated"
			}
			allocation = Fact[string]{
				State: FactKnown, Value: phase,
				Evidence: EvidenceDerived, Source: SourceResourceClaims,
			}
		}
		nodes[index].Signals = append(nodes[index].Signals, Signal{
			Role: metadata.role, Representation: RepresentationDRA,
			Pool: metadata.pool, Category: metadata.category,
			Associations: append([]string(nil), metadata.associations...),
			Device:       &DeviceIdentity{Driver: device.Driver, Pool: device.Pool, Device: device.Device},
			Attributes:   cloneStringMap(device.Attributes),
			Source:       SourceResourceSlices,
			Capacity: Fact[int64]{
				State: FactUnknown, Evidence: EvidenceUnavailable,
				Source: SourceResourceSlices, Reason: "native DRA devices are not scalar capacity",
			},
			Allocatable: Fact[int64]{
				State: FactUnknown, Evidence: EvidenceUnavailable,
				Source: SourceResourceSlices, Reason: "native DRA availability is claim-based",
			},
			Requested: Fact[int64]{
				State: FactUnknown, Evidence: EvidenceUnavailable,
				Source: SourceResourceClaims, Reason: "native DRA requests are not scalar quantities",
			},
			Allocation: allocation,
			Health: Fact[string]{
				State: FactUnknown, Evidence: EvidenceUnavailable,
				Source: SourceResourceSlices, Reason: "DRA device health was not reported",
			},
			Vendor: metadataFact(metadata.vendor, nodes[index].Ownership, SourceResourceSlices),
			Model:  modelFact(metadata.model, device.Attributes, device.Driver, nodes[index].Ownership),
		})
	}
	completeness := CompletenessComplete
	if len(observation.Nodes) == 0 && !sourceAvailable(observation.Sources, SourceNodes) {
		completeness = CompletenessDiagnosticsOnly
	} else {
		for _, source := range observation.Sources {
			if source.Availability != AvailabilityAvailable || source.Freshness == FreshnessIncomplete {
				completeness = CompletenessPartial
				break
			}
		}
	}
	freshness := aggregateFreshness(observation.Sources)
	summary := Summary{Nodes: len(nodes)}
	for _, node := range nodes {
		if node.Ownership == OwnershipKasim {
			summary.KasimNodes++
		} else {
			summary.NonKasimNodes++
		}
		for _, signal := range node.Signals {
			if signal.Representation == RepresentationDRA {
				summary.DRADevices++
			} else {
				summary.ScalarSignals++
			}
		}
	}
	return Snapshot{
		Revision: revision, GeneratedAt: observation.ObservedAt,
		Completeness: completeness, Freshness: freshness,
		Target:      observation.Target,
		Sources:     append([]SourceState(nil), observation.Sources...),
		Nodes:       nodes,
		Diagnostics: append([]Diagnostic(nil), observation.Diagnostics...),
		Summary:     summary,
	}
}

func sourceAvailable(sources []SourceState, name SourceName) bool {
	for _, source := range sources {
		if source.Name == name {
			return source.Availability == AvailabilityAvailable
		}
	}
	return false
}

func aggregateFreshness(sources []SourceState) Freshness {
	result := FreshnessFresh
	for _, source := range sources {
		switch source.Freshness {
		case FreshnessStale:
			return FreshnessStale
		case FreshnessReconnecting:
			result = FreshnessReconnecting
		case FreshnessResyncing:
			if result == FreshnessFresh {
				result = FreshnessResyncing
			}
		case FreshnessIncomplete:
			if result == FreshnessFresh {
				result = FreshnessIncomplete
			}
		}
	}
	return result
}

func isExtendedResource(name string) bool {
	parts := strings.SplitN(name, "/", 2)
	return len(parts) == 2 && parts[0] != "kubernetes.io" && parts[1] != ""
}

type signalClassification struct {
	role         SignalRole
	pool         string
	category     string
	vendor       string
	model        string
	associations []string
}

var (
	classificationOnce   sync.Once
	exactClassifications map[string]signalClassification
)

func classifyExact(
	representation Representation,
	resourceName string,
) signalClassification {
	classificationOnce.Do(func() {
		exactClassifications = make(map[string]signalClassification)
		ambiguous := make(map[string]struct{})
		snapshot, err := catalog.LoadBundled()
		if err != nil {
			return
		}
		for _, summary := range snapshot.List() {
			profile, err := snapshot.Show(summary.ID())
			if err != nil {
				continue
			}
			for _, contract := range profile.Contracts() {
				representation := RepresentationScalar
				if contract.Kind() == "dra" {
					representation = RepresentationDRA
				}
				for _, resource := range contract.Resources() {
					if resource.Name() == "" {
						continue
					}
					key := string(representation) + "\x00" + resource.Name()
					role := SignalRoleAccelerator
					if contract.Subject() == "auxiliary" {
						role = SignalRoleAuxiliary
					}
					candidate := signalClassification{
						role: role, category: contract.AuxiliaryCategory(),
						vendor: profile.DisplayName(),
					}
					if current, found := exactClassifications[key]; found &&
						(current.vendor != candidate.vendor || current.role != candidate.role) {
						ambiguous[key] = struct{}{}
						delete(exactClassifications, key)
						continue
					}
					if _, conflict := ambiguous[key]; !conflict {
						exactClassifications[key] = candidate
					}
				}
			}
		}
	})
	classification, found := exactClassifications[string(representation)+"\x00"+resourceName]
	if !found {
		return signalClassification{role: SignalRoleUnclassified}
	}
	classification.associations = append([]string(nil), classification.associations...)
	return classification
}

func scenarioSignal(
	scenario ScenarioRecord,
	nodeGroup,
	resourceName string,
) signalClassification {
	for _, signal := range scenario.Signals {
		if signal.NodeGroup == nodeGroup && signal.ResourceName == resourceName {
			return signalClassification{
				role: signal.Role, pool: signal.Pool, category: signal.Category,
				vendor: signal.Vendor, model: signal.Model,
				associations: append(
					[]string(nil),
					signal.AssociatedAcceleratorPools...,
				),
			}
		}
	}
	representation := RepresentationScalar
	if !strings.Contains(resourceName, "/") {
		representation = RepresentationDRA
	}
	return classifyExact(representation, resourceName)
}

func metadataFact(
	value string,
	ownership Ownership,
	source SourceName,
) Fact[string] {
	if value == "" {
		return Fact[string]{
			State: FactUnknown, Evidence: EvidenceUnavailable, Source: source,
			Reason: "no exact source-backed evidence",
		}
	}
	if ownership == OwnershipKasim {
		return Fact[string]{
			State: FactKnown, Value: value,
			Evidence: EvidenceKasimSimulated, Source: SourceScenarios,
		}
	}
	return Fact[string]{
		State: FactKnown, Value: value,
		Evidence: EvidenceDerived, Source: source,
	}
}

func modelFact(
	model string,
	attributes map[string]string,
	driver string,
	ownership Ownership,
) Fact[string] {
	if model != "" {
		return metadataFact(model, ownership, SourceResourceSlices)
	}
	attribute := ""
	switch driver {
	case "gpu.nvidia.com":
		attribute = "gpu.nvidia.com/productName"
	case "gpu.amd.com":
		attribute = "gpu.amd.com/productName"
	case "neuron.aws.com":
		attribute = "neuron.aws.com/instanceType"
	}
	if attribute != "" && attributes[attribute] != "" {
		return Fact[string]{
			State: FactKnown, Value: attributes[attribute],
			Evidence: EvidenceObserved, Source: SourceResourceSlices,
		}
	}
	return Fact[string]{
		State: FactUnknown, Evidence: EvidenceUnavailable,
		Source: SourceResourceSlices, Reason: "model has no allowlisted source-backed attribute",
	}
}

func deviceKey(driver, pool, device string) string {
	return driver + "\x00" + pool + "\x00" + device
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
