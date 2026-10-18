package sqlite

import (
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestSessionTitleFallsBackToPreviewThenModelThenEmpty(t *testing.T) {
	model := "gpt-5"
	preview := "fix the build"
	untitled := contract.RequestRecord{
		ID: "request_title", StartedAt: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC),
		Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolOpenAIResponses,
		Audit: contract.NotCapturedAuditSummary(),
	}
	withModel := untitled
	withModel.RequestedModel = &model
	withPreview := withModel
	withPreview.InputPreview = &preview
	for _, test := range []struct {
		record contract.RequestRecord
		want   string
	}{
		{withPreview, preview},
		{withModel, model},
		{untitled, ""},
	} {
		session, err := sessionFromTurns([]contract.RequestRecord{test.record})
		if err != nil {
			t.Fatal(err)
		}
		if session.Title != test.want {
			t.Fatalf("title = %q, want %q", session.Title, test.want)
		}
	}
}
