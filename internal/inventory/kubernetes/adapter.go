// Package kubernetes implements the production Kubernetes source Adapter for
// the Cluster Simulation Inventory Module.
package kubernetes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	simulationv1alpha1 "github.com/LinkMaq/kube-accelerator-sim/api/simulation/v1alpha1"
	"github.com/LinkMaq/kube-accelerator-sim/internal/catalog"
	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/inventory"
	"github.com/LinkMaq/kube-accelerator-sim/internal/scenario"
)

const (
	listPageSize       = int64(500)
	pollInterval       = 30 * time.Second
	staleAfter         = 15 * time.Second
	debounceInterval   = 250 * time.Millisecond
	maximumPublishWait = 2 * time.Second
	minimumBackoff     = 250 * time.Millisecond
	maximumBackoff     = 30 * time.Second
)

var (
	nodesGVR     = schema.GroupVersionResource{Version: "v1", Resource: "nodes"}
	podsGVR      = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	scenariosGVR = schema.GroupVersionResource{
		Group: "simulation.kasim.io", Version: "v1alpha1", Resource: "scenarioinstances",
	}
	resourceSlicesGVR = schema.GroupVersionResource{
		Group: "resource.k8s.io", Version: "v1", Resource: "resourceslices",
	}
	resourceClaimsGVR = schema.GroupVersionResource{
		Group: "resource.k8s.io", Version: "v1", Resource: "resourceclaims",
	}
	deviceClassesGVR = schema.GroupVersionResource{
		Group: "resource.k8s.io", Version: "v1", Resource: "deviceclasses",
	}
)

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (*Adapter) Open(
	ctx context.Context,
	selection cluster.TargetSelection,
) (inventory.SourceStream, error) {
	config, canonicalPath, serverURL, caDigest, err := loadConfig(selection)
	if err != nil {
		return nil, err
	}
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("construct Kubernetes discovery client: %w", err)
	}
	serverVersion, err := discoveryClient.ServerVersion()
	if err != nil {
		return nil, fmt.Errorf("read Kubernetes server version: %w", err)
	}
	if _, err := supportedMinor(serverVersion); err != nil {
		return nil, err
	}
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("construct Kubernetes inventory client: %w", err)
	}
	catalogSnapshot, err := catalog.LoadBundled()
	if err != nil {
		return nil, fmt.Errorf("load bundled inventory profile catalog: %w", err)
	}
	fingerprintInput := strings.Join([]string{
		"kasim.inventory-target.v1", serverURL, caDigest, selection.ContextName,
	}, "\x00")
	fingerprint := sha256.Sum256([]byte(fingerprintInput))
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &sourceStream{
		client: dynamicClient,
		target: inventory.Target{
			ContextName:       selection.ContextName,
			Fingerprint:       "sha256:" + hex.EncodeToString(fingerprint[:]),
			KubernetesVersion: serverVersion.GitVersion,
		},
		canonicalKubeconfig: canonicalPath,
		catalog:             catalogSnapshot,
		updates:             make(chan inventory.Observation, 1),
		dirty:               make(chan struct{}, 1),
		done:                make(chan struct{}),
		cancel:              cancel,
		states:              make(map[inventory.SourceName]inventory.SourceState),
		objects:             make(map[inventory.SourceName][]unstructured.Unstructured),
	}
	go func() {
		defer close(stream.done)
		stream.run(streamCtx)
	}()
	return stream, nil
}

type sourceSpec struct {
	name       inventory.SourceName
	gvr        schema.GroupVersionResource
	namespaced bool
	limit      int
	optional   bool
}

