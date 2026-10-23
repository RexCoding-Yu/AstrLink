package plugin

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// pluginSourceV2 is an apiVersion 2 plugin of key asking for capabilities,
// followed by body.
func pluginSourceV2(key string, capabilities []string, body string) string {
	return strings.Replace(pluginSource(key, capabilities, body), "apiVersion: 1", "apiVersion: 2", 1)
}

func TestAsyncHooksAwaitHostFunctions(t *testing.T) {
	var requests atomic.Int32
	fetcher := fetcherFunc(func(ctx context.Context, _ *Plugin, request HTTPRequest) HTTPResponse {
		requests.Add(1)
		select {
		case <-time.After(60 * time.Millisecond):
		case <-ctx.Done():
			return HTTPResponse{Error: FetchTimeout}
		}
		return HTTPResponse{Status: 200, Header: map[string]string{"content-type": "application/json"}, Body: []byte(`{"path":"` + request.URL + `"}`)}
	})
	manager := newTestManager(t, map[string]string{
		"demo": pluginSourceV2("demo", []string{CapabilityNetwork, CapabilityStorage}, `
export async function prepareTurn() {
  await astrlink.store.set("seen", { count: 1 });
  const stored = await astrlink.store.get("seen");
  const [a, b] = await Promise.all([
    astrlink.http.fetch({ method: "GET", url: "https://a.example/one" }),
    astrlink.http.fetch({ method: "GET", url: "https://a.example/two" }),
  ]);
  return { stored, paths: [a.json.path, b.json.path], keys: await astrlink.store.keys() };
}
`),
	}, settingsMap{"demo": enabled(CapabilityNetwork, CapabilityStorage)}, Services{Fetcher: fetcher, Store: &memoryStore{}}, Limits{})
	started := time.Now()
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{NetworkBudget: time.Second})
	if outcome.err != nil {
		t.Fatalf("call: %v", outcome.err)
	}
	if elapsed := time.Since(started); elapsed > 110*time.Millisecond {
		t.Fatalf("two awaited requests took %s, want them in parallel", elapsed)
	}
	var result struct {
		Stored struct{ Count int } `json:"stored"`
		Paths  []string            `json:"paths"`
		Keys   []string            `json:"keys"`
	}
	if err := json.Unmarshal(outcome.result, &result); err != nil {
		t.Fatalf("result %s: %v", outcome.result, err)
	}
	if result.Stored.Count != 1 || strings.Join(result.Paths, ",") != "https://a.example/one,https://a.example/two" || strings.Join(result.Keys, ",") != "seen" {
		t.Fatalf("result = %s", outcome.result)
	}
	if requests.Load() != 2 || outcome.info.Network.Requests != 2 {
		t.Fatalf("requests = %d, counted %d", requests.Load(), outcome.info.Network.Requests)
	}
	// The script time leaves out the waits.
	if outcome.info.Script > 50*time.Millisecond {
		t.Fatalf("script time = %s, want the waits left out", outcome.info.Script)
	}
}

func TestFetchGlobalSpeaksTheWebAPI(t *testing.T) {
	var seen HTTPRequest
	fetcher := fetcherFunc(func(_ context.Context, _ *Plugin, request HTTPRequest) HTTPResponse {
		seen = request
		if strings.Contains(request.URL, "denied") {
			return HTTPResponse{Error: FetchDenied}
		}
		return HTTPResponse{Status: 201, Header: map[string]string{"content-type": "application/json", "x-trace": "abc"}, Body: []byte(`{"ok":true}`)}
	})
	manager := newTestManager(t, map[string]string{
		"demo": pluginSourceV2("demo", []string{CapabilityNetwork}, `
export async function prepareTurn() {
  const url = new URL("/v1/items?limit=2", "https://api.example");
  url.searchParams.set("q", "a b&c");
  const response = await fetch(url, {
    method: "POST",
    headers: { "X-Mode": "test" },
    body: new Uint8Array([1, 2, 3]),
    credential: "token",
  });
  let denied = "";
  try {
    await fetch("https://denied.example/");
  } catch (error) {
    denied = error.name + ":" + error.cause.code;
  }
  return {
    status: response.status, ok: response.ok, trace: response.headers.get("X-Trace"),
    body: await response.json(), used: response.bodyUsed, denied, href: url.href,
  };
}
`),
	}, settingsMap{"demo": enabled(CapabilityNetwork)}, Services{Fetcher: fetcher}, Limits{})
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{NetworkBudget: time.Second})
	if outcome.err != nil {
		t.Fatalf("call: %v", outcome.err)
	}
	want := `{"status":201,"ok":true,"trace":"abc","body":{"ok":true},"used":true,"denied":"TypeError:denied","href":"https://api.example/v1/items?limit=2&q=a+b%26c"}`
	if string(outcome.result) != want {
		t.Fatalf("result = %s\nwant     %s", outcome.result, want)
	}
	if seen.URL != "https://denied.example/" {
		t.Fatalf("last request = %+v", seen)
	}
}

