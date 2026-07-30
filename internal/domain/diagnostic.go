package domain

import "fmt"

// MaximumDiagnosticMessageBytes bounds machine and human diagnostic detail.
const MaximumDiagnosticMessageBytes = 1024

var diagnosticCodes = map[string]struct{}{
	"InvocationInvalid":     {},
	"ScenarioInvalid":       {},
	"CatalogInvalid":        {},
	"TargetUnavailable":     {},
	"AuthenticationFailed":  {},
	"AuthorizationDenied":   {},
	"CapabilityUnavailable": {},
	"RuntimeUnavailable":    {},
	"UIDConflict":           {},
	"GenerationConflict":    {},
	"TargetMismatch":        {},
	"FidelityConflict":      {},
	"OwnershipConflict":     {},
	"ConvergenceFailed":     {},
	"ConvergenceTimeout":    {},
	"CleanupBlocked":        {},
}

var exitCategories = map[int]struct{}{
	0: {},
	2: {},
	3: {},
	4: {},
	5: {},
}

// DiagnosticCode is a stable, versioned automation signal.
type DiagnosticCode struct {
	value string
}

// ParseDiagnosticCode rejects codes outside the supported automation contract.
func ParseDiagnosticCode(value string) (DiagnosticCode, error) {
	if _, supported := diagnosticCodes[value]; !supported {
		return DiagnosticCode{}, fmt.Errorf("unsupported diagnostic code %q", value)
	}
	return DiagnosticCode{value: value}, nil
}

func (code DiagnosticCode) String() string {
	return code.value
}

// ExitCategory is the stable coarse process outcome defined by the CLI
// contract.
type ExitCategory struct {
	code int
}

// ParseExitCategory accepts only the documented success and failure classes.
func ParseExitCategory(code int) (ExitCategory, error) {
	if _, supported := exitCategories[code]; !supported {
		return ExitCategory{}, fmt.Errorf("unsupported exit category %d", code)
	}
	return ExitCategory{code: code}, nil
}

func (category ExitCategory) Code() int {
	return category.code
}

// Diagnostic keeps code, retryability, revision acceptance, and exit category
// separate so automation does not infer one signal from another.
type Diagnostic struct {
	code             DiagnosticCode
	message          string
	retryable        bool
	revisionAccepted bool
	exitCategory     ExitCategory
}

// NewDiagnostic rejects contradictory acceptance and exit-category states.
func NewDiagnostic(
	code DiagnosticCode,
	message string,
	retryable bool,
	revisionAccepted bool,
	exitCategory ExitCategory,
) (Diagnostic, error) {
	if _, supported := diagnosticCodes[code.value]; !supported {
		return Diagnostic{}, fmt.Errorf("invalid diagnostic code")
	}
	if len(message) == 0 || len(message) > MaximumDiagnosticMessageBytes {
		return Diagnostic{}, fmt.Errorf(
			"diagnostic message must contain 1 to %d bytes",
			MaximumDiagnosticMessageBytes,
		)
	}
	if _, supported := exitCategories[exitCategory.code]; !supported || exitCategory.code == 0 {
		return Diagnostic{}, fmt.Errorf("diagnostic requires a non-success exit category")
	}
	if revisionAccepted != (exitCategory.code == 5) {
		return Diagnostic{}, fmt.Errorf(
			"revision acceptance %t conflicts with exit category %d",
			revisionAccepted,
			exitCategory.code,
		)
	}
	return Diagnostic{
		code:             code,
		message:          message,
		retryable:        retryable,
		revisionAccepted: revisionAccepted,
		exitCategory:     exitCategory,
	}, nil
}

func (diagnostic Diagnostic) Code() DiagnosticCode {
	return diagnostic.code
}

func (diagnostic Diagnostic) Message() string {
	return diagnostic.message
}

func (diagnostic Diagnostic) Retryable() bool {
	return diagnostic.retryable
}

func (diagnostic Diagnostic) RevisionAccepted() bool {
	return diagnostic.revisionAccepted
}

func (diagnostic Diagnostic) ExitCategory() ExitCategory {
	return diagnostic.exitCategory
}
