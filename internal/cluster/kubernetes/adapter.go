package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/version"
	coordinationapplyv1 "k8s.io/client-go/applyconfigurations/coordination/v1"
	coreapplyv1 "k8s.io/client-go/applyconfigurations/core/v1"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"
	resourceapplyv1 "k8s.io/client-go/applyconfigurations/resource/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"

	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

const (
	discoveryPageSize           = int64(200)
	minimumMinor                = 30
	maximumMinor                = 36
	ownedMutationConcurrency    = 32
	ownedObservationConcurrency = 32
)

var ownedMutationConflictBackoff = wait.Backoff{
	Steps:    8,
	Duration: 5 * time.Millisecond,
	Factor:   2,
	Jitter:   0.1,
	Cap:      250 * time.Millisecond,
}

var requiredResources = []struct {
	groupVersion string
	resources    []string
}{
	{groupVersion: "v1", resources: []string{"namespaces", "nodes", "pods"}},
	{
		groupVersion: "coordination.k8s.io/v1",
		resources:    []string{"leases"},
	},
	{
		groupVersion: "authorization.k8s.io/v1",
		resources:    []string{"selfsubjectaccessreviews"},
	},
	{
		groupVersion: "simulation.kasim.io/v1alpha1",
		resources:    []string{"scenarioinstances", "scenarioinstances/status"},
	},
}

// Adapter implements the Cluster port with typed client-go operations.
type Adapter struct {
	client clientset.Interface
}

func NewAdapter(kubernetesClient clientset.Interface) *Adapter {
	return &Adapter{client: kubernetesClient}
}

func (adapter *Adapter) Discover(
	ctx context.Context,
) (cluster.TargetCapabilities, error) {
	if adapter.client == nil {
		return cluster.TargetCapabilities{}, cluster.NewError(
			cluster.ErrorTargetUnavailable,
			"Kubernetes client is not configured",
			false,
		)
	}
	serverVersion, err := adapter.client.Discovery().ServerVersion()
	if err != nil {
		return cluster.TargetCapabilities{}, classify(
			"discover Kubernetes server version",
			err,
		)
	}
	minor, err := parseMinor(serverVersion)
	if err != nil {
		return cluster.TargetCapabilities{}, cluster.NewError(
			cluster.ErrorCapabilityUnavailable,
			err.Error(),
			false,
		)
	}
	switch {
	case minor < minimumMinor:
		return cluster.TargetCapabilities{}, cluster.NewError(
			cluster.ErrorKubernetesVersionUnsupported,
			fmt.Sprintf(
				"Kubernetes minor 1.%d is below the supported 1.%d floor",
				minor,
				minimumMinor,
			),
			false,
		)
	case minor > maximumMinor:
		return cluster.TargetCapabilities{}, cluster.NewError(
			cluster.ErrorKubernetesVersionUntested,
			fmt.Sprintf(
				"Kubernetes minor 1.%d is above the validated 1.%d ceiling",
				minor,
				maximumMinor,
			),
			false,
		)
	}

	resources := make([]cluster.ResourceCapability, 0, 16)
	for _, requirement := range requiredResources {
		groupVersion := requirement.groupVersion
		resourceList, err := adapter.client.Discovery().
			ServerResourcesForGroupVersion(groupVersion)
		if err != nil {
			code := cluster.ErrorCapabilityUnavailable
			if groupVersion == "simulation.kasim.io/v1alpha1" {
				code = cluster.ErrorRuntimeUnavailable
			}
			return cluster.TargetCapabilities{}, cluster.NewError(
				code,
				fmt.Sprintf(
					"required Kubernetes API %s is unavailable",
					groupVersion,
				),
				false,
			)
		}
		discovered := make(map[string]struct{}, len(resourceList.APIResources))
		for _, resource := range resourceList.APIResources {
			discovered[resource.Name] = struct{}{}
			verbs := append([]string(nil), resource.Verbs...)
			slices.Sort(verbs)
			resources = append(resources, cluster.ResourceCapability{
				GroupVersion: groupVersion,
				Resource:     resource.Name,
				Namespaced:   resource.Namespaced,
				Verbs:        verbs,
			})
		}
		for _, resource := range requirement.resources {
			if _, found := discovered[resource]; found {
				continue
			}
			code := cluster.ErrorCapabilityUnavailable
			if groupVersion == "simulation.kasim.io/v1alpha1" {
				code = cluster.ErrorRuntimeUnavailable
			}
			return cluster.TargetCapabilities{}, cluster.NewError(
				code,
				fmt.Sprintf(
					"required Kubernetes resource %s/%s is unavailable",
					groupVersion,
					resource,
				),
				false,
			)
		}
	}
	if minor >= 34 {
		if resourceList, err := adapter.client.Discovery().
			ServerResourcesForGroupVersion("resource.k8s.io/v1"); err == nil {
			for _, resource := range resourceList.APIResources {
				verbs := append([]string(nil), resource.Verbs...)
				slices.Sort(verbs)
				resources = append(resources, cluster.ResourceCapability{
					GroupVersion: "resource.k8s.io/v1",
					Resource:     resource.Name,
					Namespaced:   resource.Namespaced,
					Verbs:        verbs,
				})
			}
		}
	}
	slices.SortFunc(
		resources,
		func(left, right cluster.ResourceCapability) int {
			if compared := strings.Compare(left.GroupVersion, right.GroupVersion); compared != 0 {
				return compared
			}
			return strings.Compare(left.Resource, right.Resource)
		},
	)
	return cluster.TargetCapabilities{
		ServerVersion:   serverVersion.GitVersion,
		KubernetesMinor: minor,
		Resources:       resources,
	}, nil
}

func (adapter *Adapter) Authorize(
	ctx context.Context,
	requirements []cluster.AccessRequirement,
) (cluster.AuthorizationReport, error) {
	if len(requirements) == 0 {
		return cluster.AuthorizationReport{}, cluster.NewError(
			cluster.ErrorInvalidIntent,
			"authorization requires at least one exact operation",
			false,
		)
	}
	decisions := make(
		[]cluster.AuthorizationDecision,
		0,
		len(requirements),
	)
	for _, requirement := range requirements {
		if err := requirement.Validate(); err != nil {
			return cluster.AuthorizationReport{}, cluster.NewError(
				cluster.ErrorInvalidIntent,
				err.Error(),
				false,
			)
		}
		review := &authorizationv1.SelfSubjectAccessReview{
			Spec: authorizationv1.SelfSubjectAccessReviewSpec{
				ResourceAttributes: &authorizationv1.ResourceAttributes{
					Namespace:   requirement.Namespace,
					Verb:        requirement.Verb,
					Group:       requirement.Group,
					Resource:    requirement.Resource,
					Subresource: requirement.Subresource,
					Name:        requirement.Name,
				},
			},
		}
		result, err := adapter.client.AuthorizationV1().
			SelfSubjectAccessReviews().
			Create(ctx, review, metav1.CreateOptions{})
		if err != nil {
			return cluster.AuthorizationReport{}, classify(
				"review exact Kubernetes authorization",
				err,
			)
		}
		decisions = append(decisions, cluster.AuthorizationDecision{
			Requirement:     requirement,
			Allowed:         result.Status.Allowed,
			Reason:          result.Status.Reason,
			EvaluationError: result.Status.EvaluationError,
		})
	}
	return cluster.AuthorizationReport{Decisions: decisions}, nil
}