var sourceSpecs = []sourceSpec{
	{name: inventory.SourceNodes, gvr: nodesGVR, limit: cluster.MaximumObservedObjects},
	{name: inventory.SourcePods, gvr: podsGVR, namespaced: true, limit: cluster.MaximumObservedPods},
	{name: inventory.SourceScenarios, gvr: scenariosGVR, limit: cluster.MaximumObservedObjects, optional: true},
	{name: inventory.SourceResourceSlices, gvr: resourceSlicesGVR, limit: cluster.MaximumObservedObjects, optional: true},
	{name: inventory.SourceResourceClaims, gvr: resourceClaimsGVR, namespaced: true, limit: cluster.MaximumObservedClaims, optional: true},
	{name: inventory.SourceDeviceClasses, gvr: deviceClassesGVR, limit: cluster.MaximumObservedObjects, optional: true},
}

type sourceStream struct {
	client              dynamic.Interface
	target              inventory.Target
	canonicalKubeconfig string
	catalog             catalog.Snapshot
	updates             chan inventory.Observation
	dirty               chan struct{}
	done                chan struct{}
	cancel              context.CancelFunc
	once                sync.Once
	mu                  sync.RWMutex
	states              map[inventory.SourceName]inventory.SourceState
	objects             map[inventory.SourceName][]unstructured.Unstructured
}

func (stream *sourceStream) Target() inventory.Target { return stream.target }

func (stream *sourceStream) Next(ctx context.Context) (inventory.Observation, error) {
	select {
	case observation := <-stream.updates:
		return observation, nil
	case <-stream.done:
		return inventory.Observation{}, fmt.Errorf("Kubernetes inventory stream is closed")
	case <-ctx.Done():
		return inventory.Observation{}, ctx.Err()
	}
}

func (stream *sourceStream) Close() error {
	stream.once.Do(func() {
		stream.cancel()
		<-stream.done
	})
	return nil
}

func (stream *sourceStream) run(ctx context.Context) {
	var sources sync.WaitGroup
	sources.Add(len(sourceSpecs))
	for _, spec := range sourceSpecs {
		spec := spec
		go func() {
			defer sources.Done()
			stream.syncSource(ctx, spec)
		}()
	}
	publisherDone := make(chan struct{})
	go func() {
		defer close(publisherDone)
		stream.publishLoop(ctx)
	}()
	sources.Wait()
	<-publisherDone
}

func (stream *sourceStream) syncSource(ctx context.Context, spec sourceSpec) {
	backoff := minimumBackoff
	for ctx.Err() == nil {
		items, resourceVersion, err := listAll(ctx, stream.client, spec)
		if err != nil {
			stream.recordSourceError(spec, err)
			if !waitContext(ctx, fullJitter(backoff)) {
				return
			}
			backoff = min(backoff*2, maximumBackoff)
			continue
		}
		stream.replaceSource(spec, items, inventory.SourceModeInitializing)
		watcher, err := resourceInterface(stream.client, spec).Watch(ctx, metav1.ListOptions{
			ResourceVersion:     resourceVersion,
			AllowWatchBookmarks: true,
		})
		if apierrors.IsForbidden(err) {
			stream.setSourceMode(spec, inventory.SourceModePolling)
			stream.pollSource(ctx, spec)
			return
		}
		if err != nil {
			stream.recordSourceError(spec, err)
			if !waitContext(ctx, fullJitter(backoff)) {
				return
			}
			backoff = min(backoff*2, maximumBackoff)
			continue
		}
		backoff = minimumBackoff
		stream.setSourceMode(spec, inventory.SourceModeLive)
		err = stream.consumeWatch(ctx, spec, watcher)
		watcher.Stop()
		if ctx.Err() != nil {
			return
		}
		stream.recordSourceError(spec, err)
		if apierrors.IsGone(err) {
			stream.setSourceFreshness(spec, inventory.FreshnessResyncing)
			continue
		}
		if !waitContext(ctx, fullJitter(backoff)) {
			return
		}
		backoff = min(backoff*2, maximumBackoff)
	}
}

