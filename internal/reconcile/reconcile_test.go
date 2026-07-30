package reconcile_test

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LinkMaq/kube-accelerator-sim/internal/catalog"
	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster/recording"
	"github.com/LinkMaq/kube-accelerator-sim/internal/controlplane"
	"github.com/LinkMaq/kube-accelerator-sim/internal/controlplane/memory"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
	"github.com/LinkMaq/kube-accelerator-sim/internal/projection"
	draprojection "github.com/LinkMaq/kube-accelerator-sim/internal/projection/dra"
	"github.com/LinkMaq/kube-accelerator-sim/internal/projection/extended"
	"github.com/LinkMaq/kube-accelerator-sim/internal/reconcile"
	"github.com/LinkMaq/kube-accelerator-sim/internal/scenario"
)

var fixedTime = time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)

func TestReconcileCreatesOwnedLeaseThenClosedSyntheticNode(t *testing.T) {
	t.Parallel()

	fixture := newFixture(t, recording.Options{
		Capabilities: schedulingCapabilities(),
	})
	result, err := fixture.reconciler.Reconcile(
		context.Background(),
		fixture.key,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Requeue() || result.Phase() != "Reconciling" {
		t.Fatalf("first result = requeue %t, phase %q", result.Requeue(), result.Phase())
	}
	changeSets := fixture.cluster.PersistentChangeSets()
	if len(changeSets) != 1 {
		t.Fatalf("persistent change sets = %d, want 1", len(changeSets))
	}
	changes := changeSets[0].Changes()
	if len(changes) != 2 {
		t.Fatalf("first change set = %d changes, want Lease and Node", len(changes))
	}
	lease, ok := changes[0].(cluster.ApplyLease)
	if !ok {
		t.Fatalf("first change = %T, want ApplyLease", changes[0])
	}
	if lease.Key().Namespace() != "kube-node-lease" ||
		lease.HolderIdentity() != lease.Key().Name() ||
		lease.LeaseDurationSeconds() != 40 {
		t.Fatalf("unexpected Lease intent: %#v", lease)
	}
	node, ok := changes[1].(cluster.ApplySyntheticNode)
	if !ok {
		t.Fatalf("second change = %T, want ApplySyntheticNode", changes[1])
	}
	if !node.Unschedulable() ||
		node.Key().Name() != lease.Key().Name() ||
		node.Annotations()["kwok.x-k8s.io/node"] != "fake" ||
		node.Labels()["simulation.kasim.io/node-group"] != "nodes" {
		t.Fatalf("unexpected closed Synthetic Node intent: %#v", node)
	}
	for _, change := range changes {
		if change.Key().Kind() != cluster.ObjectKindLease &&
			change.Key().Kind() != cluster.ObjectKindNode {
			t.Fatalf("unexpected object mutation %s", change.Key().Kind())
		}
	}
	if len(fixture.commits) != 1 ||
		fixture.commits[0].ObservedGeneration.Value() != 0 ||
		fixture.commits[0].Status.Phase != "Reconciling" ||
		fixture.commits[0].Finalization != reconcile.FinalizationEnsure {
		t.Fatalf("unexpected committed status intent: %#v", fixture.commits)
	}
}

func TestReconcileProjectsResourcesWithoutOwningKWOKReadyCondition(t *testing.T) {
	t.Parallel()

	nodeName := syntheticNodeName(t)
	generation, err := domain.NewGeneration(1)
	if err != nil {
		t.Fatal(err)
	}
	nodeKey, err := cluster.NewObjectKey(cluster.ObjectKindNode, "", nodeName)
	if err != nil {
		t.Fatal(err)
	}
	leaseKey, err := cluster.NewObjectKey(
		cluster.ObjectKindLease,
		"kube-node-lease",
		nodeName,
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture := newFixture(t, recording.Options{
		Capabilities: schedulingCapabilities(),
		Observed: cluster.ObservedGraph{Objects: []cluster.ObservedObject{
			{
				Key:               leaseKey,
				UID:               "lease-uid",
				ResourceVersion:   "10",
				DesiredGeneration: generation,
				Lease: &cluster.ObservedLeaseState{
					HolderIdentity:       nodeName,
					LeaseDurationSeconds: 40,
					RenewTime:            fixedTime,
				},
			},
			{
				Key:               nodeKey,
				UID:               "node-uid",
				ResourceVersion:   "11",
				DesiredGeneration: generation,
				Node: &cluster.ObservedNodeState{
					Labels: map[string]string{
						"kubernetes.io/hostname":            nodeName,
						"simulation.kasim.io/scenario":      "training-lab",
						"simulation.kasim.io/node-group":    "nodes",
						"simulation.kasim.io/replica-index": "0",
						cluster.ManagedByLabel:              cluster.ManagedByValue,
						cluster.InstanceUIDLabel:            "memory-1",
						cluster.DesiredGenerationLabel:      "1",
					},
					Annotations:   map[string]string{"kwok.x-k8s.io/node": "fake"},
					Unschedulable: true,
					Ready:         true,
				},
			},
		}},
	})
	_, err = fixture.reconciler.Reconcile(context.Background(), fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	changeSets := fixture.cluster.PersistentChangeSets()
	if len(changeSets) != 1 || len(changeSets[0].Changes()) != 1 {
		t.Fatalf("unexpected status stage changes: %#v", changeSets)
	}
	status, ok := changeSets[0].Changes()[0].(cluster.UpdateSyntheticNodeStatus)
	if !ok {
		t.Fatalf("change = %T, want UpdateSyntheticNodeStatus", changeSets[0].Changes()[0])
	}
	if status.Capacity()["nvidia.com/gpu"] != "8" ||
		status.Allocatable()["nvidia.com/gpu"] != "8" ||
		status.ManagesReady() {
		t.Fatalf("unexpected resource-only status intent: %#v", status)
	}
}

func TestReconcileOpensSchedulingOnlyAfterEverySurfaceIsObserved(t *testing.T) {
	t.Parallel()

	fixture := newFixture(t, recording.Options{
		Capabilities: schedulingCapabilities(),
		Observed:     completeObservedGraph(t, true, nil),
	})
	result, err := fixture.reconciler.Reconcile(context.Background(), fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Requeue() || result.Phase() != "Reconciling" {
		t.Fatalf("open result = requeue %t phase %q", result.Requeue(), result.Phase())
	}
	changeSets := fixture.cluster.PersistentChangeSets()
	if len(changeSets) != 1 || len(changeSets[0].Changes()) != 1 {
		t.Fatalf("unexpected open stage: %#v", changeSets)
	}
	node, ok := changeSets[0].Changes()[0].(cluster.ApplySyntheticNode)
	if !ok || node.Unschedulable() {
		t.Fatalf("open change = %#v, want schedulable ApplySyntheticNode", changeSets[0].Changes()[0])
	}
}

func TestReconcileReportsReadyOnlyAfterOpenedObservedFidelity(t *testing.T) {
	t.Parallel()

	fixture := newFixture(t, recording.Options{
		Capabilities: schedulingCapabilities(),
		Observed:     completeObservedGraph(t, false, nil),
	})
	result, err := fixture.reconciler.Reconcile(context.Background(), fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if result.Requeue() || result.Phase() != "Ready" {
		t.Fatalf("ready result = requeue %t phase %q", result.Requeue(), result.Phase())
	}
	if len(fixture.cluster.PersistentChangeSets()) != 0 {
		t.Fatal("Ready reconciliation unexpectedly mutated the cluster")
	}
	if len(fixture.commits) != 1 ||
		fixture.commits[0].ObservedGeneration.Value() != 1 ||
		fixture.commits[0].Status.Phase != "Ready" {
		t.Fatalf("unexpected Ready status intent: %#v", fixture.commits)
	}
	foundFidelity := false
	for _, condition := range fixture.commits[0].Status.Conditions {
		if condition.Type == "FidelitySatisfied" && condition.Status == "True" {
			foundFidelity = true
		}
	}
	if !foundFidelity {
		t.Fatalf("Ready status has no FidelitySatisfied condition: %#v", fixture.commits[0])
	}
}

func TestDRAReconcileConvergesClassThenCompleteInventoryBeforeOpening(
	t *testing.T,
) {
	t.Parallel()

	base := completeDRAObservedGraph(t, true)
	classFixture := newDRAFixture(t, recording.Options{
		Capabilities: draCapabilities(),
		Observed:     base,
	}, 2, 1, 1)
	result, err := classFixture.reconciler.Reconcile(
		context.Background(),
		classFixture.key,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Requeue() || len(classFixture.cluster.PersistentChangeSets()) != 1 {
		t.Fatalf("DeviceClass stage result = %#v", result)
	}
	classChanges := classFixture.cluster.PersistentChangeSets()[0].Changes()
	if len(classChanges) != 1 {
		t.Fatalf("DeviceClass stage changes = %#v", classChanges)
	}
	classChange, ok := classChanges[0].(cluster.ApplyDeviceClass)
	if !ok {
		t.Fatalf("first DRA change = %T, want ApplyDeviceClass", classChanges[0])
	}

	withClass := cloneObservedGraph(base)
	withClass.Objects = append(
		withClass.Objects,
		observedDeviceClass(t, classChange),
	)
	sliceFixture := newDRAFixture(t, recording.Options{
		Capabilities: draCapabilities(),
		Observed:     withClass,
	}, 2, 1, 1)
	result, err = sliceFixture.reconciler.Reconcile(
		context.Background(),
		sliceFixture.key,
	)
	if err != nil {
		t.Fatal(err)
	}
	sliceChanges := sliceFixture.cluster.PersistentChangeSets()[0].Changes()
	if len(sliceChanges) != 1 {
		t.Fatalf("ResourceSlice stage changes = %#v", sliceChanges)
	}
	sliceChange, ok := sliceChanges[0].(cluster.ApplyResourceSlice)
	if !ok {
		t.Fatalf("second DRA change = %T, want ApplyResourceSlice", sliceChanges[0])
	}
	if len(sliceChange.Devices()) != 2 ||
		sliceChange.Devices()[0].Name == sliceChange.Devices()[1].Name {
		t.Fatalf("ResourceSlice lost deterministic devices: %#v", sliceChange)
	}

	completeClosed := cloneObservedGraph(withClass)
	completeClosed.Objects = append(
		completeClosed.Objects,
		observedResourceSlice(t, sliceChange),
	)
	openFixture := newDRAFixture(t, recording.Options{
		Capabilities: draCapabilities(),
		Observed:     completeClosed,
	}, 2, 1, 1)
	result, err = openFixture.reconciler.Reconcile(
		context.Background(),
		openFixture.key,
	)
	if err != nil {
		t.Fatal(err)
	}
	openChanges := openFixture.cluster.PersistentChangeSets()[0].Changes()
	openNode, ok := openChanges[0].(cluster.ApplySyntheticNode)
	if len(openChanges) != 1 || !ok || openNode.Unschedulable() {
		t.Fatalf("complete DRA inventory did not open scheduling: %#v", openChanges)
	}

	completeOpen := cloneObservedGraph(completeClosed)
	for index := range completeOpen.Objects {
		if completeOpen.Objects[index].Node != nil {
			completeOpen.Objects[index].Node.Unschedulable = false
		}
	}
	readyFixture := newDRAFixture(t, recording.Options{
		Capabilities: draCapabilities(),
		Observed:     completeOpen,
	}, 2, 1, 1)
	result, err = readyFixture.reconciler.Reconcile(
		context.Background(),
		readyFixture.key,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Requeue() || result.Phase() != "Ready" ||
		len(readyFixture.cluster.PersistentChangeSets()) != 0 {
		t.Fatalf("complete stable DRA projection was not Ready: %#v", result)
	}
	if len(readyFixture.commits) != 1 ||
		readyFixture.commits[0].Status.Pools[0].ObservedTotal != 2 ||
		readyFixture.commits[0].Status.Pools[0].ObservedHealthy != 1 {
		t.Fatalf("DRA status did not count observed devices: %#v", readyFixture.commits)
	}
	for surface, state := range map[string]string{
		"device-class":               "achieved",
		"resource-slice-inventory":   "achieved",
		"resource-claim-allocation":  "out-of-scope",
		"resource-claim-reservation": "out-of-scope",
		"pod-scheduling":             "out-of-scope",
		"node-prepare-resources":     "excluded",
		"cdi":                        "out-of-scope",
		"device-access":              "out-of-scope",
		"accelerator-compute":        "out-of-scope",
	} {
		if !slices.ContainsFunc(
			readyFixture.commits[0].Status.Fidelity,
			func(value controlplane.FidelitySurfaceStatus) bool {
				return value.Surface == surface && value.State == state
			},
		) {
			t.Fatalf(
				"Ready status omitted fidelity surface %s=%s: %#v",
				surface,
				state,
				readyFixture.commits[0].Status.Fidelity,
			)
		}
	}

	selectedWorkload := cloneObservedGraph(completeOpen)
	selectedWorkload.ResourceClaims = []cluster.ObservedResourceClaim{{
		Namespace:        "team-a",
		Name:             "probe-claim",
		UID:              "claim-uid",
		ResourceVersion:  "31",
		DeviceClassNames: []string{classChange.Key().Name()},
		Allocations: []cluster.DRAAllocationResult{{
			Request: "accelerator",
			Driver:  sliceChange.Driver(),
			Pool:    sliceChange.PoolName(),
			Device:  sliceChange.Devices()[0].Name,
		}},
		ReservedFor: []cluster.DRAConsumerReference{{
			Resource: "pods",
			Name:     "probe-pod",
			UID:      "probe-pod-uid",
		}},
	}}
	selectedWorkload.Pods = []cluster.ObservedPod{{
		Namespace:      "team-a",
		Name:           "probe-pod",
		UID:            "probe-pod-uid",
		NodeName:       sliceChange.NodeName(),
		Phase:          "Running",
		ResourceClaims: []string{"probe-claim"},
	}}
	workloadFixture := newDRAFixture(t, recording.Options{
		Capabilities: draCapabilities(),
		Observed:     selectedWorkload,
	}, 2, 1, 1)
	result, err = workloadFixture.reconciler.Reconcile(
		context.Background(),
		workloadFixture.key,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Requeue() || result.Phase() != "Ready" {
		t.Fatalf("exact scheduler-owned DRA evidence was not Ready: %#v", result)
	}
	for _, surface := range []string{
		"resource-claim-allocation",
		"resource-claim-reservation",
		"pod-scheduling",
	} {
		if !slices.ContainsFunc(
			workloadFixture.commits[0].Status.Fidelity,
			func(value controlplane.FidelitySurfaceStatus) bool {
				return value.Surface == surface && value.State == "achieved"
			},
		) {
			t.Fatalf(
				"selected DRA workload omitted %s=achieved: %#v",
				surface,
				workloadFixture.commits[0].Status.Fidelity,
			)
		}
	}
}

func TestDRADeletionBlocksOnExternalClaimThenDeletesSlicesBeforeClasses(
	t *testing.T,
) {
	t.Parallel()

	base := completeDRAObservedGraph(t, true)
	classFixture := newDRAFixture(t, recording.Options{
		Capabilities: draCapabilities(),
		Observed:     base,
	}, 1, 1, 1)
	_, err := classFixture.reconciler.Reconcile(context.Background(), classFixture.key)
	if err != nil {
		t.Fatal(err)
	}
	classChange := classFixture.cluster.PersistentChangeSets()[0].
		Changes()[0].(cluster.ApplyDeviceClass)
	withClass := cloneObservedGraph(base)
	withClass.Objects = append(withClass.Objects, observedDeviceClass(t, classChange))
	sliceFixture := newDRAFixture(t, recording.Options{
		Capabilities: draCapabilities(),
		Observed:     withClass,
	}, 1, 1, 1)
	_, err = sliceFixture.reconciler.Reconcile(context.Background(), sliceFixture.key)
	if err != nil {
		t.Fatal(err)
	}
	sliceChange := sliceFixture.cluster.PersistentChangeSets()[0].
		Changes()[0].(cluster.ApplyResourceSlice)
	complete := cloneObservedGraph(withClass)
	complete.Objects = append(
		complete.Objects,
		observedResourceSlice(t, sliceChange),
	)
	complete.ResourceClaims = []cluster.ObservedResourceClaim{{
		Namespace:        "team-a",
		Name:             "external",
		UID:              "claim-uid",
		ResourceVersion:  "31",
		DeviceClassNames: []string{classChange.Key().Name()},
	}}
	blocked := newDRAFixture(t, recording.Options{
		Capabilities: draCapabilities(),
		Observed:     complete,
	}, 1, 1, 1)
	requestFixtureDeletion(t, blocked)
	result, err := blocked.reconciler.Reconcile(context.Background(), blocked.key)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Requeue() || len(blocked.cluster.PersistentChangeSets()) != 0 ||
		len(blocked.commits) != 1 ||
		blocked.commits[0].Status.Diagnostics[0].Code != "CleanupBlocked" {
		t.Fatalf("external DRA claim did not block deletion: %#v", blocked.commits)
	}

	unblockedGraph := cloneObservedGraph(complete)
	unblockedGraph.ResourceClaims = nil
	unblocked := newDRAFixture(t, recording.Options{
		Capabilities: draCapabilities(),
		Observed:     unblockedGraph,
	}, 1, 1, 1)
	requestFixtureDeletion(t, unblocked)
	_, err = unblocked.reconciler.Reconcile(context.Background(), unblocked.key)
	if err != nil {
		t.Fatal(err)
	}
	deletion := unblocked.cluster.PersistentChangeSets()[0].Changes()
	if len(deletion) != 1 ||
		deletion[0].Key().Kind() != cluster.ObjectKindResourceSlice {
		t.Fatalf("DRA cleanup did not delete ResourceSlice first: %#v", deletion)
	}

	classOnly := cloneObservedGraph(withClass)
	classOnly.Objects = append([]cluster.ObservedObject(nil), withClass.Objects...)
	classCleanup := newDRAFixture(t, recording.Options{
		Capabilities: draCapabilities(),
		Observed:     classOnly,
	}, 1, 1, 1)
	requestFixtureDeletion(t, classCleanup)
	_, err = classCleanup.reconciler.Reconcile(
		context.Background(),
		classCleanup.key,
	)
	if err != nil {
		t.Fatal(err)
	}
	classDeletion := classCleanup.cluster.PersistentChangeSets()[0].Changes()
	if len(classDeletion) != 1 ||
		classDeletion[0].Key().Kind() != cluster.ObjectKindDeviceClass {
		t.Fatalf("DRA cleanup did not delete DeviceClass second: %#v", classDeletion)
	}
}

func TestReconcileHealthUpdatePreservesCapacityAndNeverMutatesPods(t *testing.T) {
	t.Parallel()

	observed := completeObservedGraphWithResources(t, true, []cluster.ObservedPod{{
		Namespace: "default",
		Name:      "bound-workload",
		UID:       "pod-uid",
		NodeName:  syntheticNodeName(t),
		Phase:     "Running",
		Requested: map[string]string{"nvidia.com/gpu": "6"},
	}}, 8, 8)
	fixture := newFixtureScenario(t, recording.Options{
		Capabilities: schedulingCapabilities(),
		Observed:     observed,
	}, 8, 4, 1)
	_, err := fixture.reconciler.Reconcile(context.Background(), fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	changeSets := fixture.cluster.PersistentChangeSets()
	if len(changeSets) != 1 || len(changeSets[0].Changes()) != 1 {
		t.Fatalf("unexpected health stage: %#v", changeSets)
	}
	status, ok := changeSets[0].Changes()[0].(cluster.UpdateSyntheticNodeStatus)
	if !ok {
		t.Fatalf("health change = %T, want Node status", changeSets[0].Changes()[0])
	}
	if status.Capacity()["nvidia.com/gpu"] != "8" ||
		status.Allocatable()["nvidia.com/gpu"] != "4" {
		t.Fatalf("health change altered capacity semantics: %#v", status)
	}
}

func TestReconcileReportsOvercommitmentWithoutTouchingBoundPods(t *testing.T) {
	t.Parallel()

	observed := completeObservedGraphWithResources(t, false, []cluster.ObservedPod{{
		Namespace: "default",
		Name:      "bound-workload",
		UID:       "pod-uid",
		NodeName:  syntheticNodeName(t),
		Phase:     "Running",
		Requested: map[string]string{"nvidia.com/gpu": "6"},
	}}, 8, 4)
	fixture := newFixtureScenario(t, recording.Options{
		Capabilities: schedulingCapabilities(),
		Observed:     observed,
	}, 8, 4, 1)
	result, err := fixture.reconciler.Reconcile(context.Background(), fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if result.Phase() != "Ready" || result.Requeue() {
		t.Fatalf("overcommitted result = %#v", result)
	}
	if len(fixture.cluster.PersistentChangeSets()) != 0 {
		t.Fatal("overcommitment reconciliation mutated Node or Pod state")
	}
	found := false
	for _, condition := range fixture.commits[0].Status.Conditions {
		if condition.Type == "Overcommitted" &&
			condition.Status == "True" &&
			condition.Message == "nvidia.com/gpu requested 6 exceeds allocatable 4 on 1 node" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Overcommitted condition missing: %#v", fixture.commits[0].Status.Conditions)
	}
}

func TestReconcileDeletionClosesSchedulingBeforeInspectingBoundPods(t *testing.T) {
	t.Parallel()

	fixture := newFixture(t, recording.Options{
		Capabilities: schedulingCapabilities(),
		Observed: completeObservedGraph(t, false, []cluster.ObservedPod{{
			Namespace: "default",
			Name:      "bound-workload",
			UID:       "pod-uid",
			NodeName:  syntheticNodeName(t),
			Phase:     "Running",
			Requested: map[string]string{"nvidia.com/gpu": "1"},
		}}),
	})
	if err := fixture.controlPlane.RequestDeletion(
		context.Background(),
		fixture.key,
	); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.reconciler.Reconcile(context.Background(), fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Requeue() || result.Phase() != "Deleting" {
		t.Fatalf("deletion close result = %#v", result)
	}
	changeSets := fixture.cluster.PersistentChangeSets()
	if len(changeSets) != 1 || len(changeSets[0].Changes()) != 1 {
		t.Fatalf("unexpected deletion close stage: %#v", changeSets)
	}
	node, ok := changeSets[0].Changes()[0].(cluster.ApplySyntheticNode)
	if !ok || !node.Unschedulable() {
		t.Fatalf("delete did not close scheduling first: %#v", changeSets[0].Changes()[0])
	}
	if fixture.commits[0].Finalization != reconcile.FinalizationRetain ||
		fixture.commits[0].Status.Phase != "Deleting" {
		t.Fatalf("deletion finalizer/status not retained: %#v", fixture.commits[0])
	}
}

func TestReconcileDeletionReportsBoundPodWithoutMutatingIt(t *testing.T) {
	t.Parallel()

	fixture := newFixture(t, recording.Options{
		Capabilities: schedulingCapabilities(),
		Observed: completeObservedGraph(t, true, []cluster.ObservedPod{{
			Namespace: "team-a",
			Name:      "training",
			UID:       "pod-uid",
			NodeName:  syntheticNodeName(t),
			Phase:     "Running",
			Requested: map[string]string{"nvidia.com/gpu": "1"},
		}}),
	})
	requestFixtureDeletion(t, fixture)

	result, err := fixture.reconciler.Reconcile(context.Background(), fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Requeue() || result.Phase() != "Deleting" {
		t.Fatalf("blocked deletion result = %#v", result)
	}
	if len(fixture.cluster.PersistentChangeSets()) != 0 {
		t.Fatal("blocked deletion mutated an owned object or Pod")
	}
	intent := fixture.commits[0]
	if intent.Finalization != reconcile.FinalizationRetain ||
		len(intent.Status.Diagnostics) != 1 ||
		intent.Status.Diagnostics[0].Code != "CleanupBlocked" ||
		intent.Status.Diagnostics[0].Message !=
			"cleanup is blocked by bound Pods: team-a/training" {
		t.Fatalf("unexpected cleanup blocker intent: %#v", intent)
	}
}

func TestReconcileDeletionDeletesNodesBeforeLeasesWithExactIdentity(t *testing.T) {
	t.Parallel()

	fixture := newFixture(t, recording.Options{
		Capabilities: schedulingCapabilities(),
		Observed:     completeObservedGraph(t, true, nil),
	})
	requestFixtureDeletion(t, fixture)

	result, err := fixture.reconciler.Reconcile(context.Background(), fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Requeue() || result.Phase() != "Deleting" {
		t.Fatalf("Node deletion result = %#v", result)
	}
	changeSets := fixture.cluster.PersistentChangeSets()
	if len(changeSets) != 1 || len(changeSets[0].Changes()) != 1 {
		t.Fatalf("unexpected Node deletion stage: %#v", changeSets)
	}
	deletion, ok := changeSets[0].Changes()[0].(cluster.DeleteOwnedObject)
	if !ok ||
		deletion.Key().Kind() != cluster.ObjectKindNode ||
		deletion.Preconditions().UID != "node-uid" ||
		deletion.Preconditions().ResourceVersion != "11" {
		t.Fatalf("Node deletion lacks exact identity: %#v", changeSets[0].Changes()[0])
	}
	if fixture.commits[0].Finalization != reconcile.FinalizationRetain {
		t.Fatalf("finalizer removed before cleanup proof: %#v", fixture.commits[0])
	}
}

func TestReconcileDeletionDeletesRemainingLeaseAfterNodeDisappears(t *testing.T) {
	t.Parallel()

	observed := completeObservedGraph(t, true, nil)
	observed.Objects = observed.Objects[:1]
	fixture := newFixture(t, recording.Options{
		Capabilities: schedulingCapabilities(),
		Observed:     observed,
	})
	requestFixtureDeletion(t, fixture)

	result, err := fixture.reconciler.Reconcile(context.Background(), fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Requeue() || result.Phase() != "Deleting" {
		t.Fatalf("Lease deletion result = %#v", result)
	}
	changeSets := fixture.cluster.PersistentChangeSets()
	deletion, ok := changeSets[0].Changes()[0].(cluster.DeleteOwnedObject)
	if len(changeSets) != 1 ||
		len(changeSets[0].Changes()) != 1 ||
		!ok ||
		deletion.Key().Kind() != cluster.ObjectKindLease ||
		deletion.Preconditions().UID != "lease-uid" ||
		deletion.Preconditions().ResourceVersion != "10" {
		t.Fatalf("Lease deletion lacks exact identity: %#v", changeSets)
	}
}

func TestReconcileDeletionRemovesFinalizerOnlyAfterZeroOwnedObjects(t *testing.T) {
	t.Parallel()

	fixture := newFixture(t, recording.Options{
		Capabilities: schedulingCapabilities(),
	})
	requestFixtureDeletion(t, fixture)

	result, err := fixture.reconciler.Reconcile(context.Background(), fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if result.Requeue() || result.Phase() != "Deleting" {
		t.Fatalf("completed deletion result = %#v", result)
	}
	if len(fixture.cluster.PersistentChangeSets()) != 0 {
		t.Fatal("zero-object cleanup proof produced a mutation")
	}
	if fixture.commits[0].Finalization != reconcile.FinalizationRemove {
		t.Fatalf("finalizer not removed after cleanup proof: %#v", fixture.commits[0])
	}
}

func TestReconcileDeletionFailureAlwaysRetainsOwnershipFinalizer(t *testing.T) {
	t.Parallel()

	fixture := newFixture(t, recording.Options{
		Capabilities: schedulingCapabilities(),
		Observed:     completeObservedGraph(t, false, nil),
		Errors: map[recording.Call]error{
			recording.CallExecute: cluster.NewError(
				cluster.ErrorTransient,
				"temporary API outage",
				true,
			),
		},
	})
	requestFixtureDeletion(t, fixture)

	result, err := fixture.reconciler.Reconcile(context.Background(), fixture.key)
	if err == nil || !result.Requeue() || result.Phase() != "Failed" {
		t.Fatalf("deletion failure result=%#v error=%v", result, err)
	}
	if fixture.commits[0].Finalization != reconcile.FinalizationRetain {
		t.Fatalf("deletion failure released ownership: %#v", fixture.commits[0])
	}
}

func TestReconcileDeletionBypassesProjectionAndRuntimeCapabilities(t *testing.T) {
	t.Parallel()

	fixture := newFixture(t, recording.Options{})
	requestFixtureDeletion(t, fixture)
	result, err := fixture.reconciler.Reconcile(context.Background(), fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if result.Requeue() ||
		result.Phase() != "Deleting" ||
		fixture.commits[0].Finalization != reconcile.FinalizationRemove {
		t.Fatalf("capability-independent deletion result: %#v", result)
	}
	if calls := fixture.cluster.Calls(); len(calls) != 1 ||
		calls[0] != recording.CallObserve {
		t.Fatalf("deletion touched shared runtime/discovery: %#v", calls)
	}
}

func TestReconcileCleanupBlockerDetailsRemainBounded(t *testing.T) {
	t.Parallel()

	veryLongName := strings.Repeat("x", domain.MaximumDiagnosticMessageBytes*2)
	fixture := newFixture(t, recording.Options{
		Capabilities: schedulingCapabilities(),
		Observed: completeObservedGraph(t, true, []cluster.ObservedPod{{
			Namespace: "a",
			Name:      veryLongName,
			UID:       "pod-long",
			NodeName:  syntheticNodeName(t),
			Phase:     "Pending",
		}, {
			Namespace: "z",
			Name:      "later",
			UID:       "pod-later",
			NodeName:  syntheticNodeName(t),
			Phase:     "Running",
		}}),
	})
	requestFixtureDeletion(t, fixture)
	if _, err := fixture.reconciler.Reconcile(
		context.Background(),
		fixture.key,
	); err != nil {
		t.Fatal(err)
	}
	intent := fixture.commits[0]
	if len(intent.Status.Diagnostics) != 1 ||
		len(intent.Status.Diagnostics[0].Message) != domain.MaximumDiagnosticMessageBytes ||
		!strings.HasPrefix(
			intent.Status.Diagnostics[0].Message,
			"cleanup is blocked by bound Pods: a/",
		) ||
		len(intent.Status.Conditions) != 1 ||
		len(intent.Status.Conditions[0].Message) != domain.MaximumDiagnosticMessageBytes {
		t.Fatalf("cleanup blocker details were not bounded: %#v", intent)
	}
}

func TestReconcileScaleDownClosesStaleNodeBeforeInspectingBoundPods(t *testing.T) {
	t.Parallel()

	observed := completeObservedGraph(t, false, nil)
	staleNode, staleLease := staleObservedObjects(t, false)
	observed.Objects = append(observed.Objects, staleLease, staleNode)
	observed.Pods = []cluster.ObservedPod{{
		Namespace: "team-a",
		Name:      "stale-workload",
		UID:       "pod-stale",
		NodeName:  staleNode.Key.Name(),
		Phase:     "Running",
	}}
	fixture := newFixture(t, recording.Options{
		Capabilities: schedulingCapabilities(),
		Observed:     observed,
	})

	result, err := fixture.reconciler.Reconcile(context.Background(), fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Requeue() || result.Phase() != "Reconciling" {
		t.Fatalf("scale-down close result = %#v", result)
	}
	changeSets := fixture.cluster.PersistentChangeSets()
	if len(changeSets) != 1 || len(changeSets[0].Changes()) != 1 {
		t.Fatalf("unexpected scale-down close stage: %#v", changeSets)
	}
	node, ok := changeSets[0].Changes()[0].(cluster.ApplySyntheticNode)
	if !ok ||
		node.Key().Name() != staleNode.Key.Name() ||
		!node.Unschedulable() {
		t.Fatalf("stale Node was not closed first: %#v", changeSets[0].Changes()[0])
	}
	if fixture.commits[0].Finalization != reconcile.FinalizationEnsure {
		t.Fatalf("scale-down lost ownership finalizer: %#v", fixture.commits[0])
	}
}

func TestReconcileScaleDownReportsBlockerWithoutDeletingNodeOrPod(t *testing.T) {
	t.Parallel()

	observed := completeObservedGraph(t, false, nil)
	staleNode, staleLease := staleObservedObjects(t, true)
	observed.Objects = append(observed.Objects, staleLease, staleNode)
	observed.Pods = []cluster.ObservedPod{{
		Namespace: "team-a",
		Name:      "stale-workload",
		UID:       "pod-stale",
		NodeName:  staleNode.Key.Name(),
		Phase:     "Running",
	}}
	fixture := newFixture(t, recording.Options{
		Capabilities: schedulingCapabilities(),
		Observed:     observed,
	})

	result, err := fixture.reconciler.Reconcile(context.Background(), fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Requeue() || result.Phase() != "Reconciling" {
		t.Fatalf("blocked scale-down result = %#v", result)
	}
	if len(fixture.cluster.PersistentChangeSets()) != 0 {
		t.Fatal("blocked scale-down mutated an owned object or Pod")
	}
	if fixture.commits[0].Status.Diagnostics[0].Code != "CleanupBlocked" ||
		fixture.commits[0].Status.Diagnostics[0].Message !=
			"scale-down is blocked by bound Pods: team-a/stale-workload" {
		t.Fatalf("unexpected stale blocker: %#v", fixture.commits[0])
	}
}

func TestReconcileScaleDownDeletesClosedStaleNodeBeforeItsLease(t *testing.T) {
	t.Parallel()

	observed := completeObservedGraph(t, false, nil)
	staleNode, staleLease := staleObservedObjects(t, true)
	observed.Objects = append(observed.Objects, staleLease, staleNode)
	fixture := newFixture(t, recording.Options{
		Capabilities: schedulingCapabilities(),
		Observed:     observed,
	})

	result, err := fixture.reconciler.Reconcile(context.Background(), fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Requeue() || result.Phase() != "Reconciling" {
		t.Fatalf("stale Node deletion result = %#v", result)
	}
	changes := fixture.cluster.PersistentChangeSets()[0].Changes()
	deletion, ok := changes[0].(cluster.DeleteOwnedObject)
	if len(changes) != 1 ||
		!ok ||
		deletion.Key().Name() != staleNode.Key.Name() ||
		deletion.Key().Kind() != cluster.ObjectKindNode ||
		deletion.Preconditions().UID != "stale-node-uid" {
		t.Fatalf("unexpected stale Node deletion: %#v", changes)
	}
}

func TestReconcileScaleDownDeletesOrphanedStaleLease(t *testing.T) {
	t.Parallel()

	observed := completeObservedGraph(t, false, nil)
	_, staleLease := staleObservedObjects(t, true)
	observed.Objects = append(observed.Objects, staleLease)
	fixture := newFixture(t, recording.Options{
		Capabilities: schedulingCapabilities(),
		Observed:     observed,
	})

	result, err := fixture.reconciler.Reconcile(context.Background(), fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Requeue() || result.Phase() != "Reconciling" {
		t.Fatalf("stale Lease deletion result = %#v", result)
	}
	changes := fixture.cluster.PersistentChangeSets()[0].Changes()
	deletion, ok := changes[0].(cluster.DeleteOwnedObject)
	if len(changes) != 1 ||
		!ok ||
		deletion.Key().Name() != staleLease.Key.Name() ||
		deletion.Key().Kind() != cluster.ObjectKindLease ||
		deletion.Preconditions().UID != "stale-lease-uid" {
		t.Fatalf("unexpected stale Lease deletion: %#v", changes)
	}
}

func TestReconcileStatusAggregatesPoolCountsAcrossReplicas(t *testing.T) {
	t.Parallel()

	fixture := newFixtureScenario(t, recording.Options{
		Capabilities: schedulingCapabilities(),
	}, 8, 6, 3)
	_, err := fixture.reconciler.Reconcile(context.Background(), fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	pools := fixture.commits[0].Status.Pools
	if len(pools) != 1 ||
		pools[0].Group != "nodes" ||
		pools[0].RequestedTotal != 24 ||
		pools[0].RequestedHealthy != 18 {
		t.Fatalf("pool status was not aggregated across replicas: %#v", pools)
	}
}

func TestReconcileRetriesSameAcceptedRevisionWithoutIdentityDrift(t *testing.T) {
	t.Parallel()

	fixture := newFixture(t, recording.Options{
		Capabilities: schedulingCapabilities(),
		Errors: map[recording.Call]error{
			recording.CallExecute: cluster.NewError(
				cluster.ErrorTransient,
				"synthetic transport interruption",
				true,
			),
		},
	})
	firstResult, err := fixture.reconciler.Reconcile(context.Background(), fixture.key)
	if err == nil ||
		!firstResult.Requeue() ||
		firstResult.Phase() != "Failed" {
		t.Fatalf("first transient result=%#v error=%v", firstResult, err)
	}
	attempts := fixture.cluster.AttemptedChangeSets()
	if len(attempts) != 1 {
		t.Fatalf("failed execution attempts = %d, want 1", len(attempts))
	}
	fixture.cluster.ClearError(recording.CallExecute)
	secondResult, err := fixture.reconciler.Reconcile(context.Background(), fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if !secondResult.Requeue() || secondResult.Phase() != "Reconciling" {
		t.Fatalf("retry result = %#v", secondResult)
	}
	persisted := fixture.cluster.PersistentChangeSets()
	if len(persisted) != 1 {
		t.Fatalf("retry persisted sets = %d, want 1", len(persisted))
	}
	firstChanges := attempts[0].Changes()
	secondChanges := persisted[0].Changes()
	if len(firstChanges) != len(secondChanges) {
		t.Fatalf("retry change count drifted: %d -> %d", len(firstChanges), len(secondChanges))
	}
	for index := range firstChanges {
		if firstChanges[index].Kind() != secondChanges[index].Kind() ||
			firstChanges[index].Key() != secondChanges[index].Key() {
			t.Fatalf(
				"retry identity drift at %d: %#v -> %#v",
				index,
				firstChanges[index],
				secondChanges[index],
			)
		}
	}
}

func TestReconcileRecoversFromPartiallyAppliedIdentityBatch(t *testing.T) {
	t.Parallel()

	observed := completeObservedGraph(t, true, nil)
	observed.Objects = observed.Objects[:1]
	fixture := newFixture(t, recording.Options{
		Capabilities: schedulingCapabilities(),
		Observed:     observed,
	})

	result, err := fixture.reconciler.Reconcile(context.Background(), fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Requeue() || result.Phase() != "Reconciling" {
		t.Fatalf("partial recovery result = %#v", result)
	}
	changes := fixture.cluster.PersistentChangeSets()[0].Changes()
	node, ok := changes[0].(cluster.ApplySyntheticNode)
	if len(changes) != 1 ||
		!ok ||
		node.Key().Name() != syntheticNodeName(t) ||
		!node.Unschedulable() {
		t.Fatalf("partial batch did not resume at missing Node: %#v", changes)
	}
}

func TestReconcileOwnershipConflictNeverAdoptsDesiredNameObject(t *testing.T) {
	t.Parallel()

	fixture := newFixture(t, recording.Options{
		Capabilities: schedulingCapabilities(),
		Errors: map[recording.Call]error{
			recording.CallExecute: cluster.NewError(
				cluster.ErrorOwnershipConflict,
				"desired Node name already exists without exact ownership",
				false,
			),
		},
	})
	result, err := fixture.reconciler.Reconcile(context.Background(), fixture.key)
	if err == nil ||
		result.Requeue() ||
		result.Phase() != "Failed" {
		t.Fatalf("ownership conflict result=%#v error=%v", result, err)
	}
	if len(fixture.cluster.PersistentChangeSets()) != 0 {
		t.Fatal("ownership conflict adopted or mutated the conflicting object")
	}
	intent := fixture.commits[0]
	if len(intent.Status.Diagnostics) != 1 ||
		intent.Status.Diagnostics[0].Code != "OwnershipConflict" ||
		len(intent.Status.Conditions) != 1 ||
		intent.Status.Conditions[0].Type != "OwnershipConflict" {
		t.Fatalf("ownership conflict evidence missing: %#v", intent)
	}
}

func TestReconcileReplacementClosesOldGenerationBeforeUpdatingIdentity(t *testing.T) {
	t.Parallel()

	observed := completeObservedGraph(t, false, nil)
	oldGeneration, err := domain.NewGeneration(2)
	if err != nil {
		t.Fatal(err)
	}
	for index := range observed.Objects {
		observed.Objects[index].DesiredGeneration = oldGeneration
		if observed.Objects[index].Node != nil {
			observed.Objects[index].Node.Labels[cluster.DesiredGenerationLabel] = "2"
		}
	}
	fixture := newFixture(t, recording.Options{
		Capabilities: schedulingCapabilities(),
		Observed:     observed,
	})

	result, err := fixture.reconciler.Reconcile(context.Background(), fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Requeue() || result.Phase() != "Reconciling" {
		t.Fatalf("replacement result = %#v", result)
	}
	changes := fixture.cluster.PersistentChangeSets()[0].Changes()
	if len(changes) != 2 {
		t.Fatalf("replacement changes = %d, want Node and Lease", len(changes))
	}
	node, ok := changes[0].(cluster.ApplySyntheticNode)
	if !ok || !node.Unschedulable() {
		t.Fatalf("replacement did not close scheduling first: %#v", changes[0])
	}
	if _, ok := changes[1].(cluster.ApplyLease); !ok {
		t.Fatalf("replacement second change = %T, want ApplyLease", changes[1])
	}
}

func TestReconcileResourceIdentityReplacementClosesSchedulingFirst(t *testing.T) {
	t.Parallel()

	observed := completeObservedGraph(t, false, nil)
	for index := range observed.Objects {
		if observed.Objects[index].Node == nil {
			continue
		}
		observed.Objects[index].Node.Capacity["amd.com/gpu"] = "4"
		observed.Objects[index].Node.Allocatable["amd.com/gpu"] = "4"
	}
	fixture := newFixture(t, recording.Options{
		Capabilities: schedulingCapabilities(),
		Observed:     observed,
	})

	result, err := fixture.reconciler.Reconcile(context.Background(), fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Requeue() || result.Phase() != "Reconciling" {
		t.Fatalf("resource replacement close result = %#v", result)
	}
	changes := fixture.cluster.PersistentChangeSets()[0].Changes()
	node, ok := changes[0].(cluster.ApplySyntheticNode)
	if len(changes) != 1 || !ok || !node.Unschedulable() {
		t.Fatalf("resource identity replacement did not close first: %#v", changes)
	}
}

func TestReconcileResourceIdentityReplacementRemovesStaleCatalogResource(t *testing.T) {
	t.Parallel()

	observed := completeObservedGraph(t, true, nil)
	for index := range observed.Objects {
		if observed.Objects[index].Node == nil {
			continue
		}
		observed.Objects[index].Node.Capacity["amd.com/gpu"] = "4"
		observed.Objects[index].Node.Allocatable["amd.com/gpu"] = "4"
	}
	fixture := newFixture(t, recording.Options{
		Capabilities: schedulingCapabilities(),
		Observed:     observed,
	})

	result, err := fixture.reconciler.Reconcile(context.Background(), fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Requeue() || result.Phase() != "Reconciling" {
		t.Fatalf("resource replacement status result = %#v", result)
	}
	changes := fixture.cluster.PersistentChangeSets()[0].Changes()
	status, ok := changes[0].(cluster.UpdateSyntheticNodeStatus)
	if len(changes) != 1 ||
		!ok ||
		status.Capacity()["nvidia.com/gpu"] != "8" ||
		status.Allocatable()["nvidia.com/gpu"] != "8" {
		t.Fatalf("unexpected replacement status intent: %#v", changes)
	}
	if _, stale := status.Capacity()["amd.com/gpu"]; stale {
		t.Fatalf("replacement retained stale capacity: %#v", status.Capacity())
	}
	if _, stale := status.Allocatable()["amd.com/gpu"]; stale {
		t.Fatalf("replacement retained stale allocatable: %#v", status.Allocatable())
	}
}

func TestReconcileVendorIdentityReplacementRemovesStaleCatalogLabel(t *testing.T) {
	t.Parallel()

	observed := completeObservedGraph(t, false, nil)
	for index := range observed.Objects {
		if observed.Objects[index].Node != nil {
			observed.Objects[index].Node.Labels["amd.com/gpu.product-name"] = "MI300X"
		}
	}
	fixture := newFixture(t, recording.Options{
		Capabilities: schedulingCapabilities(),
		Observed:     observed,
	})

	result, err := fixture.reconciler.Reconcile(context.Background(), fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Requeue() || result.Phase() != "Reconciling" {
		t.Fatalf("identity replacement result = %#v", result)
	}
	changes := fixture.cluster.PersistentChangeSets()[0].Changes()
	node, ok := changes[0].(cluster.ApplySyntheticNode)
	if len(changes) != 1 || !ok || !node.Unschedulable() {
		t.Fatalf("vendor identity replacement did not close first: %#v", changes)
	}
	if _, stale := node.Labels()["amd.com/gpu.product-name"]; stale {
		t.Fatalf("replacement retained stale vendor label: %#v", node.Labels())
	}
}

type fixture struct {
	key          controlplane.InstanceKey
	reconciler   *reconcile.InstanceReconciler
	controlPlane *memory.Adapter
	cluster      *recording.Adapter
	commits      []reconcile.StatusIntent
}

func requestFixtureDeletion(t *testing.T, fixture *fixture) {
	t.Helper()
	if err := fixture.controlPlane.RequestDeletion(
		context.Background(),
		fixture.key,
	); err != nil {
		t.Fatal(err)
	}
}

func newFixture(t *testing.T, clusterOptions recording.Options) *fixture {
	t.Helper()
	return newFixtureScenario(t, clusterOptions, 8, 8, 1)
}

func newFixtureScenario(
	t *testing.T,
	clusterOptions recording.Options,
	count,
	healthy,
	nodes int64,
) *fixture {
	return newFixtureWithProjection(
		t,
		clusterOptions,
		count,
		healthy,
		nodes,
		"",
		"",
		"",
		extended.New(),
	)
}

func newDRAFixture(
	t *testing.T,
	clusterOptions recording.Options,
	count,
	healthy,
	nodes int64,
) *fixture {
	t.Helper()
	return newFixtureWithProjection(
		t,
		clusterOptions,
		count,
		healthy,
		nodes,
		"dra-control-plane",
		"dra",
		"device",
		draprojection.New(),
	)
}

func newFixtureWithProjection(
	t *testing.T,
	clusterOptions recording.Options,
	count,
	healthy,
	nodes int64,
	fidelity,
	contract,
	resourceAlias string,
	resourceProjection projection.ResourceProjection,
) *fixture {
	t.Helper()
	snapshot, err := catalog.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	healthyCopy := healthy
	compiledInput, err := scenario.Shortcut(scenario.ShortcutInput{
		Name:                "training-lab",
		ProfileID:           "nvidia",
		ModelID:             "nvidia-h100",
		Fidelity:            fidelity,
		ContractID:          contract,
		ResourceAlias:       resourceAlias,
		Nodes:               nodes,
		AcceleratorsPerNode: count,
		HealthyPerNode:      &healthyCopy,
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, receipt, err := scenario.Compile(compiledInput, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := domain.ParseDigest(
		"sha256:4ec3c3f619002282cc8452656d1e7f156b4498309996485be6cbf4ce53de1c0c",
	)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := domain.NewGeneration(1)
	if err != nil {
		t.Fatal(err)
	}
	zero, err := domain.NewGeneration(0)
	if err != nil {
		t.Fatal(err)
	}
	profiles := make([]controlplane.ProfileReceipt, 0)
	seenProfiles := make(map[string]struct{})
	for _, resolution := range receipt.Resolutions() {
		profile := compiled.Scenario().NodeGroups()[0].Pools()[0].Profile()
		if _, seen := seenProfiles[profile.ID().String()]; seen {
			continue
		}
		seenProfiles[profile.ID().String()] = struct{}{}
		profiles = append(profiles, controlplane.ProfileReceipt{
			ID:       profile.ID().String(),
			Revision: profile.Revision(),
			Digest:   resolution.ProfileDigest(),
			Class:    resolution.ProfileClass(),
		})
	}
	controlPlane := memory.New(memory.Options{})
	target := controlplane.ExplicitTarget{
		ContextName: "fixture",
		Fingerprint: fingerprint,
	}
	_, err = controlPlane.Submit(context.Background(), controlplane.RevisionCommand{
		Target:           target,
		Name:             compiled.Scenario().Name(),
		CreationIdentity: "fixture",
		Fidelity:         compiled.Scenario().Fidelity(),
		Preconditions: controlplane.Preconditions{
			ExpectedGeneration: zero,
		},
		Revision: controlplane.ScenarioRevision{
			Generation:        generation,
			Digest:            compiled.Digest(),
			CanonicalScenario: compiled.Bytes(),
			Profiles:          profiles,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	key := controlplane.InstanceKey{
		TargetFingerprint: fingerprint,
		Name:              compiled.Scenario().Name(),
	}
	clusterAdapter := recording.New(clusterOptions)
	result := &fixture{
		key:          key,
		controlPlane: controlPlane,
		cluster:      clusterAdapter,
	}
	reconciler, err := reconcile.New(reconcile.Options{
		ControlPlane: controlPlane,
		Cluster:      clusterAdapter,
		Catalog:      snapshot,
		Projection:   resourceProjection,
		Now:          func() time.Time { return fixedTime },
		Commit: func(_ context.Context, intent reconcile.StatusIntent) error {
			result.commits = append(result.commits, intent)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result.reconciler = reconciler
	return result
}

func schedulingCapabilities() cluster.TargetCapabilities {
	return cluster.TargetCapabilities{
		ServerVersion:   "v1.36.2",
		KubernetesMinor: 36,
		Resources: []cluster.ResourceCapability{
			{GroupVersion: "v1", Resource: "nodes"},
			{
				GroupVersion: "coordination.k8s.io/v1",
				Resource:     "leases",
				Namespaced:   true,
			},
		},
	}
}

func draCapabilities() cluster.TargetCapabilities {
	ownedVerbs := []string{"get", "list", "watch", "create", "patch", "delete"}
	return cluster.TargetCapabilities{
		ServerVersion:   "v1.36.2",
		KubernetesMinor: 36,
		Resources: []cluster.ResourceCapability{
			{GroupVersion: "v1", Resource: "nodes", Verbs: ownedVerbs},
			{
				GroupVersion: "v1",
				Resource:     "pods",
				Namespaced:   true,
				Verbs:        []string{"get", "list", "watch"},
			},
			{
				GroupVersion: "coordination.k8s.io/v1",
				Resource:     "leases",
				Namespaced:   true,
				Verbs:        ownedVerbs,
			},
			{
				GroupVersion: "resource.k8s.io/v1",
				Resource:     "deviceclasses",
				Verbs:        ownedVerbs,
			},
			{
				GroupVersion: "resource.k8s.io/v1",
				Resource:     "resourceslices",
				Verbs:        ownedVerbs,
			},
			{
				GroupVersion: "resource.k8s.io/v1",
				Resource:     "resourceclaims",
				Namespaced:   true,
				Verbs:        []string{"get", "list", "watch"},
			},
		},
	}
}

func syntheticNodeName(t *testing.T) string {
	t.Helper()
	return syntheticNodeNameAt(t, 0)
}

func syntheticNodeNameAt(t *testing.T, index uint64) string {
	t.Helper()
	instanceName, err := domain.ParseName("training-lab")
	if err != nil {
		t.Fatal(err)
	}
	uid, err := domain.ParseInstanceUID("memory-1")
	if err != nil {
		t.Fatal(err)
	}
	group, err := domain.ParseName("nodes")
	if err != nil {
		t.Fatal(err)
	}
	name, err := domain.SyntheticNodeName(instanceName, uid, group, index)
	if err != nil {
		t.Fatal(err)
	}
	return name.String()
}

func staleObservedObjects(
	t *testing.T,
	unschedulable bool,
) (cluster.ObservedObject, cluster.ObservedObject) {
	t.Helper()
	name := syntheticNodeNameAt(t, 1)
	generation, err := domain.NewGeneration(1)
	if err != nil {
		t.Fatal(err)
	}
	nodeKey, err := cluster.NewObjectKey(cluster.ObjectKindNode, "", name)
	if err != nil {
		t.Fatal(err)
	}
	leaseKey, err := cluster.NewObjectKey(
		cluster.ObjectKindLease,
		"kube-node-lease",
		name,
	)
	if err != nil {
		t.Fatal(err)
	}
	return cluster.ObservedObject{
			Key:               nodeKey,
			UID:               "stale-node-uid",
			ResourceVersion:   "21",
			DesiredGeneration: generation,
			Node: &cluster.ObservedNodeState{
				Labels: map[string]string{
					"kubernetes.io/hostname":            name,
					"simulation.kasim.io/scenario":      "training-lab",
					"simulation.kasim.io/node-group":    "nodes",
					"simulation.kasim.io/replica-index": "1",
					cluster.ManagedByLabel:              cluster.ManagedByValue,
					cluster.InstanceUIDLabel:            "memory-1",
					cluster.DesiredGenerationLabel:      "1",
				},
				Annotations:   map[string]string{"kwok.x-k8s.io/node": "fake"},
				Unschedulable: unschedulable,
				Capacity:      map[string]string{"nvidia.com/gpu": "8"},
				Allocatable:   map[string]string{"nvidia.com/gpu": "8"},
				Ready:         true,
			},
		}, cluster.ObservedObject{
			Key:               leaseKey,
			UID:               "stale-lease-uid",
			ResourceVersion:   "20",
			DesiredGeneration: generation,
			Lease: &cluster.ObservedLeaseState{
				HolderIdentity:       name,
				LeaseDurationSeconds: 40,
				RenewTime:            fixedTime,
			},
		}
}

func completeObservedGraph(
	t *testing.T,
	unschedulable bool,
	pods []cluster.ObservedPod,
) cluster.ObservedGraph {
	t.Helper()
	return completeObservedGraphWithResources(t, unschedulable, pods, 8, 8)
}

func completeObservedGraphWithResources(
	t *testing.T,
	unschedulable bool,
	pods []cluster.ObservedPod,
	capacity,
	allocatable uint64,
) cluster.ObservedGraph {
	t.Helper()
	nodeName := syntheticNodeName(t)
	generation, err := domain.NewGeneration(1)
	if err != nil {
		t.Fatal(err)
	}
	nodeKey, err := cluster.NewObjectKey(cluster.ObjectKindNode, "", nodeName)
	if err != nil {
		t.Fatal(err)
	}
	leaseKey, err := cluster.NewObjectKey(
		cluster.ObjectKindLease,
		"kube-node-lease",
		nodeName,
	)
	if err != nil {
		t.Fatal(err)
	}
	return cluster.ObservedGraph{
		Objects: []cluster.ObservedObject{
			{
				Key:               leaseKey,
				UID:               "lease-uid",
				ResourceVersion:   "10",
				DesiredGeneration: generation,
				Lease: &cluster.ObservedLeaseState{
					HolderIdentity:       nodeName,
					LeaseDurationSeconds: 40,
					RenewTime:            fixedTime,
				},
			},
			{
				Key:               nodeKey,
				UID:               "node-uid",
				ResourceVersion:   "11",
				DesiredGeneration: generation,
				Node: &cluster.ObservedNodeState{
					Labels: map[string]string{
						"kubernetes.io/hostname":            nodeName,
						"simulation.kasim.io/scenario":      "training-lab",
						"simulation.kasim.io/node-group":    "nodes",
						"simulation.kasim.io/replica-index": "0",
						cluster.ManagedByLabel:              cluster.ManagedByValue,
						cluster.InstanceUIDLabel:            "memory-1",
						cluster.DesiredGenerationLabel:      "1",
					},
					Annotations:   map[string]string{"kwok.x-k8s.io/node": "fake"},
					Unschedulable: unschedulable,
					Capacity: map[string]string{
						"nvidia.com/gpu": strconv.FormatUint(capacity, 10),
					},
					Allocatable: map[string]string{
						"nvidia.com/gpu": strconv.FormatUint(allocatable, 10),
					},
					Ready: true,
				},
			},
		},
		Pods: pods,
	}
}

func completeDRAObservedGraph(
	t *testing.T,
	unschedulable bool,
) cluster.ObservedGraph {
	t.Helper()
	graph := completeObservedGraphWithResources(t, unschedulable, nil, 0, 0)
	for index := range graph.Objects {
		if graph.Objects[index].Node != nil {
			graph.Objects[index].Node.Capacity = map[string]string{}
			graph.Objects[index].Node.Allocatable = map[string]string{}
		}
	}
	return graph
}

func observedDeviceClass(
	t *testing.T,
	change cluster.ApplyDeviceClass,
) cluster.ObservedObject {
	t.Helper()
	generation, err := domain.NewGeneration(1)
	if err != nil {
		t.Fatal(err)
	}
	return cluster.ObservedObject{
		Key:               change.Key(),
		UID:               "class-uid",
		ResourceVersion:   "21",
		DesiredGeneration: generation,
		DeviceClass: &cluster.ObservedDeviceClassState{
			Selectors: change.Selectors(),
		},
	}
}

func observedResourceSlice(
	t *testing.T,
	change cluster.ApplyResourceSlice,
) cluster.ObservedObject {
	t.Helper()
	generation, err := domain.NewGeneration(1)
	if err != nil {
		t.Fatal(err)
	}
	return cluster.ObservedObject{
		Key:               change.Key(),
		UID:               "slice-uid",
		ResourceVersion:   "22",
		DesiredGeneration: generation,
		ResourceSlice: &cluster.ObservedResourceSliceState{
			Driver:             change.Driver(),
			PoolName:           change.PoolName(),
			PoolGeneration:     change.PoolGeneration(),
			ResourceSliceCount: change.ResourceSliceCount(),
			NodeName:           change.NodeName(),
			Devices:            change.Devices(),
		},
	}
}

func cloneObservedGraph(input cluster.ObservedGraph) cluster.ObservedGraph {
	result := cluster.ObservedGraph{
		Objects: make([]cluster.ObservedObject, len(input.Objects)),
		Pods:    append([]cluster.ObservedPod(nil), input.Pods...),
		ResourceClaims: append(
			[]cluster.ObservedResourceClaim(nil),
			input.ResourceClaims...,
		),
	}
	for index, object := range input.Objects {
		result.Objects[index] = object
		if object.Node != nil {
			node := *object.Node
			node.Labels = cloneStringMapForTest(object.Node.Labels)
			node.Annotations = cloneStringMapForTest(object.Node.Annotations)
			node.Capacity = cloneStringMapForTest(object.Node.Capacity)
			node.Allocatable = cloneStringMapForTest(object.Node.Allocatable)
			node.Taints = append([]cluster.NodeTaint(nil), object.Node.Taints...)
			result.Objects[index].Node = &node
		}
		if object.Lease != nil {
			lease := *object.Lease
			result.Objects[index].Lease = &lease
		}
		if object.DeviceClass != nil {
			deviceClass := *object.DeviceClass
			deviceClass.Selectors = append(
				[]string(nil),
				object.DeviceClass.Selectors...,
			)
			result.Objects[index].DeviceClass = &deviceClass
		}
		if object.ResourceSlice != nil {
			resourceSlice := *object.ResourceSlice
			resourceSlice.Devices = append(
				[]cluster.DRADevice(nil),
				object.ResourceSlice.Devices...,
			)
			result.Objects[index].ResourceSlice = &resourceSlice
		}
	}
	return result
}

func cloneStringMapForTest(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
