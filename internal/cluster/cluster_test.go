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
		{
			name: "all namespaces with exact name",
			requirement: cluster.AccessRequirement{
				Verb:          "get",
				Group:         "resource.k8s.io",
				Resource:      "resourceclaims",
				Namespaced:    true,
				AllNamespaces: true,
				Name:          "ambiguous-namespace",
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
	allNamespaces := cluster.AccessRequirement{
		Verb:          "list",
		Group:         "resource.k8s.io",
		Resource:      "resourceclaims",
		Namespaced:    true,
		AllNamespaces: true,
	}
	if err := allNamespaces.Validate(); err != nil {
		t.Fatalf("all-namespace list authorization failed: %v", err)
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

func TestStableDRAChangesAreTypedAndResourceClaimsRemainReadOnly(t *testing.T) {
	t.Parallel()

	classKey, err := cluster.NewObjectKey(
		cluster.ObjectKindDeviceClass,
		"",
		"kasim-class-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	classChange, err := cluster.NewApplyDeviceClass(
		classKey,
		cluster.ObjectPreconditions{},
		cluster.DeviceClassInput{
			Selectors: []string{`device.driver == "gpu.nvidia.com"`},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	typedClass := classChange.(cluster.ApplyDeviceClass)
	if typedClass.Kind() != cluster.ChangeApplyDeviceClass ||
		typedClass.Selectors()[0] != `device.driver == "gpu.nvidia.com"` {
		t.Fatalf("DeviceClass change lost intent: %#v", typedClass)
	}

	sliceKey, err := cluster.NewObjectKey(
		cluster.ObjectKindResourceSlice,
		"",
		"kasim-slice-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	attributes := map[string]cluster.DeviceAttributeValue{
		"simulation.kasim.io/simulated": cluster.NewBoolDeviceAttribute(true),
	}
	sliceChange, err := cluster.NewApplyResourceSlice(
		sliceKey,
		cluster.ObjectPreconditions{},
		cluster.ResourceSliceInput{
			Driver:             "gpu.nvidia.com",
			PoolName:           "kasim-pool-a",
			PoolGeneration:     1,
			ResourceSliceCount: 1,
			NodeName:           "kasim-node-a",
			Devices: []cluster.DRADevice{{
				Name:       "kasim-device-a",
				Attributes: attributes,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	attributes["simulation.kasim.io/simulated"] =
		cluster.NewBoolDeviceAttribute(false)
	typedSlice := sliceChange.(cluster.ApplyResourceSlice)
	if typedSlice.Kind() != cluster.ChangeApplyResourceSlice ||
		!typedSlice.Devices()[0].
			Attributes["simulation.kasim.io/simulated"].
			Bool() {
		t.Fatalf("ResourceSlice change was not immutable: %#v", typedSlice)
	}

	if _, err := cluster.NewObjectKey(
		cluster.ObjectKind("ResourceClaim"),
		"team-a",
		"claim-a",
	); err == nil {
		t.Fatal("ResourceClaim unexpectedly became a mutable owned object kind")
	}
}
