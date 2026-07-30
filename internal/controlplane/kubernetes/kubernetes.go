// Package kubernetes implements the Scenario Control Plane port with the
// product's versioned cluster-scoped transport.
package kubernetes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"sigs.k8s.io/controller-runtime/pkg/client"

	simulationv1alpha1 "github.com/LinkMaq/kube-accelerator-sim/api/simulation/v1alpha1"
	"github.com/LinkMaq/kube-accelerator-sim/internal/controlplane"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

const instanceFinalizer = "simulation.kasim.io/owned-resources"

// Adapter translates intention-level commands to the product API.
type Adapter struct {
	client            client.WithWatch
	transportRevision string
}

// New constructs an adapter over one immutable configured Kubernetes client.
func New(kubernetesClient client.WithWatch, transportRevision string) *Adapter {
	return &Adapter{
		client:            kubernetesClient,
		transportRevision: transportRevision,
	}
}

// Probe reports the exact target identity supplied by the outer target loader.
func (adapter *Adapter) Probe(
	_ context.Context,
	target controlplane.ExplicitTarget,
) (controlplane.TargetCapabilities, error) {
	if target.ContextName == "" || target.Fingerprint.String() == "" ||
		adapter.client == nil || adapter.transportRevision == "" {
		return controlplane.TargetCapabilities{}, controlplane.NewError(
			controlplane.ErrorInvalidCommand,
			"Kubernetes Scenario Control Plane target is incomplete",
			"",
		)
	}
	return controlplane.TargetCapabilities{
		TargetFingerprint: target.Fingerprint,
		TransportRevision: adapter.transportRevision,
	}, nil
}

// Read translates one product object without leaking transport types.
func (adapter *Adapter) Read(
	ctx context.Context,
	key controlplane.InstanceKey,
) (controlplane.InstanceRecord, error) {
	instance := &simulationv1alpha1.ScenarioInstance{}
	if err := adapter.client.Get(
		ctx,
		client.ObjectKey{Name: key.Name.String()},
		instance,
	); err != nil {
		return controlplane.InstanceRecord{}, classifyReadError(key, err)
	}
	record, err := toRecord(instance)
	if err != nil {
		return controlplane.InstanceRecord{}, err
	}
	if record.Target.Fingerprint != key.TargetFingerprint {
		return controlplane.InstanceRecord{}, controlplane.NewError(
			controlplane.ErrorTargetMismatch,
			"Scenario Instance target fingerprint does not match the explicit target",
			"",
		)
	}
	return record, nil
}

