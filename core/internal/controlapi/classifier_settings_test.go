package controlapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

const classifierAPIKey = "sk-classifier-control-test-key"

func putClassifierCredential(t *testing.T, handler *Handler) {
	t.Helper()
	response := controlRequest(t, handler, http.MethodPut, ClassifierSettingsPath+"/credential",
		"application/json", `{"secret":"`+classifierAPIKey+`"}`, "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"configured":true`) {
		t.Fatalf("credential put = %d %s", response.Code, response.Body.String())
	}
}

func TestClassifierSettingsResourceRoundTrip(t *testing.T) {
	store, handler := newRouteHandler(t)

	response := controlRequest(t, handler, http.MethodGet, ClassifierSettingsPath, "", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("get = %d %s", response.Code, response.Body.String())
	}
	var initial struct {
		Enabled              bool `json:"enabled"`
		CredentialConfigured bool `json:"credential_configured"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	if initial.Enabled || initial.CredentialConfigured {
		t.Fatalf("initial = %+v", initial)
	}

	putClassifierCredential(t, handler)

	document := `{"enabled":true,"classifier_source":"inline","inline":{"protocol":"jev","endpoint":"https://api.typesafe.ai/v1/systemone","credential_ref":"local://classifier/default","model":"jev-latest","timeout_ms":1500}}`
	response = controlRequest(t, handler, http.MethodPut, ClassifierSettingsPath, "application/json", document, "")
	if response.Code != http.StatusOK {
		t.Fatalf("put = %d %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if strings.Contains(body, classifierAPIKey) {
		t.Fatalf("settings response leaks the API key: %s", body)
	}
	if !strings.Contains(body, `"credential_configured":true`) {
		t.Fatalf("credential status missing: %s", body)
	}
	stored, err := store.GetClassifierSettings(context.Background())
	if err != nil || !stored.CloudConfigured() {
		t.Fatalf("stored settings = %+v %v", stored, err)
	}

	response = controlRequest(t, handler, http.MethodDelete, ClassifierSettingsPath+"/credential", "", "", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"configured":false`) {
		t.Fatalf("credential delete = %d %s", response.Code, response.Body.String())
	}
}

func TestClassifierSettingsValidation(t *testing.T) {
	_, handler := newRouteHandler(t)
	tests := []struct {
		name   string
		status int
		body   string
		code   string
	}{
		{
			name:   "enabled provider_ref is not supported",
			status: http.StatusUnprocessableEntity,
			body:   `{"enabled":true,"classifier_source":"provider_ref","provider_ref":{"ref":"local://classifier-profile/x"}}`,
			code:   "classifier_source_not_supported",
		},
		{
			name:   "enabled inline missing endpoint",
			status: http.StatusUnprocessableEntity,
			body:   `{"enabled":true,"classifier_source":"inline","inline":{"protocol":"jev","credential_ref":"local://classifier/default","model":"jev-latest"}}`,
			code:   "invalid_classifier_settings",
		},
		{
			name:   "endpoint with userinfo",
			status: http.StatusUnprocessableEntity,
			body:   `{"enabled":true,"classifier_source":"inline","inline":{"protocol":"jev","endpoint":"https://user:pass@api.example/v1","credential_ref":"local://classifier/default","model":"jev-latest"}}`,
			code:   "invalid_classifier_settings",
		},
		{
			name:   "unknown prompt placeholder",
			status: http.StatusUnprocessableEntity,
			body:   `{"enabled":true,"classifier_source":"inline","inline":{"protocol":"jev","endpoint":"https://api.example/v1","credential_ref":"local://classifier/default","model":"jev-latest","prompt":"pick {{model}}"}}`,
			code:   "invalid_classifier_settings",
		},
		{
			name:   "timeout beyond cap",
			status: http.StatusUnprocessableEntity,
			body:   `{"enabled":true,"classifier_source":"inline","inline":{"protocol":"jev","endpoint":"https://api.example/v1","credential_ref":"local://classifier/default","model":"jev-latest","timeout_ms":6000}}`,
			code:   "invalid_classifier_settings",
		},
		{
			// Strict decoding rejects unknown keys before validation runs.
			name:   "unknown field rejected",
			status: http.StatusBadRequest,
			body:   `{"enabled":false,"classifier_source":"inline","api_key":"nope"}`,
			code:   "invalid_json",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := controlRequest(t, handler, http.MethodPut, ClassifierSettingsPath, "application/json", test.body, "")
			if response.Code != test.status {
				t.Fatalf("put = %d %s", response.Code, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), test.code) {
				t.Fatalf("put code: %s", response.Body.String())
			}
		})
	}
}

