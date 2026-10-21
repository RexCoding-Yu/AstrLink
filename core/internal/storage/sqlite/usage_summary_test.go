package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestUsageSummaryCompleteWindowAndOutcomeSemantics(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "usage.db"))
	defer store.Close()
	ctx := context.Background()
	zone, _ := time.LoadLocation("Asia/Tokyo")
	from := time.Date(2026, 9, 19, 0, 0, 0, 0, zone)
	to := from.AddDate(0, 0, 1)
	options := storage.UsageSummaryOptions{From: from, To: to, TimeZone: "Asia/Tokyo", Bucket: "hour"}
	service := contract.ServiceID("service_one")
	read, write := 1, 2
	base := contract.RequestRecord{ID: "request_base", StartedAt: from, Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(),
		ServiceID: &service, RequestedModel: ptrString("model_one"),
		Usage: &contract.Usage{InputTokens: 5, OutputTokens: 2, TotalTokens: 7, CacheReadTokens: &read, CacheWriteTokens: &write}}
	for i := 0; i < 4005; i++ {
		record := base
		record.ID = contract.RequestID(fmt.Sprintf("request_%05d", i))
		record.StartedAt = from.Add(time.Duration(i) * time.Nanosecond)
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		id                        string
		status                    contract.RequestStatus
		at                        time.Time
		child, httpError, noUsage bool
	}{
		{"before", contract.RequestStatusSucceeded, from.Add(-time.Nanosecond), false, false, false},
		{"after", contract.RequestStatusSucceeded, to, false, false, false},
		{"after_fraction", contract.RequestStatusSucceeded, to.Add(time.Nanosecond), false, false, false},
		{"child", contract.RequestStatusSucceeded, from, true, false, false},
		{"failed", contract.RequestStatusFailed, from.Add(time.Hour), false, false, false},
		{"legacy", contract.RequestStatusSucceeded, from.Add(time.Hour), false, true, false},
		{"pending", contract.RequestStatusPending, from, false, false, false},
		{"blocked", contract.RequestStatusBlocked, from, false, false, false},
		{"cancelled", contract.RequestStatusCancelled, from, false, false, false},
		{"empty", contract.RequestStatusSucceeded, from.Add(2 * time.Hour), false, false, true},
	} {
		record := base
		record.ID, record.Status, record.StartedAt = contract.RequestID("request_"+test.id), test.status, test.at
		if test.child {
			parent := contract.RequestID("request_00000")
			record.ParentRequestID, record.AttemptIndex = &parent, 1
		}
		if test.httpError {
			code := 500
			record.HTTPStatus = &code
		}
		if test.noUsage {
			record.Usage, record.ServiceID, record.RequestedModel = nil, nil, nil
		}
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := store.GetUsageSummary(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	want := storage.UsageTotals{Requests: 4006, FailedRequests: 2, InputTokens: 4005 * 5, OutputTokens: 4005 * 2, TotalTokens: 4005 * 7, CacheReadTokens: 4005, CacheWriteTokens: 4005 * 2}
	if summary.Totals != want || summary.ScannedRecords != 4008 {
		t.Fatalf("totals=%+v scanned=%d", summary.Totals, summary.ScannedRecords)
	}
	// Outcomes that are not requests are counted beside them, never in them.
	wantStatuses := storage.UsageStatusCounts{Succeeded: 4006, Failed: 2, Blocked: 1, Cancelled: 1, Pending: 1}
	if summary.StatusCounts != wantStatuses {
		t.Fatalf("status counts=%+v", summary.StatusCounts)
	}
	if len(summary.ByDay) != 1 || summary.ByDay[0].Date != "2026-09-19" || summary.ByDay[0].UsageTotals != want {
		t.Fatalf("days=%+v", summary.ByDay)
	}
	if len(summary.ByHour) != 3 || *summary.ByHour[1].Hour != 1 || summary.ByHour[1].FailedRequests != 2 {
		t.Fatalf("hours=%+v", summary.ByHour)
	}
	if len(summary.ByService) != 2 || summary.ByService[0].Requests != 4005 || summary.ByService[1].ID != nil || summary.ByService[1].Requests != 1 {
		t.Fatalf("services=%+v", summary.ByService)
	}
	if err := store.DeleteRequestRecord(ctx, "request_00000"); err != nil {
		t.Fatal(err)
	}
	summary, err = store.GetUsageSummary(ctx, options)
	if err != nil || summary.Totals.TotalTokens != want.TotalTokens-7 {
		t.Fatalf("delete summary=%+v err=%v", summary, err)
	}
	if _, err := store.PurgeRequestRecords(ctx, contract.PurgeRequest{Scope: contract.PurgeScopeAll, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	summary, err = store.GetUsageSummary(ctx, options)
	if err != nil || summary.ScannedRecords != 0 || summary.ByDay == nil || summary.ByService == nil || summary.StatusCounts != (storage.UsageStatusCounts{}) {
		t.Fatalf("empty=%+v err=%v", summary, err)
	}
}

// The tray and the overview open a status count as the session list filtered
// by call status over the same window. Every counted call must sit in a listed
// conversation, including one that recovered and now reads as succeeded.
func TestUsageSummaryFailuresListAsFailedSessions(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "failed-sessions.db"))
	defer store.Close()
	ctx := context.Background()
	zone, _ := time.LoadLocation("Asia/Tokyo")
	from := time.Date(2026, 9, 19, 0, 0, 0, 0, zone)
	to := from.AddDate(0, 0, 1)
	insert := func(id, session string, status contract.RequestStatus, at time.Time, parent string) {
		t.Helper()
		sessionID := contract.SessionID(session)
		record := contract.RequestRecord{ID: contract.RequestID(id), SessionID: &sessionID, StartedAt: at, Status: status,
			InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary()}
		if parent != "" {
			parentID := contract.RequestID(parent)
			record.ParentRequestID, record.AttemptIndex = &parentID, 1
		}
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	at := func(hours int) time.Time { return from.Add(time.Duration(hours) * time.Hour) }
	insert("request_recovered_1", "session_recovered", contract.RequestStatusFailed, at(1), "")
	insert("request_recovered_2", "session_recovered", contract.RequestStatusSucceeded, at(2), "")
	insert("request_failing_1", "session_failing", contract.RequestStatusFailed, at(3), "")
	insert("request_failing_2", "session_failing", contract.RequestStatusFailed, at(4), "")
	// A policy block is its own outcome, and a retried attempt is not a call.
	insert("request_blocked", "session_blocked", contract.RequestStatusBlocked, at(5), "")
	insert("request_retried", "session_retried", contract.RequestStatusSucceeded, at(6), "")
	insert("request_retried_attempt", "session_retried", contract.RequestStatusFailed, at(6).Add(time.Minute), "request_retried")
	insert("request_yesterday", "session_yesterday", contract.RequestStatusFailed, from.Add(-time.Hour), "")
	// Model discovery is not a model call: neither counted nor listed.
	discovery := contract.RequestRecord{ID: "request_discovery", StartedAt: at(7), Status: contract.RequestStatusFailed,
		InputProtocol: contract.ProtocolOpenAIModels, Audit: contract.NotCapturedAuditSummary()}
	if err := store.InsertRequestRecord(ctx, discovery); err != nil {
		t.Fatal(err)
	}

	summary, err := store.GetUsageSummary(ctx, storage.UsageSummaryOptions{From: from, To: to, TimeZone: "Asia/Tokyo", Bucket: "day"})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Totals.FailedRequests != 3 || summary.Totals.Requests != 2 {
		t.Fatalf("totals=%+v", summary.Totals)
	}
	if want := (storage.UsageStatusCounts{Succeeded: 2, Failed: 3, Blocked: 1}); summary.StatusCounts != want {
		t.Fatalf("status counts=%+v", summary.StatusCounts)
	}
	for status, want := range map[contract.RequestStatus]map[contract.SessionID]contract.SessionStatus{
		contract.RequestStatusFailed: {
			"session_failing":   contract.SessionStatusFailed,
			"session_recovered": contract.SessionStatusSucceeded,
		},
		contract.RequestStatusSucceeded: {
			"session_recovered": contract.SessionStatusSucceeded,
			"session_retried":   contract.SessionStatusSucceeded,
		},
		contract.RequestStatusBlocked: {
			"session_blocked": contract.SessionStatusBlocked,
		},
	} {
		page, err := store.ListRequestSessions(ctx, storage.RequestSessionListOptions{Kind: "inference", From: &from, To: &to, Status: &status})
		if err != nil {
			t.Fatal(err)
		}
		listed := map[contract.SessionID]contract.SessionStatus{}
		for _, session := range page.Items {
			listed[session.ID] = session.Status
		}
		if !reflect.DeepEqual(listed, want) {
			t.Fatalf("%s listed=%v want=%v", status, listed, want)
		}
	}
}

func TestUsageSummaryDSTRepeatingHour(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "dst.db"))
	defer store.Close()
	zone, _ := time.LoadLocation("America/New_York")
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, zone)
	for i, hours := range []int{1, 2, 24} {
		if err := store.InsertRequestRecord(context.Background(), contract.RequestRecord{
			ID: contract.RequestID(fmt.Sprintf("request_dst_%d", i)), StartedAt: from.Add(time.Duration(hours) * time.Hour),
			Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIResponses,
			Audit: contract.NotCapturedAuditSummary(), Usage: &contract.Usage{TotalTokens: 3},
		}); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := store.GetUsageSummary(context.Background(), storage.UsageSummaryOptions{From: from, To: from.AddDate(0, 0, 1), TimeZone: zone.String(), Bucket: "hour"})
	if err != nil || len(summary.ByHour) != 2 || *summary.ByHour[0].Hour != 1 || summary.ByHour[0].Requests != 2 || *summary.ByHour[1].Hour != 23 {
		t.Fatalf("dst=%+v err=%v", summary, err)
	}
}

