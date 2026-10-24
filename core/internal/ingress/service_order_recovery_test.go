package ingress

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func TestFailoverOnlyRetriesLastAvailableService(t *testing.T) {
	for _, tt := range []struct {
		name                        string
		providers, usable, retries  int
		maxAttempts, successAttempt int
		want                        []string
		stop                        string
	}{
		{"single provider", 1, 1, 3, 6, 0, []string{"A.example", "A.example", "A.example", "A.example"}, "targets_exhausted"},
		{"retry succeeds", 1, 1, 3, 6, 2, []string{"A.example", "A.example"}, ""},
		{"retry last provider", 3, 3, 2, 10, 0, []string{"A.example", "B.example", "C.example", "C.example", "C.example"}, "targets_exhausted"},
		{"backups cannot be prepared", 3, 1, 2, 6, 0, []string{"A.example", "A.example", "A.example"}, "targets_exhausted"},
		{"last backup cannot be prepared", 3, 2, 2, 6, 0, []string{"A.example", "B.example", "B.example", "B.example"}, "targets_exhausted"},
		{"zero retries", 1, 1, 0, 6, 0, []string{"A.example"}, "targets_exhausted"},
		{"total attempt limit", 1, 1, 3, 2, 0, []string{"A.example", "A.example"}, "attempt_limit"},
		{"failover shares attempt limit", 3, 3, 3, 4, 0, []string{"A.example", "B.example", "C.example", "C.example"}, "attempt_limit"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			policy := contract.DefaultFailurePolicy()
			policy.MaxRetries, policy.InitialDelayMS = tt.retries, 0
			candidates := recoveryCandidates(tt.providers, policy, contract.FailoverPolicy{Enabled: true, Strategy: contract.FailoverOnly, MaxAttempts: tt.maxAttempts})
			for index := range candidates {
				candidates[index].Endpoint.Capabilities[0].Streaming = true
				if index >= tt.usable {
					candidates[index].Endpoint.Capabilities = nil
				}
			}
			const body = `{"model":"public","stream":true,"messages":[{"role":"user","content":"hello"}]}`
			const failure = "error code: 502\n"
			const success = "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: [DONE]\n\n"
			var attempts []string
			store := &memoryRequestRecordStore{}
			handler := NewWithDependencies(Dependencies{
				Resolver:       candidateResolver{candidates: candidates},
				RequestRecords: store,
				Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					attempts = append(attempts, request.URL.Host)
					data, err := io.ReadAll(request.Body)
					if err != nil || string(data) != body {
						t.Fatalf("replayed body = %q, err = %v", data, err)
					}
					if len(attempts) == tt.successAttempt {
						return sseResponse(success), nil
					}
					return &http.Response{StatusCode: http.StatusBadGateway, Header: http.Header{"Content-Type": {"text/plain; charset=UTF-8"}}, Body: io.NopCloser(strings.NewReader(failure))}, nil
				})),
			})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
			wantStatus, wantBody, wantRecordStatus := http.StatusBadGateway, failure, contract.RequestStatusFailed
			if tt.successAttempt > 0 {
				wantStatus, wantBody, wantRecordStatus = http.StatusOK, success, contract.RequestStatusSucceeded
			}
			if response.Code != wantStatus || response.Body.String() != wantBody || !reflect.DeepEqual(attempts, tt.want) {
				t.Fatalf("status=%d attempts=%v body=%s", response.Code, attempts, response.Body.String())
			}
			root, children := splitRecords(t, store)
			if root.Status != wantRecordStatus || root.AttemptIndex != len(tt.want) || root.ChildCount != len(tt.want)-1 || len(children) != len(tt.want)-1 {
				t.Fatalf("root=%+v children=%d", root, len(children))
			}
			if tt.stop != "" && (root.Recovery == nil || root.Recovery.StopReason != tt.stop) {
				t.Fatalf("recovery=%+v want stop=%s", root.Recovery, tt.stop)
			}
		})
	}
}

func TestFailoverOnlyRepairsOnceBeforeApplyingErrorRules(t *testing.T) {
	policy := contract.DefaultFailurePolicy()
	policy.MaxRetries = 0
	policy.HTTPStatus["400"] = contract.FailureFailover
	candidates := thinkingCandidates(policy, 20, "claude-sonnet-4-6", "claude-opus-4-6")
	candidates[0].Failover.Strategy = contract.FailoverOnly
	trips := 0
	var attempts []string
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: candidates},
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			trips++
			attempts = append(attempts, request.URL.Host)
			body, _ := io.ReadAll(request.Body)
			if strings.Contains(string(body), `"signature":"invalid"`) != (trips%2 == 1) {
				t.Fatal("each target must receive the original request, then its own repair")
			}
			return jsonResponse(400, signatureFailure), nil
		})),
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(signedThinkingRequest)))
	if !reflect.DeepEqual(attempts, []string{"A.example", "A.example", "B.example", "B.example"}) || response.Code != 400 {
		t.Fatalf("attempts=%v response=%d", attempts, response.Code)
	}
}