func (adapter *Adapter) Observe(
	ctx context.Context,
	scope cluster.OwnershipScope,
) (cluster.ObservedGraph, error) {
	if scope.InstanceUID().String() == "" ||
		scope.DesiredGeneration().Value() == 0 {
		return cluster.ObservedGraph{}, cluster.NewError(
			cluster.ErrorInvalidIntent,
			"observation requires exact ownership",
			false,
		)
	}
	selector := ownershipSelector(scope.InstanceUID().String())
	objects := make([]cluster.ObservedObject, 0)
	ownedNodeNames := make([]string, 0)
	continueToken := ""
	for {
		result, err := adapter.client.CoreV1().Nodes().List(
			ctx,
			metav1.ListOptions{
				LabelSelector: selector,
				Limit:         discoveryPageSize,
				Continue:      continueToken,
			},
		)
		if err != nil {
			return cluster.ObservedGraph{}, classify("observe owned Nodes", err)
		}
		for index := range result.Items {
			key, _ := cluster.NewObjectKey(
				cluster.ObjectKindNode,
				"",
				result.Items[index].Name,
			)
			object, err := observedObject(
				key,
				result.Items[index].UID,
				result.Items[index].ResourceVersion,
				result.Items[index].Labels,
				result.Items[index].OwnerReferences,
				scope,
			)
			if err != nil {
				return cluster.ObservedGraph{}, err
			}
			object.Node = observedNodeState(&result.Items[index])
			objects = append(objects, object)
			ownedNodeNames = append(ownedNodeNames, result.Items[index].Name)
			if len(objects) > cluster.MaximumObservedObjects {
				return cluster.ObservedGraph{}, cluster.NewError(
					cluster.ErrorCapabilityUnavailable,
					"owned observation exceeded its bounded object limit",
					false,
				)
			}
		}
		continueToken = result.Continue
		if continueToken == "" {
			break
		}
	}
	continueToken = ""
	for {
		result, err := adapter.client.CoordinationV1().
			Leases("kube-node-lease").
			List(ctx, metav1.ListOptions{
				LabelSelector: selector,
				Limit:         discoveryPageSize,
				Continue:      continueToken,
			})
		if err != nil {
			return cluster.ObservedGraph{}, classify("observe owned Leases", err)
		}
		for index := range result.Items {
			key, _ := cluster.NewObjectKey(
				cluster.ObjectKindLease,
				result.Items[index].Namespace,
				result.Items[index].Name,
			)
			object, err := observedObject(
				key,
				result.Items[index].UID,
				result.Items[index].ResourceVersion,
				result.Items[index].Labels,
				result.Items[index].OwnerReferences,
				scope,
			)
			if err != nil {
				return cluster.ObservedGraph{}, err
			}
			object.Lease = observedLeaseState(&result.Items[index])
			objects = append(objects, object)
			if len(objects) > cluster.MaximumObservedObjects {
				return cluster.ObservedGraph{}, cluster.NewError(
					cluster.ErrorCapabilityUnavailable,
					"owned observation exceeded its bounded object limit",
					false,
				)
			}
		}
		continueToken = result.Continue
		if continueToken == "" {
			break
		}
	}
	observeDRA := scope.Fidelity().String() == "dra-control-plane"
	ownedDRA := false
	ownedClassNames := make(map[string]struct{})
	ownedDeviceTuples := make(map[string]struct{})
	continueToken = ""
	for observeDRA {
		result, err := adapter.client.ResourceV1().DeviceClasses().List(
			ctx,
			metav1.ListOptions{
				LabelSelector: selector,
				Limit:         discoveryPageSize,
				Continue:      continueToken,
			},
		)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return cluster.ObservedGraph{}, classify(
					"observe owned DeviceClasses",
					err,
				)
			}
			break
		}
		for index := range result.Items {
			item := &result.Items[index]
			key, _ := cluster.NewObjectKey(
				cluster.ObjectKindDeviceClass,
				"",
				item.Name,
			)
			object, err := observedObject(
				key,
				item.UID,
				item.ResourceVersion,
				item.Labels,
				item.OwnerReferences,
				scope,
			)
			if err != nil {
				return cluster.ObservedGraph{}, err
			}
			object.DeviceClass = observedDeviceClassState(item)
			objects = append(objects, object)
			ownedClassNames[item.Name] = struct{}{}
			ownedDRA = true
			if len(objects) > cluster.MaximumObservedObjects {
				return cluster.ObservedGraph{}, cluster.NewError(
					cluster.ErrorCapabilityUnavailable,
					"owned observation exceeded its bounded object limit",
					false,
				)
			}
		}
		continueToken = result.Continue
		if continueToken == "" {
			break
		}
	}
	continueToken = ""
	for observeDRA {
		result, err := adapter.client.ResourceV1().ResourceSlices().List(
			ctx,
			metav1.ListOptions{
				LabelSelector: selector,
				Limit:         discoveryPageSize,
				Continue:      continueToken,
			},
		)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return cluster.ObservedGraph{}, classify(
					"observe owned ResourceSlices",
					err,
				)
			}
			break
		}
		for index := range result.Items {
			item := &result.Items[index]
			key, _ := cluster.NewObjectKey(
				cluster.ObjectKindResourceSlice,
				"",
				item.Name,
			)
			object, err := observedObject(
				key,
				item.UID,
				item.ResourceVersion,
				item.Labels,
				item.OwnerReferences,
				scope,
			)
			if err != nil {
				return cluster.ObservedGraph{}, err
			}
			object.ResourceSlice = observedResourceSliceState(item)
			objects = append(objects, object)
			for _, device := range item.Spec.Devices {
				ownedDeviceTuples[draTuple(
					item.Spec.Driver,
					item.Spec.Pool.Name,
					device.Name,
				)] = struct{}{}
			}
			ownedDRA = true
			if len(objects) > cluster.MaximumObservedObjects {
				return cluster.ObservedGraph{}, cluster.NewError(
					cluster.ErrorCapabilityUnavailable,
					"owned observation exceeded its bounded object limit",
					false,
				)
			}
		}
		continueToken = result.Continue
		if continueToken == "" {
			break
		}
	}
	resourceClaims := make([]cluster.ObservedResourceClaim, 0)
	if ownedDRA {
		continueToken = ""
		for {
			result, err := adapter.client.ResourceV1().
				ResourceClaims(metav1.NamespaceAll).
				List(ctx, metav1.ListOptions{
					Limit:    discoveryPageSize,
					Continue: continueToken,
				})
			if err != nil {
				return cluster.ObservedGraph{}, classify(
					"observe ResourceClaims referencing owned DRA inventory",
					err,
				)
			}
			for index := range result.Items {
				claim := observedResourceClaim(&result.Items[index])
				if !claimReferencesOwnedDRA(
					claim,
					ownedClassNames,
					ownedDeviceTuples,
				) {
					continue
				}
				resourceClaims = append(resourceClaims, claim)
				if len(resourceClaims) > cluster.MaximumObservedClaims {
					return cluster.ObservedGraph{}, cluster.NewError(
						cluster.ErrorCapabilityUnavailable,
						"DRA ResourceClaim observation exceeded its bounded limit",
						false,
					)
				}
			}
			continueToken = result.Continue
			if continueToken == "" {
				break
			}
		}
	}
	slices.Sort(ownedNodeNames)
	ownedNodeNameSet := make(map[string]struct{}, len(ownedNodeNames))
	for _, name := range ownedNodeNames {
		ownedNodeNameSet[name] = struct{}{}
	}
	pods := make([]cluster.ObservedPod, 0)
	observedPodKeys := make(map[string]struct{})
	appendPod := func(pod *corev1.Pod) error {
		key := pod.Namespace + "\x00" + pod.Name
		if _, duplicate := observedPodKeys[key]; duplicate {
			return nil
		}
		observedPodKeys[key] = struct{}{}
		pods = append(pods, observedPod(pod))
		if len(pods) > cluster.MaximumObservedPods {
			return cluster.NewError(
				cluster.ErrorCapabilityUnavailable,
				"owned-node and DRA Pod observation exceeded its bounded limit",
				false,
			)
		}
		return nil
	}
	claimNamesByNamespace := make(map[string]map[string]struct{})
	for _, claim := range resourceClaims {
		if claimNamesByNamespace[claim.Namespace] == nil {
			claimNamesByNamespace[claim.Namespace] =
				make(map[string]struct{})
		}
		claimNamesByNamespace[claim.Namespace][claim.Name] = struct{}{}
	}
	if len(claimNamesByNamespace) != 0 {
		continueToken = ""
		for {
			result, err := adapter.client.CoreV1().Pods(metav1.NamespaceAll).List(
				ctx,
				metav1.ListOptions{
					Limit:    discoveryPageSize,
					Continue: continueToken,
				},
			)
			if err != nil {
				return cluster.ObservedGraph{}, classify(
					"observe Pods bound to owned Nodes or referencing selected DRA ResourceClaims",
					err,
				)
			}
			for index := range result.Items {
				pod := &result.Items[index]
				_, boundToOwnedNode := ownedNodeNameSet[pod.Spec.NodeName]
				referencesOwnedClaim := podReferencesResourceClaim(
					pod,
					claimNamesByNamespace[pod.Namespace],
				)
				if !boundToOwnedNode && !referencesOwnedClaim {
					continue
				}
				if err := appendPod(pod); err != nil {
					return cluster.ObservedGraph{}, err
				}
			}
			continueToken = result.Continue
			if continueToken == "" {
				break
			}
		}
	} else if len(ownedNodeNames) != 0 {
		boundPods, err := adapter.observePodsBoundToNodes(
			ctx,
			ownedNodeNames,
			cluster.MaximumObservedPods,
		)
		if err != nil {
			return cluster.ObservedGraph{}, err
		}
		for index := range boundPods {
			if err := appendPod(&boundPods[index]); err != nil {
				return cluster.ObservedGraph{}, err
			}
		}
	}
	slices.SortFunc(pods, func(left, right cluster.ObservedPod) int {
		if compared := strings.Compare(left.Namespace, right.Namespace); compared != 0 {
			return compared
		}
		return strings.Compare(left.Name, right.Name)
	})
	slices.SortFunc(
		resourceClaims,
		func(left, right cluster.ObservedResourceClaim) int {
			if compared := strings.Compare(left.Namespace, right.Namespace); compared != 0 {
				return compared
			}
			return strings.Compare(left.Name, right.Name)
		},
	)
	return cluster.ObservedGraph{
		Objects:        objects,
		Pods:           pods,
		ResourceClaims: resourceClaims,
	}, nil
}