func (stream *sourceStream) pollSource(ctx context.Context, spec sourceSpec) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			items, _, err := listAll(ctx, stream.client, spec)
			if err != nil {
				stream.recordSourceError(spec, err)
				continue
			}
			stream.replaceSource(spec, items, inventory.SourceModePolling)
		case <-ctx.Done():
			return
		}
	}
}

func (stream *sourceStream) consumeWatch(
	ctx context.Context,
	spec sourceSpec,
	watcher watch.Interface,
) error {
	for {
		select {
		case event, open := <-watcher.ResultChan():
			if !open {
				return fmt.Errorf("watch stream closed")
			}
			if event.Type == watch.Bookmark {
				stream.touchSource(spec)
				continue
			}
			if event.Type == watch.Error {
				return apierrors.FromObject(event.Object)
			}
			object, ok := event.Object.(*unstructured.Unstructured)
			if !ok || object == nil {
				return fmt.Errorf("watch returned an unsupported object schema")
			}
			if err := stream.applyWatchEvent(spec, event.Type, *object); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (stream *sourceStream) replaceSource(
	spec sourceSpec,
	items []unstructured.Unstructured,
	mode inventory.SourceMode,
) {
	now := time.Now().UTC()
	stream.mu.Lock()
	stream.objects[spec.name] = cloneObjects(items)
	stream.states[spec.name] = inventory.SourceState{
		Name: spec.name, Availability: inventory.AvailabilityAvailable,
		Mode: mode, Freshness: inventory.FreshnessFresh, LastSuccess: now,
	}
	stream.mu.Unlock()
	stream.markDirty()
}

func (stream *sourceStream) setSourceMode(
	spec sourceSpec,
	mode inventory.SourceMode,
) {
	stream.mu.Lock()
	state := stream.states[spec.name]
	state.Name = spec.name
	state.Availability = inventory.AvailabilityAvailable
	state.Mode = mode
	state.Freshness = inventory.FreshnessFresh
	state.LastSuccess = time.Now().UTC()
	state.Diagnostic = ""
	stream.states[spec.name] = state
	stream.mu.Unlock()
	stream.markDirty()
}

func (stream *sourceStream) setSourceFreshness(
	spec sourceSpec,
	freshness inventory.Freshness,
) {
	stream.mu.Lock()
	state := stream.states[spec.name]
	state.Name = spec.name
	state.Freshness = freshness
	stream.states[spec.name] = state
	stream.mu.Unlock()
	stream.markDirty()
}

func (stream *sourceStream) touchSource(spec sourceSpec) {
	stream.mu.Lock()
	state := stream.states[spec.name]
	state.Name = spec.name
	state.Availability = inventory.AvailabilityAvailable
	state.Mode = inventory.SourceModeLive
	state.Freshness = inventory.FreshnessFresh
	state.LastSuccess = time.Now().UTC()
	state.Diagnostic = ""
	stream.states[spec.name] = state
	stream.mu.Unlock()
}

func (stream *sourceStream) applyWatchEvent(
	spec sourceSpec,
	eventType watch.EventType,
	object unstructured.Unstructured,
) error {
	key := objectKey(object)
	if key == "" {
		return fmt.Errorf("watch object has no stable identity")
	}
	now := time.Now().UTC()
	stream.mu.Lock()
	items := stream.objects[spec.name]
	index := slices.IndexFunc(items, func(candidate unstructured.Unstructured) bool {
		return objectKey(candidate) == key
	})
	switch eventType {
	case watch.Added, watch.Modified:
		if index >= 0 {
			items[index] = *object.DeepCopy()
		} else {
			if len(items) >= spec.limit {
				stream.mu.Unlock()
				return fmt.Errorf("source object limit %d exceeded", spec.limit)
			}
			items = append(items, *object.DeepCopy())
		}
	case watch.Deleted:
		if index >= 0 {
			items = append(items[:index], items[index+1:]...)
		}
	default:
		stream.mu.Unlock()
		return fmt.Errorf("unsupported watch event %q", eventType)
	}
	stream.objects[spec.name] = items
	stream.states[spec.name] = inventory.SourceState{
		Name: spec.name, Availability: inventory.AvailabilityAvailable,
		Mode: inventory.SourceModeLive, Freshness: inventory.FreshnessFresh,
		LastSuccess: now,
	}
	stream.mu.Unlock()
	stream.markDirty()
	return nil
}

func (stream *sourceStream) recordSourceError(spec sourceSpec, err error) {
	now := time.Now().UTC()
	stream.mu.Lock()
	previous := stream.states[spec.name]
	stream.states[spec.name] = sourceErrorState(spec, previous, now, err)
	stream.mu.Unlock()
	stream.markDirty()
}

func (stream *sourceStream) markDirty() {
	select {
	case stream.dirty <- struct{}{}:
	default:
	}
}

func (stream *sourceStream) publishLoop(ctx context.Context) {
	var debounce *time.Timer
	var maximum *time.Timer
	var debounceC <-chan time.Time
	var maximumC <-chan time.Time
	stopTimers := func() {
		if debounce != nil {
			debounce.Stop()
		}
		if maximum != nil {
			maximum.Stop()
		}
		debounceC = nil
		maximumC = nil
	}
	flush := func() {
		stopTimers()
		stream.mu.RLock()
		states := cloneStates(stream.states)
		objects := cloneObjectMap(stream.objects)
		stream.mu.RUnlock()
		stream.publish(buildObservation(
			stream.target,
			time.Now().UTC(),
			states,
			objects,
			stream.catalog,
		))
	}
	for {
		select {
		case <-stream.dirty:
			if maximum == nil {
				maximum = time.NewTimer(maximumPublishWait)
				maximumC = maximum.C
			}
			if debounce == nil {
				debounce = time.NewTimer(debounceInterval)
			} else {
				if !debounce.Stop() {
					select {
					case <-debounce.C:
					default:
					}
				}
				debounce.Reset(debounceInterval)
			}
			debounceC = debounce.C
		case <-debounceC:
			flush()
			debounce = nil
			maximum = nil
		case <-maximumC:
			flush()
			debounce = nil
			maximum = nil
		case <-ctx.Done():
			stopTimers()
			return
		}
	}
}

func (stream *sourceStream) publish(observation inventory.Observation) {
	select {
	case stream.updates <- observation:
		return
	default:
	}
	select {
	case <-stream.updates:
	default:
	}
	select {
	case stream.updates <- observation:
	case <-stream.done:
	}
}

func listAll(
	ctx context.Context,
	client dynamic.Interface,
	spec sourceSpec,
) ([]unstructured.Unstructured, string, error) {
	getter := resourceInterface(client, spec)
	items := make([]unstructured.Unstructured, 0)
	continuation := ""
	resourceVersion := ""
	for {
		page, err := getter.List(ctx, metav1.ListOptions{
			Limit: listPageSize, Continue: continuation,
		})
		if err != nil {
			return nil, "", err
		}
		if len(items)+len(page.Items) > spec.limit {
			return nil, "", fmt.Errorf("source object limit %d exceeded", spec.limit)
		}
		items = append(items, page.Items...)
		resourceVersion = page.GetResourceVersion()
		continuation = page.GetContinue()
		if continuation == "" {
			break
		}
	}
	return items, resourceVersion, nil
}

func resourceInterface(
	client dynamic.Interface,
	spec sourceSpec,
) dynamic.ResourceInterface {
	resource := client.Resource(spec.gvr)
	if spec.namespaced {
		return resource.Namespace(metav1.NamespaceAll)
	}
	return resource
}

func sourceErrorState(
	spec sourceSpec,
	previous inventory.SourceState,
	now time.Time,
	err error,
) inventory.SourceState {
	state := inventory.SourceState{
		Name: spec.name, Mode: inventory.SourceModeUnavailable,
		Freshness:  inventory.FreshnessIncomplete,
		Diagnostic: redactedDiagnostic(err), LastSuccess: previous.LastSuccess,
	}
	switch {
	case apierrors.IsForbidden(err):
		state.Availability = inventory.AvailabilityForbidden
	case apierrors.IsNotFound(err) && spec.optional:
		if strings.HasPrefix(spec.gvr.Group, "resource.k8s.io") {
			state.Availability = inventory.AvailabilityUnsupportedSchema
		} else {
			state.Availability = inventory.AvailabilityUnsupported
		}
		state.Mode = inventory.SourceModeUnavailable
	default:
		state.Availability = inventory.AvailabilityFailed
		if !previous.LastSuccess.IsZero() {
			state.Mode = inventory.SourceModePolling
			state.Freshness = inventory.FreshnessReconnecting
			if now.Sub(previous.LastSuccess) >= staleAfter {
				state.Freshness = inventory.FreshnessStale
			}
		}
	}
	return state
}

func buildObservation(
	target inventory.Target,
	observedAt time.Time,
	states map[inventory.SourceName]inventory.SourceState,
	objects map[inventory.SourceName][]unstructured.Unstructured,
	catalogSnapshot catalog.Snapshot,
) inventory.Observation {
	observation := inventory.Observation{Target: target, ObservedAt: observedAt}
	for _, spec := range sourceSpecs {
		if state, found := states[spec.name]; found {
			observation.Sources = append(observation.Sources, state)
		}
	}
	for _, object := range objects[inventory.SourceNodes] {
		var node corev1.Node
		if runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, &node) != nil {
			continue
		}
		observation.Nodes = append(observation.Nodes, nodeRecord(node))
	}
	for _, object := range objects[inventory.SourcePods] {
		var pod corev1.Pod
		if runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, &pod) != nil {
			continue
		}
		observation.Pods = append(observation.Pods, podRecord(pod))
	}
	for _, object := range objects[inventory.SourceScenarios] {
		var instance simulationv1alpha1.ScenarioInstance
		if runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, &instance) != nil {
			continue
		}
		record, err := scenarioRecord(instance, catalogSnapshot)
		if err != nil {
			observation.Diagnostics = append(observation.Diagnostics, inventory.Diagnostic{
				Code: "ScenarioEvidenceIncomplete", Source: inventory.SourceScenarios,
				Message: "one Scenario Instance could not be matched to the bundled profile evidence",
			})
		}
		observation.Scenarios = append(observation.Scenarios, record)
	}
	for _, object := range objects[inventory.SourceResourceClaims] {
		var claim resourcev1.ResourceClaim
		if runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, &claim) != nil {
			continue
		}
		observation.Claims = append(observation.Claims, claimRecord(claim))
	}
	for _, object := range objects[inventory.SourceResourceSlices] {
		var resourceSlice resourcev1.ResourceSlice
		if runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, &resourceSlice) != nil {
			continue
		}
		if len(resourceSlice.Spec.Devices) > cluster.MaximumDevicesPerSlice {
			continue
		}
		for _, device := range resourceSlice.Spec.Devices {
			nodeName := ""
			if resourceSlice.Spec.NodeName != nil {
				nodeName = *resourceSlice.Spec.NodeName
			} else if device.NodeName != nil {
				nodeName = *device.NodeName
			}
			if nodeName == "" {
				continue
			}
			observation.Devices = append(observation.Devices, inventory.DRADeviceRecord{
				NodeName: nodeName, Driver: resourceSlice.Spec.Driver,
				Pool: resourceSlice.Spec.Pool.Name, Device: device.Name,
				Attributes: deviceAttributes(device.Attributes),
			})
		}
	}
	slices.SortFunc(observation.Nodes, func(left, right inventory.NodeRecord) int {
		return strings.Compare(left.Name, right.Name)
	})
	slices.SortFunc(observation.Pods, func(left, right inventory.PodRecord) int {
		if left.Namespace != right.Namespace {
			return strings.Compare(left.Namespace, right.Namespace)
		}
		return strings.Compare(left.Name, right.Name)
	})
	slices.SortFunc(observation.Claims, func(left, right inventory.ClaimRecord) int {
		if left.Namespace != right.Namespace {
			return strings.Compare(left.Namespace, right.Namespace)
		}
		return strings.Compare(left.Name, right.Name)
	})
	return observation
}

