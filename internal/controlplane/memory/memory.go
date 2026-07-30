// Package memory implements the Scenario Control Plane contract for
// deterministic application and contract tests.
package memory

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"sync"

	"github.com/LinkMaq/kube-accelerator-sim/internal/controlplane"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

// Options bounds retained watch history.
type Options struct {
	HistoryLimit int
}

type storedEvent struct {
	resourceVersion uint64
	key             string
	record          controlplane.InstanceRecord
}

// Adapter is a concurrency-safe deterministic implementation.
type Adapter struct {
	mutex        sync.RWMutex
	historyLimit int
	nextVersion  uint64
	nextUID      uint64
	records      map[string]controlplane.InstanceRecord
	events       []storedEvent
}

// New constructs one isolated adapter.
func New(options Options) *Adapter {
	historyLimit := options.HistoryLimit
	if historyLimit <= 0 {
		historyLimit = 128
	}
	return &Adapter{
		historyLimit: historyLimit,
		records:      make(map[string]controlplane.InstanceRecord),
	}
}

// Probe returns deterministic transport identity without external I/O.
func (adapter *Adapter) Probe(
	_ context.Context,
	target controlplane.ExplicitTarget,
) (controlplane.TargetCapabilities, error) {
	if target.ContextName == "" || target.Fingerprint.String() == "" {
		return controlplane.TargetCapabilities{}, controlplane.NewError(
			controlplane.ErrorInvalidCommand,
			"explicit target is incomplete",
			"",
		)
	}
	return controlplane.TargetCapabilities{
		TargetFingerprint: target.Fingerprint,
		TransportRevision: "memory-v1alpha1",
	}, nil
}

// Read returns one immutable record copy.
func (adapter *Adapter) Read(
	_ context.Context,
	key controlplane.InstanceKey,
) (controlplane.InstanceRecord, error) {
	adapter.mutex.RLock()
	defer adapter.mutex.RUnlock()
	record, found := adapter.records[recordKey(key)]
	if !found {
		return controlplane.InstanceRecord{}, controlplane.NewError(
			controlplane.ErrorNotFound,
			fmt.Sprintf("Scenario Instance %q was not found", key.Name.String()),
			"",
		)
	}
	if record.Target.Fingerprint != key.TargetFingerprint {
		return controlplane.InstanceRecord{}, controlplane.NewError(
			controlplane.ErrorTargetMismatch,
			"Scenario Instance target fingerprint does not match the explicit target",
			"",
		)
	}
	return controlplane.CloneRecord(record), nil
}

// RequestDeletion marks one stored Scenario Instance as deleting. It is
// concrete test/control-plane fixture plumbing rather than part of the public
// Scenario Control Plane seam, whose deletion signal is read from the durable
// record.
func (adapter *Adapter) RequestDeletion(
	_ context.Context,
	key controlplane.InstanceKey,
) error {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()

	storageKey := recordKey(key)
	record, found := adapter.records[storageKey]
	if !found {
		return controlplane.NewError(
			controlplane.ErrorNotFound,
			fmt.Sprintf("Scenario Instance %q was not found", key.Name.String()),
			"",
		)
	}
	if record.Target.Fingerprint != key.TargetFingerprint {
		return controlplane.NewError(
			controlplane.ErrorTargetMismatch,
			"Scenario Instance target fingerprint does not match the explicit target",
			"",
		)
	}
	if record.DeletionRequested {
		return nil
	}
	adapter.nextVersion++
	record.ResourceVersion = strconv.FormatUint(adapter.nextVersion, 10)
	record.DeletionRequested = true
	adapter.records[storageKey] = record
	adapter.recordEvent(storageKey, record)
	return nil
}