func (adapter *Adapter) observePodsBoundToNodes(
	ctx context.Context,
	nodeNames []string,
	maxPods int64,
) ([]corev1.Pod, error) {
	type indexedResult struct {
		index int
		pods  []corev1.Pod
		err   error
	}
	if maxPods <= 0 {
		return nil, cluster.NewError(
			cluster.ErrorCapabilityUnavailable,
			"owned-node Pod observation has no positive budget",
			false,
		)
	}
	var observedPods atomic.Int64
	pods := make([]corev1.Pod, 0)
	for waveStart := 0; waveStart < len(nodeNames); waveStart +=
		ownedObservationConcurrency {
		if err := ctx.Err(); err != nil {
			return nil, classify("observe Pods bound to owned Nodes", err)
		}
		waveEnd := min(
			waveStart+ownedObservationConcurrency,
			len(nodeNames),
		)
		waveCtx, cancelWave := context.WithCancel(ctx)
		results := make(chan indexedResult, waveEnd-waveStart)
		for index := waveStart; index < waveEnd; index++ {
			go func(index int) {
				observed, err := adapter.observePodsBoundToNode(
					waveCtx,
					nodeNames[index],
					&observedPods,
					maxPods,
				)
				results <- indexedResult{
					index: index,
					pods:  observed,
					err:   err,
				}
			}(index)
		}
		podsByIndex := make([][]corev1.Pod, waveEnd-waveStart)
		errorsByIndex := make([]error, waveEnd-waveStart)
		for range waveEnd - waveStart {
			result := <-results
			resultIndex := result.index - waveStart
			podsByIndex[resultIndex] = result.pods
			errorsByIndex[resultIndex] = result.err
			if result.err != nil {
				cancelWave()
			}
		}
		cancelWave()
		selectedError := -1
		for index, err := range errorsByIndex {
			if err == nil {
				continue
			}
			if selectedError == -1 ||
				(errors.Is(errorsByIndex[selectedError], context.Canceled) &&
					!errors.Is(err, context.Canceled)) {
				selectedError = index
			}
		}
		if selectedError != -1 {
			return nil, classify(
				fmt.Sprintf(
					"observe Pods bound to owned Node %q",
					nodeNames[waveStart+selectedError],
				),
				errorsByIndex[selectedError],
			)
		}
		for index := range podsByIndex {
			pods = append(pods, podsByIndex[index]...)
			if int64(len(pods)) > maxPods {
				return nil, cluster.NewError(
					cluster.ErrorCapabilityUnavailable,
					"owned-node Pod observation exceeded its bounded limit",
					false,
				)
			}
		}
	}
	return pods, nil
}

func (adapter *Adapter) observePodsBoundToNode(
	ctx context.Context,
	nodeName string,
	observedPods *atomic.Int64,
	maxPods int64,
) ([]corev1.Pod, error) {
	pods := make([]corev1.Pod, 0)
	continueToken := ""
	for {
		result, err := adapter.client.CoreV1().Pods(metav1.NamespaceAll).List(
			ctx,
			metav1.ListOptions{
				FieldSelector: fields.OneTermEqualSelector(
					"spec.nodeName",
					nodeName,
				).String(),
				Limit:    discoveryPageSize,
				Continue: continueToken,
			},
		)
		if err != nil {
			return nil, err
		}
		for index := range result.Items {
			if result.Items[index].Spec.NodeName != nodeName {
				continue
			}
			if observedPods.Add(1) > maxPods {
				return nil, cluster.NewError(
					cluster.ErrorCapabilityUnavailable,
					"owned-node Pod observation exceeded its shared limit",
					false,
				)
			}
			pods = append(pods, result.Items[index])
		}
		continueToken = result.Continue
		if continueToken == "" {
			return pods, nil
		}
	}
}

func (adapter *Adapter) Execute(
	ctx context.Context,
	changeSet cluster.OwnedChangeSet,
) (cluster.MutationReceipt, error) {
	changes := changeSet.Changes()
	receipt := cluster.MutationReceipt{
		DryRun:    changeSet.Mode() == cluster.ExecutionServerDryRun,
		Attempted: len(changes),
	}
	if len(changes) == 1 {
		err := adapter.executeOwnedChange(
			ctx,
			changeSet.Scope(),
			changes[0],
			receipt.DryRun,
		)
		if err != nil {
			return receipt, err
		}
		if !receipt.DryRun {
			receipt.Persisted = 1
		}
		return receipt, nil
	}

	type indexedResult struct {
		index int
		err   error
	}
	for waveStart := 0; waveStart < len(changes); waveStart += ownedMutationConcurrency {
		if err := ctx.Err(); err != nil {
			return receipt, classify("execute owned mutation batch", err)
		}
		waveEnd := min(waveStart+ownedMutationConcurrency, len(changes))
		results := make(chan indexedResult, waveEnd-waveStart)
		for index := waveStart; index < waveEnd; index++ {
			go func(index int) {
				results <- indexedResult{
					index: index,
					err: adapter.executeOwnedChange(
						ctx,
						changeSet.Scope(),
						changes[index],
						receipt.DryRun,
					),
				}
			}(index)
		}
		errorsByIndex := make([]error, waveEnd-waveStart)
		for range waveEnd - waveStart {
			result := <-results
			errorsByIndex[result.index-waveStart] = result.err
			if result.err == nil && !receipt.DryRun {
				receipt.Persisted++
			}
		}
		for _, err := range errorsByIndex {
			if err != nil {
				return receipt, err
			}
		}
	}
	return receipt, nil
}

func (adapter *Adapter) executeOwnedChange(
	ctx context.Context,
	scope cluster.OwnershipScope,
	change cluster.OwnedChange,
	dryRun bool,
) error {
	switch typed := change.(type) {
	case cluster.ApplySyntheticNode:
		return adapter.applySyntheticNode(
			ctx,
			scope,
			typed,
			dryRun,
		)
	case cluster.UpdateSyntheticNodeStatus:
		return adapter.updateSyntheticNodeStatus(
			ctx,
			scope,
			typed,
			dryRun,
		)
	case cluster.ApplyLease:
		return adapter.applyLease(
			ctx,
			scope,
			typed,
			dryRun,
		)
	case cluster.ApplyDeviceClass:
		return adapter.applyDeviceClass(
			ctx,
			scope,
			typed,
			dryRun,
		)
	case cluster.ApplyResourceSlice:
		return adapter.applyResourceSlice(
			ctx,
			scope,
			typed,
			dryRun,
		)
	case cluster.DeleteOwnedObject:
		return adapter.deleteOwned(
			ctx,
			scope,
			typed,
			dryRun,
		)
	default:
		return cluster.NewError(
			cluster.ErrorInvalidIntent,
			"Cluster adapter received an unsupported change intention",
			false,
		)
	}
}

