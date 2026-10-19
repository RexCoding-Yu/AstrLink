package plugin

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"github.com/QuantumNous/astrlink/core/plugins"
)

// fakePowerContext answers like PowerContext 1.3.0 and keeps what it was sent.
// With slowSearch set, searches that embed the query time out and those by
// words alone answer; with downSearch, every search fails; with emptySearch,
// memory searches find nothing. flushFailures counts the flushes that fail
// before one succeeds.
type fakePowerContext struct {
	mutex         sync.Mutex
	seen          []HTTPRequest
	slowSearch    bool
	downSearch    bool
	emptySearch   bool
	flushFailures int
	slowFlush     bool
	// version is what server-info reports, 1.3.0 when empty.
	version string
	// position is the journal position of the last capture.
	position int
}

func (server *fakePowerContext) Fetch(_ context.Context, _ *Plugin, request HTTPRequest) HTTPResponse {
	server.mutex.Lock()
	server.seen = append(server.seen, request)
	slow, down, empty := server.slowSearch, server.downSearch, server.emptySearch
	version := server.version
	server.mutex.Unlock()
	if version == "" {
		version = "1.3.0"
	}
	reply := func(status int, body string) HTTPResponse {
		return HTTPResponse{Status: status, Header: map[string]string{"content-type": "application/json"}, Body: []byte(body), Took: 3 * time.Millisecond}
	}
	if strings.HasSuffix(request.URL, "/search") {
		var search struct {
			Query string `json:"query"`
			Mode  string `json:"mode"`
		}
		_ = json.Unmarshal(request.JSON, &search)
		switch {
		case down:
			return reply(503, `{"error":{"code":"unavailable","message":"The Server is unavailable."}}`)
		case slow && search.Mode != "text":
			return HTTPResponse{Error: FetchBudget, Took: 2400 * time.Millisecond}
		case strings.Contains(request.URL, "/topic-memory/") && termsOf(search.Query) > 64:
			return reply(400, `{"error":{"code":"invalid_request","message":"The request violates the API contract.","details":{"errors":[{"msg":"Value error, text search accepts at most 64 analyzed terms"}]}}}`)
		case empty && !strings.Contains(request.URL, "/skill/"):
			return reply(200, `{"results":[]}`)
		}
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
		server.mutex.Lock()
		server.position++
		position := server.position
		server.mutex.Unlock()
		return reply(202, `{"status":"accepted","source":{"name":"content","source_id":"x"},"position":`+strconv.Itoa(position)+`}`)
	case path == "/v1/memory/flush":
		server.mutex.Lock()
		defer server.mutex.Unlock()
		if server.slowFlush {
			return HTTPResponse{Error: FetchTimeout, Took: time.Duration(request.TimeoutMS) * time.Millisecond}
		}
		if server.flushFailures > 0 {
			server.flushFailures--
			return reply(500, `{"error":{"code":"internal_error","message":"The Server failed.","details":null}}`)
		}
		return reply(200, `{"status":"processed","current_cursor":`+strconv.Itoa(server.position)+`,"remaining_work":false}`)
	}
	return reply(404, `{}`)
}