// Submit creates or atomically appends one logical revision.
func (adapter *Adapter) Submit(
	ctx context.Context,
	command controlplane.RevisionCommand,
) (controlplane.SubmissionReceipt, error) {
	if err := controlplane.ValidateCommand(command); err != nil {
		return controlplane.SubmissionReceipt{}, err
	}
	current := &simulationv1alpha1.ScenarioInstance{}
	err := adapter.client.Get(
		ctx,
		client.ObjectKey{Name: command.Name.String()},
		current,
	)
	if apierrors.IsNotFound(err) {
		return adapter.create(ctx, command)
	}
	if err != nil {
		return controlplane.SubmissionReceipt{}, fmt.Errorf(
			"read Scenario Instance before submit: %w",
			err,
		)
	}
	record, err := toRecord(current)
	if err != nil {
		return controlplane.SubmissionReceipt{}, err
	}
	if record.Target.Fingerprint != command.Target.Fingerprint {
		return controlplane.SubmissionReceipt{}, controlplane.NewError(
			controlplane.ErrorTargetMismatch,
			"Scenario Instance target fingerprint is immutable",
			"",
		)
	}
	if record.CreationIdentity != command.CreationIdentity {
		return controlplane.SubmissionReceipt{}, controlplane.NewError(
			controlplane.ErrorCreationIdentityConflict,
			"Scenario Instance creation identity is immutable",
			"",
		)
	}
	if record.Fidelity != command.Fidelity {
		return controlplane.SubmissionReceipt{}, controlplane.NewError(
			controlplane.ErrorFidelityConflict,
			"Scenario Instance Fidelity Mode is immutable",
			"",
		)
	}
	if record.Revision.Digest == command.Revision.Digest {
		return receipt(record, false, true, command.ServerDryRun), nil
	}
	if record.InstanceUID != command.Preconditions.InstanceUID {
		return controlplane.SubmissionReceipt{}, controlplane.NewError(
			controlplane.ErrorUIDConflict,
			"Scenario Instance UID precondition failed",
			"",
		)
	}
	if record.DesiredGeneration != command.Preconditions.ExpectedGeneration {
		return controlplane.SubmissionReceipt{}, controlplane.NewError(
			controlplane.ErrorGenerationConflict,
			"Scenario Instance generation precondition failed",
			"",
		)
	}
	if record.ResourceVersion != command.Preconditions.ResourceVersion {
		return controlplane.SubmissionReceipt{}, controlplane.NewError(
			controlplane.ErrorResourceVersionConflict,
			"Scenario Instance resourceVersion precondition failed",
			"",
		)
	}
	if command.Revision.Generation.Value() != record.DesiredGeneration.Value()+1 {
		return controlplane.SubmissionReceipt{}, controlplane.NewError(
			controlplane.ErrorGenerationConflict,
			"revision generation does not follow the current desired generation",
			"",
		)
	}

	updated := current.DeepCopy()
	updated.Spec.DesiredGeneration = int64(command.Revision.Generation.Value())
	updated.Spec.CanonicalScenario = string(command.Revision.CanonicalScenario)
	updated.Spec.Revisions = append(
		updated.Spec.Revisions,
		toTransportRevision(command.Revision),
	)
	updateOptions := make([]client.UpdateOption, 0, 1)
	if command.ServerDryRun {
		updateOptions = append(
			updateOptions,
			&client.UpdateOptions{DryRun: []string{metav1.DryRunAll}},
		)
	}
	if err := adapter.client.Update(ctx, updated, updateOptions...); err != nil {
		if apierrors.IsConflict(err) {
			return controlplane.SubmissionReceipt{}, controlplane.NewError(
				controlplane.ErrorResourceVersionConflict,
				"Scenario Instance changed during revision submission",
				"",
			)
		}
		return controlplane.SubmissionReceipt{}, fmt.Errorf(
			"append Scenario revision: %w",
			err,
		)
	}
	accepted, err := toRecord(updated)
	if err != nil {
		return controlplane.SubmissionReceipt{}, err
	}
	return receipt(
		accepted,
		!command.ServerDryRun,
		false,
		command.ServerDryRun,
	), nil
}