func (adapter *Adapter) applySyntheticNode(
	ctx context.Context,
	scope cluster.OwnershipScope,
	change cluster.ApplySyntheticNode,
	dryRun bool,
) error {
	objectLabels := change.Labels()
	if objectLabels == nil {
		objectLabels = make(map[string]string)
	}
	addOwnershipLabels(objectLabels, scope)
	taints := make([]corev1.Taint, 0, len(change.Taints()))
	applyTaints := make(
		[]*coreapplyv1.TaintApplyConfiguration,
		0,
		len(change.Taints()),
	)
	for _, taint := range change.Taints() {
		effect := corev1.TaintEffect(taint.Effect)
		taints = append(taints, corev1.Taint{
			Key: taint.Key, Value: taint.Value, Effect: effect,
		})
		applyTaints = append(
			applyTaints,
			coreapplyv1.Taint().
				WithKey(taint.Key).
				WithValue(taint.Value).
				WithEffect(effect),
		)
	}
	options := applyOptions(dryRun)
	preconditions := change.Preconditions()
	if preconditions.UID == "" {
		ownerReferences := ownerReferences(scope)
		_, err := adapter.client.CoreV1().Nodes().Create(
			ctx,
			&corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:            change.Key().Name(),
					Labels:          objectLabels,
					Annotations:     change.Annotations(),
					OwnerReferences: ownerReferences,
				},
				Spec: corev1.NodeSpec{
					Unschedulable: change.Unschedulable(),
					Taints:        taints,
				},
			},
			metav1.CreateOptions{DryRun: options.DryRun},
		)
		if apierrors.IsAlreadyExists(err) {
			return cluster.NewError(
				cluster.ErrorOwnershipConflict,
				fmt.Sprintf(
					"Node %q appeared before create and was not adopted",
					change.Key().Name(),
				),
				false,
			)
		}
		return classify("create exact Synthetic Node", err)
	}
	err := adapter.retryOwnedMutation(
		ctx,
		change.Key(),
		scope,
		preconditions,
		func() (ownedObjectMetadata, error) {
			return adapter.getNodeMetadata(ctx, change.Key().Name())
		},
		func(current ownedObjectMetadata) error {
			configuration := coreapplyv1.Node(change.Key().Name()).
				WithUID(current.uid).
				WithResourceVersion(current.resourceVersion).
				WithLabels(objectLabels).
				WithAnnotations(change.Annotations()).
				WithSpec(
					coreapplyv1.NodeSpec().
						WithUnschedulable(change.Unschedulable()).
						WithTaints(applyTaints...),
				)
			if owner := ownerReferenceApply(scope); owner != nil {
				configuration = configuration.WithOwnerReferences(owner)
			}
			_, applyErr := adapter.client.CoreV1().Nodes().Apply(
				ctx,
				configuration,
				options,
			)
			return applyErr
		},
	)
	return classify("server-side apply exact Synthetic Node", err)
}

func (adapter *Adapter) updateSyntheticNodeStatus(
	ctx context.Context,
	scope cluster.OwnershipScope,
	change cluster.UpdateSyntheticNodeStatus,
	dryRun bool,
) error {
	capacity, err := resourceList(change.Capacity())
	if err != nil {
		return err
	}
	allocatable, err := resourceList(change.Allocatable())
	if err != nil {
		return err
	}
	status := coreapplyv1.NodeStatus().
		WithCapacity(capacity).
		WithAllocatable(allocatable)
	if change.ManagesReady() {
		readyStatus := corev1.ConditionFalse
		if change.Ready() {
			readyStatus = corev1.ConditionTrue
		}
		observedAt := metav1.NewTime(change.ObservedAt())
		status = status.WithConditions(
			coreapplyv1.NodeCondition().
				WithType(corev1.NodeReady).
				WithStatus(readyStatus).
				WithReason("KubeAcceleratorSimObserved").
				WithMessage("Synthetic Node status is reconciled").
				WithLastHeartbeatTime(observedAt).
				WithLastTransitionTime(observedAt),
		)
	}
	// Node status requests preserve metadata. Publishing the ownership labels
	// in the same resourceVersion-guarded ApplyStatus makes the desired
	// generation fence atomic with the scheduler-visible resource change.
	objectLabels := map[string]string{}
	addOwnershipLabels(objectLabels, scope)
	err = adapter.retryOwnedMutation(
		ctx,
		change.Key(),
		scope,
		change.Preconditions(),
		func() (ownedObjectMetadata, error) {
			return adapter.getNodeMetadata(ctx, change.Key().Name())
		},
		func(current ownedObjectMetadata) error {
			configuration := coreapplyv1.Node(change.Key().Name()).
				WithUID(current.uid).
				WithResourceVersion(current.resourceVersion).
				WithLabels(objectLabels).
				WithStatus(status)
			_, applyErr := adapter.client.CoreV1().Nodes().ApplyStatus(
				ctx,
				configuration,
				applyOptions(dryRun),
			)
			return applyErr
		},
	)
	return classify("apply exact Synthetic Node status", err)
}

type ownedObjectMetadata struct {
	labels          map[string]string
	uid             types.UID
	resourceVersion string
	ownerReferences []metav1.OwnerReference
}

func metadataOf(object metav1.Object) ownedObjectMetadata {
	if object == nil {
		return ownedObjectMetadata{}
	}
	return ownedObjectMetadata{
		labels:          object.GetLabels(),
		uid:             object.GetUID(),
		resourceVersion: object.GetResourceVersion(),
		ownerReferences: object.GetOwnerReferences(),
	}
}

func (adapter *Adapter) getNodeMetadata(
	ctx context.Context,
	name string,
) (ownedObjectMetadata, error) {
	current, err := adapter.client.CoreV1().Nodes().Get(
		ctx,
		name,
		metav1.GetOptions{},
	)
	if err != nil {
		return ownedObjectMetadata{}, err
	}
	return metadataOf(current), nil
}

func (adapter *Adapter) retryOwnedMutation(
	ctx context.Context,
	key cluster.ObjectKey,
	scope cluster.OwnershipScope,
	preconditions cluster.ObjectPreconditions,
	get func() (ownedObjectMetadata, error),
	mutate func(ownedObjectMetadata) error,
) error {
	return retry.OnError(
		ownedMutationConflictBackoff,
		apierrors.IsConflict,
		func() error {
			current, err := get()
			if err != nil {
				return err
			}
			if err := validateOwnedIdentity(
				key,
				current.labels,
				current.uid,
				current.ownerReferences,
				scope,
				preconditions,
			); err != nil {
				return err
			}
			return mutate(current)
		},
	)
}

