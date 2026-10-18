package privacy

import (
	"context"
	"slices"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestWarningSummaryContainsOnlyDeterministicCategoriesAndCounts(t *testing.T) {
	summary := WarningSummary([]Finding{
		{Kind: KindPhone},
		{Kind: KindEmail},
		{Kind: KindEmail},
		{Kind: Kind("unknown")},
	})
	if summary != "email=2,phone=1" {
		t.Fatalf("WarningSummary = %q", summary)
	}
}

func TestEngineFingerprintsOnlyTheValuesAWarningLetThrough(t *testing.T) {
	const token = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	engine := newTestEngine(t, nil)
	inspect := func(action Action, input string) Result {
		t.Helper()
		result, err := engine.Inspect(context.Background(), Policy{
			Enabled: true, Mode: ModeRegex, Action: action,
		}, contract.ProtocolOpenAIResponses, []byte(`{"model":"safe","input":"`+input+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := inspect(ActionWarn, "alice@example.com "+token+" alice@example.com")
	if first.Decision != DecisionWarn || len(first.Fingerprints) != 2 {
		t.Fatalf("warn fingerprints = %d for %#v", len(first.Fingerprints), first)
	}
	resent := inspect(ActionWarn, "still here: "+token)
	if len(resent.Fingerprints) != 1 || !slices.Contains(first.Fingerprints, resent.Fingerprints[0]) {
		t.Fatal("the same value must keep its fingerprint across requests")
	}
	other := inspect(ActionWarn, "bob@example.com")
	if len(other.Fingerprints) != 1 || slices.Contains(first.Fingerprints, other.Fingerprints[0]) {
		t.Fatal("a different value must get a different fingerprint")
	}
	for _, action := range []Action{ActionBlock, ActionRedact} {
		if result := inspect(action, "alice@example.com"); result.Fingerprints != nil {
			t.Fatalf("%s carried fingerprints", action)
		}
	}
}
