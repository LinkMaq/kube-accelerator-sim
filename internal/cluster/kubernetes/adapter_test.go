package kubernetes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
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
	"k8s.io/client-go/rest"
	clienttesting "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	clusterkubernetes "github.com/LinkMaq/kube-accelerator-sim/internal/cluster/kubernetes"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return roundTrip(request)
}

type leaseTransportReaction func(
	context.Context,
	string,
) (*metav1.Status, error)

func newLeaseTransportClient(
	t *testing.T,
	reaction leaseTransportReaction,
) kubernetes.Interface {
	t.Helper()

	kubernetesClient, err := kubernetes.NewForConfig(&rest.Config{
		Host: "https://cluster.example.test",
		ContentConfig: rest.ContentConfig{
			AcceptContentTypes: runtime.ContentTypeJSON,
			ContentType:        runtime.ContentTypeJSON,
		},
		Transport: roundTripFunc(func(
			request *http.Request,
		) (*http.Response, error) {
			if request.Body != nil {
				defer request.Body.Close()
			}
			if request.Method != http.MethodPost ||
				request.URL.Path != "/apis/coordination.k8s.io/v1/namespaces/kube-node-lease/leases" {
				t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Header:     http.Header{"Content-Type": []string{runtime.ContentTypeJSON}},
					Body: io.NopCloser(bytes.NewBufferString(
						`{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"NotFound","code":404}`,
					)),
					Request: request,
				}, nil
			}
			lease := &coordinationv1.Lease{}
			if decodeErr := json.NewDecoder(request.Body).Decode(lease); decodeErr != nil {
				return nil, decodeErr
			}
			status, reactionErr := reaction(request.Context(), lease.Name)
			if reactionErr != nil {
				return nil, reactionErr
			}
			statusCode := http.StatusCreated
			var responseObject any = lease
			if status != nil {
				statusCode = int(status.Code)
				responseObject = status
			} else {
				lease.TypeMeta = metav1.TypeMeta{
					APIVersion: "coordination.k8s.io/v1",
					Kind:       "Lease",
				}
				lease.UID = types.UID("created-" + lease.Name)
				lease.ResourceVersion = "1"
			}
			payload, encodeErr := json.Marshal(responseObject)
			if encodeErr != nil {
				return nil, encodeErr
			}
			return &http.Response{
				StatusCode: statusCode,
				Header: http.Header{
					"Content-Type": []string{runtime.ContentTypeJSON},
				},
				Body:    io.NopCloser(bytes.NewReader(payload)),
				Request: request,
			}, nil
		}),
		QPS:   1000,
		Burst: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	return kubernetesClient
}

func leaseFailureStatus(
	code int32,
	reason metav1.StatusReason,
	message string,
) *metav1.Status {
	return &metav1.Status{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"},
		Status:   metav1.StatusFailure,
		Message:  message,
		Reason:   reason,
		Code:     code,
	}
}

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
		) ||
		!slices.ContainsFunc(
			capabilities.Resources,
			func(capability cluster.ResourceCapability) bool {
				return capability.GroupVersion == "resource.k8s.io/v1" &&
					capability.Resource == "resourceclaims" &&
					capability.Namespaced &&
					slices.Contains(capability.Verbs, "watch")
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

func TestAdapterObservesBoundPodsWithBoundedConcurrency(t *testing.T) {
	t.Parallel()

	scope := ownershipScope(t, 3)
	ownedLabels := ownershipLabels(scope)
	nodes := make([]corev1.Node, 0, 64)
	for index := 0; index < 64; index++ {
		nodes = append(nodes, corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:            fmt.Sprintf("owned-node-%02d", index),
				UID:             types.UID(fmt.Sprintf("node-uid-%02d", index)),
				ResourceVersion: strconv.Itoa(index + 1),
				Labels:          ownedLabels,
			},
		})
	}
	var active atomic.Int32
	var peak atomic.Int32
	started := make(chan struct{}, 64)
	release := make(chan struct{})
	kubernetesClient, err := kubernetes.NewForConfig(&rest.Config{
		Host: "https://cluster.example.test",
		ContentConfig: rest.ContentConfig{
			AcceptContentTypes: runtime.ContentTypeJSON,
			ContentType:        runtime.ContentTypeJSON,
		},
		Transport: roundTripFunc(func(
			request *http.Request,
		) (*http.Response, error) {
			var responseObject any
			switch request.URL.Path {
			case "/api/v1/nodes":
				responseObject = &corev1.NodeList{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "v1",
						Kind:       "NodeList",
					},
					Items: nodes,
				}
			case "/apis/coordination.k8s.io/v1/namespaces/kube-node-lease/leases":
				responseObject = &coordinationv1.LeaseList{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "coordination.k8s.io/v1",
						Kind:       "LeaseList",
					},
				}
			case "/api/v1/pods":
				fieldSelector := request.URL.Query().Get("fieldSelector")
				const prefix = "spec.nodeName="
				if !strings.HasPrefix(fieldSelector, prefix) {
					return nil, fmt.Errorf(
						"unexpected Pod field selector %q",
						fieldSelector,
					)
				}
				nodeName := strings.TrimPrefix(fieldSelector, prefix)
				items := []corev1.Pod{}
				if nodeName == "owned-node-63" {
					items = append(items, corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "team-a",
							Name:      "owned-workload",
							UID:       types.UID("owned-pod-uid"),
						},
						Spec: corev1.PodSpec{NodeName: nodeName},
					})
				}
				responseObject = &corev1.PodList{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "v1",
						Kind:       "PodList",
					},
					Items: items,
				}
				current := active.Add(1)
				for {
					observed := peak.Load()
					if current <= observed || peak.CompareAndSwap(observed, current) {
						break
					}
				}
				started <- struct{}{}
				<-release
				active.Add(-1)
			default:
				return nil, fmt.Errorf(
					"unexpected observation request %s",
					request.URL.String(),
				)
			}
			payload, marshalErr := json.Marshal(responseObject)
			if marshalErr != nil {
				return nil, marshalErr
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": []string{runtime.ContentTypeJSON},
				},
				Body:    io.NopCloser(bytes.NewReader(payload)),
				Request: request,
			}, nil
		}),
		QPS:   1000,
		Burst: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}

	adapter := clusterkubernetes.NewAdapter(kubernetesClient)
	type observationResult struct {
		graph cluster.ObservedGraph
		err   error
	}
	completed := make(chan observationResult, 1)
	go func() {
		graph, observeErr := adapter.Observe(context.Background(), scope)
		completed <- observationResult{graph: graph, err: observeErr}
	}()
	for observed := 0; observed < 4; observed++ {
		select {
		case <-started:
		case result := <-completed:
			close(release)
			t.Fatalf(
				"Pod observation completed before concurrent progress: graph %#v, error %v",
				result.graph,
				result.err,
			)
		case <-time.After(time.Second):
			close(release)
			t.Fatalf(
				"Pod observation did not make bounded concurrent progress: peak %d",
				peak.Load(),
			)
		}
	}
	close(release)
	result := <-completed
	if result.err != nil {
		t.Fatal(result.err)
	}
	graph := result.graph
	if len(graph.Pods) != 1 ||
		graph.Pods[0].Namespace != "team-a" ||
		graph.Pods[0].Name != "owned-workload" ||
		graph.Pods[0].NodeName != "owned-node-63" {
		t.Fatalf("unexpected exact-owned Pod observation: %#v", graph.Pods)
	}
	if peak.Load() < 4 || peak.Load() > 32 {
		t.Fatalf("bounded Pod observation peak = %d, want 4..32", peak.Load())
	}
}

