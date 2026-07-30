package application

import (
	"fmt"
	"math"

	"github.com/LinkMaq/kube-accelerator-sim/internal/controlplane"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
	"github.com/LinkMaq/kube-accelerator-sim/internal/scenario"
)

// NewRevisionIntent translates one offline compiler result into the exact
// intention-level mutation accepted by ScenarioRuntime. ResourceVersion is
// deliberately bound during ordered target preflight.
func NewRevisionIntent(
	compiled scenario.CanonicalScenario,
	receipt scenario.CompileReceipt,
	creationIdentity string,
	instanceUID domain.InstanceUID,
	expectedGeneration domain.Generation,
) (controlplane.RevisionIntent, error) {
	if creationIdentity == "" {
		return controlplane.RevisionIntent{}, fmt.Errorf(
			"revision intent requires one immutable creation identity",
		)
	}
	if expectedGeneration.Value() == 0 && instanceUID.String() != "" {
		return controlplane.RevisionIntent{}, fmt.Errorf(
			"create precondition must not include an instance UID",
		)
	}
	if expectedGeneration.Value() != 0 && instanceUID.String() == "" {
		return controlplane.RevisionIntent{}, fmt.Errorf(
			"updating a Scenario requires an exact instance UID and expected generation",
		)
	}
	if expectedGeneration.Value() >= math.MaxInt64 {
		return controlplane.RevisionIntent{}, fmt.Errorf(
			"Scenario generation cannot advance",
		)
	}
	nextGeneration, err := domain.NewGeneration(
		int64(expectedGeneration.Value()) + 1,
	)
	if err != nil {
		return controlplane.RevisionIntent{}, err
	}
	profiles, err := profileReceipts(compiled, receipt)
	if err != nil {
		return controlplane.RevisionIntent{}, err
	}
	intent := controlplane.RevisionIntent{
		Name:             compiled.Scenario().Name(),
		CreationIdentity: creationIdentity,
		Fidelity:         compiled.Scenario().Fidelity(),
		Preconditions: controlplane.Preconditions{
			InstanceUID:        instanceUID,
			ExpectedGeneration: expectedGeneration,
		},
		Revision: controlplane.ScenarioRevision{
			Generation:        nextGeneration,
			Digest:            compiled.Digest(),
			CanonicalScenario: compiled.Bytes(),
			Profiles:          profiles,
		},
	}
	if err := controlplane.ValidateRevisionIntent(intent); err != nil {
		return controlplane.RevisionIntent{}, err
	}
	return intent, nil
}
