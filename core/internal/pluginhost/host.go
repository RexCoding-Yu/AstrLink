// Package pluginhost runs the plugins' part in inference requests: the views
// a plugin reads, the privacy check of what leaves for a plugin, the
// prepareSession and prepareTurn hooks within the turn's budget, the blocks
// they return, the session bindings marker lines make, and the observeTurn
// queue.
//
// Plugins never hold a request up for long and never fail one: whatever goes
// wrong on their side costs the request its injection or its capture, and the
// request record says why. Privacy is the exception: when the check of the
// turn's text cannot run, no text leaves for a plugin the user does not trust.
package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/injection"
	"github.com/QuantumNous/astrlink/core/internal/metadata"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// Budgets of a request's plugins.
const (
	// TurnBudget is how long a request waits for its plugins, counted from
	// the moment it starts them. The privacy check of the turn's text counts
	// against it.
	TurnBudget = 1500 * time.Millisecond
	// sessionHookTimeout bounds prepareSession. A request stops waiting at
	// TurnBudget, but the hook runs on: the block it computes serves the
	// session's later calls.
	sessionHookTimeout = 10 * time.Second
	// turnHookSlack is how long prepareTurn may run past the budget, for its
	// requests to end on their own terms; its result is no use by then.
	turnHookSlack = 500 * time.Millisecond
	// inspectTimeout bounds the privacy check. Past the budget it still
	// decides what observeTurn may capture.
	inspectTimeout = 30 * time.Second
	// bindingTimeout bounds each read and write of session bindings.
	bindingTimeout = 500 * time.Millisecond
)

// ErrBlocked is what Input.Inspect returns when the privacy policy blocks
// the text.
var ErrBlocked = errors.New("the privacy policy blocks the text")

// Options configure a Host.
type Options struct {
	Plugins  *plugin.Manager
	Metadata *metadata.Registry
	// Bindings keeps the sessions' metadata values.
	Bindings storage.MetadataStore
	Logf     func(string, ...any)
}

// Host runs the plugins' part in requests. It is safe for concurrent use.
type Host struct {
	plugins  *plugin.Manager
	metadata *metadata.Registry
	bindings storage.MetadataStore
	ledger   *injection.Ledger
	sessions *sessions
	observe  *observeQueue
	logf     func(string, ...any)
	// budget is TurnBudget; tests shorten it.
	budget time.Duration
}

// New returns a Host. Close stops it.
func New(options Options) *Host {
	logf := options.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Host{
		plugins:  options.Plugins,
		metadata: options.Metadata,
		bindings: options.Bindings,
		ledger:   injection.NewLedger(),
		sessions: newSessions(),
		observe:  newObserveQueue(logf),
		logf:     logf,
		budget:   TurnBudget,
	}
}

// Close stops the observeTurn queue. Captures still waiting are dropped.
func (host *Host) Close() {
	if host != nil {
		host.observe.close()
	}
}

// Input is a request that may run plugins.
type Input struct {
	Protocol contract.ProtocolID
	// Model is the model the client asked for, before any redirect.
	Model  string
	Client string
	Stream bool
	Token  contract.AccessTokenID
	Header http.Header
	// Session and Turn place the request in its conversation. Turn is 0
	// when the request has no user turn.
	Session string
	Turn    int
	// Body is the client's body as it came.
	Body []byte
	// Inspect checks texts against the privacy policy before they go to a
	// plugin the user does not trust, and returns them as an upstream would
	// see them; ErrBlocked when the policy blocks them, another error when
	// the check failed. nil when no policy applies.
	Inspect func(ctx context.Context, texts []string) ([]string, error)
}

// Run is the plugins' part in one request.
type Run struct {
	host  *Host
	input Input
	began time.Time
	turn  turn
	// values are the session's bindings; nil when they could not be read.
	values   map[string]storage.MetadataValueRecord
	metadata contract.RequestMetadata
	// userMarker is set when the turn's opening message is one marker line:
	// it binds the session and is neither recalled for nor captured.
	userMarker bool
	// turnKey names the request's turn: its index and its opening message.
	// The index alone is not enough: a session linked through an older
	// record can give two turns the same index.
	turnKey string
	parts   []*part
	// ready closes once the turn's text passed the privacy check and the
	// prepareTurn calls started.
	ready chan struct{}

	mutex    sync.Mutex
	reported bool
	records  []contract.RequestPluginRecord
	events   []contract.RequestEvent
}

