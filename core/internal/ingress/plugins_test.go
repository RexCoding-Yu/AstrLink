package ingress

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/metadata"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/pluginhost"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
	"github.com/QuantumNous/astrlink/core/internal/transport"
	"github.com/tidwall/gjson"
)

// pluginTestSource asks for a project until the session has one, then keeps
// a snapshot of it in the system prompt and recalls the typed text.
const pluginTestSource = `export const meta = {
  apiVersion: 1, key: "memo", name: "Memo", version: "1.0.0",
  capabilities: ["context.inject", "request.read"],
  metadata: [{ key: "project", label: "Project", pattern: "^[a-z0-9-]{1,32}$", marker: "memo-project" }],
};
export function prepareSession(view) {
  const project = view.meta.project;
  if (project === undefined) return { system: "ASK" };
  return { system: { open: "<ctx>", items: ["snapshot of " + project.value], close: "</ctx>" } };
}
export function prepareTurn(turn) {
  if (turn.meta.project === undefined) return { skip: "no_project" };
  return { context: { open: "<mem>", items: ["recall: " + turn.user.typed], close: "</mem>" } };
}
`

type pluginSettings map[string]plugin.Settings

func (settings pluginSettings) PluginSettings(context.Context) (map[string]plugin.Settings, error) {
	return settings, nil
}

type pluginGateway struct {
	t        *testing.T
	handler  *Handler
	store    *memoryRequestRecordStore
	path     string
	mutex    sync.Mutex
	sent     [][]byte
	encoding []string
	reply    string
	header   http.Header
}

func newPluginGateway(t *testing.T, protocol contract.ProtocolID, path string, streaming bool) *pluginGateway {
	t.Helper()
	ctx := context.Background()
	bindings, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bindings.Close() })
	manager := plugin.NewManager(plugin.ManagerOptions{
		Builtin:  fstest.MapFS{"memo/plugin.js": &fstest.MapFile{Data: []byte(pluginTestSource)}},
		Settings: pluginSettings{"memo": {Enabled: true, Capabilities: []string{plugin.CapabilityInject, plugin.CapabilityRequest}}},
	})
	if err := manager.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	registry := metadata.NewRegistry(bindings)
	if err := registry.RegisterPlugin(ctx, "memo", manager.Current().Plugin("memo").Attributes()); err != nil {
		t.Fatal(err)
	}
	host := pluginhost.New(pluginhost.Options{Plugins: manager, Metadata: registry, Bindings: bindings, Logf: t.Logf})
	t.Cleanup(host.Close)
	gateway := &pluginGateway{t: t, store: &memoryRequestRecordStore{}, path: path, header: http.Header{}}
	gateway.handler = NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{{
			Endpoint: validEndpoint(protocol, streaming), UpstreamModel: "upstream-model",
		}}},
		RequestRecords: gateway.store,
		AuditBlobs:     &memoryAuditBlobs{},
		Metadata:       registry,
		Plugins:        host,
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, request *http.Request, _ transport.Target) error {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			if request.ContentLength != int64(len(body)) {
				t.Errorf("the request declares %d bytes and carries %d", request.ContentLength, len(body))
			}
			gateway.mutex.Lock()
			gateway.sent = append(gateway.sent, body)
			gateway.encoding = append(gateway.encoding, request.Header.Get("Accept-Encoding"))
			gateway.mutex.Unlock()
			contentType := "application/json"
			if strings.HasPrefix(gateway.reply, "data:") {
				contentType = "text/event-stream"
			}
			writer.Header().Set("Content-Type", contentType)
			writer.WriteHeader(http.StatusOK)
			_, err = writer.Write([]byte(gateway.reply))
			return err
		}),
	})
	return gateway
}

// serve sends body and returns what went upstream and the request's record.
func (gateway *pluginGateway) serve(body, reply string) ([]byte, contract.RequestRecord) {
	gateway.t.Helper()
	gateway.reply = reply
	request := httptest.NewRequest(http.MethodPost, gateway.path, strings.NewReader(body))
	request.Header = gateway.header.Clone()
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept-Encoding", "gzip")
	recorder := httptest.NewRecorder()
	gateway.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		gateway.t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	gateway.mutex.Lock()
	defer gateway.mutex.Unlock()
	if gateway.encoding[len(gateway.encoding)-1] != "" {
		gateway.t.Fatalf("a request with plugins asked upstream for a compressed reply")
	}
	record := latestRoot(gateway.t, gateway.store)
	if err := record.Validate(); err != nil {
		gateway.t.Fatalf("the record does not validate: %v", err)
	}
	return gateway.sent[len(gateway.sent)-1], record
}

func pluginRecord(t *testing.T, record contract.RequestRecord, hook string) contract.RequestPluginRecord {
	t.Helper()
	for _, entry := range record.PluginRecords {
		if entry.Hook == hook {
			return entry
		}
	}
	t.Fatalf("no %s record in %+v", hook, record.PluginRecords)
	return contract.RequestPluginRecord{}
}

