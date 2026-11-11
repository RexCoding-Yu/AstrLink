package privacy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestTokenPlaceholderFragmentsAreSuppressed(t *testing.T) {
	allocator := newPlaceholderAllocator(testDerivationKey(5), tokenKindRule, nil)
	for _, kind := range contract.PrivacyKinds() {
		marker, _, err := allocator.allocate(Kind(kind), "fixture value")
		if err != nil {
			t.Fatal(err)
		}
		for _, test := range []struct {
			name string
			span string
		}{
			{"whole", marker},
			{"body", marker[1 : len(marker)-1]},
			{"suffix", marker[len(marker)-placeholderTokenHexLength-1 : len(marker)-1]},
			{"without opening bracket", marker[1:]},
			{"without closing bracket", marker[:len(marker)-1]},
			{"padded", " \t" + marker + "\n "},
		} {
			t.Run(kind+"/"+test.name, func(t *testing.T) {
				text := "上下文 \t" + marker + "\n 仍需保留"
				encoded, err := json.Marshal(text)
				if err != nil {
					t.Fatal(err)
				}
				const path = "/messages/0/reasoning_content"
				body := []byte(`{"messages":[{"role":"assistant","reasoning_content":` + string(encoded) + `}]}`)
				engine := newTestEngine(t, structuredSpanDetector(t,
					structuredTestSpan{path: path, text: test.span, kind: KindCommonSecret},
				))
				for _, action := range []Action{ActionRedact, ActionBlock, ActionWarn} {
					result, err := engine.Inspect(t.Context(), Policy{
						Enabled: true, Mode: ModeLocalModel, LocalModelID: testLocalModelID, Action: action,
					}, contract.ProtocolOpenAIChat, body)
					if err != nil || result.Decision != DecisionAllow || len(result.Redactions) != 0 || len(result.Findings) != 0 {
						t.Fatalf("%s: placeholder fragment was treated as private: %#v %v", action, result, err)
					}
					if len(result.SuppressedFindings) != 1 {
						t.Fatalf("%s: suppressed findings = %#v", action, result.SuppressedFindings)
					}
					finding := result.SuppressedFindings[0]
					if finding.Suppression != SuppressionPlaceholder || finding.Path != path ||
						text[finding.Start:finding.End] != test.span {
						t.Fatalf("%s: suppressed finding = %#v", action, finding)
					}
				}
			})
		}
	}
}

func TestTokenPlaceholderInReasoningSurvivesRepeatedInspection(t *testing.T) {
	const secret = "abcdefghijklmnop123456"
	first, err := mustTestEngine(t).Inspect(t.Context(), tokenPolicy(), contract.ProtocolOpenAIChat,
		[]byte(`{"messages":[{"role":"user","content":"api_key=`+secret+`"}]}`))
	if err != nil || first.Decision != DecisionRedact || len(first.Redactions) != 1 {
		t.Fatalf("first inspect: %#v %v", first, err)
	}
	marker := first.Redactions[0].Placeholder
	inner := marker[1 : len(marker)-1]
	const reasoningPath = "/messages/0/reasoning_content"
	const contentPath = "/messages/1/content"
	engine := newTestEngine(t, structuredSpanDetector(t,
		structuredTestSpan{path: reasoningPath, text: inner, kind: KindCommonSecret},
		structuredTestSpan{path: contentPath, text: secret, kind: KindCommonSecret},
	))
	engine.derivationKey = testDerivationKey(0)
	policy := tokenPolicy()
	policy.Mode = ModeLocalModel
	policy.LocalModelID = testLocalModelID
	body := []byte(`{"messages":[{"role":"assistant","reasoning_content":"原来的密钥是 ` + marker +
		`"},{"role":"user","content":"api_key=` + secret + `"}]}`)
	second, err := engine.Inspect(t.Context(), policy, contract.ProtocolOpenAIChat, body)
	if err != nil || second.Decision != DecisionRedact || len(second.Redactions) != 1 {
		t.Fatalf("second inspect: %#v %v", second, err)
	}
	if second.Redactions[0].Value != secret || second.Redactions[0].Placeholder != marker {
		t.Fatalf("the original secret changed placeholders: %#v", second.Redactions)
	}
	if len(second.SuppressedFindings) != 1 || second.SuppressedFindings[0].Suppression != SuppressionPlaceholder {
		t.Fatalf("reasoning placeholder was not suppressed: %#v", second.SuppressedFindings)
	}
	want := strings.Replace(string(body), secret, marker, 1)
	if string(second.Body) != want {
		t.Fatalf("replayed body = %s, want %s", second.Body, want)
	}
	engine.modelDetector = structuredSpanDetector(t,
		structuredTestSpan{path: reasoningPath, text: inner, kind: KindCommonSecret},
		structuredTestSpan{path: contentPath, text: inner, kind: KindCommonSecret},
	)
	third, err := engine.Inspect(t.Context(), policy, contract.ProtocolOpenAIChat, second.Body)
	if err != nil || third.Decision != DecisionAllow || len(third.Redactions) != 0 || len(third.SuppressedFindings) != 2 {
		t.Fatalf("third inspect: %#v %v", third, err)
	}
}

