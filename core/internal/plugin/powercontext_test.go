package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/plugins"
)

// fakePowerContext answers like PowerContext 1.3.0 and keeps what it was
// sent. Its scopes are listed by id, as the server does; topicPages splits the
// topics into pages of that many.
type fakePowerContext struct {
	mutex sync.Mutex
	seen  []HTTPRequest
	// version is what server-info reports, 1.3.0 when empty.
	version string
	// scopes are the projects on the server by title; policies the scopes
	// with a profile policy.
	scopes   map[string]string
	policies map[string]bool
	profile  string
	topics   []string
	// processing is what capabilities say of automatic processing: on, off
	// or absent.
	processing string
	topicPages int
	down       bool
}

func newFakePowerContext() *fakePowerContext {
	server := &fakePowerContext{
		scopes:     map[string]string{"datav": "scp_datav", "paseo": "scp_paseo", "Has Space": "scp_bad"},
		policies:   map[string]bool{"scp_paseo": true},
		profile:    `{"title":"沟通约定","summary":"用中文沟通，注释写中文。\n测试用表驱动写法。"}`,
		processing: "on",
	}
	for index := range 30 {
		server.topics = append(server.topics, fmt.Sprintf(`{"title":"主题 %02d","summary":"%s","published_at":"2026-10-%02dT08:00:00Z"}`,
			index, strings.Repeat("摘", 90+index), 1+index%28))
	}
	return server
}

func (server *fakePowerContext) Fetch(_ context.Context, _ *Plugin, request HTTPRequest) HTTPResponse {
	server.mutex.Lock()
	defer server.mutex.Unlock()
	server.seen = append(server.seen, request)
	reply := func(status int, body string) HTTPResponse {
		return HTTPResponse{Status: status, Header: map[string]string{"content-type": "application/json"}, Body: []byte(body), Took: 3 * time.Millisecond}
	}
	if server.down {
		return HTTPResponse{Error: FetchNetwork, Took: time.Millisecond}
	}
	target, _ := url.Parse(request.URL)
	query := target.Query()
	switch {
	case request.Method == "GET" && target.Path == "/v1/server-info":
		version := server.version
		if version == "" {
			version = "1.3.0"
		}
		return reply(200, `{"package_version":"`+version+`"}`)
	case request.Method == "GET" && target.Path == "/v1/capabilities":
		background := `"automatic_processing_enabled":true`
		if server.processing == "off" {
			background = `"automatic_processing_enabled":false`
		}
		extraction := `{"configuration":"configured","background":{"location":"local","state":"running",` + background + `},
			"observation":{"status":"observed","since":"2026-10-21T00:00:00Z","last_success_at":"2026-10-21T03:04:05Z",
			"last_failure":{"code":"model_timeout","stage":"inference","occurred_at":"2026-10-20T15:00:00Z"}}}`
		if server.processing == "" {
			extraction = "null"
		}
		return reply(200, `{"artifact_families":["memory","atomic-memory","topic-memory","experience","skill","profile"],"memory_extraction":true,"extraction":`+extraction+`}`)
	case request.Method == "GET" && target.Path == "/v1/scopes":
		var items []string
		for title, id := range server.scopes {
			if query.Get("query") != "" && !strings.Contains(title, query.Get("query")) {
				continue
			}
			items = append(items, `{"scope_id":"`+id+`","title":"`+title+`","summary":"s","context_references":[],"external_references":[],"version":1}`)
		}
		return reply(200, `{"items":[`+strings.Join(items, ",")+`],"next_cursor":null}`)
	case request.Method == "POST" && target.Path == "/v1/scopes":
		var created struct {
			Title string `json:"title"`
		}
		_ = json.Unmarshal(request.JSON, &created)
		server.scopes[created.Title] = "scp_" + created.Title
		return reply(201, `{"scope_id":"scp_`+created.Title+`","title":"`+created.Title+`","summary":"s","context_references":[],"external_references":[],"version":1}`)
	case strings.HasSuffix(target.Path, "/profile-policy"):
		scope := strings.TrimSuffix(strings.TrimPrefix(target.Path, "/v1/scopes/"), "/profile-policy")
		if request.Method == "PUT" {
			server.policies[scope] = true
			return reply(200, `{"scope_id":"`+scope+`","generation_enabled":true,"activation_mode":"automatic","pending_candidate_id":null,"version":1,"updated_at":"2026-10-21T00:00:00Z"}`)
		}
		if !server.policies[scope] {
			return reply(404, `{"error":{"code":"not_found","message":"Not found."}}`)
		}
		return reply(200, `{"scope_id":"`+scope+`","generation_enabled":false,"activation_mode":"automatic","pending_candidate_id":null,"version":3,"updated_at":"2026-10-21T00:00:00Z"}`)
	case strings.HasSuffix(target.Path, "/artifacts/profile"):
		return reply(200, `{"items":[`+server.profile+`],"next_cursor":null}`)
	case strings.HasSuffix(target.Path, "/artifacts/topic-memory"):
		topics := server.topics
		next := "null"
		if server.topicPages > 0 {
			start := 0
			if cursor := query.Get("cursor"); cursor != "" {
				fmt.Sscanf(cursor, "page-%d", &start)
			}
			end := min(start+server.topicPages, len(topics))
			if end < len(topics) {
				next = fmt.Sprintf(`"page-%d"`, end)
			}
			topics = topics[start:end]
		}
		return reply(200, `{"items":[`+strings.Join(topics, ",")+`],"next_cursor":`+next+`}`)
	case request.Method == "POST" && target.Path == "/v1/sources/content":
		return reply(202, `{"status":"accepted","source":{"name":"content","source_id":"x"},"position":7}`)
	}
	return reply(404, `{"error":{"code":"not_found","message":"Not found."}}`)
}

