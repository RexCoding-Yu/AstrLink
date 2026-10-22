package providerapi

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"strings"
)

// CodexResponse repairs missing or incorrect SSE MIME types before the
// response reaches stream observers and non-streaming aggregation. JSON and
// HTTP errors retain their original headers and bytes.
func CodexResponse(response *http.Response) {
	encoding := strings.TrimSpace(response.Header.Get("Content-Encoding"))
	if response.StatusCode < 200 || response.StatusCode >= 300 ||
		(encoding != "" && !strings.EqualFold(encoding, "identity")) ||
		strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		return
	}
	reader := bufio.NewReaderSize(response.Body, 512)
	response.Body = &codexResponseBody{Reader: reader, Closer: response.Body}
	if codexSSEPrefix(reader) {
		if response.Header == nil {
			response.Header = make(http.Header)
		}
		response.Header.Set("Content-Type", "text/event-stream; charset=utf-8")
	}
}

// Peek only as far as the first field name, never a whole event or response.
// The bounded lookahead also preserves the prefix for the normal stream reader.
func codexSSEPrefix(reader *bufio.Reader) bool {
	for offset := 0; offset < 512; offset++ {
		prefix, err := reader.Peek(offset + 1)
		if err != nil {
			return false
		}
		if offset == 0 && prefix[0] == 0xef {
			bom, _ := reader.Peek(3)
			if bytes.Equal(bom, []byte{0xef, 0xbb, 0xbf}) {
				offset = 2
				continue
			}
		}
		switch prefix[offset] {
		case ' ', '\t', '\r', '\n':
			continue
		case ':':
			return true
		}
		for _, field := range []string{"event:", "data:", "id:", "retry:"} {
			if prefix[offset] == field[0] {
				prefix, err = reader.Peek(offset + len(field))
				return err == nil && string(prefix[offset:]) == field
			}
		}
		return false
	}
	return false
}

type codexResponseBody struct {
	io.Reader
	io.Closer
}
