// Package recording provides the deterministic Cluster port Adapter used by
// lifecycle Module tests.
package recording

import (
	"context"
	"fmt"
	"sync"

	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
)

// Call is one observable Cluster port invocation.
type Call string

const (
	CallDiscover      Call = "Discover"
	CallAuthorize     Call = "Authorize"
	CallObserve       Call = "Observe"
	CallExecuteDryRun Call = "ExecuteServerDryRun"
	CallExecute       Call = "ExecutePersistent"
)

// Options configures deterministic returned evidence.
type Options struct {
	Capabilities cluster.TargetCapabilities
	Observed     cluster.ObservedGraph
	Denied       []cluster.AccessRequirement
	Errors       map[Call]error
}

// Adapter records calls and persistent change sets without Kubernetes types.
type Adapter struct {
	mutex      sync.Mutex
	options    Options
	calls      []Call
	attempted  []cluster.OwnedChangeSet
	persistent []cluster.OwnedChangeSet
}

func New(options Options) *Adapter {
	options.Capabilities = cloneCapabilities(options.Capabilities)
	options.Observed.Objects = append(
		[]cluster.ObservedObject(nil),
		options.Observed.Objects...,
	)
	for index := range options.Observed.Objects {
		options.Observed.Objects[index] = cloneObservedObject(
			options.Observed.Objects[index],
		)
	}
	options.Observed.Pods = cloneObservedPods(options.Observed.Pods)
	options.Denied = append([]cluster.AccessRequirement(nil), options.Denied...)
	errorsByCall := make(map[Call]error, len(options.Errors))
	for call, err := range options.Errors {
		errorsByCall[call] = err
	}
	options.Errors = errorsByCall
	return &Adapter{options: options}
}

func (adapter *Adapter) Discover(
	_ context.Context,
) (cluster.TargetCapabilities, error) {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	adapter.calls = append(adapter.calls, CallDiscover)
	if err := adapter.options.Errors[CallDiscover]; err != nil {
		return cluster.TargetCapabilities{}, err
	}
	return cloneCapabilities(adapter.options.Capabilities), nil
}

func (adapter *Adapter) Authorize(
	_ context.Context,
	requirements []cluster.AccessRequirement,
) (cluster.AuthorizationReport, error) {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	adapter.calls = append(adapter.calls, CallAuthorize)
	if err := adapter.options.Errors[CallAuthorize]; err != nil {
		return cluster.AuthorizationReport{}, err
	}
	decisions := make([]cluster.AuthorizationDecision, 0, len(requirements))
	for _, requirement := range requirements {
		if err := requirement.Validate(); err != nil {
			return cluster.AuthorizationReport{}, err
		}
		allowed := !containsRequirement(adapter.options.Denied, requirement)
		reason := "allowed by deterministic recording adapter"
		if !allowed {
			reason = "denied by deterministic recording adapter"
		}
		decisions = append(decisions, cluster.AuthorizationDecision{
			Requirement: requirement,
			Allowed:     allowed,
			Reason:      reason,
		})
	}
	return cluster.AuthorizationReport{Decisions: decisions}, nil
}

func (adapter *Adapter) Observe(
	_ context.Context,
	scope cluster.OwnershipScope,
) (cluster.ObservedGraph, error) {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	adapter.calls = append(adapter.calls, CallObserve)
	if err := adapter.options.Errors[CallObserve]; err != nil {
		return cluster.ObservedGraph{}, err
	}
	if scope.InstanceUID().String() == "" ||
		scope.DesiredGeneration().Value() == 0 {
		return cluster.ObservedGraph{}, fmt.Errorf(
			"recording observation requires exact ownership",
		)
	}
	return cluster.ObservedGraph{
		Objects: cloneObservedObjects(adapter.options.Observed.Objects),
		Pods:    cloneObservedPods(adapter.options.Observed.Pods),
	}, nil
}

func (adapter *Adapter) Execute(
	_ context.Context,
	changeSet cluster.OwnedChangeSet,
) (cluster.MutationReceipt, error) {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	dryRun := changeSet.Mode() == cluster.ExecutionServerDryRun
	call := CallExecute
	if dryRun {
		call = CallExecuteDryRun
	}
	adapter.calls = append(adapter.calls, call)
	adapter.attempted = append(adapter.attempted, changeSet)
	if err := adapter.options.Errors[call]; err != nil {
		return cluster.MutationReceipt{}, err
	}
	changes := changeSet.Changes()
	receipt := cluster.MutationReceipt{
		DryRun:    dryRun,
		Attempted: len(changes),
	}
	if !dryRun {
		adapter.persistent = append(adapter.persistent, changeSet)
		receipt.Persisted = len(changes)
	}
	return receipt, nil
}

func (adapter *Adapter) Calls() []Call {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	return append([]Call(nil), adapter.calls...)
}

func (adapter *Adapter) PersistentChangeSets() []cluster.OwnedChangeSet {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	return append([]cluster.OwnedChangeSet(nil), adapter.persistent...)
}

func (adapter *Adapter) AttemptedChangeSets() []cluster.OwnedChangeSet {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	return append([]cluster.OwnedChangeSet(nil), adapter.attempted...)
}

// ClearError releases one deterministic failure point so the same adapter can
// exercise a later retry without replacing its recorded history.
func (adapter *Adapter) ClearError(call Call) {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()
	delete(adapter.options.Errors, call)
}

func containsRequirement(
	requirements []cluster.AccessRequirement,
	candidate cluster.AccessRequirement,
) bool {
	for _, requirement := range requirements {
		if requirement == candidate {
			return true
		}
	}
	return false
}

func cloneCapabilities(
	input cluster.TargetCapabilities,
) cluster.TargetCapabilities {
	result := input
	result.Resources = make(
		[]cluster.ResourceCapability,
		len(input.Resources),
	)
	for index, capability := range input.Resources {
		result.Resources[index] = capability
		result.Resources[index].Verbs = append(
			[]string(nil),
			capability.Verbs...,
		)
	}
	return result
}

func cloneObservedObjects(
	input []cluster.ObservedObject,
) []cluster.ObservedObject {
	result := make([]cluster.ObservedObject, len(input))
	for index, object := range input {
		result[index] = cloneObservedObject(object)
	}
	return result
}

func cloneObservedObject(
	input cluster.ObservedObject,
) cluster.ObservedObject {
	result := input
	if input.Node != nil {
		node := *input.Node
		node.Labels = cloneStringMap(input.Node.Labels)
		node.Annotations = cloneStringMap(input.Node.Annotations)
		node.Taints = append([]cluster.NodeTaint(nil), input.Node.Taints...)
		node.Capacity = cloneStringMap(input.Node.Capacity)
		node.Allocatable = cloneStringMap(input.Node.Allocatable)
		result.Node = &node
	}
	if input.Lease != nil {
		lease := *input.Lease
		result.Lease = &lease
	}
	return result
}

func cloneObservedPods(input []cluster.ObservedPod) []cluster.ObservedPod {
	result := make([]cluster.ObservedPod, len(input))
	for index, pod := range input {
		result[index] = pod
		result[index].Requested = cloneStringMap(pod.Requested)
	}
	return result
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

var _ cluster.Port = (*Adapter)(nil)