// part is one plugin's part in a run.
type part struct {
	plugin      *plugin.Plugin
	info        plugin.RequestInfo
	view        SessionView
	residentKey string
	// sessionCall is prepareSession's call, nil when the cached block
	// serves.
	sessionCall *hookCall
	// settle is set on the call that settles the plugin's block for the
	// turn; turnCall is its prepareTurn call, skip why there is none.
	settle   bool
	turnCall *hookCall
	skip     string
	// observe is what became of the turn's capture.
	observe string
}

// hookCall is a hook call running in the background.
type hookCall struct {
	done   chan struct{}
	result hookResult
	info   plugin.CallInfo
	err    error
}

// Begin starts the plugins' part in a request, or returns nil when no
// plugin takes part. ctx bounds the reads of the session's bindings; the
// hooks run apart from the request.
func (host *Host) Begin(ctx context.Context, input Input) *Run {
	if host == nil || host.plugins == nil || input.Session == "" {
		return nil
	}
	if _, ok := convoProtocols[input.Protocol]; !ok {
		return nil
	}
	var plugins []*plugin.Plugin
	for _, candidate := range host.plugins.Current().Plugins() {
		if candidate.Enabled() && (candidate.Has(plugin.HookPrepareSession) || candidate.Has(plugin.HookPrepareTurn) || candidate.Has(plugin.HookObserveTurn)) {
			plugins = append(plugins, candidate)
		}
	}
	if len(plugins) == 0 {
		return nil
	}
	run := &Run{host: host, input: input, began: time.Now(), turn: readTurn(input.Protocol, input.Body), ready: make(chan struct{})}
	if opening, ok := host.ledger.Opening(input.Protocol, input.Body); ok && input.Turn > 0 {
		run.turnKey = strconv.Itoa(input.Turn) + ":" + opening
	}
	events := run.bind(ctx)
	if host.metadata != nil {
		run.metadata = host.metadata.Resolve(input.Header, input.Token, run.values)
	}
	run.metadata.Events = clampEvents(append(events, run.metadata.Events...))
	run.plan(plugins)
	go run.work()
	return run
}

// Metadata is the request's metadata: its values, with the session's
// bindings, and what the turn's opening message changed.
func (run *Run) Metadata() contract.RequestMetadata {
	if run == nil {
		return contract.RequestMetadata{}
	}
	return run.metadata
}

// plan settles what each plugin does in the request and starts the
// prepareSession calls, which read no user text.
func (run *Run) plan(plugins []*plugin.Plugin) {
	host := run.host
	meta := map[string]plugin.MetaValue{}
	for key, value := range run.metadata.Values {
		meta[key] = plugin.MetaValue{Value: value.Value, Source: value.Source}
	}
	host.sessions.mutex.Lock()
	defer host.sessions.mutex.Unlock()
	for _, candidate := range plugins {
		read := candidate.Can(plugin.CapabilityRequest)
		view := SessionView{
			Protocol: string(run.input.Protocol), Model: run.input.Model, Client: run.input.Client,
			Meta: map[string]plugin.MetaValue{}, Session: plugin.SessionRef{ID: run.input.Session, Turn: run.input.Turn},
		}
		if read {
			view.Meta = meta
		}
		current := &part{
			plugin: candidate,
			view:   view,
			info: plugin.RequestInfo{
				Protocol: view.Protocol, Model: view.Model, Client: view.Client, Stream: run.input.Stream,
				Meta: view.Meta, Session: &plugin.SessionRef{ID: run.input.Session, Turn: run.input.Turn},
			},
			residentKey: residentKey(candidate, view.Meta),
		}
		run.parts = append(run.parts, current)
		state := host.sessions.plugin(run.input.Session, candidate.Key)
		inject := candidate.Can(plugin.CapabilityInject)
		current.settle = run.turn.opened && run.turnKey != "" && state.settled != run.turnKey
		unbound := false
		for _, attribute := range candidate.Meta.Metadata {
			if _, found := run.metadata.Values[attribute.Key]; !found {
				unbound = true
			}
		}
		// A session computes its resident block once, again when what the
		// block was computed from changed, and on each turn while it lacks
		// a value of the plugin's attributes: the block then asks for it.
		if inject && candidate.Has(plugin.HookPrepareSession) &&
			(!state.computed || state.residentKey != current.residentKey || (current.settle && unbound)) {
			current.sessionCall = run.startSession(current)
		}
		if !current.settle || run.userMarker {
			continue
		}
		if candidate.Has(plugin.HookObserveTurn) && read {
			current.observe = "pending"
			if state.bindingTurn != "" && state.bindingTurn == run.turnKey {
				current.observe = observeSkippedBindingTurn
			}
		}
	}
}

