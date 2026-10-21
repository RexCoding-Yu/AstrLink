package ingress

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

const geminiTestReply = `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`

// modelProtocolHandler routes through the real resolver and conversion
// engine, so planning, conversion and the provider surface all run.
func modelProtocolHandler(t *testing.T, service contract.Service, upstream func(*http.Request, map[string]any) *http.Response) http.Handler {
	t.Helper()
	if err := service.Validate(); err != nil {
		t.Fatal(err)
	}
	engine := relaykitbridge.NewEngine()
	resolver, err := endpoint.NewStoreResolver(redirectServices{service})
	if err != nil {
		t.Fatal(err)
	}
	resolver.WithRuntimeProfile(contract.RuntimeProfile{RelayKitAvailable: true, Edges: engine.Edges()})
	return NewWithDependencies(Dependencies{
		Resolver:         resolver,
		Authorizer:       endpoint.NewServiceAuthorizer(codingPlanCredentials{}, nil),
		ConversionEngine: engine,
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(request.Body)
			for name, values := range request.Header {
				if strings.Contains(strings.ToLower(name+strings.Join(values, ",")), "astrlink") {
					t.Errorf("forwarded header carries gateway branding: %s: %v", name, values)
				}
			}
			if strings.Contains(strings.ToLower(string(body)), "astrlink") {
				t.Errorf("forwarded body carries gateway branding: %s", body)
			}
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatalf("upstream body: %v: %s", err, body)
			}
			return upstream(request, payload), nil
		})),
	})
}

func geminiReply(streaming bool) *http.Response {
	if !streaming {
		return jsonResponse(http.StatusOK, geminiTestReply)
	}
	response := jsonResponse(http.StatusOK, "data: "+geminiTestReply+"\n\n")
	response.Header.Set("Content-Type", "text/event-stream")
	return response
}

// checkGeminiCall asserts a converted request reached model's Gemini route
// under prefix with a Gemini body.
func checkGeminiCall(t *testing.T, request *http.Request, payload map[string]any, prefix, model string, streaming bool) {
	t.Helper()
	wantPath := prefix + "/models/" + model + ":generateContent"
	if streaming {
		wantPath = prefix + "/models/" + model + ":streamGenerateContent"
		if request.URL.Query().Get("alt") != "sse" {
			t.Errorf("missing Gemini SSE query: %s", request.URL)
		}
	}
	if request.URL.Path != wantPath {
		t.Errorf("path = %q, want %q", request.URL.Path, wantPath)
	}
	if payload["contents"] == nil || payload["input"] != nil || payload["messages"] != nil {
		t.Errorf("not converted to Gemini: %v", payload)
	}
}

func checkConvertedBack(t *testing.T, response *httptest.ResponseRecorder, want string) {
	t.Helper()
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, want) || strings.Contains(body, `"candidates"`) {
		t.Fatalf("status=%d, not converted back: %s", response.Code, body)
	}
}