// termsOf counts the terms PowerContext 1.3's text search finds: a run of
// n Han characters gives n characters and n - 1 pairs, a run of other letters
// and digits one word.
func termsOf(query string) int {
	terms, run := 0, ""
	for _, char := range query {
		switch {
		case unicode.Is(unicode.Han, char):
			terms++
			if run == "han" {
				terms++
			}
			run = "han"
		case unicode.IsLetter(char) || unicode.IsDigit(char):
			if run != "word" {
				terms++
			}
			run = "word"
		default:
			run = ""
		}
	}
	return terms
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
	// Beside each search that embeds the query goes one by words alone.
	// The two go out together, in no set order.
	byMode := func(family string) (full, words map[string]any) {
		t.Helper()
		searches := server.requests("/" + family + "/search")
		if len(searches) != 2 {
			t.Fatalf("%s searches = %+v", family, searches)
		}
		for _, search := range searches {
			var body map[string]any
			if err := json.Unmarshal(search.JSON, &body); err != nil {
				t.Fatal(err)
			}
			if body["mode"] == "text" {
				words = body
			} else {
				full = body
			}
		}
		if full == nil || words == nil {
			t.Fatalf("%s searches = %+v", family, searches)
		}
		return full, words
	}
	atomicBody, atomicText := byMode("atomic-memory")
	if len(atomicBody) != 2 || atomicBody["query"] != "怎么给网关加一个插件系统？" || atomicBody["limit"] != float64(4) || len(atomicText) != 3 {
		t.Fatalf("atomic-memory searches = %v, %v", atomicBody, atomicText)
	}
	if topicBody, topicText := byMode("topic-memory"); topicBody["include_scores"] != true || topicText["include_scores"] != true || topicText["admission"] != nil {
		t.Fatalf("topic-memory searches = %v, %v", topicBody, topicText)
	}
	if len(server.requests("/skill/search")) != 1 {
		t.Fatalf("skill search = %+v", server.requests("/skill/search"))
	}
	for _, search := range server.requests("/search") {
		if search.TimeoutMS != 5000 {
			t.Fatalf("a search waits %d ms", search.TimeoutMS)
		}
	}

	type turnResult struct {
		Skip   string `json:"skip"`
		Record struct {
			Status  string `json:"status"`
			Summary string `json:"summary"`
			View    []struct {
				Rows [][]any `json:"rows"`
			} `json:"view"`
		} `json:"record"`
	}
	prepare := func(test, typed string) turnResult {
		t.Helper()
		var result turnResult
		if err := json.Unmarshal([]byte(call(HookPrepareTurn, powerContextView("demo", typed))), &result); err != nil {
			t.Fatalf("%s: %v", test, err)
		}
		return result
	}
	set := func(change func(*fakePowerContext)) {
		server.mutex.Lock()
		change(server)
		server.mutex.Unlock()
	}

	// When the embedding is late, the searches by words alone answer.
	set(func(server *fakePowerContext) { server.slowSearch = true })
	if late := prepare("late", "怎么给网关加一个插件系统？"); late.Skip != "" || late.Record.Status != "ok" ||
		late.Record.Summary != "召回 2/9 · 1 个检索失败 · 2 个只用关键词" || late.Record.View[1].Rows[0][0] != "原子记忆" ||
		late.Record.View[1].Rows[0][5] != "完整检索超出等待时间，用了关键词检索的结果" {
		t.Fatalf("prepareTurn with a late embedding = %+v", late)
	}
	// Nothing found while a search failed is not a plain miss.
	set(func(server *fakePowerContext) { server.emptySearch = true })
	if missed := prepare("missed", "怎么给网关加一个插件系统？"); missed.Skip != "search_failed" || missed.Record.Status != "skipped" {
		t.Fatalf("prepareTurn with a failed search and no hits = %+v", missed)
	}
	// Searches that all fail are the server's failure, not a miss.
	set(func(server *fakePowerContext) {
		server.slowSearch, server.emptySearch, server.downSearch = false, false, true
	})
	if failed := prepare("down", "怎么给网关加一个插件系统？"); failed.Skip != "server_unavailable" || failed.Record.Status != "failed" ||
		failed.Record.Summary != "召回 0/9 · 3 个检索失败" || failed.Record.View[1].Rows[2][5] != "HTTP 503：The Server is unavailable." {
		t.Fatalf("prepareTurn with every search failing = %+v", failed)
	}
	set(func(server *fakePowerContext) { server.downSearch = false })

	// A long question keeps to the terms PowerContext's text search takes:
	// its start, and its end, where it says what it asks.
	long := strings.Repeat("这个回复你是从上下文获取的还是插件塞进来的", 6) + "另外帮我回忆一下 powercontext 的代码注释约定"
	if bounded := prepare("long", long); bounded.Record.Status != "ok" {
		t.Fatalf("prepareTurn with a long question = %+v", bounded)
	}
	topic := server.requests("/topic-memory/search")
	var sent struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(topic[len(topic)-1].JSON, &sent); err != nil {
		t.Fatal(err)
	}
	start, end, cut := strings.Cut(sent.Query, "\n")
	if !cut || termsOf(sent.Query) > 60 || !strings.HasPrefix(long, start) || !strings.HasSuffix(long, end) || !strings.HasSuffix(end, "powercontext 的代码注释约定") {
		t.Fatalf("long query = %q (%d terms)", sent.Query, termsOf(sent.Query))
	}

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

	type writeResult struct {
		Record struct {
			Title   string `json:"title"`
			Status  string `json:"status"`
			Summary string `json:"summary"`
		} `json:"record"`
	}
	observe := func(typed string) writeResult {
		t.Helper()
		var result writeResult
		if err := json.Unmarshal([]byte(call(HookObserveTurn, powerContextView("demo", typed))), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if written := observe("  把分页改成游标  "); written.Record.Title != "记忆写入" || written.Record.Status != "ok" || written.Record.Summary != "已写入 · 已整理成记忆" {
		t.Fatalf("observeTurn = %+v", written)
	}
	if flushes := server.requests("/v1/memory/flush"); len(flushes) != 1 || string(flushes[0].JSON) != `{"scope_id":"scp_test"}` {
		t.Fatalf("flushes = %+v", flushes)
	}
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
	if result := call(HookObserveTurn, powerContextView("demo", "/compact")); result != "null" && result != "" {
		t.Fatalf("observeTurn of a slash command = %s", result)
	}
	if len(server.requests("/v1/sources/content")) != 1 {
		t.Fatal("a slash command was captured")
	}
	// Processing that fails leaves the capture waiting; the next capture of
	// the project processes both.
	set(func(server *fakePowerContext) { server.flushFailures = 3 })
	if written := observe("再补一个测试"); written.Record.Status != "failed" || written.Record.Summary != "已写入 · 整理失败：HTTP 500：The Server failed." {
		t.Fatalf("observeTurn with processing failing = %+v", written)
	}
	if status := call(HookDescribeStatus, nil); !strings.Contains(status, "1 个项目有记下的原文还没整理成记忆") || !strings.Contains(status, "demo：已写入 · 整理失败") {
		t.Fatalf("describeStatus with a capture waiting = %s", status)
	}
	if written := observe("补好了，再看看"); written.Record.Status != "ok" || written.Record.Summary != "已写入 · 已整理成记忆" {
		t.Fatalf("observeTurn after processing failed = %+v", written)
	}
	// Processing that runs long is not a failure; each call waits 15 s at
	// most, so the next one gets its turn.
	set(func(server *fakePowerContext) { server.slowFlush = true })
	if written := observe("看一下覆盖率"); written.Record.Status != "skipped" || written.Record.Summary != "已写入 · 整理超时，下一轮接着整理" {
		t.Fatalf("observeTurn with processing running long = %+v", written)
	}
	for _, flush := range server.requests("/v1/memory/flush") {
		if flush.TimeoutMS > 15000 {
			t.Fatalf("a flush waits %d ms", flush.TimeoutMS)
		}
	}
	set(func(server *fakePowerContext) { server.slowFlush = false })
	if written := observe("覆盖率够了"); written.Record.Status != "ok" {
		t.Fatalf("observeTurn after processing ran long = %+v", written)
	}

	if test := call(HookTestConnection, nil); !strings.Contains(test, `"status":"ok"`) || !strings.Contains(test, "PowerContext 1.3.0") || !strings.Contains(test, "原子记忆 / 主题记忆 / 经验 / 技能") {
		t.Fatalf("testConnection = %s", test)
	}
	status := call(HookDescribeStatus, nil)
	if !strings.Contains(status, `["demo"]`) || strings.Contains(status, "scp_test") || strings.Contains(status, "待整理") || !strings.Contains(status, "最近写入") {
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
		if len(server.requests("/v1/server-info")) != 1 || len(server.requests("/v1/scopes")) != 0 || len(server.requests("/search")) != 5 {
			t.Fatalf("version %s: requests = %+v", test.version, server.requests(""))
		}
	}
}
