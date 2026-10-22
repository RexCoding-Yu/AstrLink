package pricing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type memoryCatalog struct{ c Catalog }

func (s *memoryCatalog) PricingCatalog(context.Context) (Catalog, error)       { return s.c, nil }
func (s *memoryCatalog) SavePricingCatalog(_ context.Context, c Catalog) error { s.c = c; return nil }
func TestSyncOnlyOfficialCatalogsAtomicAndConditional(t *testing.T) {
	broken := false
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/manifest.json":
			if r.Header.Get("If-None-Match") == "v1" && !broken {
				w.WriteHeader(304)
				return
			}
			w.Header().Set("ETag", "v1")
			json.NewEncoder(w).Encode(map[string]any{"version": 1, "generatedAt": time.Now().UTC().Truncate(24 * time.Hour), "warnings": []string{}})
		case "/all.json":
			ioJSON := `{"moonshotai":{"models":{"kimi-k2":{"name":"Kimi"}}},"moonshotai-coding-plan":{"models":{}},"zai-coding-plan":{"models":{}}}`
			w.Write([]byte(ioJSON))
		case "/newapi/providers/moonshotai/ratio_config-v1-base.json":
			if broken {
				w.WriteHeader(500)
				return
			}
			w.Write([]byte(`{"success":true,"data":{"billing_mode":{"kimi-k2":"tiered_expr"},"billing_expr":{"kimi-k2":"tier(\"standard\",p * 1 + c * 4)"}}}`))
		default:
			t.Errorf("unexpected source path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	store := &memoryCatalog{}
	m := NewManager(store, server.Client())
	m.source.Base = server.URL
	status, err := m.Sync(context.Background())
	if err != nil || status.Models != 1 {
		t.Fatalf("%+v %v", status, err)
	}
	version := store.c.Version
	before := calls
	if _, err = m.Sync(context.Background()); err != nil || calls != before+1 {
		t.Fatalf("conditional sync %v %d", err, calls)
	}
	broken = true
	if _, err = m.Sync(context.Background()); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected fetch failure %v", err)
	}
	if store.c.Version != version {
		t.Fatal("failed sync replaced catalog")
	}
}

func TestLiveOfficialCatalog(t *testing.T) {
	if os.Getenv("ASTRLINK_TEST_LIVE_PRICES") != "1" {
		t.Skip("opt-in public catalog smoke test")
	}
	store := &memoryCatalog{}
	manager := NewManager(store, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	status, err := manager.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, price := range store.c.Prices {
		if strings.Contains(price.Provider, "plan") {
			t.Fatal(price.Provider)
		}
		found[price.Provider] = true
	}
	for _, provider := range []string{"openai", "anthropic", "moonshotai", "zai", "minimax"} {
		if !found[provider] {
			t.Fatalf("missing %s", provider)
		}
	}
	leveled := map[string]bool{}
	for _, model := range store.c.Models {
		leveled[model.Provider] = leveled[model.Provider] || len(model.ReasoningLevels) > 0
	}
	for _, provider := range []string{"openai", "anthropic"} {
		if !leveled[provider] {
			t.Fatalf("no reasoning levels for %s", provider)
		}
	}
	t.Logf("official prices=%d model metadata=%d source warnings=%v", status.Models, len(store.c.Models), status.Warnings)
}

func TestConcurrentSyncIsNotReportedAsSuccess(t *testing.T) {
	m := NewManager(&memoryCatalog{}, nil)
	m.syncMu.Lock()
	defer m.syncMu.Unlock()
	if _, err := m.Sync(context.Background()); err == nil {
		t.Fatal("concurrent sync reported success")
	}
}

func TestSyncKeepsModelMetadataAndRefetchesOlderCatalogs(t *testing.T) {
	manifests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest.json":
			manifests++
			if r.Header.Get("If-None-Match") == "v1" {
				w.WriteHeader(304)
				return
			}
			w.Header().Set("ETag", "v1")
			json.NewEncoder(w).Encode(map[string]any{"version": 1, "generatedAt": time.Now().UTC().Truncate(24 * time.Hour), "warnings": []string{}})
		case "/all.json":
			w.Write([]byte(`{
"openai":{"models":{
  "gpt-5.6":{"name":"GPT-5.6","reasoning":true,"reasoning_options":[{"type":"effort","values":["none","low","high"]}],"limit":{"context":1050000,"output":128000}},
  "gpt-4o":{"reasoning":false}}},
"zai-coding-plan":{"models":{"glm-5.3":{"reasoning":true}}}}`))
		case "/newapi/providers/openai/ratio_config-v1-base.json":
			w.Write([]byte(`{"success":true,"data":{"billing_mode":{"gpt-5.6":"tiered_expr"},"billing_expr":{"gpt-5.6":"tier(\"standard\",p * 1 + c * 4)"}}}`))
		default:
			t.Errorf("unexpected source path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	// Saved before model metadata was kept, from the same publication.
	store := &memoryCatalog{c: Catalog{Version: "old", ETag: "v1", Prices: []Price{{Provider: "openai", Model: "gpt-5.6"}}}}
	m := NewManager(store, server.Client())
	m.source.Base = server.URL
	if _, err := m.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, model := range store.c.Models {
		ids = append(ids, model.Provider+"/"+model.Model)
	}
	if strings.Join(ids, " ") != "openai/gpt-4o openai/gpt-5.6" || store.c.Version == "old" ||
		store.c.Prices[0].Name != "GPT-5.6" || store.c.Models[1].ContextTokens != 1050000 {
		t.Fatalf("%s %+v %+v", store.c.Version, store.c.Prices, store.c.Models)
	}
	version, before := store.c.Version, manifests
	if _, err := m.Sync(context.Background()); err != nil || manifests != before+1 || store.c.Version != version {
		t.Fatalf("conditional sync %v %d", err, manifests)
	}
}
