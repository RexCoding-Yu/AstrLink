package ingress

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func TestInferencePreflightDoesNotAuthenticateRouteOrRecord(t *testing.T) {
	store := &memoryRequestRecordStore{}
	handler, err := NewProduction(Dependencies{
		AllowedHost:    "127.0.0.1:8317",
		RequestRecords: store,
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			t.Fatal("preflight resolved an endpoint")
			return endpoint.Resolved{}, endpoint.ErrNoEndpoint
		}),
		Authorizer: authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) {
			t.Fatal("preflight authorized an upstream")
			return nil, nil
		}),
		AccessTokenAuthenticator: AccessTokenAuthenticatorFunc(func(context.Context, string) (contract.AccessTokenID, error) {
			t.Fatal("preflight authenticated an API token")
			return "", nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path, method, host string
		status             int
	}{
		{path: "/v1/responses", method: "POST", status: 204},
		{path: "/v1/chat/completions", method: "POST", status: 204},
		{path: "/v1/messages", method: "POST", status: 204},
		{path: "/v1beta/models/model:generateContent", method: "POST", status: 204},
		{path: "/v1/models", method: "GET", status: 204},
		{path: ReachabilityPath, method: "GET", status: 204},
		{path: "/v1/responses", method: "DELETE", status: 405},
		{path: "/unknown", method: "POST", status: 404},
		{path: "/v1/responses", method: "POST", host: "wrong.example:8317", status: 421},
	} {
		t.Run(test.method+test.path+test.host, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodOptions, test.path, nil)
			request.Host = "127.0.0.1:8317"
			if test.host != "" {
				request.Host = test.host
			}
			request.Header.Set("Origin", "chrome-extension://example")
			request.Header.Set("Access-Control-Request-Method", test.method)
			request.Header.Set("Access-Control-Request-Headers", "authorization, content-type, x-api-key, x-goog-api-key, anthropic-version, x-client-custom")
			request.Header.Set("Access-Control-Request-Private-Network", "true")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			response := recorder.Result()
			defer response.Body.Close()
			if response.StatusCode != test.status || response.Header.Get("Access-Control-Allow-Origin") != "*" {
				t.Fatalf("response=%d %v", response.StatusCode, response.Header)
			}
			if test.status == 204 {
				if response.Header.Get("Access-Control-Allow-Methods") != test.method || response.Header.Get("Access-Control-Allow-Headers") != request.Header.Get("Access-Control-Request-Headers") || response.Header.Get("Access-Control-Allow-Private-Network") != "true" || recorder.Body.Len() != 0 {
					t.Fatalf("preflight response=%v body=%q", response.Header, recorder.Body.String())
				}
			}
			if response.Header.Get("Access-Control-Allow-Credentials") != "" {
				t.Fatal("API must not use browser cookies for authentication")
			}
		})
	}
	if len(store.records) != 0 {
		t.Fatalf("preflights created %d records", len(store.records))
	}
}

func TestBrowserInferenceUsesAPITokenAndOwnsCORS(t *testing.T) {
	const token = "astr_0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"
	for _, test := range []struct {
		name, origin, header, value string
		status                      int
	}{
		{"bearer", "https://browser.example", "Authorization", "Bearer " + token, 200},
		{"extension", "chrome-extension://example", "X-Api-Key", token, 200},
		{"opaque origin", "null", "X-Goog-Api-Key", token, 200},
		{"missing token", "https://browser.example", "", "", 401},
		{"wrong token", "https://browser.example", "Authorization", "Bearer wrong", 401},
		{"cookie is not a token", "https://browser.example", "Cookie", "access_token=" + token, 401},
	} {
		t.Run(test.name, func(t *testing.T) {
			hits := 0
			wire := codexSSE(codexCompletedWithoutOutput)
			handler, err := NewNetworkProduction(Dependencies{
				Resolver: candidateResolver{candidates: []endpoint.Resolved{riskCodexCandidate("service_codex", "https://codex.example")}},
				Authorizer: authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) {
					return http.Header{"Authorization": {"Bearer upstream-secret"}}, nil
				}),
				AccessTokenAuthenticator: AccessTokenAuthenticatorFunc(func(_ context.Context, presented string) (contract.AccessTokenID, error) {
					if presented == token {
						return "token_primary", nil
					}
					return "", errors.New("invalid token")
				}),
				Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
					hits++
					for _, name := range []string{"Origin", "Referer", "Cookie", "Sec-Fetch-Site", "X-Api-Key", "X-Goog-Api-Key"} {
						if request.Header.Get(name) != "" {
							t.Errorf("browser header %s reached upstream", name)
						}
					}
					if request.Header.Get("Authorization") != "Bearer upstream-secret" {
						t.Error("local token reached upstream")
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}, "Access-Control-Allow-Origin": {"https://provider.example"}, "Access-Control-Allow-Credentials": {"true"}, "X-Request-Id": {"upstream-request"}}, Body: io.NopCloser(strings.NewReader(wire))}, nil
				})),
			})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-6-astra","input":"ok","stream":true}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Referer", "https://browser.example/private-page")
			request.Header.Set("Sec-Fetch-Site", "cross-site")
			if test.header != "" {
				request.Header.Set(test.header, test.value)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			response := recorder.Result()
			defer response.Body.Close()
			if response.StatusCode != test.status || response.Header.Get("Access-Control-Allow-Origin") != "*" || response.Header.Get("Access-Control-Expose-Headers") != "*" || response.Header.Get("Access-Control-Allow-Credentials") != "" {
				t.Fatalf("response=%d %v %s", response.StatusCode, response.Header, recorder.Body.String())
			}
			if test.status == 200 {
				if hits != 1 || recorder.Body.String() != wire || response.Header.Get("X-Request-Id") != "upstream-request" {
					t.Fatalf("hits=%d body=%s", hits, recorder.Body.String())
				}
			} else if hits != 0 {
				t.Fatalf("unauthenticated request reached upstream %d times", hits)
			}
		})
	}
}
