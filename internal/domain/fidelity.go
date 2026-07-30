package domain

import "fmt"

const (
	schedulingFidelity      = "scheduling"
	draControlPlaneFidelity = "dra-control-plane"
)

// FidelityMode identifies one explicitly supported Kubernetes behavior
// boundary. Node-runtime protocol testing is intentionally not a product mode.
type FidelityMode struct {
	value string
}

// ParseFidelityMode rejects aliases, backend names, and unsupported modes.
func ParseFidelityMode(value string) (FidelityMode, error) {
	switch value {
	case schedulingFidelity, draControlPlaneFidelity:
		return FidelityMode{value: value}, nil
	default:
		return FidelityMode{}, fmt.Errorf("unsupported Fidelity Mode %q", value)
	}
}

func (mode FidelityMode) String() string {
	return mode.value
}
