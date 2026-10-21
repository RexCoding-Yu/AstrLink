package contract_test

import (
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestCopilotSubscriptionProviderMapsToKindAndDeviceCodeOnly(t *testing.T) {
	t.Parallel()
	provider := contract.SubscriptionProviderGitHubCopilot
	kind := contract.ServiceKindCopilotSubscription
	if !provider.Valid() || provider.ServiceKind() != kind || kind.SubscriptionProvider() != provider ||
		!kind.IsSubscription() || kind.IsHTTP() {
		t.Fatal("copilot provider and kind are not linked")
	}
	if !contract.AuthorizationFlowDeviceCode.SupportedBy(provider) ||
		contract.AuthorizationFlowBrowser.SupportedBy(provider) ||
		contract.AuthorizationFlowCode.SupportedBy(provider) {
		t.Fatal("copilot must support only device_code")
	}
	protocols := map[contract.ProtocolID]bool{}
	for _, capability := range provider.Capabilities() {
		protocols[capability.Protocol] = capability.Mode == contract.CapabilityModeNative
	}
	for _, protocol := range []contract.ProtocolID{
		contract.ProtocolAnthropicMessages, contract.ProtocolOpenAIResponses,
		contract.ProtocolOpenAIChat, contract.ProtocolOpenAIModels,
	} {
		if !protocols[protocol] {
			t.Fatalf("capabilities = %#v", provider.Capabilities())
		}
	}
	now := time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	enabled := true
	service := contract.Service{
		ID: "service_copilot_01", Name: "Copilot", Kind: kind, Enabled: true,
		Models: []string{}, Capabilities: provider.Capabilities(), ResponsesWebSocketEnabled: &enabled,
		Subscription: &contract.SubscriptionConnection{Provider: provider, Status: contract.SubscriptionStatusDisconnected},
		CreatedAt:    now, UpdatedAt: now,
	}
	if err := service.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	if service.ResponsesWebSocket() {
		t.Fatal("Copilot must keep Responses on HTTP")
	}
	service.Capabilities = append(service.Capabilities, contract.Capability{
		Protocol: contract.ProtocolGoogleGenerateContent, Mode: contract.CapabilityModeNative,
		ConvertTo: contract.ProtocolAnthropicMessages, Streaming: true,
	})
	if err := service.Validate(); err != nil {
		t.Fatalf("Validate() rejected a conversion into Messages: %v", err)
	}
	service.Subscription.Provider = contract.SubscriptionProviderXAIGrok
	if err := service.Validate(); err == nil {
		t.Fatal("Validate() accepted a mismatched provider")
	}
}

func TestCopilotModelsUseTheirPreferredNativeProtocol(t *testing.T) {
	t.Parallel()
	for model, want := range map[string]contract.ProtocolID{
		"claude-sonnet-4.6": contract.ProtocolAnthropicMessages,
		"Claude-Opus-5.5":   contract.ProtocolAnthropicMessages,
		"gpt-5.4":           contract.ProtocolOpenAIResponses,
		"gpt-5-mini":        contract.ProtocolOpenAIResponses,
		"gpt-5.3-codex":     contract.ProtocolOpenAIResponses,
		"o4-mini":           contract.ProtocolOpenAIResponses,
		"gpt-4.1":           contract.ProtocolOpenAIChat,
		"gemini-3.1-pro":    contract.ProtocolOpenAIChat,
		"grok-code-fast-1":  contract.ProtocolOpenAIChat,
		"oswe-vscode-prime": contract.ProtocolOpenAIChat,
	} {
		if got := contract.ServiceKindCopilotSubscription.BuiltinModelProtocol(model); got != want {
			t.Errorf("BuiltinModelProtocol(%q) = %q, want %q", model, got, want)
		}
	}
}
