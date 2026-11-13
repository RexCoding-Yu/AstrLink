package pluginhost

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// A route with converter plugin has the plugin's hooks convert it:
// convertRequest turns the request in the route's protocol into the
// vendor's, convertResponse and convertStreamEvent turn the vendor's answer
// back. convertResponse may send one more request instead of answering, for
// a vendor that takes several: an upload first, or a job polled until it
// is done.

// Bounds of a converted route.
const (
	maxConvertedRequests = 32
	maxConvertedDelay    = 10 * time.Second
)

// convertedRequest is a request the hooks describe: convertRequest's
// result, or the next of a chain.
type convertedRequest struct {
	Body           json.RawMessage   `json:"body"`
	ContentType    string            `json:"contentType"`
	Path           string            `json:"path"`
	URL            string            `json:"url"`
	Method         string            `json:"method"`
	Headers        map[string]string `json:"headers"`
	Stream         *bool             `json:"stream"`
	State          json.RawMessage   `json:"state"`
	NativeResponse bool              `json:"nativeResponse"`
	DelayMs        int               `json:"delayMs"`
	Websocket      json.RawMessage   `json:"websocket"`
	Form           json.RawMessage   `json:"form"`
	File           json.RawMessage   `json:"file"`
}

// convertedAnswer is what convertResponse returns.
type convertedAnswer struct {
	Body        json.RawMessage   `json:"body"`
	BodyBase64  *string           `json:"bodyBase64"`
	ContentType string            `json:"contentType"`
	Next        *convertedRequest `json:"next"`
	State       json.RawMessage   `json:"state"`
	Error       *convertError     `json:"error"`
}

type convertError struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

// convertedEvents is what convertStreamEvent returns.
type convertedEvents struct {
	Events []struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	} `json:"events"`
	Body        json.RawMessage `json:"body"`
	ContentType string          `json:"contentType"`
	State       json.RawMessage `json:"state"`
	Error       *convertError   `json:"error"`
}

// relaySend is how a converted route sends each of its requests: on the
// route's address, with the key, the token and signRequest.
type relaySend struct {
	host        *Host
	relay       *relayTarget
	target      transport.Target
	roundTrip   RoundTrip
	root        *url.URL
	address     *url.URL
	baseURL     string
	key         string
	credentials http.Header
	query       url.Values
	header      http.Header
}

// send sends one request the hooks described.
func (send *relaySend) send(ctx context.Context, method string, request convertedRequest, body []byte) (*http.Response, error) {
	address := *send.address
	switch {
	case request.Path != "" && request.URL != "":
		return nil, errors.New("a converted request has a path or a url, not both")
	case request.Path != "":
		if !strings.HasPrefix(request.Path, "/") || strings.HasPrefix(request.Path, "//") {
			return nil, fmt.Errorf("the path %q does not start with a single /", request.Path)
		}
		joined, err := joinPath(send.root, request.Path)
		if err != nil {
			return nil, err
		}
		address = *joined
	case request.URL != "":
		parsed, err := url.Parse(request.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
			return nil, fmt.Errorf("the url %q is not an http or https address", request.URL)
		}
		address = *parsed
	}
	query := address.Query()
	for name, values := range send.query {
		if !query.Has(name) {
			query[name] = slices.Clone(values)
		}
	}
	credentials := send.credentials.Clone()
	header := send.header.Clone()
	for name, value := range request.Headers {
		canonical := http.CanonicalHeaderKey(name)
		if relayProtected(canonical, "") || !validHeader(name, value) {
			return nil, fmt.Errorf("a converted request may not set the header %s", name)
		}
		header.Set(canonical, value)
	}
	if len(body) > 0 {
		contentType := request.ContentType
		if contentType == "" {
			contentType = "application/json"
		}
		header.Set("Content-Type", contentType)
	} else {
		header.Del("Content-Type")
	}
	address.RawQuery = query.Encode()
	if send.relay.plugin.HasTaskHook("signRequest") {
		final := header.Clone()
		maps.Copy(final, credentials)
		headers, extra, err := send.relay.sign(ctx, send.baseURL, send.key, method, &address, final, body)
		if err != nil {
			return nil, err
		}
		for name, value := range headers {
			credentials.Set(name, value)
		}
		for name, value := range extra {
			query.Set(name, value)
		}
		address.RawQuery = query.Encode()
	}
	outbound, err := http.NewRequestWithContext(ctx, method, (&url.URL{Path: address.Path, RawQuery: address.RawQuery}).String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	outbound.Header = header
	outbound.Header.Set("Content-Length", strconv.Itoa(len(body)))
	outbound.ContentLength = int64(len(body))
	sent := send.target
	sent.BaseURL = &url.URL{Scheme: address.Scheme, Host: address.Host}
	sent.RequestHeaders = credentials
	return send.roundTrip(outbound, sent)
}

// convertedBody is the bytes a converted request sends: a string as it
// is, anything else as JSON.
func convertedBody(raw json.RawMessage) []byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	var text string
	if trimmed[0] == '"' && json.Unmarshal(trimmed, &text) == nil {
		return []byte(text)
	}
	return trimmed
}

