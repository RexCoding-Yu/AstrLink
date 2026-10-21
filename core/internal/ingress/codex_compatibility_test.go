package ingress

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
)

func TestCodexResponsesInputShorthand(t *testing.T) {
	for _, input := range []string{`""`, `"hello"`, `"中文\n\"quoted\" \\path <tool>"`, `null`, `[{"role":"user","content":"hello"}]`} {
		t.Run(input, func(t *testing.T) {
			fields, _, _ := prepareCodexBody(t, contract.ProtocolOpenAIResponses, `{"input":`+input+`}`)
			want := input
			if strings.HasPrefix(input, `"`) {
				want = `[{"role":"user","content":` + input + `}]`
			}
			var gotValue, wantValue any
			if json.Unmarshal(fields["input"], &gotValue) != nil || json.Unmarshal([]byte(want), &wantValue) != nil || !reflect.DeepEqual(gotValue, wantValue) {
				t.Fatalf("input = %s, want %s", fields["input"], want)
			}
		})
	}
}

func TestCodexResponsesCompatibilityOnWire(t *testing.T) {
	for _, contentType := range []string{"text/event-stream", "", "application/json"} {
		for _, stream := range []string{"true", "false", "omitted"} {
			t.Run(contentType+"/stream="+stream, func(t *testing.T) {
				wire := codexSSE(codexOutputItemDone(0, "ok"), codexCompletedWithoutOutput)
				upstream := newCodexSSEUpstreamWithType(t, contentType, wire)
				handler := NewWithDependencies(Dependencies{
					Resolver:   candidateResolver{candidates: []endpoint.Resolved{riskCodexCandidate("service_codex", upstream.url)}},
					Authorizer: endpoint.NewServiceAuthorizer(nil, codingPlanCredentials{}, accountauth.CodexIdentityPolicy{}),
				})
				server := httptest.NewServer(handler)
				defer server.Close()
				body := `{"model":"gpt-6-astra","input":"ok"`
				if stream != "omitted" {
					body += `,"stream":` + stream
				}
				request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/responses", strings.NewReader(body+`}`))
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Accept-Encoding", "br")
				response, err := server.Client().Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				got, err := io.ReadAll(response.Body)
				if err != nil {
					t.Fatal(err)
				}
				if response.StatusCode != http.StatusOK {
					t.Fatalf("status=%d body=%s", response.StatusCode, got)
				}
				if stream == "true" {
					if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") || string(got) != wire {
						t.Fatalf("stream response: headers=%v body=%s", response.Header, got)
					}
				} else {
					var completed struct {
						Object string            `json:"object"`
						Output []json.RawMessage `json:"output"`
					}
					if response.Header.Get("Content-Type") != "application/json" || json.Unmarshal(got, &completed) != nil || completed.Object != "response" || len(completed.Output) != 1 {
						t.Fatalf("non-stream response: headers=%v body=%s", response.Header, got)
					}
				}
				var forwarded map[string]json.RawMessage
				if err := json.Unmarshal(<-upstream.bodies, &forwarded); err != nil {
					t.Fatal(err)
				}
				if string(forwarded["input"]) != `[{"role":"user","content":"ok"}]` || string(forwarded["stream"]) != "true" {
					t.Fatalf("upstream request = %v", forwarded)
				}
			})
		}
	}
}
