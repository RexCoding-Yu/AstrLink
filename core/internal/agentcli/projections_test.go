package agentcli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/controlapi"
)

// Values that must never reach an agent through the CLI.
var projectionSecrets = []string{
	"credential_ref", "keychain://", "sk-path-key", "proxy-user", "proxy.internal",
	"acct_provider_1", "provider_account_id", "corp.example.net", "10.0.0.0/8",
	"secret-literal", "SECRET", "/v1",
}

func newProjectionClient(t *testing.T) (*Client, *http.ServeMux) {
	t.Helper()
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client, err := Dial(DialOptions{ControlURL: server.URL, ControlToken: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	return client, mux
}

func assertNoProjectionSecrets(t *testing.T, raw json.RawMessage) {
	t.Helper()
	for _, secret := range projectionSecrets {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("projection leaked %q: %s", secret, raw)
		}
	}
}

func projectionServices() []any {
	return []any{
		map[string]any{
			"id": "svc_http", "name": "Gateway", "kind": "openai", "enabled": true,
			"models": []any{"gpt-5"}, "capabilities": []any{map[string]any{"protocol": "openai_responses", "mode": "native", "streaming": true}},
			"http": map[string]any{
				"base_url":       "https://proxy-user:pw@api.example.com:8443/v1/sk-path-key?token=sk-path-key",
				"auth":           map[string]any{"scheme": "bearer"},
				"credential_ref": "keychain://astrlink/svc_http",
			},
			"proxy":      map[string]any{"mode": "custom", "url": "http://proxy-user:pw@proxy.internal:3128", "credential_ref": "keychain://astrlink/proxy"},
			"created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z",
		},
		map[string]any{
			"id": "svc_sub", "name": "Codex", "kind": "codex_subscription", "enabled": true,
			"models": []any{}, "capabilities": []any{},
			"subscription": map[string]any{
				"provider": "codex", "status": "connected", "account_hint": "a***@example.org",
				"provider_account_id": "acct_provider_1", "credential_ref": "keychain://astrlink/svc_sub",
				"authorization_boundary": "subscription",
				"risk":                   map[string]any{"state": "paused", "code": "rate_limited", "occurrences": 2},
			},
			"created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z",
		},
	}
}

func TestListServicesProjectsWithoutCredentials(t *testing.T) {
	client, mux := newProjectionClient(t)
	pages := 0
	mux.HandleFunc(controlapi.ServicesPath, func(writer http.ResponseWriter, request *http.Request) {
		pages++
		services := projectionServices()
		if request.URL.Query().Get("cursor") == "" {
			writeJSON(writer, map[string]any{"items": services[:1], "next_cursor": "page2"})
			return
		}
		if request.URL.Query().Get("cursor") != "page2" {
			t.Errorf("cursor = %q", request.URL.Query().Get("cursor"))
		}
		writeJSON(writer, map[string]any{"items": services[1:], "next_cursor": nil})
	})

	raw, err := callCommand(context.Background(), client, "services", nil)
	if err != nil {
		t.Fatal(err)
	}
	assertNoProjectionSecrets(t, raw)
	if pages != 2 {
		t.Fatalf("pages = %d, want 2", pages)
	}
	var decoded struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Items) != 2 {
		t.Fatalf("items = %s", raw)
	}
	if origin := decoded.Items[0]["base_origin"]; origin != "https://api.example.com:8443" {
		t.Fatalf("base_origin = %v", origin)
	}
	subscription := decoded.Items[1]["subscription"].(map[string]any)
	if subscription["status"] != "connected" || subscription["account_hint"] != "a***@example.org" {
		t.Fatalf("subscription = %#v", subscription)
	}
	if subscription["risk"].(map[string]any)["state"] != "paused" {
		t.Fatalf("risk = %#v", subscription["risk"])
	}
}

