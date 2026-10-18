package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/astrlink/convo"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/metadata"
	"github.com/QuantumNous/astrlink/core/internal/pluginhost"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/tidwall/gjson"
)

// beginPlugins starts the plugins' part in a request a client sent. The
// gateway's own calls, Claude Code's background requests and protocols
// without conversation turns run none.
func (handler *Handler) beginPlugins(request *http.Request, classified Request, session *recordSession) {
	if handler.plugins == nil || session == nil || len(classified.body) == 0 {
		return
	}
	ctx := request.Context()
	if builtinInternalFrom(ctx) != nil || classifierInternalFrom(ctx) != nil {
		return
	}
	switch classified.Protocol {
	case contract.ProtocolAnthropicMessages, contract.ProtocolOpenAIChat,
		contract.ProtocolOpenAIResponses, contract.ProtocolGoogleGenerateContent:
	default:
		return
	}
	if claudeCodeHaikuRequest(session.clientType, classified.Model) {
		return
	}
	turn := 0
	if session.turn != nil {
		turn = session.turn.Index
	}
	accessTokenID, _ := AccessTokenIDFromContext(ctx)
	run := handler.plugins.Begin(ctx, pluginhost.Input{
		Protocol: classified.Protocol, Model: classified.Model, Client: string(session.clientType),
		Stream: classified.Streaming, Token: accessTokenID, Header: request.Header,
		Session: string(session.sessionID), Turn: turn, Body: classified.body,
		Inspect: handler.pluginInspector(session, classified, accessTokenID),
	})
	if run == nil {
		return
	}
	session.plugins = run
	session.metadata = run.Metadata()
	session.scanner.watchMarkers(run.NewMarkerScanner)
}

// pluginInspector checks the turn's text against the privacy policy before
// it leaves for a plugin, as for an upstream nobody trusts: allow and warn
// pass the text, redact passes it redacted with the placeholders the
// upstream sees, block passes nothing. nil when no policy can apply.
func (handler *Handler) pluginInspector(session *recordSession, classified Request, accessTokenID contract.AccessTokenID) func(context.Context, []string) ([]string, error) {
	filter := handler.privacyFilter
	if filter == nil {
		return nil
	}
	protocol, model, conversation := classified.Protocol, classified.Model, string(session.sessionID)
	return func(ctx context.Context, texts []string) ([]string, error) {
		policy, err := filter.ResolvePolicy(ctx, privacy.Scope{Protocol: protocol, Model: model, AccessTokenID: accessTokenID})
		if err != nil {
			return nil, err
		}
		if !policy.Enabled {
			return texts, nil
		}
		body, paths, err := pluginSampleBody(protocol, texts)
		if err != nil {
			return nil, err
		}
		result, err := filter.Inspect(privacy.WithConversation(ctx, conversation), policy, protocol, body)
		if err != nil {
			return nil, err
		}
		switch result.Decision {
		case privacy.DecisionAllow, privacy.DecisionWarn:
			return texts, nil
		case privacy.DecisionRedact:
			checked := make([]string, len(paths))
			for index, path := range paths {
				value := gjson.GetBytes(result.Body, path)
				if value.Type != gjson.String {
					return nil, errors.New("the redacted text lost its place")
				}
				checked[index] = value.Str
			}
			return checked, nil
		case privacy.DecisionBlock:
			return nil, pluginhost.ErrBlocked
		default:
			return nil, errors.New("unknown privacy decision")
		}
	}
}

// pluginSampleBody wraps texts as user messages of a request in protocol,
// the smallest body the privacy policy inspects, and returns where each
// text sits in it.
func pluginSampleBody(protocol contract.ProtocolID, texts []string) ([]byte, []string, error) {
	messages := make([]any, len(texts))
	paths := make([]string, len(texts))
	field := "messages"
	for index, text := range texts {
		at := strconv.Itoa(index)
		switch protocol {
		case contract.ProtocolGoogleGenerateContent:
			field = "contents"
			messages[index] = map[string]any{"role": "user", "parts": []map[string]string{{"text": text}}}
			paths[index] = "contents." + at + ".parts.0.text"
		case contract.ProtocolOpenAIResponses:
			field = "input"
			messages[index] = map[string]string{"role": "user", "content": text}
			paths[index] = "input." + at + ".content"
		default:
			messages[index] = map[string]string{"role": "user", "content": text}
			paths[index] = "messages." + at + ".content"
		}
	}
	body, err := json.Marshal(map[string]any{field: messages})
	return body, paths, err
}

