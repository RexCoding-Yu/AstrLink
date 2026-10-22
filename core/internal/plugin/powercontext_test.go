package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"sort"
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
	// with a profile policy. profile is the profile's Markdown, none when
	// empty.
	scopes   map[string]string
	policies map[string]bool
	profile  string
	topics   []string
	// processing is what capabilities say of automatic processing: on, off
	// or absent.
	processing string
	topicPages int
	down       bool
	// found are the search results of each kind of memory; slow kinds
	// answer only a search by words, failing ones nothing.
	found   map[string][]string
	slow    map[string]bool
	failing map[string]bool
	// nextScopes is the cursor the list of scopes ends with.
	nextScopes string
}

func newFakePowerContext() *fakePowerContext {
	server := &fakePowerContext{
		scopes:     map[string]string{"datav": "scp_datav", "paseo": "scp_paseo", "Has Space": "scp_bad"},
		policies:   map[string]bool{"scp_paseo": true},
		profile:    "## 沟通约定\n- 用中文沟通，注释写中文。\n- 测试用表驱动写法。\n",
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
		titles := make([]string, 0, len(server.scopes))
		for title := range server.scopes {
			titles = append(titles, title)
		}
		sort.Strings(titles)
		for _, title := range titles {
			if query.Get("query") != "" && !strings.Contains(title, query.Get("query")) {
				continue
			}
			items = append(items, `{"scope_id":"`+server.scopes[title]+`","title":"`+title+`","summary":"s","context_references":[],"external_references":[],"version":1}`)
		}
		next := "null"
		if server.nextScopes != "" && query.Get("cursor") == "" {
			next = `"` + server.nextScopes + `"`
		}
		return reply(200, `{"items":[`+strings.Join(items, ",")+`],"next_cursor":`+next+`}`)
	case request.Method == "POST" && strings.HasSuffix(target.Path, "/search"):
		family := strings.TrimSuffix(target.Path[strings.LastIndex(strings.TrimSuffix(target.Path, "/search"), "/")+1:], "/search")
		var body struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
			Mode  string `json:"mode"`
		}
		_ = json.Unmarshal(request.JSON, &body)
		switch {
		case server.failing[family]:
			return reply(422, `{"error":{"code":"unsupported","message":"search mode is not supported"}}`)
		case server.slow[family] && body.Mode != "text":
			return HTTPResponse{Error: FetchTimeout, Took: 5 * time.Second}
		}
		results := server.found[family]
		if len(results) > body.Limit {
			results = results[:body.Limit]
		}
		return reply(200, `{"results":[`+strings.Join(results, ",")+`]}`)
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
	case strings.HasSuffix(target.Path, "/artifacts/profile/profile"):
		// The profile artifact itself: its list item carries no text. 1.3
		// answers for one that does not exist as for one nobody owns yet.
		if server.profile == "" {
			return reply(503, `{"error":{"code":"artifact_owner_pending","message":"Access Control is unavailable.","details":null}}`)
		}
		content, _ := json.Marshal(server.profile)
		return reply(200, `{"scope_id":"scp","family":"profile","artifact_id":"profile","revision":2,"content":{"schema":"powercontext.profile.v1","media_type":"text/markdown","content":`+string(content)+`},"sources":[],"artifacts":[],"content_digest":"sha256:0"}`)
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
	all := []string{CapabilityInject, CapabilityRequest, CapabilityNetwork, CapabilityStorage, CapabilityViews, CapabilityTools}
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
		// The profile's Markdown as the server wrote it.
		"项目画像\n## 沟通约定\n- 用中文沟通，注释写中文。\n- 测试用表驱动写法。\n主题目录",
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
	// The profile is read whole, from its artifact.
	if reads := h.server.requests("GET", "/artifacts/profile/profile"); len(reads) != 1 {
		t.Fatalf("profile reads = %d", len(reads))
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
		"en": {"Below is this project's memory", "Project profile\n## 沟通约定\n- 用中文沟通", "Topics (the 2 updated most recently)", "redaction placeholders", "More topics are not listed here."},
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
	Block struct {
		Items []string `json:"items"`
	} `json:"context"`
	Choices map[string][]string `json:"choices"`
	Record  struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
	} `json:"record"`
	// Context is the block's items one a line, as the host lays them out.
	Context string `json:"-"`
}

func decodeTurn(t *testing.T, raw string) turnResult {
	t.Helper()
	result := decodeResult[turnResult](t, raw)
	result.Context = strings.Join(result.Block.Items, "\n")
	return result
}

