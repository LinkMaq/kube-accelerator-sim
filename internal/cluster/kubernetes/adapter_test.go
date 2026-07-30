package kubernetes_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/version"
	discoveryfake "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	clusterkubernetes "github.com/LinkMaq/kube-accelerator-sim/internal/cluster/kubernetes"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

func TestAdapterDiscoversTheFrozenKubernetesRangeAndRuntimeAPI(t *testing.T) {
	t.Parallel()

	kubernetesClient := kubernetesfake.NewSimpleClientset()
	configureDiscovery(kubernetesClient, "36")
	adapter := clusterkubernetes.NewAdapter(kubernetesClient)
	capabilities, err := adapter.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.ServerVersion != "v1.36.3" ||
		capabilities.KubernetesMinor != 36 ||
		!slices.ContainsFunc(
			capabilities.Resources,
			func(capability cluster.ResourceCapability) bool {
				return capability.GroupVersion == "simulation.kasim.io/v1alpha1" &&
					capability.Resource == "scenarioinstances"
			},
		) {
		t.Fatalf("unexpected discovery result: %#v", capabilities)
	}

	configureDiscovery(kubernetesClient, "29")
	if _, err := adapter.Discover(context.Background()); cluster.ErrorCodeOf(err) !=
		cluster.ErrorKubernetesVersionUnsupported {
		t.Fatalf("Kubernetes 1.29 error = %v", err)
	}
	configureDiscovery(kubernetesClient, "37")
	if _, err := adapter.Discover(context.Background()); cluster.ErrorCodeOf(err) !=
		cluster.ErrorKubernetesVersionUntested {
		t.Fatalf("Kubernetes 1.37 error = %v", err)
	}
	configureDiscovery(kubernetesClient, "36")
	fakeDiscovery := kubernetesClient.Discovery().(*discoveryfake.FakeDiscovery)
	fakeDiscovery.Resources = fakeDiscovery.Resources[:3]
	if _, err := adapter.Discover(context.Background()); cluster.ErrorCodeOf(err) !=
		cluster.ErrorRuntimeUnavailable {
		t.Fatalf("missing product runtime error = %v", err)
	}
}

func TestAdapterSubmitsExactSelfSubjectAccessReviews(t *testing.T) {
	t.Parallel()

	kubernetesClient := kubernetesfake.NewSimpleClientset()
	var observed authorizationv1.ResourceAttributes
	kubernetesClient.Fake.PrependReactor(
		"create",
		"selfsubjectaccessreviews",
		func(action clienttesting.Action) (bool, runtime.Object, error) {
			review := action.(clienttesting.CreateAction).
				GetObject().(*authorizationv1.SelfSubjectAccessReview)
			observed = *review.Spec.ResourceAttributes.DeepCopy()
			result := review.DeepCopy()
			result.Status.Allowed = true
			result.Status.Reason = "contract-test"
			return true, result, nil
		},
	)
	adapter := clusterkubernetes.NewAdapter(kubernetesClient)
	requirement := cluster.AccessRequirement{
		Verb:        "update",
		Group:       "simulation.kasim.io",
		Resource:    "scenarioinstances",
		Subresource: "status",
		Name:        "training-lab",
	}
	report, err := adapter.Authorize(
		context.Background(),
		[]cluster.AccessRequirement{requirement},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Decisions) != 1 || !report.Decisions[0].Allowed ||
		observed.Verb != requirement.Verb ||
		observed.Group != requirement.Group ||
		observed.Resource != requirement.Resource ||
		observed.Subresource != requirement.Subresource ||
		observed.Name != requirement.Name ||
		observed.Namespace != "" {
		t.Fatalf("SelfSubjectAccessReview was not exact: %#v", observed)
	}
}