func TestTokenPlaceholderGuardKeepsOtherFindings(t *testing.T) {
	const secret = "sk-abcdefghijklmnop123456"
	allocator := newPlaceholderAllocator(testDerivationKey(5), tokenKindRule, nil)
	marker, _, err := allocator.allocate(KindCommonSecret, secret)
	if err != nil {
		t.Fatal(err)
	}
	inner := marker[1 : len(marker)-1]
	suffix := marker[len(marker)-placeholderTokenHexLength-1 : len(marker)-1]
	for _, test := range []struct {
		name string
		text string
		span string
	}{
		{"adjacent secret", marker + secret, secret},
		{"same hex outside marker", suffix + " " + marker, suffix},
		{"bare body", inner, inner},
		{"missing opening bracket", marker[1:], inner},
		{"missing closing bracket", marker[:len(marker)-1], inner},
		{"invalid suffix", strings.Replace(marker, suffix, "g"+suffix[1:], 1), "g" + suffix[1:]},
		{"long suffix", strings.Replace(marker, suffix, suffix+"a", 1), suffix + "a"},
		{"unknown marker kind", "<OTHER_" + suffix + ">", suffix},
		{"secret inside unrelated tag", "<SECRET " + secret + ">", secret},
		{"crosses opening bracket", secret + marker, secret + marker[:len(marker)-1]},
		{"crosses closing bracket", marker + secret, marker[1:] + secret},
		{"between two markers", marker + secret + marker, marker + secret + marker},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(test.text)
			if err != nil {
				t.Fatal(err)
			}
			body := `{"messages":[{"role":"user","content":` + string(encoded) + `}]}`
			engine := newTestEngine(t, structuredSpanDetector(t,
				structuredTestSpan{path: "/messages/0/content", text: test.span, kind: KindCommonSecret},
			))
			result, err := engine.Inspect(t.Context(), Policy{
				Enabled: true, Mode: ModeLocalModel, LocalModelID: testLocalModelID, Action: ActionRedact,
			}, contract.ProtocolOpenAIChat, []byte(body))
			if err != nil || result.Decision != DecisionRedact || len(result.Redactions) != 1 || len(result.SuppressedFindings) != 0 {
				t.Fatalf("finding was incorrectly suppressed: %#v %v", result, err)
			}
			if result.Redactions[0].Value != test.span {
				t.Fatalf("redacted value = %q, want %q", result.Redactions[0].Value, test.span)
			}
			if test.name == "adjacent secret" && !strings.Contains(string(result.Body), marker) {
				t.Fatalf("adjacent marker was changed: %s", result.Body)
			}
		})
	}
}
