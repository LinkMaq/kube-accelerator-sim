package main

import (
	"testing"
)

func TestParseOptionsUsesSafeRuntimeDefaults(t *testing.T) {
	t.Parallel()

	options, err := parseOptions(nil)
	if err != nil {
		t.Fatalf("parse defaults: %v", err)
	}
	if options.metricsAddress != ":8080" {
		t.Errorf("metrics address = %q, want :8080", options.metricsAddress)
	}
	if options.healthAddress != ":8081" {
		t.Errorf("health address = %q, want :8081", options.healthAddress)
	}
	if options.maxConcurrentReconciles != 8 {
		t.Errorf(
			"max concurrent reconciles = %d, want 8",
			options.maxConcurrentReconciles,
		)
	}
	if options.leaderElection {
		t.Error("leader election unexpectedly enabled by default")
	}
}

func TestParseOptionsHasNoKubeconfigEscapeHatch(t *testing.T) {
	t.Parallel()

	if _, err := parseOptions([]string{"--kubeconfig=/tmp/admin.conf"}); err == nil {
		t.Fatal("controller unexpectedly accepted a kubeconfig flag")
	}
}

func TestParseOptionsBoundsConcurrency(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"0", "17"} {
		if _, err := parseOptions([]string{
			"--max-concurrent-reconciles=" + value,
		}); err == nil {
			t.Fatalf("controller unexpectedly accepted concurrency %s", value)
		}
	}
}

func TestParseOptionsRestrictsInternalStageOperation(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"apply", "delete"} {
		options, err := parseOptions([]string{"--kwok-stage-operation=" + value})
		if err != nil {
			t.Fatalf("parse stage operation %q: %v", value, err)
		}
		if options.kwokStageOperation != value {
			t.Errorf("stage operation = %q, want %q", options.kwokStageOperation, value)
		}
	}
	if _, err := parseOptions([]string{
		"--kwok-stage-operation=replace-all",
	}); err == nil {
		t.Fatal("controller accepted an unbounded Stage operation")
	}
}