func TestAdapterPaginatesBoundPodObservationUntilTheServerCursorCloses(
	t *testing.T,
) {
	t.Parallel()

	scope := ownershipScope(t, 3)
	kubernetesClient := kubernetesfake.NewSimpleClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{
			Name:            "owned-node",
			UID:             types.UID("node-uid"),
			ResourceVersion: "11",
			Labels:          ownershipLabels(scope),
		}},
	)
	podListCalls := 0
	kubernetesClient.Fake.PrependReactor(
		"list",
		"pods",
		func(action clienttesting.Action) (bool, runtime.Object, error) {
			options := action.(interface {
				GetListOptions() metav1.ListOptions
			}).GetListOptions()
			podListCalls++
			if options.FieldSelector != "spec.nodeName=owned-node" ||
				options.Limit != 200 {
				t.Fatalf("Pod page options = %#v", options)
			}
			switch podListCalls {
			case 1:
				if options.Continue != "" {
					t.Fatalf("first Pod page options = %#v", options)
				}
				return true, &corev1.PodList{
					ListMeta: metav1.ListMeta{Continue: "next-page"},
					Items: []corev1.Pod{{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "team-b",
							Name:      "unrelated",
						},
						Spec: corev1.PodSpec{NodeName: "real-node"},
					}},
				}, nil
			case 2:
				if options.Continue != "next-page" {
					t.Fatalf("second Pod page options = %#v", options)
				}
				return true, &corev1.PodList{Items: []corev1.Pod{{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "team-a",
						Name:      "owned-workload",
						UID:       types.UID("owned-pod-uid"),
					},
					Spec: corev1.PodSpec{NodeName: "owned-node"},
				}}}, nil
			default:
				t.Fatalf("unexpected Pod list call %d", podListCalls)
				return true, nil, nil
			}
		},
	)

	graph, err := clusterkubernetes.NewAdapter(kubernetesClient).
		Observe(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if podListCalls != 2 ||
		len(graph.Pods) != 1 ||
		graph.Pods[0].Namespace != "team-a" ||
		graph.Pods[0].Name != "owned-workload" {
		t.Fatalf(
			"Pod pagination calls=%d graph=%#v",
			podListCalls,
			graph,
		)
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

func TestAdapterObservesOwnedStableDRAInventoryAndSchedulerClaimEvidence(
	t *testing.T,
) {
	t.Parallel()

	scope := ownershipScope(t, 3)
	draFidelity, err := domain.ParseFidelityMode("dra-control-plane")
	if err != nil {
		t.Fatal(err)
	}
	scope, err = scope.ForFidelity(draFidelity)
	if err != nil {
		t.Fatal(err)
	}
	ownedLabels := ownershipLabels(scope)
	trueValue := true
	allocatable := true
	className := "kasim-class-a"
	poolName := "kasim-pool-a"
	deviceName := "kasim-device-a"
	kubernetesClient := kubernetesfake.NewSimpleClientset(
		&resourcev1.DeviceClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:            className,
				UID:             types.UID("class-uid"),
				ResourceVersion: "21",
				Labels:          ownedLabels,
			},
			Spec: resourcev1.DeviceClassSpec{
				Selectors: []resourcev1.DeviceSelector{{
					CEL: &resourcev1.CELDeviceSelector{
						Expression: `device.driver == "gpu.nvidia.com"`,
					},
				}},
			},
		},
		&resourcev1.ResourceSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "kasim-slice-a",
				UID:             types.UID("slice-uid"),
				ResourceVersion: "22",
				Labels:          ownedLabels,
			},
			Spec: resourcev1.ResourceSliceSpec{
				Driver: "gpu.nvidia.com",
				Pool: resourcev1.ResourcePool{
					Name: poolName, Generation: 3, ResourceSliceCount: 1,
				},
				NodeName: &[]string{"kasim-node-a"}[0],
				Devices: []resourcev1.Device{{
					Name: deviceName,
					Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
						"simulation.kasim.io/simulated": {
							BoolValue: &trueValue,
						},
						"simulation.kasim.io/allocatable": {
							BoolValue: &allocatable,
						},
					},
				}},
			},
		},
		&resourcev1.ResourceClaim{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:       "team-a",
				Name:            "training",
				UID:             types.UID("claim-uid"),
				ResourceVersion: "23",
			},
			Spec: resourcev1.ResourceClaimSpec{
				Devices: resourcev1.DeviceClaim{
					Requests: []resourcev1.DeviceRequest{{
						Name: "accelerator",
						Exactly: &resourcev1.ExactDeviceRequest{
							DeviceClassName: className,
						},
					}},
				},
			},
			Status: resourcev1.ResourceClaimStatus{
				Allocation: &resourcev1.AllocationResult{
					Devices: resourcev1.DeviceAllocationResult{
						Results: []resourcev1.DeviceRequestAllocationResult{{
							Request: "accelerator",
							Driver:  "gpu.nvidia.com",
							Pool:    poolName,
							Device:  deviceName,
						}},
					},
				},
				ReservedFor: []resourcev1.ResourceClaimConsumerReference{{
					Resource: "pods",
					Name:     "workload",
					UID:      types.UID("pod-uid"),
				}},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "team-a",
				Name:      "workload",
				UID:       types.UID("pod-uid"),
			},
			Spec: corev1.PodSpec{
				ResourceClaims: []corev1.PodResourceClaim{{
					Name:              "accelerator",
					ResourceClaimName: &[]string{"training"}[0],
				}},
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
			return object.Key.Kind() == cluster.ObjectKindDeviceClass &&
				object.DeviceClass != nil &&
				slices.Equal(
					object.DeviceClass.Selectors,
					[]string{`device.driver == "gpu.nvidia.com"`},
				)
		}) ||
		!slices.ContainsFunc(graph.Objects, func(object cluster.ObservedObject) bool {
			return object.Key.Kind() == cluster.ObjectKindResourceSlice &&
				object.ResourceSlice != nil &&
				object.ResourceSlice.Driver == "gpu.nvidia.com" &&
				object.ResourceSlice.PoolName == poolName &&
				len(object.ResourceSlice.Devices) == 1
		}) {
		t.Fatalf("unexpected stable DRA observation: %#v", graph.Objects)
	}
	if len(graph.ResourceClaims) != 1 ||
		!slices.Equal(graph.ResourceClaims[0].DeviceClassNames, []string{className}) ||
		len(graph.ResourceClaims[0].Allocations) != 1 ||
		graph.ResourceClaims[0].Allocations[0].Device != deviceName ||
		len(graph.ResourceClaims[0].ReservedFor) != 1 ||
		graph.ResourceClaims[0].ReservedFor[0].UID != "pod-uid" {
		t.Fatalf("unexpected ResourceClaim observation: %#v", graph.ResourceClaims)
	}
	if len(graph.Pods) != 1 ||
		graph.Pods[0].Namespace != "team-a" ||
		graph.Pods[0].Name != "workload" ||
		graph.Pods[0].UID != "pod-uid" ||
		!slices.Equal(graph.Pods[0].ResourceClaims, []string{"training"}) {
		t.Fatalf("pending DRA Pod was not observed by exact claim reference: %#v", graph.Pods)
	}
}