func nodeRecord(node corev1.Node) inventory.NodeRecord {
	capacity := make(map[string]int64, len(node.Status.Capacity))
	for name, quantity := range node.Status.Capacity {
		capacity[string(name)] = quantity.Value()
	}
	allocatable := make(map[string]int64, len(node.Status.Allocatable))
	for name, quantity := range node.Status.Allocatable {
		allocatable[string(name)] = quantity.Value()
	}
	var ready *bool
	for _, condition := range node.Status.Conditions {
		if condition.Type != corev1.NodeReady {
			continue
		}
		value := condition.Status == corev1.ConditionTrue
		ready = &value
		break
	}
	return inventory.NodeRecord{
		Name: node.Name, Labels: cloneMap(node.Labels),
		Capacity: capacity, Allocatable: allocatable, Ready: ready,
	}
}

func podRecord(pod corev1.Pod) inventory.PodRecord {
	regular := make(map[string]int64)
	for _, container := range pod.Spec.Containers {
		addRequests(regular, container.Resources.Requests)
	}
	initMaximum := make(map[string]int64)
	for _, container := range pod.Spec.InitContainers {
		for name, quantity := range container.Resources.Requests {
			value := quantity.Value()
			if value > initMaximum[string(name)] {
				initMaximum[string(name)] = value
			}
		}
	}
	for name, value := range initMaximum {
		if value > regular[name] {
			regular[name] = value
		}
	}
	addRequests(regular, pod.Spec.Overhead)
	claims := make([]string, 0, len(pod.Spec.ResourceClaims))
	for _, reference := range pod.Spec.ResourceClaims {
		if reference.ResourceClaimName != nil {
			claims = append(claims, *reference.ResourceClaimName)
		}
	}
	slices.Sort(claims)
	return inventory.PodRecord{
		Namespace: pod.Namespace, Name: pod.Name,
		UID: string(pod.UID), NodeName: pod.Spec.NodeName,
		Requests: regular, Claims: claims,
	}
}

