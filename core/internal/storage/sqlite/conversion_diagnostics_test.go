package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/migrate"
)

func TestConversionDiagnosticsSurviveAnUpgradedDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversion-diagnostics.db")
	database, err := sql.Open(driverName, sqliteFileDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	migrations := migrate.DefaultMigrations()
	var shipped []migrate.Migration
	for _, migration := range migrations {
		if migration.Name == "request_conversion_diagnostics" {
			break
		}
		shipped = append(shipped, migration)
	}
	if len(shipped) == len(migrations) {
		t.Fatal("request_conversion_diagnostics migration is missing")
	}
	runner, err := migrate.New(migrate.SQLDatabase{DB: database}, shipped)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	audit, err := json.Marshal(contract.NotCapturedAuditSummary())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO request_records (id, started_at, status, input_protocol, requested_model, streaming, audit_json, created_at)
VALUES ('request_before_diagnostics', '2026-09-30T00:00:00Z', 'succeeded', 'openai.responses', 'gpt-5', 0, ?, '2026-09-30T00:00:00Z')`, string(audit)); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	store := openTestStore(t, path)
	defer store.Close()
	ctx := context.Background()
	legacy, err := store.GetRequestRecord(ctx, "request_before_diagnostics")
	if err != nil || legacy.ConversionDiagnostics != nil {
		t.Fatalf("legacy=%#v err=%v", legacy.ConversionDiagnostics, err)
	}

	diagnostics := []contract.ConversionDiagnostic{
		{
			Phase: contract.ConversionDiagnosticPhaseRequest, Severity: contract.ConversionDiagnosticError,
			Code: "unsupported_hosted_tool", Path: "tools[0]",
			Message: `OpenAI Chat Completions cannot represent hosted tool "local_shell"`,
		},
		{
			Phase: contract.ConversionDiagnosticPhaseResponse, Severity: contract.ConversionDiagnosticWarning,
			Code: "hosted_tool_event_unrepresentable", Path: "item", Message: "dropped",
		},
	}
	started := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	serviceID := contract.ServiceID("chat-provider")
	pending := contract.RequestRecord{
		ID: "request_diagnostics_live", AttemptIndex: 1, StartedAt: started, Status: contract.RequestStatusPending,
		InputProtocol: contract.ProtocolOpenAIResponses, RequestedModel: ptrString("gpt-5"), ServiceID: &serviceID,
		Audit: contract.NotCapturedAuditSummary(), ConversionDiagnostics: diagnostics[:1],
	}
	if err := store.UpsertRequestRecord(ctx, pending); err != nil {
		t.Fatal(err)
	}
	final := pending
	final.Status = contract.RequestStatusSucceeded
	final.CompletedAt = ptrTime(started.Add(time.Second))
	final.ConversionDiagnostics = diagnostics
	if err := store.UpsertRequestRecord(ctx, final); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetRequestRecord(ctx, final.ID)
	if err != nil || !reflect.DeepEqual(got.ConversionDiagnostics, diagnostics) {
		t.Fatalf("get=%#v err=%v", got.ConversionDiagnostics, err)
	}
	page, err := store.ListRequestRecords(ctx, storagecontract.RequestRecordListOptions{Limit: 10})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("list=%#v err=%v", page, err)
	}
	for _, item := range page.Items {
		want := diagnostics
		if item.ID == legacy.ID {
			want = nil
		}
		if !reflect.DeepEqual(item.ConversionDiagnostics, want) {
			t.Fatalf("list item %s diagnostics=%#v", item.ID, item.ConversionDiagnostics)
		}
	}

	// Stored diagnostics are validated like every other record field.
	for _, document := range []string{
		`[{"phase":"request"`,
		`[{"phase":"sideways","severity":"error","code":"x","message":"m"}]`,
		`[{"phase":"request","severity":"fatal","code":"x","message":"m"}]`,
		`[{"phase":"request","severity":"error","code":"","message":"m"}]`,
	} {
		if _, err := store.db.ExecContext(ctx, `UPDATE request_records SET conversion_diagnostics_json = ? WHERE id = ?`, document, string(legacy.ID)); err != nil {
			t.Fatal(err)
		}
		if _, err := store.GetRequestRecord(ctx, legacy.ID); !errors.Is(err, storagecontract.ErrInvalidRecord) {
			t.Fatalf("%s loaded with err=%v", document, err)
		}
	}
}
