package pluginhost

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/tidwall/gjson"
)

// loaderPlugin loads a project's profile into the system prompt, in the
// session's language, and asks for a project in the first turn it sees,
// offering two.
const loaderPlugin = `export const meta = {
  apiVersion: 1, key: "loader", name: "Loader", version: "1.0.0",
  capabilities: ["context.inject", "request.read", "network", "ui.views"], network: { purpose: "test" },
  metadata: [{ key: "project", label: "Project", pattern: "^[a-z0-9-]{1,32}$", marker: "memo-project" }],
};
export function prepareSession(view) {
  const project = view.meta.project;
  if (project === undefined || project.value === "none") return undefined;
  const profile = astrlink.http.fetch({ method: "GET", url: "https://memory.test/profile/" + project.value });
  return {
    system: { open: "<ctx " + view.language + ">", items: [project.value + ": " + (profile.text ?? profile.error)], close: "</ctx>" },
    record: { title: "装载", status: "ok", summary: view.reason, actions: [{ id: "refresh", label: "刷新记忆", confirm: "会重写缓存" }] },
  };
}
export function prepareTurn(turn) {
  if (turn.meta.project !== undefined || !turn.session.first) return { skip: "no_ask" };
  return {
    context: "ASK " + turn.language + (turn.workspace ? " in " + turn.workspace.name : ""),
    choices: { project: ["astrlink", "paseo", "Not Valid"] },
  };
}
export function onAction(view) {
  return { refresh: view.action === "refresh", message: "refresh " + view.session.id + "\n" };
}
`

// profiles answers each profile with the text the test set for it. While
// hold is set, a request waits for it.
type profiles struct {
	mutex sync.Mutex
	text  map[string]string
	hold  chan struct{}
	seen  int
}

func newProfiles() *profiles {
	return &profiles{text: map[string]string{"astrlink": "v1", "paseo": "p1", "brand-new": "b1"}}
}

func (server *profiles) set(project, text string) {
	server.mutex.Lock()
	defer server.mutex.Unlock()
	server.text[project] = text
}

func (server *profiles) holding(hold chan struct{}) {
	server.mutex.Lock()
	defer server.mutex.Unlock()
	server.hold = hold
}

func (server *profiles) Fetch(ctx context.Context, _ *plugin.Plugin, request plugin.HTTPRequest) plugin.HTTPResponse {
	server.mutex.Lock()
	hold := server.hold
	server.seen++
	server.mutex.Unlock()
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return plugin.HTTPResponse{Error: plugin.FetchTimeout}
		}
	}
	server.mutex.Lock()
	defer server.mutex.Unlock()
	return plugin.HTTPResponse{Status: http.StatusOK, Body: []byte(server.text[strings.TrimPrefix(request.URL, "https://memory.test/profile/")])}
}

func (server *profiles) requests() int {
	server.mutex.Lock()
	defer server.mutex.Unlock()
	return server.seen
}

// call is one request of a test session.
type call struct {
	session string
	client  string
	turn    int
	body    string
	project string
}

func (h *harness) send(t *testing.T, request call) (*Run, []byte) {
	t.Helper()
	header := http.Header{}
	if request.project != "" {
		header.Set(contract.MetadataHeader("project"), request.project)
	}
	client := request.client
	if client == "" {
		client = "claude_code"
	}
	run := h.host.Begin(context.Background(), Input{
		RequestID: "request_1", Protocol: contract.ProtocolAnthropicMessages, Model: "claude", Client: client,
		Header: header, Session: request.session, Turn: request.turn, Body: []byte(request.body),
	})
	if run == nil {
		t.Fatal("Begin = nil")
	}
	return run, run.Inject([]byte(request.body))
}

// withSystem is a request body with a system prompt.
func withSystem(system string, items ...string) string {
	encoded, _ := json.Marshal(system)
	return `{"model":"claude","system":` + string(encoded) + `,"messages":[` + strings.Join(items, ",") + `]}`
}

func systemOf(body []byte) string {
	system := gjson.GetBytes(body, "system")
	if system.Type == gjson.String {
		return system.Str
	}
	return system.Get("@this.#.text").String()
}

