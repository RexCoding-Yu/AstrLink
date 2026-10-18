package contract

import (
	"fmt"
	"slices"
	"unicode/utf8"
)

// MaxSystemPromptRunes bounds each global system prompt text.
const MaxSystemPromptRunes = 20000

// ReasoningMode chooses how the gateway sets the reasoning of inference
// requests.
type ReasoningMode string

const (
	// ReasoningModePassthrough leaves reasoning as each client sets it.
	ReasoningModePassthrough ReasoningMode = "passthrough"
	// ReasoningModeFixed sends every request at ReasoningLevel, turning
	// reasoning on or off as needed.
	ReasoningModeFixed ReasoningMode = "fixed"
	// ReasoningModeAdaptive sends a level inside the AdaptivePreset range:
	// the lower end for a simple request, one level higher for each of more
	// than 10 messages, more than 3 tools and a last user message over 2000
	// characters, and the upper end when all three apply.
	ReasoningModeAdaptive ReasoningMode = "adaptive"
)

func (mode ReasoningMode) Validate() error {
	switch mode {
	case ReasoningModePassthrough, ReasoningModeFixed, ReasoningModeAdaptive:
		return nil
	default:
		return fmt.Errorf("reasoning_mode must be passthrough, fixed or adaptive")
	}
}

// ReasoningLevel is a reasoning strength: the level the fixed mode sends, or
// an end of the adaptive range.
type ReasoningLevel string

const (
	// ReasoningLevelOff turns reasoning off, or to the lowest setting of a
	// model that cannot turn it off.
	ReasoningLevelOff    ReasoningLevel = "off"
	ReasoningLevelLow    ReasoningLevel = "low"
	ReasoningLevelMedium ReasoningLevel = "medium"
	ReasoningLevelHigh   ReasoningLevel = "high"
	// ReasoningLevelMax selects the highest level each model supports.
	ReasoningLevelMax ReasoningLevel = "max"
)

// ReasoningLevels lists the levels from least to most reasoning.
var ReasoningLevels = []ReasoningLevel{
	ReasoningLevelOff, ReasoningLevelLow, ReasoningLevelMedium, ReasoningLevelHigh, ReasoningLevelMax,
}

// Rank is the level's index in ReasoningLevels; -1 for an unknown level.
func (level ReasoningLevel) Rank() int {
	return slices.Index(ReasoningLevels, level)
}

func (level ReasoningLevel) Validate() error {
	return level.validate("reasoning_level")
}

func (level ReasoningLevel) validate(field string) error {
	if level.Rank() < 0 {
		return fmt.Errorf("%s must be off, low, medium, high or max", field)
	}
	return nil
}

// AdaptivePreset names the range of levels the adaptive mode sends.
type AdaptivePreset string

const (
	// AdaptivePresetEveryday sends low through max.
	AdaptivePresetEveryday AdaptivePreset = "everyday"
	// AdaptivePresetResearch sends medium through max.
	AdaptivePresetResearch AdaptivePreset = "research"
	// AdaptivePresetCustom sends AdaptiveMin through AdaptiveMax.
	AdaptivePresetCustom AdaptivePreset = "custom"
)

func (preset AdaptivePreset) Validate() error {
	switch preset {
	case AdaptivePresetEveryday, AdaptivePresetResearch, AdaptivePresetCustom:
		return nil
	default:
		return fmt.Errorf("adaptive_preset must be everyday, research or custom")
	}
}

// AdaptiveRange is the lowest and the highest level the adaptive mode sends
// with the settings' preset.
func (settings RoutingSettings) AdaptiveRange() (ReasoningLevel, ReasoningLevel) {
	switch settings.AdaptivePreset {
	case AdaptivePresetEveryday:
		return ReasoningLevelLow, ReasoningLevelMax
	case AdaptivePresetResearch:
		return ReasoningLevelMedium, ReasoningLevelMax
	default:
		return settings.AdaptiveMin, settings.AdaptiveMax
	}
}

// validateAdaptiveRange checks the custom range, which is kept while another
// preset or mode is chosen.
func validateAdaptiveRange(low, high ReasoningLevel) error {
	if err := low.validate("adaptive_min"); err != nil {
		return err
	}
	if err := high.validate("adaptive_max"); err != nil {
		return err
	}
	if low.Rank() > high.Rank() {
		return fmt.Errorf("adaptive_min must be at most adaptive_max")
	}
	return nil
}

func validateSystemPromptText(name, text string) error {
	if !utf8.ValidString(text) {
		return fmt.Errorf("%s must be valid UTF-8", name)
	}
	if utf8.RuneCountInString(text) > MaxSystemPromptRunes {
		return fmt.Errorf("%s must be at most %d characters", name, MaxSystemPromptRunes)
	}
	return nil
}
