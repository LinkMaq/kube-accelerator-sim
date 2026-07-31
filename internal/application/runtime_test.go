package application_test

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LinkMaq/kube-accelerator-sim/internal/application"
	"github.com/LinkMaq/kube-accelerator-sim/internal/catalog"
	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster/recording"
	"github.com/LinkMaq/kube-accelerator-sim/internal/controlplane"
	"github.com/LinkMaq/kube-accelerator-sim/internal/controlplane/memory"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
	"github.com/LinkMaq/kube-accelerator-sim/internal/scenario"
)

func TestScenarioRuntimeApplyAsyncAcceptsAfterOrderedPreflight(t *testing.T) {
	t.Parallel()

	trace := []string{}
	command := revisionCommand(t, 1, "first")
	controlAdapter := memory.New(memory.Options{HistoryLimit: 8})
	clusterAdapter := recording.New(recording.Options{
		Capabilities: cluster.TargetCapabilities{
			ServerVersion:   "v1.36.3",
			KubernetesMinor: 36,
		},
	})
	connected := application.ConnectedTarget{
		Receipt:      targetReceipt(command.Target),
		Target:       command.Target,
		ControlPlane: &tracedControlPlane{trace: &trace, delegate: controlAdapter},
		Cluster:      &tracedCluster{trace: &trace, delegate: clusterAdapter},
	}
	runtime, err := application.NewScenarioRuntime(application.RuntimeOptions{
		Connect: func(
			context.Context,
			cluster.TargetSelection,
		) (application.ConnectedTarget, error) {
			trace = append(trace, "connect")
			return connected, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Apply(context.Background(), application.ApplyRequest{
		Selection: cluster.TargetSelection{
			KubeconfigPath: "/explicit/config",
			ContextName:    "test-context",
		},
		Intent: intentOf(command),
		Mode:   application.DryRunNone,
		Async:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := trace, []string{
		"connect",
		"probe",
		"discover",
		"authorize",
		"read",
		"submit-dry-run",
		"submit",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("runtime Apply order = %#v, want %#v", got, want)
	}
	if !result.Receipt.RevisionAccepted() ||
		result.Receipt.NoOp() ||
		result.Receipt.InstanceUID().String() == "" ||
		result.Receipt.DesiredGeneration().Value() != 1 ||
		result.Receipt.ObservedGeneration().Value() != 0 ||
		result.Receipt.RevisionDigest() != command.Revision.Digest ||
		result.Connection.TargetFingerprint != command.Target.Fingerprint {
		t.Fatalf("unexpected accepted result: %#v", result)
	}
	record, err := controlAdapter.Read(context.Background(), controlplane.InstanceKey{
		TargetFingerprint: command.Target.Fingerprint,
		Name:              command.Name,
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.DesiredGeneration.Value() != 1 {
		t.Fatalf("persistent acceptance generation = %d", record.DesiredGeneration.Value())
	}
}

func TestScenarioRuntimeApplyWaitsForObservedReadyGeneration(t *testing.T) {
	t.Parallel()

	command := revisionCommand(t, 1, "first")
	controlAdapter := memory.New(memory.Options{HistoryLimit: 8})
	clusterAdapter := recording.New(recording.Options{
		Capabilities: cluster.TargetCapabilities{
			ServerVersion:   "v1.36.3",
			KubernetesMinor: 36,
		},
	})
	connected := application.ConnectedTarget{
		Receipt:      targetReceipt(command.Target),
		Target:       command.Target,
		ControlPlane: controlAdapter,
		Cluster:      clusterAdapter,
	}
	runtime, err := application.NewScenarioRuntime(application.RuntimeOptions{
		Connect: func(
			context.Context,
			cluster.TargetSelection,
		) (application.ConnectedTarget, error) {
			return connected, nil
		},
		ReconnectDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	key := controlplane.InstanceKey{
		TargetFingerprint: command.Target.Fingerprint,
		Name:              command.Name,
	}
	commitErrors := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(time.Second)
		for {
			record, readErr := controlAdapter.Read(context.Background(), key)
			if readErr == nil {
				commitErrors <- controlAdapter.CommitStatus(
					context.Background(),
					key,
					record.DesiredGeneration,
					controlplane.InstanceStatus{
						RevisionDigest: record.Revision.Digest,
						Phase:          "Ready",
					},
				)
				return
			}
			if time.Now().After(deadline) {
				commitErrors <- readErr
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	result, err := runtime.Apply(context.Background(), application.ApplyRequest{
		Selection: cluster.TargetSelection{
			KubeconfigPath: "/explicit/config",
			ContextName:    "test-context",
		},
		Intent:  intentOf(command),
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-commitErrors; err != nil {
		t.Fatal(err)
	}
	if result.Snapshot == nil ||
		result.Snapshot.Phase != "Ready" ||
		result.Snapshot.ObservedGeneration.Value() != 1 ||
		result.Receipt.ObservedGeneration().Value() != 1 ||
		result.Receipt.DesiredGeneration().Value() != 1 {
		t.Fatalf("runtime returned before observed Ready: %#v", result)
	}
}

func TestScenarioRuntimeApplyReturnsSameDigestNoOpWithoutWaiting(t *testing.T) {
	t.Parallel()

	command := revisionCommand(t, 1, "first")
	controlAdapter := memory.New(memory.Options{HistoryLimit: 8})
	created, err := controlAdapter.Submit(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	key := controlplane.InstanceKey{
		TargetFingerprint: command.Target.Fingerprint,
		Name:              command.Name,
	}
	if err := controlAdapter.CommitStatus(
		context.Background(),
		key,
		created.DesiredGeneration,
		controlplane.InstanceStatus{
			RevisionDigest: command.Revision.Digest,
			Phase:          "Ready",
		},
	); err != nil {
		t.Fatal(err)
	}
	connected := application.ConnectedTarget{
		Receipt:      targetReceipt(command.Target),
		Target:       command.Target,
		ControlPlane: controlAdapter,
		Cluster: recording.New(recording.Options{
			Capabilities: cluster.TargetCapabilities{
				ServerVersion: "v1.36.3", KubernetesMinor: 36,
			},
		}),
	}
	runtime, err := application.NewScenarioRuntime(application.RuntimeOptions{
		Connect: func(
			context.Context,
			cluster.TargetSelection,
		) (application.ConnectedTarget, error) {
			return connected, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Apply(context.Background(), application.ApplyRequest{
		Selection: cluster.TargetSelection{
			KubeconfigPath: "/explicit/config",
			ContextName:    "test-context",
		},
		Intent:  intentOf(command),
		Timeout: time.Nanosecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Receipt.NoOp() ||
		result.Receipt.RevisionAccepted() ||
		result.Receipt.DesiredGeneration().Value() != 1 ||
		result.Receipt.ObservedGeneration().Value() != 1 ||
		result.Snapshot == nil ||
		result.Snapshot.Phase != "Ready" {
		t.Fatalf("unexpected no-op result: %#v", result)
	}
}

func TestScenarioRuntimeTimeoutReturnsAcceptedReceiptAndLatestSnapshot(t *testing.T) {
	t.Parallel()

	command := revisionCommand(t, 1, "first")
	controlAdapter := memory.New(memory.Options{HistoryLimit: 8})
	connected := application.ConnectedTarget{
		Receipt:      targetReceipt(command.Target),
		Target:       command.Target,
		ControlPlane: controlAdapter,
		Cluster: recording.New(recording.Options{
			Capabilities: cluster.TargetCapabilities{
				ServerVersion: "v1.36.3", KubernetesMinor: 36,
			},
		}),
	}
	runtime, err := application.NewScenarioRuntime(application.RuntimeOptions{
		Connect: func(
			context.Context,
			cluster.TargetSelection,
		) (application.ConnectedTarget, error) {
			return connected, nil
		},
		ReconnectDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Apply(context.Background(), application.ApplyRequest{
		Selection: cluster.TargetSelection{
			KubeconfigPath: "/explicit/config",
			ContextName:    "test-context",
		},
		Intent:  intentOf(command),
		Timeout: 10 * time.Millisecond,
	})
	var runtimeError *application.RuntimeError
	if !errors.As(err, &runtimeError) {
		t.Fatalf("timeout error = %T %v", err, err)
	}
	diagnostic := runtimeError.Diagnostic()
	if diagnostic.Code().String() != "ConvergenceTimeout" ||
		diagnostic.ExitCategory().Code() != 5 ||
		!diagnostic.RevisionAccepted() ||
		!result.Receipt.RevisionAccepted() ||
		result.Receipt.InstanceUID().String() == "" ||
		result.Snapshot == nil ||
		result.Snapshot.Phase != "Pending" ||
		runtimeError.Result().Receipt.InstanceUID() != result.Receipt.InstanceUID() {
		t.Fatalf("timeout lost accepted outcome: result=%#v error=%#v", result, runtimeError)
	}
}

func TestScenarioRuntimeServerDryRunReturnsProposalWithoutAcceptance(t *testing.T) {
	t.Parallel()

	command := revisionCommand(t, 1, "first")
	controlAdapter := memory.New(memory.Options{HistoryLimit: 8})
	connected := application.ConnectedTarget{
		Receipt:      targetReceipt(command.Target),
		Target:       command.Target,
		ControlPlane: controlAdapter,
		Cluster: recording.New(recording.Options{
			Capabilities: cluster.TargetCapabilities{
				ServerVersion: "v1.36.3", KubernetesMinor: 36,
			},
		}),
	}
	runtime, err := application.NewScenarioRuntime(application.RuntimeOptions{
		Connect: func(
			context.Context,
			cluster.TargetSelection,
		) (application.ConnectedTarget, error) {
			return connected, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Apply(context.Background(), application.ApplyRequest{
		Selection: cluster.TargetSelection{
			KubeconfigPath: "/explicit/config",
			ContextName:    "test-context",
		},
		Intent: intentOf(command),
		Mode:   application.DryRunServer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt.RevisionAccepted() ||
		result.Receipt.DesiredGeneration().Value() != 1 ||
		result.Warning == "" {
		t.Fatalf("unexpected server dry-run proposal: %#v", result)
	}
	if _, err := controlAdapter.Read(
		context.Background(),
		controlplane.InstanceKey{
			TargetFingerprint: command.Target.Fingerprint,
			Name:              command.Name,
		},
	); controlplane.ErrorCodeOf(err) != controlplane.ErrorNotFound {
		t.Fatalf("server dry-run persisted a Scenario Instance: %v", err)
	}
}

func TestScenarioRuntimeAcceptedFailedPhaseReturnsCategoryFiveSnapshot(t *testing.T) {
	t.Parallel()

	command := revisionCommand(t, 1, "first")
	controlAdapter := memory.New(memory.Options{HistoryLimit: 8})
	connected := application.ConnectedTarget{
		Receipt:      targetReceipt(command.Target),
		Target:       command.Target,
		ControlPlane: controlAdapter,
		Cluster: recording.New(recording.Options{
			Capabilities: cluster.TargetCapabilities{
				ServerVersion: "v1.36.3", KubernetesMinor: 36,
			},
		}),
	}
	runtime, err := application.NewScenarioRuntime(application.RuntimeOptions{
		Connect: func(
			context.Context,
			cluster.TargetSelection,
		) (application.ConnectedTarget, error) {
			return connected, nil
		},
		ReconnectDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	key := controlplane.InstanceKey{
		TargetFingerprint: command.Target.Fingerprint,
		Name:              command.Name,
	}
	controllerErrors := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(time.Second)
		for {
			record, readErr := controlAdapter.Read(context.Background(), key)
			if readErr == nil {
				controllerErrors <- controlAdapter.CommitStatus(
					context.Background(),
					key,
					record.DesiredGeneration,
					controlplane.InstanceStatus{
						RevisionDigest: record.Revision.Digest,
						Phase:          "Failed",
						Diagnostics: []controlplane.DiagnosticStatus{{
							Code:             "OwnershipConflict",
							Message:          "owned Node identity conflicted",
							Retryable:        false,
							RevisionAccepted: true,
							ExitCategory:     5,
						}},
					},
				)
				return
			}
			if time.Now().After(deadline) {
				controllerErrors <- readErr
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	result, err := runtime.Apply(context.Background(), application.ApplyRequest{
		Selection: cluster.TargetSelection{
			KubeconfigPath: "/explicit/config",
			ContextName:    "test-context",
		},
		Intent:  intentOf(command),
		Timeout: time.Second,
	})
	if controllerErr := <-controllerErrors; controllerErr != nil {
		t.Fatal(controllerErr)
	}
	var runtimeError *application.RuntimeError
	if !errors.As(err, &runtimeError) ||
		runtimeError.Diagnostic().Code().String() != "OwnershipConflict" ||
		runtimeError.Diagnostic().ExitCategory().Code() != 5 ||
		result.Snapshot == nil ||
		result.Snapshot.Phase != "Failed" ||
		len(result.Snapshot.Diagnostics) != 1 {
		t.Fatalf("accepted failure lost status: result=%#v err=%#v", result, err)
	}
}

func TestScenarioRuntimeDeleteAsyncUsesExactPreconditionsAndIsIdempotent(t *testing.T) {
	t.Parallel()

	trace := []string{}
	command := revisionCommand(t, 1, "first")
	controlAdapter := memory.New(memory.Options{HistoryLimit: 8})
	created, err := controlAdapter.Submit(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	clusterAdapter := recording.New(recording.Options{
		Capabilities: cluster.TargetCapabilities{
			ServerVersion: "v1.36.3", KubernetesMinor: 36,
		},
	})
	connected := application.ConnectedTarget{
		Receipt:      targetReceipt(command.Target),
		Target:       command.Target,
		ControlPlane: &tracedControlPlane{trace: &trace, delegate: controlAdapter},
		Cluster:      &tracedCluster{trace: &trace, delegate: clusterAdapter},
	}
	runtime, err := application.NewScenarioRuntime(application.RuntimeOptions{
		Connect: func(
			context.Context,
			cluster.TargetSelection,
		) (application.ConnectedTarget, error) {
			trace = append(trace, "connect")
			return connected, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := application.DeleteRequest{
		Selection: cluster.TargetSelection{
			KubeconfigPath: "/explicit/config",
			ContextName:    "test-context",
		},
		Name:               command.Name,
		InstanceUID:        created.InstanceUID,
		ExpectedGeneration: created.DesiredGeneration,
		Async:              true,
	}
	result, err := runtime.Delete(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := trace, []string{
		"connect", "probe", "discover", "authorize", "read", "delete",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("delete order = %#v, want %#v", got, want)
	}
	if !result.Receipt.RevisionAccepted() ||
		result.Receipt.NoOp() ||
		result.Receipt.InstanceUID() != created.InstanceUID ||
		result.Receipt.DesiredGeneration() != created.DesiredGeneration ||
		len(clusterAdapter.PersistentChangeSets()) != 0 {
		t.Fatalf("unexpected async deletion result: %#v", result)
	}
	record, err := controlAdapter.Read(context.Background(), controlplane.InstanceKey{
		TargetFingerprint: command.Target.Fingerprint,
		Name:              command.Name,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !record.DeletionRequested {
		t.Fatal("async deletion did not persist desired state")
	}

	trace = nil
	retry, err := runtime.Delete(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Receipt.RevisionAccepted() || !retry.Receipt.NoOp() {
		t.Fatalf("delete retry was not idempotent: %#v", retry)
	}
}

func TestScenarioRuntimeDeleteWaitsForConfirmedObjectRemoval(t *testing.T) {
	t.Parallel()

	command := revisionCommand(t, 1, "first")
	controlAdapter := memory.New(memory.Options{HistoryLimit: 8})
	created, err := controlAdapter.Submit(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	connected := application.ConnectedTarget{
		Receipt:      targetReceipt(command.Target),
		Target:       command.Target,
		ControlPlane: controlAdapter,
		Cluster: recording.New(recording.Options{
			Capabilities: cluster.TargetCapabilities{
				ServerVersion: "v1.36.3", KubernetesMinor: 36,
			},
		}),
	}
	runtime, err := application.NewScenarioRuntime(application.RuntimeOptions{
		Connect: func(
			context.Context,
			cluster.TargetSelection,
		) (application.ConnectedTarget, error) {
			return connected, nil
		},
		ReconnectDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	key := controlplane.InstanceKey{
		TargetFingerprint: command.Target.Fingerprint,
		Name:              command.Name,
	}
	controllerErrors := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(time.Second)
		for {
			record, readErr := controlAdapter.Read(context.Background(), key)
			if readErr == nil && record.DeletionRequested {
				if commitErr := controlAdapter.CommitStatus(
					context.Background(),
					key,
					record.DesiredGeneration,
					controlplane.InstanceStatus{
						RevisionDigest: record.Revision.Digest,
						Phase:          "Deleting",
					},
				); commitErr != nil {
					controllerErrors <- commitErr
					return
				}
				time.Sleep(5 * time.Millisecond)
				controllerErrors <- controlAdapter.CompleteDeletion(
					context.Background(),
					key,
				)
				return
			}
			if time.Now().After(deadline) {
				controllerErrors <- readErr
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	result, err := runtime.Delete(context.Background(), application.DeleteRequest{
		Selection: cluster.TargetSelection{
			KubeconfigPath: "/explicit/config",
			ContextName:    "test-context",
		},
		Name:               command.Name,
		InstanceUID:        created.InstanceUID,
		ExpectedGeneration: created.DesiredGeneration,
		Timeout:            time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-controllerErrors; err != nil {
		t.Fatal(err)
	}
	if result.Snapshot == nil ||
		result.Snapshot.Phase != "Deleting" ||
		result.Receipt.ObservedGeneration().Value() != 1 {
		t.Fatalf("delete completion lost latest Snapshot: %#v", result)
	}
	if _, err := controlAdapter.Read(
		context.Background(),
		key,
	); controlplane.ErrorCodeOf(err) != controlplane.ErrorNotFound {
		t.Fatalf("delete returned before object removal: %v", err)
	}
}

func TestScenarioRuntimeDeleteReturnsAcceptedCleanupBlocker(t *testing.T) {
	t.Parallel()

	command := revisionCommand(t, 1, "first")
	controlAdapter := memory.New(memory.Options{HistoryLimit: 8})
	created, err := controlAdapter.Submit(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	connected := application.ConnectedTarget{
		Receipt:      targetReceipt(command.Target),
		Target:       command.Target,
		ControlPlane: controlAdapter,
		Cluster: recording.New(recording.Options{
			Capabilities: cluster.TargetCapabilities{
				ServerVersion: "v1.36.3", KubernetesMinor: 36,
			},
		}),
	}
	runtime, err := application.NewScenarioRuntime(application.RuntimeOptions{
		Connect: func(
			context.Context,
			cluster.TargetSelection,
		) (application.ConnectedTarget, error) {
			return connected, nil
		},
		ReconnectDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	key := controlplane.InstanceKey{
		TargetFingerprint: command.Target.Fingerprint,
		Name:              command.Name,
	}
	controllerErrors := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(time.Second)
		for {
			record, readErr := controlAdapter.Read(context.Background(), key)
			if readErr == nil && record.DeletionRequested {
				controllerErrors <- controlAdapter.CommitStatus(
					context.Background(),
					key,
					record.DesiredGeneration,
					controlplane.InstanceStatus{
						RevisionDigest: record.Revision.Digest,
						Phase:          "Deleting",
						Conditions: []controlplane.ConditionStatus{{
							Type:    "CleanupBlocked",
							Status:  "True",
							Reason:  "CleanupBlocked",
							Message: "unowned Pod workload/training remains bound",
							ObservedGeneration: int64(
								record.DesiredGeneration.Value(),
							),
							LastTransitionTime: time.Now().UTC(),
						}},
					},
				)
				return
			}
			if time.Now().After(deadline) {
				controllerErrors <- readErr
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	result, err := runtime.Delete(context.Background(), application.DeleteRequest{
		Selection: cluster.TargetSelection{
			KubeconfigPath: "/explicit/config",
			ContextName:    "test-context",
		},
		Name:               command.Name,
		InstanceUID:        created.InstanceUID,
		ExpectedGeneration: created.DesiredGeneration,
		Timeout:            time.Second,
	})
	if controllerErr := <-controllerErrors; controllerErr != nil {
		t.Fatal(controllerErr)
	}
	var runtimeError *application.RuntimeError
	if !errors.As(err, &runtimeError) ||
		runtimeError.Diagnostic().Code().String() != "CleanupBlocked" ||
		runtimeError.Diagnostic().ExitCategory().Code() != 5 ||
		result.Snapshot == nil ||
		result.Snapshot.Phase != "Deleting" ||
		len(result.Snapshot.Conditions) != 1 {
		t.Fatalf("cleanup blocker was not preserved: result=%#v err=%#v", result, err)
	}
}

func TestScenarioRuntimeObserveReturnsBoundedTargetBoundSnapshot(t *testing.T) {
	t.Parallel()

	command := revisionCommand(t, 1, "first")
	controlAdapter := memory.New(memory.Options{HistoryLimit: 8})
	created, err := controlAdapter.Submit(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	key := controlplane.InstanceKey{
		TargetFingerprint: command.Target.Fingerprint,
		Name:              command.Name,
	}
	conditions := make(
		[]controlplane.ConditionStatus,
		controlplane.MaximumStatusConditions+1,
	)
	for index := range conditions {
		conditions[index] = controlplane.ConditionStatus{
			Type: "FidelitySatisfied", Status: "True",
			Reason: "FidelitySatisfied", Message: "all surfaces observed",
			ObservedGeneration: 1, LastTransitionTime: time.Now().UTC(),
		}
	}
	fidelity := make(
		[]controlplane.FidelitySurfaceStatus,
		controlplane.MaximumStatusFidelity+1,
	)
	for index := range fidelity {
		fidelity[index] = controlplane.FidelitySurfaceStatus{
			Surface: "surface",
			State:   "achieved",
		}
	}
	if err := controlAdapter.CommitStatus(
		context.Background(),
		key,
		created.DesiredGeneration,
		controlplane.InstanceStatus{
			RevisionDigest: command.Revision.Digest,
			Phase:          "Ready",
			Pools: []controlplane.PoolStatus{{
				Group: "nodes", Pool: "accelerators",
				RequestedTotal: 8, RequestedHealthy: 6,
				ObservedTotal: 8, ObservedHealthy: 6,
			}},
			Inventory: []controlplane.InventoryEntry{{
				APIVersion: "v1", Kind: "Node", Count: 1,
			}},
			Fidelity:   fidelity,
			Conditions: conditions,
		},
	); err != nil {
		t.Fatal(err)
	}
	connected := application.ConnectedTarget{
		Receipt:      targetReceipt(command.Target),
		Target:       command.Target,
		ControlPlane: controlAdapter,
		Cluster: recording.New(recording.Options{
			Capabilities: cluster.TargetCapabilities{
				ServerVersion: "v1.36.3", KubernetesMinor: 36,
			},
		}),
	}
	runtime, err := application.NewScenarioRuntime(application.RuntimeOptions{
		Connect: func(
			context.Context,
			cluster.TargetSelection,
		) (application.ConnectedTarget, error) {
			return connected, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Observe(context.Background(), application.ObserveRequest{
		Selection: cluster.TargetSelection{
			KubeconfigPath: "/explicit/config",
			ContextName:    "test-context",
		},
		Name: command.Name,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Snapshot == nil ||
		result.Snapshot.InstanceName != command.Name ||
		result.Snapshot.InstanceUID != created.InstanceUID ||
		result.Snapshot.TargetFingerprint != command.Target.Fingerprint ||
		result.Snapshot.Phase != "Ready" ||
		len(result.Snapshot.Pools) != 1 ||
		len(result.Snapshot.Inventory) != 1 ||
		len(result.Snapshot.Fidelity) != controlplane.MaximumStatusFidelity ||
		!result.Snapshot.FidelityTruncated ||
		len(result.Snapshot.Conditions) != controlplane.MaximumStatusConditions ||
		!result.Snapshot.ConditionsTruncated ||
		!result.Receipt.RevisionAccepted() ||
		result.Receipt.ObservedGeneration().Value() != 1 ||
		result.Connection.APIServerURL != "https://example.invalid" {
		t.Fatalf("unexpected observed result: %#v", result)
	}
}

func TestScenarioRuntimeStatusWatchReconnectsAfterNormalClosure(t *testing.T) {
	t.Parallel()

	command := revisionCommand(t, 1, "first")
	controlAdapter := memory.New(memory.Options{HistoryLimit: 8})
	created, err := controlAdapter.Submit(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	key := controlplane.InstanceKey{
		TargetFingerprint: command.Target.Fingerprint,
		Name:              command.Name,
	}
	if err := controlAdapter.CommitStatus(
		context.Background(),
		key,
		zeroGenerationForTest(t),
		controlplane.InstanceStatus{
			RevisionDigest: command.Revision.Digest,
			Phase:          "Reconciling",
		},
	); err != nil {
		t.Fatal(err)
	}
	counting := &countingControlPlane{delegate: controlAdapter}
	connected := application.ConnectedTarget{
		Receipt:      targetReceipt(command.Target),
		Target:       command.Target,
		ControlPlane: counting,
		Cluster: recording.New(recording.Options{
			Capabilities: cluster.TargetCapabilities{
				ServerVersion: "v1.36.3", KubernetesMinor: 36,
			},
		}),
	}
	runtime, err := application.NewScenarioRuntime(application.RuntimeOptions{
		Connect: func(
			context.Context,
			cluster.TargetSelection,
		) (application.ConnectedTarget, error) {
			return connected, nil
		},
		ReconnectDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	controllerErrors := make(chan error, 1)
	go func() {
		time.Sleep(20 * time.Millisecond)
		controllerErrors <- controlAdapter.CommitStatus(
			context.Background(),
			key,
			created.DesiredGeneration,
			controlplane.InstanceStatus{
				RevisionDigest: command.Revision.Digest,
				Phase:          "Ready",
			},
		)
	}()
	result, err := runtime.Observe(context.Background(), application.ObserveRequest{
		Selection: cluster.TargetSelection{
			KubeconfigPath: "/explicit/config",
			ContextName:    "test-context",
		},
		Name:    command.Name,
		Watch:   true,
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-controllerErrors; err != nil {
		t.Fatal(err)
	}
	if result.Snapshot == nil ||
		result.Snapshot.Phase != "Ready" ||
		counting.watchCalls.Load() < 2 {
		t.Fatalf(
			"status watch did not reconnect: result=%#v calls=%d",
			result,
			counting.watchCalls.Load(),
		)
	}
}

func TestScenarioRuntimeStatusWatchFollowsAConcurrentNewerRevision(t *testing.T) {
	t.Parallel()

	first := revisionCommand(t, 1, "first")
	second := revisionCommand(t, 2, "second")
	controlAdapter := memory.New(memory.Options{HistoryLimit: 8})
	created, err := controlAdapter.Submit(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	second.Preconditions = controlplane.Preconditions{
		InstanceUID:        created.InstanceUID,
		ExpectedGeneration: created.DesiredGeneration,
		ResourceVersion:    created.ResourceVersion,
	}
	connected := application.ConnectedTarget{
		Receipt:      targetReceipt(first.Target),
		Target:       first.Target,
		ControlPlane: controlAdapter,
		Cluster: recording.New(recording.Options{
			Capabilities: cluster.TargetCapabilities{
				ServerVersion: "v1.36.3", KubernetesMinor: 36,
			},
		}),
	}
	runtime, err := application.NewScenarioRuntime(application.RuntimeOptions{
		Connect: func(
			context.Context,
			cluster.TargetSelection,
		) (application.ConnectedTarget, error) {
			return connected, nil
		},
		ReconnectDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	key := controlplane.InstanceKey{
		TargetFingerprint: first.Target.Fingerprint,
		Name:              first.Name,
	}
	controllerErrors := make(chan error, 1)
	go func() {
		time.Sleep(10 * time.Millisecond)
		accepted, submitErr := controlAdapter.Submit(
			context.Background(),
			second,
		)
		if submitErr != nil {
			controllerErrors <- submitErr
			return
		}
		controllerErrors <- controlAdapter.CommitStatus(
			context.Background(),
			key,
			accepted.DesiredGeneration,
			controlplane.InstanceStatus{
				RevisionDigest: second.Revision.Digest,
				Phase:          "Ready",
			},
		)
	}()
	result, err := runtime.Observe(context.Background(), application.ObserveRequest{
		Selection: cluster.TargetSelection{
			KubeconfigPath: "/explicit/config",
			ContextName:    "test-context",
		},
		Name:    first.Name,
		Watch:   true,
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-controllerErrors; err != nil {
		t.Fatal(err)
	}
	if result.Snapshot == nil ||
		result.Snapshot.Phase != "Ready" ||
		result.Snapshot.DesiredGeneration.Value() != 2 ||
		result.Snapshot.ObservedGeneration.Value() != 2 {
		t.Fatalf("status did not follow the latest revision: %#v", result)
	}
}

func TestScenarioRuntimeStatusWatchRecoversFromExpiredCursor(t *testing.T) {
	t.Parallel()

	command := revisionCommand(t, 1, "first")
	controlAdapter := memory.New(memory.Options{HistoryLimit: 8})
	created, err := controlAdapter.Submit(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	expiring := &expiringControlPlane{delegate: controlAdapter}
	connected := application.ConnectedTarget{
		Receipt:      targetReceipt(command.Target),
		Target:       command.Target,
		ControlPlane: expiring,
		Cluster: recording.New(recording.Options{
			Capabilities: cluster.TargetCapabilities{
				ServerVersion: "v1.36.3", KubernetesMinor: 36,
			},
		}),
	}
	runtime, err := application.NewScenarioRuntime(application.RuntimeOptions{
		Connect: func(
			context.Context,
			cluster.TargetSelection,
		) (application.ConnectedTarget, error) {
			return connected, nil
		},
		ReconnectDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	key := controlplane.InstanceKey{
		TargetFingerprint: command.Target.Fingerprint,
		Name:              command.Name,
	}
	controllerErrors := make(chan error, 1)
	go func() {
		time.Sleep(10 * time.Millisecond)
		controllerErrors <- controlAdapter.CommitStatus(
			context.Background(),
			key,
			created.DesiredGeneration,
			controlplane.InstanceStatus{
				RevisionDigest: command.Revision.Digest,
				Phase:          "Ready",
			},
		)
	}()
	result, err := runtime.Observe(context.Background(), application.ObserveRequest{
		Selection: cluster.TargetSelection{
			KubeconfigPath: "/explicit/config",
			ContextName:    "test-context",
		},
		Name:    command.Name,
		Watch:   true,
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-controllerErrors; err != nil {
		t.Fatal(err)
	}
	if result.Snapshot == nil ||
		result.Snapshot.Phase != "Ready" ||
		expiring.watchCalls.Load() < 2 {
		t.Fatalf(
			"status did not recover from cursor expiry: result=%#v calls=%d",
			result,
			expiring.watchCalls.Load(),
		)
	}
}

func TestScenarioRuntimeTypedHealthAndScaleCreateCanonicalRevisions(t *testing.T) {
	t.Parallel()

	catalogSnapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	input, err := scenario.Shortcut(scenario.ShortcutInput{
		Name:                "training-lab",
		ProfileID:           "nvidia",
		ModelID:             "nvidia-h100",
		Nodes:               2,
		AcceleratorsPerNode: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, compileReceipt, err := scenario.Compile(input, catalogSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	command := compiledCommand(t, compiled, compileReceipt)
	controlAdapter := memory.New(memory.Options{HistoryLimit: 8})
	created, err := controlAdapter.Submit(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	clusterAdapter := recording.New(recording.Options{
		Capabilities: cluster.TargetCapabilities{
			ServerVersion: "v1.36.3", KubernetesMinor: 36,
		},
	})
	connected := application.ConnectedTarget{
		Receipt:      targetReceipt(command.Target),
		Target:       command.Target,
		ControlPlane: controlAdapter,
		Cluster:      clusterAdapter,
	}
	runtime, err := application.NewScenarioRuntime(application.RuntimeOptions{
		Connect: func(
			context.Context,
			cluster.TargetSelection,
		) (application.ConnectedTarget, error) {
			return connected, nil
		},
		Catalog: catalogSnapshot,
	})
	if err != nil {
		t.Fatal(err)
	}
	health, err := scenario.Health("nodes", "accelerators", 4)
	if err != nil {
		t.Fatal(err)
	}
	healthResult, err := runtime.Apply(
		context.Background(),
		application.ApplyRequest{
			Selection: cluster.TargetSelection{
				KubeconfigPath: "/explicit/config",
				ContextName:    "test-context",
			},
			TypedRevision: &application.TypedRevisionRequest{
				Name:               command.Name,
				InstanceUID:        created.InstanceUID,
				ExpectedGeneration: created.DesiredGeneration,
				Change:             health,
			},
			Async: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !healthResult.Receipt.RevisionAccepted() ||
		healthResult.Receipt.DesiredGeneration().Value() != 2 {
		t.Fatalf("health revision result: %#v", healthResult)
	}
	healthRecord, err := controlAdapter.Read(
		context.Background(),
		controlplane.InstanceKey{
			TargetFingerprint: command.Target.Fingerprint,
			Name:              command.Name,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	healthScenario, err := compileRecordScenario(
		healthRecord,
		catalogSnapshot,
	)
	if err != nil {
		t.Fatal(err)
	}
	if healthScenario.NodeGroups()[0].Replicas().Value() != 2 ||
		healthScenario.NodeGroups()[0].Pools()[0].Counts().Healthy() != 4 {
		t.Fatalf("typed health changed the wrong fields: %#v", healthScenario)
	}

	scale, err := scenario.Scale("nodes", 5)
	if err != nil {
		t.Fatal(err)
	}
	scaleResult, err := runtime.Apply(
		context.Background(),
		application.ApplyRequest{
			Selection: cluster.TargetSelection{
				KubeconfigPath: "/explicit/config",
				ContextName:    "test-context",
			},
			TypedRevision: &application.TypedRevisionRequest{
				Name:               command.Name,
				InstanceUID:        healthResult.Receipt.InstanceUID(),
				ExpectedGeneration: healthResult.Receipt.DesiredGeneration(),
				Change:             scale,
			},
			Async: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	scaleRecord, err := controlAdapter.Read(
		context.Background(),
		controlplane.InstanceKey{
			TargetFingerprint: command.Target.Fingerprint,
			Name:              command.Name,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	scaleScenario, err := compileRecordScenario(scaleRecord, catalogSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if scaleResult.Receipt.DesiredGeneration().Value() != 3 ||
		scaleScenario.NodeGroups()[0].Replicas().Value() != 5 ||
		scaleScenario.NodeGroups()[0].Pools()[0].Counts().Healthy() != 4 ||
		len(clusterAdapter.PersistentChangeSets()) != 0 {
		t.Fatalf(
			"typed scale changed the wrong fields or wrote Cluster objects: result=%#v scenario=%#v",
			scaleResult,
			scaleScenario,
		)
	}
}

func TestScenarioRuntimeTypedRevisionRebasesStatusOnlyResourceVersionDrift(
	t *testing.T,
) {
	t.Parallel()

	harness := newTypedRevisionHarness(
		t,
		func(delegate *memory.Adapter) controlplane.ScenarioControlPlane {
			return &statusDriftControlPlane{delegate: delegate}
		},
	)
	result, err := harness.applyScale(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	record := harness.read(t)
	if result.Receipt.DesiredGeneration().Value() != 2 ||
		record.DesiredGeneration.Value() != 2 ||
		len(record.Revisions) != 2 ||
		record.Revision.Digest != result.Receipt.RevisionDigest() {
		t.Fatalf("status-only drift did not accept one revision: %#v", record)
	}
}

func TestScenarioRuntimeTypedRevisionRejectsLogicalDriftWithoutAcceptance(
	t *testing.T,
) {
	t.Parallel()

	alternateUID, err := domain.ParseInstanceUID("another-instance")
	if err != nil {
		t.Fatal(err)
	}
	alternateGeneration, err := domain.NewGeneration(2)
	if err != nil {
		t.Fatal(err)
	}
	alternateFidelity, err := domain.ParseFidelityMode("dra-control-plane")
	if err != nil {
		t.Fatal(err)
	}
	tamperedRevisionDigest := digest(t, "tampered-revision")
	tamperedHistoryDigest := digest(t, "tampered-history")
	tests := []struct {
		name     string
		mutate   func(*controlplane.InstanceRecord)
		wantCode controlplane.ErrorCode
	}{
		{
			name: "UID",
			mutate: func(record *controlplane.InstanceRecord) {
				record.InstanceUID = alternateUID
			},
			wantCode: controlplane.ErrorUIDConflict,
		},
		{
			name: "generation",
			mutate: func(record *controlplane.InstanceRecord) {
				record.DesiredGeneration = alternateGeneration
			},
			wantCode: controlplane.ErrorGenerationConflict,
		},
		{
			name: "creation identity",
			mutate: func(record *controlplane.InstanceRecord) {
				record.CreationIdentity = "another-creator"
			},
			wantCode: controlplane.ErrorCreationIdentityConflict,
		},
		{
			name: "Fidelity Mode",
			mutate: func(record *controlplane.InstanceRecord) {
				record.Fidelity = alternateFidelity
			},
			wantCode: controlplane.ErrorFidelityConflict,
		},
		{
			name: "deletion",
			mutate: func(record *controlplane.InstanceRecord) {
				record.DeletionRequested = true
			},
			wantCode: controlplane.ErrorResourceVersionConflict,
		},
		{
			name: "empty resourceVersion",
			mutate: func(record *controlplane.InstanceRecord) {
				record.ResourceVersion = ""
			},
			wantCode: controlplane.ErrorResourceVersionConflict,
		},
		{
			name: "revision digest",
			mutate: func(record *controlplane.InstanceRecord) {
				record.Revision.Digest = tamperedRevisionDigest
			},
			wantCode: controlplane.ErrorResourceVersionConflict,
		},
		{
			name: "canonical Scenario",
			mutate: func(record *controlplane.InstanceRecord) {
				record.Revision.CanonicalScenario = []byte(`{"tampered":true}`)
			},
			wantCode: controlplane.ErrorResourceVersionConflict,
		},
		{
			name: "revision history",
			mutate: func(record *controlplane.InstanceRecord) {
				changed := controlplane.CloneRevision(record.Revision)
				changed.Digest = tamperedHistoryDigest
				record.Revisions = append(record.Revisions, changed)
			},
			wantCode: controlplane.ErrorResourceVersionConflict,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			harness := newTypedRevisionHarness(
				t,
				func(delegate *memory.Adapter) controlplane.ScenarioControlPlane {
					return &logicalDriftControlPlane{
						ScenarioControlPlane: delegate,
						mutate:               test.mutate,
					}
				},
			)
			_, err := harness.applyScale(context.Background())
			if controlplane.ErrorCodeOf(err) != test.wantCode {
				t.Fatalf("logical drift error = %v, want %s", err, test.wantCode)
			}
			harness.assertOriginalRevision(t)
		})
	}
}

func TestScenarioRuntimeTypedRevisionExhaustsConflictsWithoutAcceptance(
	t *testing.T,
) {
	t.Parallel()

	harness := newTypedRevisionHarness(
		t,
		func(delegate *memory.Adapter) controlplane.ScenarioControlPlane {
			return &logicalDriftControlPlane{
				ScenarioControlPlane: delegate,
				alwaysConflict:       true,
			}
		},
	)
	if _, err := harness.applyScale(
		context.Background(),
	); controlplane.ErrorCodeOf(err) !=
		controlplane.ErrorResourceVersionConflict {
		t.Fatalf("retry exhaustion error = %v", err)
	}
	harness.assertOriginalRevision(t)
}

type typedRevisionHarness struct {
	runtime  *application.ScenarioRuntime
	command  controlplane.RevisionCommand
	created  controlplane.SubmissionReceipt
	delegate *memory.Adapter
	scale    scenario.TypedRevisionChange
}

func newTypedRevisionHarness(
	t *testing.T,
	wrap func(*memory.Adapter) controlplane.ScenarioControlPlane,
) typedRevisionHarness {
	t.Helper()

	catalogSnapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	input, err := scenario.Shortcut(scenario.ShortcutInput{
		Name:                "training-lab",
		ProfileID:           "nvidia",
		ModelID:             "nvidia-h100",
		Nodes:               2,
		AcceleratorsPerNode: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, compileReceipt, err := scenario.Compile(input, catalogSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	command := compiledCommand(t, compiled, compileReceipt)
	delegate := memory.New(memory.Options{HistoryLimit: 8})
	created, err := delegate.Submit(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	controlPlane := controlplane.ScenarioControlPlane(delegate)
	if wrap != nil {
		controlPlane = wrap(delegate)
	}
	connected := application.ConnectedTarget{
		Receipt:      targetReceipt(command.Target),
		Target:       command.Target,
		ControlPlane: controlPlane,
		Cluster: recording.New(recording.Options{
			Capabilities: cluster.TargetCapabilities{
				ServerVersion: "v1.36.3", KubernetesMinor: 36,
			},
		}),
	}
	runtime, err := application.NewScenarioRuntime(application.RuntimeOptions{
		Connect: func(
			context.Context,
			cluster.TargetSelection,
		) (application.ConnectedTarget, error) {
			return connected, nil
		},
		Catalog: catalogSnapshot,
	})
	if err != nil {
		t.Fatal(err)
	}
	scale, err := scenario.Scale("nodes", 5)
	if err != nil {
		t.Fatal(err)
	}
	return typedRevisionHarness{
		runtime:  runtime,
		command:  command,
		created:  created,
		delegate: delegate,
		scale:    scale,
	}
}

func (harness typedRevisionHarness) applyScale(
	ctx context.Context,
) (application.LifecycleResult, error) {
	return harness.runtime.Apply(ctx, application.ApplyRequest{
		Selection: cluster.TargetSelection{
			KubeconfigPath: "/explicit/config",
			ContextName:    "test-context",
		},
		TypedRevision: &application.TypedRevisionRequest{
			Name:               harness.command.Name,
			InstanceUID:        harness.created.InstanceUID,
			ExpectedGeneration: harness.created.DesiredGeneration,
			Change:             harness.scale,
		},
		Async: true,
	})
}

func (harness typedRevisionHarness) read(
	t *testing.T,
) controlplane.InstanceRecord {
	t.Helper()
	record, err := harness.delegate.Read(
		context.Background(),
		controlplane.InstanceKey{
			TargetFingerprint: harness.command.Target.Fingerprint,
			Name:              harness.command.Name,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func (harness typedRevisionHarness) assertOriginalRevision(t *testing.T) {
	t.Helper()
	record := harness.read(t)
	if record.DesiredGeneration != harness.created.DesiredGeneration ||
		record.Revision.Digest != harness.command.Revision.Digest ||
		len(record.Revisions) != 1 {
		t.Fatalf("failed retry accepted or changed a revision: %#v", record)
	}
}

func compiledCommand(
	t *testing.T,
	compiled scenario.CanonicalScenario,
	receipt scenario.CompileReceipt,
) controlplane.RevisionCommand {
	t.Helper()
	base := revisionCommand(t, 1, "compiled")
	base.Name = compiled.Scenario().Name()
	base.Fidelity = compiled.Scenario().Fidelity()
	base.Revision.Digest = compiled.Digest()
	base.Revision.CanonicalScenario = compiled.Bytes()
	base.Revision.Profiles = nil
	seen := make(map[string]struct{})
	resolutionIndex := 0
	for _, group := range compiled.Scenario().NodeGroups() {
		for _, pool := range group.Pools() {
			resolution := receipt.Resolutions()[resolutionIndex]
			resolutionIndex++
			if _, found := seen[pool.Profile().ID().String()]; found {
				continue
			}
			seen[pool.Profile().ID().String()] = struct{}{}
			base.Revision.Profiles = append(
				base.Revision.Profiles,
				controlplane.ProfileReceipt{
					ID:       pool.Profile().ID().String(),
					Revision: pool.Profile().Revision(),
					Digest:   resolution.ProfileDigest(),
					Class:    resolution.ProfileClass(),
				},
			)
		}
	}
	return base
}

func compileRecordScenario(
	record controlplane.InstanceRecord,
	snapshot catalog.Snapshot,
) (domain.Scenario, error) {
	input, err := scenario.Document(record.Revision.CanonicalScenario)
	if err != nil {
		return domain.Scenario{}, err
	}
	compiled, _, err := scenario.Compile(input, snapshot)
	if err != nil {
		return domain.Scenario{}, err
	}
	return compiled.Scenario(), nil
}

type countingControlPlane struct {
	delegate   controlplane.ScenarioControlPlane
	watchCalls atomic.Int32
}

type expiringControlPlane struct {
	delegate   controlplane.ScenarioControlPlane
	watchCalls atomic.Int32
}

type statusDriftControlPlane struct {
	delegate *memory.Adapter
	drifted  atomic.Bool
}

type logicalDriftControlPlane struct {
	controlplane.ScenarioControlPlane
	mutate         func(*controlplane.InstanceRecord)
	alwaysConflict bool
	conflicted     atomic.Bool
}

func (adapter *statusDriftControlPlane) Probe(
	ctx context.Context,
	target controlplane.ExplicitTarget,
) (controlplane.TargetCapabilities, error) {
	return adapter.delegate.Probe(ctx, target)
}

func (adapter *statusDriftControlPlane) Read(
	ctx context.Context,
	key controlplane.InstanceKey,
) (controlplane.InstanceRecord, error) {
	record, err := adapter.delegate.Read(ctx, key)
	if err != nil || !adapter.drifted.CompareAndSwap(false, true) {
		return record, err
	}
	if err := adapter.delegate.CommitStatus(
		ctx,
		key,
		record.DesiredGeneration,
		controlplane.InstanceStatus{
			RevisionDigest: record.Revision.Digest,
			Phase:          "Ready",
		},
	); err != nil {
		return controlplane.InstanceRecord{}, err
	}
	return record, nil
}

func (adapter *statusDriftControlPlane) Submit(
	ctx context.Context,
	command controlplane.RevisionCommand,
) (controlplane.SubmissionReceipt, error) {
	return adapter.delegate.Submit(ctx, command)
}

func (adapter *statusDriftControlPlane) Delete(
	ctx context.Context,
	command controlplane.DeletionCommand,
) (controlplane.DeletionReceipt, error) {
	return adapter.delegate.Delete(ctx, command)
}

func (adapter *statusDriftControlPlane) Watch(
	ctx context.Context,
	cursor controlplane.WatchCursor,
) (controlplane.InstanceEventStream, error) {
	return adapter.delegate.Watch(ctx, cursor)
}

func (adapter *logicalDriftControlPlane) Read(
	ctx context.Context,
	key controlplane.InstanceKey,
) (controlplane.InstanceRecord, error) {
	record, err := adapter.ScenarioControlPlane.Read(ctx, key)
	if err != nil || !adapter.conflicted.Load() {
		return record, err
	}
	if adapter.mutate != nil {
		adapter.mutate(&record)
	}
	if record.ResourceVersion != "" {
		record.ResourceVersion = "scripted-logical-drift"
	}
	return record, nil
}

func (adapter *logicalDriftControlPlane) Submit(
	ctx context.Context,
	command controlplane.RevisionCommand,
) (controlplane.SubmissionReceipt, error) {
	if adapter.alwaysConflict ||
		adapter.conflicted.CompareAndSwap(false, true) {
		adapter.conflicted.Store(true)
		return controlplane.SubmissionReceipt{}, controlplane.NewError(
			controlplane.ErrorResourceVersionConflict,
			"scripted revision conflict",
			"",
		)
	}
	return adapter.ScenarioControlPlane.Submit(ctx, command)
}

func (adapter *expiringControlPlane) Probe(
	ctx context.Context,
	target controlplane.ExplicitTarget,
) (controlplane.TargetCapabilities, error) {
	return adapter.delegate.Probe(ctx, target)
}

func (adapter *expiringControlPlane) Read(
	ctx context.Context,
	key controlplane.InstanceKey,
) (controlplane.InstanceRecord, error) {
	return adapter.delegate.Read(ctx, key)
}

func (adapter *expiringControlPlane) Submit(
	ctx context.Context,
	command controlplane.RevisionCommand,
) (controlplane.SubmissionReceipt, error) {
	return adapter.delegate.Submit(ctx, command)
}

func (adapter *expiringControlPlane) Delete(
	ctx context.Context,
	command controlplane.DeletionCommand,
) (controlplane.DeletionReceipt, error) {
	return adapter.delegate.Delete(ctx, command)
}

func (adapter *expiringControlPlane) Watch(
	ctx context.Context,
	cursor controlplane.WatchCursor,
) (controlplane.InstanceEventStream, error) {
	if adapter.watchCalls.Add(1) == 1 {
		return nil, controlplane.NewError(
			controlplane.ErrorCursorExpired,
			"watch cursor expired",
			cursor.AfterResourceVersion,
		)
	}
	return adapter.delegate.Watch(ctx, cursor)
}

func (adapter *countingControlPlane) Probe(
	ctx context.Context,
	target controlplane.ExplicitTarget,
) (controlplane.TargetCapabilities, error) {
	return adapter.delegate.Probe(ctx, target)
}

func (adapter *countingControlPlane) Read(
	ctx context.Context,
	key controlplane.InstanceKey,
) (controlplane.InstanceRecord, error) {
	return adapter.delegate.Read(ctx, key)
}

func (adapter *countingControlPlane) Submit(
	ctx context.Context,
	command controlplane.RevisionCommand,
) (controlplane.SubmissionReceipt, error) {
	return adapter.delegate.Submit(ctx, command)
}

func (adapter *countingControlPlane) Delete(
	ctx context.Context,
	command controlplane.DeletionCommand,
) (controlplane.DeletionReceipt, error) {
	return adapter.delegate.Delete(ctx, command)
}

func (adapter *countingControlPlane) Watch(
	ctx context.Context,
	cursor controlplane.WatchCursor,
) (controlplane.InstanceEventStream, error) {
	adapter.watchCalls.Add(1)
	return adapter.delegate.Watch(ctx, cursor)
}

func zeroGenerationForTest(t *testing.T) domain.Generation {
	t.Helper()
	generation, err := domain.NewGeneration(0)
	if err != nil {
		t.Fatal(err)
	}
	return generation
}
