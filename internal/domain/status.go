package domain

import (
	"fmt"
	"time"
)

// MaximumSnapshotDiagnostics bounds status growth independently of scale.
const MaximumSnapshotDiagnostics = 32

var phases = map[string]struct{}{
	"Pending":     {},
	"Reconciling": {},
	"Ready":       {},
	"Failed":      {},
	"Deleting":    {},
}

var fidelitySurfaceStates = map[string]struct{}{
	"achieved":     {},
	"excluded":     {},
	"unavailable":  {},
	"out-of-scope": {},
}

var conditionTypes = map[string]struct{}{
	"Progressing":       {},
	"Retrying":          {},
	"Overcommitted":     {},
	"OwnershipConflict": {},
	"CleanupBlocked":    {},
	"FidelitySatisfied": {},
}

// Phase is the bounded Scenario Instance lifecycle state.
type Phase struct {
	value string
}

// ParsePhase accepts only the five documented lifecycle phases.
func ParsePhase(value string) (Phase, error) {
	if _, supported := phases[value]; !supported {
		return Phase{}, fmt.Errorf("unsupported lifecycle phase %q", value)
	}
	return Phase{value: value}, nil
}

func (phase Phase) String() string {
	return phase.value
}

// FidelitySurfaceState records a truthful outcome for one requested surface.
type FidelitySurfaceState struct {
	value string
}

// ParseFidelitySurfaceState rejects vague degraded or supported aliases.
func ParseFidelitySurfaceState(value string) (FidelitySurfaceState, error) {
	if _, supported := fidelitySurfaceStates[value]; !supported {
		return FidelitySurfaceState{}, fmt.Errorf("unsupported fidelity surface state %q", value)
	}
	return FidelitySurfaceState{value: value}, nil
}

func (state FidelitySurfaceState) String() string {
	return state.value
}

// ConditionType is one typed lifecycle signal exposed in bounded status.
type ConditionType struct {
	value string
}

// ParseConditionType accepts only the lifecycle conditions in the v1 contract.
func ParseConditionType(value string) (ConditionType, error) {
	if _, supported := conditionTypes[value]; !supported {
		return ConditionType{}, fmt.Errorf("unsupported condition type %q", value)
	}
	return ConditionType{value: value}, nil
}

func (conditionType ConditionType) String() string {
	return conditionType.value
}

// Condition records one typed state transition with a bounded explanation.
type Condition struct {
	conditionType      ConditionType
	active             bool
	reason             DiagnosticCode
	message            string
	lastTransitionTime time.Time
}

// NewCondition requires valid type, reason, detail, and transition time.
func NewCondition(
	conditionType ConditionType,
	active bool,
	reason DiagnosticCode,
	message string,
	lastTransitionTime time.Time,
) (Condition, error) {
	if _, supported := conditionTypes[conditionType.value]; !supported {
		return Condition{}, fmt.Errorf("Condition requires a valid type")
	}
	if _, supported := diagnosticCodes[reason.value]; !supported {
		return Condition{}, fmt.Errorf("Condition requires a valid reason code")
	}
	if len(message) == 0 || len(message) > MaximumDiagnosticMessageBytes {
		return Condition{}, fmt.Errorf(
			"Condition message must contain 1 to %d bytes",
			MaximumDiagnosticMessageBytes,
		)
	}
	if lastTransitionTime.IsZero() {
		return Condition{}, fmt.Errorf("Condition requires a transition time")
	}
	return Condition{
		conditionType:      conditionType,
		active:             active,
		reason:             reason,
		message:            message,
		lastTransitionTime: lastTransitionTime.UTC(),
	}, nil
}

func (condition Condition) LastTransitionTime() time.Time {
	return condition.lastTransitionTime
}

func (condition Condition) Type() ConditionType {
	return condition.conditionType
}

func (condition Condition) Active() bool {
	return condition.active
}

func (condition Condition) Reason() DiagnosticCode {
	return condition.reason
}

func (condition Condition) Message() string {
	return condition.message
}

// PoolSnapshot compares requested and observed counts for one named pool.
type PoolSnapshot struct {
	group     Name
	pool      Name
	requested PoolCounts
	observed  PoolCounts
}

