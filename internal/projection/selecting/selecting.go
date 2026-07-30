// Package selecting binds the two maintained Resource Projection adapters to
// their explicit Fidelity Modes. It is controller composition, not a public
// backend registry.
package selecting

import (
	"fmt"

	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/projection"
	"github.com/LinkMaq/kube-accelerator-sim/internal/projection/dra"
	"github.com/LinkMaq/kube-accelerator-sim/internal/projection/extended"
)

// Adapter dispatches only the two accepted first-release Fidelity Modes.
type Adapter struct {
	scheduling extended.Adapter
	dra        dra.Adapter
}

func New() Adapter {
	return Adapter{
		scheduling: extended.New(),
		dra:        dra.New(),
	}
}

func (adapter Adapter) Support(
	capabilities cluster.TargetCapabilities,
	graph projection.DesiredGraph,
) projection.SupportReport {
	selected, err := adapter.selectProjection(graph)
	if err != nil {
		return projection.NewSupportReport([]projection.SupportIssue{{
			Code:    "UnsupportedFidelity",
			Message: err.Error(),
		}})
	}
	return selected.Support(capabilities, graph)
}

func (adapter Adapter) Render(
	graph projection.DesiredGraph,
	capabilities cluster.TargetCapabilities,
) (projection.ProjectionFragment, error) {
	selected, err := adapter.selectProjection(graph)
	if err != nil {
		return projection.ProjectionFragment{}, err
	}
	fragment, err := selected.Render(graph, capabilities)
	if err != nil {
		return projection.ProjectionFragment{}, err
	}
	return fragment.WithFidelity(graph.Fidelity()), nil
}

func (adapter Adapter) Assess(
	observed projection.ObservedGraph,
	fragment projection.ProjectionFragment,
) projection.FidelityReport {
	switch fragment.Fidelity().String() {
	case "scheduling":
		return adapter.scheduling.Assess(observed, fragment)
	case "dra-control-plane":
		return adapter.dra.Assess(observed, fragment)
	default:
		return projection.NewFidelityReport(nil, nil, nil, nil, false)
	}
}

func (adapter Adapter) selectProjection(
	graph projection.DesiredGraph,
) (projection.ResourceProjection, error) {
	switch graph.Fidelity().String() {
	case "scheduling":
		return adapter.scheduling, nil
	case "dra-control-plane":
		return adapter.dra, nil
	default:
		return nil, fmt.Errorf(
			"unsupported Fidelity Mode %q",
			graph.Fidelity().String(),
		)
	}
}

var _ projection.ResourceProjection = Adapter{}
