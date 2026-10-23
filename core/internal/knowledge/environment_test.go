package knowledge

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/embedmodel"
)

func TestDerivePasswordIsStablePerKey(t *testing.T) {
	first, err := DerivePassword(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	again, _ := DerivePassword(bytes.Repeat([]byte{1}, 32))
	other, _ := DerivePassword(bytes.Repeat([]byte{2}, 32))
	if first != again || first == other || len(first) != 32 {
		t.Fatalf("passwords: %q %q %q", first, again, other)
	}
}

func TestStatusListsTheLocalModels(t *testing.T) {
	environment := NewEnvironment(EnvironmentConfig{Directory: t.TempDir()})
	defer environment.Close()
	status := environment.Status()
	if len(status.EmbeddingModels) != len(embedmodel.Models()) {
		t.Fatalf("models = %+v", status.EmbeddingModels)
	}
	granite := status.EmbeddingModels[0]
	if granite.State != contract.KnowledgeModelNotInstalled || granite.DownloadBytes != embedmodel.Granite.Size() {
		t.Fatalf("granite = %+v", granite)
	}
	if status.Runtime.State != contract.KnowledgeRuntimeUnsupported && status.Runtime.State != contract.KnowledgeRuntimeNotInstalled {
		t.Fatalf("runtime = %+v", status.Runtime)
	}
	if _, err := environment.DB(); err != ErrNotRunning {
		t.Fatalf("DB before start: %v", err)
	}
	if err := environment.StartServer(); err != ErrNotInstalled && err != ErrUnsupported {
		t.Fatalf("StartServer without a runtime: %v", err)
	}
}

// TestEnvironmentLifecycle runs the real search engine:
// ASTRLINK_SEEKDB_BUNDLE=/path/to/bundle.tar.gz.
func TestEnvironmentLifecycle(t *testing.T) {
	archive := os.Getenv("ASTRLINK_SEEKDB_BUNDLE")
	if archive == "" {
		t.Skip("set ASTRLINK_SEEKDB_BUNDLE to the seekdb runtime archive")
	}
	environment := NewEnvironment(EnvironmentConfig{
		Directory: t.TempDir(),
		Password:  func(context.Context) (string, error) { return DerivePassword(bytes.Repeat([]byte{7}, 32)) },
		Logf:      t.Logf,
	})
	defer environment.Close()
	ctx := context.Background()
	if err := environment.ImportRuntime(ctx, archive); err != nil {
		t.Fatalf("ImportRuntime: %v", err)
	}
	if state := environment.Status().Runtime.State; state != contract.KnowledgeRuntimeInstalled {
		t.Fatalf("after import: %s", state)
	}
	if err := environment.StartServer(); err != nil {
		t.Fatalf("StartServer: %v", err)
	}
	waitForState(t, environment, contract.KnowledgeRuntimeRunning)
	check, err := environment.CheckRuntime(ctx)
	if err != nil || !check.OK {
		t.Fatalf("CheckRuntime: %+v, %v", check, err)
	}
	t.Logf("engine %s, steps %+v", check.Engine, check.Steps)
	if err := environment.StopServer(); err != nil {
		t.Fatal(err)
	}
	if state := environment.Status().Runtime.State; state != contract.KnowledgeRuntimeInstalled {
		t.Fatalf("after stop: %s", state)
	}
	if err := environment.ResetData(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(environment.config.Directory, "seekdb")); !os.IsNotExist(err) {
		t.Fatalf("data directory after reset: %v", err)
	}
}

// TestModelCheck runs granite through the worker: ASTRLINK_EMBEDDING_WORKER
// and ASTRLINK_GRANITE_DIR, a directory with the pinned files.
func TestModelCheck(t *testing.T) {
	worker, source := os.Getenv("ASTRLINK_EMBEDDING_WORKER"), os.Getenv("ASTRLINK_GRANITE_DIR")
	if worker == "" || source == "" {
		t.Skip("set ASTRLINK_EMBEDDING_WORKER and ASTRLINK_GRANITE_DIR")
	}
	environment := NewEnvironment(EnvironmentConfig{Directory: t.TempDir(), WorkerExecutable: worker})
	defer environment.Close()
	ctx := context.Background()
	if err := environment.ImportModel(ctx, embedmodel.Granite.ID, source); err != nil {
		t.Fatalf("ImportModel: %v", err)
	}
	check, err := environment.CheckModel(ctx, embedmodel.Granite.ID)
	if err != nil {
		t.Fatalf("CheckModel: %v", err)
	}
	t.Logf("load %d ms, embed %d ms, pairs %+v", check.LoadMS, check.EmbedMS, check.Pairs)
	related, unrelated := check.Pairs[0].Similarity, check.Pairs[2].Similarity
	if related <= unrelated+0.2 {
		t.Fatalf("a paraphrase scored %.3f against %.3f for unrelated text", related, unrelated)
	}
	if !environment.Status().EmbeddingModels[0].Loaded {
		t.Fatal("the model is not reported loaded after a check")
	}
}

func waitForState(t *testing.T, environment *Environment, want contract.KnowledgeRuntimeState) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		status := environment.Status().Runtime
		if status.State == want {
			return
		}
		if status.State == contract.KnowledgeRuntimeFailed {
			t.Fatalf("runtime failed: %s", status.Error)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("runtime did not reach %s", want)
}
