package relaykitbridge

import "testing"

func TestOpenAIHighestEffort(t *testing.T) {
	for model, want := range map[string]string{
		"gpt-5.1-codex-max":            "xhigh",
		"gpt-5.1-codex-max-2025-12-04": "xhigh",
		"gpt-5.2":                      "xhigh",
		"gpt-5.2-codex":                "xhigh",
		"gpt-5.3-codex":                "xhigh",
		"gpt-5.1":                      "high",
		"gpt-5.1-2025-11-13":           "high",
		"gpt-5.1-codex":                "high",
		"gpt-5.1-codex-mini":           "high",
		"gpt-5.1-codex-maximal":        "high",
		"gpt-5.4":                      "xhigh",
		"gpt-5.4-mini":                 "xhigh",
		"openai/GPT-5.5":               "xhigh",
		"gpt-5.10":                     "xhigh",
		"gpt-6":                        "xhigh",
		"gpt-6.1-mini":                 "xhigh",
		"gpt-5":                        "high",
		"gpt-5-2025-08-07":             "high",
		"gpt-5-mini":                   "high",
		"gpt-5-nano":                   "high",
		"gpt-5-codex":                  "high",
		"gpt-5.0":                      "high",
		"gpt-4.1":                      "high",
		"o3":                           "high",
		"o4-mini":                      "high",
		"codex-mini-latest":            "high",
		"gpt-":                         "high",
		"gpt-x.9":                      "high",
		"":                             "high",
	} {
		if got := OpenAIHighestEffort(model); got != want {
			t.Errorf("OpenAIHighestEffort(%q) = %q, want %q", model, got, want)
		}
	}
}
