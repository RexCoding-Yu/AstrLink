package sqlite

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestSessionSummaryMarksConversationsWithAWarnedCall(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "warned.db"))
	defer store.Close()
	ctx := context.Background()
	started := time.Now().UTC()
	warn, allow, redact := contract.PrivacyDecisionWarn, contract.PrivacyDecisionAllow, contract.PrivacyDecisionRedact
	warnedID, cleanID := contract.SessionID("session_warned"), contract.SessionID("session_clean")
	findings := []contract.PrivacyFinding{{Kind: contract.CanonicalKindCommonSecret, JSONPath: "/input", Count: 1}}
	for _, record := range []contract.RequestRecord{
		{ID: "request_warned_first", SessionID: &warnedID, StartedAt: started, PrivacyDecision: &warn, PrivacyFindings: findings},
		// The warning stays on the row after a later call finds nothing.
		{ID: "request_warned_latest", SessionID: &warnedID, StartedAt: started.Add(time.Second), PrivacyDecision: &allow},
		{ID: "request_clean", SessionID: &cleanID, StartedAt: started.Add(2 * time.Second), PrivacyDecision: &redact, PrivacyFindings: findings},
	} {
		record.Status = contract.RequestStatusSucceeded
		record.InputProtocol = contract.ProtocolOpenAIResponses
		if err := store.InsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	page, err := store.ListRequestSessions(ctx, storagecontract.RequestSessionListOptions{})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("sessions = %#v, %v", page, err)
	}
	warned := map[contract.SessionID]bool{}
	for _, session := range page.Items {
		warned[session.ID] = session.PrivacyWarned
	}
	if !warned[warnedID] || warned[cleanID] {
		t.Fatalf("privacy_warned = %v", warned)
	}
	encoded, _ := json.Marshal(page.Items)
	if strings.Count(string(encoded), `"privacy_warned"`) != 1 {
		t.Fatalf("only the warned session carries the flag: %s", encoded)
	}
	detail, err := store.GetRequestSession(ctx, string(warnedID))
	if err != nil || !detail.PrivacyWarned || len(detail.Turns) != 2 ||
		len(detail.Turns[0].PrivacyFindings) != 1 || detail.Turns[0].PrivacyFindings[0] != findings[0] {
		t.Fatalf("detail = %#v, %v", detail, err)
	}
}