func scenarioRecord(
	instance simulationv1alpha1.ScenarioInstance,
	catalogSnapshot catalog.Snapshot,
) (inventory.ScenarioRecord, error) {
	record := inventory.ScenarioRecord{Name: instance.Name, UID: string(instance.UID)}
	input, err := scenario.Document([]byte(instance.Spec.CanonicalScenario))
	if err != nil {
		return record, err
	}
	compiled, receipt, err := scenario.Compile(input, catalogSnapshot)
	if err != nil {
		return record, err
	}
	acceleratorResolutions := receipt.Resolutions()
	auxiliaryResolutions := receipt.AuxiliaryResolutions()
	acceleratorIndex := 0
	auxiliaryIndex := 0
	for _, group := range compiled.Scenario().NodeGroups() {
		for _, pool := range group.Pools() {
			resolved := acceleratorResolutions[acceleratorIndex]
			acceleratorIndex++
			vendor, model := profileAndModelDisplay(
				catalogSnapshot,
				pool.Profile().ID().String(),
				resolved.ModelID(),
			)
			record.Signals = append(record.Signals, inventory.ScenarioSignalRecord{
				NodeGroup: group.Name().String(), Pool: pool.Name().String(),
				Role:         inventory.SignalRoleAccelerator,
				ResourceName: resolved.ResourceName(), Vendor: vendor, Model: model,
			})
		}
		for _, pool := range group.AuxiliaryPools() {
			resolved := auxiliaryResolutions[auxiliaryIndex]
			auxiliaryIndex++
			associations := make([]string, 0, len(pool.AssociatedAcceleratorPools()))
			for _, association := range pool.AssociatedAcceleratorPools() {
				associations = append(associations, association.String())
			}
			vendor, _ := profileAndModelDisplay(
				catalogSnapshot,
				pool.Profile().ID().String(),
				"",
			)
			record.Signals = append(record.Signals, inventory.ScenarioSignalRecord{
				NodeGroup: group.Name().String(), Pool: pool.Name().String(),
				Role:         inventory.SignalRoleAuxiliary,
				Category:     resolved.AuxiliaryCategory(),
				ResourceName: resolved.ResourceName(), Vendor: vendor,
				AssociatedAcceleratorPools: associations,
			})
		}
	}
	slices.SortFunc(record.Signals, func(
		left,
		right inventory.ScenarioSignalRecord,
	) int {
		return strings.Compare(
			left.NodeGroup+"\x00"+left.Pool+"\x00"+left.ResourceName,
			right.NodeGroup+"\x00"+right.Pool+"\x00"+right.ResourceName,
		)
	})
	return record, nil
}

