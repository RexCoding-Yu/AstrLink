package contract

import "time"

// KnowledgeRuntimeState is where the knowledge base's search engine (seekdb)
// stands on this device.
type KnowledgeRuntimeState string

const (
	// KnowledgeRuntimeUnsupported: seekdb ships no runtime for this platform.
	KnowledgeRuntimeUnsupported  KnowledgeRuntimeState = "unsupported"
	KnowledgeRuntimeNotInstalled KnowledgeRuntimeState = "not_installed"
	KnowledgeRuntimeInstalling   KnowledgeRuntimeState = "installing"
	// KnowledgeRuntimeInstalled: on disk, not running.
	KnowledgeRuntimeInstalled KnowledgeRuntimeState = "installed"
	KnowledgeRuntimeStarting  KnowledgeRuntimeState = "starting"
	KnowledgeRuntimeRunning   KnowledgeRuntimeState = "running"
	KnowledgeRuntimeFailed    KnowledgeRuntimeState = "failed"
)

// KnowledgeProgress counts the bytes of a download; Total is 0 while it is
// not yet known.
type KnowledgeProgress struct {
	Done  int64 `json:"done"`
	Total int64 `json:"total"`
}

type KnowledgeRuntimeStatus struct {
	State KnowledgeRuntimeState `json:"state"`
	// Reason says why an unsupported platform is unsupported.
	Reason string `json:"reason,omitempty"`
	// Version is the seekdb release the runtime carries.
	Version       string             `json:"version"`
	Platform      string             `json:"platform,omitempty"`
	DownloadBytes int64              `json:"download_bytes,omitempty"`
	Progress      *KnowledgeProgress `json:"progress,omitempty"`
	Error         string             `json:"error,omitempty"`
	// DataBytes is the disk the server's data directory takes.
	DataBytes int64      `json:"data_bytes"`
	StartedAt *time.Time `json:"started_at,omitempty"`
}

type KnowledgeModelState string

const (
	KnowledgeModelNotInstalled KnowledgeModelState = "not_installed"
	KnowledgeModelInstalling   KnowledgeModelState = "installing"
	KnowledgeModelInstalled    KnowledgeModelState = "installed"
	KnowledgeModelFailed       KnowledgeModelState = "failed"
)

// KnowledgeEmbeddingModel is a local embedding model AstrLink can download.
type KnowledgeEmbeddingModel struct {
	ID            string              `json:"id"`
	DisplayName   string              `json:"display_name"`
	RepoID        string              `json:"repo_id"`
	Revision      string              `json:"revision"`
	License       string              `json:"license"`
	Dimensions    int                 `json:"dimensions"`
	DownloadBytes int64               `json:"download_bytes"`
	State         KnowledgeModelState `json:"state"`
	Progress      *KnowledgeProgress  `json:"progress,omitempty"`
	Error         string              `json:"error,omitempty"`
	// Loaded reports a worker that holds the model in memory now.
	Loaded bool `json:"loaded"`
}

// KnowledgeEnvironment is everything the knowledge base runs on.
type KnowledgeEnvironment struct {
	Runtime         KnowledgeRuntimeStatus    `json:"runtime"`
	EmbeddingModels []KnowledgeEmbeddingModel `json:"embedding_models"`
}

// KnowledgeImportRequest names a file or directory on this device.
type KnowledgeImportRequest struct {
	Path string `json:"path"`
}

type KnowledgeCheckStep struct {
	Name       string `json:"name"`
	OK         bool   `json:"ok"`
	DurationMS int64  `json:"duration_ms"`
	Detail     string `json:"detail,omitempty"`
}

// KnowledgeRuntimeCheck is a run of the search engine's self-test: full-text,
// vector and hybrid queries on a scratch table.
type KnowledgeRuntimeCheck struct {
	OK     bool                 `json:"ok"`
	Engine string               `json:"engine,omitempty"`
	Steps  []KnowledgeCheckStep `json:"steps"`
}

// KnowledgeEmbeddingPair is the cosine similarity of two sample texts.
type KnowledgeEmbeddingPair struct {
	Left       string  `json:"left"`
	Right      string  `json:"right"`
	Similarity float64 `json:"similarity"`
}

// KnowledgeEmbeddingCheck is a run of a local model on sample texts.
type KnowledgeEmbeddingCheck struct {
	Dimensions int                      `json:"dimensions"`
	LoadMS     int64                    `json:"load_ms"`
	EmbedMS    int64                    `json:"embed_ms"`
	Pairs      []KnowledgeEmbeddingPair `json:"pairs"`
}
