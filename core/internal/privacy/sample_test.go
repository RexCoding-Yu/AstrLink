package privacy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestWrapSampleTextBuildsInspectableBodies(t *testing.T) {
	sample := "reach me at alice@example.com"
	for _, protocol := range []contract.ProtocolID{
		contract.ProtocolOpenAIChat,
		contract.ProtocolAnthropicMessages,
		contract.ProtocolOpenAICompletions,
		contract.ProtocolOpenAIResponses,
		contract.ProtocolOpenAIResponsesCompact,
		contract.ProtocolGoogleGenerateContent,
		contract.ProtocolOpenAIEmbeddings,
		contract.ProtocolGoogleEmbeddings,
		contract.ProtocolRerank,
	} {
		body, err := WrapSampleText(protocol, sample)
		if err != nil {
			t.Fatalf("%s: %v", protocol, err)
		}
		_, extracted, err := extractDocument(protocol, body, InspectionOptions{})
		if err != nil || len(extracted) == 0 {
			t.Fatalf("%s extract=%#v err=%v body=%s", protocol, extracted, err, body)
		}
		found := false
		for _, segment := range extracted {
			if strings.Contains(segment.Value, sample) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s did not embed sample: %s", protocol, body)
		}
	}
}

func TestExtractDocumentInspectsRetrievalText(t *testing.T) {
	for _, test := range []struct {
		protocol contract.ProtocolID
		body     string
		want     []string
	}{
		{
			protocol: contract.ProtocolOpenAIEmbeddings,
			body:     `{"model":"m","input":["first","second"],"encoding_format":"float"}`,
			want:     []string{"first", "second"},
		},
		{
			protocol: contract.ProtocolRerank,
			body:     `{"model":"m","query":"which","documents":["first",{"text":"second"}],"top_n":1}`,
			want:     []string{"which", "first", "second"},
		},
		{
			protocol: contract.ProtocolGoogleEmbeddings,
			body:     `{"title":"first","content":{"parts":[{"text":"second"}]},"taskType":"RETRIEVAL_DOCUMENT"}`,
			want:     []string{"first", "second"},
		},
		{
			protocol: contract.ProtocolGoogleEmbeddings,
			body:     `{"requests":[{"model":"models/m","content":{"parts":[{"text":"first"}]}},{"model":"models/m","content":{"parts":[{"text":"second"}]}}]}`,
			want:     []string{"first", "second"},
		},
	} {
		_, extracted, err := extractDocument(test.protocol, []byte(test.body), InspectionOptions{})
		if err != nil {
			t.Fatalf("%s: %v", test.protocol, err)
		}
		var got []string
		for _, segment := range extracted {
			got = append(got, segment.Value)
		}
		if strings.Join(got, "|") != strings.Join(test.want, "|") {
			t.Fatalf("%s extracted %q, want %q", test.protocol, got, test.want)
		}
	}
}

func TestWrapSampleTextRejectsInvalidInput(t *testing.T) {
	if _, err := WrapSampleText(contract.ProtocolOpenAIModels, "x"); err == nil {
		t.Fatal("expected unsupported protocol")
	}
	if _, err := WrapSampleText(contract.ProtocolOpenAIChat, ""); err == nil {
		t.Fatal("expected empty sample rejection")
	}
	if _, err := WrapSampleText(contract.ProtocolOpenAIChat, strings.Repeat("a", contract.MaxPolicyDryRunSampleBytes+1)); err == nil {
		t.Fatal("expected oversized sample rejection")
	}
}

func TestLocateFindingsMapsPathsWithoutPlaintext(t *testing.T) {
	body, err := WrapSampleText(contract.ProtocolOpenAIChat, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	findings := []Finding{{
		Segment: 0, Start: 0, End: len("alice@example.com"),
		Kind: KindEmail, Confidence: 0.91,
	}}
	locations, err := LocateFindings(contract.ProtocolOpenAIChat, body, findings, InspectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(locations) != 1 || locations[0].Kind != "email" ||
		locations[0].Path == "" || locations[0].Confidence != 0.91 {
		t.Fatalf("locations=%#v", locations)
	}
	encoded, _ := json.Marshal(locations)
	if strings.Contains(string(encoded), "alice@example.com") {
		t.Fatalf("plaintext leaked: %s", encoded)
	}
}
