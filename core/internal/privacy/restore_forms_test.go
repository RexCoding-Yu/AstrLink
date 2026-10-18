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

// TestTextRestorerHoldsSpellingCutShortUntilFinal pins the streaming contract:
// an incomplete spelling at the end of non-final text is held back, and final
// text resolves it.
func TestTextRestorerHoldsSpellingCutShortUntilFinal(t *testing.T) {
	restorer := NewTextRestorer([]Redaction{
		{Placeholder: testPersonMarker, Kind: KindPerson, Value: "Zoë"},
	}, ValuePlain)

	restored, count, hold := restorer.Restore("Hi &lt;PRIVATE_PER", false)
	if restored != "Hi " || count != 0 || hold != len("&lt;PRIVATE_PER") {
		t.Fatalf("partial marker: %q count=%d hold=%d", restored, count, hold)
	}
	restored, count, hold = restorer.Restore("&lt;PRIVATE_PERSON_6ad1158cae28c183&gt; ok", false)
	if restored != "Zoë ok" || count != 1 || hold != 0 {
		t.Fatalf("completed marker: %q count=%d hold=%d", restored, count, hold)
	}

	// A marker without its brackets could still continue into a longer word.
	input := "By PRIVATE_PERSON_6ad1158cae28c183"
	restored, _, hold = restorer.Restore(input, false)
	if restored != "By " || hold != len("PRIVATE_PERSON_6ad1158cae28c183") {
		t.Fatalf("unbounded bare marker: %q hold=%d", restored, hold)
	}
	if restored, count, hold = restorer.Restore(input, true); restored != "By Zoë" ||
		count != 1 || hold != 0 {
		t.Fatalf("final bare marker: %q count=%d hold=%d", restored, count, hold)
	}
	if restored, count, hold = restorer.Restore("Hi &lt;PRIVATE_PER", true); restored != "Hi &lt;PRIVATE_PER" ||
		count != 0 || hold != 0 {
		t.Fatalf("final partial: %q count=%d hold=%d", restored, count, hold)
	}
}

// TestAllocatorKeepsMarkerQuotedInAnotherSpelling covers a client that resends
// an unrestored marker escaped or without its brackets: it is still this
// value's marker, so the value keeps it.
func TestAllocatorKeepsMarkerQuotedInAnotherSpelling(t *testing.T) {
	key := testDerivationKey(7)
	free := newPlaceholderAllocator(key, naturalKindRule, nil)
	marker, _, err := free.allocate(KindPerson, "Zoë")
	if err != nil {
		t.Fatal(err)
	}
	inner := strings.Trim(marker, "<>")
	for _, spelling := range []string{"&lt;" + inner + "&gt;", `\u003c` + inner + `\u003e`, inner} {
		body := []byte(`{"input":"Zoë, ` + spelling + `"}`)
		replay := newPlaceholderAllocator(key, naturalKindRule, body)
		if got, _, err := replay.allocate(KindPerson, "Zoë"); err != nil || got != marker {
			t.Fatalf("marker = %q (%v), want %q quoted as %q", got, err, marker, spelling)
		}
	}
}