// Delete atomically accepts exact deletion desired state.
func (adapter *Adapter) Delete(
	_ context.Context,
	command controlplane.DeletionCommand,
) (controlplane.DeletionReceipt, error) {
	if err := controlplane.ValidateDeletionCommand(command); err != nil {
		return controlplane.DeletionReceipt{}, err
	}
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()

	key := recordKey(controlplane.InstanceKey{
		TargetFingerprint: command.Target.Fingerprint,
		Name:              command.Name,
	})
	record, found := adapter.records[key]
	if !found {
		return controlplane.DeletionReceipt{}, controlplane.NewError(
			controlplane.ErrorNotFound,
			fmt.Sprintf("Scenario Instance %q was not found", command.Name.String()),
			"",
		)
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
	adapter.nextVersion++
	record.ResourceVersion = strconv.FormatUint(adapter.nextVersion, 10)
	record.DeletionRequested = true
	adapter.records[key] = record
	adapter.recordEvent(key, record)
	return deletionReceipt(record, true, false), nil
}

// CommitStatus advances the deterministic controller-facing status of one
// record and emits a resumable event. It is concrete in-memory adapter
// plumbing, not another Scenario Control Plane operation.
func (adapter *Adapter) CommitStatus(
	_ context.Context,
	key controlplane.InstanceKey,
	observedGeneration domain.Generation,
	status controlplane.InstanceStatus,
) error {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()

	storageKey := recordKey(key)
	record, found := adapter.records[storageKey]
	if !found {
		return controlplane.NewError(
			controlplane.ErrorNotFound,
			fmt.Sprintf("Scenario Instance %q was not found", key.Name.String()),
			"",
		)
	}
	if record.Target.Fingerprint != key.TargetFingerprint {
		return controlplane.NewError(
			controlplane.ErrorTargetMismatch,
			"Scenario Instance target fingerprint does not match the explicit target",
			"",
		)
	}
	if observedGeneration.Value() > record.DesiredGeneration.Value() {
		return controlplane.NewError(
			controlplane.ErrorGenerationConflict,
			"observed generation exceeds desired generation",
			"",
		)
	}
	adapter.nextVersion++
	record.ResourceVersion = strconv.FormatUint(adapter.nextVersion, 10)
	record.ObservedGeneration = observedGeneration
	record.Status = cloneStatus(status)
	adapter.records[storageKey] = record
	adapter.recordEvent(storageKey, record)
	return nil
}

// CompleteDeletion removes a deletion-requested record to model API-server
// finalizer completion in deterministic application tests.
func (adapter *Adapter) CompleteDeletion(
	_ context.Context,
	key controlplane.InstanceKey,
) error {
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()

	storageKey := recordKey(key)
	record, found := adapter.records[storageKey]
	if !found {
		return nil
	}
	if record.Target.Fingerprint != key.TargetFingerprint {
		return controlplane.NewError(
			controlplane.ErrorTargetMismatch,
			"Scenario Instance target fingerprint does not match the explicit target",
			"",
		)
	}
	if !record.DeletionRequested {
		return controlplane.NewError(
			controlplane.ErrorInvalidCommand,
			"Scenario Instance deletion has not been requested",
			"",
		)
	}
	delete(adapter.records, storageKey)
	return nil
}

// Submit atomically enforces immutable identity and optimistic preconditions.
func (adapter *Adapter) Submit(
	_ context.Context,
	command controlplane.RevisionCommand,
) (controlplane.SubmissionReceipt, error) {
	if err := controlplane.ValidateCommand(command); err != nil {
		return controlplane.SubmissionReceipt{}, err
	}
	adapter.mutex.Lock()
	defer adapter.mutex.Unlock()

	key := recordKey(controlplane.InstanceKey{
		TargetFingerprint: command.Target.Fingerprint,
		Name:              command.Name,
	})
	current, found := adapter.records[key]
	if !found {
		return adapter.create(key, command)
	}
	if current.Target.Fingerprint != command.Target.Fingerprint {
		return controlplane.SubmissionReceipt{}, controlplane.NewError(
			controlplane.ErrorTargetMismatch,
			"Scenario Instance target fingerprint is immutable",
			"",
		)
	}
	if current.CreationIdentity != command.CreationIdentity {
		return controlplane.SubmissionReceipt{}, controlplane.NewError(
			controlplane.ErrorCreationIdentityConflict,
			"Scenario Instance creation identity is immutable",
			"",
		)
	}
	if current.Fidelity != command.Fidelity {
		return controlplane.SubmissionReceipt{}, controlplane.NewError(
			controlplane.ErrorFidelityConflict,
			"Scenario Instance Fidelity Mode is immutable",
			"",
		)
	}
	if current.Revision.Digest == command.Revision.Digest {
		return receipt(current, false, true, command.ServerDryRun), nil
	}
	if current.InstanceUID != command.Preconditions.InstanceUID {
		return controlplane.SubmissionReceipt{}, controlplane.NewError(
			controlplane.ErrorUIDConflict,
			"Scenario Instance UID precondition failed",
			"",
		)
	}
	if current.DesiredGeneration != command.Preconditions.ExpectedGeneration {
		return controlplane.SubmissionReceipt{}, controlplane.NewError(
			controlplane.ErrorGenerationConflict,
			"Scenario Instance generation precondition failed",
			"",
		)
	}
	if current.ResourceVersion != command.Preconditions.ResourceVersion {
		return controlplane.SubmissionReceipt{}, controlplane.NewError(
			controlplane.ErrorResourceVersionConflict,
			"Scenario Instance resourceVersion precondition failed",
			"",
		)
	}
	nextGeneration := current.DesiredGeneration.Value() + 1
	if command.Revision.Generation.Value() != nextGeneration {
		return controlplane.SubmissionReceipt{}, controlplane.NewError(
			controlplane.ErrorGenerationConflict,
			"revision generation does not follow the current desired generation",
			"",
		)
	}
	if command.ServerDryRun {
		proposed := controlplane.CloneRecord(current)
		proposed.DesiredGeneration = command.Revision.Generation
		proposed.Revision = controlplane.CloneRevision(command.Revision)
		proposed.Revisions = append(
			proposed.Revisions,
			controlplane.CloneRevision(command.Revision),
		)
		return receipt(proposed, false, false, true), nil
	}
	adapter.nextVersion++
	current.ResourceVersion = strconv.FormatUint(adapter.nextVersion, 10)
	current.DesiredGeneration = command.Revision.Generation
	current.Revision = controlplane.CloneRevision(command.Revision)
	current.Revisions = append(
		current.Revisions,
		controlplane.CloneRevision(command.Revision),
	)
	adapter.records[key] = current
	adapter.recordEvent(key, current)
	return receipt(current, true, false, false), nil
}

func (adapter *Adapter) create(
	key string,
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
	if command.ServerDryRun {
		observed, err := domain.NewGeneration(0)
		if err != nil {
			return controlplane.SubmissionReceipt{}, err
		}
		revision := controlplane.CloneRevision(command.Revision)
		return receipt(controlplane.InstanceRecord{
			Target:             command.Target,
			Name:               command.Name,
			CreationIdentity:   command.CreationIdentity,
			Fidelity:           command.Fidelity,
			DesiredGeneration:  command.Revision.Generation,
			ObservedGeneration: observed,
			Revision:           revision,
			Revisions:          []controlplane.ScenarioRevision{revision},
		}, false, false, true), nil
	}
	adapter.nextVersion++
	adapter.nextUID++
	uid, err := domain.ParseInstanceUID(fmt.Sprintf("memory-%d", adapter.nextUID))
	if err != nil {
		return controlplane.SubmissionReceipt{}, err
	}
	observed, err := domain.NewGeneration(0)
	if err != nil {
		return controlplane.SubmissionReceipt{}, err
	}
	revision := controlplane.CloneRevision(command.Revision)
	record := controlplane.InstanceRecord{
		Target:             command.Target,
		Name:               command.Name,
		InstanceUID:        uid,
		ResourceVersion:    strconv.FormatUint(adapter.nextVersion, 10),
		CreationIdentity:   command.CreationIdentity,
		Fidelity:           command.Fidelity,
		DesiredGeneration:  command.Revision.Generation,
		ObservedGeneration: observed,
		Revision:           revision,
		Revisions:          []controlplane.ScenarioRevision{revision},
	}
	adapter.records[key] = record
	adapter.recordEvent(key, record)
	return receipt(record, true, false, false), nil
}

func (adapter *Adapter) recordEvent(key string, record controlplane.InstanceRecord) {
	resourceVersion, _ := strconv.ParseUint(record.ResourceVersion, 10, 64)
	adapter.events = append(adapter.events, storedEvent{
		resourceVersion: resourceVersion,
		key:             key,
		record:          controlplane.CloneRecord(record),
	})
	if len(adapter.events) > adapter.historyLimit {
		adapter.events = append([]storedEvent(nil), adapter.events[len(adapter.events)-adapter.historyLimit:]...)
	}
}

// Watch returns a finite snapshot stream; callers resume from its last cursor.
func (adapter *Adapter) Watch(
	_ context.Context,
	cursor controlplane.WatchCursor,
) (controlplane.InstanceEventStream, error) {
	if err := controlplane.ValidateWatchCursor(cursor); err != nil {
		return nil, err
	}
	after, err := parseCursor(cursor.AfterResourceVersion)
	if err != nil {
		return nil, err
	}
	adapter.mutex.RLock()
	defer adapter.mutex.RUnlock()
	if cursor.AfterResourceVersion != "" && len(adapter.events) != 0 {
		earliest := adapter.events[0].resourceVersion
		if earliest > 0 && after < earliest-1 {
			return nil, controlplane.NewError(
				controlplane.ErrorCursorExpired,
				"watch cursor expired from bounded history",
				strconv.FormatUint(earliest-1, 10),
			)
		}
	}
	key := recordKey(cursor.Key)
	events := make([]controlplane.InstanceEvent, 0, cursor.Limit)
	for _, event := range adapter.events {
		if event.key != key ||
			event.record.Target.Fingerprint != cursor.Key.TargetFingerprint ||
			event.resourceVersion <= after {
			continue
		}
		events = append(events, controlplane.InstanceEvent{
			Cursor: strconv.FormatUint(event.resourceVersion, 10),
			Record: controlplane.CloneRecord(event.record),
		})
		if len(events) == cursor.Limit {
			break
		}
	}
	return &stream{events: events}, nil
}

func recordKey(key controlplane.InstanceKey) string {
	return key.Name.String()
}

func cloneStatus(status controlplane.InstanceStatus) controlplane.InstanceStatus {
	result := status
	result.Pools = append([]controlplane.PoolStatus(nil), status.Pools...)
	result.Inventory = append(
		[]controlplane.InventoryEntry(nil),
		status.Inventory...,
	)
	result.Diagnostics = append(
		[]controlplane.DiagnosticStatus(nil),
		status.Diagnostics...,
	)
	result.Conditions = append(
		[]controlplane.ConditionStatus(nil),
		status.Conditions...,
	)
	return result
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

func parseCursor(value string) (uint64, error) {
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, controlplane.NewError(
			controlplane.ErrorInvalidCommand,
			"memory watch cursor is invalid",
			"",
		)
	}
	return parsed, nil
}

type stream struct {
	events []controlplane.InstanceEvent
	index  int
	closed bool
}

func (stream *stream) Next(ctx context.Context) (controlplane.InstanceEvent, error) {
	if stream.closed || stream.index >= len(stream.events) {
		return controlplane.InstanceEvent{}, io.EOF
	}
	select {
	case <-ctx.Done():
		return controlplane.InstanceEvent{}, ctx.Err()
	default:
	}
	event := stream.events[stream.index]
	stream.index++
	return event, nil
}

func (stream *stream) Close() error {
	stream.closed = true
	return nil
}
