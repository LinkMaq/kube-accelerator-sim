package domain_test

import (
	"strings"
	"testing"

	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

func TestDiagnosticKeepsAutomationSignalsSeparateAndConsistent(t *testing.T) {
	t.Parallel()

	code, err := domain.ParseDiagnosticCode("CleanupBlocked")
	if err != nil {
		t.Fatal(err)
	}
	category, err := domain.ParseExitCategory(5)
	if err != nil {
		t.Fatal(err)
	}
	diagnostic, err := domain.NewDiagnostic(
		code,
		"foreign workload remains bound",
		true,
		true,
		category,
	)
	if err != nil {
		t.Fatal(err)
	}

	if diagnostic.Code().String() != "CleanupBlocked" {
		t.Errorf("code = %q", diagnostic.Code())
	}
	if !diagnostic.Retryable() {
		t.Error("diagnostic is not retryable")
	}
	if !diagnostic.RevisionAccepted() {
		t.Error("diagnostic lost revision acceptance")
	}
	if diagnostic.ExitCategory().Code() != 5 {
		t.Errorf("exit category = %d", diagnostic.ExitCategory().Code())
	}

	preflight, err := domain.ParseExitCategory(3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := domain.NewDiagnostic(code, "contradiction", false, true, preflight); err == nil {
		t.Fatal("accepted revision with preflight exit category unexpectedly succeeded")
	}
}

func TestDiagnosticRejectsUnboundedMessageDetail(t *testing.T) {
	t.Parallel()

	code, err := domain.ParseDiagnosticCode("ScenarioInvalid")
	if err != nil {
		t.Fatal(err)
	}
	category, err := domain.ParseExitCategory(2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := domain.NewDiagnostic(
		code,
		strings.Repeat("x", domain.MaximumDiagnosticMessageBytes+1),
		false,
		false,
		category,
	); err == nil {
		t.Fatal("unbounded diagnostic message unexpectedly succeeded")
	}
}
