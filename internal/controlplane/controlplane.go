// Package controlplane defines the private intention-level Scenario Control
// Plane port shared by the local runtime and its in-memory/Kubernetes adapters.
package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

const (
	MaximumStatusPools       = 1024
	MaximumStatusInventory   = 64
	MaximumStatusFidelity    = 32
	MaximumStatusDiagnostics = 32
	MaximumStatusConditions  = 6
)

// ExplicitTarget is already-loaded immutable target identity. Kubeconfig
// loading and capability discovery remain behind outer application modules.
type ExplicitTarget struct {
	ContextName string
	Fingerprint domain.Digest
}

// TargetCapabilities records the target identity and installed transport
// revision observed by Probe.
type TargetCapabilities struct {
	TargetFingerprint domain.Digest
	TransportRevision string
}

// InstanceKey scopes one cluster-scoped name to an exact target.
type InstanceKey struct {
	TargetFingerprint domain.Digest
	Name              domain.Name
}

// ProfileReceipt pins one profile identity inside an immutable revision.
type ProfileReceipt struct {
	ID       string
	Revision string
	Digest   domain.Digest
	Class    string
}

// ScenarioRevision is target-independent canonical desired state plus its
// exact profile pins. It contains no Kubernetes objects or patches.
type ScenarioRevision struct {
	Generation        domain.Generation
	Digest            domain.Digest
	CanonicalScenario []byte
	Profiles          []ProfileReceipt
}

// Preconditions are atomically enforced by Submit.
type Preconditions struct {
	InstanceUID        domain.InstanceUID
	ExpectedGeneration domain.Generation
	ResourceVersion    string
}

// RevisionCommand is the only mutation accepted by this port.
type RevisionCommand struct {
	Target           ExplicitTarget
	Name             domain.Name
	CreationIdentity string
	Fidelity         domain.FidelityMode
	Preconditions    Preconditions
	Revision         ScenarioRevision
	ServerDryRun     bool
}

// RevisionIntent is target-independent compiled desired state. Application
// preflight binds it to the fingerprint learned from one explicit connection.
type RevisionIntent struct {
	Name             domain.Name
	CreationIdentity string
	Fidelity         domain.FidelityMode
	Preconditions    Preconditions
	Revision         ScenarioRevision
}

// DeletionPreconditions are atomically checked before Kubernetes deletion is
// accepted as desired state.
type DeletionPreconditions struct {
	InstanceUID        domain.InstanceUID
	ExpectedGeneration domain.Generation
}

// DeletionCommand identifies exactly one instance on exactly one target.
type DeletionCommand struct {
	Target        ExplicitTarget
	Name          domain.Name
	Preconditions DeletionPreconditions
}

// Bind returns the complete intention-level transport command for one
// immutable explicit target.
func (intent RevisionIntent) Bind(target ExplicitTarget) RevisionCommand {
	return RevisionCommand{
		Target:           target,
		Name:             intent.Name,
		CreationIdentity: intent.CreationIdentity,
		Fidelity:         intent.Fidelity,
		Preconditions:    intent.Preconditions,
		Revision:         CloneRevision(intent.Revision),
	}
}

// InstanceRecord is the version-neutral durable logical representation.
type InstanceRecord struct {
	Target             ExplicitTarget
	Name               domain.Name
	InstanceUID        domain.InstanceUID
	ResourceVersion    string
	DeletionRequested  bool
	CreationIdentity   string
	Fidelity           domain.FidelityMode
	DesiredGeneration  domain.Generation
	ObservedGeneration domain.Generation
	Revision           ScenarioRevision
	Revisions          []ScenarioRevision
	Status             InstanceStatus
}

// PoolStatus is one requested/observed pool aggregate.
type PoolStatus struct {
	Group            string
	Pool             string
	RequestedTotal   int64
	RequestedHealthy int64
	ObservedTotal    int64
	ObservedHealthy  int64
}

// InventoryEntry counts one exact allowlisted owned object kind.
type InventoryEntry struct {
	APIVersion string
	Kind       string
	Count      int32
}

// FidelitySurfaceStatus records one bounded, reader-facing truth claim.
type FidelitySurfaceStatus struct {
	Surface string
	State   string
}

// DiagnosticStatus preserves stable automation signals after acceptance.
type DiagnosticStatus struct {
	Code             string
	Message          string
	Retryable        bool
	RevisionAccepted bool
	ExitCategory     int32
}

