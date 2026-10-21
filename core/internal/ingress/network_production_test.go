package ingress

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/gorilla/websocket"
)

func TestNetworkProductionRequiresTheSameDependenciesAndNoHostPin(t *testing.T) {
	complete := Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{}, endpoint.ErrNoEndpoint
		}),
		Authorizer: authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) { return nil, nil }),
		AccessTokenAuthenticator: AccessTokenAuthenticatorFunc(func(context.Context, string) (contract.AccessTokenID, error) {
			return "", errors.New("not found")
		}),
	}
	if _, err := NewNetworkProduction(complete); err != nil {
		t.Fatalf("NewNetworkProduction: %v", err)
	}
	withoutToken := complete
	withoutToken.AccessTokenAuthenticator = nil
	withoutResolver := complete
	withoutResolver.Resolver = nil
	pinned := complete
	pinned.AllowedHost = "127.0.0.1:8317"
	for name, dependencies := range map[string]Dependencies{
		"no access token authenticator": withoutToken,
		"no resolver":                   withoutResolver,
		"loopback Host pin":             pinned,
	} {
		if _, err := NewNetworkProduction(dependencies); err == nil {
			t.Errorf("%s: NewNetworkProduction accepted it", name)
		}
	}
}

func TestNetworkProductionAcceptsAnyHostAndOriginButRequiresToken(t *testing.T) {
	const token = "astr_0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"
	store := &memoryRequestRecordStore{}
	resolveCalls := 0
	handler, err := NewNetworkProduction(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			resolveCalls++
			return endpoint.Resolved{}, endpoint.ErrNoEndpoint
		}),
		Authorizer: authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) {
			return nil, errors.New("authorizer must not run without an endpoint")
		}),
		AccessTokenAuthenticator: AccessTokenAuthenticatorFunc(func(_ context.Context, presented string) (contract.AccessTokenID, error) {
			if presented == token {
				return "token_primary", nil
			}
			return "", errors.New("not found")
		}),
		RequestRecords: store,
	})
	if err != nil {
		t.Fatalf("NewNetworkProduction: %v", err)
	}
	tests := []struct {
		name          string
		host          string
		origin        string
		authorization string
		query         string
		status        int
		code          string
		resolves      bool
	}{
		{name: "LAN address", host: "192.168.1.20:8317", authorization: "Bearer " + token, status: http.StatusUnprocessableEntity, code: "missing_protocol_capability", resolves: true},
		{name: "container name", host: "astrlink:8317", authorization: "Bearer " + token, status: http.StatusUnprocessableEntity, code: "missing_protocol_capability", resolves: true},
		{name: "proxied hostname", host: "llm.example.com", authorization: "Bearer " + token, status: http.StatusUnprocessableEntity, code: "missing_protocol_capability", resolves: true},
		{name: "browser Origin", host: "llm.example.com", origin: "https://browser.example", authorization: "Bearer " + token, status: http.StatusUnprocessableEntity, code: "missing_protocol_capability", resolves: true},
		{name: "browser missing token", host: "llm.example.com", origin: "https://browser.example", status: http.StatusUnauthorized, code: "invalid_access_token"},
		{name: "missing token", host: "llm.example.com", status: http.StatusUnauthorized, code: "invalid_access_token"},
		{name: "wrong token", host: "llm.example.com", authorization: "Bearer wrong_token_0123456789abcdef", status: http.StatusUnauthorized, code: "invalid_access_token"},
		{name: "query token", host: "llm.example.com", query: "?key=" + token, status: http.StatusUnauthorized, code: "token_query_forbidden"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := resolveCalls
			request := httptest.NewRequest(http.MethodPost, "/v1/responses"+test.query, strings.NewReader(`{"model":"m","input":"hi"}`))
			request.Host = test.host
			request.Header.Set("Content-Type", "application/json")
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.authorization != "" {
				request.Header.Set("Authorization", test.authorization)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			assertInferenceError(t, response, test.status, test.code)
			if got := resolveCalls > before; got != test.resolves {
				t.Fatalf("resolver called=%t, want %t", got, test.resolves)
			}
		})
	}
	// Only the four authenticated requests reach the records; nothing an
	// anonymous caller sends is persisted.
	if len(store.records) != 4 {
		t.Fatalf("records = %d, want only the authenticated requests: %#v", len(store.records), store.records)
	}
	for _, record := range store.records {
		if record.LocalAccessTokenID == nil || *record.LocalAccessTokenID != "token_primary" {
			t.Fatalf("unauthenticated request was recorded: %#v", record)
		}
	}
}

func TestNetworkProductionResponsesWebSocketAcceptsAuthenticatedOrigin(t *testing.T) {
	store := &memoryRequestRecordStore{}
	handler, err := NewNetworkProduction(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			t.Error("an upgrade without a response.create must not resolve")
			return endpoint.Resolved{}, endpoint.ErrNoEndpoint
		}),
		Authorizer: authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) { return nil, nil }),
		AccessTokenAuthenticator: AccessTokenAuthenticatorFunc(func(context.Context, string) (contract.AccessTokenID, error) {
			return "token_primary", nil
		}),
		RequestRecords: store,
	})
	if err != nil {
		t.Fatalf("NewNetworkProduction: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	header := http.Header{"Origin": {"https://browser.example"}, "Authorization": {"Bearer astr_0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"}}
	client, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", header)
	if err != nil {
		t.Fatalf("authenticated browser upgrade: %v (response=%v)", err, response)
	}
	defer client.Close()
	if len(store.records) != 0 {
		t.Fatalf("upgrade without a turn was recorded: %#v", store.records)
	}
}
