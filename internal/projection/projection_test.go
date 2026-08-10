package projection

import (
	"strings"
	"testing"
)

func TestSingleAcceleratorModelRequiresOneConsistentNodeModel(t *testing.T) {
	t.Parallel()

	model, err := singleAcceleratorModel([]DesiredPool{
		{modelID: "nvidia-h200"},
		{modelID: "nvidia-h200"},
	})
	if err != nil || model != "nvidia-h200" {
		t.Fatalf("singleAcceleratorModel() = %q, %v", model, err)
	}
	if _, err := singleAcceleratorModel([]DesiredPool{
		{modelID: "nvidia-h200"},
		{modelID: "nvidia-b200"},
	}); err == nil || !strings.Contains(err.Error(), "cannot project accelerator model label") {
		t.Fatalf("mixed models error = %v", err)
	}
}
