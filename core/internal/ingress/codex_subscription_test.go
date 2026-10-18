package ingress

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
	"github.com/gorilla/websocket"
)

func prepareCodexBody(t *testing.T, protocol contract.ProtocolID, body string) (map[string]json.RawMessage, []byte, bool) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	forced, err := prepareCodexSubscriptionRequest(request, protocol, codexRequestOptions{normalize: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	return fields, raw, forced
}

func TestPrepareCodexSubscriptionRequestNormalizesStructureOnly(t *testing.T) {
	const input = `{"model":"gpt-6-astra","input":[{"role":"user","content":"hello"}],"store":true,
		"reasoning":{"effort":"high"},"include":["file_search_call.results"],"stream":false,
		"previous_response_id":"resp_prev","service_tier":"priority","prompt_cache_key":"cache-1",
		"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],
		"max_output_tokens":10,"max_completion_tokens":10,"temperature":0.2,"top_p":0.9,
		"frequency_penalty":0,"presence_penalty":0,"truncation":"auto","prompt_cache_retention":"24h",
		"prompt_cache_options":{"mode":"explicit"},"safety_identifier":"s","user":"u","metadata":{"a":"b"},"stream_options":{"include_usage":true}}`
	fields, _, forced := prepareCodexBody(t, contract.ProtocolOpenAIResponses, input)
	if !forced {
		t.Fatal("stream:false was not forced upstream")
	}
	for name, want := range map[string]string{
		"store": `false`, "instructions": `""`, "stream": `true`,
		"include":              `["file_search_call.results","reasoning.encrypted_content"]`,
		"previous_response_id": `"resp_prev"`, "service_tier": `"priority"`, "prompt_cache_key": `"cache-1"`,
		"input": `[{"role":"user","content":"hello"}]`,
		"tools": `[{"type":"function","name":"lookup","parameters":{"type":"object"}}]`,
	} {
		if got := string(fields[name]); got != want {
			t.Errorf("%s = %s, want %s", name, got, want)
		}
	}
	for _, name := range codexSubscriptionUnsupportedFields {
		if _, ok := fields[name]; ok {
			t.Errorf("unsupported field %s was forwarded", name)
		}
	}

	// Normalizing an already normalized body is a byte-identical no-op.
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	_, again, forcedAgain := prepareCodexBody(t, contract.ProtocolOpenAIResponses, string(encoded))
	if forcedAgain || !bytes.Equal(again, encoded) {
		t.Fatalf("second pass changed the body (forced=%t):\n%s\n%s", forcedAgain, encoded, again)
	}
}

func TestPrepareCodexSubscriptionRequestKeepsCompliantBytes(t *testing.T) {
	const compliant = "{ \"model\": \"gpt-6-astra\",\n  \"instructions\": \"be brief\", \"input\": \"hello\",\n" +
		"  \"store\": false, \"stream\": true, \"reasoning\": {\"effort\": \"low\"},\n" +
		"  \"include\": [\"reasoning.encrypted_content\"] }"
	_, raw, forced := prepareCodexBody(t, contract.ProtocolOpenAIResponses, compliant)
	if forced || string(raw) != compliant {
		t.Fatalf("compliant body rewritten (forced=%t): %s", forced, raw)
	}
}

func TestPrepareCodexSubscriptionRequestEdgeCases(t *testing.T) {
	for _, test := range []struct {
		name, body string
		protocol   contract.ProtocolID
		check      func(t *testing.T, fields map[string]json.RawMessage, forced bool)
	}{
		{
			name: "compact only drops unsupported fields", protocol: contract.ProtocolOpenAIResponsesCompact,
			body: `{"model":"gpt-6-astra","input":"hello","instructions":null,"reasoning":{"effort":"low"},"temperature":1}`,
			check: func(t *testing.T, fields map[string]json.RawMessage, forced bool) {
				if forced || string(fields["instructions"]) != `""` || fields["temperature"] != nil {
					t.Fatalf("compact body = %v forced=%t", fields, forced)
				}
				for _, name := range []string{"store", "stream", "include"} {
					if _, ok := fields[name]; ok {
						t.Fatalf("compact body gained %s: %v", name, fields)
					}
				}
			},
		},
		{
			name: "null reasoning adds no include", protocol: contract.ProtocolOpenAIResponses,
			body: `{"model":"gpt-6-astra","input":"hello","reasoning":null,"stream":true}`,
			check: func(t *testing.T, fields map[string]json.RawMessage, forced bool) {
				if _, ok := fields["include"]; ok || forced {
					t.Fatalf("body = %v forced=%t", fields, forced)
				}
			},
		},
		{
			name: "malformed include is left for upstream", protocol: contract.ProtocolOpenAIResponses,
			body: `{"model":"gpt-6-astra","input":"hello","reasoning":{},"include":"reasoning","stream":true}`,
			check: func(t *testing.T, fields map[string]json.RawMessage, _ bool) {
				if string(fields["include"]) != `"reasoning"` {
					t.Fatalf("include = %s", fields["include"])
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fields, _, forced := prepareCodexBody(t, test.protocol, test.body)
			test.check(t, fields, forced)
		})
	}

	for _, invalid := range []string{`[]`, `null`, `{"model":`} {
		request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(invalid))
		if _, err := prepareCodexSubscriptionRequest(request, contract.ProtocolOpenAIResponses, codexRequestOptions{normalize: true}); err == nil {
			t.Errorf("prepareCodexSubscriptionRequest(%s) succeeded", invalid)
		}
	}
}

type codexSSEUpstream struct {
	hits   atomic.Int32
	bodies chan []byte
	accept chan string
	url    string
}

// newCodexSSEUpstream answers attempt N with streams[min(N, len-1)].
func newCodexSSEUpstream(t *testing.T, streams ...string) *codexSSEUpstream {
	t.Helper()
	upstream := &codexSSEUpstream{bodies: make(chan []byte, 8), accept: make(chan string, 8)}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hit := int(upstream.hits.Add(1)) - 1
		body, _ := io.ReadAll(request.Body)
		upstream.bodies <- body
		upstream.accept <- request.Header.Get("Accept")
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, streams[min(hit, len(streams)-1)])
	}))
	t.Cleanup(server.Close)
	upstream.url = server.URL
	return upstream
}

