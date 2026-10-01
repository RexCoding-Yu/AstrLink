package privacy

import (
	"strings"
	"testing"
)

const testPersonMarker = "<PRIVATE_PERSON_6ad1158cae28c183>"

func TestTextRestorerRestoresMarkerInTheSyntaxItWasWrittenIn(t *testing.T) {
	restorer := NewTextRestorer([]Redaction{{
		Placeholder: testPersonMarker,
		Kind:        KindPerson,
		Value:       `Zoë "Z" O'Neil & Co`,
	}}, ValuePlain)
	for _, test := range []struct {
		name, input, want string
	}{
		{"exact", "by " + testPersonMarker + ".", `by Zoë "Z" O'Neil & Co.`},
		{
			"html named entities",
			"<footer>&lt;PRIVATE_PERSON_6ad1158cae28c183&gt;</footer>",
			"<footer>Zoë &#34;Z&#34; O&#39;Neil &amp; Co</footer>",
		},
		{
			"html numeric entities",
			"&#60;PRIVATE_PERSON_6ad1158cae28c183&#X3E;",
			"Zoë &#34;Z&#34; O&#39;Neil &amp; Co",
		},
		{
			"json unicode escapes",
			`{"name": "\u003cPRIVATE_PERSON_6ad1158cae28c183\u003E"}`,
			`{"name": "Zoë \"Z\" O'Neil & Co"}`,
		},
		{
			"percent encoding",
			"mailto:x?subject=%3cPRIVATE_PERSON_6ad1158cae28c183%3E",
			"mailto:x?subject=Zo%C3%AB%20%22Z%22%20O%27Neil%20%26%20Co",
		},
		{"brackets dropped", "Name: PRIVATE_PERSON_6ad1158cae28c183, ok", `Name: Zoë "Z" O'Neil & Co, ok`},
		{"inside identifier", "XPRIVATE_PERSON_6ad1158cae28c183", "XPRIVATE_PERSON_6ad1158cae28c183"},
		{"longer identifier", "PRIVATE_PERSON_6ad1158cae28c183a", "PRIVATE_PERSON_6ad1158cae28c183a"},
		{
			"escaped backslash",
			`\\u003cPRIVATE_PERSON_6ad1158cae28c183\u003e`,
			`\\u003cPRIVATE_PERSON_6ad1158cae28c183\u003e`,
		},
		{"other suffix", "&lt;PRIVATE_PERSON_6ad1158cae28c184&gt;", "&lt;PRIVATE_PERSON_6ad1158cae28c184&gt;"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, _, hold := restorer.Restore(test.input, true)
			if got != test.want || hold != 0 {
				t.Fatalf("Restore(%q) = %q hold=%d, want %q", test.input, got, hold, test.want)
			}
		})
	}
}

// TestTextRestorerEscapesForSerializedJSON covers text that itself holds JSON:
// every spelling must leave the inner document valid.
func TestTextRestorerEscapesForSerializedJSON(t *testing.T) {
	restorer := NewTextRestorer([]Redaction{{
		Placeholder: testPersonMarker,
		Kind:        KindPerson,
		Value:       `Ann "A" Lee`,
	}}, ValueJSONString)
	input := `{"a":"` + testPersonMarker + `","b":"&lt;PRIVATE_PERSON_6ad1158cae28c183&gt;"}`
	got, count, _ := restorer.Restore(input, true)
	want := `{"a":"Ann \"A\" Lee","b":"Ann &#34;A&#34; Lee"}`
	if got != want || count != 2 {
		t.Fatalf("Restore = %q count=%d, want %q", got, count, want)
	}
}

func TestTextRestorerDoesNotBareMatchLegacyMarker(t *testing.T) {
	restorer := NewTextRestorer([]Redaction{{
		Placeholder: "<PRIVATE_EMAIL>",
		Kind:        KindEmail,
		Value:       "alice@example.com",
	}}, ValuePlain)
	input := "PRIVATE_EMAIL and &lt;PRIVATE_EMAIL&gt;"
	if got, count, _ := restorer.Restore(input, true); got != input || count != 0 {
		t.Fatalf("legacy marker body restored: %q", got)
	}
}

