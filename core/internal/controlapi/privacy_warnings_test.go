package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

func TestPrivacyWarningsAPIServesKindsWithoutValues(t *testing.T) {
	const token = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "warnings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	feed := privacy.NewWarningFeed()
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("0.1.0-test", "abc1234"), Dependencies{
		ServiceStore: store, ControlToken: testControlToken, PrivacyWarnings: feed,
	})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := privacy.New(privacy.PolicyProviderFunc(func(context.Context, privacy.Scope) (privacy.Policy, error) {
		return privacy.Policy{}, nil
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"key " + token, "key " + token + " again"} {
		result, err := engine.Inspect(context.Background(), privacy.Policy{
			Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionWarn,
		}, contract.ProtocolOpenAIResponses, []byte(`{"model":"safe","input":"`+input+`"}`))
		if err != nil || result.Decision != privacy.DecisionWarn {
			t.Fatalf("result = %#v, %v", result, err)
		}
		feed.Report(privacy.Warning{
			RequestID: "request_warned", Findings: result.Findings,
			Notified: result.Notified, Fingerprints: result.Fingerprints,
		})
	}

	response := accessTokenRequest(t, handler, http.MethodGet, PrivacyWarningsPath, "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), token) || strings.Contains(response.Body.String(), "ghp_") {
		t.Fatalf("feed response carries the value: %s", response.Body.String())
	}
	var page privacy.WarningPage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Epoch == "" || len(page.Events) != 2 || !page.Events[0].Notifiable || page.Events[1].Notifiable ||
		len(page.Events[0].Kinds) != 1 || page.Events[0].Kinds[0].Kind != privacy.KindCommonSecret {
		t.Fatalf("page = %s", response.Body.String())
	}

	later := accessTokenRequest(t, handler, http.MethodGet, PrivacyWarningsPath+"?since=1", "", "")
	if err := json.Unmarshal(later.Body.Bytes(), &page); err != nil || len(page.Events) != 1 || page.Events[0].Sequence != 2 {
		t.Fatalf("since=1 status=%d body=%s", later.Code, later.Body.String())
	}
	for _, query := range []string{"?since=-1", "?since=next"} {
		if invalid := accessTokenRequest(t, handler, http.MethodGet, PrivacyWarningsPath+query, "", ""); invalid.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d", query, invalid.Code)
		}
	}
	if post := accessTokenRequest(t, handler, http.MethodPost, PrivacyWarningsPath, "", ""); post.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status=%d", post.Code)
	}
	anonymous := httptest.NewRecorder()
	handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, PrivacyWarningsPath, nil))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d", anonymous.Code)
	}
}