func (server *fakePowerContext) requests(method, path string) []HTTPRequest {
	server.mutex.Lock()
	defer server.mutex.Unlock()
	var matched []HTTPRequest
	for _, request := range server.seen {
		target, _ := url.Parse(request.URL)
		if request.Method == method && strings.HasSuffix(target.Path, path) {
			matched = append(matched, request)
		}
	}
	return matched
}

// sessionsUsing answers astrlink.meta.values: the projects this computer's
// sessions used.
type sessionsUsing []ValueStat

func (stats sessionsUsing) ValueStats(context.Context, string, int) ([]ValueStat, error) {
	return stats, nil
}

type powerContextHarness struct {
	t      *testing.T
	server *fakePowerContext
	plugin *Plugin
	store  *memoryStore
}

func newPowerContextHarness(t *testing.T, config string, used sessionsUsing) *powerContextHarness {
	t.Helper()
	server := newFakePowerContext()
	store := &memoryStore{}
	all := []string{CapabilityInject, CapabilityRequest, CapabilityNetwork, CapabilityStorage, CapabilityViews}
	manager := NewManager(ManagerOptions{
		Builtin: plugins.Builtin,
		Settings: settingsMap{"powercontext": {
			Enabled: true, Capabilities: all, Config: json.RawMessage(config),
		}},
		Services: Services{
			Fetcher: server, Store: store, Metadata: used,
			Now:  func() time.Time { return time.Date(2026, 10, 21, 22, 30, 0, 0, time.UTC) },
			Zone: time.FixedZone("EDT", -4*3600),
		},
	})
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &powerContextHarness{t: t, server: server, plugin: loadedPlugin(t, manager, "powercontext"), store: store}
}

func (h *powerContextHarness) call(hook string, arg any) string {
	h.t.Helper()
	result, _, err := h.plugin.Call(context.Background(), hook, arg, CallOptions{NetworkBudget: 5 * time.Second})
	if err != nil {
		h.t.Fatalf("%s: %v", hook, err)
	}
	return string(result)
}

func powerContextView(project, language string, extra map[string]any) map[string]any {
	view := map[string]any{
		"protocol": "anthropic.messages", "model": "claude-test", "client": "claude_code", "stream": true,
		"meta": map[string]any{}, "language": language,
		"session": map[string]any{"id": "session_1", "turn": 1, "first": true},
		"user":    map[string]any{"text": "帮我把分页改成游标", "typed": "帮我把分页改成游标"},
	}
	if project != "" {
		view["meta"] = map[string]any{"project": map[string]any{"value": project, "source": "session"}}
	}
	for key, value := range extra {
		view[key] = value
	}
	return view
}