func profileAndModelDisplay(
	snapshot catalog.Snapshot,
	profileID,
	modelID string,
) (string, string) {
	profile, err := snapshot.Show(profileID)
	if err != nil {
		return "", ""
	}
	modelDisplay := ""
	for _, model := range profile.Models() {
		if model.ID() == modelID {
			modelDisplay = model.DisplayName()
			break
		}
	}
	return profile.DisplayName(), modelDisplay
}

func claimRecord(claim resourcev1.ResourceClaim) inventory.ClaimRecord {
	record := inventory.ClaimRecord{Namespace: claim.Namespace, Name: claim.Name}
	if claim.Status.Allocation != nil {
		for _, result := range claim.Status.Allocation.Devices.Results {
			record.Allocations = append(record.Allocations, inventory.ClaimAllocationRecord{
				Driver: result.Driver, Pool: result.Pool, Device: result.Device,
			})
		}
	}
	for _, reservation := range claim.Status.ReservedFor {
		if reservation.Resource == "pods" && reservation.UID != "" {
			record.ReservedFor = append(record.ReservedFor, string(reservation.UID))
		}
	}
	slices.SortFunc(record.Allocations, func(
		left,
		right inventory.ClaimAllocationRecord,
	) int {
		return strings.Compare(
			left.Driver+"\x00"+left.Pool+"\x00"+left.Device,
			right.Driver+"\x00"+right.Pool+"\x00"+right.Device,
		)
	})
	slices.Sort(record.ReservedFor)
	return record
}