func codexSSE(events ...string) string {
	var builder strings.Builder
	for _, event := range events {
		var header struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(event), &header)
		builder.WriteString("event: " + header.Type + "\r\ndata: " + event + "\r\n\r\n")
	}
	return builder.String()
}

const codexCompletedWithoutOutput = `{"type":"response.completed","response":{"id":"resp_forced","object":"response","status":"completed","output":[],"usage":{"input_tokens":11,"output_tokens":4,"total_tokens":15}}}`

func codexOutputItemDone(index int, text string) string {
	return `{"type":"response.output_item.done","output_index":` + strconv.Itoa(index) +
		`,"item":{"id":"msg_` + text + `","type":"message","role":"assistant","content":[{"type":"output_text","text":"` + text + `"}]}}`
}

func serveCodexForcedStream(t *testing.T, candidate endpoint.Resolved, records *memoryRequestRecordStore) *httptest.ResponseRecorder {
	t.Helper()
	dependencies := Dependencies{
		Resolver:   candidateResolver{candidates: []endpoint.Resolved{candidate}},
		Authorizer: endpoint.NewServiceAuthorizer(nil, codingPlanCredentials{}, accountauth.CodexIdentityPolicy{}),
	}
	if records != nil {
		dependencies.RequestRecords = records
	}
	handler := NewWithDependencies(dependencies)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses",
		strings.NewReader(`{"model":"gpt-6-astra","input":"hello","stream":false}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "third-party-sdk/1.0")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestCodexSubscriptionAggregatesForcedStream(t *testing.T) {
	t.Parallel()
	upstream := newCodexSSEUpstream(t, codexSSE(
		`{"type":"response.created","response":{"id":"resp_forced","status":"in_progress","output":[]}}`,
		codexOutputItemDone(1, "second"),
		codexOutputItemDone(0, "first"),
		codexCompletedWithoutOutput,
	))
	records := &memoryRequestRecordStore{}
	response := serveCodexForcedStream(t, riskCodexCandidate("service_codex", upstream.url), records)
	if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("response = %d %q %s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	var aggregated struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Output []struct {
			ID string `json:"id"`
		} `json:"output"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &aggregated); err != nil {
		t.Fatalf("aggregated body is not JSON: %v %s", err, response.Body.String())
	}
	if aggregated.ID != "resp_forced" || aggregated.Status != "completed" || len(aggregated.Output) != 2 ||
		aggregated.Output[0].ID != "msg_first" || aggregated.Output[1].ID != "msg_second" {
		t.Fatalf("aggregated response = %+v", aggregated)
	}
	var sent map[string]json.RawMessage
	if err := json.Unmarshal(<-upstream.bodies, &sent); err != nil {
		t.Fatal(err)
	}
	if string(sent["stream"]) != "true" || string(sent["store"]) != "false" || <-upstream.accept != "text/event-stream" {
		t.Fatalf("upstream request was not forced to stream: %v", sent)
	}
	if len(records.records) != 1 || records.records[0].Usage == nil ||
		records.records[0].Usage.InputTokens != 11 || records.records[0].Usage.OutputTokens != 4 ||
		records.records[0].Usage.BillingIncomplete {
		t.Fatalf("usage from forced stream = %+v", records.records)
	}
}