// Delete atomically checks logical identity, then asks Kubernetes to set
// deletion desired state with exact UID and resourceVersion preconditions.
func (adapter *Adapter) Delete(
	ctx context.Context,
	command controlplane.DeletionCommand,
) (controlplane.DeletionReceipt, error) {
	if err := controlplane.ValidateDeletionCommand(command); err != nil {
		return controlplane.DeletionReceipt{}, err
	}
	current := &simulationv1alpha1.ScenarioInstance{}
	if err := adapter.client.Get(
		ctx,
		client.ObjectKey{Name: command.Name.String()},
		current,
	); err != nil {
		if apierrors.IsNotFound(err) {
			return controlplane.DeletionReceipt{}, controlplane.NewError(
				controlplane.ErrorNotFound,
				fmt.Sprintf(
					"Scenario Instance %q was not found",
					command.Name.String(),
				),
				"",
			)
		}
		return controlplane.DeletionReceipt{}, fmt.Errorf(
			"read Scenario Instance before delete: %w",
			err,
		)
	}
	record, err := toRecord(current)
	if err != nil {
		return controlplane.DeletionReceipt{}, err
	}
	if record.Target.Fingerprint != command.Target.Fingerprint {
		return controlplane.DeletionReceipt{}, controlplane.NewError(
			controlplane.ErrorTargetMismatch,
			"Scenario Instance target fingerprint is immutable",
			"",
		)
	}
	if record.InstanceUID != command.Preconditions.InstanceUID {
		return controlplane.DeletionReceipt{}, controlplane.NewError(
			controlplane.ErrorUIDConflict,
			"Scenario Instance UID precondition failed",
			"",
		)
	}
	if record.DesiredGeneration != command.Preconditions.ExpectedGeneration {
		return controlplane.DeletionReceipt{}, controlplane.NewError(
			controlplane.ErrorGenerationConflict,
			"Scenario Instance generation precondition failed",
			"",
		)
	}
	if record.DeletionRequested {
		return deletionReceipt(record, false, true), nil
	}

	uid := types.UID(record.InstanceUID.String())
	resourceVersion := record.ResourceVersion
	if err := adapter.client.Delete(
		ctx,
		current,
		&client.DeleteOptions{Raw: &metav1.DeleteOptions{
			Preconditions: &metav1.Preconditions{
				UID:             &uid,
				ResourceVersion: &resourceVersion,
			},
		}},
	); err != nil {
		switch {
		case apierrors.IsConflict(err):
			return controlplane.DeletionReceipt{}, controlplane.NewError(
				controlplane.ErrorResourceVersionConflict,
				"Scenario Instance changed during deletion",
				"",
			)
		case apierrors.IsNotFound(err):
			return controlplane.DeletionReceipt{}, controlplane.NewError(
				controlplane.ErrorNotFound,
				"Scenario Instance disappeared during deletion",
				"",
			)
		default:
			return controlplane.DeletionReceipt{}, fmt.Errorf(
				"delete exact Scenario Instance: %w",
				err,
			)
		}
	}
	deleting := &simulationv1alpha1.ScenarioInstance{}
	if err := adapter.client.Get(
		ctx,
		client.ObjectKey{Name: command.Name.String()},
		deleting,
	); err == nil {
		updated, translateErr := toRecord(deleting)
		if translateErr != nil {
			return controlplane.DeletionReceipt{}, translateErr
		}
		return deletionReceipt(updated, true, false), nil
	} else if !apierrors.IsNotFound(err) {
		return controlplane.DeletionReceipt{}, fmt.Errorf(
			"read accepted Scenario Instance deletion: %w",
			err,
		)
	}
	return deletionReceipt(record, true, false), nil
}

func (adapter *Adapter) create(
	ctx context.Context,
	command controlplane.RevisionCommand,
) (controlplane.SubmissionReceipt, error) {
	if command.Preconditions.ExpectedGeneration.Value() != 0 {
		return controlplane.SubmissionReceipt{}, controlplane.NewError(
			controlplane.ErrorGenerationConflict,
			"create requires expected generation zero",
			"",
		)
	}
	if command.Preconditions.InstanceUID.String() != "" {
		return controlplane.SubmissionReceipt{}, controlplane.NewError(
			controlplane.ErrorUIDConflict,
			"create must not provide an instance UID",
			"",
		)
	}
	if command.Preconditions.ResourceVersion != "" {
		return controlplane.SubmissionReceipt{}, controlplane.NewError(
			controlplane.ErrorResourceVersionConflict,
			"create must not provide a resourceVersion",
			"",
		)
	}
	if command.Revision.Generation.Value() != 1 {
		return controlplane.SubmissionReceipt{}, controlplane.NewError(
			controlplane.ErrorGenerationConflict,
			"first revision generation must be one",
			"",
		)
	}
	instance := &simulationv1alpha1.ScenarioInstance{
		TypeMeta: metav1.TypeMeta{
			APIVersion: simulationv1alpha1.GroupVersion.String(),
			Kind:       "ScenarioInstance",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       command.Name.String(),
			Finalizers: []string{instanceFinalizer},
		},
		Spec: simulationv1alpha1.ScenarioInstanceSpec{
			TargetFingerprint: command.Target.Fingerprint.String(),
			CreationIdentity:  command.CreationIdentity,
			Fidelity:          command.Fidelity.String(),
			DesiredGeneration: 1,
			CanonicalScenario: string(command.Revision.CanonicalScenario),
			Revisions: []simulationv1alpha1.ScenarioRevision{
				toTransportRevision(command.Revision),
			},
		},
	}
	createOptions := make([]client.CreateOption, 0, 1)
	if command.ServerDryRun {
		createOptions = append(
			createOptions,
			&client.CreateOptions{DryRun: []string{metav1.DryRunAll}},
		)
	}
	if err := adapter.client.Create(ctx, instance, createOptions...); err != nil {
		if apierrors.IsAlreadyExists(err) || apierrors.IsConflict(err) {
			return controlplane.SubmissionReceipt{}, controlplane.NewError(
				controlplane.ErrorResourceVersionConflict,
				"Scenario Instance appeared during create",
				"",
			)
		}
		return controlplane.SubmissionReceipt{}, fmt.Errorf(
			"create Scenario Instance: %w",
			err,
		)
	}
	record, err := toRecord(instance)
	if err != nil {
		return controlplane.SubmissionReceipt{}, err
	}
	return receipt(
		record,
		!command.ServerDryRun,
		false,
		command.ServerDryRun,
	), nil
}

