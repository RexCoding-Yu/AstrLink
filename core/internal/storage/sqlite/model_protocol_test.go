package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// Per-model formats live in the service document: older documents read
// without them, and they survive a restart and compatibility writes.
func TestModelProtocolsPersistAcrossRestartAndLegacyWrites(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "formats.db")
	store := openTestStore(t, path)
	service := contract.ServiceFromEndpoint(testEndpoint("service_formats"))
	service.Models = []string{"gemini-x"}
	service.ModelProtocols = []contract.ModelProtocol{{Model: "gemini-x", Protocol: contract.ProtocolGoogleGenerateContent}}
	if _, err := store.CreateService(ctx, service, storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	plain := contract.ServiceFromEndpoint(testEndpoint("service_plain"))
	if _, err := store.CreateService(ctx, plain, storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	store.Close()
	store = openTestStore(t, path)
	defer store.Close()
	if record, err := store.GetService(ctx, plain.ID); err != nil || record.Service.ModelProtocols != nil {
		t.Fatalf("plain service = %#v, %v", record.Service.ModelProtocols, err)
	}
	record, err := store.GetService(ctx, service.ID)
	if err != nil || record.Service.ModelProtocol("gemini-x") != contract.ProtocolGoogleGenerateContent {
		t.Fatalf("formats did not survive restart: %#v, %v", record.Service.ModelProtocols, err)
	}
	legacy, _ := record.Service.EndpointView()
	legacy.Name = "renamed by compatibility API"
	if _, err := store.UpdateEndpoint(ctx, legacy, storage.CredentialMutation{}, record.ETag); err != nil {
		t.Fatal(err)
	}
	if record, _ = store.GetService(ctx, service.ID); len(record.Service.ModelProtocols) != 1 {
		t.Fatal("legacy write removed the formats")
	}
}
