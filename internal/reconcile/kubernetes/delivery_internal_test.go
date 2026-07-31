package kubernetes

import (
	"errors"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/LinkMaq/kube-accelerator-sim/internal/reconcile"
)

func TestDeliveryOutcomeRequeuesOptimisticStatusConflictWithoutError(
	t *testing.T,
) {
	t.Parallel()

	delay := time.Second
	result, err := deliveryOutcome(
		reconcile.Result{},
		newStatusCommitConflict(apierrors.NewConflict(
			schema.GroupResource{
				Group:    "simulation.kasim.io",
				Resource: "scenarioinstances",
			},
			"reference-scale",
			errors.New("status intent resourceVersion precondition failed"),
		)),
		delay,
	)
	if err != nil {
		t.Fatalf("optimistic status conflict escaped delivery: %v", err)
	}
	if result.RequeueAfter != delay {
		t.Fatalf(
			"optimistic status conflict requeue = %s, want %s",
			result.RequeueAfter,
			delay,
		)
	}
}

func TestDeliveryOutcomePreservesUnrelatedAPIConflict(t *testing.T) {
	t.Parallel()

	expected := apierrors.NewConflict(
		schema.GroupResource{
			Group:    "simulation.kasim.io",
			Resource: "scenarioinstances",
		},
		"reference-scale",
		errors.New("unrelated mutation conflict"),
	)
	result, err := deliveryOutcome(reconcile.Result{}, expected, time.Second)
	if !errors.Is(err, expected) ||
		result.Requeue ||
		result.RequeueAfter != 0 {
		t.Fatalf("unrelated API conflict outcome = %#v, %v", result, err)
	}
}

func TestDeliveryOutcomePreservesUnexpectedError(t *testing.T) {
	t.Parallel()

	expected := errors.New("target transport failed")
	result, err := deliveryOutcome(reconcile.Result{}, expected, time.Second)
	if !errors.Is(err, expected) ||
		result.Requeue ||
		result.RequeueAfter != 0 {
		t.Fatalf("unexpected error outcome = %#v, %v", result, err)
	}
}

func TestStatusWriteErrorMarksOnlyAPIConflict(t *testing.T) {
	t.Parallel()

	conflict := apierrors.NewConflict(
		schema.GroupResource{
			Group:    "simulation.kasim.io",
			Resource: "scenarioinstances",
		},
		"reference-scale",
		errors.New("finalizer update resourceVersion changed"),
	)
	marked := statusWriteError("ensure ownership finalizer", conflict)
	if !isStatusCommitConflict(marked) ||
		!apierrors.IsConflict(marked) ||
		!errors.Is(marked, conflict) {
		t.Fatalf("status write conflict was not narrowly marked: %v", marked)
	}

	expected := errors.New("target transport failed")
	unexpected := statusWriteError("update status", expected)
	if isStatusCommitConflict(unexpected) ||
		!errors.Is(unexpected, expected) {
		t.Fatalf("non-conflict status write error was marked: %v", unexpected)
	}
}
