package domain_test

import (
	"testing"

	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

func TestFidelityModeAcceptsOnlyProductModes(t *testing.T) {
	t.Parallel()

	for input, want := range map[string]string{
		"scheduling":        "scheduling",
		"dra-control-plane": "dra-control-plane",
	} {
		mode, err := domain.ParseFidelityMode(input)
		if err != nil {
			t.Fatalf("ParseFidelityMode(%q): %v", input, err)
		}
		if mode.String() != want {
			t.Errorf("ParseFidelityMode(%q) = %q, want %q", input, mode, want)
		}
	}

	for _, input := range []string{"", "node-runtime", "backend", "SCHEDULING"} {
		if _, err := domain.ParseFidelityMode(input); err == nil {
			t.Errorf("ParseFidelityMode(%q) unexpectedly succeeded", input)
		}
	}
}
