package embedmodel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testModel(files map[string]string) Model {
	model := Model{
		ID: "test-model", RepoID: "acme/test-model", Revision: "0123456789abcdef",
		Dimensions: 8, MaxTokens: 16, Pooling: "cls", PadTokenID: 0, OutputName: "last_hidden_state",
	}
	for _, path := range []string{"model.onnx", "tokenizer.json"} {
		digest := sha256.Sum256([]byte(files[path]))
		model.Assets = append(model.Assets, Asset{Path: path, Size: int64(len(files[path])), SHA256: hex.EncodeToString(digest[:])})
	}
	return model
}

func TestInstallFallsBackToTheNextSourceAndWritesTheManifest(t *testing.T) {
	files := map[string]string{"model.onnx": strings.Repeat("m", 4096), "tokenizer.json": `{"model":{}}`}
	model := testModel(files)
	missing := httptest.NewServer(http.NotFoundHandler())
	defer missing.Close()
	var requests []string
	serving := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.URL.Path)
		prefix := "/" + model.RepoID + "/resolve/" + model.Revision + "/"
		body, ok := files[strings.TrimPrefix(request.URL.Path, prefix)]
		if !ok || !strings.HasPrefix(request.URL.Path, prefix) {
			http.NotFound(writer, request)
			return
		}
		http.ServeContent(writer, request, "", testTime, strings.NewReader(body))
	}))
	defer serving.Close()

	root := t.TempDir()
	installer := Installer{Root: root, BaseURLs: []string{missing.URL, serving.URL}}
	var lastDone, lastTotal int64
	installation, err := installer.Install(context.Background(), model, func(done, total int64) { lastDone, lastTotal = done, total })
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if lastTotal != model.Size() || lastDone != model.Size() {
		t.Fatalf("progress = %d/%d, want %d", lastDone, lastTotal, model.Size())
	}
	if _, ok := installer.Installed(model); !ok {
		t.Fatal("Installed does not recognize the installed model")
	}
	data, err := os.ReadFile(filepath.Join(installation.Directory, WorkerManifestName))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest["identity"] != model.Identity() || manifest["pooling"] != "cls" || manifest["normalize"] != true {
		t.Fatalf("worker manifest = %v", manifest)
	}

	// A file that lost bytes resumes; a reinstall of a complete model fetches nothing.
	requests = nil
	if _, err := installer.Install(context.Background(), model, nil); err != nil || len(requests) != 0 {
		t.Fatalf("reinstall fetched %v: %v", requests, err)
	}
	if err := os.Remove(filepath.Join(installation.Directory, WorkerManifestName)); err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(installation.Directory, "model.onnx")
	if err := os.Rename(modelPath, modelPath+".part"); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(modelPath+".part", 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := installer.Install(context.Background(), model, nil); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if _, ok := installer.Installed(model); !ok {
		t.Fatal("a resumed install is not recognized")
	}
}

func TestInstallRejectsAFileThatDoesNotMatchItsPin(t *testing.T) {
	files := map[string]string{"model.onnx": "model", "tokenizer.json": "tokenizer"}
	model := testModel(files)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(strings.Repeat("x", 5)))
	}))
	defer server.Close()
	installer := Installer{Root: t.TempDir(), BaseURLs: []string{server.URL}}
	if _, err := installer.Install(context.Background(), model, nil); err == nil {
		t.Fatal("Install accepted files that do not match their pins")
	}
	if _, ok := installer.Installed(model); ok {
		t.Fatal("a failed install reports a model")
	}
}

func TestImportCopiesOnlyThePinnedFiles(t *testing.T) {
	files := map[string]string{"model.onnx": "model bytes", "tokenizer.json": "tokenizer bytes"}
	model := testModel(files)
	source := t.TempDir()
	for path, body := range files {
		if err := os.WriteFile(filepath.Join(source, path), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	installer := Installer{Root: t.TempDir()}
	if _, err := installer.Import(context.Background(), model, source); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, ok := installer.Installed(model); !ok {
		t.Fatal("an imported model is not recognized")
	}
	if err := os.WriteFile(filepath.Join(source, "tokenizer.json"), []byte("tokenizer BYTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (Installer{Root: t.TempDir()}).Import(context.Background(), model, source); err == nil {
		t.Fatal("Import accepted a changed file")
	}
}

func TestGraniteIsPinned(t *testing.T) {
	if Granite.Dimensions != 384 || Granite.Pooling != "cls" || Granite.PadTokenID != 1 || len(Granite.Revision) != 40 {
		t.Fatalf("granite pin = %+v", Granite)
	}
	for _, asset := range Granite.Assets {
		if len(asset.SHA256) != 64 || asset.Size <= 0 {
			t.Fatalf("asset pin = %+v", asset)
		}
	}
	if model, ok := Lookup(Granite.ID); !ok || model.Identity() != Granite.Identity() {
		t.Fatal("Lookup does not find granite")
	}
}