func TestClassifierCredentialValidation(t *testing.T) {
	_, handler := newRouteHandler(t)
	// The wrong-type case fails strict decoding before validation runs.
	for name, body := range map[string]string{
		"empty": `{"secret":""}`,
		// The JSON escape decodes to a real newline, which the credential
		// itself must reject.
		"newlines": `{"secret":"line1\nline2"}`,
		"too long": `{"secret":"` + strings.Repeat("x", 16385) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			response := controlRequest(t, handler, http.MethodPut, ClassifierSettingsPath+"/credential", "application/json", body, "")
			if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "invalid_credential") {
				t.Fatalf("credential put = %d %s", response.Code, response.Body.String())
			}
		})
	}
	t.Run("wrong type", func(t *testing.T) {
		response := controlRequest(t, handler, http.MethodPut, ClassifierSettingsPath+"/credential", "application/json", `{"secret":42}`, "")
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_json") {
			t.Fatalf("credential put = %d %s", response.Code, response.Body.String())
		}
	})
}

// The dry-run answers even when nothing is configured, and its sample text
// passes the production privacy filter before any cloud call happens.
func TestClassifierDryRunSanitizesBeforeCloudCall(t *testing.T) {
	var cloudBodies []string
	cloudServer := newClassifierCloudServer(t, `{"answers":{"task_type":{"choice":"coding","confidence":0.9}}}`, &cloudBodies)
	handler := newClassifierDryRunHandler(t, cloudServer.URL, privacy.Policy{Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact})
	putClassifierCredential(t, handler)

	response := controlRequest(t, handler, http.MethodPost, ClassifierSettingsPath+"/dry-run", "application/json",
		`{"text":"write go code and email alice@example.com today"}`, "")
	if response.Code != http.StatusOK {
		t.Fatalf("dry-run = %d %s", response.Code, response.Body.String())
	}
	var result struct {
		Category         string  `json:"category"`
		Confidence       float64 `json:"confidence"`
		Privacy          string  `json:"privacy"`
		SanitizedPreview string  `json:"sanitized_preview"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Category != "coding" || result.Confidence != 0.9 || result.Privacy != "redact" {
		t.Fatalf("dry-run result = %+v", result)
	}
	if strings.Contains(result.SanitizedPreview, "alice@example.com") {
		t.Fatalf("sanitized preview leaks PII: %q", result.SanitizedPreview)
	}
	if len(cloudBodies) != 1 {
		t.Fatalf("cloud calls = %d", len(cloudBodies))
	}
	if strings.Contains(cloudBodies[0], "alice@example.com") {
		t.Fatalf("cloud received unredacted text: %s", cloudBodies[0])
	}
}

func TestClassifierDryRunBlockedPolicyMakesNoCloudCall(t *testing.T) {
	var cloudBodies []string
	cloudServer := newClassifierCloudServer(t, `{}`, &cloudBodies)
	handler := newClassifierDryRunHandler(t, cloudServer.URL, privacy.Policy{Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionBlock})
	putClassifierCredential(t, handler)
	response := controlRequest(t, handler, http.MethodPost, ClassifierSettingsPath+"/dry-run", "application/json",
		`{"text":"email alice@example.com"}`, "")
	if response.Code != http.StatusOK {
		t.Fatalf("dry-run = %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"privacy":"blocked"`) || !strings.Contains(response.Body.String(), `"fallback_reason":"privacy_blocked"`) {
		t.Fatalf("dry-run result = %s", response.Body.String())
	}
	if len(cloudBodies) != 0 {
		t.Fatalf("blocked dry-run still called the cloud: %v", cloudBodies)
	}
}

