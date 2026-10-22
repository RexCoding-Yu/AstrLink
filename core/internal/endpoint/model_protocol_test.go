package endpoint

import (
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func modelProtocolService() contract.Service {
	return contract.Service{
		ID: "service_newapi", Name: "New API", Kind: contract.ServiceKindNewAPI, Enabled: true,
		Models:         []string{"gemini-x", "gpt-x"},
		ModelRedirects: []contract.ModelRedirect{{From: "gemini-alias", To: "gemini-x", Enabled: true}},
		ModelProtocols: []contract.ModelProtocol{{Model: "gemini-x", Protocol: contract.ProtocolGoogleGenerateContent}},
		Capabilities: []contract.Capability{
			{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true},
			{Protocol: contract.ProtocolOpenAIResponsesCompact, Mode: contract.CapabilityModeNative},
			{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true, ConvertTo: contract.ProtocolOpenAIResponses},
			{Protocol: contract.ProtocolGoogleGenerateContent, Mode: contract.CapabilityModeNative, Streaming: true},
		},
		HTTP: &contract.HTTPConnection{BaseURL: "https://newapi.example", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeBearer}},
	}
}

var modelProtocolRuntime = contract.RuntimeProfile{RelayKitAvailable: true, Edges: []contract.ConversionEdge{
	{From: contract.ProtocolOpenAIResponses, To: contract.ProtocolGoogleGenerateContent, Streaming: true},
	{From: contract.ProtocolOpenAIChat, To: contract.ProtocolGoogleGenerateContent, Streaming: true},
	{From: contract.ProtocolOpenAIChat, To: contract.ProtocolOpenAIResponses, Streaming: true},
	{From: contract.ProtocolAnthropicMessages, To: contract.ProtocolOpenAIResponses, Streaming: true},
}}

// A pinned model is called in its format from every chat entry, after the
// provider's redirects; an entry already in the format passes through, and
// models left on auto keep each row's own choice.
func TestModelProtocolDecidesTheUpstreamFormat(t *testing.T) {
	service := modelProtocolService()
	if err := service.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		entry    contract.ProtocolID
		model    string
		upstream contract.ProtocolID
		plan     contract.PlanType
	}{
		{contract.ProtocolOpenAIResponses, "gemini-x", contract.ProtocolGoogleGenerateContent, contract.PlanTypeRelayKit},
		{contract.ProtocolOpenAIChat, "gemini-x", contract.ProtocolGoogleGenerateContent, contract.PlanTypeRelayKit},
		{contract.ProtocolOpenAIResponses, "gemini-alias", contract.ProtocolGoogleGenerateContent, contract.PlanTypeRelayKit},
		{contract.ProtocolGoogleGenerateContent, "gemini-x", contract.ProtocolGoogleGenerateContent, ""},
		// Auto: the Chat row's own conversion runs at execution, as before.
		{contract.ProtocolOpenAIChat, "gpt-x", contract.ProtocolOpenAIChat, ""},
		{contract.ProtocolOpenAIResponses, "gpt-x", contract.ProtocolOpenAIResponses, ""},
	} {
		request := ResolveRequest{Protocol: test.entry, Model: test.model, Streaming: true}
		candidates := defaultCandidates([]contract.Service{service}, request, "", modelProtocolRuntime)
		if len(candidates) != 1 || candidates[0].UpstreamProtocol != test.upstream || candidates[0].PlanType != test.plan {
			t.Fatalf("%s %s: %#v", test.entry, test.model, candidates)
		}
	}
	// Without a converter the pinned model is skipped instead of sent as is.
	request := ResolveRequest{Protocol: contract.ProtocolOpenAIChat, Model: "gemini-x", Streaming: true}
	if _, skip := planService(service, request, "", contract.RuntimeProfile{}); skip != contract.RoutingSkipConversionUnavailable {
		t.Fatalf("no converter skip = %q", skip)
	}
}

// The format governs chat entries only; compaction keeps its own row.
func TestModelProtocolLeavesOtherEntryProtocolsToTheirRows(t *testing.T) {
	service := modelProtocolService()
	service.ModelProtocols = []contract.ModelProtocol{{Model: "gpt-x", Protocol: contract.ProtocolOpenAIResponses}}
	request := ResolveRequest{Protocol: contract.ProtocolOpenAIResponsesCompact, Model: "gpt-x"}
	candidates := defaultCandidates([]contract.Service{service}, request, "", modelProtocolRuntime)
	if len(candidates) != 1 || candidates[0].UpstreamProtocol != contract.ProtocolOpenAIResponsesCompact || candidates[0].PlanType != "" {
		t.Fatalf("compaction: %#v", candidates)
	}
}

// A provider's own entry replaces its kind's built-in format for that model.
func TestModelProtocolOverridesTheBuiltinFormat(t *testing.T) {
	service := contract.Service{
		ID: "service_opencode", Kind: contract.ServiceKindOpenCodeGo, Enabled: true,
		Models:         []string{"kimi-k3"},
		ModelProtocols: []contract.ModelProtocol{{Model: "kimi-k3", Protocol: contract.ProtocolOpenAIResponses}},
		Capabilities:   []contract.Capability{{Protocol: contract.ProtocolAnthropicMessages, Mode: contract.CapabilityModeNative, Streaming: true}},
	}
	request := ResolveRequest{Protocol: contract.ProtocolAnthropicMessages, Model: "kimi-k3", Streaming: true}
	candidates := defaultCandidates([]contract.Service{service}, request, "", modelProtocolRuntime)
	if len(candidates) != 1 || candidates[0].UpstreamProtocol != contract.ProtocolOpenAIResponses {
		t.Fatalf("override: %#v", candidates)
	}
}