// TestTextRestorerRestoresRegroupedStandIns covers models that rewrite a
// natural stand-in in a local convention, which is what they do with a phone
// number written into an HTML page.
func TestTextRestorerRestoresRegroupedStandIns(t *testing.T) {
	restorer := NewTextRestorer([]Redaction{
		{Placeholder: "+1-555-555-0152", Kind: KindPhone, Value: "+86 138 0013 8000"},
		{Placeholder: "4000000000000123", Kind: KindPaymentCard, Value: "4242424242424242"},
		{Placeholder: "XX00REDACTED0000000042", Kind: KindAccount, Value: "DE89370400440532013000"},
	}, ValuePlain)
	for _, test := range []struct {
		input, want string
	}{
		{"+1 (555) 555-0152", "+86 138 0013 8000"},
		{`href="tel:+15555550152"`, `href="tel:+86 138 0013 8000"`},
		{"call (555) 555-0152.", "call +86 138 0013 8000."},
		{"555.555.0152", "+86 138 0013 8000"},
		{"1-555-555-0152", "+86 138 0013 8000"},
		{"+1-555-555-0152", "+86 138 0013 8000"},
		{"55555501523", "55555501523"},
		{"+155555501520", "+155555501520"},
		{"x5555550152", "x5555550152"},
		{"+1 (555) 555-0153", "+1 (555) 555-0153"},
		{"4000 0000 0000 0123", "4242424242424242"},
		{"4000-0000-0000-0123", "4242424242424242"},
		{"XX00 REDA CTED 0000 0000 42", "DE89370400440532013000"},
	} {
		got, _, _ := restorer.Restore(test.input, true)
		if got != test.want {
			t.Errorf("Restore(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

// TestTextRestorerHoldsSpellingCutShortUntilFinal pins the streaming contract:
// an incomplete spelling at the end of non-final text is held back, and final
// text resolves it.
func TestTextRestorerHoldsSpellingCutShortUntilFinal(t *testing.T) {
	restorer := NewTextRestorer([]Redaction{
		{Placeholder: testPersonMarker, Kind: KindPerson, Value: "Zoë"},
		{Placeholder: "+1-555-555-0152", Kind: KindPhone, Value: "+86 138 0013 8000"},
	}, ValuePlain)

	restored, count, hold := restorer.Restore("Hi &lt;PRIVATE_PER", false)
	if restored != "Hi " || count != 0 || hold != len("&lt;PRIVATE_PER") {
		t.Fatalf("partial marker: %q count=%d hold=%d", restored, count, hold)
	}
	restored, count, hold = restorer.Restore("&lt;PRIVATE_PERSON_6ad1158cae28c183&gt; ok", false)
	if restored != "Zoë ok" || count != 1 || hold != 0 {
		t.Fatalf("completed marker: %q count=%d hold=%d", restored, count, hold)
	}

	// The number could still continue with another digit.
	input := "Call +1 (555) 555-0152"
	restored, _, hold = restorer.Restore(input, false)
	if restored != "Call " || hold != len("+1 (555) 555-0152") {
		t.Fatalf("unbounded phone: %q hold=%d", restored, hold)
	}
	if restored, count, hold = restorer.Restore(input, true); restored != "Call +86 138 0013 8000" ||
		count != 1 || hold != 0 {
		t.Fatalf("final phone: %q count=%d hold=%d", restored, count, hold)
	}
	if restored, count, hold = restorer.Restore("Hi &lt;PRIVATE_PER", true); restored != "Hi &lt;PRIVATE_PER" ||
		count != 0 || hold != 0 {
		t.Fatalf("final partial: %q count=%d hold=%d", restored, count, hold)
	}
}

// TestAllocatorRederivesWhenRestorableSpellingOccursInBody extends the literal
// collision guard to every spelling the restorer recognises: an escaped marker
// or a regrouped number already in the request is genuine text too.
func TestAllocatorRederivesWhenRestorableSpellingOccursInBody(t *testing.T) {
	key := testDerivationKey(7)
	free := newPlaceholderAllocator(key, naturalKindRule, nil)
	marker, _, err := free.allocate(KindPerson, "Zoë")
	if err != nil {
		t.Fatal(err)
	}
	phone, _, err := free.allocate(KindPhone, "+86 138 0013 8000")
	if err != nil {
		t.Fatal(err)
	}
	escaped := "&lt;" + strings.Trim(marker, "<>") + "&gt;"
	regrouped := "+1 (555) 555-" + phone[len(phone)-4:]
	body := []byte(`{"input":"` + escaped + ` and ` + regrouped + `"}`)
	constrained := newPlaceholderAllocator(key, naturalKindRule, body)
	if avoided, _, err := constrained.allocate(KindPerson, "Zoë"); err != nil || avoided == marker {
		t.Fatalf("marker = %q (%v), already present as %q", avoided, err, escaped)
	}
	if avoided, _, err := constrained.allocate(KindPhone, "+86 138 0013 8000"); err != nil || avoided == phone {
		t.Fatalf("phone = %q (%v), already present as %q", avoided, err, regrouped)
	}
}