type sessionResult struct {
	System *struct {
		Open  string   `json:"open"`
		Items []string `json:"items"`
		Close string   `json:"close"`
	} `json:"system"`
	Record struct {
		Title   string `json:"title"`
		Status  string `json:"status"`
		Summary string `json:"summary"`
		Actions []struct {
			ID      string `json:"id"`
			Label   string `json:"label"`
			Confirm string `json:"confirm"`
		} `json:"actions"`
	} `json:"record"`
}

func decodeResult[T any](t *testing.T, raw string) T {
	t.Helper()
	var value T
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return value
}

func TestThePowerContextPluginLoadsTheProfileAndTheNewestTopics(t *testing.T) {
	h := newPowerContextHarness(t, `{"serverUrl":"https://memory.test","topicCount":5}`, nil)
	raw := h.call(HookPrepareSession, powerContextView("datav", "zh-CN", map[string]any{"reason": "start"}))
	result := decodeResult[sessionResult](t, raw)
	if result.System == nil {
		t.Fatalf("prepareSession = %s", raw)
	}
	block := result.System.Open + "\n" + strings.Join(result.System.Items, "\n") + "\n" + result.System.Close
	want := []string{
		"<project-context>\n以下是本项目的历史记忆，与用户当前指示冲突时以当前指示为准。",
		"项目画像\n- 沟通约定：用中文沟通，注释写中文。\n测试用表驱动写法。",
		// The five topics published last, newest first, their summaries cut
		// at 100 characters.
		"主题目录（最近更新的 5 个）\n- 主题 27：" + strings.Repeat("摘", 100) + "…\n- 主题 26：",
		"- 主题 23：",
		"还有更多主题没有列出。\n</project-context>",
	}
	for _, part := range want {
		if !strings.Contains(block, part) {
			t.Fatalf("block lacks %q:\n%s", part, block)
		}
	}
	if strings.Contains(block, "脱敏占位符") || strings.Contains(block, "主题 22") {
		t.Fatalf("block = %s", block)
	}
	if result.Record.Title != "记忆装载" || result.Record.Status != "ok" || !strings.HasPrefix(result.Record.Summary, "画像 · 主题 5/30 个 · ") ||
		len(result.Record.Actions) != 1 || result.Record.Actions[0].ID != "refresh" || result.Record.Actions[0].Label != "刷新记忆" {
		t.Fatalf("record = %+v", result.Record)
	}
	// The project was found on the server by its title, and its profile
	// policy turned on once.
	if created := h.server.requests("POST", "/v1/scopes"); len(created) != 0 {
		t.Fatalf("an existing project was created again: %+v", created)
	}
	if puts := h.server.requests("PUT", "/profile-policy"); len(puts) != 1 || !strings.Contains(string(puts[0].JSON), `"expected_version":0`) {
		t.Fatalf("policy writes = %+v", puts)
	}
	// The same data renders the same bytes, and the policy is not asked for
	// again.
	if again := h.call(HookPrepareSession, powerContextView("datav", "zh-CN", map[string]any{"reason": "manual"})); again != raw {
		t.Fatalf("the block changed:\n%s\n%s", raw, again)
	}
	if reads := h.server.requests("GET", "/profile-policy"); len(reads) != 1 {
		t.Fatalf("policy reads = %d", len(reads))
	}
	// A policy someone set stays as it is.
	h.call(HookPrepareSession, powerContextView("paseo", "zh-CN", nil))
	if puts := h.server.requests("PUT", "/profile-policy"); len(puts) != 1 {
		t.Fatalf("an existing policy was rewritten: %+v", puts)
	}
	// Nothing asks the server to process: that is its own schedule's work.
	for _, path := range []string{"/v1/memory/flush", "/v1/profile/flush", "/v1/topic-memory/flush"} {
		if posts := h.server.requests("POST", path); len(posts) != 0 {
			t.Fatalf("%s was called", path)
		}
	}
}