// OpenCode Go serves Gemini models only in Gemini format, under /inference.
// A Responses client reaches one through a saved /zen base and entry rows
// that leave Gemini off, and gets Responses back.
func TestOpenCodeGeminiModelIsCalledInGeminiFormat(t *testing.T) {
	const model = "gemini-4-argon-contributor"
	service := contract.Service{
		ID: "service_opencode", Name: "OpenCode Go", Kind: contract.ServiceKindOpenCodeGo, Enabled: true,
		Models: []string{model, "kimi-k3"},
		Capabilities: []contract.Capability{
			{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true},
			{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true},
			{Protocol: contract.ProtocolAnthropicMessages, Mode: contract.CapabilityModeNative, Streaming: true},
		},
		HTTP: &contract.HTTPConnection{
			BaseURL: "https://opencode.ai/zen/go/v1", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeBearer},
			CredentialRef: "local://service/service_opencode",
		},
	}
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", streaming), func(t *testing.T) {
			called := false
			handler := modelProtocolHandler(t, service, func(request *http.Request, payload map[string]any) *http.Response {
				called = true
				if request.URL.Host != "opencode.ai" {
					t.Errorf("host = %q", request.URL.Host)
				}
				checkGeminiCall(t, request, payload, "/inference/go/v1", model, streaming)
				if request.Header.Get("Authorization") != "Bearer plan-key" || request.Header.Get("X-Api-Key") != "" ||
					request.Header.Get("X-Goog-Api-Key") != "" {
					t.Errorf("OpenCode reads a Bearer key on every route: %v", request.Header)
				}
				if request.Header.Get("X-Opencode-Session") == "" {
					t.Error("missing OpenCode session")
				}
				return geminiReply(streaming)
			})
			request := httptest.NewRequest(http.MethodPost, "/v1/responses",
				strings.NewReader(fmt.Sprintf(`{"model":%q,"input":"hello","stream":%t}`, model, streaming)))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer local-secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if !called {
				t.Fatalf("upstream not called: status=%d body=%s", response.Code, response.Body.String())
			}
			want := `"output_text"`
			if streaming {
				want = "response.completed"
			}
			checkConvertedBack(t, response, want)
		})
	}
}

// A New API model pinned to Gemini is called in Gemini from Responses and
// Chat clients without a Gemini row. A model left on auto keeps its row's
// own conversion.
func TestPinnedModelIsCalledInItsFormat(t *testing.T) {
	service := contract.Service{
		ID: "service_newapi", Name: "New API", Kind: contract.ServiceKindNewAPI, Enabled: true,
		Models:         []string{"gemini-x", "gpt-x"},
		ModelProtocols: []contract.ModelProtocol{{Model: "gemini-x", Protocol: contract.ProtocolGoogleGenerateContent}},
		Capabilities: []contract.Capability{
			{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true},
			{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true, ConvertTo: contract.ProtocolOpenAIResponses},
		},
		HTTP: &contract.HTTPConnection{
			BaseURL: "https://newapi.example", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeBearer},
			CredentialRef: "local://service/service_newapi",
		},
	}
	for _, entry := range []contract.ProtocolID{contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIChat} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", entry, streaming), func(t *testing.T) {
				called := false
				handler := modelProtocolHandler(t, service, func(request *http.Request, payload map[string]any) *http.Response {
					called = true
					checkGeminiCall(t, request, payload, "/v1beta", "gemini-x", streaming)
					if request.Header.Get("Authorization") != "Bearer plan-key" {
						t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
					}
					return geminiReply(streaming)
				})
				path, body, _ := nativeProviderExchange(entry, streaming)
				body = strings.Replace(body, `"model-test"`, `"gemini-x"`, 1)
				request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if !called {
					t.Fatalf("upstream not called: status=%d body=%s", response.Code, response.Body.String())
				}
				want := map[contract.ProtocolID]string{contract.ProtocolOpenAIResponses: `"output_text"`, contract.ProtocolOpenAIChat: `"content":"ok"`}[entry]
				if streaming {
					want = map[contract.ProtocolID]string{contract.ProtocolOpenAIResponses: "response.completed", contract.ProtocolOpenAIChat: `"content":"ok"`}[entry]
				}
				checkConvertedBack(t, response, want)
			})
		}
	}
	t.Run("auto", func(t *testing.T) {
		called := false
		handler := modelProtocolHandler(t, service, func(request *http.Request, payload map[string]any) *http.Response {
			called = true
			if request.URL.Path != "/v1/responses" || payload["input"] == nil {
				t.Errorf("auto model left its row's conversion: %s %v", request.URL.Path, payload)
			}
			return jsonResponse(http.StatusOK, `{"id":"resp_1","object":"response","status":"completed","model":"gpt-x","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok","annotations":[]}]}]}`)
		})
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			strings.NewReader(`{"model":"gpt-x","messages":[{"role":"user","content":"hello"}]}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if !called {
			t.Fatalf("upstream not called: status=%d body=%s", response.Code, response.Body.String())
		}
		checkConvertedBack(t, response, `"content":"ok"`)
	})
}