// Without the sealed credential the dry-run still answers 200 with the
// deterministic fallback; no cloud call happens.
func TestClassifierDryRunUnconfiguredAnswersFallback(t *testing.T) {
	var cloudBodies []string
	cloudServer := newClassifierCloudServer(t, `{}`, &cloudBodies)
	handler := newClassifierDryRunHandler(t, cloudServer.URL, privacy.Policy{})
	response := controlRequest(t, handler, http.MethodPost, ClassifierSettingsPath+"/dry-run", "application/json",
		`{"text":"hello"}`, "")
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"fallback_reason":"classifier_unavailable"`) {
		t.Fatalf("dry-run = %d %s", response.Code, response.Body.String())
	}
	if len(cloudBodies) != 0 {
		t.Fatalf("credential-less dry-run still called the cloud: %v", cloudBodies)
	}
}

func TestClassifierDryRunGroupDecision(t *testing.T) {
	var cloudBodies []string
	cloudServer := newClassifierCloudServer(t, `{"answers":{"task_type":{"choice":"coding","confidence":0.95}}}`, &cloudBodies)
	handler := newClassifierDryRunHandler(t, cloudServer.URL, privacy.Policy{})
	putClassifierCredential(t, handler)
	store := handler.serviceStore.(storage.ServiceStore)
	service := contract.Service{
		ID: "service_01", Name: "Classifier dry-run service", Kind: contract.ServiceKindOpenAI, Enabled: true,
		Models: []string{"fast-model", "strong-model"},
		HTTP:   &contract.HTTPConnection{BaseURL: "https://example.test", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeNone}},
		Capabilities: []contract.Capability{{
			Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true,
		}},
	}
	if _, err := store.CreateService(context.Background(), service, storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	group := contract.ModelRoutingGroup{
		ID: "superclaude", Enabled: true,
		Members: []contract.ModelRoutingMember{
			{ID: "fast", ServiceID: "service_01", Model: "fast-model"},
			{ID: "strong", ServiceID: "service_01", Model: "strong-model"},
		},
		DefaultMemberID: "fast",
		Rules: []contract.ModelRoutingRule{{
			ID: "coding", Priority: 1,
			When:           contract.ModelRoutingCondition{Field: "task_type", In: []string{"coding"}},
			TargetMemberID: "strong",
		}},
	}
	if err := handler.routingSettings.UpdateRoutingSettings(context.Background(), contractRoutingSettingsWithGroup(group)); err != nil {
		t.Fatal(err)
	}
	response := controlRequest(t, handler, http.MethodPost, ClassifierSettingsPath+"/dry-run", "application/json",
		`{"text":"write go code","group_id":"superclaude"}`, "")
	if response.Code != http.StatusOK {
		t.Fatalf("dry-run = %d %s", response.Code, response.Body.String())
	}
	var result struct {
		Decision struct {
			MemberID string `json:"member_id"`
			Model    string `json:"model"`
			RuleID   string `json:"rule_id"`
		} `json:"decision"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Decision.MemberID != "strong" || result.Decision.Model != "strong-model" || result.Decision.RuleID != "coding" {
		t.Fatalf("dry-run decision = %+v", result.Decision)
	}
}

func contractRoutingSettingsWithGroup(group contract.ModelRoutingGroup) contract.RoutingSettings {
	settings := contract.DefaultRoutingSettings()
	settings.ModelRoutingGroups = []contract.ModelRoutingGroup{group}
	return settings
}

func newClassifierCloudServer(t *testing.T, responseBody string, bodies *[]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		data, _ := io.ReadAll(request.Body)
		*bodies = append(*bodies, string(data))
		_, _ = writer.Write([]byte(responseBody))
	}))
	t.Cleanup(server.Close)
	return server
}

