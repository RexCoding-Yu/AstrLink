package plugininstall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

// hostTransport sends every request to one test server, which tells the
// hosts apart by the Host header.
type hostTransport struct{ target *url.URL }

func (transport hostTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	routed := request.Clone(request.Context())
	routed.Host = request.URL.Host
	routed.URL.Scheme, routed.URL.Host = transport.target.Scheme, transport.target.Host
	return http.DefaultTransport.RoundTrip(routed)
}

// fakeMarket serves the official marketplace: GitHub's raw host, which
// may hang, its mirror, whose icons may hang too, and two npm registries
// without the icon package.
type fakeMarket struct {
	t         *testing.T
	server    *httptest.Server
	mutex     sync.Mutex
	hangs     bool
	iconsHang bool
	served    map[string]int
	files     map[string]string
	stop      chan struct{}
}

const marketIcon1 = `<svg xmlns="http://www.w3.org/2000/svg"><circle r="1"/></svg>`

func newFakeMarket(t *testing.T) *fakeMarket {
	sum := sha256.Sum256([]byte(marketIcon1))
	index := fmt.Sprintf(`{"indexVersion": 1, "name": "New API Official Plugins", "plugins": [
		{"key": "incho", "name": "Incho", "icon": "text", "iconFile": {"path": "plugins/tasks/incho/icon.svg", "sha256": %q},
		 "latest": "1.0.2", "versions": [{"version": "1.0.2", "path": "plugins/tasks/incho/1.0.2/plugin.js", "sha256": "00"}], "models": ["incho-1"]},
		{"key": "kling", "name": "Kling", "icon": "Kling.Color",
		 "latest": "1.1.1", "versions": [{"version": "1.1.1", "path": "plugins/tasks/kling/1.1.1/plugin.js", "sha256": "00"}]},
		{"key": "sora", "name": "Sora", "icon": "Sora.Color",
		 "latest": "1.1.0", "versions": [{"version": "1.1.0", "path": "plugins/tasks/sora/1.1.0/plugin.js", "sha256": "00"}]},
		{"key": "typesafe", "name": "TypeSafe", "icon": "text:TS",
		 "latest": "1.0.0", "versions": [{"version": "1.0.0", "path": "plugins/tasks/typesafe/1.0.0/plugin.js", "sha256": "00"}]}
	]}`, hex.EncodeToString(sum[:]))
	market := &fakeMarket{t: t, served: map[string]int{}, stop: make(chan struct{}), files: map[string]string{
		"/gh/QuantumNous/new-api-plugins@main/index.json":                   index,
		"/gh/QuantumNous/new-api-plugins@main/plugins/tasks/incho/icon.svg": marketIcon1,
		"/QuantumNous/new-api-plugins/main/index.json":                      index,
		"/QuantumNous/new-api-plugins/main/plugins/tasks/incho/icon.svg":    marketIcon1,
	}}
	market.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		market.mutex.Lock()
		market.served[request.Host]++
		hangs, iconsHang := market.hangs, market.iconsHang
		body, found := market.files[request.URL.Path]
		market.mutex.Unlock()
		switch {
		case request.Host == "raw.githubusercontent.com" && hangs,
			iconsHang && strings.HasSuffix(request.URL.Path, ".svg"):
			// A network that goes nowhere.
			select {
			case <-request.Context().Done():
			case <-market.stop:
			}
		case strings.HasPrefix(request.URL.Path, "/npm"):
			writer.WriteHeader(http.StatusBadGateway)
		case found:
			_, _ = writer.Write([]byte(body))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(func() {
		close(market.stop)
		market.server.Close()
	})
	return market
}

func (market *fakeMarket) count(host string) int {
	market.mutex.Lock()
	defer market.mutex.Unlock()
	return market.served[host]
}

func (market *fakeMarket) installer(now func() time.Time) *Installer {
	target, _ := url.Parse(market.server.URL)
	return New(Options{
		Dir:        market.t.TempDir(),
		Client:     &http.Client{Transport: hostTransport{target: target}},
		Registries: func() []string { return []string{market.server.URL + "/npm-a", market.server.URL + "/npm-b"} },
		Now:        now,
	})
}

// shorten makes the waits of reading a marketplace short for a test.
func shorten(t *testing.T, attempt, icons time.Duration) {
	previousAttempt, previousIcons := indexAttempt, iconBudget
	indexAttempt, iconBudget = attempt, icons
	t.Cleanup(func() { indexAttempt, iconBudget = previousAttempt, previousIcons })
}

func iconsOf(market contract.PluginMarket) map[string]bool {
	icons := map[string]bool{}
	for _, entry := range market.Plugins {
		icons[entry.Key] = strings.HasPrefix(entry.IconURL, "data:image/svg+xml;base64,")
	}
	return icons
}

func TestTheOfficialMarketGoesToItsMirrorWhenGitHubHangs(t *testing.T) {
	shorten(t, 200*time.Millisecond, 5*time.Second)
	market := newFakeMarket(t)
	market.hangs = true
	installer := market.installer(nil)
	started := time.Now()
	listed, err := installer.Market(context.Background(), "", func(key string) string {
		if key == "sora" {
			return "1.0.3"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(started); took > 3*time.Second {
		t.Fatalf("the listing took %v", took)
	}
	if listed.Index != contract.OfficialPluginMarket || listed.Name != "New API Official Plugins" || len(listed.Plugins) != 4 {
		t.Fatalf("listed = %+v", listed)
	}
	// Its links stay the official index's: an install reads it the same way.
	for _, entry := range listed.Plugins {
		if !strings.HasPrefix(entry.Link, contract.OfficialPluginMarket+"#"+entry.Key) {
			t.Fatalf("link of %s = %q", entry.Key, entry.Link)
		}
		if entry.Key == "sora" && entry.Installed != "1.0.3" {
			t.Fatalf("sora installed = %q", entry.Installed)
		}
	}
	// The icon the index lists comes from the mirror too; the icon package
	// cannot be fetched, so the LobeHub ones are left out.
	if icons := iconsOf(listed); !icons["incho"] || icons["kling"] || icons["sora"] || icons["typesafe"] {
		t.Fatalf("icons = %v", icons)
	}
	// Two plugins named LobeHub icons; the package was tried once on each
	// registry, not once per icon.
	if npm := market.count(strings.TrimPrefix(market.server.URL, "http://")); npm != 2 {
		t.Fatalf("registry requests = %d", npm)
	}
	// The next read, of another marketplace's listing or an install, goes
	// to the mirror first.
	if _, base, err := installer.fetchIndex(context.Background(), contract.OfficialPluginMarket); err != nil || base != contract.OfficialPluginMarketFallback {
		t.Fatalf("fetchIndex = %q, %v", base, err)
	}
	if raw := market.count("raw.githubusercontent.com"); raw != 1 {
		t.Fatalf("GitHub was asked %d times", raw)
	}
}

func TestAMarketListsItsPluginsBeforeIconsThatTakeTooLong(t *testing.T) {
	shorten(t, 200*time.Millisecond, 300*time.Millisecond)
	market := newFakeMarket(t)
	market.iconsHang = true
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	clock := &now
	installer := market.installer(func() time.Time { return *clock })
	started := time.Now()
	listed, err := installer.Market(context.Background(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(started); took > 2*time.Second {
		t.Fatalf("the listing waited %v for its icons", took)
	}
	if len(listed.Plugins) != 4 {
		t.Fatalf("listed = %+v", listed)
	}
	if icons := iconsOf(listed); icons["incho"] {
		t.Fatalf("icons = %v", icons)
	}
	// An icon that could not be fetched waits before another try.
	market.mutex.Lock()
	market.iconsHang = false
	market.mutex.Unlock()
	if listed, _ = installer.Market(context.Background(), "", nil); iconsOf(listed)["incho"] {
		t.Fatal("the icon was fetched again at once")
	}
	*clock = clock.Add(iconRetry + time.Second)
	if listed, _ = installer.Market(context.Background(), "", nil); !iconsOf(listed)["incho"] {
		t.Fatal("the icon was not fetched again after a while")
	}
}
