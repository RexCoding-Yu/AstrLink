package ingress

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// upstreamErrorWireLimit bounds the encoded bytes kept for one upstream error.
// It leaves room to decode a compressed body into the record's own limit.
const upstreamErrorWireLimit = 64 * 1024

// upstreamErrorCapture keeps the body of an upstream HTTP error whatever the
// audit capture settings, so a failed record shows the provider's own words.
type upstreamErrorCapture struct {
	status          int
	contentType     string
	contentEncoding string
	wire            []byte
	truncated       bool
}

func newUpstreamErrorCapture(status int, contentType, contentEncoding string) *upstreamErrorCapture {
	return &upstreamErrorCapture{
		status:          status,
		contentType:     strings.TrimSpace(contentType),
		contentEncoding: strings.TrimSpace(contentEncoding),
	}
}

func (capture *upstreamErrorCapture) observe(chunk []byte) {
	if capture == nil || capture.truncated {
		return
	}
	room := upstreamErrorWireLimit - len(capture.wire)
	if len(chunk) > room {
		chunk = chunk[:room]
		capture.truncated = true
	}
	capture.wire = append(capture.wire, chunk...)
}

// response decodes the captured body into the verbatim text a record keeps.
func (capture *upstreamErrorCapture) response() contract.UpstreamErrorResponse {
	response := contract.UpstreamErrorResponse{
		Status:      capture.status,
		ContentType: contract.ClampRunes(capture.contentType, 256),
		Truncated:   capture.truncated,
	}
	body := capture.wire
	if capture.contentEncoding != "" && !strings.EqualFold(capture.contentEncoding, "identity") {
		decoded, err := transport.DecodeBody(body, capture.contentEncoding, upstreamErrorWireLimit*16)
		if err != nil {
			response.Body = fmt.Sprintf(
				"(%d bytes with Content-Encoding %q could not be decoded: %v)",
				len(body), capture.contentEncoding, err,
			)
			return response
		}
		body = decoded
	}
	if len(body) > contract.MaxUpstreamErrorBodyBytes {
		body = body[:contract.MaxUpstreamErrorBodyBytes]
		response.Truncated = true
	}
	// A cut can split a multi-byte character; bytes.ToValidUTF8 keeps the rest.
	response.Body = string(bytes.ToValidUTF8(body, []byte("�")))
	for len(response.Body) > contract.MaxUpstreamErrorBodyBytes {
		_, size := utf8.DecodeLastRuneInString(response.Body)
		response.Body = response.Body[:len(response.Body)-size]
	}
	return response
}

// upstreamHTTPErrorSummary describes an upstream HTTP error in the provider's
// own words: the message it gave and its response body, both unredacted.
func (session *recordSession) upstreamHTTPErrorSummary(status int) contract.ErrorSummary {
	summary := errorSummaryFromHTTPStatus(status)
	if session == nil || session.upstreamError == nil {
		return summary
	}
	upstream := session.upstreamError.response()
	summary.Upstream = &upstream
	if message := nativeErrorMessage(upstream); message != "" {
		summary.Message = contract.ClampRunes(message, 1024)
	}
	return summary
}

// nativeErrorMessage extracts the human-readable message from a provider error
// body, prefixed with the provider's error type or code when it gives one.
func nativeErrorMessage(response contract.UpstreamErrorResponse) string {
	body := strings.TrimSpace(response.Body)
	if body == "" {
		return ""
	}
	if message := jsonErrorMessage([]byte(body)); message != "" {
		return message
	}
	if strings.HasPrefix(body, "event:") || strings.HasPrefix(body, "data:") {
		scanner := bufio.NewScanner(strings.NewReader(body))
		scanner.Buffer(make([]byte, 0, 4096), contract.MaxUpstreamErrorBodyBytes)
		for scanner.Scan() {
			data, ok := strings.CutPrefix(strings.TrimSpace(scanner.Text()), "data:")
			if !ok {
				continue
			}
			if message := jsonErrorMessage([]byte(strings.TrimSpace(data))); message != "" {
				return message
			}
		}
	}
	if strings.Contains(strings.ToLower(response.ContentType), "html") || strings.HasPrefix(body, "<") {
		// The page stays in Upstream.Body; its markup is no message.
		return ""
	}
	return strings.Join(strings.Fields(body), " ")
}

func jsonErrorMessage(raw []byte) string {
	var envelope map[string]any
	if json.Unmarshal(raw, &envelope) != nil {
		// Gemini can wrap the error object in an array.
		var list []map[string]any
		if json.Unmarshal(raw, &list) != nil || len(list) == 0 {
			return ""
		}
		envelope = list[0]
	}
	if text, ok := envelope["error"].(string); ok && strings.TrimSpace(text) != "" {
		return strings.TrimSpace(text)
	}
	object, _ := envelope["error"].(map[string]any)
	if object == nil {
		object = envelope
	}
	message := ""
	for _, key := range []string{"message", "detail", "msg", "error_description"} {
		if text, ok := object[key].(string); ok && strings.TrimSpace(text) != "" {
			message = strings.TrimSpace(text)
			break
		}
	}
	if message == "" {
		return ""
	}
	for _, key := range []string{"type", "code", "status"} {
		label, _ := object[key].(string)
		label = strings.TrimSpace(label)
		if label == "" || label == "<nil>" {
			continue
		}
		if !strings.Contains(message, label) {
			return label + ": " + message
		}
		break
	}
	return message
}
