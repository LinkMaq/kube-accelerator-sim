package recording_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster/recording"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

func TestAdapterRecordsPortOrderAndNeverPersistsServerDryRun(t *testing.T) {
	t.Parallel()

	adapter := recording.New(recording.Options{
		Capabilities: cluster.TargetCapabilities{
			ServerVersion:   "v1.36.3",
			KubernetesMinor: 36,
		},
	})
	requirement := cluster.AccessRequirement{
		Verb:     "get",
		Group:    "simulation.kasim.io",
		Resource: "scenarioinstances",
		Name:     "training-lab",
	}
	if _, err := adapter.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	report, err := adapter.Authorize(
		context.Background(),
		[]cluster.AccessRequirement{requirement},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Decisions) != 1 || !report.Decisions[0].Allowed {
		t.Fatalf("unexpected authorization report: %#v", report)
	}

	scope, deletion := ownedDeletion(t)
	if _, err := adapter.Observe(context.Background(), scope); err != nil {
		t.Fatal(err)
	}
	dryRun, err := cluster.NewOwnedChangeSet(
		scope,
		cluster.ExecutionServerDryRun,
		[]cluster.OwnedChange{deletion},
	)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := adapter.Execute(context.Background(), dryRun)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.DryRun || receipt.Persisted != 0 ||
		len(adapter.PersistentChangeSets()) != 0 {
		t.Fatalf("server dry-run persisted state: %#v", receipt)
	}
	if got, want := adapter.Calls(), []recording.Call{
		recording.CallDiscover,
		recording.CallAuthorize,
		recording.CallObserve,
		recording.CallExecuteDryRun,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %#v, want %#v", got, want)
	}

	persistent, err := cluster.NewOwnedChangeSet(
		scope,
		cluster.ExecutionPersistent,
		[]cluster.OwnedChange{deletion},
	)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err = adapter.Execute(context.Background(), persistent)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.DryRun || receipt.Persisted != 1 ||
		len(adapter.PersistentChangeSets()) != 1 {
		t.Fatalf("persistent execution was not recorded: %#v", receipt)
	}
}

func ownedDeletion(
	t *testing.T,
) (cluster.OwnershipScope, cluster.OwnedChange) {
	t.Helper()
	uid, err := domain.ParseInstanceUID("6cb2dd6f-c608-4e79-aaf6-e3fa1287f73c")
	if err != nil {
		t.Fatal(err)
	}
	generation, err := domain.NewGeneration(2)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := cluster.NewOwnershipScope(uid, generation)
	if err != nil {
		t.Fatal(err)
	}
	key, err := cluster.NewObjectKey(cluster.ObjectKindNode, "", "synthetic-node")
	if err != nil {
		t.Fatal(err)
	}
	deletion, err := cluster.NewDeleteOwnedObject(
		key,
		cluster.ObjectPreconditions{
			UID:             "node-uid",
			ResourceVersion: "9",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return scope, deletion
}
