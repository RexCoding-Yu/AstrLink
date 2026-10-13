package sqlite

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestModelRoutingRecordAndBindingRoundTrip(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "routing.db"))
	defer store.Close()
	ctx := context.Background()
	decision := &contract.ModelRoutingDecision{
		SchemaVersion: 1, VirtualModelID: "astrlink/superclaude", GroupID: "superclaude",
		ConfigSnapshotVersion: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		SelectionSource:       contract.ModelRoutingSelectionRule, UsedDefault: false,
		Fields: []contract.ModelRoutingFieldObservation{
			{Field: "streaming", Source: "request", Known: true},
			{Field: "task_type", Source: "classifier", Known: true, ClassifierConfidence: 0.81, Candidates: []contract.ModelRoutingCandidateObservation{
				{Value: "coding", Score: 0.81, Rank: 0, Chosen: true},
				{Value: "general", Score: 0.64, Rank: 1, Chosen: false},
			}},
		},
		MemberID: "strong", MemberServiceID: "service_01", MemberModel: "model-strong",
		Execution:       contract.ModelRoutingExecution{Status: contract.ModelRoutingExecutionSucceeded, AttemptCount: 1},
		PrivacyAttempts: []contract.ModelRoutingPrivacyAttempt{{AttemptIndex: 0, ServiceID: "service_01", Decision: contract.ModelRoutingPrivacyNone}},
	}
	record := contract.RequestRecord{ID: "request_routing_01", StartedAt: time.Now().UTC(), Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIResponses, RequestedModel: stringPtr("astrlink/superclaude"), ModelRouting: decision, Audit: contract.NotCapturedAuditSummary()}
	if err := store.InsertRequestRecord(ctx, record); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetRequestRecord(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ModelRouting == nil || loaded.ModelRouting.MemberModel != "model-strong" {
		t.Fatalf("model routing = %+v", loaded.ModelRouting)
	}
	wantCandidates := decision.Fields[1].Candidates
	if got := loaded.ModelRouting.Fields[1].Candidates; !reflect.DeepEqual(got, wantCandidates) {
		t.Fatalf("stored candidates = %+v, want %+v", got, wantCandidates)
	}

	now := time.Now().UTC()
	binding := contract.ModelRoutingBinding{Principal: "principal_01", SessionID: "session_01", Protocol: contract.ProtocolOpenAIResponses, VirtualModelID: "astrlink/superclaude", GroupID: "superclaude", MemberID: "strong", ServiceID: "service_01", Model: "model-strong", ConfigSnapshotVersion: decision.ConfigSnapshotVersion, CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano)}
	if err := store.UpsertModelRoutingBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetModelRoutingBinding(ctx, binding.Principal, binding.SessionID, binding.Protocol, binding.VirtualModelID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MemberID != binding.MemberID || got.Model != binding.Model {
		t.Fatalf("binding = %+v", got)
	}
}

func stringPtr(value string) *string { return &value }
