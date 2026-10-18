package relaykitbridge

import (
	"math"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"
)

// Reasoning levels use RelayKit's effort vocabulary: none, minimal, low,
// medium, high, xhigh and max.

// ClaudeThinking is how RelayKit renders a reasoning level for a Claude model.
type ClaudeThinking struct {
	// Type is the thinking type to send: enabled, adaptive, disabled or
	// between_tools. Empty leaves the request's thinking as it is.
	Type string
	// BudgetTokens is the manual thinking budget; nil for other types.
	BudgetTokens *int
	// Effort is the output_config.effort to send; empty leaves it as it is.
	Effort string
	// MaxTokens is set when manual thinking needs a larger max_tokens.
	MaxTokens *int
	// ClearSampling drops temperature, top_p and top_k;
	// ConstrainSampling keeps only a top_p from 0.95 through 1.
	ClearSampling, ConstrainSampling bool
	// Level is RelayKit's name for the strength that is sent; none when
	// thinking is off.
	Level string
}

// ClaudeModelKnown reports whether RelayKit has thinking capabilities for a
// Claude model name.
func ClaudeModelKnown(model string) bool {
	return reasoning.IsKnownClaudeModel(model)
}

// ClaudeDefaultLevel is the level a Claude model thinks at when a request
// sets no thinking: none for models that think only when asked.
func ClaudeDefaultLevel(model string) string {
	intent := reasoning.ResolveClaudeDefault(model, reasoning.Intent{})
	if !intent.HasStrength() {
		return string(reasoning.EffortNone)
	}
	return string(reasoning.EffectiveEffort(intent))
}

// ClaudeDefaultEffort is the effort a Claude model thinks at when a request
// turns thinking on without naming one.
func ClaudeDefaultEffort(model string) string {
	return string(reasoning.ClaudeDefaultEffort(model))
}

// RenderClaudeThinking renders a level and its thinking budget for a Claude
// model. A model with adaptive thinking takes the level as its effort; a
// model with only manual thinking takes the budget, which RelayKit keeps at
// 1024 or more and below max_tokens, raising a max_tokens of 1024 or less.
// The intent asks for adaptive thinking because RelayKit then picks adaptive
// thinking wherever the model has it, Claude 4.6 included, and manual
// thinking with the budget elsewhere.
//
// Level none turns thinking off, or to the model's lowest setting when it
// cannot be turned off; the request's own effort, clientEffort, is then kept
// at a level the model accepts while thinking is off. maxTokens is the
// request's max_tokens; include mirrors thinking.display. An empty Effort in
// the result means the model takes no effort here.
func RenderClaudeThinking(model, level string, budget int, clientEffort string, maxTokens *int, include *bool) (ClaudeThinking, error) {
	effort, err := reasoning.ParseEffort(level)
	if err != nil {
		return ClaudeThinking{}, err
	}
	intent := reasoning.Intent{Mode: reasoning.ModeAdaptive, Effort: effort, BudgetTokens: &budget, IncludeThoughts: include}
	if effort == reasoning.EffortNone {
		intent = reasoning.Intent{Mode: reasoning.ModeDisabled, IncludeThoughts: include}
		if requested, err := reasoning.ParseEffort(clientEffort); err == nil && requested != "" && requested != reasoning.EffortNone {
			intent.Effort = requested
		}
	}
	var limit *uint
	if maxTokens != nil && *maxTokens > 0 {
		value := uint(*maxTokens)
		limit = &value
	}
	rendered, err := reasoning.RenderClaude(model, intent, limit, 0)
	if err != nil {
		return ClaudeThinking{}, err
	}
	result := ClaudeThinking{
		Effort:            string(rendered.OutputEffort),
		ClearSampling:     rendered.ClearSampling,
		ConstrainSampling: rendered.ConstrainThinkingSampling,
		Level:             string(rendered.EffectiveEffort),
	}
	if result.Level == "" {
		result.Level = string(effort)
	}
	if rendered.Thinking != nil {
		result.Type = rendered.Thinking.Type
		result.BudgetTokens = rendered.Thinking.BudgetTokens
	}
	if rendered.MaxTokens != nil && *rendered.MaxTokens <= math.MaxInt32 {
		raised := int(*rendered.MaxTokens)
		result.MaxTokens = &raised
	}
	return result, nil
}

// GeminiThinking is a Gemini thinkingConfig strength: a budget on models
// that take one, or a thinking level.
type GeminiThinking struct {
	Budget *int
	Level  string
}