func addRequests(target map[string]int64, values corev1.ResourceList) {
	for name, quantity := range values {
		target[string(name)] += quantity.Value()
	}
}

func deviceAttributes(
	values map[resourcev1.QualifiedName]resourcev1.DeviceAttribute,
) map[string]string {
	result := make(map[string]string, len(values))
	for name, value := range values {
		switch {
		case value.BoolValue != nil:
			result[string(name)] = strconv.FormatBool(*value.BoolValue)
		case value.IntValue != nil:
			result[string(name)] = strconv.FormatInt(*value.IntValue, 10)
		case value.StringValue != nil:
			result[string(name)] = *value.StringValue
		case value.VersionValue != nil:
			result[string(name)] = *value.VersionValue
		}
	}
	return result
}

func loadConfig(
	selection cluster.TargetSelection,
) (*rest.Config, string, string, string, error) {
	if selection.KubeconfigPath == "" || selection.ContextName == "" {
		return nil, "", "", "", fmt.Errorf(
			"explicit kubeconfig path and context name are both required",
		)
	}
	absolute, err := filepath.Abs(selection.KubeconfigPath)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("resolve explicit kubeconfig path: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("resolve explicit kubeconfig symlinks: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.Mode().IsRegular() {
		return nil, "", "", "", fmt.Errorf("explicit kubeconfig must be one regular file")
	}
	raw, err := clientcmd.LoadFromFile(canonical)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("load explicit kubeconfig failed")
	}
	if _, found := raw.Contexts[selection.ContextName]; !found {
		return nil, "", "", "", fmt.Errorf(
			"explicit context %q does not exist in the selected kubeconfig",
			selection.ContextName,
		)
	}
	config, err := clientcmd.NewNonInteractiveClientConfig(
		*raw, selection.ContextName,
		&clientcmd.ConfigOverrides{CurrentContext: selection.ContextName}, nil,
	).ClientConfig()
	if err != nil {
		return nil, "", "", "", fmt.Errorf("load explicit context failed")
	}
	if err := rest.LoadTLSFiles(config); err != nil {
		return nil, "", "", "", fmt.Errorf("load explicit target TLS files failed")
	}
	if config.Insecure || len(config.CAData) == 0 || !strings.HasPrefix(config.Host, "https://") {
		return nil, "", "", "", fmt.Errorf("explicit target requires verified HTTPS and cluster CA data")
	}
	config = rest.CopyConfig(config)
	config.UserAgent = "kube-accelerator-sim/inventory"
	config.QPS = 400
	config.Burst = 800
	ca := sha256.Sum256(config.CAData)
	return config, canonical, strings.TrimSuffix(config.Host, "/"),
		"sha256:" + hex.EncodeToString(ca[:]), nil
}

