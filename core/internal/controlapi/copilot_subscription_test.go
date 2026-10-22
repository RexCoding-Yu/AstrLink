package controlapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/servicemodel"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

func TestCopilotSubscriptionDeviceCodeModelsUsageAndLogout(t *testing.T) {
	ctx := context.Background()
	var polls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.UserAgent() != "opencode/"+accountauth.DefaultCopilotClientVersion {
			t.Errorf("%s User-Agent = %q", r.URL.Path, r.UserAgent())
		}
		switch r.URL.Path {
		case "/login/device/code":
			io.WriteString(w, `{"device_code":"copilot-device-secret","user_code":"COPI-LOT1","verification_uri":"https://github.com/login/device","expires_in":899,"interval":0}`)
		case "/login/oauth/access_token":
			if polls.Add(1) == 1 {
				io.WriteString(w, `{"error":"authorization_pending"}`)
				return
			}
			io.WriteString(w, `{"access_token":"gho_copilot_secret","token_type":"bearer","scope":"read:user"}`)
		case "/user", "/copilot_internal/user":
			if r.Header.Get("Authorization") != "token gho_copilot_secret" {
				t.Errorf("%s Authorization = %q", r.URL.Path, r.Header.Get("Authorization"))
			}
			if r.URL.Path == "/user" {
				io.WriteString(w, `{"login":"octocat","id":583231,"email":"octocat@example.com"}`)
				return
			}
			io.WriteString(w, `{"copilot_plan":"individual_pro","quota_reset_date":"2099-11-01","quota_snapshots":{"premium_interactions":{"entitlement":1500,"remaining":1200,"percent_remaining":80,"unlimited":false},"chat":{"unlimited":true}}}`)
		case "/models":
			if r.Header.Get("Authorization") != "Bearer gho_copilot_secret" || r.Header.Get("X-Github-Api-Version") != "2026-06-01" ||
				r.Header.Get("ChatGPT-Account-ID") != "" || r.Header.Get("Anthropic-Beta") != "" {
				t.Errorf("wrong Copilot authentication on models: %v", r.Header)
			}
			io.WriteString(w, `{"object":"list","data":[{"id":"claude-sonnet-4.6","model_picker_enabled":true,"supported_endpoints":["/v1/messages","/chat/completions"]},{"id":"gpt-5.4","model_picker_enabled":true,"supported_endpoints":["/responses"]},{"id":"text-embedding-3-small","model_picker_enabled":false}]}`)
		default:
			t.Errorf("unexpected upstream request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	store, err := openControlTestStore(t, ctx, t.TempDir()+"/astrlink.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	credentials := accountauth.NewMemoryCredentialStore()
	manager, err := subscription.NewManager(subscription.StorageAccountStore{Store: store}, credentials,
		accountauth.OAuthConfig{HTTPClient: upstream.Client()},
		accountauth.OAuthConfig{Provider: contract.SubscriptionProviderGitHubCopilot, Issuer: upstream.URL, APIBaseURL: upstream.URL,
			UserInfoURL: upstream.URL + "/user", UsageURL: upstream.URL + "/copilot_internal/user",
			DevicePollMinInterval: 5 * time.Millisecond, DevicePollMaxInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("test", "abc1234"), Dependencies{
		ServiceStore: store, Subscriptions: manager, ServiceModels: servicemodel.New(store, manager, upstream.Client()), ControlToken: testControlToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string, status int) []byte {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+testControlToken)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: status=%d body=%s", method, path, w.Code, w.Body.String())
		}
		for _, secret := range []string{"gho_copilot_secret", "copilot-device-secret", "octocat@example.com"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("credential leaked in public response: %s", w.Body.String())
			}
		}
		return w.Body.Bytes()
	}
	var service contract.Service
	if err := json.Unmarshal(call("POST", ServicesPath, `{"name":"Copilot","kind":"copilot_subscription","responses_websocket_enabled":true}`, 201), &service); err != nil {
		t.Fatal(err)
	}
	if service.Subscription.Provider != contract.SubscriptionProviderGitHubCopilot || len(service.Capabilities) != 4 || service.ResponsesWebSocket() {
		t.Fatalf("wrong provider configuration: %#v", service)
	}
	path := ServicesPath + "/" + string(service.ID)
	call("POST", path+"/authorization", `{"flow":"browser"}`, 422)
	call("POST", path+"/authorization", `{"flow":"authorization_code"}`, 422)
	var session contract.AuthorizationSession
	if err := json.Unmarshal(call("POST", path+"/authorization", `{"flow":"device_code"}`, 202), &session); err != nil {
		t.Fatal(err)
	}
	if session.Provider != contract.SubscriptionProviderGitHubCopilot || session.DeviceCode == nil ||
		session.DeviceCode.UserCode != "COPI-LOT1" || session.DeviceCode.VerificationURL != "https://github.com/login/device" {
		t.Fatalf("invalid authorization session: %#v", session)
	}
	for {
		if err := json.Unmarshal(call("GET", path+"/authorization", "", 200), &session); err != nil {
			t.Fatal(err)
		}
		if session.Status == contract.AuthorizationSessionStatusCompleted {
			break
		}
		if session.Status != contract.AuthorizationSessionStatusPending {
			t.Fatalf("device session did not complete: %#v", session)
		}
		time.Sleep(5 * time.Millisecond)
	}
	var connected contract.Service
	if err := json.Unmarshal(call("GET", path, "", 200), &connected); err != nil {
		t.Fatal(err)
	}
	if connected.Subscription.Status != contract.SubscriptionStatusConnected || connected.Subscription.ProviderAccountID != "583231" {
		t.Fatalf("connected service = %#v", connected.Subscription)
	}
	models := string(call("POST", path+"/probe-models", `{"protocol":"openai.models"}`, 200))
	if !strings.Contains(models, "claude-sonnet-4.6") || !strings.Contains(models, "gpt-5.4") || strings.Contains(models, "text-embedding") {
		t.Fatalf("model probe = %s", models)
	}
	usageRaw := call("GET", path+"/usage", "", 200)
	var usage contract.SubscriptionUsage
	if err := json.Unmarshal(usageRaw, &usage); err != nil || usage.PlanType != "individual_pro" || usage.Primary == nil ||
		usage.Primary.UsedPercent != 20 || usage.Primary.ResetAt == nil || len(usage.AdditionalRateLimits) != 0 {
		t.Fatalf("invalid Copilot usage: %s", usageRaw)
	}
	call("POST", path+"/usage/reset", "", 502)
	call("POST", path+"/logout", "", 200)
	if _, err := credentials.Get(ctx, service.ID); err == nil {
		t.Fatal("logout retained credentials")
	}
	if _, err := manager.AccessToken(ctx, service.ID); err == nil {
		t.Fatal("logged out account remains usable")
	}
}
