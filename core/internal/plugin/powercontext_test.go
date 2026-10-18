package plugin

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/plugins"
)

// fakePowerContext answers like PowerContext 1.3.0 and keeps what it was sent.
// With slowSearch set, every search times out.
type fakePowerContext struct {
	mutex      sync.Mutex
	seen       []HTTPRequest
	slowSearch bool
	// version is what server-info reports, 1.3.0 when empty.
	version string
}

func (server *fakePowerContext) Fetch(_ context.Context, _ *Plugin, request HTTPRequest) HTTPResponse {
	server.mutex.Lock()
	server.seen = append(server.seen, request)
	slow := server.slowSearch
	version := server.version
	server.mutex.Unlock()
	if version == "" {
		version = "1.3.0"
	}
	if slow && strings.HasSuffix(request.URL, "/search") {
		return HTTPResponse{Error: FetchTimeout, Took: time.Duration(request.TimeoutMS) * time.Millisecond}
	}
	reply := func(status int, body string) HTTPResponse {
		return HTTPResponse{Status: status, Header: map[string]string{"content-type": "application/json"}, Body: []byte(body), Took: 3 * time.Millisecond}
	}
	path := strings.TrimPrefix(request.URL, "https://memory.test")
	switch {
	case path == "/v1/server-info":
		return reply(200, `{"package_version":"`+version+`"}`)
	case path == "/v1/capabilities":
		return reply(200, `{"artifact_families":["memory","atomic-memory","topic-memory","experience","skill","profile"]}`)
	case path == "/v1/scopes":
		return reply(201, `{"scope_id":"scp_test","title":"demo","summary":"","context_references":[],"external_references":[],"version":1}`)
	case path == "/v1/scopes/scp_test/artifacts/profile?limit=1":
		return reply(200, `{"items":[{"title":"约定","summary":"测试用表驱动写法"}],"next_cursor":""}`)
	case path == "/v1/scopes/scp_test/artifacts/atomic-memory/search":
		return reply(200, `{"results":[{"family":"atomic-memory","artifact_id":"a1","revision":2,"content":{"text":"分页用游标"},"lineage":{}}]}`)
	case path == "/v1/scopes/scp_test/artifacts/topic-memory/search":
		return reply(200, `{"results":[
			{"family":"topic-memory","artifact_id":"t1","revision":1,"content":{"title":"插件","summary":"宿主用 moejs"},"lineage":{},"scores":{"retrieval":0.8,"channels":{}}},
			{"family":"topic-memory","artifact_id":"t2","revision":1,"content":{"title":"无关","summary":"低分"},"lineage":{},"scores":{"retrieval":0.1,"channels":{}}}]}`)
	case path == "/v1/scopes/scp_test/artifacts/skill/search":
		return reply(200, `{"results":[]}`)
	case path == "/v1/sources/content":
		return reply(202, `{"status":"accepted","source":{"name":"content","source_id":"x"},"position":1}`)
	}
	return reply(404, `{}`)
}

func (server *fakePowerContext) requests(path string) []HTTPRequest {
	server.mutex.Lock()
	defer server.mutex.Unlock()
	var matched []HTTPRequest
	for _, request := range server.seen {
		if strings.HasSuffix(request.URL, path) {
			matched = append(matched, request)
		}
	}
	return matched
}

func powerContextView(project string, typed string) map[string]any {
	view := map[string]any{
		"protocol": "anthropic.messages", "model": "claude-test", "client": "claude_code", "stream": true,
		"meta":    map[string]any{},
		"session": map[string]any{"id": "session_1", "turn": 3},
		"user":    map[string]any{"text": typed, "typed": typed, "previous": "上一轮问了分页"},
	}
	if project != "" {
		view["meta"] = map[string]any{"project": map[string]any{"value": project, "source": "session"}}
	}
	return view
}

