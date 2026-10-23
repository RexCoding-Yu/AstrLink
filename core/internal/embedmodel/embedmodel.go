// Package embedmodel installs the local embedding models the knowledge base
// can use and describes each one to the inference worker, which loads it
// with --embedding-model-dir.
package embedmodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/QuantumNous/astrlink/core/internal/download"
)

// WorkerManifestName is the file the worker reads from a model directory.
const WorkerManifestName = "astrlink-embedding-model.json"

// Asset is one pinned file of a model, at Path inside both the repository
// and the installed directory.
type Asset struct {
	Path   string
	Size   int64
	SHA256 string
}

// Model pins a local embedding model and how to run it.
type Model struct {
	ID          string
	DisplayName string
	RepoID      string
	Revision    string
	License     string
	Dimensions  int
	MaxTokens   int
	Pooling     string
	PadTokenID  int
	// OutputName holds the encoder's hidden states. The granite export calls
	// them "logits".
	OutputName string
	Assets     []Asset
}

// Granite is IBM's granite-embedding-107m-multilingual, which covers Chinese
// and English text well at 384 dimensions. Its ONNX export stays in fp32:
// dynamic int8 quantization of this model loses almost all retrieval quality.
var Granite = Model{
	ID:          "granite-embedding-107m-multilingual",
	DisplayName: "Granite Embedding 107M Multilingual",
	RepoID:      "ibm-granite/granite-embedding-107m-multilingual",
	Revision:    "d6cffd338414d6a1c1f5decfad5fec62eebc90d5",
	License:     "Apache-2.0",
	Dimensions:  384,
	MaxTokens:   512,
	Pooling:     "cls",
	PadTokenID:  1,
	OutputName:  "logits",
	Assets: []Asset{
		{Path: "model.onnx", Size: 428102968, SHA256: "387e6f91dce3c651cb3dc06661d74fb1d3947f20ed9c3ecf49d2b3b77086b954"},
		{Path: "tokenizer.json", Size: 9081382, SHA256: "7fc9e475f2ac473f070052d92b0c46ec2b91d9cd672935feb6fd837a65d33f78"},
	},
}

// Models lists the local models this build can install.
func Models() []Model { return []Model{Granite} }

// Lookup returns the model with id.
func Lookup(id string) (Model, bool) {
	for _, model := range Models() {
		if model.ID == id {
			return model, true
		}
	}
	return Model{}, false
}

// Size is the total download size.
func (model Model) Size() int64 {
	var total int64
	for _, asset := range model.Assets {
		total += asset.Size
	}
	return total
}

// Identity names the exact pinned files.
func (model Model) Identity() string { return model.RepoID + "@" + model.Revision }

// Installation is a model directory ready for the worker.
type Installation struct {
	Directory string
	Model     Model
}

// Installer downloads models under Root, one directory per model.
type Installer struct {
	Root   string
	Client *http.Client
	// BaseURLs are tried in order; the default is Hugging Face, then its
	// hf-mirror.com mirror. Every file is checked against its pin.
	BaseURLs []string
}

var defaultBaseURLs = []string{"https://huggingface.co/", "https://hf-mirror.com/"}

func (installer Installer) directory(model Model) string {
	return filepath.Join(installer.Root, model.ID)
}

// Installed reports a model whose worker manifest matches its pin and whose
// files are all present at their pinned sizes.
func (installer Installer) Installed(model Model) (Installation, bool) {
	directory := installer.directory(model)
	data, err := os.ReadFile(filepath.Join(directory, WorkerManifestName))
	if err != nil {
		return Installation{}, false
	}
	var manifest workerManifest
	if json.Unmarshal(data, &manifest) != nil || manifest.Identity != model.Identity() {
		return Installation{}, false
	}
	for _, asset := range model.Assets {
		info, err := os.Stat(filepath.Join(directory, filepath.FromSlash(asset.Path)))
		if err != nil || !info.Mode().IsRegular() || info.Size() != asset.Size {
			return Installation{}, false
		}
	}
	return Installation{Directory: directory, Model: model}, true
}

// Install downloads every asset, resuming partial files, verifies each one
// and writes the worker manifest last, so a half-done install never loads.
func (installer Installer) Install(ctx context.Context, model Model, progress download.Progress) (Installation, error) {
	if installation, ok := installer.Installed(model); ok {
		return installation, nil
	}
	directory := installer.directory(model)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return Installation{}, fmt.Errorf("prepare embedding model directory: %w", err)
	}
	total := model.Size()
	var finished int64
	for _, asset := range model.Assets {
		path := filepath.Join(directory, filepath.FromSlash(asset.Path))
		if download.Verify(path, asset.Size, asset.SHA256) == nil {
			finished += asset.Size
			continue
		}
		report := func(done, _ int64) {
			if progress != nil {
				progress(finished+done, total)
			}
		}
		if err := installer.fetch(ctx, model, asset, path, report); err != nil {
			return Installation{}, err
		}
		finished += asset.Size
	}
	if err := writeWorkerManifest(directory, model); err != nil {
		return Installation{}, err
	}
	return Installation{Directory: directory, Model: model}, nil
}