func TestAdapterClassifiesAndRedactsKubernetesAuthorizationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		code cluster.ErrorCode
	}{
		{
			name: "unauthorized",
			err:  apierrors.NewUnauthorized("bearer-token-must-not-escape"),
			code: cluster.ErrorAuthenticationFailed,
		},
		{
			name: "forbidden",
			err: apierrors.NewForbidden(
				schema.GroupResource{
					Group:    "simulation.kasim.io",
					Resource: "scenarioinstances",
				},
				"training-lab",
				apierrors.NewUnauthorized("private-detail"),
			),
			code: cluster.ErrorAuthorizationDenied,
		},
		{
			name: "rate limited",
			err:  apierrors.NewTooManyRequests("private-detail", 1),
			code: cluster.ErrorRateLimited,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			kubernetesClient := kubernetesfake.NewSimpleClientset()
			kubernetesClient.Fake.PrependReactor(
				"create",
				"selfsubjectaccessreviews",
				func(clienttesting.Action) (bool, runtime.Object, error) {
					return true, nil, test.err
				},
			)
			adapter := clusterkubernetes.NewAdapter(kubernetesClient)
			_, err := adapter.Authorize(
				context.Background(),
				[]cluster.AccessRequirement{{
					Verb:     "get",
					Group:    "simulation.kasim.io",
					Resource: "scenarioinstances",
					Name:     "training-lab",
				}},
			)
			if cluster.ErrorCodeOf(err) != test.code {
				t.Fatalf("classified error = %v, want %s", err, test.code)
			}
			if strings.Contains(err.Error(), "private") ||
				strings.Contains(err.Error(), "bearer") {
				t.Fatalf("classified error exposed raw detail: %v", err)
			}
		})
	}
}

func TestAdapterObservesOnlyExactUIDOwnedNodesAndLeases(t *testing.T) {
	t.Parallel()

	scope := ownershipScope(t, 3)
	ownedLabels := ownershipLabels(scope)
	kubernetesClient := kubernetesfake.NewSimpleClientset(
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "owned-node",
				UID:             types.UID("node-uid"),
				ResourceVersion: "11",
				Labels:          ownedLabels,
			},
		},
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "real-node",
				UID:             types.UID("real-uid"),
				ResourceVersion: "12",
			},
		},
		&coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:       "kube-node-lease",
				Name:            "owned-node",
				UID:             types.UID("lease-uid"),
				ResourceVersion: "13",
				Labels:          ownedLabels,
			},
		},
	)
	adapter := clusterkubernetes.NewAdapter(kubernetesClient)
	graph, err := adapter.Observe(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Objects) != 2 ||
		!slices.ContainsFunc(graph.Objects, func(object cluster.ObservedObject) bool {
			return object.Key.Kind() == cluster.ObjectKindNode &&
				object.Key.Name() == "owned-node" &&
				object.UID == "node-uid"
		}) ||
		!slices.ContainsFunc(graph.Objects, func(object cluster.ObservedObject) bool {
			return object.Key.Kind() == cluster.ObjectKindLease &&
				object.Key.Name() == "owned-node" &&
				object.UID == "lease-uid"
		}) {
		t.Fatalf("unexpected exact-owned observation: %#v", graph)
	}
}

