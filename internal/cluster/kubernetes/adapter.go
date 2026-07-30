package kubernetes

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	authorizationv1 "k8s.io/api/authorization/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/version"
	coordinationapplyv1 "k8s.io/client-go/applyconfigurations/coordination/v1"
	coreapplyv1 "k8s.io/client-go/applyconfigurations/core/v1"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"
	clientset "k8s.io/client-go/kubernetes"

	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

const (
	discoveryPageSize = int64(200)
	minimumMinor      = 30
	maximumMinor      = 36
)

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
	slices.Sort(ownedNodeNames)
	pods := make([]cluster.ObservedPod, 0)
	for _, nodeName := range ownedNodeNames {
		continueToken = ""
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
				return cluster.ObservedGraph{}, classify(
					"observe Pods bound to owned Nodes",
					err,
				)
			}
			for index := range result.Items {
				pod := &result.Items[index]
				// The field selector is the authoritative server-side bound,
				// while this equality check also protects test clients and
				// proxies that do not enforce field selectors.
				if pod.Spec.NodeName != nodeName {
					continue
				}
				pods = append(pods, observedPod(pod))
				if len(pods) > cluster.MaximumObservedPods {
					return cluster.ObservedGraph{}, cluster.NewError(
						cluster.ErrorCapabilityUnavailable,
						"owned-node Pod observation exceeded its bounded limit",
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
	slices.SortFunc(pods, func(left, right cluster.ObservedPod) int {
		if compared := strings.Compare(left.Namespace, right.Namespace); compared != 0 {
			return compared
		}
		return strings.Compare(left.Name, right.Name)
	})
	return cluster.ObservedGraph{Objects: objects, Pods: pods}, nil
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
	for _, change := range changes {
		var err error
		switch typed := change.(type) {
		case cluster.ApplySyntheticNode:
			err = adapter.applySyntheticNode(
				ctx,
				changeSet.Scope(),
				typed,
				receipt.DryRun,
			)
		case cluster.UpdateSyntheticNodeStatus:
			err = adapter.updateSyntheticNodeStatus(
				ctx,
				changeSet.Scope(),
				typed,
				receipt.DryRun,
			)
		case cluster.ApplyLease:
			err = adapter.applyLease(
				ctx,
				changeSet.Scope(),
				typed,
				receipt.DryRun,
			)
		case cluster.DeleteOwnedObject:
			err = adapter.deleteOwned(
				ctx,
				changeSet.Scope(),
				typed,
				receipt.DryRun,
			)
		default:
			err = cluster.NewError(
				cluster.ErrorInvalidIntent,
				"Cluster adapter received an unsupported change intention",
				false,
			)
		}
		if err != nil {
			return receipt, err
		}
		if !receipt.DryRun {
			receipt.Persisted++
		}
	}
	return receipt, nil
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
	current, err := adapter.client.CoreV1().Nodes().Get(
		ctx,
		change.Key().Name(),
		metav1.GetOptions{},
	)
	if err != nil {
		return classify("revalidate Synthetic Node before apply", err)
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
	configuration := coreapplyv1.Node(change.Key().Name()).
		WithUID(current.UID).
		WithResourceVersion(current.ResourceVersion).
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
	_, err = adapter.client.CoreV1().Nodes().Apply(
		ctx,
		configuration,
		options,
	)
	return classify("server-side apply exact Synthetic Node", err)
}

func (adapter *Adapter) updateSyntheticNodeStatus(
	ctx context.Context,
	scope cluster.OwnershipScope,
	change cluster.UpdateSyntheticNodeStatus,
	dryRun bool,
) error {
	current, err := adapter.client.CoreV1().Nodes().Get(
		ctx,
		change.Key().Name(),
		metav1.GetOptions{},
	)
	if err != nil {
		return classify("revalidate Synthetic Node before status apply", err)
	}
	if err := validateOwnedMetadata(
		change.Key(),
		current.Labels,
		current.UID,
		current.ResourceVersion,
		current.OwnerReferences,
		scope,
		change.Preconditions(),
	); err != nil {
		return err
	}
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
	configuration := coreapplyv1.Node(change.Key().Name()).
		WithUID(current.UID).
		WithResourceVersion(current.ResourceVersion).
		WithStatus(status)
	_, err = adapter.client.CoreV1().Nodes().ApplyStatus(
		ctx,
		configuration,
		applyOptions(dryRun),
	)
	return classify("apply exact Synthetic Node status", err)
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
	current, err := adapter.client.CoordinationV1().
		Leases(change.Key().Namespace()).
		Get(ctx, change.Key().Name(), metav1.GetOptions{})
	if err != nil {
		return classify("revalidate Synthetic Node Lease before apply", err)
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
	configuration := coordinationapplyv1.Lease(
		change.Key().Name(),
		change.Key().Namespace(),
	).
		WithUID(current.UID).
		WithResourceVersion(current.ResourceVersion).
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
	_, err = adapter.client.CoordinationV1().
		Leases(change.Key().Namespace()).
		Apply(ctx, configuration, options)
	return classify("server-side apply exact Synthetic Node Lease", err)
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

func observedPod(pod *corev1.Pod) cluster.ObservedPod {
	return cluster.ObservedPod{
		Namespace: pod.Namespace,
		Name:      pod.Name,
		UID:       string(pod.UID),
		NodeName:  pod.Spec.NodeName,
		Phase:     string(pod.Status.Phase),
		Requested: encodedResourceList(effectivePodRequests(pod)),
	}
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
