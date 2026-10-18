package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestPluginSettingsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugins.db")
	store := openTestStore(t, path)
	ctx := context.Background()
	if _, err := store.GetPluginSettings(ctx, "powercontext"); !errors.Is(err, storagecontract.ErrNotFound) {
		t.Fatalf("settings before any write: %v", err)
	}
	record := storagecontract.PluginSettingsRecord{
		Key:          "powercontext",
		Enabled:      true,
		Capabilities: []string{"context.inject", "network"},
		Origins:      []storagecontract.PluginOrigin{{Origin: "http://192.0.2.1:9090", AllowPlaintext: true}},
		Trusted:      true,
		Config:       json.RawMessage(`{"serverUrl":"http://192.0.2.1:9090"}`),
	}
	if err := store.PutPluginSettings(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := store.PutPluginSettings(ctx, storagecontract.PluginSettingsRecord{Key: "team-rules"}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutPluginSettings(ctx, storagecontract.PluginSettingsRecord{Key: "broken", Config: json.RawMessage(`{`)}); !errors.Is(err, storagecontract.ErrInvalidArgument) {
		t.Fatalf("invalid config: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store = openTestStore(t, path)
	defer store.Close()
	records, err := store.ListPluginSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Key != "powercontext" || records[1].Key != "team-rules" {
		t.Fatalf("ListPluginSettings = %+v", records)
	}
	got := records[0]
	if !got.Enabled || !got.Trusted || !slices.Equal(got.Capabilities, record.Capabilities) || !slices.Equal(got.Origins, record.Origins) || string(got.Config) != string(record.Config) || got.CreatedAt.IsZero() {
		t.Fatalf("settings = %+v", got)
	}
	if empty := records[1]; empty.Enabled || empty.Capabilities == nil || len(empty.Capabilities) != 0 || string(empty.Config) != "{}" {
		t.Fatalf("default settings = %+v", empty)
	}
}

func TestPluginCredentialsAreSealedAndBoundToAnOrigin(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "plugins.db"))
	defer store.Close()
	ctx := context.Background()
	secret := []byte("pc-token-0123456789abcdef3f2a")
	// Writing a credential creates the plugin's settings.
	if err := store.PutPluginCredential(ctx, "powercontext", "token", "http://192.0.2.1:9090", secret); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetPluginSettings(ctx, "powercontext"); err != nil {
		t.Fatalf("settings after a credential write: %v", err)
	}
	var stored []byte
	var sealed bool
	if err := store.db.QueryRowContext(ctx, `SELECT credential_value, sealed FROM plugin_credentials WHERE id = 'powercontext/token'`).Scan(&stored, &sealed); err != nil {
		t.Fatal(err)
	}
	if !sealed || bytes.Contains(stored, secret) {
		t.Fatal("the credential is stored in the clear")
	}
	value, origin, err := store.PluginCredential(ctx, "powercontext", "token")
	if err != nil || string(value) != string(secret) || origin != "http://192.0.2.1:9090" {
		t.Fatalf("PluginCredential = %q, %q, %v", value, origin, err)
	}
	ref := secretstore.Ref(contract.PluginCredentialRef("powercontext", "token"))
	if _, err := secretstore.ParseRef(string(ref)); err != nil {
		t.Fatalf("ParseRef(%s): %v", ref, err)
	}
	if value, err := store.Get(ctx, ref); err != nil || string(value) != string(secret) {
		t.Fatalf("Get(%s) = %q, %v", ref, value, err)
	}
	listed, err := store.ListPluginCredentials(ctx, "powercontext")
	if err != nil || len(listed) != 1 || listed[0].Name != "token" || listed[0].Hint != "3f2a" || listed[0].Origin != "http://192.0.2.1:9090" {
		t.Fatalf("ListPluginCredentials = %+v, %v", listed, err)
	}
	status, err := store.LocalDataStatus(ctx)
	if err != nil || status.UnreadableCredentials != 0 {
		t.Fatalf("LocalDataStatus = %+v, %v", status, err)
	}
	for _, bad := range [][]byte{nil, []byte("line\nbreak"), []byte(strings.Repeat("x", 20000))} {
		if err := store.PutPluginCredential(ctx, "powercontext", "token", "http://192.0.2.1:9090", bad); !errors.Is(err, storagecontract.ErrInvalidArgument) {
			t.Fatalf("PutPluginCredential(%q) = %v", bad, err)
		}
	}
	if err := store.DeletePluginCredential(ctx, "powercontext", "token"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.PluginCredential(ctx, "powercontext", "token"); !errors.Is(err, storagecontract.ErrNotFound) {
		t.Fatalf("credential after delete: %v", err)
	}
	if _, err := store.Get(ctx, ref); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("Get after delete: %v", err)
	}
	if err := store.DeletePluginCredential(ctx, "powercontext", "token"); !errors.Is(err, storagecontract.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestPluginValuesKeepTheirLimits(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "plugins.db"))
	defer store.Close()
	ctx := context.Background()
	for key, value := range map[string]string{"scope:astrlink": `"scp_1"`, "scope:paseo": `"scp_2"`, "scope%x": `1`, "server": `{"version":"1.3.0"}`} {
		if err := store.PutPluginValue(ctx, "powercontext", key, json.RawMessage(value)); err != nil {
			t.Fatal(err)
		}
	}
	if value, found, err := store.GetPluginValue(ctx, "powercontext", "server"); err != nil || !found || string(value) != `{"version":"1.3.0"}` {
		t.Fatalf("GetPluginValue = %s, %v, %v", value, found, err)
	}
	if _, found, err := store.GetPluginValue(ctx, "other", "server"); err != nil || found {
		t.Fatalf("another plugin's value: found %v, %v", found, err)
	}
	keys, err := store.PluginValueKeys(ctx, "powercontext", "scope:")
	if err != nil || !slices.Equal(keys, []string{"scope:astrlink", "scope:paseo"}) {
		t.Fatalf("PluginValueKeys = %v, %v", keys, err)
	}
	if err := store.PutPluginValue(ctx, "powercontext", "big", json.RawMessage(`"`+strings.Repeat("x", storagecontract.MaxPluginValueBytes)+`"`)); !errors.Is(err, storagecontract.ErrLimitReached) {
		t.Fatalf("oversized value: %v", err)
	}
	chunk := json.RawMessage(`"` + strings.Repeat("x", 60<<10) + `"`)
	var limited error
	for index := range 20 {
		if limited = store.PutPluginValue(ctx, "powercontext", "chunk"+string(rune('a'+index)), chunk); limited != nil {
			break
		}
	}
	if !errors.Is(limited, storagecontract.ErrLimitReached) {
		t.Fatalf("values past 1 MiB: %v", limited)
	}
	// Replacing a value counts only the new one.
	if err := store.PutPluginValue(ctx, "powercontext", "chunka", chunk); err != nil {
		t.Fatalf("rewriting a value at the limit: %v", err)
	}
	if err := store.DeletePluginValue(ctx, "powercontext", "scope:paseo"); err != nil {
		t.Fatal(err)
	}
	if keys, _ := store.PluginValueKeys(ctx, "powercontext", "scope:"); !slices.Equal(keys, []string{"scope:astrlink"}) {
		t.Fatalf("keys after delete = %v", keys)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM plugin_settings WHERE plugin_key = 'powercontext'`); err != nil {
		t.Fatal(err)
	}
	if keys, _ := store.PluginValueKeys(ctx, "powercontext", ""); len(keys) != 0 {
		t.Fatalf("values outlived their plugin: %v", keys)
	}
}
