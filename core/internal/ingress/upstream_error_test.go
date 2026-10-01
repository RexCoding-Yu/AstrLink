package ingress

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func TestFailedRecordKeepsTheUpstreamErrorVerbatim(t *testing.T) {
	const upstreamBody = `{"error":{"type":"new_api_error","message":"分组 auto 下模型 claude-haiku-4-5 的可用渠道不存在（retry） (request id: 2026)"},"type":"error"}`
	var gzipped bytes.Buffer
	writer := gzip.NewWriter(&gzipped)
	_, _ = writer.Write([]byte(upstreamBody))
	_ = writer.Close()
	tests := []struct {
		name     string
		header   http.Header
		wire     []byte
		wantType string
	}{
		{name: "plain", header: http.Header{"Content-Type": {"application/json"}}, wire: []byte(upstreamBody), wantType: "application/json"},
		{
			name:     "gzip",
			header:   http.Header{"Content-Type": {"application/json"}, "Content-Encoding": {"gzip"}},
			wire:     gzipped.Bytes(),
			wantType: "application/json",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &memoryRequestRecordStore{}
			// Audit capture stays off: the failure is recorded regardless.
			handler := NewWithDependencies(Dependencies{
				Resolver: candidateResolver{candidates: []endpoint.Resolved{{
					Endpoint: validEndpoint(contract.ProtocolAnthropicMessages, false),
				}}},
				RequestRecords: store,
				Forwarder: transport.New(roundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusInternalServerError,
						Header:     test.header.Clone(),
						Body:       io.NopCloser(bytes.NewReader(test.wire)),
					}, nil
				})),
			})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(
				http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-haiku-4-5","messages":[]}`),
			))
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("client status = %d", response.Code)
			}
			if len(store.records) != 1 || store.records[0].Error == nil {
				t.Fatalf("records = %#v", store.records)
			}
			failure := store.records[0].Error
			wantMessage := "new_api_error: 分组 auto 下模型 claude-haiku-4-5 的可用渠道不存在（retry） (request id: 2026)"
			if failure.Code != "upstream_http_error" || failure.Message != wantMessage {
				t.Fatalf("error = %#v, want message %q", failure, wantMessage)
			}
			want := contract.UpstreamErrorResponse{
				Status: http.StatusInternalServerError, ContentType: test.wantType, Body: upstreamBody,
			}
			if failure.Upstream == nil || *failure.Upstream != want {
				t.Fatalf("upstream = %#v, want %#v", failure.Upstream, want)
			}
			if err := store.records[0].Validate(); err != nil {
				t.Fatalf("record does not validate: %v", err)
			}
		})
	}
}