func TestUsageSummaryExcludesModelDiscovery(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "discovery.db"))
	defer store.Close()
	ctx := context.Background()
	from := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	options := storage.UsageSummaryOptions{From: from, To: from.AddDate(0, 0, 2), TimeZone: "UTC", Bucket: "hour"}
	empty, err := store.GetUsageSummary(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	service := contract.ServiceID("service_one")
	for protocolIndex, protocol := range []contract.ProtocolID{contract.ProtocolOpenAIModels, contract.ProtocolGoogleModels} {
		for i, outcome := range []struct {
			status     contract.RequestStatus
			httpStatus int
			attributed bool
		}{
			{contract.RequestStatusSucceeded, 200, false},
			{contract.RequestStatusFailed, 502, false},
			{contract.RequestStatusSucceeded, 500, false},
			{contract.RequestStatusSucceeded, 200, true},
		} {
			record := contract.RequestRecord{
				ID: contract.RequestID(fmt.Sprintf("request_discovery_%d_%d", protocolIndex, i)), StartedAt: from.Add(time.Duration(i) * time.Hour),
				Status: outcome.status, HTTPStatus: &outcome.httpStatus, InputProtocol: protocol, Audit: contract.NotCapturedAuditSummary(),
			}
			// Exclude discovery by protocol even if it has attribution or usage.
			if outcome.attributed {
				record.ServiceID, record.RequestedModel = &service, ptrString("model_one")
				record.Usage = &contract.Usage{InputTokens: 5, OutputTokens: 2, TotalTokens: 7}
			}
			if err := store.InsertRequestRecord(ctx, record); err != nil {
				t.Fatal(err)
			}
		}
	}
	summary, err := store.GetUsageSummary(ctx, options)
	if err != nil || !reflect.DeepEqual(summary, empty) {
		t.Fatalf("discovery-only summary=%+v err=%v", summary, err)
	}
	for i, status := range []contract.RequestStatus{contract.RequestStatusSucceeded, contract.RequestStatusSucceeded, contract.RequestStatusFailed} {
		record := contract.RequestRecord{
			ID: contract.RequestID(fmt.Sprintf("request_inference_%d", i)), StartedAt: from.Add(24 * time.Hour),
			Status: status, InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(),
			ServiceID: &service, RequestedModel: ptrString("model_one"),
		}
		if i == 0 {
			record.Usage = &contract.Usage{InputTokens: 5, OutputTokens: 2, TotalTokens: 7}
		}
		if status == contract.RequestStatusFailed {
			record.ServiceID = nil // Pre-routing failures still count.
		}
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	summary, err = store.GetUsageSummary(ctx, options)
	want := storage.UsageTotals{Requests: 2, FailedRequests: 1, InputTokens: 5, OutputTokens: 2, TotalTokens: 7}
	if err != nil || summary.Totals != want || summary.ScannedRecords != 3 {
		t.Fatalf("mixed summary=%+v err=%v", summary, err)
	}
	if len(summary.ByDay) != 1 || summary.ByDay[0].Date != "2026-09-20" || summary.ByDay[0].UsageTotals != want ||
		len(summary.ByHour) != 1 || summary.ByHour[0].Date != "2026-09-20" || *summary.ByHour[0].Hour != 0 || summary.ByHour[0].UsageTotals != want {
		t.Fatalf("days=%+v hours=%+v", summary.ByDay, summary.ByHour)
	}
	// The pre-routing failure clears only the service, so it opens a null service
	// bucket. The requested model is still set and stays on that model group.
	if len(summary.ByService) != 2 || summary.ByService[0].ID == nil || summary.ByService[0].Requests != 2 ||
		summary.ByService[0].FailedRequests != 0 || summary.ByService[0].TotalTokens != 7 ||
		summary.ByService[1].ID != nil || summary.ByService[1].Requests != 0 || summary.ByService[1].FailedRequests != 1 {
		t.Fatalf("services=%+v", summary.ByService)
	}
	if len(summary.ByModel) != 1 || summary.ByModel[0].ID == nil || *summary.ByModel[0].ID != "model_one" ||
		summary.ByModel[0].Requests != 2 || summary.ByModel[0].FailedRequests != 1 || summary.ByModel[0].TotalTokens != 7 {
		t.Fatalf("models=%+v", summary.ByModel)
	}
}

func TestSessionSummaryMatchesDetailAcrossFiltersAndRetries(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "summaries.db"))
	defer store.Close()
	ctx := context.Background()
	start := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	for s := 0; s < 3; s++ {
		session := contract.SessionID(fmt.Sprintf("session_%d", s))
		for i, turn := range []int{1, 1, 2, 1} {
			record := contract.RequestRecord{ID: contract.RequestID(fmt.Sprintf("request_%d_%d", s, i)), StartedAt: start.Add(time.Duration(i) * time.Minute),
				SessionID: &session, TurnIndex: &turn, Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIResponses,
				Audit: contract.NotCapturedAuditSummary(), RequestedModel: ptrString("model"), ReasoningEffort: ptrString("high")}
			if i == 0 {
				record.InputPreview = ptrString("first title")
			}
			if i == 3 {
				record.Status = contract.RequestStatusCancelled
			}
			if err := store.InsertRequestRecord(ctx, record); err != nil {
				t.Fatal(err)
			}
			parent := record.ID
			record.ID, record.ParentRequestID, record.AttemptIndex = contract.RequestID(fmt.Sprintf("child_%d_%d", s, i)), &parent, 1
			if err := store.InsertRequestRecord(ctx, record); err != nil {
				t.Fatal(err)
			}
		}
	}
	from := start.Add(time.Minute)
	page, err := store.ListRequestSessions(ctx, storage.RequestSessionListOptions{Limit: 2, From: &from})
	if err != nil || len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	for _, session := range page.Items {
		detail, err := store.GetRequestSession(ctx, string(session.ID))
		if err != nil || !reflect.DeepEqual(session, detail.RequestSession) {
			t.Fatalf("summary=%+v detail=%+v err=%v", session, detail.RequestSession, err)
		}
		if session.TurnCount != 3 || session.CallCount != 8 || session.Title != "first title" || session.Status != contract.SessionStatusInterrupted {
			t.Fatalf("summary=%+v", session)
		}
	}
	next, err := store.ListRequestSessions(ctx, storage.RequestSessionListOptions{Limit: 2, From: &from, Cursor: page.NextCursor})
	if err != nil || len(next.Items) != 1 || next.NextCursor != "" {
		t.Fatalf("next=%+v err=%v", next, err)
	}
}

