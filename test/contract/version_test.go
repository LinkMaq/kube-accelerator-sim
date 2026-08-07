package contract_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestProductBinariesReportVersionMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "CLI",
			args: []string{"run", "../../cmd/kasim", "version"},
			want: []string{
				"kasim dev",
				"schema=v1alpha1",
				"catalog=2026-08-03",
				"kubernetes=1.30-1.36",
			},
		},
		{
			name: "controller",
			args: []string{"run", "../../cmd/kasim-controller", "--version"},
			want: []string{
				"kasim-controller dev",
				"schema=v1alpha1",
				"catalog=2026-08-03",
				"kubernetes=1.30-1.36",
			},
		},
		{
			name: "telemetry",
			args: []string{"run", "../../cmd/kasim-telemetry", "--version"},
			want: []string{
				"kasim-telemetry dev",
				"schema=v1alpha1",
				"catalog=2026-08-03",
				"kubernetes=1.30-1.36",
				"telemetry-catalog=2026-08-07",
				"digest=sha256:500ab769c3746e746b0bb880089052114f14f6894f6fe9c74c22bcb78dc2557e",
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			command := exec.Command("go", test.args...)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("%v failed: %v\n%s", command.Args, err, output)
			}

			actual := string(output)
			for _, want := range test.want {
				if !strings.Contains(actual, want) {
					t.Errorf("output %q does not contain %q", actual, want)
				}
			}
		})
	}
}