// startSession calls prepareSession in the background. Its block, once it
// comes, serves the session from then on.
func (run *Run) startSession(current *part) *hookCall {
	call := &hookCall{done: make(chan struct{})}
	host, session, key := run.host, run.input.Session, current.residentKey
	go func() {
		defer close(call.done)
		ctx, cancel := context.WithTimeout(context.Background(), sessionHookTimeout)
		defer cancel()
		raw, info, err := current.plugin.Call(ctx, plugin.HookPrepareSession, current.view, plugin.CallOptions{
			Request: &current.info, NetworkBudget: sessionHookTimeout,
		})
		call.info, call.err = info, err
		if err != nil {
			return
		}
		call.result = parseResult(raw, current.plugin)
		if call.result.invalid {
			return
		}
		text, dropped := call.result.system.Render(injection.MaxBlockBytes)
		call.result.dropped = dropped
		host.sessions.mutex.Lock()
		defer host.sessions.mutex.Unlock()
		state := host.sessions.plugin(session, current.plugin.Key)
		state.computed, state.residentKey, state.resident = true, key, text
		state.residentTitle = call.result.record.Title
	}()
	return call
}

// work checks the turn's text against the privacy policy, then starts the
// prepareTurn calls and queues the captures.
func (run *Run) work() {
	defer close(run.ready)
	needsText := false
	for _, current := range run.parts {
		if current.settle && !run.userMarker && (run.prepares(current) || current.observe == "pending") {
			needsText = true
		}
	}
	if !needsText {
		return
	}
	checked, skip := run.inspect()
	run.mutex.Lock()
	defer run.mutex.Unlock()
	late := time.Since(run.began) >= run.host.budget
	for _, current := range run.parts {
		if !current.settle || run.userMarker {
			continue
		}
		user := run.turn.user
		reason := ""
		if !current.plugin.Settings.Trusted {
			user, reason = checked, skip
		}
		turnView := TurnView{SessionView: current.view, Stream: run.input.Stream}
		if current.plugin.Can(plugin.CapabilityRequest) {
			turnView.User = user
		}
		if run.prepares(current) {
			switch {
			case reason != "":
				current.skip = reason
			case late:
				current.skip = contract.PluginSkipTimeout
			default:
				current.turnCall = run.startTurn(current, turnView)
			}
		}
		if current.observe != "pending" {
			continue
		}
		switch {
		case reason != "":
			current.observe = observeSkippedPrivacy
		case run.host.observe.push(observeJob{plugin: current.plugin, view: turnView, info: current.info}):
			current.observe = observeQueued
		default:
			current.observe = observeQueueFull
		}
	}
}

// inspect returns the turn's text as a plugin the user does not trust may
// read it, or why it may read none.
func (run *Run) inspect() (UserText, string) {
	user := run.turn.user
	if run.input.Inspect == nil {
		return user, ""
	}
	trusted := true
	for _, current := range run.parts {
		trusted = trusted && current.plugin.Settings.Trusted
	}
	if trusted {
		return user, ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), inspectTimeout)
	defer cancel()
	texts := []string{user.Text}
	if user.Previous != "" {
		texts = append(texts, user.Previous)
	}
	checked, err := run.input.Inspect(ctx, texts)
	if errors.Is(err, ErrBlocked) {
		return UserText{}, contract.PluginSkipPrivacyBlocked
	}
	if err != nil || len(checked) != len(texts) {
		return UserText{}, contract.PluginSkipPrivacyUnavailable
	}
	if checked[0] != user.Text {
		user.Text, user.Typed = checked[0], typedOf(run.input.Protocol, checked[0])
	}
	if len(checked) > 1 {
		user.Previous = checked[1]
	}
	return user, ""
}