// ConditionStatus is the transport-independent condition view.
type ConditionStatus struct {
	Type               string
	Status             string
	Reason             string
	Message            string
	ObservedGeneration int64
	LastTransitionTime time.Time
}

// InstanceStatus is a bounded defensive view of durable status.
type InstanceStatus struct {
	RevisionDigest       domain.Digest
	Phase                string
	Pools                []PoolStatus
	PoolsTruncated       bool
	Inventory            []InventoryEntry
	InventoryTruncated   bool
	Fidelity             []FidelitySurfaceStatus
	FidelityTruncated    bool
	Diagnostics          []DiagnosticStatus
	DiagnosticsTruncated bool
	Conditions           []ConditionStatus
	ConditionsTruncated  bool
}

// SubmissionReceipt distinguishes newly accepted work from an idempotent
// no-op while preserving concurrency identity.
type SubmissionReceipt struct {
	InstanceUID       domain.InstanceUID
	DesiredGeneration domain.Generation
	ResourceVersion   string
	RevisionDigest    domain.Digest
	Accepted          bool
	NoOp              bool
	DryRun            bool
}

// DeletionReceipt distinguishes newly accepted deletion from an idempotent
// retry while retaining exact concurrency identity.
type DeletionReceipt struct {
	InstanceUID       domain.InstanceUID
	DesiredGeneration domain.Generation
	ResourceVersion   string
	Accepted          bool
	NoOp              bool
}

// WatchCursor resumes events strictly after one opaque resource version.
type WatchCursor struct {
	Key                  InstanceKey
	AfterResourceVersion string
	Limit                int
}

// InstanceEvent is one bounded watch result and its resume cursor.
type InstanceEvent struct {
	Cursor string
	Record InstanceRecord
}

// InstanceEventStream is a finite or remotely closed event sequence. Callers
// reopen it with the last successful cursor.
type InstanceEventStream interface {
	Next(context.Context) (InstanceEvent, error)
	Close() error
}

// ScenarioControlPlane is the private remote-owned transport seam.
type ScenarioControlPlane interface {
	Probe(context.Context, ExplicitTarget) (TargetCapabilities, error)
	Read(context.Context, InstanceKey) (InstanceRecord, error)
	Submit(context.Context, RevisionCommand) (SubmissionReceipt, error)
	Delete(context.Context, DeletionCommand) (DeletionReceipt, error)
	Watch(context.Context, WatchCursor) (InstanceEventStream, error)
}

// ErrorCode is a stable transport conflict or resumability classification.
type ErrorCode string

const (
	ErrorNotFound                 ErrorCode = "NotFound"
	ErrorUIDConflict              ErrorCode = "UIDConflict"
	ErrorGenerationConflict       ErrorCode = "GenerationConflict"
	ErrorResourceVersionConflict  ErrorCode = "ResourceVersionConflict"
	ErrorTargetMismatch           ErrorCode = "TargetMismatch"
	ErrorFidelityConflict         ErrorCode = "FidelityConflict"
	ErrorCreationIdentityConflict ErrorCode = "CreationIdentityConflict"
	ErrorCursorExpired            ErrorCode = "CursorExpired"
	ErrorInvalidCommand           ErrorCode = "InvalidCommand"
)

// Error preserves one stable code and optional safe watch resume cursor.
type Error struct {
	code         ErrorCode
	message      string
	resumeCursor string
}

// NewError constructs one adapter-neutral transport error.
func NewError(code ErrorCode, message, resumeCursor string) error {
	return &Error{code: code, message: message, resumeCursor: resumeCursor}
}

func (transportError *Error) Error() string {
	return transportError.message
}

// ErrorCodeOf returns the stable code or the empty value for unrelated errors.
func ErrorCodeOf(err error) ErrorCode {
	transportError, ok := err.(*Error)
	if !ok {
		return ""
	}
	return transportError.code
}

// ResumeCursorOf returns the safe cursor attached to an expiry error.
func ResumeCursorOf(err error) string {
	transportError, ok := err.(*Error)
	if !ok {
		return ""
	}
	return transportError.resumeCursor
}

// CloneRevision returns a deep copy across adapter boundaries.
func CloneRevision(revision ScenarioRevision) ScenarioRevision {
	return ScenarioRevision{
		Generation:        revision.Generation,
		Digest:            revision.Digest,
		CanonicalScenario: append([]byte(nil), revision.CanonicalScenario...),
		Profiles:          append([]ProfileReceipt(nil), revision.Profiles...),
	}
}