func TestURLOriginDropsEverythingButSchemeAndHost(t *testing.T) {
	for raw, want := range map[string]string{
		"https://user:pw@api.example.com:8443/v1/key?x=1#frag": "https://api.example.com:8443",
		"http://127.0.0.1:11434":                               "http://127.0.0.1:11434",
		"not a url":                                            "",
		"/relative/path":                                       "",
	} {
		if got := urlOrigin(raw); got != want {
			t.Fatalf("urlOrigin(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestGetServiceStatusSummarizesRecentRequests(t *testing.T) {
	client, mux := newProjectionClient(t)
	mux.HandleFunc(controlapi.ServicesPath+"/svc_sub", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, projectionServices()[1])
	})
	riskCalls := 0
	mux.HandleFunc(controlapi.ServicesPath+"/svc_sub/risk-events", func(writer http.ResponseWriter, request *http.Request) {
		riskCalls++
		if request.URL.Query().Get("limit") != "5" {
			t.Errorf("risk limit = %q", request.URL.Query().Get("limit"))
		}
		writeJSON(writer, map[string]any{"items": []any{map[string]any{
			"id": 1, "service_id": "svc_sub", "kind": "rate_limited", "observed_at": "2026-01-02T00:00:00Z",
		}}})
	})
	mux.HandleFunc(controlapi.RequestsPath, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("service_id") != "svc_sub" || request.URL.Query().Get("limit") != "20" {
			t.Errorf("requests query = %s", request.URL.RawQuery)
		}
		writeJSON(writer, map[string]any{"items": []any{
			map[string]any{"id": "req_3", "status": "failed", "http_status": 429, "started_at": "2026-01-03T00:00:00Z",
				"error": map[string]any{"category": "upstream", "code": "rate_limited", "message": "slow down", "retryable": true}},
			map[string]any{"id": "req_2", "status": "succeeded", "http_status": 200, "started_at": "2026-01-02T00:00:00Z"},
			map[string]any{"id": "req_1", "status": "failed", "http_status": 500, "started_at": "2026-01-01T00:00:00Z"},
		}})
	})
	mux.HandleFunc(controlapi.ServicesPath+"/svc_usage_must_not_be_called/usage", func(http.ResponseWriter, *http.Request) {
		t.Error("service status must not call the usage endpoint")
	})

	raw, err := callCommand(context.Background(), client, "service", map[string]any{"id": "svc_sub"})
	if err != nil {
		t.Fatal(err)
	}
	assertNoProjectionSecrets(t, raw)
	if riskCalls != 1 {
		t.Fatalf("risk calls = %d", riskCalls)
	}
	var decoded struct {
		RecentRequests struct {
			Sampled       int            `json:"sampled"`
			ByStatus      map[string]int `json:"by_status"`
			LastSuccessAt string         `json:"last_success_at"`
			LastFailure   struct {
				RequestID  string `json:"request_id"`
				HTTPStatus int    `json:"http_status"`
				Error      struct {
					Code string `json:"code"`
				} `json:"error"`
			} `json:"last_failure"`
		} `json:"recent_requests"`
		RiskEvents []map[string]any `json:"risk_events"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	recent := decoded.RecentRequests
	if recent.Sampled != 3 || recent.ByStatus["failed"] != 2 || recent.ByStatus["succeeded"] != 1 {
		t.Fatalf("recent = %+v", recent)
	}
	if recent.LastFailure.RequestID != "req_3" || recent.LastFailure.HTTPStatus != 429 || recent.LastFailure.Error.Code != "rate_limited" {
		t.Fatalf("last failure = %+v", recent.LastFailure)
	}
	if !strings.HasPrefix(recent.LastSuccessAt, "2026-01-02") {
		t.Fatalf("last success = %q", recent.LastSuccessAt)
	}
	if len(decoded.RiskEvents) != 1 {
		t.Fatalf("risk events = %s", raw)
	}
}

func TestGetServiceStatusToleratesUnavailableRiskEvents(t *testing.T) {
	client, mux := newProjectionClient(t)
	mux.HandleFunc(controlapi.ServicesPath+"/svc_sub", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, projectionServices()[1])
	})
	mux.HandleFunc(controlapi.ServicesPath+"/svc_sub/risk-events", func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, `{"error":{"code":"subscription_unavailable"}}`, http.StatusServiceUnavailable)
	})
	mux.HandleFunc(controlapi.RequestsPath, func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, map[string]any{"items": []any{}})
	})
	raw, err := callCommand(context.Background(), client, "service", map[string]any{"id": "svc_sub"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "risk_events") {
		t.Fatalf("unavailable risk events should be omitted: %s", raw)
	}
}

// projectionPolicy is a full policy whose allowlist and regex patterns
// carry values no agent may read.
func projectionPolicy() map[string]any {
	return map[string]any{
		"id": "policy_default", "name": "Default", "enabled": true, "priority": 0,
		"detector": "regex", "local_model_id": nil, "min_confidence": 0.5, "regex_source": "builtin",
		"custom_regex_rules": []any{
			map[string]any{"kind": "EMPLOYEE_ID", "pattern": `\bSECRET\d+`},
			map[string]any{"kind": "EMPLOYEE_ID", "pattern": `\bSECRET\d+x`},
		},
		"kind_rules": []any{
			map[string]any{"kind": "EMAIL", "enabled": true, "style": "placeholder"},
			map[string]any{"kind": "PHONE", "enabled": false, "style": "placeholder"},
		},
		"allowlist_rules": []any{
			map[string]any{"type": "literal", "value": "secret-literal"},
			map[string]any{"type": "domain_suffix", "value": "corp.example.net"},
			map[string]any{"type": "cidr", "value": "10.0.0.0/8"},
			map[string]any{"type": "literal", "value": "secret-literal-2"},
		},
		"match":          map[string]any{"protocols": []any{}, "models": []any{}, "service_ids": []any{}},
		"request_action": "redact", "response_action": "restore",
		"response_restore": true, "restore_tool_arguments": true,
	}
}

func TestGetPrivacyPolicyReportsCountsNotValues(t *testing.T) {
	client, mux := newProjectionClient(t)
	encoded, err := json.Marshal(projectionPolicy())
	if err != nil {
		t.Fatal(err)
	}
	var full contract.Policy
	if err := json.Unmarshal(encoded, &full); err != nil {
		t.Fatal(err)
	}
	// Core answers the observer token the CLI holds with the summary.
	mux.HandleFunc(controlapi.PoliciesPath, func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, map[string]any{"items": []any{controlapi.SummarizePolicy(full)}, "next_cursor": nil})
	})
	raw, err := callCommand(context.Background(), client, "privacy", nil)
	if err != nil {
		t.Fatal(err)
	}
	assertNoProjectionSecrets(t, raw)
	var decoded struct {
		Items []struct {
			CustomRegexRules struct {
				Count int      `json:"count"`
				Kinds []string `json:"kinds"`
			} `json:"custom_regex_rules"`
			EnabledKinds []string `json:"enabled_kinds"`
			Allowlist    struct {
				Count  int            `json:"count"`
				ByType map[string]int `json:"by_type"`
			} `json:"allowlist"`
			RequestAction string `json:"request_action"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	policy := decoded.Items[0]
	if policy.CustomRegexRules.Count != 2 || len(policy.CustomRegexRules.Kinds) != 1 || policy.CustomRegexRules.Kinds[0] != "EMPLOYEE_ID" {
		t.Fatalf("custom regex = %+v", policy.CustomRegexRules)
	}
	if len(policy.EnabledKinds) != 1 || policy.EnabledKinds[0] != "EMAIL" {
		t.Fatalf("enabled kinds = %v", policy.EnabledKinds)
	}
	if policy.Allowlist.Count != 4 || policy.Allowlist.ByType["literal"] != 2 || policy.Allowlist.ByType["cidr"] != 1 {
		t.Fatalf("allowlist = %+v", policy.Allowlist)
	}
	if policy.RequestAction != "redact" {
		t.Fatalf("request action = %q", policy.RequestAction)
	}
}

func TestGetPrivacyPolicyRefusesAFullPolicy(t *testing.T) {
	client, mux := newProjectionClient(t)
	mux.HandleFunc(controlapi.PoliciesPath, func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, map[string]any{"items": []any{projectionPolicy()}, "next_cursor": nil})
	})
	raw, err := callCommand(context.Background(), client, "privacy", nil)
	if err == nil {
		t.Fatalf("a full policy passed through: %s", raw)
	}
	assertNoProjectionSecrets(t, json.RawMessage(err.Error()))
}

