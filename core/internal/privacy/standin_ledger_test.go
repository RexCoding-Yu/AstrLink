package privacy

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// memoryStandIns outlives the engines a test restarts. failReads fails every
// read; failWrites fails that many writes.
type memoryStandIns struct {
	mutex      sync.Mutex
	rows       map[contract.SessionID][]storage.PrivacyStandIn
	failReads  bool
	failWrites int
}

func (store *memoryStandIns) PrivacyStandIns(_ context.Context, id contract.SessionID) ([]storage.PrivacyStandIn, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.failReads {
		return nil, errors.New("stand-in store unavailable")
	}
	return slices.Clone(store.rows[id]), nil
}

func (store *memoryStandIns) RememberPrivacyStandIns(_ context.Context, id contract.SessionID, standIns []storage.PrivacyStandIn) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.failWrites > 0 {
		store.failWrites--
		return errors.New("stand-in store unavailable")
	}
	if store.rows == nil {
		store.rows = make(map[contract.SessionID][]storage.PrivacyStandIn)
	}
	for _, standIn := range standIns {
		if !slices.ContainsFunc(store.rows[id], func(row storage.PrivacyStandIn) bool { return row.StandIn == standIn.StandIn }) {
			store.rows[id] = append(store.rows[id], standIn)
		}
	}
	return nil
}

// startEngine is one Core process: a fresh engine keyed by the device's audit
// key.
func startEngine(t *testing.T, auditKey []byte, store storage.PrivacyStandInStore) *Engine {
	t.Helper()
	engine := mustTestEngine(t)
	if err := engine.PersistPlaceholders(auditKey, store); err != nil {
		t.Fatal(err)
	}
	return engine
}

func placeholderOf(t *testing.T, result Result, kind Kind) string {
	t.Helper()
	for _, redaction := range result.Redactions {
		if redaction.Kind == kind {
			return redaction.Placeholder
		}
	}
	t.Fatalf("no %s redaction in %#v", kind, result.Redactions)
	return ""
}

func allocateIPv4(t *testing.T, key []byte, ledger *standInLedger, body, value string) string {
	t.Helper()
	allocator := newPlaceholderAllocator(key, naturalKindRule, []byte(body))
	allocator.ledger = ledger
	standIn, style, err := allocator.allocate(KindIPAddress, value)
	if err != nil || style != contract.PlaceholderStyleNatural {
		t.Fatalf("allocate %q: %q %q %v", value, standIn, style, err)
	}
	return standIn
}

// collidingIPv4 finds another address whose first stand-in is the same as
// value's: with 768 stand-ins in the pool, any two values may meet.
func collidingIPv4(t *testing.T, key []byte, value string) string {
	t.Helper()
	want := allocateIPv4(t, key, nil, "", value)
	for index := range 1 << 16 {
		other := "10.9." + strconv.Itoa(index/256) + "." + strconv.Itoa(index%256)
		if other != value && allocateIPv4(t, key, nil, "", other) == want {
			return other
		}
	}
	t.Fatal("no colliding address found")
	return ""
}

// TestLedgerKeepsIPv4StandInQuotedBack is the IPv4 form of a placeholder that
// reasoning quoted and the client resent: the conversation issued it to this
// value, so the value keeps it instead of being renamed.
func TestLedgerKeepsIPv4StandInQuotedBack(t *testing.T) {
	key := testDerivationKey(11)
	var ledgers standInLedgers
	ledger := ledgers.conversation(t.Context(), "session_one")
	first := allocateIPv4(t, key, ledger, `{"input":"ssh 10.4.7.9"}`, "10.4.7.9")
	echo := `{"input":["ssh 10.4.7.9","connecting to ` + first + `"]}`
	if got := allocateIPv4(t, key, ledger, echo, "10.4.7.9"); got != first {
		t.Fatalf("stand-in %q became %q after reasoning quoted it", first, got)
	}
	if got := allocateIPv4(t, key, nil, echo, "10.4.7.9"); got == first {
		t.Fatalf("without a conversation the quoted stand-in %q was reused", got)
	}
}

// TestLedgerKeepsAnotherValuesStandInFromValue covers history that lost a value
// but still quotes its stand-in: handing that stand-in to a new value would
// restore the quote as the wrong address.
func TestLedgerKeepsAnotherValuesStandInFromValue(t *testing.T) {
	key := testDerivationKey(12)
	value := "10.4.7.9"
	other := collidingIPv4(t, key, value)
	var ledgers standInLedgers
	ledger := ledgers.conversation(t.Context(), "session_one")
	taken := allocateIPv4(t, key, ledger, `{"input":"ping `+other+`"}`, other)
	for _, body := range []string{
		`{"input":["earlier: ` + taken + `","ssh ` + value + `"]}`,
		`{"input":"ssh ` + value + `"}`,
	} {
		if got := allocateIPv4(t, key, ledger, body, value); got == taken {
			t.Fatalf("%s got %q, which the conversation issued to %s", value, got, other)
		}
	}
}

