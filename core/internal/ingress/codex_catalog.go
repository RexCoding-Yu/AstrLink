package ingress

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

// Codex's models endpoint shares the OpenAI path but uses a capability catalog,
// not data[].id. Match the original request before upstream identity overlays.
func wantsCodexModelCatalog(request *http.Request, protocol contract.ProtocolID) bool {
	return protocol == contract.ProtocolOpenAIModels &&
		(strings.TrimSpace(request.URL.Query().Get("client_version")) != "" ||
			detectClientType(request.Header) == contract.ClientCodex)
}

func codexCatalogDiscoveryEntries(list subscription.ModelList) ([]discoveryEntry, error) {
	entries := make([]discoveryEntry, 0, len(list.Data))
	for _, model := range list.Data {
		raw, err := json.Marshal(struct {
			ID string `json:"id"`
		}{ID: model.ID})
		if err != nil {
			return nil, err
		}
		entries = append(entries, discoveryEntry{id: model.ID, raw: raw, codexCatalog: model.CodexCatalog})
	}
	return entries, nil
}

func encodeCodexModelCatalog(entries []discoveryEntry) ([]byte, error) {
	models := make([]json.RawMessage, 0, len(entries))
	for _, entry := range entries {
		model, err := encodeCodexCatalogEntry(entry)
		if err != nil {
			return nil, err
		}
		models = append(models, model)
	}
	return json.Marshal(struct {
		Models []json.RawMessage `json:"models"`
	}{Models: models})
}

func encodeCodexCatalogEntry(entry discoveryEntry) ([]byte, error) {
	// Required ModelInfo fields for Codex 0.158.0. Unknown providers advertise
	// text and ordinary shell tools only, with no guessed context limit,
	// reasoning levels, image support, custom tools, or parallel tool calls.
	model := map[string]any{
		"slug":                         entry.id,
		"display_name":                 entry.id,
		"description":                  "",
		"visibility":                   "list",
		"supported_in_api":             true,
		"priority":                     0,
		"default_reasoning_level":      nil,
		"supported_reasoning_levels":   []any{},
		"shell_type":                   "shell_command",
		"base_instructions":            "You are a coding assistant.",
		"supports_reasoning_summaries": false,
		"support_verbosity":            false,
		"default_verbosity":            nil,
		"apply_patch_tool_type":        nil,
		"truncation_policy":            map[string]any{"mode": "tokens", "limit": 10000},
		"supports_parallel_tool_calls": false,
		"input_modalities":             []string{"text"},
		"experimental_supported_tools": []any{},
	}
	if entry.id == "codex-auto-review" {
		// Subscription discovery adds this internal model for routing, not for
		// the user's picker. An explicit upstream visibility still wins below.
		model["visibility"] = "hide"
	}
	if len(entry.codexCatalog) != 0 {
		var upstream map[string]json.RawMessage
		if err := json.Unmarshal(entry.codexCatalog, &upstream); err != nil {
			return nil, err
		}
		for key, value := range upstream {
			model[key] = value
		}
		var slug string
		_ = json.Unmarshal(upstream["slug"], &slug)
		if strings.TrimSpace(slug) != entry.id {
			// Redirect sources inherit their target's capabilities, while their
			// public name must remain selectable independently of the target.
			model["display_name"] = entry.id
		}
	}
	model["slug"] = entry.id
	return json.Marshal(model)
}