func TestFetchSendsTheBody(t *testing.T) {
	var seen HTTPRequest
	fetcher := fetcherFunc(func(_ context.Context, _ *Plugin, request HTTPRequest) HTTPResponse {
		seen = request
		return HTTPResponse{Status: 204}
	})
	manager := newTestManager(t, map[string]string{
		"demo": pluginSourceV2("demo", []string{CapabilityNetwork}, `
export async function prepareTurn() {
  const response = await fetch("https://api.example/upload", { method: "PUT", body: new Uint8Array([0, 255, 7]), credential: "token", timeoutMs: 500 });
  return [response.status, await response.text()];
}
`),
	}, settingsMap{"demo": enabled(CapabilityNetwork)}, Services{Fetcher: fetcher}, Limits{})
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{NetworkBudget: time.Second})
	if outcome.err != nil || string(outcome.result) != `[204,""]` {
		t.Fatalf("call = %s, %v", outcome.result, outcome.err)
	}
	if seen.Method != "PUT" || string(seen.BodyBytes) != "\x00\xff\x07" || seen.Credential != "token" || seen.TimeoutMS != 500 {
		t.Fatalf("request = %+v", seen)
	}
}

func TestAsyncCallsKeepTheirBudgets(t *testing.T) {
	manager := newTestManager(t, map[string]string{
		"demo": pluginSourceV2("demo", []string{CapabilityNetwork}, `
export function prepareTurn() { return new Promise(() => {}); }
export async function prepareSession() { await new Promise((resolve) => setTimeout(resolve, 10000)); }
export async function observeTurn() { await null; for (;;) {} }
export async function describeStatus() { await null; throw new RangeError("no status"); }
export async function testConnection() {
  try { await fetch("https://api.example/"); } catch (error) { return error.cause.code; }
}
`),
	}, settingsMap{"demo": enabled(CapabilityNetwork)}, Services{Fetcher: fetcherFunc(func(context.Context, *Plugin, HTTPRequest) HTTPResponse {
		return HTTPResponse{Status: 200}
	})}, Limits{ScriptTime: 30 * time.Millisecond})
	plugin := loadedPlugin(t, manager, "demo")
	callError(t, callWithin(t, context.Background(), plugin, HookPrepareTurn, nil, CallOptions{}), ReasonPluginError, "never settles")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	callError(t, callWithin(t, ctx, plugin, HookPrepareSession, nil, CallOptions{}), ReasonTimeout, "ran out of time")
	callError(t, callWithin(t, context.Background(), plugin, HookObserveTurn, nil, CallOptions{}), ReasonTimeout, "JavaScript")
	callError(t, callWithin(t, context.Background(), plugin, HookDescribeStatus, nil, CallOptions{}), ReasonPluginError, "RangeError: no status")
	// No network budget, no request.
	outcome := callWithin(t, context.Background(), plugin, HookTestConnection, nil, CallOptions{})
	if outcome.err != nil || string(outcome.result) != `"budget"` {
		t.Fatalf("testConnection = %s, %v", outcome.result, outcome.err)
	}
}

func TestTimersRunWithinTheCall(t *testing.T) {
	manager := newTestManager(t, map[string]string{
		"demo": pluginSourceV2("demo", nil, `
export async function prepareTurn() {
  const order = [];
  const cleared = setTimeout(() => order.push("cleared"), 5);
  clearTimeout(cleared);
  let ticks = 0;
  await new Promise((resolve) => {
    const interval = setInterval(() => {
      ticks++;
      if (ticks === 3) { clearInterval(interval); resolve(); }
    }, 2);
  });
  await new Promise((resolve) => setTimeout(() => { order.push("timeout"); resolve(); }, 1, "unused"));
  queueMicrotask(() => order.push("microtask"));
  await null;
  return { order, ticks };
}
`),
	}, settingsMap{"demo": enabled()}, Services{}, Limits{})
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{})
	if outcome.err != nil || string(outcome.result) != `{"order":["timeout","microtask"],"ticks":3}` {
		t.Fatalf("call = %s, %v", outcome.result, outcome.err)
	}
}