func (adapter *Adapter) applyLease(
	ctx context.Context,
	scope cluster.OwnershipScope,
	change cluster.ApplyLease,
	dryRun bool,
) error {
	objectLabels := map[string]string{}
	addOwnershipLabels(objectLabels, scope)
	holderIdentity := change.HolderIdentity()
	duration := change.LeaseDurationSeconds()
	renewTime := metav1.NewMicroTime(change.RenewTime())
	options := applyOptions(dryRun)
	preconditions := change.Preconditions()
	if preconditions.UID == "" {
		ownerReferences := ownerReferences(scope)
		_, err := adapter.client.CoordinationV1().
			Leases(change.Key().Namespace()).
			Create(ctx, &coordinationv1.Lease{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:       change.Key().Namespace(),
					Name:            change.Key().Name(),
					Labels:          objectLabels,
					OwnerReferences: ownerReferences,
				},
				Spec: coordinationv1.LeaseSpec{
					HolderIdentity:       &holderIdentity,
					LeaseDurationSeconds: &duration,
					RenewTime:            &renewTime,
				},
			}, metav1.CreateOptions{DryRun: options.DryRun})
		if apierrors.IsAlreadyExists(err) {
			return cluster.NewError(
				cluster.ErrorOwnershipConflict,
				fmt.Sprintf(
					"Lease %q appeared before create and was not adopted",
					change.Key().Name(),
				),
				false,
			)
		}
		return classify("create exact Synthetic Node Lease", err)
	}
	err := adapter.retryOwnedMutation(
		ctx,
		change.Key(),
		scope,
		preconditions,
		func() (ownedObjectMetadata, error) {
			current, getErr := adapter.client.CoordinationV1().
				Leases(change.Key().Namespace()).
				Get(ctx, change.Key().Name(), metav1.GetOptions{})
			if getErr != nil {
				return ownedObjectMetadata{}, getErr
			}
			return metadataOf(current), nil
		},
		func(current ownedObjectMetadata) error {
			configuration := coordinationapplyv1.Lease(
				change.Key().Name(),
				change.Key().Namespace(),
			).
				WithUID(current.uid).
				WithResourceVersion(current.resourceVersion).
				WithLabels(objectLabels).
				WithSpec(
					coordinationapplyv1.LeaseSpec().
						WithHolderIdentity(holderIdentity).
						WithLeaseDurationSeconds(duration).
						WithRenewTime(renewTime),
				)
			if owner := ownerReferenceApply(scope); owner != nil {
				configuration = configuration.WithOwnerReferences(owner)
			}
			_, applyErr := adapter.client.CoordinationV1().
				Leases(change.Key().Namespace()).
				Apply(ctx, configuration, options)
			return applyErr
		},
	)
	return classify("server-side apply exact Synthetic Node Lease", err)
}

func (adapter *Adapter) applyDeviceClass(
	ctx context.Context,
	scope cluster.OwnershipScope,
	change cluster.ApplyDeviceClass,
	dryRun bool,
) error {
	selectors, applySelectors := deviceClassSelectors(change.Selectors())
	objectLabels := map[string]string{}
	addOwnershipLabels(objectLabels, scope)
	preconditions := change.Preconditions()
	if preconditions.UID == "" {
		_, err := adapter.client.ResourceV1().DeviceClasses().Create(
			ctx,
			&resourcev1.DeviceClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:            change.Key().Name(),
					Labels:          objectLabels,
					OwnerReferences: ownerReferences(scope),
				},
				Spec: resourcev1.DeviceClassSpec{Selectors: selectors},
			},
			draCreateOptions(dryRun),
		)
		if apierrors.IsAlreadyExists(err) {
			return cluster.NewError(
				cluster.ErrorOwnershipConflict,
				fmt.Sprintf(
					"DeviceClass %q appeared before create and was not adopted",
					change.Key().Name(),
				),
				false,
			)
		}
		return classify("create exact stable DRA DeviceClass", err)
	}
	current, err := adapter.client.ResourceV1().DeviceClasses().Get(
		ctx,
		change.Key().Name(),
		metav1.GetOptions{},
	)
	if err != nil {
		return classify("revalidate stable DRA DeviceClass before apply", err)
	}
	if err := validateOwnedMetadata(
		change.Key(),
		current.Labels,
		current.UID,
		current.ResourceVersion,
		current.OwnerReferences,
		scope,
		preconditions,
	); err != nil {
		return err
	}
	configuration := resourceapplyv1.DeviceClass(change.Key().Name()).
		WithUID(current.UID).
		WithResourceVersion(current.ResourceVersion).
		WithLabels(objectLabels).
		WithSpec(
			resourceapplyv1.DeviceClassSpec().
				WithSelectors(applySelectors...),
		)
	if owner := ownerReferenceApply(scope); owner != nil {
		configuration = configuration.WithOwnerReferences(owner)
	}
	_, err = adapter.client.ResourceV1().DeviceClasses().Apply(
		ctx,
		configuration,
		draApplyOptions(dryRun),
	)
	return classify("server-side apply exact stable DRA DeviceClass", err)
}

func (adapter *Adapter) applyResourceSlice(
	ctx context.Context,
	scope cluster.OwnershipScope,
	change cluster.ApplyResourceSlice,
	dryRun bool,
) error {
	devices, applyDevices, err := resourceSliceDevices(change.Devices())
	if err != nil {
		return err
	}
	objectLabels := map[string]string{}
	addOwnershipLabels(objectLabels, scope)
	preconditions := change.Preconditions()
	nodeName := change.NodeName()
	if preconditions.UID == "" {
		_, err := adapter.client.ResourceV1().ResourceSlices().Create(
			ctx,
			&resourcev1.ResourceSlice{
				ObjectMeta: metav1.ObjectMeta{
					Name:            change.Key().Name(),
					Labels:          objectLabels,
					OwnerReferences: ownerReferences(scope),
				},
				Spec: resourcev1.ResourceSliceSpec{
					Driver: change.Driver(),
					Pool: resourcev1.ResourcePool{
						Name:               change.PoolName(),
						Generation:         change.PoolGeneration(),
						ResourceSliceCount: change.ResourceSliceCount(),
					},
					NodeName: &nodeName,
					Devices:  devices,
				},
			},
			draCreateOptions(dryRun),
		)
		if apierrors.IsAlreadyExists(err) {
			return cluster.NewError(
				cluster.ErrorOwnershipConflict,
				fmt.Sprintf(
					"ResourceSlice %q appeared before create and was not adopted",
					change.Key().Name(),
				),
				false,
			)
		}
		return classify("create exact stable DRA ResourceSlice", err)
	}
	current, err := adapter.client.ResourceV1().ResourceSlices().Get(
		ctx,
		change.Key().Name(),
		metav1.GetOptions{},
	)
	if err != nil {
		return classify("revalidate stable DRA ResourceSlice before apply", err)
	}
	if err := validateOwnedMetadata(
		change.Key(),
		current.Labels,
		current.UID,
		current.ResourceVersion,
		current.OwnerReferences,
		scope,
		preconditions,
	); err != nil {
		return err
	}
	configuration := resourceapplyv1.ResourceSlice(change.Key().Name()).
		WithUID(current.UID).
		WithResourceVersion(current.ResourceVersion).
		WithLabels(objectLabels).
		WithSpec(
			resourceapplyv1.ResourceSliceSpec().
				WithDriver(change.Driver()).
				WithPool(
					resourceapplyv1.ResourcePool().
						WithName(change.PoolName()).
						WithGeneration(change.PoolGeneration()).
						WithResourceSliceCount(change.ResourceSliceCount()),
				).
				WithNodeName(change.NodeName()).
				WithDevices(applyDevices...),
		)
	if owner := ownerReferenceApply(scope); owner != nil {
		configuration = configuration.WithOwnerReferences(owner)
	}
	_, err = adapter.client.ResourceV1().ResourceSlices().Apply(
		ctx,
		configuration,
		draApplyOptions(dryRun),
	)
	return classify("server-side apply exact stable DRA ResourceSlice", err)
}

