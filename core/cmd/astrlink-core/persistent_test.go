package main

import (
	"context"
	"maps"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/buildinfo"
	"github.com/QuantumNous/astrlink/core/internal/coreapp"
	"github.com/QuantumNous/astrlink/core/internal/ingress"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
)

// TestPersistentCoreKeepsPlaceholdersAcrossRestart resumes a conversation on a
// restarted Core, with reasoning from before the restart quoting both
// placeholders. Neither is renamed; a Core on another data directory, with
// another audit key, names the same values differently.
func TestPersistentCoreKeepsPlaceholdersAcrossRestart(t *testing.T) {
	policy := privacy.Policy{
		Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact,
		KindRules: map[privacy.Kind]privacy.KindRule{
			privacy.KindEmail:     {Enabled: true, Style: contract.PlaceholderStyleNatural},
			privacy.KindIPAddress: {Enabled: true, Style: contract.PlaceholderStyleNatural},
		},
	}
	ctx := privacy.WithConversation(context.Background(), "session_one")
	// run is one Core process on directory.
	run := func(directory, body string) map[privacy.Kind]string {
		t.Helper()
		core, err := openPersistentCore(context.Background(), persistentOptions{
			version:                  coreapp.DefaultConfig(buildinfo.Version, buildinfo.Commit).Version,
			dataDirectory:            directory,
			controlToken:             "astrlink-test-control-token-0123456789abcdef",
			maxConcurrentInspections: ingress.DefaultMaxConcurrentInspections,
			testNetwork:              offlineNetwork(t),
			shutdown:                 func() {},
			logf:                     t.Logf,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := core.close(); err != nil {
				t.Error(err)
			}
		}()
		engine, ok := core.gateway.PrivacyFilter.(*privacy.Engine)
		if !ok {
			t.Fatalf("privacy filter is %T", core.gateway.PrivacyFilter)
		}
		result, err := engine.Inspect(ctx, policy, contract.ProtocolOpenAIResponses, []byte(body))
		if err != nil {
			t.Fatal(err)
		}
		placeholders := make(map[privacy.Kind]string)
		for _, redaction := range result.Redactions {
			placeholders[redaction.Kind] = redaction.Placeholder
		}
		return placeholders
	}
	user := `{"role":"user","content":[{"type":"input_text","text":"mail alice@example.com, then ssh 10.4.7.9"}]}`
	directory := shortDataDir(t)
	first := run(directory, `{"input":[`+user+`]}`)
	if len(first) != 2 {
		t.Fatalf("first run placeholders = %v", first)
	}
	reasoning := `{"type":"reasoning","id":"rs_1","summary":[],"content":[{"type":"reasoning_text","text":"Mail ` +
		first[privacy.KindEmail] + ` before connecting to ` + first[privacy.KindIPAddress] + `."}]}`
	if resumed := run(directory, `{"input":[`+user+`,`+reasoning+`]}`); !maps.Equal(resumed, first) {
		t.Fatalf("placeholders after a restart = %v, want %v", resumed, first)
	}
	if other := run(shortDataDir(t), `{"input":[`+user+`]}`); other[privacy.KindEmail] == first[privacy.KindEmail] {
		t.Fatalf("another data directory named the email %q too", other[privacy.KindEmail])
	}
}
