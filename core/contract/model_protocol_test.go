package contract_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

// A service's own entry replaces its kind's built-in format; a model with
// neither leaves each capability row to decide.
func TestServiceModelProtocolPrefersItsOwnEntry(t *testing.T) {
	t.Parallel()
	service := contract.Service{
		Kind: contract.ServiceKindOpenCodeGo,
		ModelProtocols: []contract.ModelProtocol{
			{Model: "kimi-k3", Protocol: contract.ProtocolOpenAIResponses},
		},
	}
	for model, want := range map[string]contract.ProtocolID{
		"kimi-k3":                    contract.ProtocolOpenAIResponses,
		"glm-5.3":                    contract.ProtocolOpenAIChat,
		"gemini-4-argon-contributor": contract.ProtocolGoogleGenerateContent,
		"minimax-m3":                 contract.ProtocolAnthropicMessages,
	} {
		if got := service.ModelProtocol(model); got != want {
			t.Errorf("ModelProtocol(%q) = %q, want %q", model, got, want)
		}
	}
	if got := contract.ServiceKindOpenCodeZen.BuiltinModelProtocol("minimax-m3"); got != contract.ProtocolOpenAIChat {
		t.Errorf("Zen minimax-m3 = %q", got)
	}
	newAPI := contract.Service{Kind: contract.ServiceKindNewAPI}
	if got := newAPI.ModelProtocol("gemini-x"); got != "" {
		t.Errorf("auto model = %q", got)
	}
}

func TestModelProtocolsAreValidatedForTheKind(t *testing.T) {
	t.Parallel()
	entry := func(model string, protocol contract.ProtocolID) contract.ModelProtocol {
		return contract.ModelProtocol{Model: model, Protocol: protocol}
	}
	for _, test := range []struct {
		kind    contract.ServiceKind
		entries []contract.ModelProtocol
		valid   bool
	}{
		{contract.ServiceKindNewAPI, nil, true},
		{contract.ServiceKindNewAPI, []contract.ModelProtocol{entry("gemini-x", contract.ProtocolGoogleGenerateContent), entry("gpt-x", contract.ProtocolOpenAIChat)}, true},
		{contract.ServiceKindCopilotSubscription, []contract.ModelProtocol{entry("gpt-5.4", contract.ProtocolOpenAIChat)}, true},
		{contract.ServiceKindNewAPI, []contract.ModelProtocol{entry("", contract.ProtocolOpenAIChat)}, false},
		{contract.ServiceKindNewAPI, []contract.ModelProtocol{entry(strings.Repeat("m", 257), contract.ProtocolOpenAIChat)}, false},
		{contract.ServiceKindNewAPI, []contract.ModelProtocol{entry("gpt-x", contract.ProtocolOpenAIChat), entry("gpt-x", contract.ProtocolOpenAIResponses)}, false},
		{contract.ServiceKindNewAPI, []contract.ModelProtocol{entry("gpt-x", contract.ProtocolOpenAIEmbeddings)}, false},
		{contract.ServiceKindNewAPI, []contract.ModelProtocol{entry("gpt-x", contract.ProtocolOpenAIResponsesCompact)}, false},
		// A subscription is called in its own wire formats only.
		{contract.ServiceKindCopilotSubscription, []contract.ModelProtocol{entry("gemini-3.1-pro", contract.ProtocolGoogleGenerateContent)}, false},
		{contract.ServiceKindCodexSubscription, []contract.ModelProtocol{entry("gpt-5.4", contract.ProtocolOpenAIChat)}, false},
	} {
		err := contract.ValidateModelProtocols(test.kind, test.entries)
		if (err == nil) != test.valid {
			t.Errorf("%s %v: err = %v, want valid=%t", test.kind, test.entries, err, test.valid)
		}
	}
	normalized, err := contract.NormalizeModelProtocols(contract.ServiceKindNewAPI, []contract.ModelProtocol{
		entry("zeta", contract.ProtocolOpenAIChat), entry("alpha", contract.ProtocolGoogleGenerateContent),
	})
	if err != nil || len(normalized) != 2 || normalized[0].Model != "alpha" || normalized[1].Model != "zeta" {
		t.Fatalf("normalized = %v, %v", normalized, err)
	}
	if empty, err := contract.NormalizeModelProtocols(contract.ServiceKindNewAPI, []contract.ModelProtocol{}); err != nil || empty != nil {
		t.Fatalf("empty table = %#v, %v", empty, err)
	}
}

func TestModelProtocolDecodesStrictly(t *testing.T) {
	t.Parallel()
	var entry contract.ModelProtocol
	if err := json.Unmarshal([]byte(`{"model":"gemini-x","protocol":"google.generate_content"}`), &entry); err != nil ||
		entry.Model != "gemini-x" || entry.Protocol != contract.ProtocolGoogleGenerateContent {
		t.Fatalf("entry = %#v, %v", entry, err)
	}
	for _, document := range []string{
		`{"model":"gemini-x"}`,
		`{"model":"gemini-x","protocol":null}`,
		`{"model":"gemini-x","protocol":"google.generate_content","enabled":true}`,
	} {
		if err := json.Unmarshal([]byte(document), &entry); err == nil {
			t.Errorf("accepted %s", document)
		}
	}
}
