package controlapi

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
)

// relayPlugin is a new-api plugin with only a provider part: chat natively
// for its own models, converted to Claude Messages for the others, and a
// route the plugin's hooks would convert, which AstrLink leaves out.
const relayPlugin = `export const meta = {
  apiVersion: 1, key: "vendor", name: { en: "Vendor", "zh-CN": "供应商" }, version: "1.0.0", author: { name: "Test" }, baseUrl: "https://api.vendor.example",
  models: ["vendor-large"],
  relay: [
    { incoming_path: "/v1/chat/completions", upstream_path: "/v1/chat/completions", models: ["re:^vendor-"] },
    { incoming_path: "/v1/chat/completions", upstream_path: "/v1/messages", converter: "openai_chat_completions_to_anthropic_messages" },
    { incoming_path: "/v1/messages", upstream_path: "/v1/messages" },
    { incoming_path: "/v1/embeddings", upstream_path: "/embed", converter: "plugin" },
    { incoming_path: "/v1/audio/speech", upstream_path: "/speech" },
  ],
};
export function convertRequest() {} export function convertResponse() {} export function convertStreamEvent() {}
`

func TestAProviderPluginOffersARelayProvider(t *testing.T) {
	_, handler, manager, directory := newPluginHandler(t)
	handler.capabilities.ConversionEngine = relaykitbridge.Descriptor(relaykitbridge.NewEngine())
	writePlugin(t, directory, "vendor", "plugin.js", relayPlugin)
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if response := approvedRequest(t, handler, http.MethodPatch, PluginsPath+"/vendor", "application/json", `{"enabled":true}`, ""); response.Code != http.StatusOK {
		t.Fatalf("enable = %d %s", response.Code, response.Body)
	}
	detail := decodeBody[contract.PluginDetail](t, controlRequest(t, handler, http.MethodGet, PluginsPath+"/vendor", "", "", ""))
	if detail.Kind != contract.PluginKindProvider || len(detail.Providers) != 1 {
		t.Fatalf("detail = %+v", detail.PluginSummary)
	}
	offered := detail.Providers[0]
	protocols := []contract.ProtocolID{}
	for _, protocol := range offered.Protocols {
		protocols = append(protocols, protocol.Protocol)
	}
	if offered.ID != contract.PluginProviderRelay || offered.BaseURL != "https://api.vendor.example" || offered.Auth.Scheme != contract.AuthSchemeBearer ||
		!slices.Equal(protocols, []contract.ProtocolID{contract.ProtocolOpenAIChat, contract.ProtocolAnthropicMessages}) {
		t.Fatalf("provider = %+v", offered)
	}
	// A provider of it takes its capabilities from the routes, whatever the
	// request says.
	service := createServiceForTest(t, handler,
		`{"name":"Vendor","kind":"plugin","plugin":{"key":"vendor","provider":"relay"},"http":{"base_url":"","auth":{"scheme":"bearer"},"credential":{"secret":"sk-test"}},"capabilities":[{"protocol":"openai.images","mode":"native","streaming":false}]}`)
	want := []contract.Capability{
		{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true},
		{Protocol: contract.ProtocolAnthropicMessages, Mode: contract.CapabilityModeNative, Streaming: true},
	}
	if !slices.Equal(service.Capabilities, want) || service.HTTP.BaseURL != "https://api.vendor.example" || service.HTTP.Auth.Scheme != contract.AuthSchemeBearer {
		t.Fatalf("service = %+v %+v", service.Capabilities, service.HTTP)
	}
	if response := serviceRequestForTest(t, handler, http.MethodPost, ServicesPath, "application/json",
		`{"name":"Vendor","kind":"plugin","plugin":{"key":"vendor","provider":"relay","upstream":"new_api"},"http":{"base_url":"","auth":{"scheme":"none"}},"capabilities":[]}`, ""); response.Code < 400 {
		t.Fatalf("a relay provider through another gateway = %d", response.Code)
	}
}