func supportedMinor(serverVersion *version.Info) (int, error) {
	minorText := strings.TrimRight(serverVersion.Minor, "+")
	minor, err := strconv.Atoi(minorText)
	if err != nil || serverVersion.Major != "1" {
		return 0, fmt.Errorf("unsupported Kubernetes server version %q", serverVersion.GitVersion)
	}
	if minor < 30 || minor > 36 {
		return 0, fmt.Errorf(
			"Kubernetes 1.%d is outside the validated 1.30-1.36 matrix", minor,
		)
	}
	return minor, nil
}

func redactedDiagnostic(err error) string {
	switch {
	case apierrors.IsForbidden(err):
		return "list permission is forbidden for this source"
	case apierrors.IsNotFound(err):
		return "the source API or schema is not served"
	case strings.Contains(err.Error(), "source object limit"):
		return err.Error()
	default:
		return "the source request failed; credentials and server details were redacted"
	}
}

func cloneMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func cloneObjects(values []unstructured.Unstructured) []unstructured.Unstructured {
	result := make([]unstructured.Unstructured, 0, len(values))
	for _, value := range values {
		result = append(result, *value.DeepCopy())
	}
	return result
}

func cloneObjectMap(
	values map[inventory.SourceName][]unstructured.Unstructured,
) map[inventory.SourceName][]unstructured.Unstructured {
	result := make(
		map[inventory.SourceName][]unstructured.Unstructured,
		len(values),
	)
	for name, objects := range values {
		result[name] = cloneObjects(objects)
	}
	return result
}

func cloneStates(
	values map[inventory.SourceName]inventory.SourceState,
) map[inventory.SourceName]inventory.SourceState {
	result := make(map[inventory.SourceName]inventory.SourceState, len(values))
	for name, state := range values {
		result[name] = state
	}
	return result
}

func objectKey(object unstructured.Unstructured) string {
	if object.GetUID() != "" {
		return string(object.GetUID())
	}
	if object.GetName() == "" {
		return ""
	}
	return object.GetNamespace() + "\x00" + object.GetName()
}

func waitContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func fullJitter(ceiling time.Duration) time.Duration {
	if ceiling <= 1 {
		return ceiling
	}
	return time.Duration(rand.Int64N(int64(ceiling)))
}