func TestUsageSummaryGroupsFailuresAndCurrentAccessTokens(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "usage-token-groups.db"))
	defer store.Close()
	ctx := t.Context()
	metadata, err := store.ListAccessTokens(ctx)
	if err != nil || len(metadata) == 0 {
		t.Fatalf("ListAccessTokens: %v", err)
	}
	currentToken := metadata[0].ID
	deletedToken := contract.AccessTokenID("token_deleted")
	service := contract.ServiceID("service_usage")
	model := "model_usage"
	start := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	insert := func(record contract.RequestRecord) {
		t.Helper()
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	insert(contract.RequestRecord{ID: "usage_token_success", StartedAt: start, Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(),
		ServiceID: &service, RequestedModel: &model, LocalAccessTokenID: &currentToken,
		Usage: &contract.Usage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10}})
	insert(contract.RequestRecord{ID: "usage_token_failed", StartedAt: start.Add(time.Minute), Status: contract.RequestStatusFailed,
		InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(),
		ServiceID: &service, RequestedModel: &model, LocalAccessTokenID: &currentToken,
		Error: &contract.ErrorSummary{Category: "upstream", Code: "failed", Message: "failed"}})
	insert(contract.RequestRecord{ID: "usage_deleted_token", StartedAt: start.Add(2 * time.Minute), Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(),
		ServiceID: &service, RequestedModel: &model, LocalAccessTokenID: &deletedToken,
		Usage: &contract.Usage{TotalTokens: 20}})
	insert(contract.RequestRecord{ID: "usage_unassigned_failed", StartedAt: start.Add(3 * time.Minute), Status: contract.RequestStatusFailed,
		InputProtocol: contract.ProtocolOpenAIResponses, Audit: contract.NotCapturedAuditSummary(),
		Error: &contract.ErrorSummary{Category: "upstream", Code: "failed", Message: "failed"}})

	summary, err := store.GetUsageSummary(ctx, storage.UsageSummaryOptions{
		From: start.Add(-time.Second), To: start.Add(time.Hour), TimeZone: "UTC", Bucket: "hour",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.ByToken) != 1 || summary.ByToken[0].ID == nil || *summary.ByToken[0].ID != string(currentToken) {
		t.Fatalf("by_token=%+v", summary.ByToken)
	}
	if summary.ByToken[0].Requests != 1 || summary.ByToken[0].FailedRequests != 1 || summary.ByToken[0].TotalTokens != 10 {
		t.Fatalf("token totals=%+v", summary.ByToken[0])
	}
	// Deleted-token requests still contribute to service/model usage. Only the
	// token breakdown is intersected with the current-token directory.
	for _, test := range []struct {
		name   string
		id     string
		groups []storage.UsageGroup
	}{
		{name: "service", id: string(service), groups: summary.ByService},
		{name: "model", id: model, groups: summary.ByModel},
	} {
		t.Run(test.name, func(t *testing.T) {
			if len(test.groups) != 2 {
				t.Fatalf("groups=%+v", test.groups)
			}
			assigned, unassigned := test.groups[0], test.groups[1]
			if assigned.ID == nil || *assigned.ID != test.id || assigned.Requests != 2 || assigned.FailedRequests != 1 || assigned.TotalTokens != 30 {
				t.Fatalf("assigned group=%+v", assigned)
			}
			if unassigned.ID != nil || unassigned.Requests != 0 || unassigned.FailedRequests != 1 || unassigned.TotalTokens != 0 {
				t.Fatalf("unassigned group=%+v", unassigned)
			}
		})
	}
}