func (adapter *Adapter) deleteOwned(
	ctx context.Context,
	scope cluster.OwnershipScope,
	change cluster.DeleteOwnedObject,
	dryRun bool,
) error {
	key := change.Key()
	preconditions := change.Preconditions()
	var labels map[string]string
	var actualUID types.UID
	var resourceVersion string
	var objectOwnerReferences []metav1.OwnerReference
	switch key.Kind() {
	case cluster.ObjectKindNode:
		object, err := adapter.client.CoreV1().Nodes().Get(
			ctx,
			key.Name(),
			metav1.GetOptions{},
		)
		if err != nil {
			return classify("revalidate owned Node", err)
		}
		labels = object.Labels
		actualUID = object.UID
		resourceVersion = object.ResourceVersion
		objectOwnerReferences = object.OwnerReferences
	case cluster.ObjectKindLease:
		object, err := adapter.client.CoordinationV1().
			Leases(key.Namespace()).
			Get(ctx, key.Name(), metav1.GetOptions{})
		if err != nil {
			return classify("revalidate owned Lease", err)
		}
		labels = object.Labels
		actualUID = object.UID
		resourceVersion = object.ResourceVersion
		objectOwnerReferences = object.OwnerReferences
	case cluster.ObjectKindDeviceClass:
		object, err := adapter.client.ResourceV1().DeviceClasses().Get(
			ctx,
			key.Name(),
			metav1.GetOptions{},
		)
		if err != nil {
			return classify("revalidate owned DeviceClass", err)
		}
		labels = object.Labels
		actualUID = object.UID
		resourceVersion = object.ResourceVersion
		objectOwnerReferences = object.OwnerReferences
	case cluster.ObjectKindResourceSlice:
		object, err := adapter.client.ResourceV1().ResourceSlices().Get(
			ctx,
			key.Name(),
			metav1.GetOptions{},
		)
		if err != nil {
			return classify("revalidate owned ResourceSlice", err)
		}
		labels = object.Labels
		actualUID = object.UID
		resourceVersion = object.ResourceVersion
		objectOwnerReferences = object.OwnerReferences
	default:
		return cluster.NewError(
			cluster.ErrorInvalidIntent,
			"delete target is not allowlisted",
			false,
		)
	}
	if err := validateOwnedMetadata(
		key,
		labels,
		actualUID,
		resourceVersion,
		objectOwnerReferences,
		scope,
		preconditions,
	); err != nil {
		return err
	}
	propagation := metav1.DeletePropagationBackground
	options := metav1.DeleteOptions{
		Preconditions: &metav1.Preconditions{
			UID:             &actualUID,
			ResourceVersion: &resourceVersion,
		},
		PropagationPolicy: &propagation,
	}
	if dryRun {
		options.DryRun = []string{metav1.DryRunAll}
	}
	var err error
	switch key.Kind() {
	case cluster.ObjectKindNode:
		err = adapter.client.CoreV1().Nodes().Delete(
			ctx,
			key.Name(),
			options,
		)
	case cluster.ObjectKindLease:
		err = adapter.client.CoordinationV1().
			Leases(key.Namespace()).
			Delete(ctx, key.Name(), options)
	case cluster.ObjectKindDeviceClass:
		err = adapter.client.ResourceV1().DeviceClasses().
			Delete(ctx, key.Name(), options)
	case cluster.ObjectKindResourceSlice:
		err = adapter.client.ResourceV1().ResourceSlices().
			Delete(ctx, key.Name(), options)
	}
	if err != nil {
		return classify("delete exact owned object", err)
	}
	return nil
}

func validateOwnedMetadata(
	key cluster.ObjectKey,
	objectLabels map[string]string,
	actualUID types.UID,
	resourceVersion string,
	objectOwnerReferences []metav1.OwnerReference,
	scope cluster.OwnershipScope,
	preconditions cluster.ObjectPreconditions,
) error {
	if err := validateOwnedIdentity(
		key,
		objectLabels,
		actualUID,
		objectOwnerReferences,
		scope,
		preconditions,
	); err != nil {
		return err
	}
	if resourceVersion != preconditions.ResourceVersion {
		return cluster.NewError(
			cluster.ErrorResourceVersionConflict,
			fmt.Sprintf(
				"%s %q resourceVersion precondition failed",
				key.Kind(),
				key.Name(),
			),
			false,
		)
	}
	return nil
}

func validateOwnedIdentity(
	key cluster.ObjectKey,
	objectLabels map[string]string,
	actualUID types.UID,
	objectOwnerReferences []metav1.OwnerReference,
	scope cluster.OwnershipScope,
	preconditions cluster.ObjectPreconditions,
) error {
	if objectLabels[cluster.ManagedByLabel] != cluster.ManagedByValue ||
		objectLabels[cluster.InstanceUIDLabel] != scope.InstanceUID().String() {
		return cluster.NewError(
			cluster.ErrorOwnershipConflict,
			fmt.Sprintf(
				"%s %q is not owned by the exact Scenario Instance UID",
				key.Kind(),
				key.Name(),
			),
			false,
		)
	}
	encodedGeneration := objectLabels[cluster.DesiredGenerationLabel]
	actualGeneration, err := strconv.ParseUint(encodedGeneration, 10, 64)
	if err != nil || actualGeneration == 0 ||
		strconv.FormatUint(actualGeneration, 10) != encodedGeneration {
		return cluster.NewError(
			cluster.ErrorOwnershipConflict,
			fmt.Sprintf(
				"%s %q does not carry a valid desired generation",
				key.Kind(),
				key.Name(),
			),
			false,
		)
	}
	if actualGeneration > scope.DesiredGeneration().Value() {
		return cluster.NewError(
			cluster.ErrorResourceVersionConflict,
			fmt.Sprintf(
				"%s %q desired generation advanced from %d to %d",
				key.Kind(),
				key.Name(),
				scope.DesiredGeneration().Value(),
				actualGeneration,
			),
			false,
		)
	}
	if err := validateOwnerReference(objectOwnerReferences, scope); err != nil {
		return err
	}
	if string(actualUID) != preconditions.UID {
		return cluster.NewError(
			cluster.ErrorUIDConflict,
			fmt.Sprintf("%s %q UID precondition failed", key.Kind(), key.Name()),
			false,
		)
	}
	return nil
}

func addOwnershipLabels(
	objectLabels map[string]string,
	scope cluster.OwnershipScope,
) {
	objectLabels[cluster.ManagedByLabel] = cluster.ManagedByValue
	objectLabels[cluster.InstanceUIDLabel] = scope.InstanceUID().String()
	objectLabels[cluster.DesiredGenerationLabel] = strconv.FormatUint(
		scope.DesiredGeneration().Value(),
		10,
	)
}

func ownerReferences(scope cluster.OwnershipScope) []metav1.OwnerReference {
	if scope.InstanceName().String() == "" {
		return nil
	}
	controller := true
	blockOwnerDeletion := true
	return []metav1.OwnerReference{{
		APIVersion:         "simulation.kasim.io/v1alpha1",
		Kind:               "ScenarioInstance",
		Name:               scope.InstanceName().String(),
		UID:                types.UID(scope.InstanceUID().String()),
		Controller:         &controller,
		BlockOwnerDeletion: &blockOwnerDeletion,
	}}
}

func ownerReferenceApply(
	scope cluster.OwnershipScope,
) *metav1apply.OwnerReferenceApplyConfiguration {
	if scope.InstanceName().String() == "" {
		return nil
	}
	return metav1apply.OwnerReference().
		WithAPIVersion("simulation.kasim.io/v1alpha1").
		WithKind("ScenarioInstance").
		WithName(scope.InstanceName().String()).
		WithUID(types.UID(scope.InstanceUID().String())).
		WithController(true).
		WithBlockOwnerDeletion(true)
}

func applyOptions(dryRun bool) metav1.ApplyOptions {
	options := metav1.ApplyOptions{
		FieldManager: cluster.ManagedByValue,
		Force:        true,
	}
	if dryRun {
		options.DryRun = []string{metav1.DryRunAll}
	}
	return options
}

func draCreateOptions(dryRun bool) metav1.CreateOptions {
	options := metav1.CreateOptions{
		FieldValidation: metav1.FieldValidationStrict,
	}
	if dryRun {
		options.DryRun = []string{metav1.DryRunAll}
	}
	return options
}

func draApplyOptions(dryRun bool) metav1.ApplyOptions {
	options := metav1.ApplyOptions{
		FieldManager: cluster.ManagedByValue,
		Force:        false,
	}
	if dryRun {
		options.DryRun = []string{metav1.DryRunAll}
	}
	return options
}

func deviceClassSelectors(
	expressions []string,
) ([]resourcev1.DeviceSelector, []*resourceapplyv1.DeviceSelectorApplyConfiguration) {
	selectors := make([]resourcev1.DeviceSelector, 0, len(expressions))
	applySelectors := make(
		[]*resourceapplyv1.DeviceSelectorApplyConfiguration,
		0,
		len(expressions),
	)
	for _, expression := range expressions {
		selectors = append(selectors, resourcev1.DeviceSelector{
			CEL: &resourcev1.CELDeviceSelector{Expression: expression},
		})
		applySelectors = append(
			applySelectors,
			resourceapplyv1.DeviceSelector().WithCEL(
				resourceapplyv1.CELDeviceSelector().
					WithExpression(expression),
			),
		)
	}
	return selectors, applySelectors
}

