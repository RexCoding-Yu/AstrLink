// Package plugin runs gateway plugins: JavaScript or TypeScript modules that
// the host compiles once and calls in sandboxed moejs runtimes.
//
// A plugin decides policy: what to look up, what to keep, how to lay it out,
// what to show. The host owns the mechanism: the sandbox, the budgets, the
// network, the storage, and where a plugin's results go. A plugin reaches
// nothing but the astrlink.* functions the host installs: it has no files,
// timers, environment or network of its own, and never sees a credential.
package plugin

import (
	"errors"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// APIVersion is the plugin contract this host implements; a plugin states
// the one it was written for in meta.apiVersion.
const APIVersion = 1

// Capabilities a plugin asks for in meta.capabilities. None is in effect
// until the user grants it when enabling the plugin.
const (
	// CapabilityInject lets prepareSession and prepareTurn add blocks to
	// the request.
	CapabilityInject = "context.inject"
	// CapabilityRequest gives the hooks the turn's text and metadata, and
	// astrlink.request.info.
	CapabilityRequest = "request.read"
	// CapabilityNetwork gives astrlink.http.fetch, limited to the
	// addresses the user allows.
	CapabilityNetwork = "network"
	// CapabilityStorage gives astrlink.store, the plugin's own key-value
	// store in the gateway settings.
	CapabilityStorage = "storage"
	// CapabilityViews lets the hooks return views for request records and
	// the plugin page.
	CapabilityViews = "ui.views"
)

var knownCapabilities = []string{
	CapabilityInject,
	CapabilityRequest,
	CapabilityNetwork,
	CapabilityStorage,
	CapabilityViews,
}

// Hooks a plugin may export. Each is optional; whether a plugin exports one
// is settled once, when it loads.
const (
	HookPrepareSession = "prepareSession"
	HookPrepareTurn    = "prepareTurn"
	HookObserveTurn    = "observeTurn"
	HookDescribeStatus = "describeStatus"
	HookTestConnection = "testConnection"
	// HookOnAction handles a click on a button of one of the plugin's
	// records, outside any request.
	HookOnAction = "onAction"
)

var knownHooks = []string{
	HookPrepareSession,
	HookPrepareTurn,
	HookObserveTurn,
	HookDescribeStatus,
	HookTestConnection,
	HookOnAction,
}

// Limits bound one hook call.
type Limits struct {
	// ScriptTime is the JavaScript time a call may take. Waiting on the host
	// (http.fetch, the store) does not count.
	ScriptTime time.Duration
	// Memory is what one call's JavaScript may allocate.
	Memory int64
	// ResultBytes and ResultNodes bound a hook's result as JSON text: its
	// length, and the number of values in it.
	ResultBytes int
	ResultNodes int
	// Concurrency is how many calls of one plugin may run JavaScript at the
	// same time. A call waiting on the host gives up its place meanwhile.
	Concurrency int
}

// DefaultLimits are the limits of the design: 200 ms of JavaScript, 32 MiB,
// and a result of at most 64 KiB and 4096 values.
func DefaultLimits() Limits {
	return Limits{
		ScriptTime:  200 * time.Millisecond,
		Memory:      32 << 20,
		ResultBytes: 64 << 10,
		ResultNodes: 4096,
		Concurrency: runtime.GOMAXPROCS(0),
	}
}

func (limits Limits) withDefaults() Limits {
	defaults := DefaultLimits()
	if limits.ScriptTime <= 0 {
		limits.ScriptTime = defaults.ScriptTime
	}
	if limits.Memory <= 0 {
		limits.Memory = defaults.Memory
	}
	if limits.ResultBytes <= 0 {
		limits.ResultBytes = defaults.ResultBytes
	}
	if limits.ResultNodes <= 0 {
		limits.ResultNodes = defaults.ResultNodes
	}
	if limits.Concurrency <= 0 {
		limits.Concurrency = defaults.Concurrency
	}
	return limits
}

// Reasons a hook call failed. Request records and the plugin page show them;
// the request itself goes on without the plugin's result.
const (
	ReasonTimeout       = "timeout"
	ReasonMemory        = "memory_limit"
	ReasonPluginError   = "plugin_error"
	ReasonInvalidResult = "invalid_result"
	ReasonCanceled      = "canceled"
	ReasonUnavailable   = "unavailable"
)

// CallError is a hook call that produced no result.
type CallError struct {
	Reason string
	// Message is safe to show: control characters removed and at most
	// maxMessageRunes long.
	Message string
	Err     error
}

func (e *CallError) Error() string { return e.Reason + ": " + e.Message }

func (e *CallError) Unwrap() error { return e.Err }

// errScriptTime interrupts a call that used up Limits.ScriptTime.
var errScriptTime = errors.New("plugin script time limit exceeded")

// maxMessageRunes bounds the messages a plugin can put in front of the user
// through an error.
const maxMessageRunes = 512

// cleanText removes control characters other than those keep allows and cuts
// the text to max runes. Text from a plugin passes through it before the
// host shows or logs it.
func cleanText(text string, max int, keep string) string {
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "�")
	}
	var builder strings.Builder
	count := 0
	for _, r := range text {
		if unicode.IsControl(r) && !strings.ContainsRune(keep, r) {
			continue
		}
		if count == max {
			builder.WriteString("…")
			break
		}
		builder.WriteRune(r)
		count++
	}
	return builder.String()
}