func TestUsageSummaryGroupsModelsByUpstreamModel(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "usage-upstream-model.db"))
	defer store.Close()
	ctx := t.Context()
	service := contract.ServiceID("service_usage")
	start := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	insert := func(id string, requested string, redirect *contract.RequestModelRedirect, recovery *contract.RequestRecovery, tokens int) {
		t.Helper()
		if err := store.InsertRequestRecord(ctx, contract.RequestRecord{ID: contract.RequestID(id), StartedAt: start, Status: contract.RequestStatusSucceeded,
			InputProtocol: contract.ProtocolAnthropicMessages, Audit: contract.NotCapturedAuditSummary(),
			ServiceID: &service, RequestedModel: &requested, ModelRedirect: redirect, Recovery: recovery,
			Usage: &contract.Usage{TotalTokens: tokens}}); err != nil {
			t.Fatal(err)
		}
	}
	redirect := &contract.RequestModelRedirect{From: "claude-sonnet-4-5", To: "gpt-6-astra"}
	insert("usage_served", "claude-sonnet-4-5", redirect, &contract.RequestRecovery{UpstreamModel: "gpt-6-astra"}, 10)
	// A redirect whose request never reached a provider still reports its target.
	insert("usage_redirect_only", "claude-sonnet-4-5", redirect, nil, 20)
	insert("usage_direct", "claude-haiku-4-5", nil, nil, 5)

	summary, err := store.GetUsageSummary(ctx, storage.UsageSummaryOptions{
		From: start.Add(-time.Second), To: start.Add(time.Hour), TimeZone: "UTC", Bucket: "hour",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, group := range summary.ByModel {
		if group.ID == nil {
			t.Fatalf("unexpected unassigned model group=%+v", group)
		}
		got[*group.ID] = group.TotalTokens
	}
	if want := map[string]int64{"gpt-6-astra": 30, "claude-haiku-4-5": 5}; !reflect.DeepEqual(got, want) {
		t.Fatalf("by_model=%v want %v", got, want)
	}
}