func resourceSliceDevices(
	values []cluster.DRADevice,
) ([]resourcev1.Device, []*resourceapplyv1.DeviceApplyConfiguration, error) {
	devices := make([]resourcev1.Device, 0, len(values))
	applyDevices := make(
		[]*resourceapplyv1.DeviceApplyConfiguration,
		0,
		len(values),
	)
	for _, value := range values {
		attributes := make(
			map[resourcev1.QualifiedName]resourcev1.DeviceAttribute,
			len(value.Attributes),
		)
		applyAttributes := make(
			map[resourcev1.QualifiedName]resourceapplyv1.DeviceAttributeApplyConfiguration,
			len(value.Attributes),
		)
		for key, attribute := range value.Attributes {
			qualifiedName := resourcev1.QualifiedName(key)
			switch attribute.Kind() {
			case cluster.DeviceAttributeBool:
				boolValue := attribute.Bool()
				attributes[qualifiedName] = resourcev1.DeviceAttribute{
					BoolValue: &boolValue,
				}
				applyAttributes[qualifiedName] =
					*resourceapplyv1.DeviceAttribute().
						WithBoolValue(boolValue)
			case cluster.DeviceAttributeString:
				stringValue := attribute.String()
				attributes[qualifiedName] = resourcev1.DeviceAttribute{
					StringValue: &stringValue,
				}
				applyAttributes[qualifiedName] =
					*resourceapplyv1.DeviceAttribute().
						WithStringValue(stringValue)
			default:
				return nil, nil, cluster.NewError(
					cluster.ErrorInvalidIntent,
					fmt.Sprintf(
						"device %q has unsupported stable DRA attribute %q",
						value.Name,
						key,
					),
					false,
				)
			}
		}
		devices = append(devices, resourcev1.Device{
			Name:       value.Name,
			Attributes: attributes,
		})
		applyDevices = append(
			applyDevices,
			resourceapplyv1.Device().
				WithName(value.Name).
				WithAttributes(applyAttributes),
		)
	}
	return devices, applyDevices, nil
}

func resourceList(values map[string]string) (corev1.ResourceList, error) {
	result := make(corev1.ResourceList, len(values))
	for name, value := range values {
		if name == "" {
			return nil, cluster.NewError(
				cluster.ErrorInvalidIntent,
				"Synthetic Node resource name is empty",
				false,
			)
		}
		quantity, err := resource.ParseQuantity(value)
		if err != nil {
			return nil, cluster.NewError(
				cluster.ErrorInvalidIntent,
				fmt.Sprintf(
					"Synthetic Node resource %q has an invalid quantity",
					name,
				),
				false,
			)
		}
		result[corev1.ResourceName(name)] = quantity
	}
	return result, nil
}

func observedObject(
	key cluster.ObjectKey,
	uid types.UID,
	resourceVersion string,
	objectLabels map[string]string,
	objectOwnerReferences []metav1.OwnerReference,
	scope cluster.OwnershipScope,
) (cluster.ObservedObject, error) {
	if objectLabels[cluster.ManagedByLabel] != cluster.ManagedByValue ||
		objectLabels[cluster.InstanceUIDLabel] != scope.InstanceUID().String() {
		return cluster.ObservedObject{}, cluster.NewError(
			cluster.ErrorOwnershipConflict,
			"owned-object selector returned an object with conflicting identity",
			false,
		)
	}
	if err := validateOwnerReference(objectOwnerReferences, scope); err != nil {
		return cluster.ObservedObject{}, err
	}
	value, err := strconv.ParseInt(
		objectLabels[cluster.DesiredGenerationLabel],
		10,
		64,
	)
	if err != nil || value <= 0 {
		return cluster.ObservedObject{}, cluster.NewError(
			cluster.ErrorOwnershipConflict,
			"owned object has no valid desired-generation identity",
			false,
		)
	}
	generation, err := domain.NewGeneration(value)
	if err != nil {
		return cluster.ObservedObject{}, err
	}
	if uid == "" || resourceVersion == "" {
		return cluster.ObservedObject{}, cluster.NewError(
			cluster.ErrorOwnershipConflict,
			"owned object has no server identity",
			false,
		)
	}
	return cluster.ObservedObject{
		Key:               key,
		UID:               string(uid),
		ResourceVersion:   resourceVersion,
		DesiredGeneration: generation,
	}, nil
}

func validateOwnerReference(
	references []metav1.OwnerReference,
	scope cluster.OwnershipScope,
) error {
	if scope.InstanceName().String() == "" {
		return nil
	}
	for _, reference := range references {
		if reference.APIVersion == "simulation.kasim.io/v1alpha1" &&
			reference.Kind == "ScenarioInstance" &&
			reference.Name == scope.InstanceName().String() &&
			string(reference.UID) == scope.InstanceUID().String() &&
			reference.Controller != nil &&
			*reference.Controller &&
			reference.BlockOwnerDeletion != nil &&
			*reference.BlockOwnerDeletion {
			return nil
		}
	}
	return cluster.NewError(
		cluster.ErrorOwnershipConflict,
		"owned object has no exact Scenario Instance controller owner reference",
		false,
	)
}

func observedNodeState(node *corev1.Node) *cluster.ObservedNodeState {
	taints := make([]cluster.NodeTaint, 0, len(node.Spec.Taints))
	for _, taint := range node.Spec.Taints {
		taints = append(taints, cluster.NodeTaint{
			Key:    taint.Key,
			Value:  taint.Value,
			Effect: string(taint.Effect),
		})
	}
	ready := false
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			ready = condition.Status == corev1.ConditionTrue
			break
		}
	}
	return &cluster.ObservedNodeState{
		Labels:        cloneStringMap(node.Labels),
		Annotations:   cloneStringMap(node.Annotations),
		Taints:        taints,
		Unschedulable: node.Spec.Unschedulable,
		Capacity:      encodedResourceList(node.Status.Capacity),
		Allocatable:   encodedResourceList(node.Status.Allocatable),
		Ready:         ready,
	}
}

func observedLeaseState(lease *coordinationv1.Lease) *cluster.ObservedLeaseState {
	state := &cluster.ObservedLeaseState{}
	if lease.Spec.HolderIdentity != nil {
		state.HolderIdentity = *lease.Spec.HolderIdentity
	}
	if lease.Spec.LeaseDurationSeconds != nil {
		state.LeaseDurationSeconds = *lease.Spec.LeaseDurationSeconds
	}
	if lease.Spec.RenewTime != nil {
		state.RenewTime = lease.Spec.RenewTime.Time
	}
	return state
}

func observedDeviceClassState(
	deviceClass *resourcev1.DeviceClass,
) *cluster.ObservedDeviceClassState {
	selectors := make([]string, 0, len(deviceClass.Spec.Selectors))
	for _, selector := range deviceClass.Spec.Selectors {
		if selector.CEL == nil {
			selectors = append(selectors, "<unsupported-non-cel-selector>")
			continue
		}
		selectors = append(selectors, selector.CEL.Expression)
	}
	return &cluster.ObservedDeviceClassState{Selectors: selectors}
}

func observedResourceSliceState(
	resourceSlice *resourcev1.ResourceSlice,
) *cluster.ObservedResourceSliceState {
	state := &cluster.ObservedResourceSliceState{
		Driver:             resourceSlice.Spec.Driver,
		PoolName:           resourceSlice.Spec.Pool.Name,
		PoolGeneration:     resourceSlice.Spec.Pool.Generation,
		ResourceSliceCount: resourceSlice.Spec.Pool.ResourceSliceCount,
		Devices:            make([]cluster.DRADevice, 0, len(resourceSlice.Spec.Devices)),
	}
	if resourceSlice.Spec.NodeName != nil {
		state.NodeName = *resourceSlice.Spec.NodeName
	}
	for _, device := range resourceSlice.Spec.Devices {
		attributes := make(
			map[string]cluster.DeviceAttributeValue,
			len(device.Attributes),
		)
		for key, value := range device.Attributes {
			switch {
			case value.BoolValue != nil:
				attributes[string(key)] =
					cluster.NewBoolDeviceAttribute(*value.BoolValue)
			case value.StringValue != nil:
				attribute, err := cluster.NewStringDeviceAttribute(
					*value.StringValue,
				)
				if err != nil {
					attribute, _ = cluster.NewStringDeviceAttribute(
						"<unsupported-value>",
					)
				}
				attributes[string(key)] = attribute
			default:
				attribute, _ := cluster.NewStringDeviceAttribute(
					"<unsupported-kind>",
				)
				attributes[string(key)] = attribute
			}
		}
		state.Devices = append(state.Devices, cluster.DRADevice{
			Name:       device.Name,
			Attributes: attributes,
		})
	}
	return state
}

