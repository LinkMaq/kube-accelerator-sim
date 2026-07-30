package cluster_test

import (
	"testing"

	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

func TestAccessRequirementRejectsBroadOrAmbiguousAuthorizationChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		requirement cluster.AccessRequirement
	}{
		{
			name: "wildcard verb",
			requirement: cluster.AccessRequirement{
				Verb:     "*",
				Group:    "simulation.kasim.io",
				Resource: "scenarioinstances",
			},
		},
		{
			name: "wildcard resource",
			requirement: cluster.AccessRequirement{
				Verb:     "get",
				Group:    "simulation.kasim.io",
				Resource: "*",
			},
		},
		{
			name: "embedded subresource",
			requirement: cluster.AccessRequirement{
				Verb:     "update",
				Group:    "simulation.kasim.io",
				Resource: "scenarioinstances/status",
			},
		},
		{
			name: "namespaced object without namespace",
			requirement: cluster.AccessRequirement{
				Verb:       "get",
				Group:      "coordination.k8s.io",
				Resource:   "leases",
				Namespaced: true,
				Name:       "node-lease",
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.requirement.Validate(); err == nil {
				t.Fatal("broad authorization requirement unexpectedly passed")
			}
		})
	}

	exact := cluster.AccessRequirement{
		Verb:        "update",
		Group:       "simulation.kasim.io",
		Resource:    "scenarioinstances",
		Subresource: "status",
		Name:        "training-lab",
	}
	if err := exact.Validate(); err != nil {
		t.Fatalf("exact authorization requirement failed: %v", err)
	}
}

func TestOwnershipScopeRequiresExactInstanceUIDAndGeneration(t *testing.T) {
	t.Parallel()

	uid, err := domain.ParseInstanceUID("6cb2dd6f-c608-4e79-aaf6-e3fa1287f73c")
	if err != nil {
		t.Fatal(err)
	}
	generation, err := domain.NewGeneration(7)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := cluster.NewOwnershipScope(uid, generation)
	if err != nil {
		t.Fatal(err)
	}
	if scope.InstanceUID() != uid ||
		scope.DesiredGeneration() != generation ||
		scope.ManagedBy() != cluster.ManagedByValue {
		t.Fatalf("ownership scope lost exact identity: %#v", scope)
	}

	zero, err := domain.NewGeneration(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cluster.NewOwnershipScope(uid, zero); err == nil {
		t.Fatal("zero-generation ownership scope unexpectedly succeeded")
	}
}

func TestOwnedChangeSetRequiresAllowlistedKindAndExactDeletePreconditions(t *testing.T) {
	t.Parallel()

	uid, err := domain.ParseInstanceUID("6cb2dd6f-c608-4e79-aaf6-e3fa1287f73c")
	if err != nil {
		t.Fatal(err)
	}
	generation, err := domain.NewGeneration(7)
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
	if _, err := cluster.NewDeleteOwnedObject(
		key,
		cluster.ObjectPreconditions{},
	); err == nil {
		t.Fatal("delete without UID/resourceVersion preconditions unexpectedly succeeded")
	}
	deletion, err := cluster.NewDeleteOwnedObject(
		key,
		cluster.ObjectPreconditions{
			UID:             "node-uid",
			ResourceVersion: "42",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	changeSet, err := cluster.NewOwnedChangeSet(
		scope,
		cluster.ExecutionPersistent,
		[]cluster.OwnedChange{deletion},
	)
	if err != nil {
		t.Fatal(err)
	}
	if changeSet.Scope().InstanceUID() != uid ||
		changeSet.Mode() != cluster.ExecutionPersistent ||
		len(changeSet.Changes()) != 1 {
		t.Fatalf("change set lost validated intent: %#v", changeSet)
	}

	if _, err := cluster.NewObjectKey(cluster.ObjectKind("Secret"), "", "secret"); err == nil {
		t.Fatal("unsupported object kind unexpectedly entered the Cluster port")
	}
}