// TestLedgerKeepsRederivedStandInOnceTextLeaves pins the name once issued: a
// value pushed off its first stand-in by documentation text keeps the second
// even after that text leaves the history.
func TestLedgerKeepsRederivedStandInOnceTextLeaves(t *testing.T) {
	key := testDerivationKey(13)
	preferred := allocateIPv4(t, key, nil, "", "10.4.7.9")
	var ledgers standInLedgers
	ledger := ledgers.conversation(t.Context(), "session_one")
	pushed := allocateIPv4(t, key, ledger,
		`{"input":["docs use `+preferred+`","ssh 10.4.7.9"]}`, "10.4.7.9")
	if pushed == preferred {
		t.Fatalf("stand-in %q matched documentation text in the first request", pushed)
	}
	if got := allocateIPv4(t, key, ledger, `{"input":"ssh 10.4.7.9"}`, "10.4.7.9"); got != pushed {
		t.Fatalf("stand-in %q became %q once the documentation text left", pushed, got)
	}
}

// TestLedgersDoNotCrossConversations keeps one conversation's issue from
// vouching for text in another, where the same address is documentation.
func TestLedgersDoNotCrossConversations(t *testing.T) {
	key := testDerivationKey(14)
	var ledgers standInLedgers
	issued := allocateIPv4(t, key, ledgers.conversation(t.Context(), "session_a"), `{"input":"ssh 10.4.7.9"}`, "10.4.7.9")
	body := `{"input":["docs use ` + issued + `","ssh 10.4.7.9"]}`
	if got := allocateIPv4(t, key, ledgers.conversation(t.Context(), "session_b"), body, "10.4.7.9"); got == issued {
		t.Fatalf("another conversation's stand-in %q was reused over documentation text", got)
	}
}

func TestLedgersEvictLeastRecentConversation(t *testing.T) {
	var ledgers standInLedgers
	first := ledgers.conversation(t.Context(), "session_first")
	kept := ledgers.conversation(t.Context(), "session_kept")
	for index := range maxLedgerConversations - 2 {
		ledgers.conversation(t.Context(), "session_"+strconv.Itoa(index))
	}
	if ledgers.conversation(t.Context(), "session_kept") != kept {
		t.Fatal("conversation within the bound was dropped")
	}
	ledgers.conversation(t.Context(), "session_overflow")
	if ledgers.recent.Len() != maxLedgerConversations {
		t.Fatalf("ledgers hold %d conversations", ledgers.recent.Len())
	}
	if ledgers.conversation(t.Context(), "session_first") == first {
		t.Fatal("least recent conversation was not evicted")
	}
	if ledgers.conversation(t.Context(), "session_kept") != kept {
		t.Fatal("recently used conversation was evicted")
	}
	if ledgers.conversation(t.Context(), "") != nil {
		t.Fatal("an unnamed request got a ledger")
	}
}

func TestIPv4StandInSurvivesBeingQuotedInReasoning(t *testing.T) {
	engine := mustTestEngine(t)
	policy := naturalPolicy()
	ctx := WithConversation(t.Context(), "session_one")
	user := `{"role":"user","content":[{"type":"input_text","text":"ssh 10.4.7.9"}]}`
	first, err := engine.Inspect(ctx, policy, contract.ProtocolOpenAIResponses, []byte(`{"input":[`+user+`]}`))
	if err != nil || len(first.Redactions) != 1 {
		t.Fatalf("first result=%#v error=%v", first, err)
	}
	standIn := first.Redactions[0].Placeholder
	reasoning := `{"type":"reasoning","id":"rs_1","summary":[],"content":[` +
		`{"type":"reasoning_text","text":"Connect to ` + standIn + ` first."}]}`
	second, err := engine.Inspect(ctx, policy, contract.ProtocolOpenAIResponses,
		[]byte(`{"input":[`+user+`,`+reasoning+`]}`))
	if err != nil || len(second.Redactions) != 1 {
		t.Fatalf("second result=%#v error=%v", second, err)
	}
	if got := second.Redactions[0].Placeholder; got != standIn {
		t.Fatalf("stand-in changed from %q to %q once reasoning quoted it", standIn, got)
	}
	if count := strings.Count(string(second.Body), standIn); count != 2 {
		t.Fatalf("stand-in occurs %d times, want 2: %s", count, second.Body)
	}
}