// injectPlugins puts the plugins' blocks into the body every attempt sends,
// before the attempt loop, so retries and failover send the same bytes.
func (session *recordSession) injectPlugins(body *requestBodySource) {
	if session == nil || session.plugins == nil {
		return
	}
	if body.Replayable() {
		if data, err := body.bytes(); err == nil {
			if injected := session.plugins.Inject(data); len(injected) != len(data) {
				body.rewrite(injected)
			}
		}
	}
	session.notePlugins(session.plugins.Report())
}

// finishPlugins binds the session to the marker lines of the reply and
// keeps the plugins' records with the request.
func (session *recordSession) finishPlugins(ctx context.Context) {
	if session == nil || session.plugins == nil {
		return
	}
	succeeded := session.status == contract.RequestStatusSucceeded && (!session.hasHTTPStatus || session.httpStatus < http.StatusBadRequest)
	events := session.plugins.Finish(ctx, session.scanner.markerHits(), succeeded)
	if len(events) > 0 {
		session.metadata.Events = append(session.metadata.Events, events...)
		if len(session.metadata.Events) > contract.MaxRequestMetadataEvents {
			session.metadata.Events = session.metadata.Events[:contract.MaxRequestMetadataEvents]
		}
	}
	session.notePlugins(session.plugins.Report())
}

// notePlugins keeps the plugins' records and their events in the gateway
// lane.
func (session *recordSession) notePlugins(records []contract.RequestPluginRecord, events []contract.RequestEvent) {
	session.pluginRecords = append(session.pluginRecords, records...)
	if len(session.pluginRecords) > contract.MaxRequestPluginRecords {
		session.pluginRecords = session.pluginRecords[:contract.MaxRequestPluginRecords]
	}
	for _, event := range events {
		event.AttemptIndex = session.attemptIndex
		session.events = append(session.events, event)
	}
}

// replyMarkers reads the marker lines in the visible text of a reply.
type replyMarkers struct {
	newScanner func() *metadata.MarkerScanner
	scanner    *metadata.MarkerScanner
	parser     convo.ResponseParser
}

// watchMarkers makes the scanner read marker lines in the reply, afresh on
// each attempt.
func (scanner *usageScanner) watchMarkers(newScanner func() *metadata.MarkerScanner) {
	if scanner == nil || newScanner == nil {
		return
	}
	convoProto, ok := convoProtocol(scanner.protocol)
	if !ok {
		return
	}
	adapter, ok := convo.DefaultRegistry().Lookup(convoProto)
	if !ok {
		return
	}
	markers := &replyMarkers{newScanner: newScanner, scanner: newScanner(), parser: adapter.NewResponseParser(scanner.streaming)}
	if markers.scanner == nil {
		return
	}
	scanner.markers = markers
}

// markerHits returns the marker lines of the reply.
func (scanner *usageScanner) markerHits() []metadata.MarkerHit {
	if scanner == nil || scanner.markers == nil {
		return nil
	}
	// A whole body is parsed on demand.
	scanner.Usage()
	return scanner.markers.scanner.Close()
}

// observe feeds the reply's visible text to the marker scanner.
func (markers *replyMarkers) observe(document map[string]json.RawMessage) {
	markers.parser.Observe(document, markerSink{markers.scanner})
}

// markerSink passes a reply's visible text to a marker scanner. A new
// message starts a new line.
type markerSink struct{ scanner *metadata.MarkerScanner }

func (markerSink) OutputID(string) {}

func (markerSink) EchoID(string) {}

func (sink markerSink) AssistantText(text string) { sink.scanner.Write(text) }

func (sink markerSink) ResetAssistantText() { sink.scanner.Write("\n") }