// Watch opens one bounded remote event stream after an opaque resourceVersion.
func (adapter *Adapter) Watch(
	ctx context.Context,
	cursor controlplane.WatchCursor,
) (controlplane.InstanceEventStream, error) {
	if err := controlplane.ValidateWatchCursor(cursor); err != nil {
		return nil, err
	}
	options := &client.ListOptions{Raw: &metav1.ListOptions{
		ResourceVersion:     cursor.AfterResourceVersion,
		AllowWatchBookmarks: true,
	}}
	watcher, err := adapter.client.Watch(
		ctx,
		&simulationv1alpha1.ScenarioInstanceList{},
		options,
	)
	if err != nil {
		if apierrors.IsResourceExpired(err) || apierrors.IsGone(err) {
			return nil, adapter.cursorExpired(ctx, cursor.Key)
		}
		return nil, fmt.Errorf("watch Scenario Instance: %w", err)
	}
	return &eventStream{
		watcher: watcher,
		client:  adapter.client,
		key:     cursor.Key,
		limit:   cursor.Limit,
	}, nil
}

func (adapter *Adapter) cursorExpired(
	ctx context.Context,
	key controlplane.InstanceKey,
) error {
	current := &simulationv1alpha1.ScenarioInstance{}
	resume := ""
	if err := adapter.client.Get(
		ctx,
		client.ObjectKey{Name: key.Name.String()},
		current,
	); err == nil {
		resume = current.ResourceVersion
	}
	return controlplane.NewError(
		controlplane.ErrorCursorExpired,
		"watch resourceVersion expired",
		resume,
	)
}

func classifyReadError(key controlplane.InstanceKey, err error) error {
	if apierrors.IsNotFound(err) {
		return controlplane.NewError(
			controlplane.ErrorNotFound,
			fmt.Sprintf("Scenario Instance %q was not found", key.Name.String()),
			"",
		)
	}
	return fmt.Errorf("read Scenario Instance: %w", err)
}

func toTransportRevision(
	revision controlplane.ScenarioRevision,
) simulationv1alpha1.ScenarioRevision {
	profiles := make([]simulationv1alpha1.ProfileReceipt, 0, len(revision.Profiles))
	for _, profile := range revision.Profiles {
		profiles = append(profiles, simulationv1alpha1.ProfileReceipt{
			ID:       profile.ID,
			Revision: profile.Revision,
			Digest:   profile.Digest.String(),
			Class:    profile.Class,
		})
	}
	return simulationv1alpha1.ScenarioRevision{
		Generation: int64(revision.Generation.Value()),
		Digest:     revision.Digest.String(),
		Profiles:   profiles,
	}
}

