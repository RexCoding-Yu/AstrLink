package ingress

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
)

const (
	spellingPersonMarker = "<PRIVATE_PERSON_6ad1158cae28c183>"
	spellingPersonValue  = `Zoë "Z" O'Neil`
	spellingPhone        = "+1-555-555-0152"
	spellingPhoneValue   = "+86 138 0013 8000"
)

func spellingRedactions() []privacy.Redaction {
	return []privacy.Redaction{
		{Placeholder: spellingPersonMarker, Kind: privacy.KindPerson, Value: spellingPersonValue},
		{Placeholder: spellingPhone, Kind: privacy.KindPhone, Value: spellingPhoneValue},
	}
}

func writeSSEEvents(t *testing.T, writer *restoringResponseWriter, events ...map[string]any) {
	t.Helper()
	for _, event := range events {
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		wire := append([]byte("data: "), encoded...)
		wire = append(wire, '\n', '\n')
		if _, err := writer.Write(wire); err != nil {
			t.Fatal(err)
		}
	}
}

// TestRestoringWriterRestoresCodexPatchSpellings reproduces a Codex
// apply_patch that wrote a person marker HTML-escaped and a phone stand-in
// regrouped into an HTML page. A custom tool's input is raw text, so values are
// written as is rather than escaped for JSON.
func TestRestoringWriterRestoresCodexPatchSpellings(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := newRestoringResponseWriter(
		recorder, spellingRedactions(), true, contract.ProtocolOpenAIResponses, true,
	)
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.WriteHeader(http.StatusOK)

	fragments := []string{
		"*** Add File: index.html\n+<footer>&lt;PRIVATE_PERSON_6ad1",
		"158cae28c183&gt;</footer>\n+<!-- " + spellingPersonMarker + " -->\n" +
			`+<a href="tel:+15555550152">+1 (555) 555-01`,
		"52</a>",
	}
	input := strings.Join(fragments, "")
	item := func(input string) map[string]any {
		return map[string]any{
			"type": "custom_tool_call", "id": "ctc_1", "call_id": "call_1",
			"name": "apply_patch", "input": input,
		}
	}
	events := []map[string]any{{
		"type": "response.output_item.added", "output_index": 0, "item": item(""),
	}}
	for _, fragment := range fragments {
		events = append(events, map[string]any{
			"type": "response.custom_tool_call_input.delta", "item_id": "ctc_1",
			"output_index": 0, "delta": fragment,
		})
	}
	events = append(events,
		map[string]any{
			"type": "response.custom_tool_call_input.done", "item_id": "ctc_1",
			"output_index": 0, "input": input,
		},
		map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item(input)},
		map[string]any{
			"type":     "response.completed",
			"response": map[string]any{"id": "resp_1", "output": []any{item(input)}},
		},
	)
	writeSSEEvents(t, writer, events...)
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}

	want := "*** Add File: index.html\n+<footer>Zoë &#34;Z&#34; O&#39;Neil</footer>\n" +
		`+<!-- Zoë "Z" O'Neil -->` + "\n" +
		`+<a href="tel:+86 138 0013 8000">+86 138 0013 8000</a>`
	body := recorder.Body.Bytes()
	pick := func(eventType string, field func(map[string]any) any) string {
		return concatSSEStrings(t, body, func(event map[string]any) []string {
			if event["type"] != eventType {
				return nil
			}
			value, _ := field(event).(string)
			return []string{value}
		})
	}
	itemInput := func(item any) any {
		object, _ := item.(map[string]any)
		return object["input"]
	}
	for name, got := range map[string]string{
		"deltas": pick("response.custom_tool_call_input.delta", func(event map[string]any) any {
			return event["delta"]
		}),
		"done": pick("response.custom_tool_call_input.done", func(event map[string]any) any {
			return event["input"]
		}),
		"item": pick("response.output_item.done", func(event map[string]any) any {
			return itemInput(event["item"])
		}),
		"completed": pick("response.completed", func(event map[string]any) any {
			response, _ := event["response"].(map[string]any)
			output, _ := response["output"].([]any)
			if len(output) == 0 {
				return nil
			}
			return itemInput(output[0])
		}),
	} {
		if got != want {
			t.Errorf("%s input = %q\nwant %q", name, got, want)
		}
	}
	if writer.toolArgumentRestoredCount() != 16 || writer.fallbackCount() != 0 {
		t.Fatalf(
			"tool restored=%d fallback=%d",
			writer.toolArgumentRestoredCount(),
			writer.fallbackCount(),
		)
	}
}