// TestPersistedPlaceholdersSurviveRestart is a conversation that goes on after
// Core restarts, with reasoning from before the restart quoting both
// placeholders. Neither value is renamed, and the IPv4 stand-in keeps its name
// only because the conversation's ledger was stored.
func TestPersistedPlaceholdersSurviveRestart(t *testing.T) {
	auditKey := testDerivationKey(21)
	store := &memoryStandIns{}
	policy := naturalPolicy()
	ctx := WithConversation(t.Context(), "session_one")
	user := `{"role":"user","content":[{"type":"input_text","text":"mail alice@example.com, then ssh 10.4.7.9"}]}`
	first, err := startEngine(t, auditKey, store).Inspect(ctx, policy, contract.ProtocolOpenAIResponses,
		[]byte(`{"input":[`+user+`]}`))
	if err != nil || len(first.Redactions) != 2 {
		t.Fatalf("first result=%#v error=%v", first, err)
	}
	email, address := placeholderOf(t, first, KindEmail), placeholderOf(t, first, KindIPAddress)
	reasoning := `{"type":"reasoning","id":"rs_1","summary":[],"content":[` +
		`{"type":"reasoning_text","text":"Mail ` + email + ` before connecting to ` + address + `."}]}`
	resumed := []byte(`{"input":[` + user + `,` + reasoning + `]}`)

	second, err := startEngine(t, auditKey, store).Inspect(ctx, policy, contract.ProtocolOpenAIResponses, resumed)
	if err != nil {
		t.Fatal(err)
	}
	if got := placeholderOf(t, second, KindEmail); got != email {
		t.Fatalf("email placeholder %q became %q after a restart", email, got)
	}
	if got := placeholderOf(t, second, KindIPAddress); got != address {
		t.Fatalf("IPv4 stand-in %q became %q after a restart", address, got)
	}

	unread, err := startEngine(t, auditKey, &memoryStandIns{rows: store.rows, failReads: true}).
		Inspect(ctx, policy, contract.ProtocolOpenAIResponses, resumed)
	if err != nil {
		t.Fatal(err)
	}
	if got := placeholderOf(t, unread, KindEmail); got != email {
		t.Fatalf("email placeholder %q became %q without the ledger", email, got)
	}
	if got := placeholderOf(t, unread, KindIPAddress); got == address {
		t.Fatalf("an unread ledger vouched for the quoted stand-in %q", got)
	}

	other, err := startEngine(t, testDerivationKey(22), &memoryStandIns{}).
		Inspect(ctx, policy, contract.ProtocolOpenAIResponses, []byte(`{"input":[`+user+`]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := placeholderOf(t, other, KindEmail); got == email {
		t.Fatalf("another device's audit key gave the same placeholder %q", got)
	}
}

// TestStoredLedgerKeepsAnotherValuesStandInAcrossRestart covers history that
// lost a value but still quotes its stand-in when Core restarts: only the
// stored ledger knows the stand-in is taken.
func TestStoredLedgerKeepsAnotherValuesStandInAcrossRestart(t *testing.T) {
	key := testDerivationKey(23)
	value := "10.4.7.9"
	other := collidingIPv4(t, key, value)
	store := &memoryStandIns{}
	before := standInLedgers{store: store}
	ledger := before.conversation(t.Context(), "session_one")
	taken := allocateIPv4(t, key, ledger, `{"input":"ping `+other+`"}`, other)
	ledger.save(t.Context())

	after := standInLedgers{store: store}
	if got := allocateIPv4(t, key, after.conversation(t.Context(), "session_one"),
		`{"input":"ssh `+value+`"}`, value); got == taken {
		t.Fatalf("%s got %q, which the conversation issued to %s before the restart", value, got, other)
	}
}

func TestLedgerKeepsStandInsTheStoreRefused(t *testing.T) {
	store := &memoryStandIns{failWrites: 1}
	engine := startEngine(t, testDerivationKey(24), store)
	ctx := WithConversation(t.Context(), "session_one")
	inspect := func(text string) string {
		result, err := engine.Inspect(ctx, naturalPolicy(), contract.ProtocolOpenAIResponses,
			[]byte(`{"input":[{"role":"user","content":[{"type":"input_text","text":"`+text+`"}]}]}`))
		if err != nil {
			t.Fatal(err)
		}
		return placeholderOf(t, result, KindIPAddress)
	}
	refused := inspect("ssh 10.4.7.9")
	if len(store.rows["session_one"]) != 0 {
		t.Fatalf("a refused write stored %#v", store.rows)
	}
	inspect("ssh 10.4.7.9 then 10.4.7.10")
	stored := store.rows["session_one"]
	if len(stored) != 2 || stored[0].StandIn != refused {
		t.Fatalf("stored %#v, want %q first and the next turn's stand-in", stored, refused)
	}
}