func toRecord(
	instance *simulationv1alpha1.ScenarioInstance,
) (controlplane.InstanceRecord, error) {
	target, err := domain.ParseDigest(instance.Spec.TargetFingerprint)
	if err != nil {
		return controlplane.InstanceRecord{}, fmt.Errorf("transport target fingerprint: %w", err)
	}
	name, err := domain.ParseName(instance.Name)
	if err != nil {
		return controlplane.InstanceRecord{}, fmt.Errorf("transport instance name: %w", err)
	}
	uid, err := domain.ParseInstanceUID(string(instance.UID))
	if err != nil {
		return controlplane.InstanceRecord{}, fmt.Errorf("transport instance UID: %w", err)
	}
	fidelity, err := domain.ParseFidelityMode(instance.Spec.Fidelity)
	if err != nil {
		return controlplane.InstanceRecord{}, err
	}
	desired, err := domain.NewGeneration(instance.Spec.DesiredGeneration)
	if err != nil {
		return controlplane.InstanceRecord{}, err
	}
	observed, err := domain.NewGeneration(instance.Status.ObservedGeneration)
	if err != nil {
		return controlplane.InstanceRecord{}, err
	}
	if len(instance.Spec.Revisions) == 0 {
		return controlplane.InstanceRecord{}, fmt.Errorf("transport contains no logical revision")
	}
	transportRevisions := append(
		[]simulationv1alpha1.ScenarioRevision(nil),
		instance.Spec.Revisions...,
	)
	slices.SortFunc(
		transportRevisions,
		func(left, right simulationv1alpha1.ScenarioRevision) int {
			switch {
			case left.Generation < right.Generation:
				return -1
			case left.Generation > right.Generation:
				return 1
			default:
				return 0
			}
		},
	)
	latestTransport := transportRevisions[len(transportRevisions)-1]
	if latestTransport.Generation != int64(desired.Value()) {
		return controlplane.InstanceRecord{}, fmt.Errorf(
			"transport has no revision receipt for desired generation %d",
			desired.Value(),
		)
	}
	latestDigest, err := domain.ParseDigest(latestTransport.Digest)
	if err != nil {
		return controlplane.InstanceRecord{}, err
	}
	if digestBytes([]byte(instance.Spec.CanonicalScenario)) != latestDigest.String() {
		return controlplane.InstanceRecord{}, fmt.Errorf(
			"transport canonical Scenario does not match the latest revision digest",
		)
	}
	revisions := make(
		[]controlplane.ScenarioRevision,
		0,
		len(transportRevisions),
	)
	for index, revision := range transportRevisions {
		if revision.Generation != int64(index+1) {
			return controlplane.InstanceRecord{}, fmt.Errorf(
				"transport revision receipts are not contiguous from generation one",
			)
		}
		canonical := []byte(nil)
		if revision.Generation == int64(desired.Value()) {
			canonical = []byte(instance.Spec.CanonicalScenario)
		}
		translated, err := fromTransportRevision(revision, canonical)
		if err != nil {
			return controlplane.InstanceRecord{}, err
		}
		revisions = append(revisions, translated)
	}
	status, err := fromTransportStatus(instance.Status)
	if err != nil {
		return controlplane.InstanceRecord{}, err
	}
	return controlplane.InstanceRecord{
		Target: controlplane.ExplicitTarget{
			Fingerprint: target,
		},
		Name:               name,
		InstanceUID:        uid,
		ResourceVersion:    instance.ResourceVersion,
		DeletionRequested:  instance.DeletionTimestamp != nil,
		CreationIdentity:   instance.Spec.CreationIdentity,
		Fidelity:           fidelity,
		DesiredGeneration:  desired,
		ObservedGeneration: observed,
		Revision:           controlplane.CloneRevision(revisions[len(revisions)-1]),
		Revisions:          revisions,
		Status:             status,
	}, nil
}

