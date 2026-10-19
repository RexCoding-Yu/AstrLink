package pluginhost

import (
	"context"
	"maps"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/metadata"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// bind reads the session's bindings and applies what the turn's opening
// message says. A message that is one marker line binds or rebinds the
// session there and then. A session with no value stored for a marker's
// attribute, such as one whose client lost its session, works the value out
// from the history the client replayed. A stored value, none included,
// stops that: the history's markers were handled when they came, and
// replaying them would undo what the user changed since.
func (run *Run) bind(ctx context.Context) []contract.MetadataEvent {
	host := run.host
	if host.bindings == nil || host.metadata == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, bindingTimeout)
	defer cancel()
	values, err := host.bindings.MetadataValues(ctx, storage.MetadataTargetSession, run.input.Session)
	if err != nil {
		host.logf("read the session's metadata: %v", err)
		return nil
	}
	if values == nil {
		values = map[string]storage.MetadataValueRecord{}
	}
	run.values = values
	if run.turn.opened {
		if hit, ok := host.metadata.UserMarker(run.turn.own); ok {
			run.userMarker = true
			if !hit.Valid() {
				return []contract.MetadataEvent{ignored(hit, contract.MetadataSetByUserMarker)}
			}
			if event, ok := run.store(ctx, hit, contract.MetadataSetByUserMarker, run.input.Turn); ok {
				return []contract.MetadataEvent{event}
			}
			return nil
		}
	}
	var events []contract.MetadataEvent
	missing := false
	for _, key := range host.metadata.Markers() {
		if _, stored := values[key]; !stored {
			missing = true
		}
	}
	if !missing {
		return nil
	}
	for key, hit := range host.metadata.Replay(run.turn.history) {
		if _, stored := values[key]; stored {
			continue
		}
		setBy := contract.MetadataSetByModelMarker
		if hit.User {
			setBy = contract.MetadataSetByUserMarker
		}
		// Which turn the line came in is not known from one body.
		if event, ok := run.store(ctx, hit, setBy, 0); ok {
			events = append(events, event)
		}
	}
	return events
}

// store keeps hit as the session's value and returns the event that says
// so; nothing when the value is already stored or the write failed.
func (run *Run) store(ctx context.Context, hit metadata.MarkerHit, setBy string, turn int) (contract.MetadataEvent, bool) {
	previous, had := run.values[hit.Key]
	if had && previous.Value == hit.Value {
		return contract.MetadataEvent{}, false
	}
	now := time.Now().UTC()
	record := storage.MetadataValueRecord{
		TargetKind: storage.MetadataTargetSession, TargetID: run.input.Session,
		Key: hit.Key, Value: hit.Value, SetBy: setBy, Turn: turn, CreatedAt: now, UpdatedAt: now,
	}
	if err := run.host.bindings.PutMetadataValue(ctx, record); err != nil {
		run.host.logf("bind the session's metadata: %v", err)
		return contract.MetadataEvent{}, false
	}
	run.values[hit.Key] = record
	event := contract.MetadataEvent{Key: hit.Key, Event: contract.MetadataEventBound, Value: hit.Value, SetBy: setBy}
	if had {
		event.Event = contract.MetadataEventRebound
	}
	return event, true
}

func ignored(hit metadata.MarkerHit, setBy string) contract.MetadataEvent {
	return contract.MetadataEvent{Key: hit.Key, Event: contract.MetadataEventIgnored, Value: hit.Value, SetBy: setBy, Reason: hit.Reason}
}

// NewMarkerScanner returns a scanner for the reply's text, or nil when no
// attribute has a marker.
func (run *Run) NewMarkerScanner() *metadata.MarkerScanner {
	if run == nil || run.host.metadata == nil {
		return nil
	}
	return run.host.metadata.NewMarkerScanner()
}

// Finish binds the session to what the reply's marker lines say, when the
// request succeeded and the session has no value yet; a model cannot rebind
// a session. The captures that waited for the reply then run with the new
// value, and the next call of the turn settles the turn again and captures
// nothing more. Finish returns what the reply changed or tried to.
func (run *Run) Finish(ctx context.Context, hits []metadata.MarkerHit, succeeded bool) []contract.MetadataEvent {
	if run == nil {
		return nil
	}
	defer run.settleAwaited()
	if len(hits) == 0 {
		return nil
	}
	var events []contract.MetadataEvent
	bound := false
	for _, hit := range hits {
		if !hit.Valid() {
			events = append(events, ignored(hit, contract.MetadataSetByModelMarker))
			continue
		}
		if run.values == nil || !succeeded {
			continue
		}
		if _, stored := run.values[hit.Key]; stored {
			hit.Reason = contract.MetadataIgnoredAlreadyBound
			events = append(events, ignored(hit, contract.MetadataSetByModelMarker))
			continue
		}
		storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bindingTimeout)
		event, ok := run.store(storeCtx, hit, contract.MetadataSetByModelMarker, run.input.Turn)
		cancel()
		if ok {
			events = append(events, event)
			bound = true
		}
	}
	if bound {
		host := run.host
		host.sessions.mutex.Lock()
		for _, current := range run.parts {
			state := host.sessions.plugin(run.input.Session, current.plugin.Key)
			state.settled = ""
			state.bindingTurn = run.turnKey
		}
		host.sessions.mutex.Unlock()
	}
	return clampEvents(events)
}

// settleAwaited queues the captures that waited for the reply, with the
// values it bound. A plugin whose attributes the session still lacks
// captures nothing. While the privacy check of the turn's text runs, the
// captures wait for it.
func (run *Run) settleAwaited() {
	if !run.checked() {
		go func() {
			<-run.ready
			run.settleAwaited()
		}()
		return
	}
	run.mutex.Lock()
	defer run.mutex.Unlock()
	for _, current := range run.parts {
		if current.observe != observeAwaiting {
			continue
		}
		meta, complete := run.boundMeta(current)
		if !complete || current.awaited == nil {
			current.observe = observeSkippedUnbound
			continue
		}
		view := *current.awaited
		view.Meta, current.info.Meta = meta, meta
		current.observe = run.queue(current, view)
	}
}

// boundMeta returns the plugin's view of the session's values with those
// the reply bound, and whether it now has a value of each of the plugin's
// attributes.
func (run *Run) boundMeta(current *part) (map[string]plugin.MetaValue, bool) {
	meta := maps.Clone(current.view.Meta)
	if meta == nil {
		meta = map[string]plugin.MetaValue{}
	}
	for _, attribute := range current.plugin.Meta.Metadata {
		if _, found := meta[attribute.Key]; found {
			continue
		}
		bound, found := run.values[attribute.Key]
		if !found {
			return nil, false
		}
		meta[attribute.Key] = plugin.MetaValue{Value: bound.Value, Source: contract.MetadataSourceSession}
	}
	return meta, true
}

// Captures returns what became of the turn's capture for each plugin that
// captures, by plugin key; for those that waited for the reply, what Finish
// decided.
func (run *Run) Captures() map[string]string {
	if run == nil {
		return nil
	}
	run.mutex.Lock()
	defer run.mutex.Unlock()
	captures := map[string]string{}
	for _, current := range run.parts {
		if current.observe != "" && current.observe != "pending" {
			captures[current.plugin.Key] = current.observe
		}
	}
	return captures
}