func TestCodexSubscriptionForcedStreamFailureStatus(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, event, code string
		status            int
	}{
		{"rate limit", `{"type":"response.failed","response":{"id":"resp_failed","error":{"code":"rate_limit_exceeded","message":"slow down"}}}`, "rate_limit_exceeded", http.StatusTooManyRequests},
		{"context length", `{"type":"error","error":{"type":"invalid_request_error","code":"context_length_exceeded","message":"too long"}}`, "context_length_exceeded", http.StatusBadRequest},
		{"flat error", `{"type":"error","code":"invalid_prompt","message":"bad prompt"}`, "invalid_prompt", http.StatusBadRequest},
		{"server error", `{"type":"response.failed","response":{"id":"resp_failed","error":{"code":"server_error","message":"oops"}}}`, "server_error", http.StatusBadGateway},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			upstream := newCodexSSEUpstream(t, codexSSE(test.event))
			response := serveCodexForcedStream(t, riskCodexCandidate("service_codex", upstream.url), nil)
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if response.Code != test.status || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Error.Code != test.code {
				t.Fatalf("response = %d %s, want %d %s", response.Code, response.Body.String(), test.status, test.code)
			}
			if hits := upstream.hits.Load(); hits != 1 {
				t.Fatalf("failed stream was retried %d times", hits)
			}
		})
	}
}

