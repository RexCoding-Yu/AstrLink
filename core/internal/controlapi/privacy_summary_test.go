package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestPrivacySummaryAPI(t *testing.T) {
	store, err := openControlTestStore(t, context.Background(), filepath.Join(t.TempDir(), "privacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("0.1.0-test", "abc1234"), Dependencies{ServiceStore: store, RequestRecords: store, ControlToken: testControlToken})
	if err != nil {
		t.Fatal(err)
	}
	query := "?from=2026-09-18T15:00:00Z&to=2026-09-19T15:00:00Z&time_zone=Asia%2FTokyo&bucket=hour"
	response := accessTokenRequest(t, handler, http.MethodGet, PrivacySummaryPath+query, "", "")
	var empty map[string]json.RawMessage
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &empty) != nil ||
		string(empty["by_day"]) != "[]" || string(empty["by_hour"]) != "[]" || string(empty["by_kind"]) != "[]" || len(empty) != 4 {
		t.Fatalf("empty status=%d body=%s", response.Code, response.Body.String())
	}

	redact := contract.PrivacyDecisionRedact
	if err := store.InsertRequestRecord(context.Background(), contract.RequestRecord{
		ID: "request_redacted", StartedAt: time.Date(2026, 9, 19, 1, 30, 0, 0, time.UTC), Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(), PrivacyDecision: &redact,
		PrivacyRestore: &contract.PrivacyRestoreSummary{Enabled: true, MappingCount: 2, RestoredCount: 1, VisibleRestoredCount: 1,
			Hits: []contract.PrivacyHitCount{{Kind: contract.CanonicalKindEmail, Count: 2}}},
	}); err != nil {
		t.Fatal(err)
	}
	response = accessTokenRequest(t, handler, http.MethodGet, PrivacySummaryPath+query, "", "")
	var summary struct {
		Totals map[string]int64 `json:"totals"`
		ByHour []struct {
			Date     string `json:"date"`
			Hour     int    `json:"hour"`
			Replaced int64  `json:"replaced"`
		} `json:"by_hour"`
		ByKind []struct {
			Kind  string `json:"kind"`
			Count int64  `json:"count"`
		} `json:"by_kind"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &summary) != nil {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	wantTotals := map[string]int64{"requests": 1, "allowed": 0, "ignored": 0, "warned": 0, "redacted": 1, "blocked": 0, "fail_open": 0, "trusted": 0,
		"replaced": 2, "restored": 1, "restored_visible": 1, "restored_tool_arguments": 0, "unrestored": 0,
		"ignored_allowlisted": 0, "ignored_low_confidence": 0}
	if len(summary.Totals) != len(wantTotals) {
		t.Fatalf("totals = %v", summary.Totals)
	}
	for key, want := range wantTotals {
		if got, ok := summary.Totals[key]; !ok || got != want {
			t.Fatalf("totals[%s] = %d, want %d (%v)", key, got, want, summary.Totals)
		}
	}
	if len(summary.ByHour) != 1 || summary.ByHour[0].Date != "2026-09-19" || summary.ByHour[0].Hour != 10 || summary.ByHour[0].Replaced != 2 {
		t.Fatalf("hours = %+v", summary.ByHour)
	}
	if len(summary.ByKind) != 1 || summary.ByKind[0].Kind != "email" || summary.ByKind[0].Count != 2 {
		t.Fatalf("kinds = %+v", summary.ByKind)
	}

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, PrivacySummaryPath+query, nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized=%d", unauthorized.Code)
	}
	for _, bad := range []string{"", query + "&limit=1", query + "&bucket=day", "?from=%ZZ", "?from=2026-09-19T00:00:00.001Z&to=2026-09-20T00:00:00Z&time_zone=UTC&bucket=day", "?from=2025-01-01T00:00:00Z&to=2026-09-20T00:00:00Z&time_zone=UTC&bucket=day", "?from=2026-09-19T00:00:00Z&to=2026-09-20T00:00:00Z&time_zone=Missing&bucket=day"} {
		if response := accessTokenRequest(t, handler, http.MethodGet, PrivacySummaryPath+bad, "", ""); response.Code != http.StatusBadRequest {
			t.Fatalf("query=%s status=%d", bad, response.Code)
		}
	}
	if response := accessTokenRequest(t, handler, http.MethodPost, PrivacySummaryPath+query, "", ""); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method=%d", response.Code)
	}
}
