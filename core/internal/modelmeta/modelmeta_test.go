package modelmeta

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestParseReadsWhatTheCatalogSays(t *testing.T) {
	got, err := Parse([]byte(`{
"openai":{"models":{
  "gpt-5.6":{"name":"GPT-5.6","reasoning":true,"reasoning_options":[{"type":"effort","values":["none","low","medium","high","xhigh","max"]}],"limit":{"context":1050000,"input":922000,"output":128000},"modalities":{"input":["text","image","pdf"],"output":["text"]},"tool_call":true},
  "o3":{"reasoning":true,"reasoning_options":[{"type":"effort","values":["low","medium","high"]}]},
  "gpt-4o":{"name":"GPT-4o","reasoning":false,"limit":{"context":128000,"output":16384},"modalities":{"input":["text","image","smell"]}}}},
"anthropic":{"models":{
  "claude-haiku-5-5":{"reasoning":true,"reasoning_options":[{"type":"toggle"},{"type":"effort","values":["low","medium","high","xhigh","max"]}]},
  "claude-opus-4-6":{"reasoning":true,"reasoning_options":[{"type":"effort","values":["low","medium","high","max"]},{"type":"budget_tokens","min":1024}]},
  "claude-sonnet-4-5":{"reasoning":true,"reasoning_options":[{"type":"budget_tokens","min":1024}]},
  "claude-odd":{"name":7,"reasoning":true,"reasoning_options":{"type":"effort"},"limit":{"context":"big"},"tool_call":"yes"},
  "claude-odder":{"reasoning":"yes","reasoning_options":[{"type":"effort","values":[null,"high"]}]},
  "claude-oddest":"not an object"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, provider := range []string{"anthropic", "openai"} {
		for _, model := range got[provider] {
			line, err := json.Marshal(model)
			if err != nil {
				t.Fatal(err)
			}
			lines = append(lines, string(line))
		}
	}
	want := []string{
		`{"provider":"anthropic","model":"claude-haiku-5-5","name":"","reasoning":true,"reasoning_levels":["none","low","medium","high","xhigh","max"],"context_tokens":0,"output_tokens":0,"input":[],"tool_call":false}`,
		`{"provider":"anthropic","model":"claude-odd","name":"","reasoning":true,"reasoning_levels":[],"context_tokens":0,"output_tokens":0,"input":[],"tool_call":false}`,
		`{"provider":"anthropic","model":"claude-odder","name":"","reasoning":false,"reasoning_levels":[],"context_tokens":0,"output_tokens":0,"input":[],"tool_call":false}`,
		`{"provider":"anthropic","model":"claude-oddest","name":"","reasoning":false,"reasoning_levels":[],"context_tokens":0,"output_tokens":0,"input":[],"tool_call":false}`,
		`{"provider":"anthropic","model":"claude-opus-4-6","name":"","reasoning":true,"reasoning_levels":["none","low","medium","high","max"],"context_tokens":0,"output_tokens":0,"input":[],"tool_call":false}`,
		`{"provider":"anthropic","model":"claude-sonnet-4-5","name":"","reasoning":true,"reasoning_levels":[],"context_tokens":0,"output_tokens":0,"input":[],"tool_call":false}`,
		`{"provider":"openai","model":"gpt-4o","name":"GPT-4o","reasoning":false,"reasoning_levels":[],"context_tokens":128000,"output_tokens":16384,"input":["text","image"],"tool_call":false}`,
		`{"provider":"openai","model":"gpt-5.6","name":"GPT-5.6","reasoning":true,"reasoning_levels":["none","low","medium","high","xhigh","max"],"context_tokens":1050000,"output_tokens":128000,"input":["text","image","pdf"],"tool_call":true}`,
		`{"provider":"openai","model":"o3","name":"","reasoning":true,"reasoning_levels":["low","medium","high"],"context_tokens":0,"output_tokens":0,"input":[],"tool_call":false}`,
	}
	if !slices.Equal(lines, want) {
		t.Fatalf("got\n%s", lines)
	}
	if _, err := Parse([]byte(`[]`)); err == nil {
		t.Fatal("accepted a catalog that is not an object")
	}
}
