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
	persistent []cluster.OwnedChangeSet
}

func New(options Options) *Adapter {
	options.Capabilities = cloneCapabilities(options.Capabilities)
	options.Observed.Objects = append(
		[]cluster.ObservedObject(nil),
		options.Observed.Objects...,
	)
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
		Objects: append(
			[]cluster.ObservedObject(nil),
			adapter.options.Observed.Objects...,
		),
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

var _ cluster.Port = (*Adapter)(nil)
