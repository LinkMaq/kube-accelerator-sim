package application

import (
	"errors"

	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/controlplane"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
)

// DiagnosticForError maps a lifecycle error to the stable CLI automation
// contract. Post-acceptance RuntimeError values retain their original
// diagnostic unchanged.
func DiagnosticForError(err error) domain.Diagnostic {
	var runtimeError *RuntimeError
	if errors.As(err, &runtimeError) {
		return runtimeError.Diagnostic()
	}

	codeName := "TargetUnavailable"
	categoryCode := 3
	retryable := false
	switch controlplane.ErrorCodeOf(err) {
	case controlplane.ErrorUIDConflict:
		codeName, categoryCode = "UIDConflict", 4
	case controlplane.ErrorGenerationConflict,
		controlplane.ErrorResourceVersionConflict:
		codeName, categoryCode = "GenerationConflict", 4
	case controlplane.ErrorTargetMismatch:
		codeName, categoryCode = "TargetMismatch", 4
	case controlplane.ErrorFidelityConflict:
		codeName, categoryCode = "FidelityConflict", 4
	case controlplane.ErrorCreationIdentityConflict,
		controlplane.ErrorNotFound:
		codeName, categoryCode = "OwnershipConflict", 4
	case controlplane.ErrorInvalidCommand:
		codeName, categoryCode = "ScenarioInvalid", 2
	}
	var clusterError *cluster.Error
	if errors.As(err, &clusterError) {
		retryable = clusterError.Retryable
		switch clusterError.Code {
		case cluster.ErrorInvalidIntent:
			codeName, categoryCode = "InvocationInvalid", 2
		case cluster.ErrorAuthenticationFailed:
			codeName, categoryCode = "AuthenticationFailed", 3
		case cluster.ErrorAuthorizationDenied:
			codeName, categoryCode = "AuthorizationDenied", 3
		case cluster.ErrorRuntimeUnavailable:
			codeName, categoryCode = "RuntimeUnavailable", 3
		case cluster.ErrorOwnershipConflict:
			codeName, categoryCode = "OwnershipConflict", 4
		case cluster.ErrorUIDConflict:
			codeName, categoryCode = "UIDConflict", 4
		case cluster.ErrorResourceVersionConflict:
			codeName, categoryCode = "GenerationConflict", 4
		case cluster.ErrorTargetUnavailable,
			cluster.ErrorRateLimited,
			cluster.ErrorTransient:
			codeName, categoryCode = "TargetUnavailable", 3
		case cluster.ErrorCapabilityUnavailable,
			cluster.ErrorKubernetesVersionUnsupported,
			cluster.ErrorKubernetesVersionUntested,
			cluster.ErrorAdmissionRejected:
			codeName, categoryCode = "CapabilityUnavailable", 3
		}
	}
	message := "command failed safely"
	if err != nil && err.Error() != "" {
		message = err.Error()
	}
	if len(message) > domain.MaximumDiagnosticMessageBytes {
		message = message[:domain.MaximumDiagnosticMessageBytes]
	}
	code, codeErr := domain.ParseDiagnosticCode(codeName)
	category, categoryErr := domain.ParseExitCategory(categoryCode)
	diagnostic, diagnosticErr := domain.NewDiagnostic(
		code,
		message,
		retryable,
		false,
		category,
	)
	if codeErr == nil && categoryErr == nil && diagnosticErr == nil {
		return diagnostic
	}
	fallbackCode, _ := domain.ParseDiagnosticCode("InvocationInvalid")
	fallbackCategory, _ := domain.ParseExitCategory(2)
	fallback, _ := domain.NewDiagnostic(
		fallbackCode,
		"command failed safely",
		false,
		false,
		fallbackCategory,
	)
	return fallback
}