func TestCodexSubscriptionTruncatedForcedStreamIsRetried(t *testing.T) {
	t.Parallel()
	upstream := newCodexSSEUpstream(t,
		codexSSE(`{"type":"response.created","response":{"id":"resp_cut","output":[]}}`),
		codexSSE(codexOutputItemDone(0, "first"), codexCompletedWithoutOutput),
	)
	response := serveCodexForcedStream(t, riskCodexCandidate("service_codex", upstream.url), nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"msg_first"`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if hits := upstream.hits.Load(); hits != 2 {
		t.Fatalf("upstream hits = %d, want a retry after the truncated stream", hits)
	}
}

func TestCodexSubscriptionConvertedNonStreamingRequestAggregates(t *testing.T) {
	t.Parallel()
	upstream := newCodexSSEUpstream(t, codexSSE(
		codexOutputItemDone(0, "converted"),
		`{"type":"response.completed","response":{"id":"resp_converted","object":"response","model":"gpt-6-astra","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}`,
	))
	candidate := codexVersionCandidate(upstream.url + "/backend-api/codex")
	candidate.Service.Capabilities = append(candidate.Service.Capabilities, contract.Capability{
		Protocol: contract.ProtocolAnthropicMessages, Mode: contract.CapabilityModeNative, Streaming: true,
		ConvertTo: contract.ProtocolOpenAIResponses,
	})
	candidate.UpstreamProtocol = contract.ProtocolOpenAIResponses
	handler := NewWithDependencies(Dependencies{
		Resolver:         candidateResolver{candidates: []endpoint.Resolved{candidate}},
		Authorizer:       endpoint.NewServiceAuthorizer(nil, codingPlanCredentials{}, accountauth.CodexIdentityPolicy{}),
		ConversionEngine: relaykitbridge.NewEngine(),
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(
		`{"model":"gpt-6-astra","max_tokens":64,"temperature":0.3,"messages":[{"role":"user","content":"hello"}],"stream":false}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var message struct {
		Type    string `json:"type"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &message) != nil ||
		message.Type != "message" || len(message.Content) == 0 || message.Content[0].Text != "converted" {
		t.Fatalf("converted response = %d %s", response.Code, response.Body.String())
	}
	var sent map[string]json.RawMessage
	if err := json.Unmarshal(<-upstream.bodies, &sent); err != nil {
		t.Fatal(err)
	}
	if string(sent["stream"]) != "true" || sent["temperature"] != nil || sent["max_output_tokens"] != nil {
		t.Fatalf("converted upstream body = %v", sent)
	}
}

func TestCodexSubscriptionForwardsCompliantNativeBodyUnchanged(t *testing.T) {
	t.Parallel()
	const compliant = `{"model":"gpt-6-astra","instructions":"be brief","input":"hello","store":false,"stream":true}`
	upstream := newCodexSSEUpstream(t, codexSSE(codexCompletedWithoutOutput))
	handler := NewWithDependencies(Dependencies{
		Resolver:   candidateResolver{candidates: []endpoint.Resolved{riskCodexCandidate("service_codex", upstream.url)}},
		Authorizer: endpoint.NewServiceAuthorizer(nil, codingPlanCredentials{}, accountauth.CodexIdentityPolicy{}),
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(compliant))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("User-Agent", "codex_cli_rs/0.156.0 (Mac OS; arm64)")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "event: response.completed") {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if sent := <-upstream.bodies; string(sent) != compliant {
		t.Fatalf("compliant body changed:\n got %s\nwant %s", sent, compliant)
	}
}

const (
	codexWebSearchCallItem = `{"type":"web_search_call","id":"ws_1","status":"completed","action":{"type":"search","query":"news"}}`
	codexHistoryTool       = `{"type":"web_search","external_web_access":false}`
	codexLiteMetadata      = `"client_metadata":{"ws_request_header_x_openai_internal_codex_responses_lite":"true"}`
)

func TestCodexWebSearchHistoryDeclaration(t *testing.T) {
	const ws, tool = codexWebSearchCallItem, codexHistoryTool
	const user = `{"role":"user","content":"next"}`
	lite := http.Header{"X-Openai-Internal-Codex-Responses-Lite": {"true"}}
	for _, test := range []struct {
		name, body, want string
		header           http.Header
	}{
		{"declared tools are extended and the choice kept",
			`{"model":"m","input":[` + ws + `,` + user + `],"tools":[{"type":"function","name":"read"}],"tool_choice":"auto"}`,
			`{"model":"m","input":[` + ws + `,` + user + `],"tools":[{"type":"function","name":"read"},` + tool + `],"tool_choice":"auto"}`, nil},
		{"compaction without tools pins the choice",
			`{"model":"m","input":[` + ws + `,` + user + `],"tools":[],"stream":true}`,
			`{"model":"m","input":[` + ws + `,` + user + `],"tools":[` + tool + `],"stream":true,"tool_choice":"none"}`, nil},
		{"missing tools", `{"input":[` + ws + `]}`, `{"input":[` + ws + `],"tools":[` + tool + `],"tool_choice":"none"}`, nil},
		{"auto among no tools", `{"input":[` + ws + `],"tools":[],"tool_choice":"auto"}`,
			`{"input":[` + ws + `],"tools":[` + tool + `],"tool_choice":"none"}`, nil},
		{"another choice is the caller's", `{"input":[` + ws + `],"tools":[],"tool_choice":"required"}`,
			`{"input":[` + ws + `],"tools":[` + tool + `],"tool_choice":"required"}`, nil},
		{"Lite extends the first additional_tools",
			`{"input":[{"type":"additional_tools","role":"developer","tools":[{"type":"function","name":"exec"}]},` + ws + `,` + user + `]}`,
			`{"input":[{"type":"additional_tools","role":"developer","tools":[{"type":"function","name":"exec"},` + tool + `]},` + ws + `,` + user + `]}`, lite},
		{"Lite fills an empty additional_tools",
			`{"input":[{"type":"additional_tools","role":"developer","tools":[]},` + ws + `]}`,
			`{"input":[{"type":"additional_tools","role":"developer","tools":[` + tool + `]},` + ws + `],"tool_choice":"none"}`, lite},
		{"Lite inserts before a trailing compaction_trigger",
			`{` + codexLiteMetadata + `,"input":[` + ws + `,{"type":"compaction_trigger"}]}`,
			`{` + codexLiteMetadata + `,"input":[` + ws + `,{"type":"additional_tools","role":"developer","tools":[` + tool + `]},{"type":"compaction_trigger"}],"tool_choice":"none"}`, nil},
		{"Lite appends additional_tools",
			`{"input":[` + user + `,` + ws + `]}`,
			`{"input":[` + user + `,` + ws + `,{"type":"additional_tools","role":"developer","tools":[` + tool + `]}],"tool_choice":"none"}`, lite},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, changed := withCodexWebSearchHistoryTool([]byte(test.body), test.header)
			if !changed || string(got) != test.want {
				t.Fatalf("changed=%t body:\n got %s\nwant %s", changed, got, test.want)
			}
			if again, changed := withCodexWebSearchHistoryTool(got, test.header); changed || string(again) != string(got) {
				t.Fatalf("second pass changed the body: %s", again)
			}
		})
	}
	for _, test := range []struct {
		name, body string
		header     http.Header
	}{
		{"declared", `{"input":[` + ws + `],"tools":[{"type":"web_search","external_web_access":true}]}`, nil},
		{"declared version", `{"input":[` + ws + `],"tools":[{"type":"web_search_preview"}]}`, nil},
		{"declared in additional_tools", `{"input":[{"type":"additional_tools","tools":[{"type":"web_search"}]},` + ws + `],"tools":[]}`, nil},
		{"Lite declared in additional_tools", `{"input":[{"type":"additional_tools","tools":[{"type":"web_search"}]},` + ws + `]}`, lite},
		{"no hosted search", `{"input":[{"role":"user","content":[{"type":"input_text","text":"web_search_call"}]}],"tools":[]}`, lite},
		{"string input", `{"input":"web_search_call","tools":[]}`, nil},
		{"malformed tools", `{"input":[` + ws + `],"tools":{"type":"function"}}`, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got, changed := withCodexWebSearchHistoryTool([]byte(test.body), test.header); changed || string(got) != test.body {
				t.Fatalf("changed=%t body = %s", changed, got)
			}
		})
	}
}

// Every Codex subscription Responses request gets the declaration its history
// needs: official passthrough, third-party and converted alike. Compaction and
// disabled normalization are forwarded as they are.
func TestCodexWebSearchHistoryDeclarationPaths(t *testing.T) {
	const history = `{"model":"gpt-6-astra","instructions":"be brief","input":[{"role":"user","content":"search"},` +
		codexWebSearchCallItem + `,{"role":"user","content":"thanks"}],"tools":[],"store":false,"stream":true}`
	declared := strings.Replace(history, `"tools":[]`, `"tools":[`+codexHistoryTool+`]`, 1)
	declared = strings.TrimSuffix(declared, "}") + `,"tool_choice":"none"}`
	const converted = `{"model":"gpt-6-astra","max_tokens":64,"stream":true,"messages":[` +
		`{"role":"user","content":"search the news"},` +
		`{"role":"assistant","content":[{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"news"}},` +
		`{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[{"type":"web_search_result","url":"https://example.com","title":"Example","encrypted_content":"opaque"}]},` +
		`{"type":"text","text":"Here is the news."}]},` +
		`{"role":"user","content":"thanks"}]}`
	official := officialCodexHeaders(learnedCodexUA, "codex_cli_rs")
	normalizationOff := contract.DefaultRoutingSettings()
	normalizationOff.CodexRequestNormalization = false
	for _, test := range []struct {
		name, path, body string
		header           http.Header
		settings         contract.RoutingSettings
		exact            string
		declared         bool
	}{
		{name: "official passthrough", path: "/v1/responses", body: history, header: official, exact: declared},
		{name: "official without hosted search", path: "/v1/responses", body: officialCodexBody, header: official, exact: officialCodexBody},
		{name: "third-party", path: "/v1/responses", body: history, header: http.Header{"Accept": {"text/event-stream"}}, declared: true},
		{name: "converted", path: "/v1/messages", body: converted, header: http.Header{"Accept": {"text/event-stream"}}, declared: true},
		{name: "compaction", path: "/v1/responses/compact", body: strings.Replace(history, `,"stream":true`, "", 1), header: official,
			exact: strings.Replace(history, `,"stream":true`, "", 1)},
		{name: "normalization off", path: "/v1/responses", body: history, header: official, settings: normalizationOff, exact: history},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := codexSSE(codexCompletedWithoutOutput)
			contentType := "text/event-stream"
			if strings.HasSuffix(test.path, "/compact") {
				response, contentType = `{"id":"resp_compact","object":"response.compaction","output":[]}`, "application/json"
			}
			upstream := newCapturedUpstream(t, http.StatusOK, contentType, response)
			settings := test.settings
			if settings.MaxAttempts == 0 {
				settings = contract.DefaultRoutingSettings()
			}
			newIdentityHarness(t, settings, nil, codexConversionCandidate(upstream.url)).serve(t, test.path, test.body, test.header.Clone())
			_, sent := upstream.request(t)
			if test.exact != "" {
				if string(sent) != test.exact {
					t.Fatalf("upstream body:\n got %s\nwant %s", sent, test.exact)
				}
				return
			}
			var fields struct {
				Input      []map[string]any `json:"input"`
				Tools      []map[string]any `json:"tools"`
				ToolChoice any              `json:"tool_choice"`
			}
			if err := json.Unmarshal(sent, &fields); err != nil {
				t.Fatal(err)
			}
			replayed := false
			for _, item := range fields.Input {
				replayed = replayed || item["type"] == "web_search_call"
			}
			if !replayed || len(fields.Tools) != 1 || fields.Tools[0]["type"] != "web_search" ||
				fields.Tools[0]["external_web_access"] != false || fields.ToolChoice != "none" {
				t.Fatalf("upstream body = %s", sent)
			}
		})
	}
}