func (run *Run) startTurn(current *part, view TurnView) *hookCall {
	call := &hookCall{done: make(chan struct{})}
	deadline := run.began.Add(run.host.budget + turnHookSlack)
	go func() {
		defer close(call.done)
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		raw, info, err := current.plugin.Call(ctx, plugin.HookPrepareTurn, view, plugin.CallOptions{
			Request: &current.info, NetworkBudget: time.Until(deadline),
		})
		call.info, call.err = info, err
		if err == nil {
			call.result = parseResult(raw, current.plugin)
		}
	}()
	return call
}

// Inject puts the plugins' blocks into body, the request's body in the
// client's protocol, after waiting for the plugins until the turn's budget
// runs out. It returns the body to send: body itself when nothing goes in or
// the injection fails.
func (run *Run) Inject(body []byte) []byte {
	if run == nil {
		return body
	}
	run.wait()
	run.mutex.Lock()
	defer run.mutex.Unlock()
	host := run.host
	request := injection.Request{Protocol: run.input.Protocol, Session: run.input.Session, Body: body}
	results := make([]injected, len(run.parts))
	host.sessions.mutex.Lock()
	for index, current := range run.parts {
		state := host.sessions.plugin(run.input.Session, current.plugin.Key)
		entry := injection.Plugin{Key: current.plugin.Key}
		if current.plugin.Can(plugin.CapabilityInject) && state.computed {
			entry.Resident = state.resident
			results[index].residentTitle = state.residentTitle
		}
		results[index].turnTitle = state.turnTitle
		if current.settle {
			entry.Settled = true
			results[index].turnTitle = ""
			if result, ok := current.turnCall.finished(); ok && !result.invalid {
				entry.Turn, results[index].dropped = result.context.Render(injection.MaxBlockBytes)
				results[index].turnTitle = result.record.Title
			}
			state.settled = run.turnKey
			state.turnTitle = results[index].turnTitle
		}
		request.Plugins = append(request.Plugins, entry)
	}
	host.sessions.mutex.Unlock()
	applied, err := host.ledger.Apply(request)
	if err != nil {
		host.logf("plugin injection failed, the request goes out as it came: %v", err)
		run.report(results, true)
		return body
	}
	for index := range results {
		results[index].outcome = applied.Outcomes[index]
	}
	run.report(results, false)
	return applied.Body
}

// wait waits for the plugins until the turn's budget runs out.
func (run *Run) wait() {
	ctx, cancel := context.WithDeadline(context.Background(), run.began.Add(run.host.budget))
	defer cancel()
	select {
	case <-run.ready:
	case <-ctx.Done():
		return
	}
	for _, current := range run.parts {
		for _, call := range []*hookCall{current.sessionCall, current.turnCall} {
			if call == nil {
				continue
			}
			select {
			case <-call.done:
			case <-ctx.Done():
				return
			}
		}
	}
}

// finished returns the call's result when it came back without error.
func (call *hookCall) finished() (hookResult, bool) {
	if call == nil {
		return hookResult{}, false
	}
	select {
	case <-call.done:
		return call.result, call.err == nil
	default:
		return hookResult{}, false
	}
}

// Report returns the plugins' records and their events for the request
// record. The first call after Inject returns what Inject did; without
// Inject, what the plugins did so far. Later calls return nothing.
func (run *Run) Report() ([]contract.RequestPluginRecord, []contract.RequestEvent) {
	if run == nil {
		return nil, nil
	}
	run.mutex.Lock()
	defer run.mutex.Unlock()
	if !run.reported {
		run.report(make([]injected, len(run.parts)), false)
	}
	records, events := run.records, run.events
	run.records, run.events = nil, nil
	return records, events
}

// residentKey names what a resident block is computed from: the plugin's
// version and configuration, and the request's metadata.
func residentKey(candidate *plugin.Plugin, meta map[string]plugin.MetaValue) string {
	hash := sha256.New()
	hash.Write([]byte(candidate.Meta.Version + "\x00"))
	hash.Write(candidate.Settings.Config)
	keys := make([]string, 0, len(meta))
	for key := range meta {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		hash.Write([]byte("\x00" + key + "=" + meta[key].Value + "\x01" + meta[key].Source))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func clampEvents(events []contract.MetadataEvent) []contract.MetadataEvent {
	if len(events) > contract.MaxRequestMetadataEvents {
		events = events[:contract.MaxRequestMetadataEvents]
	}
	return slices.Clip(events)
}