func TestThePowerContextPluginAsksOnceWithTheKnownProjects(t *testing.T) {
	used := sessionsUsing{
		{Value: "paseo", Sessions: 3, LastUsedAt: time.Date(2026, 10, 21, 1, 0, 0, 0, time.UTC).UnixMilli()},
		{Value: "none", Sessions: 2, LastUsedAt: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC).UnixMilli()},
		{Value: "notes", Sessions: 1, LastUsedAt: time.Date(2025, 12, 30, 9, 0, 0, 0, time.UTC).UnixMilli()},
	}
	h := newPowerContextHarness(t, `{"serverUrl":"https://memory.test"}`, used)
	raw := h.call(HookPrepareTurn, powerContextView("", "zh-CN", map[string]any{"workspace": map[string]any{"name": "DataV"}}))
	result := decodeTurn(t, raw)
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
	if strings.Join(result.Choices["project"], ",") != "datav,paseo,notes" || result.Record.Summary != "列出 3 个项目" ||
		strings.Contains(result.Context, "{{lookup:") {
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
	english := decodeTurn(t, h.call(HookPrepareTurn, powerContextView("", "en", nil)))
	if !strings.Contains(english.Context, "Existing projects (3 in all; the 2 used most recently):\n1. paseo (last used Oct 20)\n2. notes (last used Dec 30, 2025)\n") ||
		len(english.Choices["project"]) != 2 {
		t.Fatalf("english ask:\n%s", english.Context)
	}
	// Past the list cut short, the model may look up the rest: an item of
	// its own, which the gateway drops where the session cannot run it.
	if !slices.Contains(english.Block.Items, "If the user names a project not listed above, look up all projects by a keyword with your shell:\n{{lookup:projects}}") {
		t.Fatalf("english ask items = %q", english.Block.Items)
	}
	japanese := decodeTurn(t, h.call(HookPrepareTurn, powerContextView("", "ja", nil)))
	if !strings.Contains(japanese.Context, "既存のプロジェクト（全 3 件、最近使った 2 件）：\n1. paseo（最終使用 10月20日）") {
		t.Fatalf("japanese ask:\n%s", japanese.Context)
	}
	// Without a list the ask asks for a name.
	h = newPowerContextHarness(t, `{"serverUrl":"https://memory.test","listProjects":0}`, used)
	plain := decodeTurn(t, h.call(HookPrepareTurn, powerContextView("", "zh-CN", nil)))
	if !strings.HasPrefix(plain.Context, "本会话还没有选定项目记忆。不要打断当前任务，在本轮回复的末尾问用户：是否把记忆纳入一个项目，纳入的话项目名用什么。\n用户回答后") ||
		len(plain.Choices["project"]) != 0 || plain.Record.Summary != "没有列出项目" {
		t.Fatalf("an ask without a list = %+v", plain)
	}
	// With the server down, this computer's projects are still offered.
	h = newPowerContextHarness(t, `{"serverUrl":"https://memory.test"}`, used)
	h.server.down = true
	offline := decodeTurn(t, h.call(HookPrepareTurn, powerContextView("", "zh-CN", nil)))
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

type toolResult struct {
	Text   string `json:"text"`
	Hits   *int   `json:"hits"`
	Record struct {
		Title, Status, Summary string
	} `json:"record"`
}

func toolView(name, project, language string, input map[string]any) map[string]any {
	view := map[string]any{
		"name": name, "input": input, "language": language,
		"session": map[string]any{"id": "session_1", "turn": 2}, "meta": map[string]any{},
	}
	if project != "" {
		view["meta"] = map[string]any{"project": map[string]any{"value": project, "source": "session"}}
	}
	return view
}

func newLookupHarness(t *testing.T, config string, used sessionsUsing) *powerContextHarness {
	t.Helper()
	h := newPowerContextHarness(t, config, used)
	h.server.found = map[string][]string{
		"topic-memory": {
			`{"family":"topic-memory","artifact_id":"tm_1","revision":2,"lineage":{},"content":{"title":"分页","summary":"列表改成游标分页。","detail":"游标是 base64 编码的 id。\n每页 50 条。"}}`,
			`{"family":"topic-memory","artifact_id":"tm_2","revision":1,"lineage":{},"content":{"title":"大屏刷新","summary":"大屏改成增量推送。","detail":"推送间隔可配置。"}}`,
		},
		"atomic-memory": {
			`{"family":"atomic-memory","artifact_id":"am_1","revision":1,"lineage":{},"content":{"schema":"powercontext.atomic-memory.v1","kind":"fact","text":"游标分页的边界：最后一页返回空游标。"}}`,
			`{"family":"atomic-memory","artifact_id":"am_2","revision":1,"lineage":{},"content":{"schema":"powercontext.atomic-memory.v1","kind":"fact","text":"分页参数叫 cursor 和 limit。"}}`,
			`{"family":"atomic-memory","artifact_id":"am_3","revision":1,"lineage":{},"content":{"schema":"powercontext.atomic-memory.v1","kind":"fact","text":"第三条。"}}`,
		},
		"experience": {
			`{"family":"experience","artifact_id":"ex_1","revision":1,"lineage":{},"content":{"situation":"改分页","action":"先写边界测试","outcome":"少返工","lesson":"改分页先补边界测试。"}}`,
		},
		"skill": {
			`{"family":"skill","artifact_id":"sk_1","revision":1,"lineage":{},"content":{"name":"pagination-review","description":"检查分页实现的清单。","instructions":"…","validation":[]}}`,
		},
	}
	return h
}

func TestThePowerContextSearchTakesEachKindInTurns(t *testing.T) {
	h := newLookupHarness(t, `{"serverUrl":"https://memory.test","lookupLimit":5}`, nil)
	result := decodeResult[toolResult](t, h.call(HookCallTool, toolView("search", "datav", "zh-CN", map[string]any{"query": "分页的游标边界"})))
	// Rank by rank, one kind after another, five in all.
	want := "- [主题记忆] 分页：列表改成游标分页。\n" +
		"- [原子记忆] 游标分页的边界：最后一页返回空游标。\n" +
		"- [经验] 改分页先补边界测试。\n" +
		"- [技能] pagination-review：检查分页实现的清单。\n" +
		"- [主题记忆] 大屏刷新：大屏改成增量推送。"
	if result.Text != want || result.Hits == nil || *result.Hits != 5 || result.Record.Title != "记忆查询" ||
		result.Record.Status != "ok" || result.Record.Summary != "检索 · 命中 5 条" {
		t.Fatalf("search = %+v\n%s", result, result.Text)
	}
	// Each kind was searched in the project's scope, the slow ones by words
	// too, and none was asked for more than the limit.
	searches := h.server.requests("POST", "/search")
	if len(searches) != 6 {
		t.Fatalf("searches = %d", len(searches))
	}
	for _, sent := range searches {
		if !strings.Contains(sent.URL, "/v1/scopes/scp_datav/artifacts/") || !strings.Contains(string(sent.JSON), `"query":"分页的游标边界"`) {
			t.Fatalf("search = %s %s", sent.URL, sent.JSON)
		}
	}
	// The model may ask for fewer, never more than the settings allow.
	fewer := decodeResult[toolResult](t, h.call(HookCallTool, toolView("search", "datav", "en", map[string]any{"query": "cursor", "limit": 2})))
	if fewer.Text != "- [topics] 分页: 列表改成游标分页。\n- [atomic memory] 游标分页的边界：最后一页返回空游标。" || fewer.Record.Summary != "search · 2 hits" {
		t.Fatalf("a search for 2 = %+v", fewer)
	}
	// A full search late gives way to the search by words; a kind that
	// fails leaves the others.
	h.server.slow = map[string]bool{"topic-memory": true}
	h.server.failing = map[string]bool{"experience": true}
	partial := decodeResult[toolResult](t, h.call(HookCallTool, toolView("search", "datav", "zh-CN", map[string]any{"query": "分页"})))
	if !strings.HasPrefix(partial.Text, "- [主题记忆] 分页：") || partial.Record.Summary != "检索 · 命中 5 条 · 只用了关键词检索 · 1 类检索失败" {
		t.Fatalf("a partial search = %+v", partial)
	}
	// The settings leave kinds out.
	h = newLookupHarness(t, `{"serverUrl":"https://memory.test","searchTopics":false,"searchExperience":false,"searchSkills":false}`, nil)
	atomic := decodeResult[toolResult](t, h.call(HookCallTool, toolView("search", "datav", "zh-CN", map[string]any{"query": "分页"})))
	if strings.Count(atomic.Text, "- [原子记忆]") != 3 || strings.Contains(atomic.Text, "主题") {
		t.Fatalf("atomic memory only = %+v", atomic)
	}
	h = newLookupHarness(t, `{"serverUrl":"https://memory.test","searchTopics":false,"searchAtomic":false,"searchExperience":false,"searchSkills":false}`, nil)
	if off := decodeResult[toolResult](t, h.call(HookCallTool, toolView("search", "datav", "zh-CN", map[string]any{"query": "分页"}))); off.Record.Status != "skipped" || *off.Hits != 0 {
		t.Fatalf("every kind off = %+v", off)
	}
	if len(h.server.requests("POST", "/search")) != 0 {
		t.Fatal("a search with every kind off reached the server")
	}
}

func TestThePowerContextSearchKeepsToWhatTheServerTakes(t *testing.T) {
	h := newLookupHarness(t, `{"serverUrl":"https://memory.test"}`, nil)
	h.server.found = map[string][]string{}
	long := strings.Repeat("分页游标", 40)
	result := decodeResult[toolResult](t, h.call(HookCallTool, toolView("search", "datav", "zh-CN", map[string]any{"query": long})))
	if result.Text != "没有找到相关的记忆。" || *result.Hits != 0 || result.Record.Summary != "检索 · 没有命中" {
		t.Fatalf("no hits = %+v", result)
	}
	// 1.3's topic search takes at most 64 terms: a long query keeps its start
	// and its end.
	var sent struct {
		Query string `json:"query"`
	}
	_ = json.Unmarshal(h.server.requests("POST", "/artifacts/topic-memory/search")[0].JSON, &sent)
	if runes := []rune(sent.Query); len(runes) > 32 || !strings.HasPrefix(sent.Query, "分页游标") || !strings.HasSuffix(sent.Query, "分页游标") {
		t.Fatalf("query sent = %q", sent.Query)
	}
	// A project the server does not know has no memory, and a lookup does
	// not make it one.
	unknown := decodeResult[toolResult](t, h.call(HookCallTool, toolView("search", "brand-new", "zh-CN", map[string]any{"query": "分页"})))
	if unknown.Text != "这个项目在记忆服务器上还没有记忆。" || len(h.server.requests("POST", "/v1/scopes")) != 0 {
		t.Fatalf("an unknown project = %+v", unknown)
	}
	h.server.down = true
	h.store.values = nil
	down := decodeResult[toolResult](t, h.call(HookCallTool, toolView("search", "datav", "en", map[string]any{"query": "x"})))
	if down.Record.Status != "failed" || down.Text != "Server unavailable: unreachable" {
		t.Fatalf("a server down = %+v", down)
	}
}

func TestThePowerContextReadGivesATopicInFull(t *testing.T) {
	h := newLookupHarness(t, `{"serverUrl":"https://memory.test"}`, nil)
	// The search finds the closest first; the topic titled so wins.
	h.server.found["topic-memory"] = []string{h.server.found["topic-memory"][1], h.server.found["topic-memory"][0]}
	result := decodeResult[toolResult](t, h.call(HookCallTool, toolView("read", "datav", "zh-CN", map[string]any{"topic": "分页"})))
	if result.Text != "主题：分页\n摘要：列表改成游标分页。\n正文：\n游标是 base64 编码的 id。\n每页 50 条。" || *result.Hits != 1 ||
		!strings.HasPrefix(result.Record.Summary, "读取主题 · ") {
		t.Fatalf("read = %+v", result)
	}
	searches := h.server.requests("POST", "/artifacts/topic-memory/search")
	if len(searches) != 1 || !strings.Contains(string(searches[0].JSON), `"mode":"text"`) || !strings.Contains(string(searches[0].JSON), `"limit":3`) {
		t.Fatalf("searches = %+v", searches)
	}
	// No title the same: the closest, said so.
	closest := decodeResult[toolResult](t, h.call(HookCallTool, toolView("read", "datav", "en", map[string]any{"topic": "刷新"})))
	if !strings.HasPrefix(closest.Text, "No topic has exactly that title; this is the closest, \"大屏刷新\".\nTopic: 大屏刷新\n") {
		t.Fatalf("closest = %+v", closest)
	}
	// A long text stops within what a lookup returns.
	h.server.found["topic-memory"] = []string{`{"family":"topic-memory","artifact_id":"tm_9","revision":1,"lineage":{},"content":{"title":"长","summary":"s","detail":"` + strings.Repeat("长", 5000) + `"}}`}
	long := decodeResult[toolResult](t, h.call(HookCallTool, toolView("read", "datav", "zh-CN", map[string]any{"topic": "长"})))
	if len(long.Text) > 7600 || !strings.HasSuffix(long.Text, "\n（正文太长，后面截掉了）") {
		t.Fatalf("a long topic of %d bytes ends %q", len(long.Text), long.Text[len(long.Text)-60:])
	}
	h.server.found["topic-memory"] = nil
	if missing := decodeResult[toolResult](t, h.call(HookCallTool, toolView("read", "datav", "ja", map[string]any{"topic": "x"}))); missing.Text != "そのトピックは見つかりませんでした。" || *missing.Hits != 0 {
		t.Fatalf("a missing topic = %+v", missing)
	}
}

func TestThePowerContextProjectsLookupPages(t *testing.T) {
	used := sessionsUsing{{Value: "paseo-web", Sessions: 1, LastUsedAt: 1}, {Value: "notes", Sessions: 1, LastUsedAt: 1}}
	h := newLookupHarness(t, `{"serverUrl":"https://memory.test"}`, used)
	h.server.scopes["paseo-api"] = "scp_paseo_api"
	h.server.nextScopes = "c2"
	// It runs for a session without a project, and leads with this
	// computer's projects.
	result := decodeResult[toolResult](t, h.call(HookCallTool, toolView("projects", "", "zh-CN", map[string]any{"query": "paseo"})))
	want := "项目（3 个）：\n- paseo-web\n- paseo\n- paseo-api\n" + `还有更多：加上 --data-urlencode "cursor=c2" 再查一次。`
	if result.Text != want || *result.Hits != 3 || result.Record.Summary != "列出项目 · 3 个" {
		t.Fatalf("projects = %+v\n%s", result, result.Text)
	}
	listed := h.server.requests("GET", "/v1/scopes")
	if got := listed[len(listed)-1].URL; !strings.Contains(got, "query=paseo") || !strings.Contains(got, "query_field=title") {
		t.Fatalf("list = %s", got)
	}
	// The next page has the server's only.
	next := decodeResult[toolResult](t, h.call(HookCallTool, toolView("projects", "", "en", map[string]any{"query": "paseo", "cursor": "c2"})))
	if next.Text != "Projects (2):\n- paseo\n- paseo-api" {
		t.Fatalf("next page = %+v", next)
	}
	none := decodeResult[toolResult](t, h.call(HookCallTool, toolView("projects", "", "zh-CN", map[string]any{"query": "nothing", "cursor": "c2"})))
	if none.Text != "没有符合的项目。" || *none.Hits != 0 {
		t.Fatalf("no project = %+v", none)
	}
}

func TestThePowerContextPluginSearchesAsTheServersVersionDoes(t *testing.T) {
	long := strings.Repeat("分页游标", 30)
	queries := func(h *powerContextHarness) map[string]map[string]any {
		sent := map[string]map[string]any{}
		for _, request := range h.server.requests("POST", "/search") {
			var body map[string]any
			_ = json.Unmarshal(request.JSON, &body)
			family := strings.TrimSuffix(request.URL[strings.Index(request.URL, "/artifacts/")+len("/artifacts/"):], "/search")
			if body["mode"] == nil || body["mode"] == "hybrid" {
				sent[family] = body
			}
		}
		return sent
	}
	// 1.3: atomic memory searches as the server sees fit; only the topic
	// search, which counts terms, is cut, by characters and pairs.
	h := newLookupHarness(t, `{"serverUrl":"https://memory.test"}`, nil)
	h.call(HookCallTool, toolView("search", "datav", "zh-CN", map[string]any{"query": long}))
	sent := queries(h)
	if _, set := sent["atomic-memory"]["mode"]; set || sent["atomic-memory"]["query"] != long {
		t.Fatalf("1.3 atomic search = %+v", sent["atomic-memory"])
	}
	older := []rune(sent["topic-memory"]["query"].(string))
	if len(older) > 32 {
		t.Fatalf("1.3 topic query of %d characters", len(older))
	}
	// 1.4 searches atomic memory by words alone unless asked for both, and
	// counts a Han character as a word at most: the topic query keeps more.
	h = newLookupHarness(t, `{"serverUrl":"https://memory.test"}`, nil)
	h.server.version = "1.4.0"
	h.call(HookCallTool, toolView("search", "datav", "zh-CN", map[string]any{"query": long}))
	sent = queries(h)
	if sent["atomic-memory"]["mode"] != "hybrid" || sent["atomic-memory"]["query"] != long {
		t.Fatalf("1.4 atomic search = %+v", sent["atomic-memory"])
	}
	if newer := []rune(sent["topic-memory"]["query"].(string)); len(newer) < 60 || len(newer) > 61 {
		t.Fatalf("1.4 topic query of %d characters", len(newer))
	}
	// Underscores join a word on both versions.
	h = newLookupHarness(t, `{"serverUrl":"https://memory.test"}`, nil)
	words := strings.TrimSpace(strings.Repeat("snake_case_name ", 70))
	h.call(HookCallTool, toolView("search", "datav", "zh-CN", map[string]any{"query": words}))
	if got := queries(h)["topic-memory"]["query"].(string); len(strings.Fields(got)) > 60 {
		t.Fatalf("a query of %d words went out", len(strings.Fields(got)))
	}
}