func observedResourceClaim(
	claim *resourcev1.ResourceClaim,
) cluster.ObservedResourceClaim {
	result := cluster.ObservedResourceClaim{
		Namespace:       claim.Namespace,
		Name:            claim.Name,
		UID:             string(claim.UID),
		ResourceVersion: claim.ResourceVersion,
	}
	for _, request := range claim.Spec.Devices.Requests {
		if request.Exactly != nil {
			result.DeviceClassNames = append(
				result.DeviceClassNames,
				request.Exactly.DeviceClassName,
			)
		}
		for _, subrequest := range request.FirstAvailable {
			result.DeviceClassNames = append(
				result.DeviceClassNames,
				subrequest.DeviceClassName,
			)
		}
	}
	slices.Sort(result.DeviceClassNames)
	if claim.Status.Allocation != nil {
		for _, allocation := range claim.Status.Allocation.Devices.Results {
			result.Allocations = append(
				result.Allocations,
				cluster.DRAAllocationResult{
					Request: allocation.Request,
					Driver:  allocation.Driver,
					Pool:    allocation.Pool,
					Device:  allocation.Device,
				},
			)
		}
	}
	for _, reservation := range claim.Status.ReservedFor {
		result.ReservedFor = append(
			result.ReservedFor,
			cluster.DRAConsumerReference{
				APIGroup: reservation.APIGroup,
				Resource: reservation.Resource,
				Name:     reservation.Name,
				UID:      string(reservation.UID),
			},
		)
	}
	return result
}

func claimReferencesOwnedDRA(
	claim cluster.ObservedResourceClaim,
	classNames,
	deviceTuples map[string]struct{},
) bool {
	for _, name := range claim.DeviceClassNames {
		if _, found := classNames[name]; found {
			return true
		}
	}
	for _, allocation := range claim.Allocations {
		if _, found := deviceTuples[draTuple(
			allocation.Driver,
			allocation.Pool,
			allocation.Device,
		)]; found {
			return true
		}
	}
	return false
}

func draTuple(driver, pool, device string) string {
	return driver + "\x00" + pool + "\x00" + device
}

func podReferencesResourceClaim(
	pod *corev1.Pod,
	claimNames map[string]struct{},
) bool {
	if len(claimNames) == 0 {
		return false
	}
	for _, claim := range pod.Spec.ResourceClaims {
		if claim.ResourceClaimName == nil {
			continue
		}
		if _, found := claimNames[*claim.ResourceClaimName]; found {
			return true
		}
	}
	return false
}

func observedPod(pod *corev1.Pod) cluster.ObservedPod {
	result := cluster.ObservedPod{
		Namespace: pod.Namespace,
		Name:      pod.Name,
		UID:       string(pod.UID),
		NodeName:  pod.Spec.NodeName,
		Phase:     string(pod.Status.Phase),
		Requested: encodedResourceList(effectivePodRequests(pod)),
	}
	for _, claim := range pod.Spec.ResourceClaims {
		if claim.ResourceClaimName != nil {
			result.ResourceClaims = append(
				result.ResourceClaims,
				*claim.ResourceClaimName,
			)
		}
	}
	slices.Sort(result.ResourceClaims)
	return result
}

func effectivePodRequests(pod *corev1.Pod) corev1.ResourceList {
	regular := corev1.ResourceList{}
	for _, container := range pod.Spec.Containers {
		addResourceList(regular, container.Resources.Requests)
	}

	restartableInit := corev1.ResourceList{}
	initMaximum := corev1.ResourceList{}
	for _, container := range pod.Spec.InitContainers {
		current := cloneResourceList(restartableInit)
		if container.RestartPolicy != nil &&
			*container.RestartPolicy == corev1.ContainerRestartPolicyAlways {
			addResourceList(restartableInit, container.Resources.Requests)
			current = cloneResourceList(restartableInit)
		} else {
			addResourceList(current, container.Resources.Requests)
		}
		maxResourceList(initMaximum, current)
	}
	addResourceList(regular, restartableInit)
	maxResourceList(regular, initMaximum)
	addResourceList(regular, pod.Spec.Overhead)
	return regular
}

func addResourceList(target, values corev1.ResourceList) {
	for name, value := range values {
		current := target[name]
		current.Add(value)
		target[name] = current
	}
}

func maxResourceList(target, values corev1.ResourceList) {
	for name, value := range values {
		current, found := target[name]
		if !found || current.Cmp(value) < 0 {
			target[name] = value.DeepCopy()
		}
	}
}

func cloneResourceList(input corev1.ResourceList) corev1.ResourceList {
	result := make(corev1.ResourceList, len(input))
	for name, value := range input {
		result[name] = value.DeepCopy()
	}
	return result
}

func encodedResourceList(input corev1.ResourceList) map[string]string {
	result := make(map[string]string, len(input))
	for name, value := range input {
		result[string(name)] = value.String()
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

func ownershipSelector(instanceUID string) string {
	return labels.Set{
		cluster.ManagedByLabel:   cluster.ManagedByValue,
		cluster.InstanceUIDLabel: instanceUID,
	}.AsSelector().String()
}

func parseMinor(info *version.Info) (int, error) {
	if info == nil {
		return 0, fmt.Errorf("Kubernetes server returned no version")
	}
	if info.Major != "1" {
		return 0, fmt.Errorf(
			"Kubernetes server major %q is unsupported",
			info.Major,
		)
	}
	value := strings.TrimSpace(info.Minor)
	value = strings.TrimRight(value, "+")
	end := 0
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, fmt.Errorf(
			"Kubernetes server minor %q is not parseable",
			info.Minor,
		)
	}
	minor, err := strconv.Atoi(value[:end])
	if err != nil {
		return 0, fmt.Errorf("parse Kubernetes server minor: %w", err)
	}
	return minor, nil
}

func classify(operation string, err error) error {
	if err == nil {
		return nil
	}
	if cluster.ErrorCodeOf(err) != "" {
		return err
	}
	switch {
	case apierrors.IsUnauthorized(err):
		return cluster.NewError(
			cluster.ErrorAuthenticationFailed,
			operation+": authentication failed",
			false,
		)
	case apierrors.IsForbidden(err):
		return cluster.NewError(
			cluster.ErrorAuthorizationDenied,
			operation+": authorization denied",
			false,
		)
	case apierrors.IsConflict(err):
		return cluster.NewError(
			cluster.ErrorResourceVersionConflict,
			operation+": Kubernetes conflict",
			false,
		)
	case apierrors.IsInvalid(err), apierrors.IsBadRequest(err):
		return cluster.NewError(
			cluster.ErrorAdmissionRejected,
			operation+": Kubernetes admission rejected the request",
			false,
		)
	case apierrors.IsTooManyRequests(err):
		return cluster.NewError(
			cluster.ErrorRateLimited,
			operation+": Kubernetes rate limit exceeded",
			true,
		)
	case apierrors.IsTimeout(err), apierrors.IsServerTimeout(err),
		apierrors.IsServiceUnavailable(err):
		return cluster.NewError(
			cluster.ErrorTransient,
			operation+": Kubernetes is temporarily unavailable",
			true,
		)
	case apierrors.IsNotFound(err):
		return cluster.NewError(
			cluster.ErrorCapabilityUnavailable,
			operation+": required object or API was not found",
			false,
		)
	default:
		return cluster.NewError(
			cluster.ErrorTargetUnavailable,
			operation+": Kubernetes request failed",
			true,
		)
	}
}

var _ cluster.Port = (*Adapter)(nil)
