package autoclassifier

import (
	"github.com/QuantumNous/astrlink/core/contract"
)

// FallbackReason values classifier sources add beyond the constants in
// client.go. They live here, in the parent package, so every source and both
// consumers (the inference path and the control-plane dry-run) share one
// reason domain instead of re-declaring it per adapter.
const (
	// FallbackRefusal marks a provider that answered with a refusal instead
	// of a classification.
	FallbackRefusal = "classifier_refusal"
	// FallbackInvalidOutput marks an answer that failed strict validation.
	FallbackInvalidOutput = "classifier_invalid_output"
)

// FailureResult is the shared mapping of one failed classification onto the
// configured fallback semantics. Value is non-empty only for a general
// fallback whose label the group accepts; Code always carries the diagnostic
// reason in the stable record-code domain (classifier_timeout, ...), never
// the raw provider reason string.
type FailureResult struct {
	Value        string
	Code         string
	DurationMS   int
	ClassifierID contract.AutoClassifierID
}

// FailureTaskType maps a failed classification onto the configured fallback.
// It is the one implementation shared by the inference path and the
// control-plane dry-run, so both report the same codes and honor
// fallback=error/general identically. Fail-open is not negotiable: even
// fallback=error only records classifier_error and returns the deterministic
// skip result; nothing here may block the caller's request.
func FailureTaskType(fallback string, group contract.ModelRoutingGroup, outcome Outcome, durationMS int) FailureResult {
	var code string
	switch outcome.FallbackReason {
	case FallbackBusy:
		code = "classifier_busy"
	case FallbackTimeout:
		code = "classifier_timeout"
	case FallbackRefusal:
		code = "classifier_refusal"
	case FallbackInvalidOutput:
		code = "classifier_invalid_output"
	default:
		code = FallbackUnavailable
	}
	switch fallback {
	case contract.ClassifierFallbackError:
		return FailureResult{Code: "classifier_error", DurationMS: durationMS, ClassifierID: outcome.ClassifierID}
	case contract.ClassifierFallbackGeneral:
		if group.ValidTaskTypeLabel("general") {
			return FailureResult{Value: "general", Code: code, DurationMS: durationMS, ClassifierID: outcome.ClassifierID}
		}
		return FailureResult{Code: "classifier_fallback_invalid", DurationMS: durationMS, ClassifierID: outcome.ClassifierID}
	default:
		return FailureResult{Code: code, DurationMS: durationMS, ClassifierID: outcome.ClassifierID}
	}
}
