package embedmodel

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

var testTime = time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)

// workerExecutable is the built inference worker; the worker also needs ONNX
// Runtime beside it or, on Linux, ASTRLINK_ONNX_RUNTIME_PATH.
func workerExecutable(t *testing.T) string {
	t.Helper()
	executable := os.Getenv("ASTRLINK_EMBEDDING_WORKER")
	if executable == "" {
		t.Skip("set ASTRLINK_EMBEDDING_WORKER to the built astrlink-classifier-worker")
	}
	return executable
}

// syntheticInstallation lays out the worker's synthetic fixture: a token
// lookup model and a whitespace word tokenizer.
func syntheticInstallation(t *testing.T) Installation {
	t.Helper()
	model, err := os.ReadFile(filepath.Join("..", "..", "..", "apps", "classifier-worker", "testdata", "synthetic-embedding.onnx"))
	if err != nil {
		t.Fatalf("read the synthetic model: %v", err)
	}
	vocabulary := map[string]int{"[PAD]": 0, "[UNK]": 1, "hello": 2, "world": 3, "deploy": 4, "windows": 5, "firewall": 6, "macos": 7}
	tokenizer, err := json.Marshal(map[string]any{
		"version": "1.0", "truncation": nil, "padding": nil, "added_tokens": []any{}, "normalizer": nil,
		"pre_tokenizer": map[string]any{"type": "Whitespace"}, "post_processor": nil, "decoder": nil,
		"model": map[string]any{"type": "WordLevel", "vocab": vocabulary, "unk_token": "[UNK]"},
	})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"model.onnx": string(model), "tokenizer.json": string(tokenizer)}
	pinned := testModel(files)
	directory := t.TempDir()
	for path, body := range files {
		if err := os.WriteFile(filepath.Join(directory, path), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	installation, err := (Installer{Root: t.TempDir()}).Import(context.Background(), pinned, directory)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	return installation
}

func TestWorkerEmbedsWithTheSyntheticModel(t *testing.T) {
	worker := &Worker{Executable: workerExecutable(t), IdleAfter: time.Minute}
	defer worker.Close()
	installation := syntheticInstallation(t)
	texts := make([]string, 0, 11)
	for i := 0; i < 11; i++ { // more than one batch
		texts = append(texts, "hello world")
	}
	texts[10] = "deploy windows firewall"
	vectors, err := worker.Embed(context.Background(), installation, Passage, texts)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vectors) != len(texts) {
		t.Fatalf("%d vectors for %d texts", len(vectors), len(texts))
	}
	for _, vector := range vectors {
		var norm float64
		for _, value := range vector {
			norm += float64(value * value)
		}
		if math.Abs(math.Sqrt(norm)-1) > 1e-4 {
			t.Fatalf("vector norm = %f", math.Sqrt(norm))
		}
	}
	if vectors[0][0] == vectors[10][0] {
		t.Fatal("different first tokens gave the same vector")
	}
	if !worker.Running() {
		t.Fatal("the worker stopped after a request")
	}
	// An empty text fails the request and stops the worker; the next request starts it again.
	if _, err := worker.Embed(context.Background(), installation, Query, []string{"  "}); err == nil {
		t.Fatal("an empty text was embedded")
	}
	if _, err := worker.Embed(context.Background(), installation, Query, []string{"macos"}); err != nil {
		t.Fatalf("Embed after a failure: %v", err)
	}
}

func TestWorkerStopsWhenIdle(t *testing.T) {
	worker := &Worker{Executable: workerExecutable(t), IdleAfter: 200 * time.Millisecond}
	defer worker.Close()
	if _, err := worker.Embed(context.Background(), syntheticInstallation(t), Query, []string{"hello"}); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for worker.Running() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if worker.Running() {
		t.Fatal("the worker kept running after the idle timeout")
	}
}

// TestGraniteRetrieval checks the worker against the Python measurements on
// the small history-search set: ASTRLINK_GRANITE_DIR is an installed granite
// directory and ASTRLINK_EMBED_DATASET the dataset.json.
func TestGraniteRetrieval(t *testing.T) {
	executable := workerExecutable(t)
	directory, datasetPath := os.Getenv("ASTRLINK_GRANITE_DIR"), os.Getenv("ASTRLINK_EMBED_DATASET")
	if directory == "" || datasetPath == "" {
		t.Skip("set ASTRLINK_GRANITE_DIR and ASTRLINK_EMBED_DATASET")
	}
	data, err := os.ReadFile(datasetPath)
	if err != nil {
		t.Fatal(err)
	}
	var dataset struct {
		Targets   [][2]string `json:"targets"`
		Negatives [][2]string `json:"negatives"`
		Queries   [][2]string `json:"queries"`
	}
	if err := json.Unmarshal(data, &dataset); err != nil {
		t.Fatal(err)
	}
	passages := append(dataset.Targets, dataset.Negatives...)
	texts := make([]string, len(passages))
	for i, passage := range passages {
		texts[i] = passage[1]
	}
	queries := make([]string, len(dataset.Queries))
	for i, query := range dataset.Queries {
		queries[i] = query[1]
	}
	worker := &Worker{Executable: executable}
	defer worker.Close()
	installation, ok := Installer{Root: filepath.Dir(directory)}.Installed(Granite)
	if !ok || installation.Directory != directory {
		// A plain download of the pinned files: verify and install it first.
		installation, err = (Installer{Root: t.TempDir()}).Import(context.Background(), Granite, directory)
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
	}
	started := time.Now()
	passageVectors, err := worker.Embed(context.Background(), installation, Passage, texts)
	if err != nil {
		t.Fatalf("embed passages: %v", err)
	}
	t.Logf("load and embed %d passages: %s", len(texts), time.Since(started).Round(time.Millisecond))
	started = time.Now()
	queryVectors, err := worker.Embed(context.Background(), installation, Query, queries)
	if err != nil {
		t.Fatalf("embed queries: %v", err)
	}
	t.Logf("embed %d queries: %s", len(queries), time.Since(started).Round(time.Millisecond))
	hits1, hits5 := 0, 0
	for i, query := range queryVectors {
		type scored struct {
			id    string
			score float64
		}
		ranked := make([]scored, len(passages))
		for j, passage := range passageVectors {
			var dot float64
			for k := range query {
				dot += float64(query[k] * passage[k])
			}
			ranked[j] = scored{passages[j][0], dot}
		}
		sort.Slice(ranked, func(a, b int) bool { return ranked[a].score > ranked[b].score })
		for rank, item := range ranked[:5] {
			if item.id == dataset.Queries[i][0] {
				if rank == 0 {
					hits1++
				}
				hits5++
			}
		}
	}
	r1, r5 := float64(hits1)/float64(len(queries)), float64(hits5)/float64(len(queries))
	t.Logf("granite R@1 %.3f, R@5 %.3f", r1, r5)
	if r1 < 0.9 || r5 < 0.95 {
		t.Fatalf("granite retrieval regressed: R@1 %.3f, R@5 %.3f (Python: 0.933, 1.0)", r1, r5)
	}
}
