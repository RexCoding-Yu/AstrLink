package privacy

import (
	"container/list"
	"context"
	"sync"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type conversationKey struct{}

// WithConversation names the conversation a request belongs to, so an IPv4
// stand-in stays with its value for every turn of it.
func WithConversation(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, conversationKey{}, id)
}

func conversationFrom(ctx context.Context) string {
	id, _ := ctx.Value(conversationKey{}).(string)
	return id
}

// maxLedgerConversations bounds the memory. An evicted conversation is read
// back from the store when it returns; without a store, its IPv4 stand-ins
// lose the guarantee below.
const maxLedgerConversations = 1024

// standInLedgers remember, per conversation, which value each IPv4 stand-in was
// issued to. That pool is too small for a stand-in to identify its value: an
// address the request already holds may be this value's stand-in quoted back
// from unrestored reasoning, another value's, or documentation text. Values are
// recorded by keyed fingerprint, never in plain text. The store, given only
// with a derivation key that outlives the process, keeps the record across
// restarts; without it the memory lives no longer than the process key the
// fingerprints depend on.
type standInLedgers struct {
	mutex  sync.Mutex
	byID   map[string]*list.Element
	recent list.List
	store  storage.PrivacyStandInStore
}

// conversation returns the conversation's ledger, read from the store the first
// time this process uses it. It is nil when the request names no conversation
// or the store cannot be read, which leaves the rule for requests without one
// in charge.
func (ledgers *standInLedgers) conversation(ctx context.Context, id string) *standInLedger {
	if id == "" {
		return nil
	}
	ledger := ledgers.entry(id)
	if !ledger.load(ctx) {
		return nil
	}
	return ledger
}

func (ledgers *standInLedgers) entry(id string) *standInLedger {
	ledgers.mutex.Lock()
	defer ledgers.mutex.Unlock()
	if element, exists := ledgers.byID[id]; exists {
		ledgers.recent.MoveToFront(element)
		return element.Value.(*standInLedger)
	}
	if ledgers.byID == nil {
		ledgers.byID = make(map[string]*list.Element)
	}
	ledger := &standInLedger{
		id:        id,
		store:     ledgers.store,
		byOwner:   make(map[string]string),
		byStandIn: make(map[string]string),
	}
	ledgers.byID[id] = ledgers.recent.PushFront(ledger)
	if ledgers.recent.Len() > maxLedgerConversations {
		oldest := ledgers.recent.Back()
		ledgers.recent.Remove(oldest)
		delete(ledgers.byID, oldest.Value.(*standInLedger).id)
	}
	return ledger
}

// standInLedger is one conversation's record. A stand-in once issued is never
// released to another value, because the history may still quote it.
type standInLedger struct {
	id        string
	store     storage.PrivacyStandInStore
	mutex     sync.Mutex
	loaded    bool
	byOwner   map[string]string
	byStandIn map[string]string
	// unsaved holds the stand-ins issued since the store last took them.
	unsaved []storage.PrivacyStandIn
}

// load reads the conversation's stand-ins from the store once. Nothing is
// issued from a ledger that failed to load, so the store's record and the
// memory never disagree.
func (ledger *standInLedger) load(ctx context.Context) bool {
	ledger.mutex.Lock()
	defer ledger.mutex.Unlock()
	if ledger.loaded || ledger.store == nil {
		ledger.loaded = true
		return true
	}
	standIns, err := ledger.store.PrivacyStandIns(ctx, contract.SessionID(ledger.id))
	if err != nil {
		return false
	}
	for _, standIn := range standIns {
		ledger.byStandIn[standIn.StandIn] = standIn.Owner
		ledger.byOwner[standIn.Owner] = standIn.StandIn
	}
	ledger.loaded = true
	return true
}

// save hands the stand-ins issued since the last save to the store. A failed
// write keeps them for the conversation's next request.
func (ledger *standInLedger) save(ctx context.Context) {
	if ledger == nil || ledger.store == nil {
		return
	}
	ledger.mutex.Lock()
	unsaved := ledger.unsaved
	ledger.unsaved = nil
	ledger.mutex.Unlock()
	if len(unsaved) == 0 {
		return
	}
	if ledger.store.RememberPrivacyStandIns(ctx, contract.SessionID(ledger.id), unsaved) != nil {
		ledger.mutex.Lock()
		ledger.unsaved = append(unsaved, ledger.unsaved...)
		ledger.mutex.Unlock()
	}
}

// standInOf returns the stand-in the value was last issued.
func (ledger *standInLedger) standInOf(owner string) (string, bool) {
	ledger.mutex.Lock()
	defer ledger.mutex.Unlock()
	standIn, exists := ledger.byOwner[owner]
	return standIn, exists
}

// claim issues a stand-in to the value. One already issued to it is always
// granted, wherever the request quotes it; one issued to another value never
// is; an unissued one is refused when the request already holds it, since that
// occurrence is text this conversation never produced.
func (ledger *standInLedger) claim(standIn, owner string, held func() bool) bool {
	ledger.mutex.Lock()
	defer ledger.mutex.Unlock()
	switch ledger.byStandIn[standIn] {
	case owner:
	case "":
		if held() {
			return false
		}
		ledger.byStandIn[standIn] = owner
		if ledger.store != nil {
			ledger.unsaved = append(ledger.unsaved, storage.PrivacyStandIn{StandIn: standIn, Owner: owner})
		}
	default:
		return false
	}
	ledger.byOwner[owner] = standIn
	return true
}