func TestAdapterObservesSchedulerStateLeaseHeartbeatAndBoundPodRequests(t *testing.T) {
	t.Parallel()

	scope := ownershipScope(t, 3)
	ownedLabels := ownershipLabels(scope)
	renewTime := metav1.NewMicroTime(time.Date(2026, 7, 30, 7, 0, 0, 0, time.UTC))
	holder := "owned-node"
	duration := int32(40)
	kubernetesClient := kubernetesfake.NewSimpleClientset(
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "owned-node",
				UID:             types.UID("node-uid"),
				ResourceVersion: "11",
				Labels:          ownedLabels,
				Annotations:     map[string]string{"kwok.x-k8s.io/node": "fake"},
			},
			Spec: corev1.NodeSpec{
				Unschedulable: true,
				Taints: []corev1.Taint{{
					Key: "accelerator", Value: "simulated", Effect: corev1.TaintEffectNoSchedule,
				}},
			},
			Status: corev1.NodeStatus{
				Capacity: corev1.ResourceList{
					corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("8"),
				},
				Allocatable: corev1.ResourceList{
					corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("6"),
				},
				Conditions: []corev1.NodeCondition{{
					Type: corev1.NodeReady, Status: corev1.ConditionTrue,
				}},
			},
		},
		&coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:       "kube-node-lease",
				Name:            "owned-node",
				UID:             types.UID("lease-uid"),
				ResourceVersion: "13",
				Labels:          ownedLabels,
			},
			Spec: coordinationv1.LeaseSpec{
				HolderIdentity:       &holder,
				LeaseDurationSeconds: &duration,
				RenewTime:            &renewTime,
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "team-a", Name: "training", UID: types.UID("pod-uid"),
			},
			Spec: corev1.PodSpec{
				NodeName: "owned-node",
				Containers: []corev1.Container{
					{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
						corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("2"),
					}}},
					{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
						corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
					}}},
				},
				InitContainers: []corev1.Container{{
					Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
						corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("4"),
					}},
				}},
				Overhead: corev1.ResourceList{
					corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
				},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "team-b", Name: "real-workload", UID: types.UID("real-pod-uid"),
			},
			Spec: corev1.PodSpec{NodeName: "real-node"},
		},
	)
	adapter := clusterkubernetes.NewAdapter(kubernetesClient)
	graph, err := adapter.Observe(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	var node *cluster.ObservedNodeState
	var lease *cluster.ObservedLeaseState
	for _, object := range graph.Objects {
		switch object.Key.Kind() {
		case cluster.ObjectKindNode:
			node = object.Node
		case cluster.ObjectKindLease:
			lease = object.Lease
		}
	}
	if node == nil ||
		!node.Unschedulable ||
		!node.Ready ||
		node.Annotations["kwok.x-k8s.io/node"] != "fake" ||
		node.Capacity["nvidia.com/gpu"] != "8" ||
		node.Allocatable["nvidia.com/gpu"] != "6" ||
		len(node.Taints) != 1 {
		t.Fatalf("unexpected observed Node state: %#v", node)
	}
	if lease == nil ||
		lease.HolderIdentity != holder ||
		lease.LeaseDurationSeconds != duration ||
		!lease.RenewTime.Equal(renewTime.Time) {
		t.Fatalf("unexpected observed Lease state: %#v", lease)
	}
	if len(graph.Pods) != 1 ||
		graph.Pods[0].Namespace != "team-a" ||
		graph.Pods[0].Name != "training" ||
		graph.Pods[0].NodeName != "owned-node" ||
		graph.Pods[0].Phase != "Running" ||
		graph.Pods[0].Requested["nvidia.com/gpu"] != "5" {
		t.Fatalf("unexpected observed Pod state: %#v", graph.Pods)
	}
}