func TestAdapterPersistsOnlyPortableStableDRAFields(t *testing.T) {
	t.Parallel()

	scope := ownershipScope(t, 3)
	kubernetesClient := kubernetesfake.NewSimpleClientset()
	adapter := clusterkubernetes.NewAdapter(kubernetesClient)
	classKey, _ := cluster.NewObjectKey(
		cluster.ObjectKindDeviceClass,
		"",
		"kasim-class-a",
	)
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
	sliceKey, _ := cluster.NewObjectKey(
		cluster.ObjectKindResourceSlice,
		"",
		"kasim-slice-a",
	)
	model, err := cluster.NewStringDeviceAttribute("nvidia-h100")
	if err != nil {
		t.Fatal(err)
	}
	sliceChange, err := cluster.NewApplyResourceSlice(
		sliceKey,
		cluster.ObjectPreconditions{},
		cluster.ResourceSliceInput{
			Driver:             "gpu.nvidia.com",
			PoolName:           "kasim-pool-a",
			PoolGeneration:     3,
			ResourceSliceCount: 1,
			NodeName:           "kasim-node-a",
			Devices: []cluster.DRADevice{{
				Name: "kasim-device-a",
				Attributes: map[string]cluster.DeviceAttributeValue{
					"simulation.kasim.io/simulated":   cluster.NewBoolDeviceAttribute(true),
					"simulation.kasim.io/allocatable": cluster.NewBoolDeviceAttribute(true),
					"simulation.kasim.io/model":       model,
				},
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []cluster.OwnedChange{classChange, sliceChange} {
		changeSet, err := cluster.NewOwnedChangeSet(
			scope,
			cluster.ExecutionPersistent,
			[]cluster.OwnedChange{change},
		)
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := adapter.Execute(context.Background(), changeSet)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.Persisted != 1 {
			t.Fatalf("stage receipt = %#v", receipt)
		}
	}
	class, err := kubernetesClient.ResourceV1().DeviceClasses().Get(
		context.Background(),
		classKey.Name(),
		metav1.GetOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(class.Spec.Selectors) != 1 ||
		class.Spec.Selectors[0].CEL == nil ||
		class.Spec.Selectors[0].CEL.Expression !=
			`device.driver == "gpu.nvidia.com"` ||
		len(class.Spec.Config) != 0 ||
		class.Spec.ExtendedResourceName != nil {
		t.Fatalf("DeviceClass escaped the portable v1 subset: %#v", class.Spec)
	}
	resourceSlice, err := kubernetesClient.ResourceV1().ResourceSlices().Get(
		context.Background(),
		sliceKey.Name(),
		metav1.GetOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resourceSlice.Spec.Driver != "gpu.nvidia.com" ||
		resourceSlice.Spec.Pool.Name != "kasim-pool-a" ||
		resourceSlice.Spec.NodeName == nil ||
		*resourceSlice.Spec.NodeName != "kasim-node-a" ||
		resourceSlice.Spec.NodeSelector != nil ||
		resourceSlice.Spec.AllNodes != nil ||
		resourceSlice.Spec.PerDeviceNodeSelection != nil ||
		len(resourceSlice.Spec.SharedCounters) != 0 ||
		len(resourceSlice.Spec.Devices) != 1 {
		t.Fatalf("ResourceSlice escaped the portable v1 subset: %#v", resourceSlice.Spec)
	}
	device := resourceSlice.Spec.Devices[0]
	if device.NodeName != nil ||
		device.NodeSelector != nil ||
		device.AllNodes != nil ||
		len(device.Taints) != 0 ||
		device.BindsToNode != nil ||
		len(device.BindingConditions) != 0 ||
		device.AllowMultipleAllocations != nil {
		t.Fatalf("Device escaped the portable v1 subset: %#v", device)
	}
	for _, action := range kubernetesClient.Actions() {
		if action.GetResource().Resource == "resourceclaims" &&
			action.GetVerb() != "get" &&
			action.GetVerb() != "list" &&
			action.GetVerb() != "watch" {
			t.Fatalf("adapter mutated ResourceClaim through action %#v", action)
		}
	}
}

func TestAdapterDRAResourceVersionDriftRemainsFailClosed(t *testing.T) {
	t.Parallel()

	scope := ownershipScope(t, 3)
	class := &resourcev1.DeviceClass{ObjectMeta: metav1.ObjectMeta{
		Name:            "kasim-class-a",
		UID:             types.UID("device-class-uid"),
		ResourceVersion: "10",
		Labels:          ownershipLabels(scope),
	}}
	kubernetesClient := kubernetesfake.NewSimpleClientset(class)
	adapter := clusterkubernetes.NewAdapter(kubernetesClient)
	key, err := cluster.NewObjectKey(
		cluster.ObjectKindDeviceClass,
		"",
		class.Name,
	)
	if err != nil {
		t.Fatal(err)
	}
	change, err := cluster.NewApplyDeviceClass(
		key,
		cluster.ObjectPreconditions{
			UID:             string(class.UID),
			ResourceVersion: "9",
		},
		cluster.DeviceClassInput{
			Selectors: []string{`device.driver == "gpu.nvidia.com"`},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	changeSet, err := cluster.NewOwnedChangeSet(
		scope,
		cluster.ExecutionPersistent,
		[]cluster.OwnedChange{change},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Execute(
		context.Background(),
		changeSet,
	); cluster.ErrorCodeOf(err) != cluster.ErrorResourceVersionConflict {
		t.Fatalf("DRA snapshot drift error = %v, want ResourceVersionConflict", err)
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

func TestAdapterExecutesIndependentOwnedMutationsWithBoundedConcurrency(
	t *testing.T,
) {
	t.Parallel()

	var active atomic.Int32
	var peak atomic.Int32
	started := make(chan struct{}, 64)
	release := make(chan struct{})
	kubernetesClient := newLeaseTransportClient(
		t,
		func(context.Context, string) (*metav1.Status, error) {
			current := active.Add(1)
			for {
				observed := peak.Load()
				if current <= observed || peak.CompareAndSwap(observed, current) {
					break
				}
			}
			started <- struct{}{}
			<-release
			active.Add(-1)
			return nil, nil
		},
	)
	const changeCount = 64
	changeSet := ownedLeaseChangeSet(t, changeCount)
	type executionResult struct {
		receipt cluster.MutationReceipt
		err     error
	}
	completed := make(chan executionResult, 1)
	go func() {
		receipt, executeErr := clusterkubernetes.NewAdapter(kubernetesClient).
			Execute(context.Background(), changeSet)
		completed <- executionResult{receipt: receipt, err: executeErr}
	}()
	for observed := 0; observed < 4; observed++ {
		select {
		case <-started:
		case result := <-completed:
			close(release)
			t.Fatalf(
				"owned mutation batch completed before concurrent progress: receipt %#v, error %v",
				result.receipt,
				result.err,
			)
		case <-time.After(time.Second):
			close(release)
			t.Fatalf(
				"owned mutation batch did not make bounded concurrent progress: peak %d",
				peak.Load(),
			)
		}
	}
	close(release)
	result := <-completed
	if result.err != nil {
		t.Fatal(result.err)
	}
	if result.receipt.Persisted != changeCount ||
		peak.Load() < 4 ||
		peak.Load() > 32 {
		t.Fatalf(
			"bounded mutation execution = receipt %#v, peak %d",
			result.receipt,
			peak.Load(),
		)
	}
}

func TestAdapterReturnsLowestInputErrorAndStopsBeforeTheNextWave(
	t *testing.T,
) {
	t.Parallel()

	var requests atomic.Int32
	zeroStarted := make(chan struct{})
	oneFailed := make(chan struct{})
	kubernetesClient := newLeaseTransportClient(
		t,
		func(_ context.Context, name string) (*metav1.Status, error) {
			requests.Add(1)
			switch name {
			case "concurrent-node-00":
				close(zeroStarted)
				<-oneFailed
				time.Sleep(50 * time.Millisecond)
				return leaseFailureStatus(
					http.StatusTooManyRequests,
					metav1.StatusReasonTooManyRequests,
					"lower input index was rate limited",
				), nil
			case "concurrent-node-01":
				<-zeroStarted
				close(oneFailed)
				return leaseFailureStatus(
					http.StatusForbidden,
					metav1.StatusReasonForbidden,
					"higher input index was forbidden",
				), nil
			default:
				return nil, nil
			}
		},
	)
	changeSet := ownedLeaseChangeSet(t, 64)
	receipt, err := clusterkubernetes.NewAdapter(kubernetesClient).
		Execute(context.Background(), changeSet)
	if cluster.ErrorCodeOf(err) != cluster.ErrorRateLimited {
		t.Fatalf("deterministic batch error = %v, want RateLimited", err)
	}
	if receipt.Attempted != 64 ||
		receipt.Persisted != 30 ||
		requests.Load() != 32 {
		t.Fatalf(
			"failed wave evidence = receipt %#v, requests %d",
			receipt,
			requests.Load(),
		)
	}
}

func TestAdapterParentCancellationStopsTheCurrentWave(t *testing.T) {
	t.Parallel()

	started := make(chan struct{}, 8)
	kubernetesClient := newLeaseTransportClient(
		t,
		func(ctx context.Context, _ string) (*metav1.Status, error) {
			started <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	)
	changeSet := ownedLeaseChangeSet(t, 8)
	ctx, cancel := context.WithCancel(context.Background())
	type executionResult struct {
		receipt cluster.MutationReceipt
		err     error
	}
	completed := make(chan executionResult, 1)
	go func() {
		receipt, err := clusterkubernetes.NewAdapter(kubernetesClient).
			Execute(ctx, changeSet)
		completed <- executionResult{receipt: receipt, err: err}
	}()
	for range 8 {
		select {
		case <-started:
		case <-time.After(time.Second):
			cancel()
			t.Fatal("current mutation wave did not start before cancellation")
		}
	}
	cancel()
	select {
	case result := <-completed:
		if cluster.ErrorCodeOf(result.err) != cluster.ErrorTargetUnavailable ||
			result.receipt.Attempted != 8 ||
			result.receipt.Persisted != 0 {
			t.Fatalf("parent cancellation evidence = %#v, error %v", result.receipt, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("parent cancellation did not stop the current mutation wave")
	}
}

func ownedLeaseChangeSet(t *testing.T, count int) cluster.OwnedChangeSet {
	t.Helper()

	changes := make([]cluster.OwnedChange, 0, count)
	for index := 0; index < count; index++ {
		key, err := cluster.NewObjectKey(
			cluster.ObjectKindLease,
			"kube-node-lease",
			fmt.Sprintf("concurrent-node-%02d", index),
		)
		if err != nil {
			t.Fatal(err)
		}
		change, err := cluster.NewApplyLease(
			key,
			cluster.ObjectPreconditions{},
			cluster.LeaseInput{
				HolderIdentity:       key.Name(),
				LeaseDurationSeconds: 40,
				RenewTime:            time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC),
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		changes = append(changes, change)
	}
	changeSet, err := cluster.NewOwnedChangeSet(
		ownershipScope(t, 2),
		cluster.ExecutionPersistent,
		changes,
	)
	if err != nil {
		t.Fatal(err)
	}
	return changeSet
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

func TestAdapterClassifiesDeleteSnapshotDriftSeparately(t *testing.T) {
	t.Parallel()

	scope := ownershipScope(t, 2)
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:            "synthetic-node",
		UID:             types.UID("node-uid"),
		ResourceVersion: "10",
		Labels:          ownershipLabels(scope),
	}}
	kubernetesClient := kubernetesfake.NewSimpleClientset(node)
	adapter := clusterkubernetes.NewAdapter(kubernetesClient)
	changeSet, err := cluster.NewOwnedChangeSet(
		scope,
		cluster.ExecutionPersistent,
		[]cluster.OwnedChange{ownedNodeDeletion(t)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Execute(
		context.Background(),
		changeSet,
	); cluster.ErrorCodeOf(err) != cluster.ErrorStaleObservation {
		t.Fatalf("delete snapshot drift error = %v, want StaleObservation", err)
	}
}

func TestAdapterClassifiesExactDeleteAPIRaceAsStaleObservation(t *testing.T) {
	t.Parallel()

	scope := ownershipScope(t, 2)
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:            "synthetic-node",
		UID:             types.UID("node-uid"),
		ResourceVersion: "9",
		Labels:          ownershipLabels(scope),
	}}
	kubernetesClient := kubernetesfake.NewSimpleClientset(node)
	kubernetesClient.Fake.PrependReactor(
		"delete",
		"nodes",
		func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewConflict(
				schema.GroupResource{Resource: "nodes"},
				node.Name,
				nil,
			)
		},
	)
	adapter := clusterkubernetes.NewAdapter(kubernetesClient)
	changeSet, err := cluster.NewOwnedChangeSet(
		scope,
		cluster.ExecutionPersistent,
		[]cluster.OwnedChange{ownedNodeDeletion(t)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Execute(
		context.Background(),
		changeSet,
	); cluster.ErrorCodeOf(err) != cluster.ErrorStaleObservation {
		t.Fatalf("exact delete API race error = %v, want StaleObservation", err)
	}
}

func TestAdapterRebasesNodeStatusAcrossOwnedResourceVersionDrift(t *testing.T) {
	t.Parallel()

	scope := ownershipScope(t, 2)
	node := ownedNode("synthetic-node", "node-uid", "10", scope)
	kubernetesClient := kubernetesfake.NewSimpleClientset(&node)
	adapter := clusterkubernetes.NewAdapter(kubernetesClient)
	key, err := cluster.NewObjectKey(
		cluster.ObjectKindNodeStatus,
		"",
		node.Name,
	)
	if err != nil {
		t.Fatal(err)
	}
	change, err := cluster.NewUpdateSyntheticNodeStatus(
		key,
		cluster.ObjectPreconditions{
			UID:             string(node.UID),
			ResourceVersion: "9",
		},
		cluster.SyntheticNodeStatusInput{
			Capacity:    map[string]string{"nvidia.com/gpu": "8"},
			Allocatable: map[string]string{"nvidia.com/gpu": "8"},
			ManageReady: true,
			Ready:       true,
			ObservedAt:  time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	executeSingle(
		t,
		adapter,
		scope,
		cluster.ExecutionPersistent,
		change,
	)
}

func TestAdapterRetriesOwnedNodeStatusWhenTheServerWinsTheApplyRace(t *testing.T) {
	t.Parallel()

	scope := ownershipScope(t, 2)
	node := ownedNode("synthetic-node", "node-uid", "10", scope)
	kubernetesClient := kubernetesfake.NewSimpleClientset(&node)
	statusPatches := 0
	kubernetesClient.Fake.PrependReactor(
		"patch",
		"nodes",
		func(action clienttesting.Action) (bool, runtime.Object, error) {
			if action.GetSubresource() != "status" {
				return false, nil, nil
			}
			statusPatches++
			if statusPatches == 1 {
				return true, nil, apierrors.NewConflict(
					schema.GroupResource{Resource: "nodes"},
					node.Name,
					nil,
				)
			}
			return false, nil, nil
		},
	)
	adapter := clusterkubernetes.NewAdapter(kubernetesClient)
	change := ownedNodeStatusChange(t, node, "9")

	executeSingle(
		t,
		adapter,
		scope,
		cluster.ExecutionPersistent,
		change,
	)
	if statusPatches != 2 {
		t.Fatalf("status patch attempts = %d, want 2", statusPatches)
	}
}

func TestAdapterStopsNodeStatusRetryWhenOwnershipChanges(t *testing.T) {
	t.Parallel()

	scope := ownershipScope(t, 2)
	node := ownedNode("synthetic-node", "node-uid", "10", scope)
	kubernetesClient := kubernetesfake.NewSimpleClientset(&node)
	statusPatches := 0
	kubernetesClient.Fake.PrependReactor(
		"patch",
		"nodes",
		func(action clienttesting.Action) (bool, runtime.Object, error) {
			if action.GetSubresource() != "status" {
				return false, nil, nil
			}
			statusPatches++
			foreign := node.DeepCopy()
			foreign.ResourceVersion = "11"
			foreign.Labels[cluster.InstanceUIDLabel] = "foreign-instance"
			if err := kubernetesClient.Tracker().Update(
				corev1.SchemeGroupVersion.WithResource("nodes"),
				foreign,
				"",
			); err != nil {
				t.Fatalf("replace ownership during conflict: %v", err)
			}
			return true, nil, apierrors.NewConflict(
				schema.GroupResource{Resource: "nodes"},
				node.Name,
				nil,
			)
		},
	)
	adapter := clusterkubernetes.NewAdapter(kubernetesClient)
	change := ownedNodeStatusChange(t, node, "9")
	changeSet, err := cluster.NewOwnedChangeSet(
		scope,
		cluster.ExecutionPersistent,
		[]cluster.OwnedChange{change},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Execute(
		context.Background(),
		changeSet,
	); cluster.ErrorCodeOf(err) != cluster.ErrorOwnershipConflict {
		t.Fatalf("ownership change error = %v, want OwnershipConflict", err)
	}
	if statusPatches != 1 {
		t.Fatalf("status patch attempts = %d, want 1 before ownership rejection", statusPatches)
	}
}

func TestAdapterAtomicallyAdvancesLaggingGenerationWithNodeStatusMutation(
	t *testing.T,
) {
	t.Parallel()

	scope := ownershipScope(t, 2)
	node := ownedNode(
		"synthetic-node",
		"node-uid",
		"10",
		ownershipScope(t, 1),
	)
	kubernetesClient := kubernetesfake.NewSimpleClientset(&node)
	statusPatches := 0
	kubernetesClient.Fake.PrependReactor(
		"patch",
		"nodes",
		func(action clienttesting.Action) (bool, runtime.Object, error) {
			if action.GetSubresource() != "status" {
				return false, nil, nil
			}
			statusPatches++
			return false, nil, nil
		},
	)
	adapter := clusterkubernetes.NewAdapter(kubernetesClient)
	change := ownedNodeStatusChange(t, node, "9")
	changeSet, err := cluster.NewOwnedChangeSet(
		scope,
		cluster.ExecutionPersistent,
		[]cluster.OwnedChange{change},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Execute(context.Background(), changeSet); err != nil {
		t.Fatal(err)
	}
	updated, err := kubernetesClient.CoreV1().Nodes().Get(
		context.Background(),
		node.Name,
		metav1.GetOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if statusPatches != 1 ||
		updated.Labels[cluster.DesiredGenerationLabel] != "2" {
		t.Fatalf(
			"atomic status fence patches=%d labels=%#v",
			statusPatches,
			updated.Labels,
		)
	}
}

func TestAdapterStopsNodeStatusRetryWhenDesiredGenerationAdvances(t *testing.T) {
	t.Parallel()

	scope := ownershipScope(t, 2)
	node := ownedNode("synthetic-node", "node-uid", "10", scope)
	kubernetesClient := kubernetesfake.NewSimpleClientset(&node)
	statusPatches := 0
	kubernetesClient.Fake.PrependReactor(
		"patch",
		"nodes",
		func(action clienttesting.Action) (bool, runtime.Object, error) {
			if action.GetSubresource() != "status" {
				return false, nil, nil
			}
			statusPatches++
			if statusPatches != 1 {
				return false, nil, nil
			}
			newer := node.DeepCopy()
			newer.ResourceVersion = "11"
			newer.Labels[cluster.DesiredGenerationLabel] = "3"
			if err := kubernetesClient.Tracker().Update(
				corev1.SchemeGroupVersion.WithResource("nodes"),
				newer,
				"",
			); err != nil {
				t.Fatalf("advance desired generation during conflict: %v", err)
			}
			return true, nil, apierrors.NewConflict(
				schema.GroupResource{Resource: "nodes"},
				node.Name,
				nil,
			)
		},
	)
	adapter := clusterkubernetes.NewAdapter(kubernetesClient)
	change := ownedNodeStatusChange(t, node, "9")
	changeSet, err := cluster.NewOwnedChangeSet(
		scope,
		cluster.ExecutionPersistent,
		[]cluster.OwnedChange{change},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Execute(
		context.Background(),
		changeSet,
	); cluster.ErrorCodeOf(err) != cluster.ErrorResourceVersionConflict {
		t.Fatalf("generation advance error = %v, want ResourceVersionConflict", err)
	}
	if statusPatches != 1 {
		t.Fatalf("status patch attempts = %d, want 1 before generation rejection", statusPatches)
	}
}

func TestAdapterRebasesOwnedNodeSpecAcrossStatusResourceVersionDrift(t *testing.T) {
	t.Parallel()

	scope := ownershipScope(t, 2)
	node := ownedNode("synthetic-node", "node-uid", "10", scope)
	kubernetesClient := kubernetesfake.NewSimpleClientset(&node)
	adapter := clusterkubernetes.NewAdapter(kubernetesClient)
	key, err := cluster.NewObjectKey(cluster.ObjectKindNode, "", node.Name)
	if err != nil {
		t.Fatal(err)
	}
	change, err := cluster.NewApplySyntheticNode(
		key,
		cluster.ObjectPreconditions{
			UID:             string(node.UID),
			ResourceVersion: "9",
		},
		cluster.SyntheticNodeInput{
			Labels:        map[string]string{"workload.example.com/class": "training"},
			Unschedulable: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	executeSingle(
		t,
		adapter,
		scope,
		cluster.ExecutionPersistent,
		change,
	)
}

func TestAdapterRetriesOwnedNodeSpecWhenTheServerWinsTheApplyRace(t *testing.T) {
	t.Parallel()

	scope := ownershipScope(t, 2)
	node := ownedNode("synthetic-node", "node-uid", "10", scope)
	kubernetesClient := kubernetesfake.NewSimpleClientset(&node)
	specPatches := 0
	kubernetesClient.Fake.PrependReactor(
		"patch",
		"nodes",
		func(action clienttesting.Action) (bool, runtime.Object, error) {
			if action.GetSubresource() != "" {
				return false, nil, nil
			}
			specPatches++
			if specPatches == 1 {
				return true, nil, apierrors.NewConflict(
					schema.GroupResource{Resource: "nodes"},
					node.Name,
					nil,
				)
			}
			return false, nil, nil
		},
	)
	adapter := clusterkubernetes.NewAdapter(kubernetesClient)
	key, err := cluster.NewObjectKey(cluster.ObjectKindNode, "", node.Name)
	if err != nil {
		t.Fatal(err)
	}
	change, err := cluster.NewApplySyntheticNode(
		key,
		cluster.ObjectPreconditions{
			UID:             string(node.UID),
			ResourceVersion: "9",
		},
		cluster.SyntheticNodeInput{
			Labels:        map[string]string{"workload.example.com/class": "training"},
			Unschedulable: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	executeSingle(
		t,
		adapter,
		scope,
		cluster.ExecutionPersistent,
		change,
	)
	if specPatches != 2 {
		t.Fatalf("spec patch attempts = %d, want 2", specPatches)
	}
}

func TestAdapterRebasesOwnedLeaseAcrossResourceVersionDrift(t *testing.T) {
	t.Parallel()

	scope := ownershipScope(t, 2)
	lease := ownedLease("synthetic-node", "lease-uid", "10", scope)
	kubernetesClient := kubernetesfake.NewSimpleClientset(&lease)
	adapter := clusterkubernetes.NewAdapter(kubernetesClient)

	executeSingle(
		t,
		adapter,
		scope,
		cluster.ExecutionPersistent,
		ownedLeaseChange(t, lease, "9"),
	)
}

func TestAdapterRetriesOwnedLeaseWhenTheServerWinsTheApplyRace(t *testing.T) {
	t.Parallel()

	scope := ownershipScope(t, 2)
	lease := ownedLease("synthetic-node", "lease-uid", "10", scope)
	kubernetesClient := kubernetesfake.NewSimpleClientset(&lease)
	leasePatches := 0
	kubernetesClient.Fake.PrependReactor(
		"patch",
		"leases",
		func(clienttesting.Action) (bool, runtime.Object, error) {
			leasePatches++
			if leasePatches == 1 {
				return true, nil, apierrors.NewConflict(
					schema.GroupResource{Resource: "leases"},
					lease.Name,
					nil,
				)
			}
			return false, nil, nil
		},
	)
	adapter := clusterkubernetes.NewAdapter(kubernetesClient)

	executeSingle(
		t,
		adapter,
		scope,
		cluster.ExecutionPersistent,
		ownedLeaseChange(t, lease, "9"),
	)
	if leasePatches != 2 {
		t.Fatalf("Lease patch attempts = %d, want 2", leasePatches)
	}
}

func TestAdapterStopsLeaseRetryWhenOwnershipChanges(t *testing.T) {
	t.Parallel()

	scope := ownershipScope(t, 2)
	lease := ownedLease("synthetic-node", "lease-uid", "10", scope)
	kubernetesClient := kubernetesfake.NewSimpleClientset(&lease)
	leasePatches := 0
	kubernetesClient.Fake.PrependReactor(
		"patch",
		"leases",
		func(clienttesting.Action) (bool, runtime.Object, error) {
			leasePatches++
			foreign := lease.DeepCopy()
			foreign.ResourceVersion = "11"
			foreign.Labels[cluster.InstanceUIDLabel] = "foreign-instance"
			if err := kubernetesClient.Tracker().Update(
				coordinationv1.SchemeGroupVersion.WithResource("leases"),
				foreign,
				lease.Namespace,
			); err != nil {
				t.Fatalf("replace Lease ownership during conflict: %v", err)
			}
			return true, nil, apierrors.NewConflict(
				schema.GroupResource{Resource: "leases"},
				lease.Name,
				nil,
			)
		},
	)
	adapter := clusterkubernetes.NewAdapter(kubernetesClient)
	changeSet, err := cluster.NewOwnedChangeSet(
		scope,
		cluster.ExecutionPersistent,
		[]cluster.OwnedChange{ownedLeaseChange(t, lease, "9")},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Execute(
		context.Background(),
		changeSet,
	); cluster.ErrorCodeOf(err) != cluster.ErrorOwnershipConflict {
		t.Fatalf("Lease ownership change error = %v, want OwnershipConflict", err)
	}
	if leasePatches != 1 {
		t.Fatalf("Lease patch attempts = %d, want 1 before ownership rejection", leasePatches)
	}
}

func TestAdapterStopsLeaseRetryWhenDesiredGenerationAdvances(t *testing.T) {
	t.Parallel()

	scope := ownershipScope(t, 2)
	lease := ownedLease("synthetic-node", "lease-uid", "10", scope)
	kubernetesClient := kubernetesfake.NewSimpleClientset(&lease)
	leasePatches := 0
	kubernetesClient.Fake.PrependReactor(
		"patch",
		"leases",
		func(clienttesting.Action) (bool, runtime.Object, error) {
			leasePatches++
			newer := lease.DeepCopy()
			newer.ResourceVersion = "11"
			newer.Labels[cluster.DesiredGenerationLabel] = "3"
			if err := kubernetesClient.Tracker().Update(
				coordinationv1.SchemeGroupVersion.WithResource("leases"),
				newer,
				lease.Namespace,
			); err != nil {
				t.Fatalf("advance Lease desired generation during conflict: %v", err)
			}
			return true, nil, apierrors.NewConflict(
				schema.GroupResource{Resource: "leases"},
				lease.Name,
				nil,
			)
		},
	)
	adapter := clusterkubernetes.NewAdapter(kubernetesClient)
	changeSet, err := cluster.NewOwnedChangeSet(
		scope,
		cluster.ExecutionPersistent,
		[]cluster.OwnedChange{ownedLeaseChange(t, lease, "9")},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Execute(
		context.Background(),
		changeSet,
	); cluster.ErrorCodeOf(err) != cluster.ErrorResourceVersionConflict {
		t.Fatalf("Lease generation advance error = %v, want ResourceVersionConflict", err)
	}
	if leasePatches != 1 {
		t.Fatalf("Lease patch attempts = %d, want 1 before generation rejection", leasePatches)
	}
}

func ownedNodeStatusChange(
	t *testing.T,
	node corev1.Node,
	resourceVersion string,
) cluster.OwnedChange {
	t.Helper()
	key, err := cluster.NewObjectKey(
		cluster.ObjectKindNodeStatus,
		"",
		node.Name,
	)
	if err != nil {
		t.Fatal(err)
	}
	change, err := cluster.NewUpdateSyntheticNodeStatus(
		key,
		cluster.ObjectPreconditions{
			UID:             string(node.UID),
			ResourceVersion: resourceVersion,
		},
		cluster.SyntheticNodeStatusInput{
			Capacity:    map[string]string{"nvidia.com/gpu": "8"},
			Allocatable: map[string]string{"nvidia.com/gpu": "8"},
			ManageReady: true,
			Ready:       true,
			ObservedAt:  time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return change
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
			Annotations:   map[string]string{"kwok.x-k8s.io/node": "fake"},
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

	nodeActivation, err := cluster.NewApplySyntheticNode(
		nodeKey,
		cluster.ObjectPreconditions{
			UID:             string(node.UID),
			ResourceVersion: node.ResourceVersion,
		},
		cluster.SyntheticNodeInput{
			Labels:        map[string]string{"workload.example.com/class": "training"},
			Annotations:   map[string]string{"kwok.x-k8s.io/node": "fake"},
			Unschedulable: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	executeSingle(t, adapter, scope, cluster.ExecutionPersistent, nodeActivation)
	node, err = kubernetesClient.CoreV1().Nodes().Get(
		context.Background(),
		nodeKey.Name(),
		metav1.GetOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if node.Annotations["kwok.x-k8s.io/node"] != "fake" {
		t.Fatalf("server-side apply did not own the runtime annotation: %#v", node.Annotations)
	}

	nodeUpdate, err := cluster.NewApplySyntheticNode(
		nodeKey,
		cluster.ObjectPreconditions{
			UID:             string(node.UID),
			ResourceVersion: node.ResourceVersion,
		},
		cluster.SyntheticNodeInput{
			Labels:        map[string]string{"workload.example.com/class": "updated"},
			Annotations:   map[string]string{"kwok.x-k8s.io/node": "disabled"},
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
	if node.Annotations["kwok.x-k8s.io/node"] != "disabled" {
		t.Fatalf(
			"server-side apply did not persist explicit runtime deactivation: %#v",
			node.Annotations,
		)
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
	node.Labels[cluster.DesiredGenerationLabel] = "1"
	node, err = kubernetesClient.CoreV1().Nodes().Update(
		context.Background(),
		node,
		metav1.UpdateOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	laggingStatusChange, err := cluster.NewUpdateSyntheticNodeStatus(
		statusKey,
		cluster.ObjectPreconditions{
			UID:             string(node.UID),
			ResourceVersion: node.ResourceVersion,
		},
		cluster.SyntheticNodeStatusInput{
			Capacity:    map[string]string{"nvidia.com/gpu": "8"},
			Allocatable: map[string]string{"nvidia.com/gpu": "5"},
			ObservedAt:  time.Date(2026, 7, 30, 6, 0, 30, 0, time.UTC),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	executeSingle(
		t,
		adapter,
		scope,
		cluster.ExecutionPersistent,
		laggingStatusChange,
	)
	node, err = kubernetesClient.CoreV1().Nodes().Get(
		context.Background(),
		nodeKey.Name(),
		metav1.GetOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if node.Labels[cluster.DesiredGenerationLabel] != "2" ||
		node.Status.Allocatable.
			Name("nvidia.com/gpu", resource.DecimalSI).
			String() != "5" {
		t.Fatalf(
			"status subresource did not atomically persist generation and resources: labels=%#v status=%#v",
			node.Labels,
			node.Status,
		)
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
	staleLeaseResourceVersion := lease.ResourceVersion
	lease = lease.DeepCopy()
	lease.Annotations = map[string]string{
		"simulation.kasim.io/status-drift": "observed",
	}
	lease, err = kubernetesClient.CoordinationV1().
		Leases(leaseKey.Namespace()).
		Update(context.Background(), lease, metav1.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if lease.ResourceVersion == staleLeaseResourceVersion {
		t.Fatal("envtest Lease metadata update did not advance resourceVersion")
	}
	leaseUpdate, err := cluster.NewApplyLease(
		leaseKey,
		cluster.ObjectPreconditions{
			UID:             string(lease.UID),
			ResourceVersion: staleLeaseResourceVersion,
		},
		cluster.LeaseInput{
			HolderIdentity:       nodeKey.Name(),
			LeaseDurationSeconds: 45,
			RenewTime:            time.Date(2026, 7, 30, 6, 1, 0, 0, time.UTC),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	executeSingle(t, adapter, scope, cluster.ExecutionPersistent, leaseUpdate)
	lease, err = kubernetesClient.CoordinationV1().
		Leases(leaseKey.Namespace()).
		Get(context.Background(), leaseKey.Name(), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if lease.Spec.LeaseDurationSeconds == nil ||
		*lease.Spec.LeaseDurationSeconds != 45 ||
		lease.Spec.RenewTime == nil ||
		!lease.Spec.RenewTime.Time.Equal(
			time.Date(2026, 7, 30, 6, 1, 0, 0, time.UTC),
		) {
		t.Fatalf("envtest Lease stale-RV rebase lost desired state: %#v", lease.Spec)
	}

	classKey, err := cluster.NewObjectKey(
		cluster.ObjectKindDeviceClass,
		"",
		"envtest-device-class",
	)
	if err != nil {
		t.Fatal(err)
	}
	classCreate, err := cluster.NewApplyDeviceClass(
		classKey,
		cluster.ObjectPreconditions{},
		cluster.DeviceClassInput{
			Selectors: []string{
				`device.driver == "gpu.nvidia.com"`,
				`device.attributes["simulation.kasim.io/simulated"].bool == true`,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	executeSingle(t, adapter, scope, cluster.ExecutionServerDryRun, classCreate)
	if _, err := kubernetesClient.ResourceV1().DeviceClasses().Get(
		context.Background(),
		classKey.Name(),
		metav1.GetOptions{},
	); !apierrors.IsNotFound(err) {
		t.Fatalf("dry-run DeviceClass create error = %v, want NotFound", err)
	}
	executeSingle(t, adapter, scope, cluster.ExecutionPersistent, classCreate)
	deviceClass, err := kubernetesClient.ResourceV1().DeviceClasses().Get(
		context.Background(),
		classKey.Name(),
		metav1.GetOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}

	model, err := cluster.NewStringDeviceAttribute("nvidia-h100")
	if err != nil {
		t.Fatal(err)
	}
	sliceKey, err := cluster.NewObjectKey(
		cluster.ObjectKindResourceSlice,
		"",
		"envtest-resource-slice",
	)
	if err != nil {
		t.Fatal(err)
	}
	sliceCreate, err := cluster.NewApplyResourceSlice(
		sliceKey,
		cluster.ObjectPreconditions{},
		cluster.ResourceSliceInput{
			Driver:             "gpu.nvidia.com",
			PoolName:           "envtest-pool",
			PoolGeneration:     2,
			ResourceSliceCount: 1,
			NodeName:           nodeKey.Name(),
			Devices: []cluster.DRADevice{{
				Name: "kasim-device-envtest",
				Attributes: map[string]cluster.DeviceAttributeValue{
					"simulation.kasim.io/simulated": cluster.NewBoolDeviceAttribute(true),
					"simulation.kasim.io/model":     model,
				},
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	executeSingle(t, adapter, scope, cluster.ExecutionServerDryRun, sliceCreate)
	if _, err := kubernetesClient.ResourceV1().ResourceSlices().Get(
		context.Background(),
		sliceKey.Name(),
		metav1.GetOptions{},
	); !apierrors.IsNotFound(err) {
		t.Fatalf("dry-run ResourceSlice create error = %v, want NotFound", err)
	}
	executeSingle(t, adapter, scope, cluster.ExecutionPersistent, sliceCreate)
	resourceSlice, err := kubernetesClient.ResourceV1().ResourceSlices().Get(
		context.Background(),
		sliceKey.Name(),
		metav1.GetOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resourceSlice.Spec.Driver != "gpu.nvidia.com" ||
		resourceSlice.Spec.Pool.Generation != 2 ||
		resourceSlice.Spec.NodeName == nil ||
		*resourceSlice.Spec.NodeName != nodeKey.Name() ||
		len(resourceSlice.Spec.Devices) != 1 {
		t.Fatalf("envtest DRA persistence lost portable intent: %#v", resourceSlice.Spec)
	}
	sliceDeletion, err := cluster.NewDeleteOwnedObject(
		sliceKey,
		cluster.ObjectPreconditions{
			UID:             string(resourceSlice.UID),
			ResourceVersion: resourceSlice.ResourceVersion,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	executeSingle(t, adapter, scope, cluster.ExecutionServerDryRun, sliceDeletion)
	if _, err := kubernetesClient.ResourceV1().ResourceSlices().Get(
		context.Background(),
		sliceKey.Name(),
		metav1.GetOptions{},
	); err != nil {
		t.Fatalf("dry-run ResourceSlice delete removed object: %v", err)
	}
	executeSingle(t, adapter, scope, cluster.ExecutionPersistent, sliceDeletion)
	classDeletion, err := cluster.NewDeleteOwnedObject(
		classKey,
		cluster.ObjectPreconditions{
			UID:             string(deviceClass.UID),
			ResourceVersion: deviceClass.ResourceVersion,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	executeSingle(t, adapter, scope, cluster.ExecutionServerDryRun, classDeletion)
	if _, err := kubernetesClient.ResourceV1().DeviceClasses().Get(
		context.Background(),
		classKey.Name(),
		metav1.GetOptions{},
	); err != nil {
		t.Fatalf("dry-run DeviceClass delete removed object: %v", err)
	}
	executeSingle(t, adapter, scope, cluster.ExecutionPersistent, classDeletion)

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
			GroupVersion: "resource.k8s.io/v1",
			APIResources: []metav1.APIResource{
				{
					Name: "deviceclasses",
					Verbs: metav1.Verbs{
						"get", "list", "watch", "create", "patch", "delete",
					},
				},
				{
					Name: "resourceslices",
					Verbs: metav1.Verbs{
						"get", "list", "watch", "create", "patch", "delete",
					},
				},
				{
					Name:       "resourceclaims",
					Namespaced: true,
					Verbs:      metav1.Verbs{"get", "list", "watch"},
				},
			},
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
		cluster.ManagedByLabel:   cluster.ManagedByValue,
		cluster.InstanceUIDLabel: scope.InstanceUID().String(),
		cluster.DesiredGenerationLabel: strconv.FormatUint(
			scope.DesiredGeneration().Value(),
			10,
		),
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

func ownedLease(
	name, uid, resourceVersion string,
	scope cluster.OwnershipScope,
) coordinationv1.Lease {
	return coordinationv1.Lease{ObjectMeta: metav1.ObjectMeta{
		Namespace:       "kube-node-lease",
		Name:            name,
		UID:             types.UID(uid),
		ResourceVersion: resourceVersion,
		Labels:          ownershipLabels(scope),
	}}
}

func ownedLeaseChange(
	t *testing.T,
	lease coordinationv1.Lease,
	resourceVersion string,
) cluster.OwnedChange {
	t.Helper()
	key, err := cluster.NewObjectKey(
		cluster.ObjectKindLease,
		lease.Namespace,
		lease.Name,
	)
	if err != nil {
		t.Fatal(err)
	}
	change, err := cluster.NewApplyLease(
		key,
		cluster.ObjectPreconditions{
			UID:             string(lease.UID),
			ResourceVersion: resourceVersion,
		},
		cluster.LeaseInput{
			HolderIdentity:       lease.Name,
			LeaseDurationSeconds: 40,
			RenewTime:            time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return change
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