// TestThePowerContextPlugin runs the builtin plugin's hooks against a fake
// PowerContext 1.3.
func TestThePowerContextPlugin(t *testing.T) {
	server := &fakePowerContext{}
	all := []string{CapabilityInject, CapabilityRequest, CapabilityNetwork, CapabilityStorage, CapabilityViews}
	manager := NewManager(ManagerOptions{
		Builtin: plugins.Builtin,
		Settings: settingsMap{"powercontext": {
			Enabled: true, Capabilities: all,
			Config: json.RawMessage(`{"serverUrl":"https://memory.test","minScore":{"topic-memory":0.5}}`),
		}},
		Services: Services{Fetcher: server, Store: &memoryStore{}},
	})
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	plugin := loadedPlugin(t, manager, "powercontext")
	if attributes := plugin.Attributes(); len(attributes) != 1 || attributes[0].Key != "project" || attributes[0].Marker != "memory-project" {
		t.Fatalf("attributes = %+v", attributes)
	}
	call := func(hook string, arg any) string {
		t.Helper()
		result, _, err := plugin.Call(context.Background(), hook, arg, CallOptions{NetworkBudget: 5 * time.Second})
		if err != nil {
			t.Fatalf("%s: %v", hook, err)
		}
		return string(result)
	}

	// Without a project the session asks for one, in words that never name
	// the gateway.
	ask := call(HookPrepareSession, powerContextView("", ""))
	if !strings.Contains(ask, "<memory-project>项目名</memory-project>") || strings.Contains(strings.ToLower(ask), "astrlink") {
		t.Fatalf("prepareSession without a project = %s", ask)
	}
	if again := call(HookPrepareSession, powerContextView("", "")); again != ask {
		t.Fatalf("the ask changed between turns:\n%s\n%s", ask, again)
	}

	resident := call(HookPrepareSession, powerContextView("demo", ""))
	if !strings.Contains(resident, "<project-context>") || !strings.Contains(resident, "- 约定：测试用表驱动写法") {
		t.Fatalf("prepareSession with a project = %s", resident)
	}
	if scopes := server.requests("/v1/scopes"); len(scopes) != 1 || !strings.Contains(string(scopes[0].JSON), `"idempotency_key":"astrlink:project:demo"`) {
		t.Fatalf("scope requests = %+v", scopes)
	}

	var turn struct {
		Context struct {
			Open  string   `json:"open"`
			Items []string `json:"items"`
		} `json:"context"`
		Skip   string `json:"skip"`
		Record struct {
			Summary string `json:"summary"`
		} `json:"record"`
	}
	if err := json.Unmarshal([]byte(call(HookPrepareTurn, powerContextView("demo", "怎么给网关加一个插件系统？"))), &turn); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"- [atomic-memory] 分页用游标 (ref atomic-memory/a1#2)",
		"- [topic-memory] 插件：宿主用 moejs (ref topic-memory/t1#1)",
	}
	if turn.Context.Open != "<project-memory>" || strings.Join(turn.Context.Items, "\n") != strings.Join(want, "\n") || turn.Record.Summary != "召回 2/9" {
		t.Fatalf("prepareTurn = %+v", turn)
	}
	// atomic-memory refuses scores and admission; the others ask for scores.
	atomic := server.requests("/atomic-memory/search")
	var atomicBody map[string]any
	if len(atomic) != 1 || json.Unmarshal(atomic[0].JSON, &atomicBody) != nil || len(atomicBody) != 2 || atomicBody["query"] != "怎么给网关加一个插件系统？" || atomicBody["limit"] != float64(4) {
		t.Fatalf("atomic-memory search = %+v", atomic)
	}
	if topic := server.requests("/topic-memory/search"); len(topic) != 1 || !strings.Contains(string(topic[0].JSON), `"include_scores":true`) {
		t.Fatalf("topic-memory search = %+v", topic)
	}

	// Searches that all time out are the server's failure, not a miss, and
	// get most of the request's wait.
	server.mutex.Lock()
	server.slowSearch = true
	server.mutex.Unlock()
	var failed struct {
		Skip   string `json:"skip"`
		Record struct {
			Status  string `json:"status"`
			Summary string `json:"summary"`
		} `json:"record"`
	}
	if err := json.Unmarshal([]byte(call(HookPrepareTurn, powerContextView("demo", "怎么给网关加一个插件系统？"))), &failed); err != nil ||
		failed.Skip != "server_unavailable" || failed.Record.Status != "failed" || failed.Record.Summary != "召回 0/9 · 3 个检索失败" {
		t.Fatalf("prepareTurn with every search timing out = %+v", failed)
	}
	for _, search := range server.requests("/search") {
		if search.TimeoutMS != 1400 {
			t.Fatalf("a search waits %d ms", search.TimeoutMS)
		}
	}
	server.mutex.Lock()
	server.slowSearch = false
	server.mutex.Unlock()

	for _, test := range []struct{ project, typed, skip string }{
		{"", "怎么给网关加一个插件系统？", "no_project"},
		{"none", "怎么给网关加一个插件系统？", "no_project"},
		{"demo", "好", ""}, // a short reply searches with the previous input
	} {
		turn.Skip = ""
		if err := json.Unmarshal([]byte(call(HookPrepareTurn, powerContextView(test.project, test.typed))), &turn); err != nil || turn.Skip != test.skip {
			t.Fatalf("prepareTurn(%q, %q) skip = %q, want %q", test.project, test.typed, turn.Skip, test.skip)
		}
	}

	call(HookObserveTurn, powerContextView("demo", "  把分页改成游标  "))
	captures := server.requests("/v1/sources/content")
	if len(captures) != 1 {
		t.Fatalf("captures = %+v", captures)
	}
	var capture struct {
		ScopeID  string         `json:"scope_id"`
		SourceID string         `json:"source_id"`
		Content  string         `json:"content"`
		Metadata map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(captures[0].JSON, &capture); err != nil {
		t.Fatal(err)
	}
	if capture.ScopeID != "scp_test" || capture.Content != "把分页改成游标" || !strings.HasPrefix(capture.SourceID, "astrlink:demo:session_1:3:") || capture.Metadata["origin"] != "astrlink" {
		t.Fatalf("capture = %+v", capture)
	}
	call(HookObserveTurn, powerContextView("demo", "/compact"))
	if len(server.requests("/v1/sources/content")) != 1 {
		t.Fatal("a slash command was captured")
	}

	if test := call(HookTestConnection, nil); !strings.Contains(test, `"status":"ok"`) || !strings.Contains(test, "PowerContext 1.3.0") || !strings.Contains(test, "atomic-memory / topic-memory / experience / skill") {
		t.Fatalf("testConnection = %s", test)
	}
	if status := call(HookDescribeStatus, nil); !strings.Contains(status, `["demo","scp_test"]`) {
		t.Fatalf("describeStatus = %s", status)
	}
	for _, request := range server.requests("") {
		if request.Credential != "token" {
			t.Fatalf("request without the token credential: %+v", request)
		}
	}
}

