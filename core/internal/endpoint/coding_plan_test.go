package endpoint

import (
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestOpenCodeSelectsNativeProtocolByModel(t *testing.T) {
	for _, test := range []struct {
		kind     contract.ServiceKind
		model    string
		protocol contract.ProtocolID
	}{
		{contract.ServiceKindOpenCodeGo, "gpt-5.6-luna", contract.ProtocolOpenAIResponses},
		{contract.ServiceKindOpenCodeGo, "kimi-k3", contract.ProtocolOpenAIChat},
		{contract.ServiceKindOpenCodeGo, "minimax-m3", contract.ProtocolAnthropicMessages},
		{contract.ServiceKindOpenCodeZen, "minimax-m3", contract.ProtocolOpenAIChat},
		{contract.ServiceKindOpenCodeZen, "claude-sonnet-4-5", contract.ProtocolAnthropicMessages},
		{contract.ServiceKindOpenCodeGo, "qwen3.8-max", contract.ProtocolAnthropicMessages},
		{contract.ServiceKindOpenCodeGo, "gemini-4-argon-contributor", contract.ProtocolGoogleGenerateContent},
		{contract.ServiceKindOpenCodeZen, "gemini-3.1-pro", contract.ProtocolGoogleGenerateContent},
	} {
		t.Run(string(test.kind)+"/"+test.model, func(t *testing.T) {
			service := contract.Service{ID: "service_opencode", Name: "OpenCode", Kind: test.kind, Enabled: true, Models: []string{test.model},
				HTTP: &contract.HTTPConnection{BaseURL: "https://opencode.ai/zen/go/v1", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeBearer}},
				Capabilities: []contract.Capability{
					{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true},
					{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true},
					{Protocol: contract.ProtocolAnthropicMessages, Mode: contract.CapabilityModeNative, Streaming: true},
				}}
			request := ResolveRequest{Protocol: contract.ProtocolAnthropicMessages, Model: test.model, Streaming: true}
			runtime := contract.RuntimeProfile{RelayKitAvailable: true, Edges: []contract.ConversionEdge{{From: request.Protocol, To: test.protocol, Streaming: true}}}
			candidates := defaultCandidates([]contract.Service{service}, request, "", runtime)
			if len(candidates) != 1 || candidates[0].UpstreamProtocol != test.protocol {
				t.Fatalf("wrong model protocol: %#v", candidates)
			}
			if test.protocol != request.Protocol {
				if candidates[0].PlanType != contract.PlanTypeRelayKit {
					t.Fatal("missing conversion plan")
				}
				if candidates := defaultCandidates([]contract.Service{service}, request, ""); len(candidates) != 0 {
					t.Fatal("routed incompatible protocol without conversion engine")
				}
			}
		})
	}
}

// The entry rows only choose which clients may enter: a model is called in
// its format whether or not a row declares that protocol.
func TestOpenCodeModelFormatNeedsNoCapabilityRow(t *testing.T) {
	service := contract.Service{
		ID: "service_opencode", Kind: contract.ServiceKindOpenCodeGo, Enabled: true,
		Models: []string{"kimi-k3", "gemini-4-argon-contributor"},
		Capabilities: []contract.Capability{
			{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true},
			{Protocol: contract.ProtocolAnthropicMessages, Mode: contract.CapabilityModeNative, Streaming: true, ConvertTo: contract.ProtocolOpenAIResponses},
		},
	}
	runtime := contract.RuntimeProfile{RelayKitAvailable: true, Edges: []contract.ConversionEdge{
		{From: contract.ProtocolAnthropicMessages, To: contract.ProtocolOpenAIChat, Streaming: true},
		{From: contract.ProtocolOpenAIResponses, To: contract.ProtocolGoogleGenerateContent, Streaming: true},
	}}
	for _, test := range []struct {
		entry    contract.ProtocolID
		model    string
		upstream contract.ProtocolID
	}{
		// The Messages row's own conversion gives way to the model's format.
		{contract.ProtocolAnthropicMessages, "kimi-k3", contract.ProtocolOpenAIChat},
		{contract.ProtocolOpenAIResponses, "gemini-4-argon-contributor", contract.ProtocolGoogleGenerateContent},
	} {
		request := ResolveRequest{Protocol: test.entry, Model: test.model, Streaming: true}
		candidates := defaultCandidates([]contract.Service{service}, request, "", runtime)
		if len(candidates) != 1 || candidates[0].UpstreamProtocol != test.upstream || candidates[0].PlanType != contract.PlanTypeRelayKit {
			t.Fatalf("%s %s: %#v", test.entry, test.model, candidates)
		}
	}
	chat := ResolveRequest{Protocol: contract.ProtocolOpenAIChat, Model: "kimi-k3", Streaming: true}
	if candidates := defaultCandidates([]contract.Service{service}, chat, "", runtime); len(candidates) != 0 {
		t.Fatal("entered on a protocol without a row")
	}
}
