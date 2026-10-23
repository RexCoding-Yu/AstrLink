// Package knowledge is the gateway's built-in knowledge base. Environment
// owns what it runs on: the seekdb runtime and server, and the local
// embedding models.
package knowledge

import (
	"context"
	"crypto/hkdf"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/embedmodel"
	"github.com/QuantumNous/astrlink/core/internal/seekdb"
)

const (
	// Database is the seekdb database the knowledge base keeps its indexes in.
	Database        = "astrlink"
	passwordKeyInfo = "astrlink/knowledge-seekdb-root/v1"
)

var (
	ErrUnsupported   = errors.New("the knowledge base search engine does not run on this device")
	ErrBusy          = errors.New("another knowledge base operation is in progress")
	ErrNotInstalled  = errors.New("the knowledge base search engine is not installed")
	ErrNotRunning    = errors.New("the knowledge base search engine is not running")
	ErrUnknownModel  = errors.New("unknown embedding model")
	ErrModelNotReady = errors.New("the embedding model is not installed")
)

// DerivePassword derives seekdb's root password from the device's audit data
// key, so it is never stored and stays the same across restarts.
func DerivePassword(auditKey []byte) (string, error) {
	key, err := hkdf.Key(sha256.New, auditKey, nil, passwordKeyInfo, 24)
	if err != nil {
		return "", fmt.Errorf("derive the knowledge base password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(key), nil
}

type EnvironmentConfig struct {
	// Directory holds runtime/, seekdb/ and models/.
	Directory string
	// WorkerExecutable is the inference worker, run in embedding mode.
	WorkerExecutable string
	Password         func(context.Context) (string, error)
	HTTPClient       *http.Client
	Logf             func(string, ...any)
}

// Environment installs and runs the knowledge base's search engine and local
// embedding models. Downloads and server starts run in the background; the
// status reports their progress.
type Environment struct {
	config   EnvironmentConfig
	runtime  seekdb.Installer
	models   embedmodel.Installer
	worker   *embedmodel.Worker
	bundle   seekdb.Bundle
	reason   string
	ctx      context.Context
	cancel   context.CancelFunc
	tasks    sync.WaitGroup
	mu       sync.Mutex
	phase    contract.KnowledgeRuntimeState
	progress *contract.KnowledgeProgress
	failure  string
	server   *seekdb.Server
	started  time.Time
	model    map[string]*modelPhase
}

type modelPhase struct {
	installing bool
	progress   *contract.KnowledgeProgress
	failure    string
}

func NewEnvironment(config EnvironmentConfig) *Environment {
	ctx, cancel := context.WithCancel(context.Background())
	environment := &Environment{
		config:  config,
		runtime: seekdb.Installer{Root: filepath.Join(config.Directory, "runtime"), Client: config.HTTPClient},
		models:  embedmodel.Installer{Root: filepath.Join(config.Directory, "models"), Client: config.HTTPClient},
		worker:  &embedmodel.Worker{Executable: config.WorkerExecutable},
		ctx:     ctx,
		cancel:  cancel,
		model:   map[string]*modelPhase{},
	}
	bundle, ok := seekdb.CurrentBundle()
	if !ok {
		environment.reason = "platform"
	} else if reason := platformReason(); reason != "" {
		environment.reason = reason
	}
	environment.bundle = bundle
	return environment
}

func (environment *Environment) logf(format string, args ...any) {
	if environment.config.Logf != nil {
		environment.config.Logf(format, args...)
	}
}

func (environment *Environment) dataDir() string {
	return filepath.Join(environment.config.Directory, "seekdb")
}

// Status reports the runtime and every local embedding model.
func (environment *Environment) Status() contract.KnowledgeEnvironment {
	environment.mu.Lock()
	defer environment.mu.Unlock()
	status := contract.KnowledgeEnvironment{Runtime: environment.runtimeStatusLocked()}
	loaded := environment.worker.Running()
	for _, model := range embedmodel.Models() {
		entry := contract.KnowledgeEmbeddingModel{
			ID: model.ID, DisplayName: model.DisplayName, RepoID: model.RepoID, Revision: model.Revision,
			License: model.License, Dimensions: model.Dimensions, DownloadBytes: model.Size(),
			State: contract.KnowledgeModelNotInstalled,
		}
		if _, ok := environment.models.Installed(model); ok {
			entry.State = contract.KnowledgeModelInstalled
			entry.Loaded = loaded
		}
		if phase := environment.model[model.ID]; phase != nil {
			switch {
			case phase.installing:
				entry.State, entry.Progress = contract.KnowledgeModelInstalling, phase.progress
			case phase.failure != "" && entry.State != contract.KnowledgeModelInstalled:
				entry.State, entry.Error = contract.KnowledgeModelFailed, phase.failure
			}
		}
		status.EmbeddingModels = append(status.EmbeddingModels, entry)
	}
	return status
}

func (environment *Environment) runtimeStatusLocked() contract.KnowledgeRuntimeStatus {
	status := contract.KnowledgeRuntimeStatus{
		Version: seekdb.Version, Platform: environment.bundle.Platform, DownloadBytes: environment.bundle.Size,
		DataBytes: directorySize(environment.dataDir()),
	}
	switch {
	case environment.reason != "":
		status.State, status.Reason = contract.KnowledgeRuntimeUnsupported, environment.reason
	case environment.phase != "":
		status.State, status.Progress, status.Error = environment.phase, environment.progress, environment.failure
	default:
		status.State = contract.KnowledgeRuntimeNotInstalled
		if _, ok := environment.runtime.Installed(environment.bundle); ok {
			status.State = contract.KnowledgeRuntimeInstalled
		}
		status.Error = environment.failure
	}
	if environment.server != nil {
		started := environment.started
		status.StartedAt = &started
	}
	return status
}

// InstallRuntime downloads the seekdb runtime in the background.
func (environment *Environment) InstallRuntime() error {
	environment.mu.Lock()
	defer environment.mu.Unlock()
	if environment.reason != "" {
		return ErrUnsupported
	}
	if environment.phase != "" {
		return ErrBusy
	}
	environment.phase, environment.failure = contract.KnowledgeRuntimeInstalling, ""
	environment.progress = &contract.KnowledgeProgress{Total: environment.bundle.Size}
	environment.tasks.Add(1)
	go func() {
		defer environment.tasks.Done()
		_, err := environment.runtime.Install(environment.ctx, environment.bundle, func(done, total int64) {
			environment.mu.Lock()
			environment.progress = &contract.KnowledgeProgress{Done: done, Total: total}
			environment.mu.Unlock()
		})
		environment.mu.Lock()
		defer environment.mu.Unlock()
		environment.phase, environment.progress = "", nil
		if err != nil {
			environment.failure = err.Error()
			environment.logf("install the knowledge base search engine: %v", err)
		}
	}()
	return nil
}

// ImportRuntime unpacks a runtime archive the user downloaded.
func (environment *Environment) ImportRuntime(ctx context.Context, path string) error {
	if err := environment.claim(contract.KnowledgeRuntimeInstalling); err != nil {
		return err
	}
	_, err := environment.runtime.Import(ctx, environment.bundle, path)
	environment.release(err)
	return err
}

// RemoveRuntime stops the server and deletes the runtime files; the data
// directory stays.
func (environment *Environment) RemoveRuntime() error {
	if err := environment.claim(contract.KnowledgeRuntimeInstalling); err != nil {
		return err
	}
	environment.stopServer()
	err := environment.runtime.Remove(environment.bundle)
	environment.release(err)
	return err
}

// StartServer starts seekdb in the background.
func (environment *Environment) StartServer() error {
	installation, ok := environment.runtime.Installed(environment.bundle)
	if environment.reason == "" && !ok {
		return ErrNotInstalled
	}
	environment.mu.Lock()
	running := environment.server != nil
	environment.mu.Unlock()
	if running {
		return nil
	}
	if err := environment.claim(contract.KnowledgeRuntimeStarting); err != nil {
		return err
	}
	environment.tasks.Add(1)
	go func() {
		defer environment.tasks.Done()
		password, err := environment.config.Password(environment.ctx)
		var server *seekdb.Server
		if err == nil {
			server, err = seekdb.Start(environment.ctx, seekdb.Options{
				Installation: installation, DataDir: environment.dataDir(), Password: password, Database: Database,
			})
		}
		environment.mu.Lock()
		defer environment.mu.Unlock()
		environment.phase, environment.progress = "", nil
		if err != nil {
			environment.phase, environment.failure = contract.KnowledgeRuntimeFailed, describeStartFailure(err)
			environment.logf("start the knowledge base search engine: %v", err)
			return
		}
		environment.server, environment.started = server, time.Now()
		environment.phase = contract.KnowledgeRuntimeRunning
		environment.tasks.Add(1)
		go environment.watch(server)
	}()
	return nil
}

// watch marks the runtime failed when the server exits on its own.
func (environment *Environment) watch(server *seekdb.Server) {
	defer environment.tasks.Done()
	select {
	case <-server.Exited():
	case <-environment.ctx.Done():
		return
	}
	environment.mu.Lock()
	defer environment.mu.Unlock()
	if environment.server == server {
		environment.server = nil
		environment.phase, environment.failure = contract.KnowledgeRuntimeFailed, "the search engine exited unexpectedly"
		environment.logf("the knowledge base search engine exited unexpectedly")
	}
}

func describeStartFailure(err error) string {
	if seekdb.IsAccessDenied(err) {
		return "the search engine's data was created with another key; clear its data to start again"
	}
	return err.Error()
}

// StopServer stops seekdb.
func (environment *Environment) StopServer() error {
	environment.mu.Lock()
	if environment.phase == contract.KnowledgeRuntimeStarting || environment.phase == contract.KnowledgeRuntimeInstalling {
		environment.mu.Unlock()
		return ErrBusy
	}
	environment.mu.Unlock()
	environment.stopServer()
	return nil
}

func (environment *Environment) stopServer() {
	environment.mu.Lock()
	server := environment.server
	environment.server = nil
	if environment.phase == contract.KnowledgeRuntimeRunning || environment.phase == contract.KnowledgeRuntimeFailed {
		environment.phase, environment.failure = "", ""
	}
	environment.mu.Unlock()
	if server != nil {
		_ = server.Close()
	}
}

// ResetData stops the server and deletes its data directory.
func (environment *Environment) ResetData() error {
	if err := environment.claim(contract.KnowledgeRuntimeInstalling); err != nil {
		return err
	}
	environment.stopServer()
	err := os.RemoveAll(environment.dataDir())
	environment.release(err)
	return err
}

// claim marks a synchronous operation that excludes others.
func (environment *Environment) claim(phase contract.KnowledgeRuntimeState) error {
	environment.mu.Lock()
	defer environment.mu.Unlock()
	if environment.reason != "" {
		return ErrUnsupported
	}
	if environment.phase == contract.KnowledgeRuntimeInstalling || environment.phase == contract.KnowledgeRuntimeStarting {
		return ErrBusy
	}
	environment.phase, environment.failure, environment.progress = phase, "", nil
	return nil
}

func (environment *Environment) release(err error) {
	environment.mu.Lock()
	defer environment.mu.Unlock()
	environment.phase, environment.progress = "", nil
	if environment.server != nil {
		environment.phase = contract.KnowledgeRuntimeRunning
	}
	if err != nil {
		environment.failure = err.Error()
	}
}

// DB returns the running server's pool.
func (environment *Environment) DB() (*sql.DB, error) {
	environment.mu.Lock()
	defer environment.mu.Unlock()
	if environment.server == nil {
		return nil, ErrNotRunning
	}
	return environment.server.DB(), nil
}

// InstallModel downloads a local embedding model in the background.
func (environment *Environment) InstallModel(id string) error {
	model, ok := embedmodel.Lookup(id)
	if !ok {
		return ErrUnknownModel
	}
	environment.mu.Lock()
	defer environment.mu.Unlock()
	if phase := environment.model[id]; phase != nil && phase.installing {
		return ErrBusy
	}
	phase := &modelPhase{installing: true, progress: &contract.KnowledgeProgress{Total: model.Size()}}
	environment.model[id] = phase
	environment.tasks.Add(1)
	go func() {
		defer environment.tasks.Done()
		_, err := environment.models.Install(environment.ctx, model, func(done, total int64) {
			environment.mu.Lock()
			phase.progress = &contract.KnowledgeProgress{Done: done, Total: total}
			environment.mu.Unlock()
		})
		environment.mu.Lock()
		defer environment.mu.Unlock()
		phase.installing, phase.progress = false, nil
		if err != nil {
			phase.failure = err.Error()
			environment.logf("install embedding model %s: %v", id, err)
		}
	}()
	return nil
}

// ImportModel copies a model directory the user downloaded.
func (environment *Environment) ImportModel(ctx context.Context, id, path string) error {
	model, ok := embedmodel.Lookup(id)
	if !ok {
		return ErrUnknownModel
	}
	environment.mu.Lock()
	if phase := environment.model[id]; phase != nil && phase.installing {
		environment.mu.Unlock()
		return ErrBusy
	}
	environment.model[id] = &modelPhase{installing: true}
	environment.mu.Unlock()
	_, err := environment.models.Import(ctx, model, path)
	environment.mu.Lock()
	defer environment.mu.Unlock()
	environment.model[id] = &modelPhase{}
	if err != nil {
		environment.model[id].failure = err.Error()
	}
	return err
}

// RemoveModel unloads and deletes a local model.
func (environment *Environment) RemoveModel(id string) error {
	model, ok := embedmodel.Lookup(id)
	if !ok {
		return ErrUnknownModel
	}
	environment.mu.Lock()
	if phase := environment.model[id]; phase != nil && phase.installing {
		environment.mu.Unlock()
		return ErrBusy
	}
	delete(environment.model, id)
	environment.mu.Unlock()
	environment.worker.Close()
	return environment.models.Remove(model)
}

// Embed runs a local model.
func (environment *Environment) Embed(ctx context.Context, id string, kind embedmodel.Kind, texts []string) ([][]float32, error) {
	model, ok := embedmodel.Lookup(id)
	if !ok {
		return nil, ErrUnknownModel
	}
	installation, ok := environment.models.Installed(model)
	if !ok {
		return nil, ErrModelNotReady
	}
	return environment.worker.Embed(ctx, installation, kind, texts)
}

// Close stops background work, the server and the worker.
func (environment *Environment) Close() {
	environment.cancel()
	environment.stopServer()
	environment.worker.Close()
	environment.tasks.Wait()
}

func directorySize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if info, err := entry.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