func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func fromTransportStatus(
	status simulationv1alpha1.ScenarioInstanceStatus,
) (controlplane.InstanceStatus, error) {
	var revisionDigest domain.Digest
	var err error
	if status.RevisionDigest != "" {
		revisionDigest, err = domain.ParseDigest(status.RevisionDigest)
		if err != nil {
			return controlplane.InstanceStatus{}, fmt.Errorf(
				"transport status revision digest: %w",
				err,
			)
		}
	}
	pools, poolsTruncated := bounded(status.Pools, controlplane.MaximumStatusPools)
	inventory, inventoryTruncated := bounded(
		status.Inventory,
		controlplane.MaximumStatusInventory,
	)
	diagnostics, diagnosticsTruncated := bounded(
		status.Diagnostics,
		controlplane.MaximumStatusDiagnostics,
	)
	conditions, conditionsTruncated := bounded(
		status.Conditions,
		controlplane.MaximumStatusConditions,
	)
	translatedPools := make([]controlplane.PoolStatus, 0, len(pools))
	for _, pool := range pools {
		translatedPools = append(translatedPools, controlplane.PoolStatus{
			Group:            pool.Group,
			Pool:             pool.Pool,
			RequestedTotal:   pool.RequestedTotal,
			RequestedHealthy: pool.RequestedHealthy,
			ObservedTotal:    pool.ObservedTotal,
			ObservedHealthy:  pool.ObservedHealthy,
		})
	}
	translatedInventory := make(
		[]controlplane.InventoryEntry,
		0,
		len(inventory),
	)
	for _, entry := range inventory {
		translatedInventory = append(
			translatedInventory,
			controlplane.InventoryEntry{
				APIVersion: entry.APIVersion,
				Kind:       entry.Kind,
				Count:      entry.Count,
			},
		)
	}
	translatedDiagnostics := make(
		[]controlplane.DiagnosticStatus,
		0,
		len(diagnostics),
	)
	for _, diagnostic := range diagnostics {
		translatedDiagnostics = append(
			translatedDiagnostics,
			controlplane.DiagnosticStatus{
				Code:             diagnostic.Code,
				Message:          diagnostic.Message,
				Retryable:        diagnostic.Retryable,
				RevisionAccepted: diagnostic.RevisionAccepted,
				ExitCategory:     diagnostic.ExitCategory,
			},
		)
	}
	translatedConditions := make(
		[]controlplane.ConditionStatus,
		0,
		len(conditions),
	)
	for _, condition := range conditions {
		translatedConditions = append(
			translatedConditions,
			controlplane.ConditionStatus{
				Type:               condition.Type,
				Status:             string(condition.Status),
				Reason:             condition.Reason,
				Message:            condition.Message,
				ObservedGeneration: condition.ObservedGeneration,
				LastTransitionTime: condition.LastTransitionTime.Time.UTC(),
			},
		)
	}
	return controlplane.InstanceStatus{
		RevisionDigest:       revisionDigest,
		Phase:                status.Phase,
		Pools:                translatedPools,
		PoolsTruncated:       poolsTruncated,
		Inventory:            translatedInventory,
		InventoryTruncated:   inventoryTruncated,
		Diagnostics:          translatedDiagnostics,
		DiagnosticsTruncated: diagnosticsTruncated,
		Conditions:           translatedConditions,
		ConditionsTruncated:  conditionsTruncated,
	}, nil
}

func bounded[T any](values []T, maximum int) ([]T, bool) {
	if len(values) <= maximum {
		return values, false
	}
	return values[:maximum], true
}