// ValidateCommand rejects incomplete intention-level mutations before an
// adapter considers persistence.
func ValidateCommand(command RevisionCommand) error {
	if command.Target.ContextName == "" ||
		command.Target.Fingerprint.String() == "" {
		return NewError(ErrorInvalidCommand, "revision command is incomplete", "")
	}
	return ValidateRevisionIntent(RevisionIntent{
		Name:             command.Name,
		CreationIdentity: command.CreationIdentity,
		Fidelity:         command.Fidelity,
		Preconditions:    command.Preconditions,
		Revision:         command.Revision,
	})
}

// ValidateDeletionCommand rejects ambiguous, wildcard, or incomplete
// deletion intention before an adapter contacts Kubernetes.
func ValidateDeletionCommand(command DeletionCommand) error {
	if command.Target.ContextName == "" ||
		command.Target.Fingerprint.String() == "" ||
		command.Name.String() == "" {
		return NewError(ErrorInvalidCommand, "deletion command is incomplete", "")
	}
	if command.Preconditions.InstanceUID.String() == "" {
		return NewError(
			ErrorInvalidCommand,
			"deletion requires an exact instance UID",
			"",
		)
	}
	if command.Preconditions.ExpectedGeneration.Value() == 0 {
		return NewError(
			ErrorInvalidCommand,
			"deletion requires a positive expected generation",
			"",
		)
	}
	return nil
}

// ValidateRevisionIntent performs the complete pure offline validation that
// does not depend on a Simulation Target.
func ValidateRevisionIntent(intent RevisionIntent) error {
	if intent.Name.String() == "" ||
		intent.CreationIdentity == "" ||
		intent.Fidelity.String() == "" ||
		intent.Revision.Digest.String() == "" ||
		len(intent.Revision.CanonicalScenario) == 0 {
		return NewError(ErrorInvalidCommand, "revision intent is incomplete", "")
	}
	if intent.Revision.Generation.Value() == 0 {
		return NewError(ErrorInvalidCommand, "revision generation must be positive", "")
	}
	sum := sha256.Sum256(intent.Revision.CanonicalScenario)
	if intent.Revision.Digest.String() != "sha256:"+hex.EncodeToString(sum[:]) {
		return NewError(
			ErrorInvalidCommand,
			"revision digest does not match canonical Scenario bytes",
			"",
		)
	}
	for _, profile := range intent.Revision.Profiles {
		if profile.ID == "" || profile.Revision == "" ||
			profile.Digest.String() == "" || profile.Class == "" {
			return NewError(ErrorInvalidCommand, "revision contains an incomplete profile receipt", "")
		}
	}
	return nil
}

// CloneRecord returns a fully independent record value.
func CloneRecord(record InstanceRecord) InstanceRecord {
	cloned := record
	cloned.Revision = CloneRevision(record.Revision)
	cloned.Revisions = make([]ScenarioRevision, len(record.Revisions))
	for index := range record.Revisions {
		cloned.Revisions[index] = CloneRevision(record.Revisions[index])
	}
	cloned.Status.Pools = append([]PoolStatus(nil), record.Status.Pools...)
	cloned.Status.Inventory = append([]InventoryEntry(nil), record.Status.Inventory...)
	cloned.Status.Fidelity = append(
		[]FidelitySurfaceStatus(nil),
		record.Status.Fidelity...,
	)
	cloned.Status.Diagnostics = append(
		[]DiagnosticStatus(nil),
		record.Status.Diagnostics...,
	)
	cloned.Status.Conditions = append([]ConditionStatus(nil), record.Status.Conditions...)
	return cloned
}

func invalidLimit(limit int) error {
	return fmt.Errorf("watch limit must be positive: %d", limit)
}

// ValidateWatchCursor applies the shared bounded stream contract.
func ValidateWatchCursor(cursor WatchCursor) error {
	if cursor.Key.TargetFingerprint.String() == "" || cursor.Key.Name.String() == "" {
		return NewError(ErrorInvalidCommand, "watch cursor requires an instance key", "")
	}
	if cursor.Limit <= 0 {
		return NewError(ErrorInvalidCommand, invalidLimit(cursor.Limit).Error(), "")
	}
	return nil
}