// TestPowerContextChecksTheVersionBesideTheSearches runs prepareTurn for a
// project whose scope is known while the server's version is not: the check
// goes out with the searches, and an old server still gets nothing.
func TestPowerContextChecksTheVersionBesideTheSearches(t *testing.T) {
	for _, test := range []struct {
		version, skip string
	}{
		{"1.3.0", ""},
		{"1.2.9", "server_unavailable"},
	} {
		server := &fakePowerContext{version: test.version}
		store := &memoryStore{values: map[string]json.RawMessage{"powercontext/scope:demo": json.RawMessage(`"scp_test"`)}}
		all := []string{CapabilityInject, CapabilityRequest, CapabilityNetwork, CapabilityStorage, CapabilityViews}
		manager := NewManager(ManagerOptions{
			Builtin: plugins.Builtin,
			Settings: settingsMap{"powercontext": {
				Enabled: true, Capabilities: all, Config: json.RawMessage(`{"serverUrl":"https://memory.test"}`),
			}},
			Services: Services{Fetcher: server, Store: store},
		})
		if err := manager.Reload(context.Background()); err != nil {
			t.Fatal(err)
		}
		result, _, err := loadedPlugin(t, manager, "powercontext").Call(context.Background(), HookPrepareTurn,
			powerContextView("demo", "怎么给网关加一个插件系统？"), CallOptions{NetworkBudget: 5 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		var turn struct {
			Skip string `json:"skip"`
		}
		if err := json.Unmarshal(result, &turn); err != nil || turn.Skip != test.skip {
			t.Fatalf("version %s: prepareTurn = %s", test.version, result)
		}
		if len(server.requests("/v1/server-info")) != 1 || len(server.requests("/v1/scopes")) != 0 || len(server.requests("/search")) != 3 {
			t.Fatalf("version %s: requests = %+v", test.version, server.requests(""))
		}
	}
}
