package pluginhost

import (
	"container/list"
	"sync"
)

// maxSessions bounds the sessions whose plugin state the host keeps. A
// session it dropped starts over: its resident block is computed again and
// its next call settles its turn again.
const maxSessions = 1024

// pluginState is what the host keeps for one plugin in one session.
type pluginState struct {
	// residentKey names what the resident block was computed from;
	// resident is the block, rendered, and residentTitle the title of the
	// record that came with it.
	residentKey   string
	resident      string
	residentTitle string
	computed      bool
	// settled names the turn whose block is settled, "" for none, and
	// turnTitle is the title of the record that settled it.
	settled   string
	turnTitle string
	// bindingTurn names the turn a model marker bound the session in: its
	// later calls settle the turn again but capture nothing.
	bindingTurn string
}

type sessionState struct {
	id      string
	plugins map[string]*pluginState
}

// sessions keeps the state of the sessions used most recently. Callers hold
// mutex while they read or change a state.
type sessions struct {
	mutex sync.Mutex
	order *list.List
	byID  map[string]*list.Element
	max   int
}

func newSessions() *sessions {
	return &sessions{order: list.New(), byID: map[string]*list.Element{}, max: maxSessions}
}

// plugin returns the state of plugin in session, creating it. The caller
// holds mutex.
func (cache *sessions) plugin(session, plugin string) *pluginState {
	var state *sessionState
	if element, found := cache.byID[session]; found {
		cache.order.MoveToFront(element)
		state = element.Value.(*sessionState)
	} else {
		state = &sessionState{id: session, plugins: map[string]*pluginState{}}
		cache.byID[session] = cache.order.PushFront(state)
		for cache.order.Len() > cache.max {
			oldest := cache.order.Back()
			cache.order.Remove(oldest)
			delete(cache.byID, oldest.Value.(*sessionState).id)
		}
	}
	entry, found := state.plugins[plugin]
	if !found {
		entry = &pluginState{}
		state.plugins[plugin] = entry
	}
	return entry
}
