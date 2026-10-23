package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/knowledge"
)

type fakeKnowledge struct {
	calls    []string
	imported string
	err      error
}

func (fake *fakeKnowledge) record(call string) error {
	fake.calls = append(fake.calls, call)
	return fake.err
}

func (fake *fakeKnowledge) Status() contract.KnowledgeEnvironment {
	return contract.KnowledgeEnvironment{Runtime: contract.KnowledgeRuntimeStatus{State: contract.KnowledgeRuntimeInstalled, Version: "1.4.0.0-r1"}}
}
func (fake *fakeKnowledge) InstallRuntime() error { return fake.record("install") }
func (fake *fakeKnowledge) ImportRuntime(_ context.Context, path string) error {
	fake.imported = path
	return fake.record("import")
}
func (fake *fakeKnowledge) RemoveRuntime() error { return fake.record("remove") }
func (fake *fakeKnowledge) StartServer() error   { return fake.record("start") }
func (fake *fakeKnowledge) StopServer() error    { return fake.record("stop") }
func (fake *fakeKnowledge) ResetData() error     { return fake.record("reset") }
func (fake *fakeKnowledge) CheckRuntime(context.Context) (contract.KnowledgeRuntimeCheck, error) {
	return contract.KnowledgeRuntimeCheck{OK: true, Steps: []contract.KnowledgeCheckStep{{Name: "connect", OK: true}}}, fake.record("check")
}
func (fake *fakeKnowledge) InstallModel(id string) error { return fake.record("install-model " + id) }
func (fake *fakeKnowledge) ImportModel(_ context.Context, id, path string) error {
	fake.imported = path
	return fake.record("import-model " + id)
}
func (fake *fakeKnowledge) RemoveModel(id string) error { return fake.record("remove-model " + id) }
func (fake *fakeKnowledge) CheckModel(_ context.Context, id string) (contract.KnowledgeEmbeddingCheck, error) {
	return contract.KnowledgeEmbeddingCheck{Dimensions: 384}, fake.record("check-model " + id)
}

func newKnowledgeHandler(t *testing.T, environment KnowledgeEnvironment) *Handler {
	t.Helper()
	store, err := openControlTestStore(t, context.Background(), filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("0.1.0-test", "abc1234"), Dependencies{
		ServiceStore: store, ControlToken: testControlToken, ObserverToken: testObserverToken, Knowledge: environment,
	})
	if err != nil {
		t.Fatalf("NewWithDependencies: %v", err)
	}
	return handler
}

func knowledgeRequest(t *testing.T, handler http.Handler, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestKnowledgeRoutesMapToTheEnvironment(t *testing.T) {
	fake := &fakeKnowledge{}
	handler := newKnowledgeHandler(t, fake)
	absolute := filepath.Join(t.TempDir(), "bundle.tar.gz")
	body, _ := json.Marshal(contract.KnowledgeImportRequest{Path: absolute})
	for _, route := range []struct{ method, path, body, call string }{
		{http.MethodPost, KnowledgePath + "/runtime/install", "", "install"},
		{http.MethodPost, KnowledgePath + "/runtime/import", string(body), "import"},
		{http.MethodPost, KnowledgePath + "/runtime/start", "", "start"},
		{http.MethodPost, KnowledgePath + "/runtime/stop", "", "stop"},
		{http.MethodPost, KnowledgePath + "/runtime/check", "", "check"},
		{http.MethodDelete, KnowledgePath + "/runtime/data", "", "reset"},
		{http.MethodDelete, KnowledgePath + "/runtime", "", "remove"},
		{http.MethodPost, KnowledgePath + "/embedding-models/granite-embedding-107m-multilingual/install", "", "install-model granite-embedding-107m-multilingual"},
		{http.MethodPost, KnowledgePath + "/embedding-models/granite-embedding-107m-multilingual/check", "", "check-model granite-embedding-107m-multilingual"},
		{http.MethodDelete, KnowledgePath + "/embedding-models/granite-embedding-107m-multilingual", "", "remove-model granite-embedding-107m-multilingual"},
	} {
		fake.calls = nil
		response := knowledgeRequest(t, handler, testControlToken, route.method, route.path, route.body)
		if response.Code != http.StatusOK || len(fake.calls) != 1 || fake.calls[0] != route.call {
			t.Fatalf("%s %s: status=%d calls=%v body=%s", route.method, route.path, response.Code, fake.calls, response.Body.String())
		}
	}
	if fake.imported != absolute {
		t.Fatalf("imported %q", fake.imported)
	}
}

func TestKnowledgeObserversOnlyRead(t *testing.T) {
	fake := &fakeKnowledge{}
	handler := newKnowledgeHandler(t, fake)
	response := knowledgeRequest(t, handler, testObserverToken, http.MethodGet, KnowledgePath+"/environment", "")
	if response.Code != http.StatusOK {
		t.Fatalf("observer read: %d %s", response.Code, response.Body.String())
	}
	var status contract.KnowledgeEnvironment
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil || status.Runtime.State != contract.KnowledgeRuntimeInstalled {
		t.Fatalf("environment = %+v, %v", status, err)
	}
	response = knowledgeRequest(t, handler, testObserverToken, http.MethodPost, KnowledgePath+"/runtime/start", "")
	if response.Code != http.StatusForbidden || len(fake.calls) != 0 {
		t.Fatalf("observer start: status=%d calls=%v", response.Code, fake.calls)
	}
}

func TestKnowledgeRejectsRelativeImportPaths(t *testing.T) {
	fake := &fakeKnowledge{}
	handler := newKnowledgeHandler(t, fake)
	for _, body := range []string{`{"path":"bundle.tar.gz"}`, `{"path":""}`, `{"path":"/x","extra":1}`, `not json`} {
		response := knowledgeRequest(t, handler, testControlToken, http.MethodPost, KnowledgePath+"/runtime/import", body)
		if response.Code != http.StatusBadRequest || len(fake.calls) != 0 {
			t.Fatalf("%s: status=%d calls=%v", body, response.Code, fake.calls)
		}
	}
}

func TestKnowledgeErrorsAreTyped(t *testing.T) {
	for _, entry := range []struct {
		err    error
		status int
		code   string
	}{
		{knowledge.ErrBusy, http.StatusConflict, "knowledge_busy"},
		{knowledge.ErrUnsupported, http.StatusConflict, "knowledge_unsupported"},
		{knowledge.ErrNotInstalled, http.StatusConflict, "knowledge_not_installed"},
		{knowledge.ErrNotRunning, http.StatusConflict, "knowledge_not_running"},
		{knowledge.ErrUnknownModel, http.StatusNotFound, "not_found"},
	} {
		handler := newKnowledgeHandler(t, &fakeKnowledge{err: entry.err})
		response := knowledgeRequest(t, handler, testControlToken, http.MethodPost, KnowledgePath+"/runtime/start", "")
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &body)
		if response.Code != entry.status || body.Error.Code != entry.code {
			t.Fatalf("%v: status=%d body=%s", entry.err, response.Code, response.Body.String())
		}
	}
}

func TestKnowledgeIsUnavailableWithoutAnEnvironment(t *testing.T) {
	handler := newKnowledgeHandler(t, nil)
	response := knowledgeRequest(t, handler, testControlToken, http.MethodGet, KnowledgePath+"/environment", "")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
}
