package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/knowledge"
)

const KnowledgePath = "/control/v1/knowledge"

// knowledgeCheckBudget bounds a self-test; loading a model on a slow disk
// takes seconds.
const knowledgeCheckBudget = 3 * time.Minute

// KnowledgeEnvironment is what the knowledge base runs on;
// *knowledge.Environment satisfies it.
type KnowledgeEnvironment interface {
	Status() contract.KnowledgeEnvironment
	InstallRuntime() error
	ImportRuntime(context.Context, string) error
	RemoveRuntime() error
	StartServer() error
	StopServer() error
	ResetData() error
	CheckRuntime(context.Context) (contract.KnowledgeRuntimeCheck, error)
	InstallModel(string) error
	ImportModel(context.Context, string, string) error
	RemoveModel(string) error
	CheckModel(context.Context, string) (contract.KnowledgeEmbeddingCheck, error)
}

// knowledgeRequiredRole lets observers read the environment and keeps every
// download, start, check and deletion to operators.
func knowledgeRequiredRole(request *http.Request) Role {
	if isSafeMethod(request.Method) {
		return RoleObserver
	}
	return RoleOperator
}

func (handler *Handler) knowledgeResource(writer http.ResponseWriter, request *http.Request) {
	environment := handler.knowledge
	if environment == nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "the knowledge base is unavailable")
		return
	}
	rest := strings.Trim(strings.TrimPrefix(request.URL.Path, KnowledgePath), "/")
	switch {
	case rest == "environment":
		if allowMethods(writer, request, http.MethodGet) {
			writeJSON(writer, http.StatusOK, environment.Status())
		}
	case rest == "runtime" || strings.HasPrefix(rest, "runtime/"):
		handler.knowledgeRuntime(writer, request, environment, strings.TrimPrefix(strings.TrimPrefix(rest, "runtime"), "/"))
	case strings.HasPrefix(rest, "embedding-models/"):
		id, action, _ := strings.Cut(strings.TrimPrefix(rest, "embedding-models/"), "/")
		handler.knowledgeModel(writer, request, environment, id, action)
	default:
		writeError(writer, http.StatusNotFound, "not_found", "knowledge endpoint not found")
	}
}

func (handler *Handler) knowledgeRuntime(writer http.ResponseWriter, request *http.Request, environment KnowledgeEnvironment, action string) {
	ctx := request.Context()
	var err error
	switch action {
	case "":
		if !allowMethods(writer, request, http.MethodDelete) {
			return
		}
		err = environment.RemoveRuntime()
	case "install":
		if !allowMethods(writer, request, http.MethodPost) {
			return
		}
		err = environment.InstallRuntime()
	case "import":
		if !allowMethods(writer, request, http.MethodPost) {
			return
		}
		path, ok := knowledgeImportPath(writer, request)
		if !ok {
			return
		}
		err = environment.ImportRuntime(ctx, path)
	case "start":
		if !allowMethods(writer, request, http.MethodPost) {
			return
		}
		err = environment.StartServer()
	case "stop":
		if !allowMethods(writer, request, http.MethodPost) {
			return
		}
		err = environment.StopServer()
	case "data":
		if !allowMethods(writer, request, http.MethodDelete) {
			return
		}
		err = environment.ResetData()
	case "check":
		if !allowMethods(writer, request, http.MethodPost) {
			return
		}
		ctx, cancel := context.WithTimeout(ctx, knowledgeCheckBudget)
		defer cancel()
		check, err := environment.CheckRuntime(ctx)
		if err != nil {
			writeKnowledgeError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, check)
		return
	default:
		writeError(writer, http.StatusNotFound, "not_found", "knowledge endpoint not found")
		return
	}
	if err != nil {
		writeKnowledgeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, environment.Status())
}

func (handler *Handler) knowledgeModel(writer http.ResponseWriter, request *http.Request, environment KnowledgeEnvironment, id, action string) {
	ctx := request.Context()
	var err error
	switch action {
	case "":
		if !allowMethods(writer, request, http.MethodDelete) {
			return
		}
		err = environment.RemoveModel(id)
	case "install":
		if !allowMethods(writer, request, http.MethodPost) {
			return
		}
		err = environment.InstallModel(id)
	case "import":
		if !allowMethods(writer, request, http.MethodPost) {
			return
		}
		path, ok := knowledgeImportPath(writer, request)
		if !ok {
			return
		}
		err = environment.ImportModel(ctx, id, path)
	case "check":
		if !allowMethods(writer, request, http.MethodPost) {
			return
		}
		ctx, cancel := context.WithTimeout(ctx, knowledgeCheckBudget)
		defer cancel()
		check, err := environment.CheckModel(ctx, id)
		if err != nil {
			writeKnowledgeError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, check)
		return
	default:
		writeError(writer, http.StatusNotFound, "not_found", "knowledge endpoint not found")
		return
	}
	if err != nil {
		writeKnowledgeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, environment.Status())
}

func knowledgeImportPath(writer http.ResponseWriter, request *http.Request) (string, bool) {
	var body contract.KnowledgeImportRequest
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || strings.TrimSpace(body.Path) == "" {
		writeError(writer, http.StatusBadRequest, "invalid_request", "a local path is required")
		return "", false
	}
	if !filepath.IsAbs(body.Path) {
		writeError(writer, http.StatusBadRequest, "invalid_request", "the path must be absolute")
		return "", false
	}
	return body.Path, true
}

func writeKnowledgeError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, knowledge.ErrUnsupported):
		writeError(writer, http.StatusConflict, "knowledge_unsupported", err.Error())
	case errors.Is(err, knowledge.ErrBusy):
		writeError(writer, http.StatusConflict, "knowledge_busy", err.Error())
	case errors.Is(err, knowledge.ErrNotInstalled), errors.Is(err, knowledge.ErrModelNotReady):
		writeError(writer, http.StatusConflict, "knowledge_not_installed", err.Error())
	case errors.Is(err, knowledge.ErrNotRunning):
		writeError(writer, http.StatusConflict, "knowledge_not_running", err.Error())
	case errors.Is(err, knowledge.ErrUnknownModel):
		writeError(writer, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		writeError(writer, http.StatusGatewayTimeout, "knowledge_timeout", err.Error())
	default:
		writeError(writer, http.StatusInternalServerError, "knowledge_failed", err.Error())
	}
}
