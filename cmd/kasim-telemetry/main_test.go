package main

import (
	"testing"
	"time"
)

func TestParseOptionsUsesSafeDefaults(t *testing.T) {
	t.Parallel()

	options, err := parseOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	if options.listenAddress != ":9400" || options.refreshInterval != 15*time.Second ||
		options.staleAfter != 45*time.Second {
		t.Fatalf("unexpected defaults: %#v", options)
	}
}

func TestParseOptionsBoundsSampling(t *testing.T) {
	t.Parallel()

	for _, arguments := range [][]string{
		{"--refresh-interval=500ms"},
		{"--refresh-interval=61s"},
		{"--refresh-interval=15s", "--stale-after=20s"},
		{"--refresh-interval=15s", "--stale-after=11m"},
		{"--listen-address="},
		{"unexpected"},
	} {
		if _, err := parseOptions(arguments); err == nil {
			t.Errorf("parseOptions(%q) unexpectedly succeeded", arguments)
		}
	}
}

func TestParseOptionsHasNoKubeconfigEscapeHatch(t *testing.T) {
	t.Parallel()

	if _, err := parseOptions([]string{"--kubeconfig=/tmp/admin.conf"}); err == nil {
		t.Fatal("telemetry runtime unexpectedly accepted a kubeconfig flag")
	}
}