// forwardConverted sends an attempt over a route the plugin's hooks
// convert, and answers in the route's protocol.
func (host *Host) forwardConverted(ctx context.Context, writer http.ResponseWriter, target transport.Target, attempt ProviderAttempt, send *relaySend, method string, body []byte, stream bool, authHeader string) error {
	relay := send.relay
	view := map[string]any{"baseUrl": send.baseURL, "path": attempt.ClientPath, "config": relay.config}
	if relay.token != nil {
		view["token"] = relay.token
	}
	raw, err := relay.call(ctx, "convertRequest", view, map[string]any{"body": json.RawMessage(body), "model": attempt.Model, "stream": stream})
	if err != nil {
		return transport.NewUpstreamError(fmt.Errorf("convertRequest: %w", err))
	}
	var request convertedRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return transport.NewUpstreamError(errors.New("convertRequest returned no request"))
	}
	if len(request.Websocket) > 0 || len(request.Form) > 0 || len(request.File) > 0 {
		return transport.NewUpstreamError(errors.New("convertRequest asked for a WebSocket session, a form or a file, which AstrLink does not send yet"))
	}
	state := request.State
	payload := convertedBody(request.Body)
	if relay.plugin.HasTaskHook("prepareRequest") && json.Valid(payload) && len(payload) > 0 {
		prepared, moved, err := relay.prepare(ctx, send.baseURL, attempt.ClientPath, attempt.Model, stream, send.address, send.root, payload, send.header, authHeader)
		var refusal *relayRefusal
		if errors.As(err, &refusal) {
			return writeRelayRefusal(writer, target, refusal)
		}
		if err != nil {
			return transport.NewUpstreamError(err)
		}
		payload = prepared
		if moved != send.address && request.Path == "" && request.URL == "" {
			request.URL = moved.String()
		}
	}
	first := true
	for sent := 0; ; sent++ {
		if sent > maxConvertedRequests {
			return transport.NewUpstreamError(fmt.Errorf("the conversion sent more than %d requests", maxConvertedRequests))
		}
		requestMethod := strings.ToUpper(request.Method)
		switch {
		case requestMethod == "" && first:
			requestMethod = method
		case requestMethod == "" && len(payload) > 0:
			requestMethod = http.MethodPost
		case requestMethod == "":
			requestMethod = http.MethodGet
		}
		if !slices.Contains([]string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions}, requestMethod) {
			return transport.NewUpstreamError(fmt.Errorf("the method %q is not one a converted request takes", request.Method))
		}
		if requestMethod == http.MethodGet || requestMethod == http.MethodHead {
			payload = nil
		}
		response, err := send.send(ctx, requestMethod, request, payload)
		if err != nil {
			if _, upstream := err.(*transport.UpstreamError); upstream {
				return err
			}
			return transport.NewUpstreamError(err)
		}
		readsStream := (request.Stream != nil && *request.Stream) || (request.Stream == nil && first && stream) || streamedType(response.Header.Get("Content-Type"))
		if request.Stream != nil && !*request.Stream {
			readsStream = false
		}
		success := response.StatusCode >= 200 && response.StatusCode < 300
		if success && request.NativeResponse {
			return answerRelay(writer, target, response)
		}
		if success && readsStream {
			return answerRelay(writer, target, host.convertStream(ctx, relay, view, state, response, attempt.Stream))
		}
		whole, err := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseBytes+1))
		_ = response.Body.Close()
		if err != nil || len(whole) > maxProviderResponseBytes {
			return transport.NewUpstreamError(errors.New("the upstream answer could not be read whole"))
		}
		input := map[string]any{"status": response.StatusCode, "headers": firstHeaders(response.Header)}
		if textual(response.Header.Get("Content-Type"), whole) {
			input["body"] = string(whole)
		} else {
			input["bodyBase64"] = base64.StdEncoding.EncodeToString(whole)
		}
		stateView := maps.Clone(view)
		stateView["state"] = jsonOrNull(state)
		raw, err := relay.call(ctx, "convertResponse", stateView, input)
		var answer convertedAnswer
		if err == nil {
			err = json.Unmarshal(raw, &answer)
		}
		original := func() *http.Response {
			response.Body = io.NopCloser(bytes.NewReader(whole))
			response.ContentLength = int64(len(whole))
			return response
		}
		switch {
		case err != nil && success:
			return transport.NewUpstreamError(fmt.Errorf("convertResponse: %w", err))
		case err != nil:
			return answerRelay(writer, target, original())
		case answer.Next != nil:
			if len(answer.State) > 0 {
				state = answer.State
			}
			request = *answer.Next
			payload = convertedBody(request.Body)
			if delay := time.Duration(min(max(request.DelayMs, 0), int(maxConvertedDelay/time.Millisecond))) * time.Millisecond; delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			first = false
			continue
		case answer.Error != nil:
			return answerRelay(writer, target, convertedFailure(*answer.Error, response.StatusCode))
		case !success:
			return answerRelay(writer, target, original())
		}
		data := convertedBody(answer.Body)
		if answer.BodyBase64 != nil {
			decoded, err := base64.StdEncoding.DecodeString(*answer.BodyBase64)
			if err != nil {
				return transport.NewUpstreamError(errors.New("convertResponse returned bodyBase64 that is not base64"))
			}
			data = decoded
		}
		contentType := answer.ContentType
		if contentType == "" {
			contentType = "application/json"
		}
		return answerRelay(writer, target, &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}},
			Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data)),
		})
	}
}

