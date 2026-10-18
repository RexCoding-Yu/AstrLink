package privacy

import (
	"container/list"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

const (
	// defaultWarningEvents bounds the feed the desktop polls; one poll every
	// few seconds reads far fewer.
	defaultWarningEvents = 128
	// defaultWarningFingerprints bounds the values the feed remembers. An
	// agent resends its whole history on every call, so a value forgotten too
	// early notifies again on the next call.
	defaultWarningFingerprints = 16_384
)

// Warning is one request the warn action let through: which request, and what
// was found. Notified is the part of Findings whose kinds notify, and
// Fingerprints identify their values without holding them.
type Warning struct {
	RequestID    contract.RequestID
	ClientType   contract.ClientType
	Protocol     contract.ProtocolID
	ServiceID    contract.ServiceID
	Findings     []Finding
	Notified     []Finding
	Fingerprints []Fingerprint
}

// WarningEvent is one warning as the feed serves it. It names the kinds that
// notify and their counts, never a value or a fingerprint.
type WarningEvent struct {
	Sequence   uint64              `json:"sequence"`
	At         time.Time           `json:"at"`
	RequestID  contract.RequestID  `json:"request_id,omitempty"`
	ClientType contract.ClientType `json:"client_type,omitempty"`
	Kinds      []WarningKindCount  `json:"kinds"`
	// Notifiable is set when the request carried a value no earlier warning of
	// this process carried. A conversation resends its history on every call,
	// so a value already warned about would otherwise notify each time.
	Notifiable bool `json:"notifiable"`
}

// WarningPage is the part of the feed after a sequence.
type WarningPage struct {
	// Epoch names this process's feed. Sequences start over with Core, so a
	// reader that sees another epoch reads the feed again from the start.
	Epoch  string         `json:"epoch"`
	Events []WarningEvent `json:"events"`
}

// WarningFeed keeps recent warnings in memory for the desktop to poll and
// decides which of them are worth a notification. Nothing in it is stored.
type WarningFeed struct {
	mu       sync.Mutex
	epoch    string
	sequence uint64
	events   []WarningEvent
	// seen holds the fingerprints of values already warned about; the least
	// recently seen is at the back of order.
	seen            map[Fingerprint]*list.Element
	order           *list.List
	maxEvents       int
	maxFingerprints int
	now             func() time.Time
}

func NewWarningFeed() *WarningFeed {
	epoch := make([]byte, 8)
	if _, err := rand.Read(epoch); err != nil {
		panic("privacy: warning feed epoch is unavailable: " + err.Error())
	}
	return &WarningFeed{
		epoch:           hex.EncodeToString(epoch),
		seen:            make(map[Fingerprint]*list.Element),
		order:           list.New(),
		maxEvents:       defaultWarningEvents,
		maxFingerprints: defaultWarningFingerprints,
		now:             time.Now,
	}
}

// Report appends a warning and returns the event it became. A value seen again
// counts as recent, so a secret that stays in a conversation stays known for
// as long as the conversation goes on. A warning with no finding of a kind
// that notifies is left out, and Report returns false.
func (feed *WarningFeed) Report(warning Warning) (WarningEvent, bool) {
	if len(warning.Notified) == 0 {
		return WarningEvent{}, false
	}
	feed.mu.Lock()
	defer feed.mu.Unlock()
	notifiable := false
	for _, fingerprint := range warning.Fingerprints {
		if element, exists := feed.seen[fingerprint]; exists {
			feed.order.MoveToFront(element)
			continue
		}
		notifiable = true
		feed.seen[fingerprint] = feed.order.PushFront(fingerprint)
		if feed.order.Len() > feed.maxFingerprints {
			oldest := feed.order.Back()
			feed.order.Remove(oldest)
			delete(feed.seen, oldest.Value.(Fingerprint))
		}
	}
	feed.sequence++
	event := WarningEvent{
		Sequence:   feed.sequence,
		At:         feed.now().UTC(),
		RequestID:  warning.RequestID,
		ClientType: warning.ClientType,
		Kinds:      warningKindCounts(warning.Notified),
		Notifiable: notifiable,
	}
	if len(feed.events) == feed.maxEvents {
		copy(feed.events, feed.events[1:])
		feed.events = feed.events[:feed.maxEvents-1]
	}
	feed.events = append(feed.events, event)
	return event, true
}

// Since returns the events after sequence, oldest first.
func (feed *WarningFeed) Since(sequence uint64) WarningPage {
	feed.mu.Lock()
	defer feed.mu.Unlock()
	page := WarningPage{Epoch: feed.epoch, Events: []WarningEvent{}}
	for _, event := range feed.events {
		if event.Sequence > sequence {
			page.Events = append(page.Events, event)
		}
	}
	return page
}
