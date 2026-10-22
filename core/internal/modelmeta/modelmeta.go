// Package modelmeta reads LLM Metadata (basellm.github.io/llm-metadata),
// AstrLink's source of first-party model facts: prices, reasoning levels,
// context and output limits, and input modalities. A fact about an official
// model comes from here, not from a list kept beside the code that needs it.
package modelmeta

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// SourceURL is the root of the published API.
const SourceURL = "https://basellm.github.io/llm-metadata/api"

// Source downloads files of the published API.
type Source struct {
	client *http.Client
	// Base is SourceURL outside tests.
	Base string
}

// NewSource reads SourceURL through client, or through a client with a 30
// second timeout when it is nil.
func NewSource(client *http.Client) *Source {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Source{client: client, Base: SourceURL}
}

// Get downloads one file. Given the ETag of a copy, it reports unchanged
// when the copy is current.
func (s *Source) Get(ctx context.Context, path, etag string) (body []byte, current string, unchanged bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.Base+"/"+path, nil)
	if err != nil {
		return nil, "", false, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", transport.SupportedResponseEncodings)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, "", false, fmt.Errorf("model metadata source unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return nil, etag, true, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", false, fmt.Errorf("model metadata source HTTP %d", resp.StatusCode)
	}
	body, err = transport.ReadResponseBody(resp, 8<<20)
	if err != nil {
		return nil, "", false, fmt.Errorf("invalid model metadata response")
	}
	return body, resp.Header.Get("ETag"), false, nil
}

// Model is what the catalog says about one first-party model.
type Model struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Name     string `json:"name"`
	// Reasoning reports whether the model reasons. ReasoningLevels are the
	// efforts it takes in RelayKit's vocabulary, weakest first; none means it
	// can also answer without reasoning. A model that reasons with no
	// strength to choose lists none.
	Reasoning       bool     `json:"reasoning"`
	ReasoningLevels []string `json:"reasoning_levels"`
	// ContextTokens and OutputTokens are 0 where the catalog gives no limit.
	ContextTokens int64 `json:"context_tokens"`
	OutputTokens  int64 `json:"output_tokens"`
	// Input lists what the model reads, among inputKinds.
	Input    []string `json:"input"`
	ToolCall bool     `json:"tool_call"`
}

// efforts is RelayKit's effort vocabulary, weakest first.
var efforts = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

var inputKinds = []string{"text", "image", "audio", "video", "pdf"}

// Parse reads all.json: each provider's models, sorted by ID. A field with
// an unexpected shape reads as unknown, so one odd entry never costs the
// rest.
func Parse(all []byte) (map[string][]Model, error) {
	var catalog map[string]struct {
		Models map[string]json.RawMessage `json:"models"`
	}
	if json.Unmarshal(all, &catalog) != nil {
		return nil, fmt.Errorf("invalid model catalog")
	}
	providers := make(map[string][]Model, len(catalog))
	for provider, entry := range catalog {
		models := []Model{}
		for id, raw := range entry.Models {
			if len(id) <= 256 {
				models = append(models, parseModel(provider, id, raw))
			}
		}
		sort.Slice(models, func(i, j int) bool { return models[i].Model < models[j].Model })
		providers[provider] = models
	}
	return providers, nil
}

type limits struct {
	Context int64 `json:"context"`
	Output  int64 `json:"output"`
}

type modalities struct {
	Input []any `json:"input"`
}

type control struct {
	Type   string `json:"type"`
	Values []any  `json:"values"`
}

func parseModel(provider, id string, raw json.RawMessage) Model {
	var fields struct {
		Name       json.RawMessage `json:"name"`
		Reasoning  json.RawMessage `json:"reasoning"`
		Options    json.RawMessage `json:"reasoning_options"`
		Limit      json.RawMessage `json:"limit"`
		Modalities json.RawMessage `json:"modalities"`
		ToolCall   json.RawMessage `json:"tool_call"`
	}
	_ = json.Unmarshal(raw, &fields)
	limit := read[limits](fields.Limit)
	model := Model{
		Provider: provider, Model: id, Name: read[string](fields.Name),
		Reasoning: read[bool](fields.Reasoning), ReasoningLevels: []string{},
		ContextTokens: max(limit.Context, 0), OutputTokens: max(limit.Output, 0),
		Input: []string{}, ToolCall: read[bool](fields.ToolCall),
	}
	if model.Reasoning {
		model.ReasoningLevels = reasoningLevels(read[[]control](fields.Options))
	}
	for _, kind := range read[modalities](fields.Modalities).Input {
		if kind, ok := kind.(string); ok && slices.Contains(inputKinds, kind) && !slices.Contains(model.Input, kind) {
			model.Input = append(model.Input, kind)
		}
	}
	return model
}

// reasoningLevels are the effort values, with none where a toggle or a
// thinking budget lets the model answer without reasoning too.
func reasoningLevels(controls []control) []string {
	values := map[string]bool{}
	optional := false
	for _, control := range controls {
		switch control.Type {
		case "effort":
			for _, value := range control.Values {
				if level, ok := value.(string); ok {
					values[level] = true
				}
			}
		case "toggle", "budget_tokens":
			optional = true
		}
	}
	levels := []string{}
	for _, level := range efforts[1:] {
		if values[level] {
			levels = append(levels, level)
		}
	}
	if len(levels) > 0 && (values["none"] || optional) {
		levels = append([]string{"none"}, levels...)
	}
	return levels
}

// read decodes raw, or gives T's zero value when raw is missing or of
// another shape.
func read[T any](raw json.RawMessage) T {
	var value T
	if json.Unmarshal(raw, &value) != nil {
		var zero T
		return zero
	}
	return value
}