func TestThePowerContextBlockFollowsTheSessionsLanguageAndPrivacy(t *testing.T) {
	h := newPowerContextHarness(t, `{"serverUrl":"https://memory.test","topicCount":2}`, nil)
	for language, want := range map[string][]string{
		"en": {"Below is this project's memory", "Project profile\n- 沟通约定: ", "Topics (the 2 updated most recently)", "redaction placeholders", "More topics are not listed here."},
		"ja": {"以下はこのプロジェクトの過去の記憶です", "プロジェクトのプロフィール", "トピック一覧（最近更新された 2 件）", "マスク用のプレースホルダー"},
	} {
		raw := h.call(HookPrepareSession, powerContextView("datav", language, map[string]any{"redacted": true}))
		result := decodeResult[sessionResult](t, raw)
		block := result.System.Open + "\n" + strings.Join(result.System.Items, "\n") + "\n" + result.System.Close
		for _, part := range want {
			if !strings.Contains(block, part) {
				t.Fatalf("%s block lacks %q:\n%s", language, part, block)
			}
		}
		if strings.Contains(strings.ToLower(block), "astrlink") {
			t.Fatalf("the %s block names the gateway:\n%s", language, block)
		}
	}
}

func TestThePowerContextBlockKeepsToItsBudget(t *testing.T) {
	h := newPowerContextHarness(t, `{"serverUrl":"https://memory.test","topicCount":50}`, nil)
	h.server.topicPages = 20
	for index := range 60 {
		h.server.topics = append(h.server.topics, fmt.Sprintf(`{"title":"长主题 %02d","summary":"%s","published_at":"2026-11-%02dT08:00:00Z"}`, index, strings.Repeat("长", 300), 1+index%28))
	}
	result := decodeResult[sessionResult](t, h.call(HookPrepareSession, powerContextView("datav", "zh-CN", nil)))
	block := result.System.Open + "\n" + strings.Join(result.System.Items, "\n") + "\n" + result.System.Close
	if len(block) > 8<<10 || !strings.HasSuffix(block, "还有更多主题没有列出。\n</project-context>") {
		t.Fatalf("block of %d bytes:\n%.300s…", len(block), block)
	}
	// Two pages of 20 were read; the rest is left to the search.
	if pages := h.server.requests("GET", "/artifacts/topic-memory"); len(pages) != 2 {
		t.Fatalf("topic pages read = %d", len(pages))
	}
	if !strings.Contains(result.Record.Summary, "/40 个") {
		t.Fatalf("record = %+v", result.Record)
	}
}

func TestThePowerContextPluginSaysWhyItLoadsNothing(t *testing.T) {
	h := newPowerContextHarness(t, `{"serverUrl":"https://memory.test"}`, nil)
	for _, test := range []struct {
		project, want string
	}{{"", "会话还没有项目"}, {"none", "本会话不纳入记忆"}} {
		result := decodeResult[sessionResult](t, h.call(HookPrepareSession, powerContextView(test.project, "zh-CN", nil)))
		if result.System != nil || result.Record.Status != "skipped" || result.Record.Summary != test.want {
			t.Fatalf("%q = %+v", test.project, result)
		}
	}
	h.server.profile, h.server.topics = "", nil
	result := decodeResult[sessionResult](t, h.call(HookPrepareSession, powerContextView("datav", "en", nil)))
	if result.System != nil || result.Record.Summary != "The project has no profile or topics yet" {
		t.Fatalf("an empty project = %+v", result)
	}
	h.server.version = "1.2.9"
	h.store.values = nil
	result = decodeResult[sessionResult](t, h.call(HookPrepareSession, powerContextView("datav", "zh-CN", nil)))
	if result.System != nil || result.Record.Summary != "服务器不可用：PowerContext 1.2.9 太旧，需要 1.3 或以上" {
		t.Fatalf("an old server = %+v", result)
	}
	h.server.down = true
	h.store.values = nil
	result = decodeResult[sessionResult](t, h.call(HookPrepareSession, powerContextView("datav", "zh-CN", nil)))
	if result.Record.Summary != "服务器不可用：连不上" {
		t.Fatalf("a server that does not answer = %+v", result)
	}
}

type turnResult struct {
	Context string              `json:"context"`
	Choices map[string][]string `json:"choices"`
	Record  struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
	} `json:"record"`
}

