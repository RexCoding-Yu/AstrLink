package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// putClassifierDryRunGroup stores the shared two-member group behind the
// dry-run candidate tests: the one rule references task_type, so the ranked
// sequence actually drives the decision.
func putClassifierDryRunGroup(t *testing.T, handler *Handler, rule contract.ModelRoutingRule) {
	t.Helper()
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
		Rules:           []contract.ModelRoutingRule{rule},
	}
	if err := handler.routingSettings.UpdateRoutingSettings(context.Background(), contractRoutingSettingsWithGroup(group)); err != nil {
		t.Fatal(err)
	}
}

type dryRunCandidate struct {
	Value  string  `json:"value"`
	Score  float64 `json:"score"`
	Rank   int     `json:"rank"`
	Chosen bool    `json:"chosen"`
}

type dryRunField struct {
	Field                string            `json:"field"`
	Source               string            `json:"source"`
	ClassifierID         string            `json:"classifier_id"`
	ClassifierConfidence float64           `json:"classifier_confidence"`
	Candidates           []dryRunCandidate `json:"candidates"`
}

type dryRunResponse struct {
	Category       string        `json:"category"`
	Confidence     float64       `json:"confidence"`
	FallbackReason string        `json:"fallback_reason"`
	Fields         []dryRunField `json:"fields"`
	Decision       struct {
		MemberID    string `json:"member_id"`
		RuleID      string `json:"rule_id"`
		UsedDefault bool   `json:"used_default"`
	} `json:"decision"`
}

// The provider's ranked list reaches the dry-run response through the same
// conversion as production: catalog-checked at the source ("writing" is not a
// group label), threshold- and order-normalized by the shared core, and the
// entry the evaluator adopted is the only chosen one. The classifier is
// called exactly once regardless of how many candidates the row walks.
func TestClassifierDryRunReportsRankedCandidates(t *testing.T) {
	var cloudBodies []string
	cloudServer := newClassifierCloudServer(t,
		`{"answers":{"task_type":{"choice":"coding","confidence":0.95,"ranked":[`+
			`{"choice":"coding","confidence":0.95},`+
			`{"choice":"research","confidence":0.7},`+
			`{"choice":"writing","confidence":0.4}]}}}`, &cloudBodies)
	handler := newClassifierDryRunHandler(t, cloudServer.URL, privacy.Policy{})
	putClassifierCredential(t, handler)
	putClassifierDryRunGroup(t, handler, contract.ModelRoutingRule{
		ID: "research", Priority: 1,
		When:           contract.ModelRoutingCondition{Field: "task_type", In: []string{"research"}},
		TargetMemberID: "strong",
	})
	response := controlRequest(t, handler, http.MethodPost, ClassifierSettingsPath+"/dry-run", "application/json",
		`{"text":"write go code","group_id":"superclaude"}`, "")
	if response.Code != http.StatusOK {
		t.Fatalf("dry-run = %d %s", response.Code, response.Body.String())
	}
	t.Logf("raw dry-run response: %s", response.Body.String())
	var result dryRunResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Category != "coding" || result.Confidence != 0.95 || result.FallbackReason != "" {
		t.Fatalf("category/confidence/fallback = %q/%v/%q", result.Category, result.Confidence, result.FallbackReason)
	}
	if result.Decision.MemberID != "strong" || result.Decision.RuleID != "research" || result.Decision.UsedDefault {
		t.Fatalf("decision = %+v, want the research rule on the strong member", result.Decision)
	}
	if len(result.Fields) != 1 || result.Fields[0].Field != "task_type" || result.Fields[0].Source != "classifier" {
		t.Fatalf("fields = %+v", result.Fields)
	}
	field := result.Fields[0]
	if field.ClassifierID != "cloud:jev" || field.ClassifierConfidence != 0.95 {
		t.Fatalf("classifier backfill = %q/%v", field.ClassifierID, field.ClassifierConfidence)
	}
	want := []dryRunCandidate{
		{Value: "coding", Score: 0.95, Rank: 0, Chosen: false},
		{Value: "research", Score: 0.7, Rank: 1, Chosen: true},
	}
	if !reflect.DeepEqual(field.Candidates, want) {
		t.Fatalf("candidates = %+v, want %+v", field.Candidates, want)
	}
	chosen, chosenRank := 0, -1
	for _, candidate := range field.Candidates {
		if candidate.Chosen {
			chosen++
			chosenRank = candidate.Rank
		}
	}
	if chosen != 1 || chosenRank != 1 {
		t.Fatalf("chosen count/rank = %d/%d, want exactly one chosen at rank 1", chosen, chosenRank)
	}
	if len(cloudBodies) != 1 {
		t.Fatalf("cloud calls = %d, want exactly one classification", len(cloudBodies))
	}
}