func TestCodexWebSearchHistoryDeclarationOverWebSocket(t *testing.T) {
	events := make(chan map[string]any, 1)
	upstream := wsUpstream(t, func(conn *websocket.Conn, _ *http.Request) {
		var event map[string]any
		if err := conn.ReadJSON(&event); err != nil {
			t.Error(err)
			return
		}
		events <- event
		_ = conn.WriteJSON(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_ws", "status": "completed", "output": []any{}}})
	})
	candidate := wsCandidate(upstream.URL + "/backend-api/codex")
	candidate.Service.Kind = contract.ServiceKindCodexSubscription
	candidate.Service.HTTP = nil
	candidate.Service.ResponsesWebSocketEnabled = nil
	candidate.Service.Subscription = &contract.SubscriptionConnection{Provider: contract.SubscriptionProviderOpenAICodex, Status: contract.SubscriptionStatusConnected, CredentialRef: "keyring://astrlink/endpoint_test", AuthorizationBoundary: "local-only"}
	candidate.Service.Capabilities = contract.SubscriptionProviderOpenAICodex.Capabilities()
	client := dialResponses(t, NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: []endpoint.Resolved{candidate}}, Authorizer: authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) {
		return http.Header{"Authorization": {"Bearer codex-upstream"}}, nil
	})}), nil)
	sendWS(t, client, `{"type":"response.create","model":"gpt-6-astra",`+codexLiteMetadata+`,"input":[`+codexWebSearchCallItem+`,{"type":"compaction_trigger"}]}`)
	if event := readWS(t, client); event["type"] != "response.completed" {
		t.Fatalf("event = %v", event)
	}
	event := <-events
	input, _ := event["input"].([]any)
	if len(input) != 3 || event["tool_choice"] != "none" || event["tools"] != nil {
		t.Fatalf("upstream event = %v", event)
	}
	declaration, _ := input[1].(map[string]any)
	tools, _ := declaration["tools"].([]any)
	if declaration["type"] != "additional_tools" || len(tools) != 1 || tools[0].(map[string]any)["type"] != "web_search" ||
		input[2].(map[string]any)["type"] != "compaction_trigger" {
		t.Fatalf("upstream input = %v", input)
	}
}