func TestThePowerContextPluginAsksOnceWithTheKnownProjects(t *testing.T) {
	used := sessionsUsing{
		{Value: "paseo", Sessions: 3, LastUsedAt: time.Date(2026, 10, 21, 1, 0, 0, 0, time.UTC).UnixMilli()},
		{Value: "none", Sessions: 2, LastUsedAt: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC).UnixMilli()},
		{Value: "notes", Sessions: 1, LastUsedAt: time.Date(2025, 12, 30, 9, 0, 0, 0, time.UTC).UnixMilli()},
	}
	h := newPowerContextHarness(t, `{"serverUrl":"https://memory.test"}`, used)
	raw := h.call(HookPrepareTurn, powerContextView("", "zh-CN", map[string]any{"workspace": map[string]any{"name": "DataV"}}))
	result := decodeResult[turnResult](t, raw)
	// The directory's project first, then this computer's by last use (in
	// New York, 01:00 UTC on the 21st is the 20th), then the server's by
	// name. A title that is no project name stays out.
	want := "本会话还没有选定项目记忆。不要打断当前任务，在本轮回复的末尾问用户：是否把记忆纳入一个项目，用下面已有的，还是新建一个。\n" +
		"已有项目（共 3 个）：\n" +
		"1. datav（当前目录）\n" +
		"2. paseo（上次使用 10 月 20 日）\n" +
		"3. notes（上次使用 2025 年 12 月 30 日）\n" +
		"用户回答后（说编号或名字都可以），在回复开头单独一行输出 <memory-project>项目名</memory-project>：\n"
	if !strings.HasPrefix(result.Context, want) || !strings.Contains(result.Context, "<memory-project>none</memory-project>") {
		t.Fatalf("the ask:\n%s\nwant it to start with:\n%s", result.Context, want)
	}
	if strings.Join(result.Choices["project"], ",") != "datav,paseo,notes" || result.Record.Summary != "列出 3 个项目" {
		t.Fatalf("result = %+v", result)
	}
	// The fixed words never name the gateway.
	if strings.Contains(strings.ToLower(result.Context), "astrlink") {
		t.Fatalf("the ask names the gateway:\n%s", result.Context)
	}
	// A turn that is not the first, or a session with a project, asks
	// nothing.
	notFirst := powerContextView("", "zh-CN", nil)
	notFirst["session"] = map[string]any{"id": "session_1", "turn": 2}
	for _, view := range []map[string]any{notFirst, powerContextView("datav", "zh-CN", nil), powerContextView("none", "zh-CN", nil)} {
		if got := h.call(HookPrepareTurn, view); got != "" && got != "null" {
			t.Fatalf("prepareTurn = %s", got)
		}
	}
	// English and Japanese sessions are asked in their language; a list cut
	// short says how many there are.
	h = newPowerContextHarness(t, `{"serverUrl":"https://memory.test","listProjects":2}`, used)
	english := decodeResult[turnResult](t, h.call(HookPrepareTurn, powerContextView("", "en", nil)))
	if !strings.Contains(english.Context, "Existing projects (3 in all; the 2 used most recently):\n1. paseo (last used Oct 20)\n2. notes (last used Dec 30, 2025)\n") ||
		len(english.Choices["project"]) != 2 {
		t.Fatalf("english ask:\n%s", english.Context)
	}
	japanese := decodeResult[turnResult](t, h.call(HookPrepareTurn, powerContextView("", "ja", nil)))
	if !strings.Contains(japanese.Context, "既存のプロジェクト（全 3 件、最近使った 2 件）：\n1. paseo（最終使用 10月20日）") {
		t.Fatalf("japanese ask:\n%s", japanese.Context)
	}
	// Without a list the ask asks for a name.
	h = newPowerContextHarness(t, `{"serverUrl":"https://memory.test","listProjects":0}`, used)
	plain := decodeResult[turnResult](t, h.call(HookPrepareTurn, powerContextView("", "zh-CN", nil)))
	if !strings.HasPrefix(plain.Context, "本会话还没有选定项目记忆。不要打断当前任务，在本轮回复的末尾问用户：是否把记忆纳入一个项目，纳入的话项目名用什么。\n用户回答后") ||
		len(plain.Choices["project"]) != 0 || plain.Record.Summary != "没有列出项目" {
		t.Fatalf("an ask without a list = %+v", plain)
	}
	// With the server down, this computer's projects are still offered.
	h = newPowerContextHarness(t, `{"serverUrl":"https://memory.test"}`, used)
	h.server.down = true
	offline := decodeResult[turnResult](t, h.call(HookPrepareTurn, powerContextView("", "zh-CN", nil)))
	if strings.Join(offline.Choices["project"], ",") != "paseo,notes" {
		t.Fatalf("an ask with the server down = %+v", offline)
	}
	// The user may opt out of asking.
	h = newPowerContextHarness(t, `{"serverUrl":"https://memory.test","askProject":false}`, used)
	if got := h.call(HookPrepareTurn, powerContextView("", "zh-CN", nil)); got != "" && got != "null" {
		t.Fatalf("prepareTurn with asking off = %s", got)
	}
}