// A single-value answer (no ranked list) still surfaces its one candidate:
// the same conversion fills the single-candidate form and marks it chosen
// when the row adopts it.
func TestClassifierDryRunSingleValueAnswerCarriesOneCandidate(t *testing.T) {
	var cloudBodies []string
	cloudServer := newClassifierCloudServer(t, `{"answers":{"task_type":{"choice":"coding","confidence":0.95}}}`, &cloudBodies)
	handler := newClassifierDryRunHandler(t, cloudServer.URL, privacy.Policy{})
	putClassifierCredential(t, handler)
	putClassifierDryRunGroup(t, handler, contract.ModelRoutingRule{
		ID: "coding", Priority: 1,
		When:           contract.ModelRoutingCondition{Field: "task_type", In: []string{"coding"}},
		TargetMemberID: "strong",
	})
	response := controlRequest(t, handler, http.MethodPost, ClassifierSettingsPath+"/dry-run", "application/json",
		`{"text":"write go code","group_id":"superclaude"}`, "")
	if response.Code != http.StatusOK {
		t.Fatalf("dry-run = %d %s", response.Code, response.Body.String())
	}
	t.Logf("raw dry-run response: %s", response.Body.String())
	var result dryRunResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	want := []dryRunCandidate{{Value: "coding", Score: 0.95, Rank: 0, Chosen: true}}
	if len(result.Fields) != 1 || !reflect.DeepEqual(result.Fields[0].Candidates, want) {
		t.Fatalf("fields = %+v, want one adopted coding candidate", result.Fields)
	}
	if len(cloudBodies) != 1 {
		t.Fatalf("cloud calls = %d", len(cloudBodies))
	}
}

// Low confidence empties the sequence and the response must not carry the
// candidates key at all — no fabricated placeholder row — while the decision
// still fails open onto the default member.
func TestClassifierDryRunLowConfidenceOmitsCandidates(t *testing.T) {
	var cloudBodies []string
	cloudServer := newClassifierCloudServer(t,
		`{"answers":{"task_type":{"choice":"coding","confidence":0.3,"ranked":[`+
			`{"choice":"coding","confidence":0.3}]}}}`, &cloudBodies)
	handler := newClassifierDryRunHandler(t, cloudServer.URL, privacy.Policy{})
	putClassifierCredential(t, handler)
	putClassifierDryRunGroup(t, handler, contract.ModelRoutingRule{
		ID: "coding", Priority: 1,
		When:           contract.ModelRoutingCondition{Field: "task_type", In: []string{"coding"}},
		TargetMemberID: "strong",
	})
	response := controlRequest(t, handler, http.MethodPost, ClassifierSettingsPath+"/dry-run", "application/json",
		`{"text":"write go code","group_id":"superclaude"}`, "")
	if response.Code != http.StatusOK {
		t.Fatalf("dry-run = %d %s", response.Code, response.Body.String())
	}
	t.Logf("raw dry-run response: %s", response.Body.String())
	body := response.Body.String()
	if !strings.Contains(body, `"fallback_reason":"classifier_low_confidence"`) {
		t.Fatalf("fallback reason missing: %s", body)
	}
	if strings.Contains(body, "candidates") {
		t.Fatalf("candidates key must stay absent on low confidence: %s", body)
	}
	if strings.Contains(body, `"category":"`) {
		t.Fatalf("low confidence must not report a category: %s", body)
	}
	var result dryRunResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Decision.UsedDefault || result.Decision.MemberID != "fast" {
		t.Fatalf("decision = %+v, want the default member", result.Decision)
	}
	if len(cloudBodies) != 1 {
		t.Fatalf("cloud calls = %d", len(cloudBodies))
	}
}

// A fallback is not a measurement: even fallback=general, which fills the
// category, never contributes a candidate row.
func TestClassifierDryRunFallbackGeneralOmitsCandidates(t *testing.T) {
	var cloudBodies []string
	cloudServer := newClassifierCloudServer(t, "not-a-jev-answer", &cloudBodies)
	handler := newClassifierDryRunHandler(t, cloudServer.URL, privacy.Policy{})
	putClassifierCredential(t, handler)
	settingsDocument := `{"enabled":true,"classifier_source":"inline","inline":{"protocol":"jev","endpoint":"` + cloudServer.URL +
		`","credential_ref":"local://classifier/default","model":"jev-latest","timeout_ms":1500,"fallback":"general"}}`
	response := controlRequest(t, handler, http.MethodPut, ClassifierSettingsPath, "application/json", settingsDocument, "")
	if response.Code != http.StatusOK {
		t.Fatalf("settings put = %d %s", response.Code, response.Body.String())
	}
	putClassifierDryRunGroup(t, handler, contract.ModelRoutingRule{
		ID: "coding", Priority: 1,
		When:           contract.ModelRoutingCondition{Field: "task_type", In: []string{"coding"}},
		TargetMemberID: "strong",
	})
	cloudBodies = nil
	response = controlRequest(t, handler, http.MethodPost, ClassifierSettingsPath+"/dry-run", "application/json",
		`{"text":"hello","group_id":"superclaude"}`, "")
	if response.Code != http.StatusOK {
		t.Fatalf("dry-run = %d %s", response.Code, response.Body.String())
	}
	t.Logf("raw dry-run response: %s", response.Body.String())
	body := response.Body.String()
	if !strings.Contains(body, `"category":"general"`) {
		t.Fatalf("general fallback must fill the category: %s", body)
	}
	if strings.Contains(body, "candidates") {
		t.Fatalf("fallback must not contribute candidates: %s", body)
	}
	if len(cloudBodies) != 1 {
		t.Fatalf("cloud calls = %d", len(cloudBodies))
	}
}