func newClassifierDryRunHandler(t *testing.T, cloudURL string, policy privacy.Policy) *Handler {
	t.Helper()
	store, err := sqlite.Open(context.Background(), t.TempDir()+"/classifier-dry-run.db")
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	filter, err := privacy.New(privacy.PolicyProviderFunc(func(context.Context, privacy.Scope) (privacy.Policy, error) {
		return policy, nil
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewWithDependencies(
		contract.DefaultVersionResponse("0.1.0-test", "abc1234"),
		Dependencies{
			ServiceStore:  store,
			PrivacyFilter: filter,
			ControlToken:  testControlToken,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	timeout := 2000
	settings := contract.ClassifierSettings{
		Enabled:          true,
		ClassifierSource: contract.ClassifierSourceInline,
		Inline: &contract.ClassifierInlineSource{
			Protocol:      contract.ClassifierProtocolJev,
			Endpoint:      cloudURL,
			CredentialRef: contract.ClassifierCredentialRef,
			Model:         "jev-latest",
			TimeoutMS:     &timeout,
		},
	}
	if err := store.UpdateClassifierSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	return handler
}

// Only one dry-run may run at a time: a concurrent request answers 429
// instead of piling cloud calls.
func TestClassifierDryRunRejectsConcurrentSecondCall(t *testing.T) {
	var cloudBodies []string
	cloudServer := newClassifierCloudServer(t, `{}`, &cloudBodies)
	handler := newClassifierDryRunHandler(t, cloudServer.URL, privacy.Policy{})
	putClassifierCredential(t, handler)
	select {
	case dryRunSlots <- struct{}{}:
	default:
		t.Fatal("dry-run slot unexpectedly occupied")
	}
	defer func() { <-dryRunSlots }()
	response := controlRequest(t, handler, http.MethodPost, ClassifierSettingsPath+"/dry-run", "application/json",
		`{"text":"hello"}`, "")
	if response.Code != http.StatusTooManyRequests || !strings.Contains(response.Body.String(), "dry_run_busy") {
		t.Fatalf("dry-run = %d %s", response.Code, response.Body.String())
	}
	if len(cloudBodies) != 0 {
		t.Fatalf("busy dry-run still called the cloud: %v", cloudBodies)
	}
}

// The dry-run failure mapping mirrors production routing through the shared
// FailureTaskType: stable record codes, never raw provider reasons, and the
// configured fallback (skip/error/general) applies identically.
// The dry-run reports a failure through the same stable record code the
// inference path writes, and never substitutes a label — there is no fallback
// to configure, so the same sample always produces the same reason. A stored
// legacy `fallback` member must not change any of that.
func TestClassifierDryRunFailureMappingMirrorsProduction(t *testing.T) {
	var cloudBodies []string
	cloudServer := newClassifierCloudServer(t, "not-a-jev-answer", &cloudBodies)
	handler := newClassifierDryRunHandler(t, cloudServer.URL, privacy.Policy{})
	putClassifierCredential(t, handler)
	settingsDocument := func(legacyFallback string) string {
		document := `{"enabled":true,"classifier_source":"inline","inline":{"protocol":"jev","endpoint":"` + cloudServer.URL +
			`","credential_ref":"local://classifier/default","model":"jev-latest","timeout_ms":1500`
		if legacyFallback != "" {
			document += `,"fallback":"` + legacyFallback + `"`
		}
		return document + `}}`
	}
	for _, testCase := range []struct {
		name           string
		legacyFallback string
	}{
		{name: "no stored fallback"},
		{name: "stored skip"},
		{name: "stored error is ignored"},
		{name: "stored general is ignored"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response := controlRequest(t, handler, http.MethodPut, ClassifierSettingsPath, "application/json", settingsDocument(testCase.legacyFallback), "")
			if response.Code != http.StatusOK {
				t.Fatalf("settings put = %d %s", response.Code, response.Body.String())
			}
			cloudBodies = nil
			response = controlRequest(t, handler, http.MethodPost, ClassifierSettingsPath+"/dry-run", "application/json", `{"text":"hello"}`, "")
			if response.Code != http.StatusOK {
				t.Fatalf("dry-run = %d %s", response.Code, response.Body.String())
			}
			body := response.Body.String()
			if !strings.Contains(body, `"fallback_reason":"classifier_invalid_output"`) {
				t.Fatalf("stable failure code missing: %s", body)
			}
			if strings.Contains(body, `"category":"`) {
				t.Fatalf("a failure must not fill a category: %s", body)
			}
			if len(cloudBodies) != 1 {
				t.Fatalf("cloud calls = %d", len(cloudBodies))
			}
		})
	}
}

// A timed-out classification surfaces the stable classifier_timeout code in
// the dry-run, not the raw provider reason ("timeout").
func TestClassifierDryRunTimeoutUsesStableCode(t *testing.T) {
	hangServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(3 * time.Second)
		_, _ = writer.Write([]byte(`{}`))
	}))
	t.Cleanup(hangServer.Close)
	var cloudBodies []string
	handler := newClassifierDryRunHandler(t, hangServer.URL, privacy.Policy{})
	putClassifierCredential(t, handler)
	response := controlRequest(t, handler, http.MethodPost, ClassifierSettingsPath+"/dry-run", "application/json", `{"text":"hello"}`, "")
	if response.Code != http.StatusOK {
		t.Fatalf("dry-run = %d %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `"fallback_reason":"classifier_timeout"`) {
		t.Fatalf("timeout code = %s", body)
	}
	if strings.Contains(body, `"fallback_reason":"timeout"`) {
		t.Fatalf("raw provider reason leaked: %s", body)
	}
	if len(cloudBodies) != 0 {
		t.Fatalf("unexpected cloud capture: %v", cloudBodies)
	}
}
