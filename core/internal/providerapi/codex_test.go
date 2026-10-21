package providerapi

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCodexResponseMIMERepairPreservesBody(t *testing.T) {
	for _, test := range []struct {
		name, contentType, encoding, body string
		status                            int
		wantSSE                           bool
	}{
		{name: "missing type", body: "event: response.completed\ndata: {}\n\n", wantSSE: true},
		{name: "wrong type", contentType: "application/json", body: "data: {}\n\n", wantSSE: true},
		{name: "comment", body: ": ping\n\ndata: {}\n\n", wantSSE: true},
		{name: "blank prefix", body: "\r\n\r\nevent: response.created\n", wantSSE: true},
		{name: "BOM", body: "\xef\xbb\xbfdata: {}\n\n", wantSSE: true},
		{name: "id field", body: "id: 1\ndata: {}\n\n", wantSSE: true},
		{name: "retry field", body: "retry: 100\ndata: {}\n\n", wantSSE: true},
		{name: "field without body yet", body: "data:", wantSSE: true},
		{name: "JSON", contentType: "application/json", body: `{"object":"response"}`},
		{name: "JSON with whitespace", body: " \n {}"},
		{name: "HTML", contentType: "text/html", body: "<!doctype html>error"},
		{name: "empty"},
		{name: "not a field", body: "database unavailable"},
		{name: "truncated prefix", body: "dat"},
		{name: "bounded lookahead", body: strings.Repeat("\n", 1024) + "data: {}\n\n"},
		{name: "HTTP error", status: http.StatusBadRequest, contentType: "application/json", body: `{"error":{"message":"bad input"}}`},
		{name: "HTTP error is not relabeled", status: http.StatusBadRequest, body: "data: {}\n\n"},
		{name: "encoded bytes untouched", encoding: "br", body: "data: compressed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			status := test.status
			if status == 0 {
				status = http.StatusOK
			}
			body := &trackedCodexBody{Reader: strings.NewReader(test.body)}
			response := &http.Response{StatusCode: status, Header: make(http.Header), Body: body}
			if test.contentType != "" {
				response.Header.Set("Content-Type", test.contentType)
			}
			if test.encoding != "" {
				response.Header.Set("Content-Encoding", test.encoding)
			}
			CodexResponse(response)
			want := test.contentType
			if test.wantSSE {
				want = "text/event-stream; charset=utf-8"
			}
			if got := response.Header.Get("Content-Type"); got != want {
				t.Fatalf("type=%q want=%q", got, want)
			}
			got, err := io.ReadAll(response.Body)
			if err != nil || string(got) != test.body {
				t.Fatalf("body=%q error=%v", got, err)
			}
			if err := response.Body.Close(); err != nil || !body.closed {
				t.Fatalf("body not closed: %v", err)
			}
		})
	}
}

type trackedCodexBody struct {
	io.Reader
	closed bool
}

func (body *trackedCodexBody) Close() error { body.closed = true; return nil }

func TestCodexResponseDoesNotWaitForCompleteEvent(t *testing.T) {
	reader, writer := io.Pipe()
	response := &http.Response{StatusCode: http.StatusOK, Body: reader}
	done := make(chan struct{})
	written := make(chan struct{})
	go func() {
		CodexResponse(response)
		close(done)
	}()
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
		<-done
		<-written
	})
	// Keep the event and stream open after only the first field name.
	go func() {
		_, _ = io.WriteString(writer, "data:")
		close(written)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("MIME detection waited for the rest of the event")
	}
	if response.Header.Get("Content-Type") != "text/event-stream; charset=utf-8" {
		t.Fatalf("response headers = %v", response.Header)
	}
	first := make([]byte, len("data:"))
	if _, err := io.ReadFull(response.Body, first); err != nil || string(first) != "data:" {
		t.Fatalf("first bytes = %q, error = %v", first, err)
	}
}