// NewPoolSnapshot creates a pool observation; its enclosing Snapshot validates
// identity.
func NewPoolSnapshot(
	group Name,
	pool Name,
	requested PoolCounts,
	observed PoolCounts,
) PoolSnapshot {
	return PoolSnapshot{
		group:     group,
		pool:      pool,
		requested: requested,
		observed:  observed,
	}
}

func (snapshot PoolSnapshot) Group() Name {
	return snapshot.group
}

func (snapshot PoolSnapshot) Pool() Name {
	return snapshot.pool
}

func (snapshot PoolSnapshot) Requested() PoolCounts {
	return snapshot.requested
}

func (snapshot PoolSnapshot) Observed() PoolCounts {
	return snapshot.observed
}

// SnapshotInput contains one bounded observation of a Scenario Instance.
type SnapshotInput struct {
	InstanceUID        InstanceUID
	TargetFingerprint  Digest
	DesiredGeneration  Generation
	ObservedGeneration Generation
	Phase              Phase
	Pools              []PoolSnapshot
	Diagnostics        []Diagnostic
}

// Snapshot is immutable lifecycle status. Deliberately requested partial
// health can be Ready when every required surface has converged.
type Snapshot struct {
	instanceUID          InstanceUID
	targetFingerprint    Digest
	desiredGeneration    Generation
	observedGeneration   Generation
	phase                Phase
	pools                []PoolSnapshot
	diagnostics          []Diagnostic
	diagnosticsTruncated bool
}

// NewSnapshot validates identity and readiness and caps diagnostic detail.
func NewSnapshot(input SnapshotInput) (Snapshot, error) {
	if input.InstanceUID.value == "" {
		return Snapshot{}, fmt.Errorf("Snapshot requires an instance UID")
	}
	if input.TargetFingerprint.value == "" {
		return Snapshot{}, fmt.Errorf("Snapshot requires a target fingerprint")
	}
	if input.Phase.value == "" {
		return Snapshot{}, fmt.Errorf("Snapshot requires a lifecycle phase")
	}
	if input.Phase.value == "Ready" &&
		input.DesiredGeneration.value != input.ObservedGeneration.value {
		return Snapshot{}, fmt.Errorf(
			"Ready Snapshot requires observed generation %d to equal desired generation %d",
			input.ObservedGeneration.value,
			input.DesiredGeneration.value,
		)
	}
	for _, pool := range input.Pools {
		if pool.group.value == "" || pool.pool.value == "" {
			return Snapshot{}, fmt.Errorf("Pool Snapshot requires group and pool names")
		}
	}
	diagnostics := input.Diagnostics
	diagnosticsTruncated := len(diagnostics) > MaximumSnapshotDiagnostics
	if diagnosticsTruncated {
		diagnostics = diagnostics[:MaximumSnapshotDiagnostics]
	}
	return Snapshot{
		instanceUID:          input.InstanceUID,
		targetFingerprint:    input.TargetFingerprint,
		desiredGeneration:    input.DesiredGeneration,
		observedGeneration:   input.ObservedGeneration,
		phase:                input.Phase,
		pools:                append([]PoolSnapshot(nil), input.Pools...),
		diagnostics:          append([]Diagnostic(nil), diagnostics...),
		diagnosticsTruncated: diagnosticsTruncated,
	}, nil
}

func (snapshot Snapshot) Phase() Phase {
	return snapshot.phase
}

func (snapshot Snapshot) InstanceUID() InstanceUID {
	return snapshot.instanceUID
}

func (snapshot Snapshot) TargetFingerprint() Digest {
	return snapshot.targetFingerprint
}

func (snapshot Snapshot) DesiredGeneration() Generation {
	return snapshot.desiredGeneration
}

func (snapshot Snapshot) ObservedGeneration() Generation {
	return snapshot.observedGeneration
}

func (snapshot Snapshot) Pools() []PoolSnapshot {
	return append([]PoolSnapshot(nil), snapshot.pools...)
}

func (snapshot Snapshot) Diagnostics() []Diagnostic {
	return append([]Diagnostic(nil), snapshot.diagnostics...)
}

func (snapshot Snapshot) DiagnosticsTruncated() bool {
	return snapshot.diagnosticsTruncated
}