func TestAllPluginsPausedLeaveTheRequestAsItCame(t *testing.T) {
	server := newProfiles()
	h := newHarness(t, allCapabilities(), map[string]string{"loader": loaderPlugin}, server)
	if _, err := h.host.UpdateSettings(context.Background(), contract.PluginHostSettingsUpdate{Paused: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	body := messages(user("<memo-project>astrlink</memo-project>"))
	run, sent := h.send(t, call{session: "session_1", turn: 1, body: body, project: "astrlink"})
	if string(sent) != body {
		t.Fatalf("a paused gateway changed the request: %s", sent)
	}
	if run.NewMarkerScanner() != nil {
		t.Fatal("a paused gateway reads markers")
	}
	records, events := run.Report()
	if len(records) != 1 || records[0].Status != contract.PluginRecordSkipped || records[0].SkippedReason != contract.PluginSkipPausedAll || records[0].Validate() != nil {
		t.Fatalf("records = %+v", records)
	}
	if len(events) != 1 || run.Metadata().Values != nil {
		t.Fatalf("events = %+v, metadata = %+v", events, run.Metadata())
	}
	if values, _ := h.bindings.MetadataValues(context.Background(), storage.MetadataTargetSession, "session_1"); len(values) != 0 {
		t.Fatalf("a paused gateway bound the session: %+v", values)
	}
	// The turn's later calls say nothing more.
	later := messages(user("<memo-project>astrlink</memo-project>"), toolRound)
	run, _ = h.send(t, call{session: "session_1", turn: 1, body: later, project: "astrlink"})
	if records, _ := run.Report(); len(records) != 0 {
		t.Fatalf("records of a later call = %+v", records)
	}
	if server.requests() != 0 {
		t.Fatalf("a paused plugin reached its server %d times", server.requests())
	}
	// The pause survives a restart.
	h.restart(t)
	if !h.host.Settings().Paused {
		t.Fatal("the pause did not survive a restart")
	}
}

func TestAPluginStaysOutOfTheClientsItIsOffFor(t *testing.T) {
	settings := allCapabilities()
	settings.ExcludedClients = []string{"claude_code"}
	h := newHarness(t, settings, map[string]string{"loader": loaderPlugin}, newProfiles())
	body := messages(user("帮我看看分页逻辑"))
	run, sent := h.send(t, call{session: "session_1", turn: 1, body: body, project: "astrlink"})
	if string(sent) != body {
		t.Fatalf("an excluded client's request changed: %s", sent)
	}
	if records, _ := run.Report(); len(records) != 1 || records[0].SkippedReason != contract.PluginSkipClientDisabled {
		t.Fatalf("records = %+v", records)
	}
	_, sent = h.send(t, call{session: "session_2", client: "codex", turn: 1, body: body, project: "astrlink"})
	if got := systemOf(sent); got != "<ctx en>\nastrlink: v1\n</ctx>" {
		t.Fatalf("another client's system = %q", got)
	}
}

func TestPausingASessionKeepsThePluginOutOfIt(t *testing.T) {
	h := newHarness(t, allCapabilities(), map[string]string{"loader": loaderPlugin}, newProfiles())
	ctx := context.Background()
	turn1 := messages(user("帮我看看分页逻辑"))
	if _, sent := h.send(t, call{session: "session_1", turn: 1, body: turn1, project: "astrlink"}); systemOf(sent) == "" {
		t.Fatalf("turn 1 got no block: %s", sent)
	}
	if err := h.host.PauseSession(ctx, "loader", "session_1", true, 1); err != nil {
		t.Fatal(err)
	}
	turn2 := messages(user("帮我看看分页逻辑"), assistant("好"), user("继续"))
	run, sent := h.send(t, call{session: "session_1", turn: 2, body: turn2, project: "astrlink"})
	if string(sent) != turn2 {
		t.Fatalf("a paused session's request changed: %s", sent)
	}
	if records, _ := run.Report(); len(records) != 1 || records[0].SkippedReason != contract.PluginSkipSessionPaused {
		t.Fatalf("records = %+v", records)
	}
	parts, err := h.host.SessionPlugins(ctx, "session_1")
	if err != nil || len(parts) != 1 || !parts[0].Paused || parts[0].FirstTurn != 1 || parts[0].Block == nil {
		t.Fatalf("SessionPlugins = %+v, %v", parts, err)
	}
	// The pause is kept: a restart does not lift it.
	h.restart(t)
	turn3 := messages(user("帮我看看分页逻辑"), assistant("好"), user("继续"), assistant("好"), user("再继续"))
	if _, sent := h.send(t, call{session: "session_1", turn: 3, body: turn3, project: "astrlink"}); string(sent) != turn3 {
		t.Fatalf("the pause did not survive a restart: %s", sent)
	}
	if err := h.host.PauseSession(ctx, "loader", "session_1", false, 3); err != nil {
		t.Fatal(err)
	}
	turn4 := messages(user("帮我看看分页逻辑"), assistant("好"), user("继续"), assistant("好"), user("再继续"), assistant("好"), user("还有"))
	if _, sent := h.send(t, call{session: "session_1", turn: 4, body: turn4, project: "astrlink"}); systemOf(sent) != "<ctx en>\nastrlink: v1\n</ctx>" {
		t.Fatalf("a resumed session got %s", sent)
	}
}

func TestTheInjectionBudgetKeepsOutTheLaterPlugins(t *testing.T) {
	big := func(key string) string {
		return `export const meta = { apiVersion: 1, key: "` + key + `", name: "` + key + `", version: "1.0.0", capabilities: ["context.inject"] };
export function prepareSession() { return { system: "` + key + `:" + "x".repeat(3000) }; }
`
	}
	h := newHarness(t, allCapabilities(), map[string]string{"alpha": big("alpha"), "beta": big("beta")}, nil)
	if _, err := h.host.UpdateSettings(context.Background(), contract.PluginHostSettingsUpdate{InjectionBudgetBytes: ptr(contract.MinPluginInjectionBudgetBytes)}); err != nil {
		t.Fatal(err)
	}
	body := messages(user("帮我看看分页逻辑"))
	run, sent := h.send(t, call{session: "session_1", turn: 1, body: body})
	if system := systemOf(sent); !strings.HasPrefix(system, "alpha:") || strings.Contains(system, "beta:") {
		t.Fatalf("system = %.40q…", system)
	}
	records, _ := run.Report()
	for _, record := range records {
		switch record.Plugin {
		case "alpha":
			if record.Injection == nil || record.SkippedReason != "" {
				t.Fatalf("alpha = %+v", record)
			}
		case "beta":
			if record.Injection != nil || record.Status != contract.PluginRecordSkipped || record.SkippedReason != contract.PluginSkipOverBudget {
				t.Fatalf("beta = %+v", record)
			}
		}
	}
	if len(records) != 2 {
		t.Fatalf("records = %+v", records)
	}
}

func TestASessionKeepsTheLanguageItStartedWith(t *testing.T) {
	h := newHarness(t, allCapabilities(), map[string]string{"loader": loaderPlugin}, newProfiles())
	ctx := context.Background()
	if _, err := h.host.UpdateSettings(ctx, contract.PluginHostSettingsUpdate{Language: ptr(contract.PluginLanguageJapanese)}); err != nil {
		t.Fatal(err)
	}
	turn1 := messages(user("帮我看看分页逻辑"))
	if _, sent := h.send(t, call{session: "session_1", turn: 1, body: turn1, project: "astrlink"}); systemOf(sent) != "<ctx ja>\nastrlink: v1\n</ctx>" {
		t.Fatalf("system = %q", systemOf(sent))
	}
	if _, err := h.host.UpdateSettings(ctx, contract.PluginHostSettingsUpdate{Language: ptr(contract.PluginLanguageChinese)}); err != nil {
		t.Fatal(err)
	}
	turn2 := messages(user("帮我看看分页逻辑"), assistant("好"), user("继续"))
	if _, sent := h.send(t, call{session: "session_1", turn: 2, body: turn2, project: "astrlink"}); systemOf(sent) != "<ctx ja>\nastrlink: v1\n</ctx>" {
		t.Fatalf("a running session changed language: %q", systemOf(sent))
	}
	if _, sent := h.send(t, call{session: "session_2", turn: 1, body: turn1, project: "astrlink"}); systemOf(sent) != "<ctx zh-CN>\nastrlink: v1\n</ctx>" {
		t.Fatalf("a new session's system = %q", systemOf(sent))
	}
	if _, err := h.host.UpdateSettings(ctx, contract.PluginHostSettingsUpdate{Language: ptr("fr")}); err == nil {
		t.Fatal("an unknown language was taken")
	}
}

func TestTheAskComesOnlyInTheFirstTurnThePluginSees(t *testing.T) {
	h := newHarness(t, allCapabilities(), map[string]string{"loader": loaderPlugin}, newProfiles())
	system := "You are a coding agent.\n<env>\nWorking directory: /work/astrlink/\nPlatform: linux\n</env>"
	turn1 := withSystem(system, user("帮我看看分页逻辑"))
	run, sent := h.send(t, call{session: "session_1", turn: 1, body: turn1})
	if got := gjson.GetBytes(sent, "messages.0.content.0.text").Str; got != "ASK en in astrlink" {
		t.Fatalf("turn 1 = %q in %s", got, sent)
	}
	if got := gjson.GetBytes(sent, "system").Str; got != system {
		t.Fatalf("the ask went into the system prompt: %q", got)
	}
	run.Report()
	// The turn's later calls put the same ask back where it was.
	call2 := withSystem(system, user("帮我看看分页逻辑"), toolRound)
	if _, again := h.send(t, call{session: "session_1", turn: 1, body: call2}); gjson.GetBytes(again, "messages.0").Raw != gjson.GetBytes(sent, "messages.0").Raw {
		t.Fatalf("the ask moved: %s", again)
	}
	turn2 := withSystem(system, user("帮我看看分页逻辑"), assistant("好"), user("继续"))
	run, sent = h.send(t, call{session: "session_1", turn: 2, body: turn2})
	if gjson.GetBytes(sent, "messages.2.content.#").Int() != 1 {
		t.Fatalf("turn 2 was asked again: %s", sent)
	}
	if records, _ := run.Report(); recordOf(t, records, plugin.HookPrepareTurn).SkippedReason != "no_ask" {
		t.Fatalf("records = %+v", records)
	}
	// A restart forgets the ask but not the turn it was in.
	h.restart(t)
	turn3 := withSystem(system, user("帮我看看分页逻辑"), assistant("好"), user("继续"), assistant("好"), user("再继续"))
	if _, sent := h.send(t, call{session: "session_1", turn: 3, body: turn3}); gjson.GetBytes(sent, "messages.4.content.#").Int() != 1 {
		t.Fatalf("turn 3 after a restart was asked: %s", sent)
	}
	// A session the plugin joins late is asked in the first turn it sees.
	late := withSystem(system, user("a"), assistant("b"), user("c"), assistant("d"), user("e"))
	if _, sent := h.send(t, call{session: "session_2", turn: 3, body: late}); gjson.GetBytes(sent, "messages.4.content.0.text").Str != "ASK en in astrlink" {
		t.Fatalf("a session joined late was not asked: %s", sent)
	}
}

func TestAnAnswerNamingAnOfferedProjectBindsAtOnce(t *testing.T) {
	h := newHarness(t, allCapabilities(), map[string]string{"loader": loaderPlugin}, newProfiles())
	turn1 := messages(user("帮我看看分页逻辑"))
	run, _ := h.send(t, call{session: "session_1", turn: 1, body: turn1})
	run.Report()
	// A number is the model's to read: it may answer another question.
	numbered := messages(user("帮我看看分页逻辑"), assistant("要纳入哪个项目？"), user("2"))
	if run, _ := h.send(t, call{session: "session_1", turn: 2, body: numbered}); run.Metadata().Values != nil {
		t.Fatalf("a number bound the session: %+v", run.Metadata())
	}
	answered := messages(user("帮我看看分页逻辑"), assistant("要纳入哪个项目？"), user("2"), assistant("哪一个？"), user(" Paseo "))
	run, sent := h.send(t, call{session: "session_1", turn: 3, body: answered})
	value := run.Metadata().Values["project"]
	if value.Value != "paseo" || value.SetBy != contract.MetadataSetByUserAnswer || value.Turn != 3 {
		t.Fatalf("metadata = %+v", run.Metadata())
	}
	if got := systemOf(sent); got != "<ctx en>\npaseo: p1\n</ctx>" {
		t.Fatalf("the answer's own request got %q", got)
	}
	// The session's block was computed, empty, before it had a project.
	records, _ := run.Report()
	if record := recordOf(t, records, plugin.HookPrepareSession); record.Trigger != contract.PluginTriggerBound || record.Summary != contract.PluginTriggerBound {
		t.Fatalf("prepareSession record = %+v", record)
	}
	if events := run.Metadata().Events; len(events) != 1 || events[0].SetBy != contract.MetadataSetByUserAnswer || events[0].New {
		t.Fatalf("events = %+v", events)
	}
}

func TestANewSessionTakesTheProjectItsWorkspaceHad(t *testing.T) {
	h := newHarness(t, allCapabilities(), map[string]string{"loader": loaderPlugin}, newProfiles())
	here := "<env>\nPrimary working directory: /work/astrlink\n</env>"
	turn1 := withSystem(here, user("帮我看看分页逻辑"))
	run, _ := h.send(t, call{session: "session_1", turn: 1, body: turn1})
	scanner := run.NewMarkerScanner()
	scanner.Write("<memo-project>astrlink</memo-project>\n好")
	if events := run.Finish(context.Background(), scanner.Close(), true); len(events) != 1 || events[0].Value != "astrlink" {
		t.Fatalf("Finish = %+v", events)
	}
	// A new session in the same directory starts with the project: its
	// first request already carries the block, and nobody asks.
	run, sent := h.send(t, call{session: "session_2", turn: 1, body: turn1})
	if value := run.Metadata().Values["project"]; value.Value != "astrlink" || value.SetBy != contract.MetadataSetByWorkspace {
		t.Fatalf("metadata = %+v", run.Metadata())
	}
	if got := systemOf(sent); !strings.HasSuffix(got, "<ctx en>\nastrlink: v1\n</ctx>") {
		t.Fatalf("system = %q", got)
	}
	if gjson.GetBytes(sent, "messages.0.content.#").Int() != 1 {
		t.Fatalf("a session bound by its workspace was asked: %s", sent)
	}
	if name, ok := h.host.SessionWorkspace("session_2"); !ok || name != "astrlink" {
		t.Fatalf("SessionWorkspace = %q, %v", name, ok)
	}
	// Another directory, or a client that wraps it otherwise, starts
	// unbound.
	elsewhere := withSystem("<env>\nWorking directory: /work/paseo\n</env>", user("帮我看看分页逻辑"))
	if run, _ := h.send(t, call{session: "session_3", turn: 1, body: elsewhere}); run.Metadata().Values != nil {
		t.Fatalf("another directory was bound: %+v", run.Metadata())
	}
	// Codex names its directory in its environment context.
	codex := withSystem("", user("<environment_context>\n  <cwd>/work/astrlink</cwd>\n</environment_context>"), user("改分页"))
	if run, _ := h.send(t, call{session: "session_4", turn: 1, body: codex}); run.Metadata().Values["project"].Value != "astrlink" {
		t.Fatalf("codex's directory was not read: %+v", run.Metadata())
	}
	// The user's choice moves the directory; a marker alone does not.
	if bound, err := h.host.BindWorkspace(context.Background(), "session_2", "project", "paseo", false); err != nil || bound {
		t.Fatalf("BindWorkspace without replace = %v, %v", bound, err)
	}
	if bound, err := h.host.BindWorkspace(context.Background(), "session_2", "project", "paseo", true); err != nil || !bound {
		t.Fatalf("BindWorkspace = %v, %v", bound, err)
	}
	if value, ok, err := h.host.WorkspaceValue(context.Background(), "session_2", "project"); err != nil || !ok || value != "paseo" {
		t.Fatalf("WorkspaceValue = %q, %v, %v", value, ok, err)
	}
}

func TestWorkspacesBindNothingWhenThePluginSaysNo(t *testing.T) {
	settings := allCapabilities()
	settings.WorkspaceBinding = false
	h := newHarness(t, settings, map[string]string{"loader": loaderPlugin}, newProfiles())
	here := withSystem("<env>\nWorking directory: /work/astrlink\n</env>", user("帮我看看分页逻辑"))
	run, _ := h.send(t, call{session: "session_1", turn: 1, body: here})
	scanner := run.NewMarkerScanner()
	scanner.Write("<memo-project>astrlink</memo-project>")
	run.Finish(context.Background(), scanner.Close(), true)
	if run, _ := h.send(t, call{session: "session_2", turn: 1, body: here}); run.Metadata().Values != nil {
		t.Fatalf("a workspace bound a session against the setting: %+v", run.Metadata())
	}
}

func TestMarkersJoinAKnownProjectWhateverTheirCase(t *testing.T) {
	h := newHarness(t, allCapabilities(), map[string]string{"loader": loaderPlugin}, newProfiles())
	bind := func(session, marker string) []contract.MetadataEvent {
		t.Helper()
		run, _ := h.send(t, call{session: session, turn: 1, body: messages(user("帮我看看分页逻辑"))})
		run.Report()
		scanner := run.NewMarkerScanner()
		scanner.Write("<memo-project>" + marker + "</memo-project>")
		return run.Finish(context.Background(), scanner.Close(), true)
	}
	// The first turn offered astrlink: picking it starts no new project.
	if events := bind("session_1", "astrlink"); len(events) != 1 || events[0].New {
		t.Fatalf("first binding = %+v", events)
	}
	for index, marker := range []string{"AstrLink", "astr_link", "Astr-Link"} {
		events := bind("session_"+string(rune('2'+index)), marker)
		if len(events) != 1 || events[0].Value != "astrlink" || events[0].Normalized != marker || events[0].New || events[0].Event != contract.MetadataEventBound {
			t.Fatalf("%s = %+v", marker, events)
		}
	}
	// A name nobody used yet keeps its words, in lowercase.
	if events := bind("session_9", "Brand-New"); len(events) != 1 || events[0].Value != "brand-new" || events[0].Normalized != "Brand-New" || !events[0].New {
		t.Fatalf("a new name = %+v", events)
	}
	// The offered names count as known even before any session used them.
	if events := bind("session_10", "PASEO"); len(events) != 1 || events[0].Value != "paseo" || events[0].New {
		t.Fatalf("an offered name = %+v", events)
	}
}

func TestACompactedHistoryRefreshesTheBlockOnlyInTime(t *testing.T) {
	server := newProfiles()
	h := newHarness(t, allCapabilities(), map[string]string{"loader": loaderPlugin}, server)
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	h.host.now = func() time.Time { return now }
	main := "main agent"
	turn1 := withSystem(main, user("帮我看看分页逻辑"))
	h.send(t, call{session: "session_1", turn: 1, body: turn1, project: "astrlink"})
	server.set("astrlink", "v2")
	// Going back to an earlier message keeps the first one: no refresh.
	edited := withSystem(main, user("帮我看看分页逻辑"), assistant("好"), user("换个说法"))
	if _, sent := h.send(t, call{session: "session_1", turn: 2, body: edited, project: "astrlink"}); systemOf(sent) != main+"\n\n<ctx en>\nastrlink: v1\n</ctx>" {
		t.Fatalf("an edit refreshed the block: %q", systemOf(sent))
	}
	// A subagent shares the session with a prompt and opening of its own.
	subagent := withSystem("explore agent", user("找出分页代码"))
	if _, sent := h.send(t, call{session: "session_1", turn: 3, body: subagent, project: "astrlink"}); !strings.HasSuffix(systemOf(sent), "astrlink: v1\n</ctx>") {
		t.Fatalf("a subagent refreshed the block: %q", systemOf(sent))
	}
	compacted := withSystem(main, user("This session continues an earlier one. Summary: ..."), user("继续"))
	run, sent := h.send(t, call{session: "session_1", turn: 4, body: compacted, project: "astrlink"})
	if got := systemOf(sent); got != main+"\n\n<ctx en>\nastrlink: v2\n</ctx>" {
		t.Fatalf("a compacted history kept the old block: %q", got)
	}
	records, _ := run.Report()
	if record := recordOf(t, records, plugin.HookPrepareSession); record.Trigger != contract.PluginTriggerCompact || len(record.Actions) != 1 {
		t.Fatalf("record = %+v", record)
	}
	// Within ten minutes another compaction keeps the block.
	server.set("astrlink", "v3")
	again := withSystem(main, user("Summary again"), user("继续"))
	if _, sent := h.send(t, call{session: "session_1", turn: 5, body: again, project: "astrlink"}); !strings.HasSuffix(systemOf(sent), "astrlink: v2\n</ctx>") {
		t.Fatalf("a second compaction within ten minutes refreshed: %q", systemOf(sent))
	}
	// A refresh that does not come in time is dropped, not kept for later.
	now = now.Add(11 * time.Minute)
	hold := make(chan struct{})
	server.holding(hold)
	h.host.budget = 100 * time.Millisecond
	late := withSystem(main, user("Summary three"), user("继续"))
	run, sent = h.send(t, call{session: "session_1", turn: 6, body: late, project: "astrlink"})
	if !strings.HasSuffix(systemOf(sent), "astrlink: v2\n</ctx>") {
		t.Fatalf("a late refresh changed the block: %q", systemOf(sent))
	}
	refresh := run.parts[0].sessionCall
	close(hold)
	<-refresh.done
	next := withSystem(main, user("Summary three"), user("继续"), toolRound)
	if _, sent := h.send(t, call{session: "session_1", turn: 6, body: next, project: "astrlink"}); !strings.HasSuffix(systemOf(sent), "astrlink: v2\n</ctx>") {
		t.Fatalf("a late refresh was kept: %q", systemOf(sent))
	}
}

func TestARefreshComputesTheBlockOnTheNextRequest(t *testing.T) {
	server := newProfiles()
	h := newHarness(t, allCapabilities(), map[string]string{"loader": loaderPlugin}, server)
	ctx := context.Background()
	turn1 := messages(user("帮我看看分页逻辑"))
	h.send(t, call{session: "session_1", turn: 1, body: turn1, project: "astrlink"})
	server.set("astrlink", "v2")
	target := h.manager.Current().Plugin("loader")
	result := h.host.Action(ctx, target, contract.PluginActionInput{Action: "refresh", Session: "session_1"})
	if result.Status != contract.PluginTestOK || !result.Refresh || result.Message != "refresh session_1" {
		t.Fatalf("Action = %+v", result)
	}
	if parts, _ := h.host.SessionPlugins(ctx, "session_1"); len(parts) != 1 || !parts[0].RefreshPending || parts[0].Block.Trigger != contract.PluginTriggerStart {
		t.Fatalf("SessionPlugins = %+v", parts)
	}
	// Even a later call of the same turn takes the new block.
	call2 := messages(user("帮我看看分页逻辑"), toolRound)
	run, sent := h.send(t, call{session: "session_1", turn: 1, body: call2, project: "astrlink"})
	if got := systemOf(sent); got != "<ctx en>\nastrlink: v2\n</ctx>" {
		t.Fatalf("system = %q", got)
	}
	records, _ := run.Report()
	if record := recordOf(t, records, plugin.HookPrepareSession); record.Trigger != contract.PluginTriggerManual || record.Summary != contract.PluginTriggerManual {
		t.Fatalf("record = %+v", record)
	}
	if parts, _ := h.host.SessionPlugins(ctx, "session_1"); parts[0].RefreshPending || parts[0].Block.Text != "<ctx en>\nastrlink: v2\n</ctx>" || parts[0].Block.Trigger != contract.PluginTriggerManual {
		t.Fatalf("SessionPlugins = %+v", parts)
	}
	if result := h.host.Action(ctx, target, contract.PluginActionInput{Action: "other", Session: "session_1"}); result.Refresh {
		t.Fatalf("Action = %+v", result)
	}
}

func TestAutomaticRefreshSwapsOnlyAChangedBlock(t *testing.T) {
	server := newProfiles()
	settings := allCapabilities()
	settings.AutoRefresh = true
	h := newHarness(t, settings, map[string]string{"loader": loaderPlugin}, server)
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	h.host.now = func() time.Time { return now }
	history := []string{user("帮我看看分页逻辑")}
	turn := func(index int) (*Run, []byte) {
		t.Helper()
		if index > 1 {
			history = append(history, assistant("好"), user("继续 "+string(rune('0'+index))))
		}
		return h.send(t, call{session: "session_1", turn: index, body: messages(history...), project: "astrlink"})
	}
	turn(1)
	// Within ten minutes nothing is checked.
	before := server.requests()
	turn(2)
	if server.requests() != before {
		t.Fatal("the block was checked within ten minutes")
	}
	// Unchanged data keeps the block and says nothing.
	now = now.Add(11 * time.Minute)
	run, _ := turn(3)
	<-run.parts[0].sessionCall.done
	records, _ := run.Report()
	if record := recordOf(t, records, plugin.HookPrepareSession); record.Trigger != "" {
		t.Fatalf("an unchanged check reported %+v", record)
	}
	// Changed data swaps the block once the check comes back; the request
	// that started the check does not wait for it.
	server.set("astrlink", "v2")
	now = now.Add(11 * time.Minute)
	hold := make(chan struct{})
	server.holding(hold)
	run, sent := turn(4)
	if !strings.HasSuffix(systemOf(sent), "v1\n</ctx>") {
		t.Fatalf("the request waited for the check: %q", systemOf(sent))
	}
	close(hold)
	<-run.parts[0].sessionCall.done
	run, sent = h.send(t, call{session: "session_1", turn: 4, body: messages(append(history, toolRound)...), project: "astrlink"})
	if !strings.HasSuffix(systemOf(sent), "v2\n</ctx>") {
		t.Fatalf("the changed block did not go in: %q", systemOf(sent))
	}
	records, _ = run.Report()
	if record := recordOf(t, records, plugin.HookPrepareSession); record.Trigger != contract.PluginTriggerAuto || record.Summary != contract.PluginTriggerAuto {
		t.Fatalf("record = %+v", record)
	}
}

func TestALateBlockIsReportedByTheRequestThatCarriesIt(t *testing.T) {
	server := newProfiles()
	h := newHarness(t, allCapabilities(), map[string]string{"loader": loaderPlugin}, server)
	hold := make(chan struct{})
	server.holding(hold)
	h.host.budget = 100 * time.Millisecond
	turn1 := messages(user("帮我看看分页逻辑"))
	run, sent := h.send(t, call{session: "session_1", turn: 1, body: turn1, project: "astrlink"})
	if string(sent) != turn1 {
		t.Fatalf("a late block went in: %s", sent)
	}
	records, _ := run.Report()
	if record := recordOf(t, records, plugin.HookPrepareSession); record.SkippedReason != contract.PluginSkipTimeout || record.Trigger != contract.PluginTriggerStart {
		t.Fatalf("record = %+v", record)
	}
	// A call of the turn while the block is still computed waits for that
	// computation instead of starting another.
	call2 := messages(user("帮我看看分页逻辑"), toolRound)
	run, _ = h.send(t, call{session: "session_1", turn: 1, body: call2, project: "astrlink"})
	if run.parts[0].sessionCall == nil || server.requests() != 1 {
		t.Fatalf("a second computation started: %d requests", server.requests())
	}
	pending := run.parts[0].sessionCall
	run.Report()
	close(hold)
	<-pending.done
	call3 := messages(user("帮我看看分页逻辑"), toolRound, toolRound)
	run, sent = h.send(t, call{session: "session_1", turn: 1, body: call3, project: "astrlink"})
	if got := systemOf(sent); got != "<ctx en>\nastrlink: v1\n</ctx>" {
		t.Fatalf("system = %q", got)
	}
	records, _ = run.Report()
	record := recordOf(t, records, plugin.HookPrepareSession)
	if record.Trigger != contract.PluginTriggerStart || record.Title != "装载" || record.Summary != contract.PluginTriggerStart || record.Injection == nil || len(record.Actions) != 1 {
		t.Fatalf("record = %+v", record)
	}
	// The request after it carries the block and says only that.
	call4 := messages(user("帮我看看分页逻辑"), toolRound, toolRound, toolRound)
	run, _ = h.send(t, call{session: "session_1", turn: 1, body: call4, project: "astrlink"})
	records, _ = run.Report()
	if record := recordOf(t, records, plugin.HookPrepareSession); record.Trigger != "" || record.Injection == nil || record.Actions[0].ID != "refresh" {
		t.Fatalf("record = %+v", record)
	}
}

func ptr[T any](value T) *T { return &value }
