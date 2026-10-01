package contract

import "fmt"

// ConversionDiagnosticPhase tells which half of a local protocol conversion
// reported a diagnostic.
type ConversionDiagnosticPhase string

const (
	ConversionDiagnosticPhaseRequest  ConversionDiagnosticPhase = "request"
	ConversionDiagnosticPhaseResponse ConversionDiagnosticPhase = "response"
)

func (phase ConversionDiagnosticPhase) Valid() bool {
	return phase == ConversionDiagnosticPhaseRequest || phase == ConversionDiagnosticPhaseResponse
}

// ConversionDiagnosticSeverity separates losses that can change what the model
// or its tools do (error) from losses of presentation-only detail (warning).
// Neither fails the request.
type ConversionDiagnosticSeverity string

const (
	ConversionDiagnosticWarning ConversionDiagnosticSeverity = "warning"
	ConversionDiagnosticError   ConversionDiagnosticSeverity = "error"
)

func (severity ConversionDiagnosticSeverity) Valid() bool {
	return severity == ConversionDiagnosticWarning || severity == ConversionDiagnosticError
}

const (
	MaxConversionDiagnostics           = 64
	MaxConversionDiagnosticCodeRunes   = 128
	MaxConversionDiagnosticPathRunes   = 256
	MaxConversionDiagnosticDetailRunes = 1024
)

// ConversionDiagnostic is one tool or field that a local protocol conversion
// dropped or rewrote for one upstream attempt. It is informational: the
// conversion still succeeded. Path points into the converted body, such as
// "tools[2]"; it never carries the content itself.
type ConversionDiagnostic struct {
	Phase    ConversionDiagnosticPhase    `json:"phase"`
	Severity ConversionDiagnosticSeverity `json:"severity"`
	Code     string                       `json:"code"`
	Path     string                       `json:"path,omitempty"`
	Message  string                       `json:"message"`
}

func validateConversionDiagnostics(diagnostics []ConversionDiagnostic) error {
	if len(diagnostics) > MaxConversionDiagnostics {
		return fmt.Errorf("conversion_diagnostics must contain at most %d items", MaxConversionDiagnostics)
	}
	for _, diagnostic := range diagnostics {
		if !diagnostic.Phase.Valid() {
			return fmt.Errorf("unknown conversion diagnostic phase %q", diagnostic.Phase)
		}
		if !diagnostic.Severity.Valid() {
			return fmt.Errorf("unknown conversion diagnostic severity %q", diagnostic.Severity)
		}
		if err := validateBoundedText("conversion diagnostic code", diagnostic.Code, MaxConversionDiagnosticCodeRunes, false); err != nil {
			return err
		}
		if err := validateBoundedText("conversion diagnostic path", diagnostic.Path, MaxConversionDiagnosticPathRunes, true); err != nil {
			return err
		}
		if err := validateBoundedText("conversion diagnostic message", diagnostic.Message, MaxConversionDiagnosticDetailRunes, true); err != nil {
			return err
		}
	}
	return nil
}