// TestRestoringWriterReleasesHeldSpellingWhenChannelEnds pins the latency
// contract: a number at the very end of a delta is held only until the stream
// says that text is finished, not until the response ends.
func TestRestoringWriterReleasesHeldSpellingWhenChannelEnds(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := newRestoringResponseWriter(
		recorder, spellingRedactions(), true, contract.ProtocolOpenAIResponses, true,
	)
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.WriteHeader(http.StatusOK)

	writeSSEEvents(t, writer, map[string]any{
		"type": "response.output_text.delta", "item_id": "msg_1",
		"output_index": 0, "content_index": 0, "delta": "Call +1 (555) 555-0152",
	})
	if recorder.Body.Len() != 0 {
		t.Fatalf("a number that may continue was released early: %s", recorder.Body.String())
	}
	writeSSEEvents(t, writer, map[string]any{
		"type": "response.output_text.done", "item_id": "msg_1",
		"output_index": 0, "content_index": 0, "text": "Call +1 (555) 555-0152",
	})
	want := "Call " + spellingPhoneValue
	got := visibleSSEText(t, contract.ProtocolOpenAIResponses, recorder.Body.Bytes())
	if got != want+want {
		t.Fatalf("visible=%q wire=%s", got, recorder.Body.String())
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
	if writer.visibleRestoredCount() != 2 || writer.fallbackCount() != 0 {
		t.Fatalf("restored=%d fallback=%d", writer.visibleRestoredCount(), writer.fallbackCount())
	}
}

func TestRestoringWriterResolvesSpellingInFinishingChatChunk(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := newRestoringResponseWriter(
		recorder, spellingRedactions(), true, contract.ProtocolOpenAIChat, true,
	)
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.WriteHeader(http.StatusOK)
	writeSSEEvents(t, writer, map[string]any{
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         map[string]any{"content": "Call 555.555.0152"},
			"finish_reason": "stop",
		}},
	})
	if got := visibleSSEText(t, contract.ProtocolOpenAIChat, recorder.Body.Bytes()); got != "Call "+spellingPhoneValue {
		t.Fatalf("visible=%q wire=%s", got, recorder.Body.String())
	}
}

// TestRestoringWriterTreatsBufferedResponseAsFinal covers a non-streaming body,
// whose text cannot continue even when it ends in a number.
func TestRestoringWriterTreatsBufferedResponseAsFinal(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := newRestoringResponseWriter(
		recorder, spellingRedactions(), false, contract.ProtocolGoogleGenerateContent, true,
	)
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	body := `{"candidates":[{"content":{"parts":[{"text":"Call +1 (555) 555-0152"}]}}]}`
	if _, err := writer.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
	want := `{"candidates":[{"content":{"parts":[{"text":"Call ` + spellingPhoneValue + `"}]}}]}`
	if recorder.Body.String() != want || writer.fallbackCount() != 0 {
		t.Fatalf("body=%s fallback=%d", recorder.Body.String(), writer.fallbackCount())
	}
}

func TestRestoringWriterRestoresSpellingsInPlainTextStream(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := newRestoringResponseWriter(
		recorder, spellingRedactions(), true, contract.ProtocolOpenAIChat, true,
	)
	writer.Header().Set("Content-Type", "text/plain")
	writer.WriteHeader(http.StatusOK)
	for _, chunk := range []string{"&lt;PRIVATE_PERSON_6ad1158cae28c183&gt; at +1 (555) 555-01", "52"} {
		if _, err := writer.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
	want := "Zoë &#34;Z&#34; O&#39;Neil at " + spellingPhoneValue
	if recorder.Body.String() != want || writer.restoredCount() != 2 || writer.fallbackCount() != 0 {
		t.Fatalf(
			"body=%q restored=%d fallback=%d",
			recorder.Body.String(), writer.restoredCount(), writer.fallbackCount(),
		)
	}
}