func TestAdapterPaginatesOwnedObservationUntilTheServerCursorCloses(t *testing.T) {
	t.Parallel()

	scope := ownershipScope(t, 3)
	kubernetesClient := kubernetesfake.NewSimpleClientset()
	nodeListCalls := 0
	kubernetesClient.Fake.PrependReactor(
		"list",
		"nodes",
		func(action clienttesting.Action) (bool, runtime.Object, error) {
			options := action.(interface {
				GetListOptions() metav1.ListOptions
			}).GetListOptions()
			nodeListCalls++
			switch nodeListCalls {
			case 1:
				if options.Continue != "" || options.Limit != 200 {
					t.Fatalf("first page options = %#v", options)
				}
				return true, &corev1.NodeList{
					ListMeta: metav1.ListMeta{Continue: "next-page"},
					Items: []corev1.Node{ownedNode(
						"page-one",
						"node-1",
						"11",
						scope,
					)},
				}, nil
			case 2:
				if options.Continue != "next-page" || options.Limit != 200 {
					t.Fatalf("second page options = %#v", options)
				}
				return true, &corev1.NodeList{
					Items: []corev1.Node{ownedNode(
						"page-two",
						"node-2",
						"12",
						scope,
					)},
				}, nil
			default:
				t.Fatalf("unexpected Node list call %d", nodeListCalls)
				return true, nil, nil
			}
		},
	)
	adapter := clusterkubernetes.NewAdapter(kubernetesClient)
	graph, err := adapter.Observe(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if nodeListCalls != 2 || len(graph.Objects) != 2 {
		t.Fatalf("pagination calls=%d graph=%#v", nodeListCalls, graph)
	}
}

func TestAdapterRevalidatesOwnershipAndHonorsServerDryRunDelete(t *testing.T) {
	t.Parallel()

	scope := ownershipScope(t, 2)
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "synthetic-node",
			UID:             types.UID("node-uid"),
			ResourceVersion: "9",
			Labels:          ownershipLabels(scope),
		},
	}
	kubernetesClient := kubernetesfake.NewSimpleClientset(node)
	kubernetesClient.Fake.PrependReactor(
		"delete",
		"nodes",
		func(action clienttesting.Action) (bool, runtime.Object, error) {
			options := action.(clienttesting.DeleteAction).GetDeleteOptions()
			if slices.Contains(options.DryRun, metav1.DryRunAll) {
				return true, nil, nil
			}
			return false, nil, nil
		},
	)
	adapter := clusterkubernetes.NewAdapter(kubernetesClient)
	change := ownedNodeDeletion(t)
	dryRun, err := cluster.NewOwnedChangeSet(
		scope,
		cluster.ExecutionServerDryRun,
		[]cluster.OwnedChange{change},
	)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := adapter.Execute(context.Background(), dryRun)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.DryRun || receipt.Persisted != 0 {
		t.Fatalf("unexpected dry-run receipt: %#v", receipt)
	}
	if _, err := kubernetesClient.CoreV1().Nodes().Get(
		context.Background(),
		"synthetic-node",
		metav1.GetOptions{},
	); err != nil {
		t.Fatalf("server dry-run removed the object: %v", err)
	}

	persistent, err := cluster.NewOwnedChangeSet(
		scope,
		cluster.ExecutionPersistent,
		[]cluster.OwnedChange{change},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Execute(context.Background(), persistent); err != nil {
		t.Fatal(err)
	}
	if _, err := kubernetesClient.CoreV1().Nodes().Get(
		context.Background(),
		"synthetic-node",
		metav1.GetOptions{},
	); !apierrors.IsNotFound(err) {
		t.Fatalf("persistent delete error = %v, want NotFound", err)
	}

	foreign := node.DeepCopy()
	foreign.ResourceVersion = "10"
	foreign.Labels = nil
	if _, err := kubernetesClient.CoreV1().Nodes().Create(
		context.Background(),
		foreign,
		metav1.CreateOptions{},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Execute(context.Background(), persistent); cluster.ErrorCodeOf(err) !=
		cluster.ErrorOwnershipConflict {
		t.Fatalf("foreign object error = %v, want OwnershipConflict", err)
	}
}

func TestAdapterCreatesNodeAndLeaseWithScenarioInstanceOwnerReference(t *testing.T) {
	t.Parallel()

	scope := namedOwnershipScope(t, 2)
	kubernetesClient := kubernetesfake.NewSimpleClientset()
	adapter := clusterkubernetes.NewAdapter(kubernetesClient)
	nodeKey, err := cluster.NewObjectKey(
		cluster.ObjectKindNode,
		"",
		"owned-node",
	)
	if err != nil {
		t.Fatal(err)
	}
	nodeChange, err := cluster.NewApplySyntheticNode(
		nodeKey,
		cluster.ObjectPreconditions{},
		cluster.SyntheticNodeInput{Unschedulable: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	executeSingle(t, adapter, scope, cluster.ExecutionPersistent, nodeChange)

	leaseKey, err := cluster.NewObjectKey(
		cluster.ObjectKindLease,
		"kube-node-lease",
		"owned-node",
	)
	if err != nil {
		t.Fatal(err)
	}
	leaseChange, err := cluster.NewApplyLease(
		leaseKey,
		cluster.ObjectPreconditions{},
		cluster.LeaseInput{
			HolderIdentity:       "owned-node",
			LeaseDurationSeconds: 40,
			RenewTime:            time.Date(2026, 7, 30, 7, 0, 0, 0, time.UTC),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	executeSingle(t, adapter, scope, cluster.ExecutionPersistent, leaseChange)

	node, err := kubernetesClient.CoreV1().Nodes().Get(
		context.Background(),
		"owned-node",
		metav1.GetOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := kubernetesClient.CoordinationV1().
		Leases("kube-node-lease").
		Get(context.Background(), "owned-node", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, references := range [][]metav1.OwnerReference{
		node.OwnerReferences,
		lease.OwnerReferences,
	} {
		if len(references) != 1 ||
			references[0].APIVersion != "simulation.kasim.io/v1alpha1" ||
			references[0].Kind != "ScenarioInstance" ||
			references[0].Name != "training-lab" ||
			string(references[0].UID) != scope.InstanceUID().String() ||
			references[0].Controller == nil ||
			!*references[0].Controller ||
			references[0].BlockOwnerDeletion == nil ||
			!*references[0].BlockOwnerDeletion {
			t.Fatalf("strongest legal owner reference missing: %#v", references)
		}
	}
}

func TestAdapterNamedScopeRejectsOwnedLabelsWithoutExactOwnerReference(t *testing.T) {
	t.Parallel()

	scope := namedOwnershipScope(t, 2)
	kubernetesClient := kubernetesfake.NewSimpleClientset(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "forged-owned-node",
			UID:             types.UID("node-uid"),
			ResourceVersion: "11",
			Labels: map[string]string{
				cluster.ManagedByLabel:         cluster.ManagedByValue,
				cluster.InstanceUIDLabel:       scope.InstanceUID().String(),
				cluster.DesiredGenerationLabel: "2",
			},
		},
	})
	adapter := clusterkubernetes.NewAdapter(kubernetesClient)
	if _, err := adapter.Observe(
		context.Background(),
		scope,
	); cluster.ErrorCodeOf(err) != cluster.ErrorOwnershipConflict {
		t.Fatalf("missing exact owner reference error = %v", err)
	}
}

func TestEnvtestServerDryRunApplyStatusAndDeleteHaveExactPersistence(t *testing.T) {
	assets := os.Getenv("KUBEBUILDER_ASSETS")
	if assets == "" {
		t.Skip("KUBEBUILDER_ASSETS is required for the pinned envtest lane")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	environment := &envtest.Environment{
		BinaryAssetsDirectory: assets,
		CRDDirectoryPaths: []string{
			filepath.Join(root, "config", "crd", "bases"),
		},
		ErrorIfCRDPathMissing: true,
	}
	configuration, err := environment.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := environment.Stop(); err != nil {
			t.Errorf("stop envtest: %v", err)
		}
	})
	kubernetesClient, err := kubernetes.NewForConfig(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kubernetesClient.CoreV1().Namespaces().Create(
		context.Background(),
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-node-lease"}},
		metav1.CreateOptions{},
	); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	adapter := clusterkubernetes.NewAdapter(kubernetesClient)
	capabilities, err := adapter.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.KubernetesMinor != 36 {
		t.Fatalf("envtest Kubernetes minor = %d", capabilities.KubernetesMinor)
	}
	scope := namedOwnershipScope(t, 2)
	nodeKey, err := cluster.NewObjectKey(
		cluster.ObjectKindNode,
		"",
		"envtest-synthetic-node",
	)
	if err != nil {
		t.Fatal(err)
	}
	nodeCreate, err := cluster.NewApplySyntheticNode(
		nodeKey,
		cluster.ObjectPreconditions{},
		cluster.SyntheticNodeInput{
			Labels:        map[string]string{"workload.example.com/class": "training"},
			Annotations:   map[string]string{"simulation.kasim.io/test": "envtest"},
			Taints:        []cluster.NodeTaint{{Key: "accelerator", Value: "simulated", Effect: "NoSchedule"}},
			Unschedulable: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	executeSingle(t, adapter, scope, cluster.ExecutionServerDryRun, nodeCreate)
	if _, err := kubernetesClient.CoreV1().Nodes().Get(
		context.Background(),
		nodeKey.Name(),
		metav1.GetOptions{},
	); !apierrors.IsNotFound(err) {
		t.Fatalf("dry-run Node create error = %v, want NotFound", err)
	}
	executeSingle(t, adapter, scope, cluster.ExecutionPersistent, nodeCreate)
	node, err := kubernetesClient.CoreV1().Nodes().Get(
		context.Background(),
		nodeKey.Name(),
		metav1.GetOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if node.Labels[cluster.InstanceUIDLabel] != scope.InstanceUID().String() ||
		node.Labels[cluster.DesiredGenerationLabel] != "2" ||
		!node.Spec.Unschedulable {
		t.Fatalf("persisted Node lost owned intent: %#v", node)
	}

	nodeUpdate, err := cluster.NewApplySyntheticNode(
		nodeKey,
		cluster.ObjectPreconditions{
			UID:             string(node.UID),
			ResourceVersion: node.ResourceVersion,
		},
		cluster.SyntheticNodeInput{
			Labels:        map[string]string{"workload.example.com/class": "updated"},
			Unschedulable: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	executeSingle(t, adapter, scope, cluster.ExecutionPersistent, nodeUpdate)
	node, err = kubernetesClient.CoreV1().Nodes().Get(
		context.Background(),
		nodeKey.Name(),
		metav1.GetOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if node.Labels["workload.example.com/class"] != "updated" {
		t.Fatalf("server-side apply did not update the owned Node: %#v", node.Labels)
	}

	statusKey, err := cluster.NewObjectKey(
		cluster.ObjectKindNodeStatus,
		"",
		nodeKey.Name(),
	)
	if err != nil {
		t.Fatal(err)
	}
	statusChange, err := cluster.NewUpdateSyntheticNodeStatus(
		statusKey,
		cluster.ObjectPreconditions{
			UID:             string(node.UID),
			ResourceVersion: node.ResourceVersion,
		},
		cluster.SyntheticNodeStatusInput{
			Capacity: map[string]string{
				"cpu":            "4",
				"memory":         "8Gi",
				"nvidia.com/gpu": "8",
			},
			Allocatable: map[string]string{
				"cpu":            "4",
				"memory":         "8Gi",
				"nvidia.com/gpu": "6",
			},
			ManageReady: true,
			Ready:       true,
			ObservedAt:  time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	executeSingle(t, adapter, scope, cluster.ExecutionServerDryRun, statusChange)
	node, err = kubernetesClient.CoreV1().Nodes().Get(
		context.Background(),
		nodeKey.Name(),
		metav1.GetOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if node.Status.Capacity != nil {
		t.Fatal("dry-run status apply persisted capacity")
	}
	statusChange, err = cluster.NewUpdateSyntheticNodeStatus(
		statusKey,
		cluster.ObjectPreconditions{
			UID:             string(node.UID),
			ResourceVersion: node.ResourceVersion,
		},
		cluster.SyntheticNodeStatusInput{
			Capacity:    map[string]string{"nvidia.com/gpu": "8"},
			Allocatable: map[string]string{"nvidia.com/gpu": "6"},
			ManageReady: true,
			Ready:       true,
			ObservedAt:  time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	executeSingle(t, adapter, scope, cluster.ExecutionPersistent, statusChange)
	node, err = kubernetesClient.CoreV1().Nodes().Get(
		context.Background(),
		nodeKey.Name(),
		metav1.GetOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if node.Status.Capacity.Name("nvidia.com/gpu", resource.DecimalSI).String() != "8" ||
		node.Status.Allocatable.Name("nvidia.com/gpu", resource.DecimalSI).String() != "6" {
		t.Fatalf("status apply did not persist exact resources: %#v", node.Status)
	}
	statusChange, err = cluster.NewUpdateSyntheticNodeStatus(
		statusKey,
		cluster.ObjectPreconditions{
			UID:             string(node.UID),
			ResourceVersion: node.ResourceVersion,
		},
		cluster.SyntheticNodeStatusInput{
			Capacity: map[string]string{
				"nvidia.com/gpu": "8",
				"amd.com/gpu":    "4",
			},
			Allocatable: map[string]string{
				"nvidia.com/gpu": "6",
				"amd.com/gpu":    "4",
			},
			ObservedAt: time.Date(2026, 7, 30, 6, 1, 0, 0, time.UTC),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	executeSingle(t, adapter, scope, cluster.ExecutionPersistent, statusChange)
	node, err = kubernetesClient.CoreV1().Nodes().Get(
		context.Background(),
		nodeKey.Name(),
		metav1.GetOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	statusChange, err = cluster.NewUpdateSyntheticNodeStatus(
		statusKey,
		cluster.ObjectPreconditions{
			UID:             string(node.UID),
			ResourceVersion: node.ResourceVersion,
		},
		cluster.SyntheticNodeStatusInput{
			Capacity:    map[string]string{"nvidia.com/gpu": "8"},
			Allocatable: map[string]string{"nvidia.com/gpu": "6"},
			ObservedAt:  time.Date(2026, 7, 30, 6, 2, 0, 0, time.UTC),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	executeSingle(t, adapter, scope, cluster.ExecutionPersistent, statusChange)
	node, err = kubernetesClient.CoreV1().Nodes().Get(
		context.Background(),
		nodeKey.Name(),
		metav1.GetOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := node.Status.Capacity["amd.com/gpu"]; found {
		t.Fatalf("status apply retained stale capacity: %#v", node.Status.Capacity)
	}
	if _, found := node.Status.Allocatable["amd.com/gpu"]; found {
		t.Fatalf(
			"status apply retained stale allocatable: %#v",
			node.Status.Allocatable,
		)
	}

	leaseKey, err := cluster.NewObjectKey(
		cluster.ObjectKindLease,
		"kube-node-lease",
		nodeKey.Name(),
	)
	if err != nil {
		t.Fatal(err)
	}
	leaseCreate, err := cluster.NewApplyLease(
		leaseKey,
		cluster.ObjectPreconditions{},
		cluster.LeaseInput{
			HolderIdentity:       nodeKey.Name(),
			LeaseDurationSeconds: 40,
			RenewTime:            time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	executeSingle(t, adapter, scope, cluster.ExecutionPersistent, leaseCreate)
	lease, err := kubernetesClient.CoordinationV1().
		Leases(leaseKey.Namespace()).
		Get(context.Background(), leaseKey.Name(), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if lease.Labels[cluster.InstanceUIDLabel] != scope.InstanceUID().String() ||
		lease.Spec.HolderIdentity == nil ||
		*lease.Spec.HolderIdentity != nodeKey.Name() {
		t.Fatalf("persisted Lease lost owned intent: %#v", lease)
	}

	nodeDeletion, err := cluster.NewDeleteOwnedObject(
		nodeKey,
		cluster.ObjectPreconditions{
			UID:             string(node.UID),
			ResourceVersion: node.ResourceVersion,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	executeSingle(t, adapter, scope, cluster.ExecutionServerDryRun, nodeDeletion)
	if _, err := kubernetesClient.CoreV1().Nodes().Get(
		context.Background(),
		nodeKey.Name(),
		metav1.GetOptions{},
	); err != nil {
		t.Fatalf("dry-run delete removed Node: %v", err)
	}
	executeSingle(t, adapter, scope, cluster.ExecutionPersistent, nodeDeletion)
	if _, err := kubernetesClient.CoreV1().Nodes().Get(
		context.Background(),
		nodeKey.Name(),
		metav1.GetOptions{},
	); !apierrors.IsNotFound(err) {
		t.Fatalf("persistent delete error = %v, want NotFound", err)
	}
}

func configureDiscovery(
	kubernetesClient *kubernetesfake.Clientset,
	minor string,
) {
	fakeDiscovery := kubernetesClient.Discovery().(*discoveryfake.FakeDiscovery)
	fakeDiscovery.FakedServerVersion = &version.Info{
		Major:      "1",
		Minor:      minor,
		GitVersion: "v1." + minor + ".3",
	}
	fakeDiscovery.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: "v1",
			APIResources: []metav1.APIResource{
				{Name: "namespaces"},
				{Name: "nodes"},
				{Name: "pods", Namespaced: true},
			},
		},
		{
			GroupVersion: "coordination.k8s.io/v1",
			APIResources: []metav1.APIResource{{Name: "leases", Namespaced: true}},
		},
		{
			GroupVersion: "authorization.k8s.io/v1",
			APIResources: []metav1.APIResource{{Name: "selfsubjectaccessreviews"}},
		},
		{
			GroupVersion: "simulation.kasim.io/v1alpha1",
			APIResources: []metav1.APIResource{
				{Name: "scenarioinstances"},
				{Name: "scenarioinstances/status"},
			},
		},
	}
}

func ownershipScope(t *testing.T, value int64) cluster.OwnershipScope {
	t.Helper()
	uid, err := domain.ParseInstanceUID("6cb2dd6f-c608-4e79-aaf6-e3fa1287f73c")
	if err != nil {
		t.Fatal(err)
	}
	generation, err := domain.NewGeneration(value)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := cluster.NewOwnershipScope(uid, generation)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func namedOwnershipScope(t *testing.T, value int64) cluster.OwnershipScope {
	t.Helper()
	uid, err := domain.ParseInstanceUID("6cb2dd6f-c608-4e79-aaf6-e3fa1287f73c")
	if err != nil {
		t.Fatal(err)
	}
	generation, err := domain.NewGeneration(value)
	if err != nil {
		t.Fatal(err)
	}
	name, err := domain.ParseName("training-lab")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := cluster.NewInstanceOwnershipScope(name, uid, generation)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func ownershipLabels(scope cluster.OwnershipScope) map[string]string {
	return map[string]string{
		cluster.ManagedByLabel:         cluster.ManagedByValue,
		cluster.InstanceUIDLabel:       scope.InstanceUID().String(),
		cluster.DesiredGenerationLabel: "3",
	}
}

func ownedNode(
	name, uid, resourceVersion string,
	scope cluster.OwnershipScope,
) corev1.Node {
	return corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:            name,
		UID:             types.UID(uid),
		ResourceVersion: resourceVersion,
		Labels:          ownershipLabels(scope),
	}}
}

func ownedNodeDeletion(t *testing.T) cluster.OwnedChange {
	t.Helper()
	key, err := cluster.NewObjectKey(
		cluster.ObjectKindNode,
		"",
		"synthetic-node",
	)
	if err != nil {
		t.Fatal(err)
	}
	change, err := cluster.NewDeleteOwnedObject(
		key,
		cluster.ObjectPreconditions{
			UID:             "node-uid",
			ResourceVersion: "9",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return change
}

func executeSingle(
	t *testing.T,
	adapter cluster.Port,
	scope cluster.OwnershipScope,
	mode cluster.ExecutionMode,
	change cluster.OwnedChange,
) {
	t.Helper()
	changeSet, err := cluster.NewOwnedChangeSet(
		scope,
		mode,
		[]cluster.OwnedChange{change},
	)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := adapter.Execute(context.Background(), changeSet)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Attempted != 1 ||
		(mode == cluster.ExecutionServerDryRun) != receipt.DryRun {
		t.Fatalf("unexpected mutation receipt: %#v", receipt)
	}
}
