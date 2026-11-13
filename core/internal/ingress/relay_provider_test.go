package ingress

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/pluginhost"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
	"github.com/QuantumNous/astrlink/core/internal/transport"
	"github.com/tidwall/gjson"
)

// vendorRelayPlugin is a new-api plugin with only a provider part: its own
// models go to its OpenAI-style path with its own key header, every other
// model is converted to Claude Messages, and prepareRequest and
// signRequest adjust each request.
const vendorRelayPlugin = `export const meta = {
  apiVersion: 1, key: "vendor", name: "Vendor", version: "1.0.0", author: { name: "Test" }, baseUrl: "https://api.vendor.example",
  models: ["vendor-large", "claude-x"],
  channelConfig: { type: "object", properties: { region: { type: "string", title: "Region", default: "us" } } },
  relay: [
    { incoming_path: "/v1/chat/completions", upstream_path: "/openai/{model}/chat", models: ["re:^vendor-"], auth: { type: "header", name: "X-Vendor-Key", value: "Key {api_key}" } },
    { incoming_path: "/v1/chat/completions", upstream_path: "/claude/v1/messages", converter: "openai_chat_completions_to_anthropic_messages", auth: { type: "header", name: "x-api-key", value: "{api_key}" } },
    { incoming_path: "/v1/models", upstream_path: "/openai/models", auth: { type: "query", name: "key", value: "{api_key}" } },
  ],
};
export function prepareRequest(ctx, request) {
  if (request.body.temperature === 9) return { effects: [{ type: "json.patch", ops: [{ op: "replace", path: "/model", value: "cheap" }] }] };
  return { effects: [
    { type: "json.patch", ops: [{ op: "add", path: "/region", value: ctx.config.region }, ...(request.body.metadata ? [{ op: "remove", path: "/metadata" }] : [])] },
    { type: "header.patch", set: { "X-Vendor-Beta": "2025-01" } },
  ] };
}
export function signRequest(ctx, request) {
  return { headers: { "X-Signature": request.bodySha256.slice(0, 8) + ":" + ctx.apiKey + ":" + request.method } };
}
`

type relayUpstream struct {
	server   *httptest.Server
	mutex    sync.Mutex
	requests []*http.Request
	bodies   []string
}