func (installer Installer) fetch(ctx context.Context, model Model, asset Asset, path string, progress download.Progress) error {
	partial := path + ".part"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	bases := installer.BaseURLs
	if len(bases) == 0 {
		bases = defaultBaseURLs
	}
	var failures []string
	for _, base := range bases {
		url := strings.TrimRight(base, "/") + "/" + model.RepoID + "/resolve/" + model.Revision + "/" + asset.Path
		err := download.File(ctx, download.Client(installer.Client), url, partial, asset.Size, progress)
		if err == nil {
			err = download.Verify(partial, asset.Size, asset.SHA256)
			if err != nil {
				_ = os.Remove(partial)
			}
		}
		if err == nil {
			return os.Rename(partial, path)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		failures = append(failures, fmt.Sprintf("%s: %v", url, err))
	}
	return fmt.Errorf("download %s: %s", asset.Path, strings.Join(failures, "; "))
}

// Import copies a model the user downloaded themselves. The directory must
// hold every pinned file, unchanged.
func (installer Installer) Import(ctx context.Context, model Model, source string) (Installation, error) {
	for _, asset := range model.Assets {
		if err := download.Verify(filepath.Join(source, filepath.FromSlash(asset.Path)), asset.Size, asset.SHA256); err != nil {
			if errors.Is(err, download.ErrMismatch) {
				return Installation{}, fmt.Errorf("%s is not the pinned %s", asset.Path, model.Identity())
			}
			return Installation{}, fmt.Errorf("read %s: %w", asset.Path, err)
		}
	}
	directory := installer.directory(model)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return Installation{}, fmt.Errorf("prepare embedding model directory: %w", err)
	}
	for _, asset := range model.Assets {
		if err := ctx.Err(); err != nil {
			return Installation{}, err
		}
		target := filepath.Join(directory, filepath.FromSlash(asset.Path))
		if err := copyFile(filepath.Join(source, filepath.FromSlash(asset.Path)), target); err != nil {
			return Installation{}, fmt.Errorf("copy %s: %w", asset.Path, err)
		}
	}
	if err := writeWorkerManifest(directory, model); err != nil {
		return Installation{}, err
	}
	return Installation{Directory: directory, Model: model}, nil
}

// Remove deletes the model's directory.
func (installer Installer) Remove(model Model) error {
	return os.RemoveAll(installer.directory(model))
}

type workerManifest struct {
	Version       int          `json:"version"`
	Identity      string       `json:"identity"`
	ModelPath     string       `json:"model_path"`
	TokenizerPath string       `json:"tokenizer_path"`
	InputNames    inputNames   `json:"input_names"`
	OutputName    string       `json:"output_name"`
	Pooling       string       `json:"pooling"`
	Normalize     bool         `json:"normalize"`
	Dimensions    int          `json:"dimensions"`
	MaxTokens     int          `json:"max_tokens"`
	PadTokenID    int          `json:"pad_token_id"`
	QueryPrefix   string       `json:"query_prefix"`
	PassagePrefix string       `json:"passage_prefix"`
	Files         []workerFile `json:"files"`
}

type inputNames struct {
	InputIDs      string `json:"input_ids"`
	AttentionMask string `json:"attention_mask"`
}

type workerFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func writeWorkerManifest(directory string, model Model) error {
	manifest := workerManifest{
		Version:       1,
		Identity:      model.Identity(),
		ModelPath:     "model.onnx",
		TokenizerPath: "tokenizer.json",
		InputNames:    inputNames{InputIDs: "input_ids", AttentionMask: "attention_mask"},
		OutputName:    model.OutputName,
		Pooling:       model.Pooling,
		Normalize:     true,
		Dimensions:    model.Dimensions,
		MaxTokens:     model.MaxTokens,
		PadTokenID:    model.PadTokenID,
	}
	for _, asset := range model.Assets {
		manifest.Files = append(manifest.Files, workerFile{Path: asset.Path, Size: asset.Size, SHA256: asset.SHA256})
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	temporary := filepath.Join(directory, WorkerManifestName+".tmp")
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, filepath.Join(directory, WorkerManifestName))
}

func copyFile(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target+".tmp", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	if closeErr := output.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = os.Remove(target + ".tmp")
		return copyErr
	}
	return os.Rename(target+".tmp", target)
}
