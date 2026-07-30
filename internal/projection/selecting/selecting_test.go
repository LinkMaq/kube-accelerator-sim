package selecting_test

import (
	"testing"

	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/projection"
	"github.com/LinkMaq/kube-accelerator-sim/internal/projection/selecting"
)

func TestAdapterRejectsAnEmptyFidelity(t *testing.T) {
	t.Parallel()

	report := selecting.New().Support(
		cluster.TargetCapabilities{},
		projection.DesiredGraph{},
	)
	if report.Supported() {
		t.Fatal("empty Fidelity Mode unexpectedly selected a projection")
	}
	if got := report.Issues()[0].Code; got != "UnsupportedFidelity" {
		t.Fatalf("issue code = %q, want UnsupportedFidelity", got)
	}
}