func TestThePowerContextPluginWritesWithoutProcessing(t *testing.T) {
	h := newPowerContextHarness(t, `{"serverUrl":"https://memory.test"}`, nil)
	result := decodeResult[struct {
		Record struct {
			Title, Status, Summary string
		} `json:"record"`
	}](t, h.call(HookObserveTurn, powerContextView("brand-new", "zh-CN", nil)))
	if result.Record.Title != "记忆写入" || result.Record.Status != "ok" || result.Record.Summary != "已写入" {
		t.Fatalf("observeTurn = %+v", result)
	}
	created := h.server.requests("POST", "/v1/scopes")
	if len(created) != 1 || !strings.Contains(string(created[0].JSON), `"idempotency_key":"astrlink:project:brand-new"`) {
		t.Fatalf("scope creations = %+v", created)
	}
	sources := h.server.requests("POST", "/v1/sources/content")
	if len(sources) != 1 || !strings.Contains(string(sources[0].JSON), `"content":"帮我把分页改成游标"`) {
		t.Fatalf("sources = %+v", sources)
	}
	if posts := h.server.requests("POST", "/v1/memory/flush"); len(posts) != 0 {
		t.Fatalf("a write asked the server to process: %+v", posts)
	}
	short := powerContextView("brand-new", "zh-CN", map[string]any{"user": map[string]any{"text": "好", "typed": "好"}})
	if got := h.call(HookObserveTurn, short); got != "" && got != "null" {
		t.Fatalf("a short turn was written: %s", got)
	}
}

func TestThePowerContextPluginShowsTheServersProcessing(t *testing.T) {
	h := newPowerContextHarness(t, `{"serverUrl":"https://memory.test"}`, nil)
	test := decodeResult[struct {
		Status  string `json:"status"`
		Summary string `json:"summary"`
		View    []struct {
			Items []struct{ Label, Value string } `json:"items"`
		} `json:"view"`
	}](t, h.call(HookTestConnection, map[string]any{"language": "zh-CN"}))
	if test.Status != "ok" || test.Summary != "PowerContext 1.3.0 · 3 ms" || len(test.View) != 1 {
		t.Fatalf("testConnection = %+v", test)
	}
	got := map[string]string{}
	for _, item := range test.View[0].Items {
		got[item.Label] = item.Value
	}
	// In New York 03:04 UTC on the 21st is 23:04 on the 20th.
	if got["自动整理"] != "已打开" || got["最近整理成功"] != "10 月 20 日 23:04" || got["最近整理失败"] != "10 月 20 日 11:00 · model_timeout" ||
		got["可检索"] != "原子记忆 / 主题记忆 / 经验 / 技能" {
		t.Fatalf("view = %+v", got)
	}
	h.server.processing = "off"
	h.call(HookObserveTurn, powerContextView("datav", "en", nil))
	status := h.call(HookDescribeStatus, map[string]any{"language": "en"})
	if !strings.Contains(status, `"Automatic processing","value":"off: no atomic memory is made"`) || !strings.Contains(status, `"Last write","value":"datav · Written · just now"`) {
		t.Fatalf("describeStatus = %s", status)
	}
	h.server.version = "1.4.0"
	if newer := h.call(HookTestConnection, map[string]any{"language": "en"}); !strings.Contains(newer, "PowerContext 1.4.0 · 3 ms · this version has not been verified") {
		t.Fatalf("a newer server = %s", newer)
	}
	h.server.version = "1.2.0"
	if older := h.call(HookTestConnection, map[string]any{"language": "zh-CN"}); !strings.Contains(older, `"status":"error"`) || !strings.Contains(older, "太旧") {
		t.Fatalf("an older server = %s", older)
	}
	if action := h.call(HookOnAction, map[string]any{"action": "refresh", "session": map[string]any{"id": "session_1", "turn": 0}, "meta": map[string]any{}, "language": "ja"}); action != `{"refresh":true,"message":"このセッションの次のリクエストで記憶を読み込み直します。"}` {
		t.Fatalf("onAction = %s", action)
	}
}