func hasPluginEvent(record contract.RequestRecord) bool {
	for _, event := range record.Events {
		if event.Kind == contract.RequestEventPlugin && event.Plugin == "memo" {
			return true
		}
	}
	return false
}

func TestPluginsAskBindAndRecallThroughTheGateway(t *testing.T) {
	gateway := newPluginGateway(t, contract.ProtocolAnthropicMessages, "/v1/messages", false)
	reply := func(text string) string {
		encoded, _ := json.Marshal(text)
		return `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":` + string(encoded) + `}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":5}}`
	}
	answer := "<memo-project>astrlink</memo-project>\n" + longAssistantReply
	sent, record := gateway.serve(
		`{"model":"claude-sonnet-5","max_tokens":64,"messages":[{"role":"user","content":"帮我看看分页逻辑"}]}`,
		reply(answer),
	)
	if gjson.GetBytes(sent, "system").Str != "ASK" || gjson.GetBytes(sent, "messages.0.content").Str != "帮我看看分页逻辑" {
		t.Fatalf("turn 1 sent %s", sent)
	}
	if entry := pluginRecord(t, record, plugin.HookPrepareTurn); entry.SkippedReason != "no_project" {
		t.Fatalf("prepareTurn record = %+v", entry)
	}
	if !hasPluginEvent(record) {
		t.Fatalf("no plugin event in %+v", record.Events)
	}
	if record.Metadata == nil || len(record.Metadata.Events) != 1 || record.Metadata.Events[0].Event != contract.MetadataEventBound {
		t.Fatalf("metadata = %+v", record.Metadata)
	}

	sent, record = gateway.serve(
		`{"model":"claude-sonnet-5","max_tokens":64,"messages":[{"role":"user","content":"帮我看看分页逻辑"},{"role":"assistant","content":[{"type":"text","text":`+jsonText(answer)+`}]},{"role":"user","content":[{"type":"text","text":"再补一个测试"}]}]}`,
		reply("好"),
	)
	if record.TurnIndex == nil || *record.TurnIndex != 2 {
		t.Fatalf("turn = %v", record.TurnIndex)
	}
	if got := gjson.GetBytes(sent, "system").Str; got != "<ctx>\nsnapshot of astrlink\n</ctx>" {
		t.Fatalf("system = %q", got)
	}
	if got := gjson.GetBytes(sent, "messages.2.content.0.text").Str; got != "<mem>\nrecall: 再补一个测试\n</mem>" {
		t.Fatalf("turn block = %q in %s", got, sent)
	}
	if value := record.Metadata.Values["project"]; value.Value != "astrlink" || value.Source != contract.MetadataSourceSession || value.SetBy != contract.MetadataSetByModelMarker {
		t.Fatalf("metadata = %+v", record.Metadata)
	}
	if entry := pluginRecord(t, record, plugin.HookPrepareTurn); entry.Injection == nil || entry.Injection.Placement != contract.PluginPlacementTurnMessage {
		t.Fatalf("prepareTurn record = %+v", entry)
	}
}

func TestPluginsReadStreamedMarkersAndBufferGemini(t *testing.T) {
	chat := newPluginGateway(t, contract.ProtocolOpenAIChat, "/v1/chat/completions", true)
	_, record := chat.serve(
		`{"model":"gpt","stream":true,"messages":[{"role":"user","content":"帮我看看分页逻辑"}]}`,
		"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"<memo-pro\"}}]}\n\n"+
			"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ject>paseo</memo-project>\\n好的\"}}]}\n\n"+
			"data: [DONE]\n\n",
	)
	if record.Metadata == nil || len(record.Metadata.Events) != 1 || record.Metadata.Events[0].Value != "paseo" {
		t.Fatalf("metadata = %+v", record.Metadata)
	}

	gemini := newPluginGateway(t, contract.ProtocolGoogleGenerateContent, "/v1beta/models/gemini-2.5-pro:generateContent", false)
	gemini.header.Set("X-AstrLink-Meta-Project", "astrlink")
	sent, record := gemini.serve(
		`{"contents":[{"role":"user","parts":[{"text":"请介绍一下 Go 语言的并发模型"}]}]}`,
		`{"responseId":"g1","candidates":[{"content":{"role":"model","parts":[{"text":"好"}]},"finishReason":"STOP"}]}`,
	)
	if got := gjson.GetBytes(sent, "contents.0.parts.0.text").Str; got != "<mem>\nrecall: 请介绍一下 Go 语言的并发模型\n</mem>" {
		t.Fatalf("gemini sent %s", sent)
	}
	if got := gjson.GetBytes(sent, "systemInstruction.parts.0.text").Str; got != "<ctx>\nsnapshot of astrlink\n</ctx>" {
		t.Fatalf("gemini system = %q", got)
	}
	if value := record.Metadata.Values["project"]; value.Source != contract.MetadataSourceHeader {
		t.Fatalf("metadata = %+v", record.Metadata)
	}
}

func jsonText(text string) string {
	encoded, _ := json.Marshal(text)
	return string(encoded)
}
