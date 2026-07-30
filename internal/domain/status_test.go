package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

func TestPhaseAcceptsOnlyLifecycleStates(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"Pending", "Reconciling", "Ready", "Failed", "Deleting"} {
		phase, err := domain.ParsePhase(input)
		if err != nil {
			t.Fatalf("ParsePhase(%q): %v", input, err)
		}
		if phase.String() != input {
			t.Errorf("ParsePhase(%q) = %q", input, phase)
		}
	}

	for _, input := range []string{"", "Healthy", "Running", "ready"} {
		if _, err := domain.ParsePhase(input); err == nil {
			t.Errorf("ParsePhase(%q) unexpectedly succeeded", input)
		}
	}
}

func TestFidelitySurfaceStatePreservesTruthfulOutcomes(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"achieved", "excluded", "unavailable", "out-of-scope"} {
		state, err := domain.ParseFidelitySurfaceState(input)
		if err != nil {
			t.Fatalf("ParseFidelitySurfaceState(%q): %v", input, err)
		}
		if state.String() != input {
			t.Errorf("ParseFidelitySurfaceState(%q) = %q", input, state)
		}
	}

	for _, input := range []string{"", "supported", "degraded", "Achieved"} {
		if _, err := domain.ParseFidelitySurfaceState(input); err == nil {
			t.Errorf("ParseFidelitySurfaceState(%q) unexpectedly succeeded", input)
		}
	}
}

func TestConditionTypeAcceptsOnlyDefinedLifecycleSignals(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		"Progressing",
		"Retrying",
		"Overcommitted",
		"OwnershipConflict",
		"CleanupBlocked",
		"FidelitySatisfied",
	} {
		conditionType, err := domain.ParseConditionType(input)
		if err != nil {
			t.Fatalf("ParseConditionType(%q): %v", input, err)
		}
		if conditionType.String() != input {
			t.Errorf("ParseConditionType(%q) = %q", input, conditionType)
		}
	}

	for _, input := range []string{"", "Healthy", "Ready", "progressing"} {
		if _, err := domain.ParseConditionType(input); err == nil {
			t.Errorf("ParseConditionType(%q) unexpectedly succeeded", input)
		}
	}
}

func TestReadySnapshotAllowsRequestedPartialHealth(t *testing.T) {
	t.Parallel()

	uid, err := domain.ParseInstanceUID("6cb2dd6f-c608-4e79-aaf6-e3fa1287f73c")
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := domain.ParseDigest(
		"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := domain.NewGeneration(7)
	if err != nil {
		t.Fatal(err)
	}
	phase, err := domain.ParsePhase("Ready")
	if err != nil {
		t.Fatal(err)
	}
	group, err := domain.ParseName("workers")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := domain.ParseName("training")
	if err != nil {
		t.Fatal(err)
	}
	requested, err := domain.NewPoolCounts(8, 3)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := domain.NewPoolCounts(8, 3)
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := domain.NewSnapshot(domain.SnapshotInput{
		InstanceUID:        uid,
		TargetFingerprint:  fingerprint,
		DesiredGeneration:  generation,
		ObservedGeneration: generation,
		Phase:              phase,
		Pools: []domain.PoolSnapshot{
			domain.NewPoolSnapshot(group, pool, requested, observed),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if snapshot.Phase().String() != "Ready" {
		t.Fatalf("phase = %q", snapshot.Phase())
	}
	if snapshot.InstanceUID() != uid ||
		snapshot.TargetFingerprint() != fingerprint ||
		snapshot.DesiredGeneration() != generation ||
		snapshot.ObservedGeneration() != generation {
		t.Fatalf("Snapshot identity was not preserved")
	}
	if snapshot.Pools()[0].Requested().Healthy() != 3 {
		t.Fatalf("requested healthy = %d", snapshot.Pools()[0].Requested().Healthy())
	}
}

func TestSnapshotCapsDiagnosticsAndReportsTruncation(t *testing.T) {
	t.Parallel()

	uid, err := domain.ParseInstanceUID("instance-uid")
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := domain.ParseDigest(
		"sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
	)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := domain.NewGeneration(1)
	if err != nil {
		t.Fatal(err)
	}
	phase, err := domain.ParsePhase("Reconciling")
	if err != nil {
		t.Fatal(err)
	}
	code, err := domain.ParseDiagnosticCode("ConvergenceFailed")
	if err != nil {
		t.Fatal(err)
	}
	category, err := domain.ParseExitCategory(5)
	if err != nil {
		t.Fatal(err)
	}
	diagnostic, err := domain.NewDiagnostic(code, "bounded detail", true, true, category)
	if err != nil {
		t.Fatal(err)
	}

	inputDiagnostics := make([]domain.Diagnostic, domain.MaximumSnapshotDiagnostics+17)
	for index := range inputDiagnostics {
		inputDiagnostics[index] = diagnostic
	}
	snapshot, err := domain.NewSnapshot(domain.SnapshotInput{
		InstanceUID:        uid,
		TargetFingerprint:  fingerprint,
		DesiredGeneration:  generation,
		ObservedGeneration: generation,
		Phase:              phase,
		Diagnostics:        inputDiagnostics,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(snapshot.Diagnostics()) != domain.MaximumSnapshotDiagnostics {
		t.Fatalf(
			"diagnostics = %d, want %d",
			len(snapshot.Diagnostics()),
			domain.MaximumSnapshotDiagnostics,
		)
	}
	if !snapshot.DiagnosticsTruncated() {
		t.Fatal("Snapshot did not report truncated diagnostics")
	}
}

func TestConditionRequiresAndNormalizesTransitionTime(t *testing.T) {
	t.Parallel()

	conditionType, err := domain.ParseConditionType("Progressing")
	if err != nil {
		t.Fatal(err)
	}
	code, err := domain.ParseDiagnosticCode("ConvergenceFailed")
	if err != nil {
		t.Fatal(err)
	}
	transition := time.Date(2026, 7, 30, 9, 15, 0, 0, time.FixedZone("CST", 8*60*60))

	condition, err := domain.NewCondition(
		conditionType,
		true,
		code,
		"applying desired generation",
		transition,
	)
	if err != nil {
		t.Fatal(err)
	}
	if condition.LastTransitionTime().Location() != time.UTC {
		t.Fatalf("transition location = %s", condition.LastTransitionTime().Location())
	}
	if condition.Type().String() != "Progressing" ||
		!condition.Active() ||
		condition.Reason().String() != "ConvergenceFailed" ||
		condition.Message() != "applying desired generation" {
		t.Fatalf("condition fields were not preserved: %#v", condition)
	}
	if !condition.LastTransitionTime().Equal(transition) {
		t.Fatalf("transition = %s, want %s", condition.LastTransitionTime(), transition)
	}

	if _, err := domain.NewCondition(
		conditionType,
		true,
		code,
		"missing timestamp",
		time.Time{},
	); err == nil {
		t.Fatal("condition with zero transition time unexpectedly succeeded")
	}
	if _, err := domain.NewCondition(
		conditionType,
		true,
		code,
		strings.Repeat("x", domain.MaximumDiagnosticMessageBytes+1),
		transition,
	); err == nil {
		t.Fatal("condition with unbounded message unexpectedly succeeded")
	}
}