func TestFailedAttemptsKeepTheirOwnUpstreamErrors(t *testing.T) {
	first := validEndpoint(contract.ProtocolOpenAIChat, false)
	first.ID = "endpoint_first"
	first.BaseURL = "https://first.example"
	second := first
	second.ID = "endpoint_second"
	second.BaseURL = "https://second.example"
	store := &memoryRequestRecordStore{}
	handler := NewWithDependencies(Dependencies{
		Resolver:       candidateResolver{candidates: []endpoint.Resolved{{Endpoint: first}, {Endpoint: second}}},
		RequestRecords: store,
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body := `{"error":{"message":"first is overloaded","type":"server_error"}}`
			if request.URL.Host == "second.example" {
				body = `{"error":{"message":"second rejected the schema","code":"invalid_function_parameters"}}`
			}
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		})),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(
		http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[]}`),
	))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "second rejected") {
		t.Fatalf("client = %d %s", response.Code, response.Body.String())
	}
	messages := map[bool]string{}
	for _, record := range store.records {
		if record.Error == nil || record.Error.Upstream == nil {
			t.Fatalf("record %s error = %#v", record.ID, record.Error)
		}
		messages[record.ParentRequestID != nil] = record.Error.Message
	}
	if messages[true] != "server_error: first is overloaded" ||
		messages[false] != "invalid_function_parameters: second rejected the schema" {
		t.Fatalf("attempt messages = %#v", messages)
	}
}

func TestGatewayErrorsNameTheirRecordInTheClientProtocol(t *testing.T) {
	limited := &endpoint.RateLimitedCandidatesError{Limits: []endpoint.RateLimitedCandidate{{
		Service: "service_neko", ServiceName: "Neko", Model: "claude-opus-5-5", Until: time.Now().Add(20 * time.Second),
	}}}
	tests := []struct {
		name       string
		path       string
		body       string
		wantType   string
		wantStatus string
	}{
		{name: "anthropic", path: "/v1/messages", body: `{"model":"claude-opus-5-5","messages":[]}`, wantType: "rate_limit_error"},
		{name: "openai", path: "/v1/chat/completions", body: `{"model":"claude-opus-5-5","messages":[]}`},
		{name: "gemini", path: "/v1beta/models/claude-opus-5-5:generateContent", body: `{"contents":[]}`, wantStatus: "RESOURCE_EXHAUSTED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &memoryRequestRecordStore{}
			handler := NewWithDependencies(Dependencies{
				Resolver:       candidateResolver{err: limited},
				RequestRecords: store,
			})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body)))

			envelope := assertInferenceError(t, response, http.StatusTooManyRequests, "upstream_rate_limited")
			if len(store.records) != 1 || envelope.RequestID != string(store.records[0].ID) {
				t.Fatalf("request_id = %q, records = %#v", envelope.RequestID, store.records)
			}
			if retryAfter := response.Header().Get("Retry-After"); retryAfter != "20" && retryAfter != "19" {
				t.Fatalf("Retry-After = %q", retryAfter)
			}
			if !strings.Contains(envelope.Error.Message, `Neko (model "claude-opus-5-5"`) ||
				store.records[0].Error.Message != envelope.Error.Message {
				t.Fatalf("message = %q, record = %#v", envelope.Error.Message, store.records[0].Error)
			}
			var raw map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
				t.Fatal(err)
			}
			wantTop := ""
			if test.wantType != "" {
				wantTop = "error"
			}
			if top, _ := raw["type"].(string); top != wantTop ||
				envelope.Error.Type != test.wantType || envelope.Error.Status != test.wantStatus {
				t.Fatalf("protocol fields = type %q, error %#v", top, envelope.Error)
			}
		})
	}
}

func TestNativeErrorMessage(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		want        string
	}{
		{name: "openai", body: `{"error":{"message":"Invalid schema","type":"invalid_request_error","code":"invalid_function_parameters"}}`, want: "invalid_request_error: Invalid schema"},
		{name: "anthropic", body: `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, want: "overloaded_error: Overloaded"},
		{name: "nil type falls through to code", body: `{"error":{"type":"<nil>","code":"bad","message":"thinking.type.disabled is not supported"}}`, want: "bad: thinking.type.disabled is not supported"},
		{name: "gemini array", body: `[{"error":{"code":400,"message":"API key not valid","status":"INVALID_ARGUMENT"}}]`, want: "INVALID_ARGUMENT: API key not valid"},
		{name: "string error", body: `{"error":"model not found"}`, want: "model not found"},
		{name: "fastapi detail", body: `{"detail":"Not authenticated"}`, want: "Not authenticated"},
		{name: "sse", body: "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":\"boom\"}}\n\n", want: "api_error: boom"},
		{name: "plain text", contentType: "text/plain", body: "upstream connect error\n or disconnect", want: "upstream connect error or disconnect"},
		{name: "html page", contentType: "text/html", body: "<html><title>502</title></html>", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := nativeErrorMessage(contract.UpstreamErrorResponse{Status: 500, ContentType: test.contentType, Body: test.body})
			if got != test.want {
				t.Fatalf("nativeErrorMessage = %q, want %q", got, test.want)
			}
		})
	}
}

func TestUpstreamErrorCaptureBoundsTheRecordedBody(t *testing.T) {
	capture := newUpstreamErrorCapture(http.StatusBadGateway, "text/plain", "")
	capture.observe(bytes.Repeat([]byte("界"), contract.MaxUpstreamErrorBodyBytes))
	response := capture.response()
	if !response.Truncated || response.Validate() != nil || len(response.Body) > contract.MaxUpstreamErrorBodyBytes {
		t.Fatalf("bounded response = truncated %v, %d bytes, %v", response.Truncated, len(response.Body), response.Validate())
	}
}
