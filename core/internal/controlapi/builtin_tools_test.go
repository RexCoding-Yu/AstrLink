package controlapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestBuiltinToolConfigurationAndCredentials(t *testing.T) {
	store, handler := newRouteHandler(t)
	path := BuiltinToolsPath + "web_search/credential"
	w := controlRequest(t, handler, http.MethodPut, path, "application/json", `{"secret":"private-test-key"}`, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-test-key") {
		t.Fatal(w.Code, w.Body.String())
	}
	value, err := store.Get(context.Background(), secretstore.Ref("local://builtin-tool/web_search"))
	if err != nil || string(value) != "private-test-key" {
		t.Fatal(err)
	}
	clear(value)
	config := `{"builtin_tools":{"web_search":{"enabled":true,"backend":"external","base_url":"https://search.example"},"image_generation":{"enabled":false,"backend":"upstream"}}}`
	w = controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", config, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = controlRequest(t, handler, http.MethodGet, RoutingSettingsPath, "", "", "")
	if strings.Contains(w.Body.String(), "private-test-key") || !strings.Contains(w.Body.String(), "builtin_tools") {
		t.Fatal(w.Body.String())
	}
	w = controlRequest(t, handler, http.MethodDelete, path, "", "", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = controlRequest(t, handler, http.MethodGet, path, "", "", "")
	if !strings.Contains(w.Body.String(), `"configured":false`) {
		t.Fatal(w.Body.String())
	}
	for _, input := range []string{`{"builtin_tools":{"web_search":{"enabled":true,"backend":"external"},"image_generation":{"enabled":false,"backend":""}}}`, `{"builtin_tools":{"web_search":{"enabled":false,"backend":"upstream","secret":"no"},"image_generation":{"enabled":false,"backend":""}}}`} {
		w = controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", input, "")
		if w.Code != 422 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestImageGenerationNeedsAProviderWithAnImagesAPI(t *testing.T) {
	store, handler := newRouteHandler(t)
	ctx := context.Background()
	for _, service := range []contract.Service{
		{ID: "service_images", Name: "Images", Kind: contract.ServiceKindOpenAI, Enabled: true, Models: []string{"gpt-image-2"}, HTTP: &contract.HTTPConnection{BaseURL: "https://images.example/v1", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeNone}}, Capabilities: []contract.Capability{{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true}}},
		{ID: "service_codex", Name: "Team", Kind: contract.ServiceKindCodexSubscription, Enabled: true, Models: []string{}, Capabilities: contract.DefaultOpenAICodexCapabilities(), Subscription: &contract.SubscriptionConnection{Provider: contract.SubscriptionProviderOpenAICodex, Status: contract.SubscriptionStatusDisconnected}},
	} {
		if _, err := store.CreateService(ctx, service, storage.CredentialMutation{}); err != nil {
			t.Fatal(err)
		}
	}
	patch := func(body string) (int, string) {
		w := controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", body, "")
		return w.Code, w.Body.String()
	}
	images := func(enabled, service string) string {
		return `{"builtin_tools":{"web_search":{"enabled":false,"backend":"upstream"},"image_generation":{"enabled":` + enabled + `,"backend":"service_images","service_id":"` + service + `","model":"gpt-image-2"}}}`
	}
	// The desktop words each refusal by its code.
	for service, want := range map[string]string{
		"service_codex":   `"code":"image_provider_unsupported"`,
		"service_missing": `"code":"image_provider_not_found"`,
	} {
		if code, body := patch(images("true", service)); code != http.StatusUnprocessableEntity || !strings.Contains(body, want) {
			t.Fatalf("%s => %d %s", service, code, body)
		}
	}
	if code, body := patch(images("true", "service_images")); code != http.StatusOK {
		t.Fatalf("Images provider => %d %s", code, body)
	}

	// A Codex choice saved before this check blocks no other setting, and it
	// can be turned off without picking another provider.
	settings, err := store.GetRoutingSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.BuiltinTools.ImageGeneration.ServiceID = "service_codex"
	if err := store.UpdateRoutingSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if code, body := patch(`{"allow_unmatched_failover":false}`); code != http.StatusOK {
		t.Fatalf("unrelated change => %d %s", code, body)
	}
	if code, body := patch(images("false", "service_codex")); code != http.StatusOK {
		t.Fatalf("turn off => %d %s", code, body)
	}
}

func TestWebSearchNeedsAProviderWithASearchAPI(t *testing.T) {
	store, handler := newRouteHandler(t)
	ctx := context.Background()
	for _, service := range []contract.Service{
		{ID: "service_images", Name: "Images", Kind: contract.ServiceKindOpenAI, Enabled: true, Models: []string{"gpt-5.5"}, HTTP: &contract.HTTPConnection{BaseURL: "https://images.example/v1", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeNone}}, Capabilities: []contract.Capability{{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true}}},
		{ID: "service_newapi", Name: "Gateway", Kind: contract.ServiceKindNewAPI, Enabled: true, Models: []string{"gpt-5.5"}, HTTP: &contract.HTTPConnection{BaseURL: "https://gateway.example", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeNone}}, Capabilities: []contract.Capability{{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true}}},
		{ID: "service_codex", Name: "Team", Kind: contract.ServiceKindCodexSubscription, Enabled: true, Models: []string{}, Capabilities: contract.DefaultOpenAICodexCapabilities(), Subscription: &contract.SubscriptionConnection{Provider: contract.SubscriptionProviderOpenAICodex, Status: contract.SubscriptionStatusDisconnected}},
	} {
		if _, err := store.CreateService(ctx, service, storage.CredentialMutation{}); err != nil {
			t.Fatal(err)
		}
	}
	search := func(enabled, service string) (int, string) {
		body := `{"builtin_tools":{"web_search":{"enabled":` + enabled + `,"backend":"service_search","service_id":"` + service + `","model":"gpt-5.5"},"image_generation":{"enabled":false,"backend":"upstream"}}}`
		w := controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", body, "")
		return w.Code, w.Body.String()
	}
	// The desktop words each refusal by its code.
	for service, want := range map[string]string{
		"service_images":  `"code":"search_provider_unsupported"`,
		"service_missing": `"code":"search_provider_not_found"`,
	} {
		if code, body := search("true", service); code != http.StatusUnprocessableEntity || !strings.Contains(body, want) {
			t.Fatalf("%s => %d %s", service, code, body)
		}
	}
	for _, service := range []string{"service_newapi", "service_codex"} {
		if code, body := search("true", service); code != http.StatusOK {
			t.Fatalf("%s => %d %s", service, code, body)
		}
	}
	if code, body := search("false", "service_images"); code != http.StatusOK {
		t.Fatalf("turned off => %d %s", code, body)
	}
	// The search API is a web search backend only.
	body := `{"builtin_tools":{"web_search":{"enabled":false,"backend":"upstream"},"image_generation":{"enabled":false,"backend":"service_search"}}}`
	if w := controlRequest(t, handler, http.MethodPatch, RoutingSettingsPath, "application/merge-patch+json", body, ""); w.Code != http.StatusUnprocessableEntity {
		t.Fatal(w.Code, w.Body.String())
	}
}