func TestWebAPIs(t *testing.T) {
	manager := newTestManager(t, map[string]string{
		"demo": pluginSourceV2("demo", nil, `
const hex = (buffer) => [...new Uint8Array(buffer)].map((byte) => byte.toString(16).padStart(2, "0")).join("");
export async function prepareTurn() {
  const url = new URL("HTTPS://User:Pw@Example.COM:443/a/../b?x=1#frag");
  const params = new URLSearchParams({ b: "2", a: "1 2" });
  params.append("a", "3");
  params.sort();
  const relative = new URL("../c?d=é", "https://example.com/a/b/");
  const digest = hex(await crypto.subtle.digest("SHA-256", new TextEncoder().encode("abc")));
  const key = await crypto.subtle.importKey("raw", new TextEncoder().encode("key"), { name: "HMAC", hash: "SHA-256" }, false, ["sign", "verify"]);
  const mac = await crypto.subtle.sign("HMAC", key, new TextEncoder().encode("The quick brown fox jumps over the lazy dog"));
  const verified = await crypto.subtle.verify("HMAC", key, mac, new TextEncoder().encode("The quick brown fox jumps over the lazy dog"));
  const random = crypto.getRandomValues(new Uint8Array(16));
  const headers = new Headers([["Accept", "a"], ["accept", "b"]]);
  console.log("logged", { n: 1 }, [2]);
  console.error(new TypeError("broken"));
  return {
    href: url.href, origin: url.origin, host: url.host, path: url.pathname, user: url.username,
    params: params.toString(), all: params.getAll("a"), relative: relative.href,
    canParse: [URL.canParse("nope"), URL.canParse("mailto:a@b.c")],
    digest, mac: hex(mac), verified, randomLength: random.length,
    uuid: /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(crypto.randomUUID()),
    accept: headers.get("ACCEPT"), clock: typeof performance.now(),
    json: await Response.json({ a: 1 }).json(),
  };
}
`),
	}, settingsMap{"demo": enabled()}, Services{}, Limits{})
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{})
	if outcome.err != nil {
		t.Fatalf("call: %v", outcome.err)
	}
	var result map[string]any
	if err := json.Unmarshal(outcome.result, &result); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"href":         "https://User:Pw@example.com/b?x=1#frag",
		"origin":       "https://example.com",
		"host":         "example.com",
		"path":         "/b",
		"user":         "User",
		"params":       "a=1+2&a=3&b=2",
		"relative":     "https://example.com/a/c?d=%C3%A9",
		"digest":       "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		"mac":          "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8",
		"verified":     true,
		"uuid":         true,
		"accept":       "a, b",
		"clock":        "number",
		"randomLength": float64(16),
	}
	for key, value := range want {
		if result[key] != value {
			t.Errorf("%s = %#v, want %#v", key, result[key], value)
		}
	}
	if got, _ := json.Marshal(result["all"]); string(got) != `["1 2","3"]` {
		t.Errorf("all = %s", got)
	}
	if got, _ := json.Marshal(result["canParse"]); string(got) != `[false,true]` {
		t.Errorf("canParse = %s", got)
	}
	if got, _ := json.Marshal(result["json"]); string(got) != `{"a":1}` {
		t.Errorf("json = %s", got)
	}
	if len(outcome.info.Logs) != 2 || outcome.info.Logs[0].Message != `logged {"n":1} [2]` || !strings.HasPrefix(outcome.info.Logs[1].Message, "TypeError: broken") || outcome.info.Logs[1].Level != "warn" {
		t.Fatalf("logs = %+v", outcome.info.Logs)
	}
}

func TestApiVersion1StaysSynchronous(t *testing.T) {
	manager := newTestManager(t, map[string]string{
		"sync":  pluginSource("sync", nil, "export async function prepareTurn() { return 1; }"),
		"timer": pluginSource("timer", nil, "export function prepareTurn() { setTimeout(() => {}, 1); }"),
	}, settingsMap{"sync": enabled(), "timer": enabled()}, Services{}, Limits{})
	if plugin := manager.Current().Plugin("sync"); plugin.Loaded() || plugin.Err == nil || plugin.Err.Code != CodeAsync {
		t.Fatalf("sync plugin = %+v, want an async load error", plugin.Err)
	}
	callError(t, callWithin(t, context.Background(), loadedPlugin(t, manager, "timer"), HookPrepareTurn, nil, CallOptions{}), ReasonPluginError, "apiVersion 2")
}

func TestJournalKeepsLogsFailuresAndTimings(t *testing.T) {
	manager := newTestManager(t, map[string]string{
		"demo": pluginSourceV2("demo", nil, `
export async function prepareTurn(input) {
  console.info("turn", input.n);
  if (input.n === 2) throw new Error("second turn");
  return input.n;
}
`),
	}, settingsMap{"demo": enabled()}, Services{}, Limits{})
	plugin := loadedPlugin(t, manager, "demo")
	for n := 1; n <= 2; n++ {
		callWithin(t, context.Background(), plugin, HookPrepareTurn, map[string]int{"n": n}, CallOptions{})
	}
	callWithin(t, context.Background(), plugin, HookPrepareTurn, map[string]int{"n": 3}, CallOptions{DryRun: true})
	entries, timings := plugin.Journal(0)
	if len(entries) != 4 {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].Message != "turn 1" || entries[2].Level != JournalError || !strings.Contains(entries[2].Message, "second turn") || !strings.Contains(entries[2].Stack, "plugin.js:") || !entries[3].DryRun {
		t.Fatalf("entries = %+v", entries)
	}
	if len(timings) != 1 || timings[0].Hook != HookPrepareTurn || timings[0].Calls != 2 || timings[0].Failures != 1 {
		t.Fatalf("timings = %+v", timings)
	}
	later, _ := plugin.Journal(entries[1].Seq)
	if len(later) != 2 {
		t.Fatalf("entries after %d = %+v", entries[1].Seq, later)
	}
}