// RenderGeminiThinking renders a level and its thinking budget for a Gemini
// model: the budget, clamped to the model's range, on a model that takes
// one, and a thinking level on the rest, where xhigh and max become high.
// Level none turns thinking off, or to the model's lowest setting. It
// reports false when RelayKit has no thinking capabilities for the model.
func RenderGeminiThinking(model, level string, budget int) (GeminiThinking, bool, error) {
	effort, err := reasoning.ParseEffort(level)
	if err != nil {
		return GeminiThinking{}, false, err
	}
	intent := reasoning.Intent{Mode: reasoning.ModeEnabled, Effort: effort, BudgetTokens: &budget}
	if effort == reasoning.EffortNone {
		intent = reasoning.Intent{Mode: reasoning.ModeDisabled}
	}
	rendered, err := reasoning.RenderGemini(model, intent, nil, 0)
	if err != nil {
		return GeminiThinking{}, false, err
	}
	if rendered.Config == nil || (rendered.Config.ThinkingBudget == nil && rendered.Config.ThinkingLevel == "") {
		return GeminiThinking{}, false, nil
	}
	return GeminiThinking{Budget: rendered.Config.ThinkingBudget, Level: rendered.Config.ThinkingLevel}, true, nil
}

// GeminiDefaultLevel is the level a Gemini model thinks at when a request
// sets no thinkingConfig. It reports false when RelayKit lists no default.
func GeminiDefaultLevel(model string) (string, bool) {
	effort := reasoning.EffectiveEffort(reasoning.ResolveGeminiDefault(model, reasoning.Intent{}))
	return string(effort), effort != ""
}

// OpenAIReasoningModel reports whether model is one of OpenAI's reasoning
// models, which take a reasoning effort, and the lowest effort it accepts:
// none for the models RelayKit lets use sampling while reasoning is off, which
// is their default; minimal for the first GPT-5 models; low for the rest.
// Chat and pro variants, and the o1 previews, take no effort or only one, so
// they are left out.
func OpenAIReasoningModel(model string) (string, bool) {
	model = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(model, "openai/")))
	if strings.Contains(model, "-chat") || strings.Contains(model, "-pro") ||
		strings.HasPrefix(model, "o1-mini") || strings.HasPrefix(model, "o1-preview") {
		return "", false
	}
	if dto.IsOpenAIReasoningOModel(model) {
		return string(reasoning.EffortLow), true
	}
	capabilities := dto.GetOpenAIChatCapabilities(model, "")
	if !dto.IsOpenAIGPT5Model(model) && !capabilities.UseMaxCompletionTokens {
		return "", false
	}
	switch {
	case capabilities.SupportsTemperature:
		return string(reasoning.EffortNone), true
	case firstGPT5Model(model):
		return string(reasoning.EffortMinimal), true
	default:
		return string(reasoning.EffortLow), true
	}
}

// OpenAIHighestEffort is the highest reasoning effort an OpenAI model takes
// when no Codex catalog lists its levels: xhigh for GPT-5.1 Codex Max and for
// GPT-5.2 and every later version, Codex variants included; high for the
// other GPT-5.1 models, the first GPT-5 models, the o-series and other names.
func OpenAIHighestEffort(model string) string {
	model = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(model, "openai/")))
	version, ok := strings.CutPrefix(model, "gpt-")
	if !ok {
		return string(reasoning.EffortHigh)
	}
	major, rest := leadingNumber(version)
	minor := 0
	if after, ok := strings.CutPrefix(rest, "."); ok {
		minor, rest = leadingNumber(after)
	}
	switch {
	case major > 5 || (major == 5 && minor >= 2):
		return string(reasoning.EffortXHigh)
	case major == 5 && minor == 1 && (rest == "-codex-max" || strings.HasPrefix(rest, "-codex-max-")):
		return string(reasoning.EffortXHigh)
	default:
		return string(reasoning.EffortHigh)
	}
}

// leadingNumber reads the decimal digits text starts with; it is 0 when
// there are none.
func leadingNumber(text string) (int, string) {
	number, index := 0, 0
	for ; index < len(text) && text[index] >= '0' && text[index] <= '9' && number < 1000; index++ {
		number = number*10 + int(text[index]-'0')
	}
	return number, text[index:]
}

func firstGPT5Model(model string) bool {
	for _, base := range []string{"gpt-5", "gpt-5-mini", "gpt-5-nano"} {
		if model == base {
			return true
		}
		if snapshot, ok := strings.CutPrefix(model, base+"-"); ok {
			if _, err := time.Parse(time.DateOnly, snapshot); err == nil {
				return true
			}
		}
	}
	return false
}

// OpenAISampling reports which sampling parameters an OpenAI model accepts at
// a reasoning effort: temperature, top_p, and logprobs with top_logprobs.
func OpenAISampling(model, effort string) (temperature, topP, logProbs bool) {
	model = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(model, "openai/")))
	capabilities := dto.GetOpenAIChatCapabilities(model, effort)
	return capabilities.SupportsTemperature, capabilities.SupportsTopP, capabilities.SupportsLogProbs
}