// convertStream reads a streamed answer event by event, has
// convertStreamEvent turn each into the route's protocol, and writes them
// as server-sent events.
func (host *Host) convertStream(ctx context.Context, relay *relayTarget, view map[string]any, state json.RawMessage, response *http.Response, client bool) *http.Response {
	reader, out := io.Pipe()
	go func() {
		defer response.Body.Close()
		convert := func(input map[string]any) (bool, error) {
			stateView := maps.Clone(view)
			stateView["state"] = jsonOrNull(state)
			raw, err := relay.call(ctx, "convertStreamEvent", stateView, input)
			if err != nil {
				return false, fmt.Errorf("convertStreamEvent: %w", err)
			}
			var step convertedEvents
			if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
				if err := json.Unmarshal(raw, &step); err != nil {
					return false, errors.New("convertStreamEvent returned no events")
				}
			}
			if len(step.State) > 0 {
				state = step.State
			}
			if step.Error != nil {
				data, _ := json.Marshal(map[string]any{"error": map[string]any{"message": step.Error.Message, "type": step.Error.Type, "code": step.Error.Code}})
				_ = writeServerSentEvent(out, "error", data)
				return true, nil
			}
			for _, event := range step.Events {
				if err := writeServerSentEvent(out, event.Event, event.Data); err != nil {
					return false, err
				}
			}
			if len(step.Body) > 0 && !client {
				_, err := out.Write(convertedBody(step.Body))
				return true, err
			}
			return false, nil
		}
		mode := streamMode(response.Header.Get("Content-Type"), true)
		err := readEvents(response.Body, mode, func(event map[string]any) (bool, error) {
			input := map[string]any{"data": event["data"]}
			if name, ok := event["event"]; ok {
				input["event"] = name
			}
			return convert(input)
		})
		if err == nil {
			_, err = convert(map[string]any{"done": true})
		}
		out.CloseWithError(err)
	}()
	contentType := "text/event-stream"
	if !client {
		contentType = "application/json"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: reader, ContentLength: -1}
}

// convertedFailure answers a vendor error a hook returned: its status, or
// the upstream's when it is an error, else 500.
func convertedFailure(failure convertError, upstream int) *http.Response {
	status := failure.Status
	if status < 400 || status > 599 {
		status = upstream
		if status < 400 {
			status = http.StatusInternalServerError
		}
	}
	data, _ := json.Marshal(map[string]any{"error": map[string]any{"message": truncateRunes(failure.Message, 4096), "type": truncateRunes(failure.Type, 256), "code": truncateRunes(failure.Code, 256)}})
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}
}

func streamedType(contentType string) bool {
	media, _, _ := mime.ParseMediaType(contentType)
	return media == "text/event-stream" || media == "application/vnd.amazon.eventstream"
}

// textual reports whether a response comes to the hooks as text.
func textual(contentType string, body []byte) bool {
	media, _, _ := mime.ParseMediaType(contentType)
	switch {
	case media == "":
		return utf8.Valid(body)
	case strings.HasPrefix(media, "text/"), strings.HasSuffix(media, "json"), strings.HasSuffix(media, "xml"),
		strings.Contains(media, "ndjson"), media == "application/x-www-form-urlencoded":
		return true
	}
	return false
}

func firstHeaders(header http.Header) map[string]string {
	headers := map[string]string{}
	for name, values := range header {
		if len(values) > 0 {
			headers[http.CanonicalHeaderKey(name)] = values[0]
		}
	}
	return headers
}

func jsonOrNull(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 {
		return json.RawMessage("null")
	}
	return raw
}