func fromTransportRevision(
	revision simulationv1alpha1.ScenarioRevision,
	canonicalScenario []byte,
) (controlplane.ScenarioRevision, error) {
	generation, err := domain.NewGeneration(revision.Generation)
	if err != nil {
		return controlplane.ScenarioRevision{}, err
	}
	digest, err := domain.ParseDigest(revision.Digest)
	if err != nil {
		return controlplane.ScenarioRevision{}, err
	}
	profiles := make([]controlplane.ProfileReceipt, 0, len(revision.Profiles))
	for _, profile := range revision.Profiles {
		profileDigest, err := domain.ParseDigest(profile.Digest)
		if err != nil {
			return controlplane.ScenarioRevision{}, err
		}
		profiles = append(profiles, controlplane.ProfileReceipt{
			ID:       profile.ID,
			Revision: profile.Revision,
			Digest:   profileDigest,
			Class:    profile.Class,
		})
	}
	return controlplane.ScenarioRevision{
		Generation:        generation,
		Digest:            digest,
		CanonicalScenario: append([]byte(nil), canonicalScenario...),
		Profiles:          profiles,
	}, nil
}

func receipt(
	record controlplane.InstanceRecord,
	accepted, noOp, dryRun bool,
) controlplane.SubmissionReceipt {
	return controlplane.SubmissionReceipt{
		InstanceUID:       record.InstanceUID,
		DesiredGeneration: record.DesiredGeneration,
		ResourceVersion:   record.ResourceVersion,
		RevisionDigest:    record.Revision.Digest,
		Accepted:          accepted,
		NoOp:              noOp,
		DryRun:            dryRun,
	}
}

func deletionReceipt(
	record controlplane.InstanceRecord,
	accepted,
	noOp bool,
) controlplane.DeletionReceipt {
	return controlplane.DeletionReceipt{
		InstanceUID:       record.InstanceUID,
		DesiredGeneration: record.DesiredGeneration,
		ResourceVersion:   record.ResourceVersion,
		Accepted:          accepted,
		NoOp:              noOp,
	}
}

type eventStream struct {
	watcher watch.Interface
	client  client.WithWatch
	key     controlplane.InstanceKey
	limit   int
	count   int
	closed  bool
	cursor  string
}

func (stream *eventStream) Next(ctx context.Context) (controlplane.InstanceEvent, error) {
	if stream.closed || stream.count >= stream.limit {
		return controlplane.InstanceEvent{}, io.EOF
	}
	for {
		select {
		case <-ctx.Done():
			return controlplane.InstanceEvent{}, ctx.Err()
		case event, open := <-stream.watcher.ResultChan():
			if !open {
				stream.closed = true
				return controlplane.InstanceEvent{}, io.EOF
			}
			if event.Type == watch.Bookmark {
				if accessor, err := meta.Accessor(event.Object); err == nil {
					stream.cursor = accessor.GetResourceVersion()
				}
				continue
			}
			if event.Type == watch.Error {
				if status, ok := event.Object.(*metav1.Status); ok &&
					(status.Reason == metav1.StatusReasonExpired ||
						status.Reason == metav1.StatusReasonGone) {
					return controlplane.InstanceEvent{}, stream.expired(ctx)
				}
				return controlplane.InstanceEvent{}, errors.New("Scenario Instance watch failed")
			}
			instance, ok := event.Object.(*simulationv1alpha1.ScenarioInstance)
			if !ok || instance.Name != stream.key.Name.String() {
				continue
			}
			record, err := toRecord(instance)
			if err != nil {
				return controlplane.InstanceEvent{}, err
			}
			if record.Target.Fingerprint != stream.key.TargetFingerprint {
				continue
			}
			stream.cursor = instance.ResourceVersion
			stream.count++
			return controlplane.InstanceEvent{
				Cursor: stream.cursor,
				Record: record,
			}, nil
		}
	}
}

func (stream *eventStream) expired(ctx context.Context) error {
	current := &simulationv1alpha1.ScenarioInstance{}
	resume := stream.cursor
	if err := stream.client.Get(
		ctx,
		client.ObjectKey{Name: stream.key.Name.String()},
		current,
	); err == nil {
		resume = current.ResourceVersion
	}
	return controlplane.NewError(
		controlplane.ErrorCursorExpired,
		"watch resourceVersion expired",
		resume,
	)
}

func (stream *eventStream) Close() error {
	if stream.closed {
		return nil
	}
	stream.closed = true
	stream.watcher.Stop()
	return nil
}
