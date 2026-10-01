// Package relaykitbridge is the only place where a future RelayKit dependency
// may be adapted. Core contracts and transport code must not import RelayKit
// types directly.
package relaykitbridge

import (
	"context"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
)

// ConversionState is what one request conversion recorded for the response
// of the same upstream attempt, such as which Responses custom tools were sent
// as functions. It is opaque outside this package. Every attempt must use the
// state returned by its own ConvertRequest; the zero value records nothing.
type ConversionState struct {
	responsesTools *convmeta.ResponsesToolState
}

// ConversionDiagnostic reports content a conversion dropped or rewrote. It is
// informational: a lossy conversion that RelayKit allows still succeeds.
type ConversionDiagnostic struct {
	Code     string
	Path     string
	Message  string
	Severity string // "warning" or "error"
}

type ConvertRequestInput struct {
	From          contract.ProtocolID
	To            contract.ProtocolID
	ContentType   string
	Body          []byte
	PublicModel   string
	UpstreamModel string
	Streaming     bool
}

type ConvertRequestOutput struct {
	ContentType string
	Body        []byte
	State       ConversionState
	Diagnostics []ConversionDiagnostic
}

type ConvertResponseInput struct {
	From          contract.ProtocolID
	To            contract.ProtocolID
	StatusCode    int
	ContentType   string
	Body          []byte
	PublicModel   string
	UpstreamModel string
	State         ConversionState
}

type ConvertResponseOutput struct {
	StatusCode  int
	ContentType string
	Body        []byte
	Diagnostics []ConversionDiagnostic
}

type StreamOptions struct {
	From          contract.ProtocolID
	To            contract.ProtocolID
	PublicModel   string
	UpstreamModel string
	ID            string
	Created       int64
	IncludeUsage  bool
	State         ConversionState
}

// ResponseEvent is an engine-neutral, stateful stream unit. Adapters decide
// how protocol-specific wire events map to and from this boundary.
type ResponseEvent struct {
	Type string
	Data []byte
}

type ResponseStream interface {
	Convert(context.Context, ResponseEvent) ([]ResponseEvent, error)
	Finalize(context.Context) ([]ResponseEvent, error)
	// Diagnostics returns every distinct diagnostic observed so far.
	Diagnostics() []ConversionDiagnostic
	Close() error
}

// ConversionEngine is deliberately thin. Native and delegated plans bypass
// it completely; only an explicit relaykit execution plan may invoke it.
type ConversionEngine interface {
	Version() string
	Edges() []contract.ConversionEdge
	ConvertRequest(context.Context, ConvertRequestInput) (ConvertRequestOutput, error)
	ConvertResponse(context.Context, ConvertResponseInput) (ConvertResponseOutput, error)
	NewResponseStream(context.Context, StreamOptions) (ResponseStream, error)
}