func newRelayUpstream(t *testing.T) *relayUpstream {
	upstream := &relayUpstream{}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		upstream.mutex.Lock()
		upstream.requests = append(upstream.requests, request.Clone(context.Background()))
		upstream.bodies = append(upstream.bodies, string(body))
		upstream.mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/base/openai/models":
			_, _ = io.WriteString(writer, `{"object":"list","data":[{"id":"vendor-large"},{"id":"vendor-mini"}]}`)
		case "/base/openai/vendor-large/chat":
			_, _ = io.WriteString(writer, `{"id":"c1","object":"chat.completion","model":"vendor-large","choices":[{"index":0,"message":{"role":"assistant","content":"native"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
		case "/base/claude/v1/messages":
			_, _ = io.WriteString(writer, `{"id":"m1","type":"message","role":"assistant","model":"claude-x","content":[{"type":"text","text":"converted"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(upstream.server.Close)
	return upstream
}

func (upstream *relayUpstream) last(t *testing.T) (*http.Request, string) {
	t.Helper()
	upstream.mutex.Lock()
	defer upstream.mutex.Unlock()
	if len(upstream.requests) == 0 {
		t.Fatal("the upstream got no request")
	}
	return upstream.requests[len(upstream.requests)-1], upstream.bodies[len(upstream.bodies)-1]
}

func newRelayGateway(t *testing.T) (*Handler, *pluginhost.Host, contract.Service, *relayUpstream) {
	return newRelayGatewayOf(t, vendorRelayPlugin, newRelayUpstream(t))
}

func newRelayGatewayOf(t *testing.T, source string, upstream *relayUpstream) (*Handler, *pluginhost.Host, contract.Service, *relayUpstream) {
	t.Helper()
	manager := plugin.NewManager(plugin.ManagerOptions{
		Builtin:  fstest.MapFS{"vendor/plugin.js": &fstest.MapFile{Data: []byte(source)}},
		Settings: pluginSettings{"vendor": plugin.Settings{Enabled: true}},
	})
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if loaded := manager.Current().Plugin("vendor"); !loaded.Loaded() || loaded.NewAPI() == nil {
		t.Fatalf("vendor did not load: %v", loaded.Err)
	}
	host := pluginhost.New(pluginhost.Options{Plugins: manager, Logf: t.Logf})
	t.Cleanup(host.Close)
	service := contract.Service{
		ID: "service_vendor", Name: "Vendor", Kind: contract.ServiceKindPlugin, Enabled: true, Models: []string{"vendor-large", "claude-x"},
		HTTP:         &contract.HTTPConnection{BaseURL: upstream.server.URL + "/base", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeBearer}, CredentialRef: "local://service/service_vendor"},
		Plugin:       &contract.ServicePlugin{Key: "vendor", Provider: contract.PluginProviderRelay, Settings: json.RawMessage(`{"region":"eu","stray":1}`)},
		Capabilities: []contract.Capability{{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true}},
	}
	if err := service.Validate(); err != nil {
		t.Fatal(err)
	}
	handler := NewWithDependencies(Dependencies{
		Resolver:         candidateResolver{candidates: []endpoint.Resolved{{Service: service, BaseURL: service.HTTP.BaseURL}}},
		Authorizer:       endpoint.NewServiceAuthorizer(codingPlanCredentials{}, nil),
		RequestRecords:   &memoryRequestRecordStore{},
		AuditBlobs:       &memoryAuditBlobs{},
		Plugins:          host,
		ConversionEngine: relaykitbridge.NewEngine(),
	})
	return handler, host, service, upstream
}

func relayChat(t *testing.T, handler *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer gateway-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestARelayProviderSendsEachModelOverItsRoute(t *testing.T) {
	handler, _, _, upstream := newRelayGateway(t)

	// The plugin's own model goes to its path with its key header, as the
	// client sent it, adjusted by prepareRequest and signed.
	response := relayChat(t, handler, `{"model":"vendor-large","messages":[{"role":"user","content":"hi"}],"metadata":{"user":"u1"}}`)
	if response.Code != http.StatusOK || gjson.Get(response.Body.String(), "choices.0.message.content").String() != "native" {
		t.Fatalf("native = %d %s", response.Code, response.Body)
	}
	sent, body := upstream.last(t)
	switch {
	case sent.URL.Path != "/base/openai/vendor-large/chat":
		t.Fatalf("native path = %s", sent.URL.Path)
	case sent.Header.Get("X-Vendor-Key") != "Key plan-key" || sent.Header.Get("Authorization") != "":
		t.Fatalf("native key headers = %v", sent.Header)
	case sent.Header.Get("X-Vendor-Beta") != "2025-01" || !strings.HasSuffix(sent.Header.Get("X-Signature"), ":plan-key:POST"):
		t.Fatalf("prepared and signed headers = %v", sent.Header)
	case gjson.Get(body, "region").String() != "eu" || gjson.Get(body, "metadata").Exists() || gjson.Get(body, "model").String() != "vendor-large":
		t.Fatalf("native body = %s", body)
	}
	// Every other model is converted into Claude Messages and sent with
	// the key as x-api-key; the answer comes back as a chat completion.
	response = relayChat(t, handler, `{"model":"claude-x","messages":[{"role":"user","content":"hi"}]}`)
	if response.Code != http.StatusOK || gjson.Get(response.Body.String(), "choices.0.message.content").String() != "converted" {
		t.Fatalf("converted = %d %s", response.Code, response.Body)
	}
	sent, body = upstream.last(t)
	switch {
	case sent.URL.Path != "/base/claude/v1/messages":
		t.Fatalf("converted path = %s", sent.URL.Path)
	case sent.Header.Get("X-Api-Key") != "plan-key" || sent.Header.Get("X-Vendor-Key") != "" || sent.Header.Get("Authorization") != "":
		t.Fatalf("converted key headers = %v", sent.Header)
	case gjson.Get(body, "messages.0.role").String() != "user" || gjson.Get(body, "region").String() != "eu" || gjson.Get(body, "max_tokens").Int() == 0:
		t.Fatalf("converted body = %s", body)
	}
	// A change to what the gateway bills by is refused, and not retried.
	before := len(upstream.requests)
	response = relayChat(t, handler, `{"model":"vendor-large","temperature":9,"messages":[{"role":"user","content":"hi"}]}`)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "model") || len(upstream.requests) != before {
		t.Fatalf("a billing change = %d %s, upstream requests %d → %d", response.Code, response.Body, before, len(upstream.requests))
	}
}

func TestARelayProviderListsItsModelsOverItsRoute(t *testing.T) {
	_, host, service, upstream := newRelayGateway(t)
	target := transport.Target{Service: service, RequestHeaders: http.Header{"Authorization": {"Bearer plan-key"}}}
	models, listed, err := host.ProviderModels(context.Background(), service, target, transport.New(nil).RoundTrip)
	if err != nil || !listed || strings.Join(models, ",") != "vendor-large,vendor-mini" {
		t.Fatalf("models = %v %v %v", models, listed, err)
	}
	sent, _ := upstream.last(t)
	if sent.URL.Path != "/base/openai/models" || sent.URL.Query().Get("key") != "plan-key" || sent.Header.Get("Authorization") != "" ||
		!strings.HasSuffix(sent.Header.Get("X-Signature"), ":plan-key:GET") {
		t.Fatalf("model list request = %s %v", sent.URL, sent.Header)
	}
	if upstream, ok := host.RelayUpstreamProtocol(service, "/v1/chat/completions", "claude-x"); !ok || upstream != contract.ProtocolAnthropicMessages {
		t.Fatalf("the route of claude-x sends %s %v", upstream, ok)
	}
}

// tokenVendorPlugin exchanges the key for a short-lived token, sends it
// signed, and takes a 403 that says expired for a rejected token.
const tokenVendorPlugin = `export const meta = {
  apiVersion: 1, key: "vendor", name: "Vendor", version: "1.0.0", author: { name: "Test" }, baseUrl: "https://api.vendor.example",
  models: ["vendor-large"],
  relay: [{ incoming_path: "/v1/chat/completions", upstream_path: "/token/chat", auth: { type: "none" } }],
};
export function exchangeKey(ctx) { return { token: { value: ctx.apiKey + ":" + utils.uuid() } }; }
export function signRequest(ctx, request) { return { headers: { "X-Token": ctx.token.value } }; }
export function credentialRejected(ctx, response) { return response.status === 403 && response.body.includes("expired"); }
export function checkKey(ctx, key) { return key.includes("|") ? undefined : { error: { en: "Write it as AccessKey|SecretKey" } }; }
`

func TestARelayProviderExchangesItsKeyAgainWhenTheTokenIsRejected(t *testing.T) {
	upstream := &relayUpstream{}
	tokens := []string{}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstream.mutex.Lock()
		tokens = append(tokens, request.Header.Get("X-Token"))
		first := len(tokens) == 1
		upstream.mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		if first {
			writer.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(writer, `{"error":"token expired"}`)
			return
		}
		_, _ = io.WriteString(writer, `{"id":"c1","object":"chat.completion","model":"vendor-large","choices":[{"index":0,"message":{"role":"assistant","content":"fresh"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(upstream.server.Close)
	handler, _, _, _ := newRelayGatewayOf(t, tokenVendorPlugin, upstream)
	response := relayChat(t, handler, `{"model":"vendor-large","messages":[{"role":"user","content":"hi"}]}`)
	if response.Code != http.StatusOK || gjson.Get(response.Body.String(), "choices.0.message.content").String() != "fresh" {
		t.Fatalf("answer = %d %s", response.Code, response.Body)
	}
	if len(tokens) != 2 || tokens[0] == tokens[1] || !strings.HasPrefix(tokens[0], "plan-key:") || !strings.HasPrefix(tokens[1], "plan-key:") {
		t.Fatalf("tokens = %v", tokens)
	}
	// A later request keeps the token the second exchange gave.
	_ = relayChat(t, handler, `{"model":"vendor-large","messages":[{"role":"user","content":"again"}]}`)
	if len(tokens) != 3 || tokens[2] != tokens[1] {
		t.Fatalf("tokens = %v", tokens)
	}
}