func TestSearchRequestsForwardsQuery(t *testing.T) {
	client, mux := newProjectionClient(t)
	mux.HandleFunc(controlapi.RequestsPath, func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		if query.Get("q") != "50% off_sale" || query.Get("status") != "failed" {
			t.Errorf("query = %s", request.URL.RawQuery)
		}
		writeJSON(writer, map[string]any{"items": []any{}, "next_cursor": nil})
	})
	if _, err := callCommand(context.Background(), client, "search", map[string]any{"q": "50% off_sale", "status": "failed"}); err != nil {
		t.Fatal(err)
	}
	for _, arguments := range []map[string]any{{}, {"q": "  "}, {"q": 3}} {
		if _, err := callCommand(context.Background(), client, "search", arguments); err == nil {
			t.Fatalf("search(%v) accepted", arguments)
		}
	}
}

func TestExplainRequestSummarizesAttempts(t *testing.T) {
	client, mux := newProjectionClient(t)
	mux.HandleFunc(controlapi.RequestsPath+"/req_1", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, map[string]any{
			"id": "req_1", "status": "failed", "http_status": 502, "service_id": "svc_http",
			"child_count": 2, "started_at": "2026-01-01T00:00:00Z",
			"routing_decision": map[string]any{"selected": "failover", "skipped": []any{}},
			"error":            map[string]any{"category": "upstream", "code": "bad_gateway", "message": "x", "retryable": false},
			"audit":            map[string]any{"request_body_captured": true},
		})
	})
	childCalls := 0
	mux.HandleFunc(controlapi.RequestsPath+"/req_1/children", func(writer http.ResponseWriter, _ *http.Request) {
		childCalls++
		writeJSON(writer, map[string]any{"items": []any{map[string]any{"id": "req_1a"}, map[string]any{"id": "req_1b"}}})
	})
	mux.HandleFunc(controlapi.RequestsPath+"/req_1/audit", func(http.ResponseWriter, *http.Request) {
		t.Error("explain_request must not fetch audit bodies")
	})
	raw, err := callCommand(context.Background(), client, "explain", map[string]any{"id": "req_1"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Summary map[string]any `json:"summary"`
		Audit   map[string]any `json:"audit"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if childCalls != 1 || decoded.Summary["attempts"] != float64(3) || decoded.Summary["routing_selected"] != "failover" {
		t.Fatalf("summary = %s", raw)
	}
	if decoded.Audit["bodies_captured"] != true {
		t.Fatalf("audit = %#v", decoded.Audit)
	}
}
