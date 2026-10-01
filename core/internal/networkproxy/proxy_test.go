package networkproxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const macFixture = `<dictionary> {
  ExceptionsList : <array> {
    0 : *.internal.example
    1 : 10.0.0.0/8
  }
  ExcludeSimpleHostnames : 1
  HTTPEnable : 1
  HTTPPort : 6152
  HTTPProxy : 127.0.0.1
  HTTPSEnable : 1
  HTTPSPort : 6152
  HTTPSProxy : 127.0.0.1
  SOCKSEnable : 1
  SOCKSPort : 6153
  SOCKSProxy : 127.0.0.1
  ProxyAutoConfigEnable : 0
  __SCOPED__ : <dictionary> {
    en0 : <dictionary> {
      HTTPSProxy : wrong.example
      HTTPSPort : 9000
    }
  }
}`

func TestSystemProxySelectionAndBypass(t *testing.T) {
	config, err := parseMacSettings(macFixture)
	if err != nil {
		t.Fatal(err)
	}
	// System selection must not accidentally inherit terminal proxy overrides.
	t.Setenv("HTTPS_PROXY", "http://wrong.example:1234")
	proxy := fromSettings(config)
	for _, test := range []struct{ target, want string }{
		{"https://chatgpt.com/backend-api/wham/usage", "http://127.0.0.1:6152"},
		{"http://upstream.example/v1", "http://127.0.0.1:6152"},
		{"http://localhost:8317", ""},
		{"https://LOCALHOST.:8317", ""},
		{"https://api.localhost:8317", ""},
		{"http://127.0.0.2:8317", ""},
		{"http://[::1]:8317", ""},
		{"http://[::ffff:127.0.0.1]:8317", ""},
		{"http://printer", ""},
		{"https://10.1.2.3", ""},
		{"https://api.internal.example", ""},
	} {
		t.Run(test.target, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, test.target, nil)
			actual, err := proxy(req)
			if err != nil {
				t.Fatal(err)
			}
			got := ""
			if actual != nil {
				got = actual.String()
			}
			if got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestDirectSkipsSystemDiscoveryAndEnvironment(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	proxy, err := selectProxy("direct", func() (settings, error) {
		t.Fatal("disabled proxy must not query system settings")
		return settings{}, nil
	})
	if err != nil || proxy != nil {
		t.Fatalf("direct proxy = %v, err = %v", proxy, err)
	}
	if _, err := New("invalid"); err == nil {
		t.Fatal("invalid mode accepted")
	}
}

func TestDiscoveryAndAutomaticProxyFailuresKeepLoopbackReachable(t *testing.T) {
	failed, _ := selectProxy("system", func() (settings, error) {
		return settings{}, errors.New("query failed")
	})
	for _, proxy := range []ProxyFunc{failed, fromSettings(settings{automatic: true})} {
		req, _ := http.NewRequest(http.MethodGet, "https://chatgpt.com", nil)
		if _, err := proxy(req); err == nil {
			t.Fatal("failed discovery/PAC silently fell back to direct")
		}
		req, _ = http.NewRequest(http.MethodGet, "http://localhost:8317", nil)
		if got, err := proxy(req); got != nil || err != nil {
			t.Fatalf("loopback blocked: %v %v", got, err)
		}
	}
}

func TestSystemProxyRefresh(t *testing.T) {
	now := time.Unix(100, 0)
	var config settings
	var readErr error
	reads := 0
	proxy := refreshingSystemProxy(func() (settings, error) {
		reads++
		return config, readErr
	}, func() time.Time { return now })
	req, _ := http.NewRequest(http.MethodGet, "https://upstream.example/v1", nil)
	queryErr := errors.New("query failed")
	first := settings{https: "http://127.0.0.1:1082"}
	second := settings{https: "http://127.0.0.1:1083"}
	for _, test := range []struct {
		name    string
		after   time.Duration
		config  settings
		readErr error
		want    string
		wantErr bool
		reads   int
	}{
		{name: "initial failure", readErr: queryErr, wantErr: true, reads: 1},
		{name: "cached failure", after: time.Second - time.Nanosecond, config: first, wantErr: true, reads: 1},
		{name: "initial recovery", after: time.Nanosecond, config: first, want: first.https, reads: 2},
		{name: "cached port", after: time.Second - time.Nanosecond, config: second, want: first.https, reads: 2},
		{name: "changed port", after: time.Nanosecond, config: second, want: second.https, reads: 3},
		{name: "disabled", after: time.Second, reads: 4},
		{name: "enabled SOCKS", after: time.Second, config: settings{socks: "socks5://127.0.0.1:1084"}, want: "socks5://127.0.0.1:1084", reads: 5},
		{name: "refresh failure", after: time.Second, readErr: queryErr, wantErr: true, reads: 6},
		{name: "recovered proxy", after: time.Second, config: first, want: first.https, reads: 7},
		{name: "changed bypass", after: time.Second, config: settings{https: first.https, bypass: "upstream.example"}, reads: 8},
		{name: "changed PAC", after: time.Second, config: settings{automatic: true}, wantErr: true, reads: 9},
		{name: "recovered direct", after: time.Second, reads: 10},
		{name: "failure after direct", after: time.Second, readErr: queryErr, wantErr: true, reads: 11},
	} {
		t.Run(test.name, func(t *testing.T) {
			now = now.Add(test.after)
			config, readErr = test.config, test.readErr
			got, err := proxy(req)
			if (err != nil) != test.wantErr {
				t.Fatalf("proxy = %v, err = %v", got, err)
			}
			if test.readErr != nil && !errors.Is(err, queryErr) {
				t.Fatalf("discovery error not preserved: %v", err)
			}
			actual := ""
			if got != nil {
				actual = got.String()
			}
			if actual != test.want || reads != test.reads {
				t.Fatalf("proxy = %q, reads = %d; want %q, %d", actual, reads, test.want, test.reads)
			}
		})
	}
}

func TestSystemProxyConcurrentRefreshKeepsLoopbackReachable(t *testing.T) {
	now := time.Unix(100, 0)
	var reads atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	proxy := refreshingSystemProxy(func() (settings, error) {
		if reads.Add(1) == 1 {
			close(started)
			<-release
		}
		return settings{https: "http://127.0.0.1:1082"}, nil
	}, func() time.Time { return now })
	req, _ := http.NewRequest(http.MethodGet, "https://upstream.example/v1", nil)
	var wg sync.WaitGroup
	fetch := func() {
		defer wg.Done()
		got, err := proxy(req)
		if err != nil || got == nil || got.String() != "http://127.0.0.1:1082" {
			t.Errorf("concurrent proxy = %v, err = %v", got, err)
		}
	}
	wg.Add(1)
	go fetch()
	<-started
	localDone := make(chan struct{})
	go func() {
		defer close(localDone)
		for _, target := range []string{"http://localhost:8317", "https://LOCALHOST.:8317", "http://api.localhost", "http://127.0.0.2", "http://[::1]", "http://[::ffff:127.0.0.1]"} {
			local, _ := http.NewRequest(http.MethodGet, target, nil)
			if got, err := proxy(local); got != nil || err != nil {
				t.Errorf("loopback %s blocked: %v %v", target, got, err)
			}
		}
	}()
	// A loopback lookup that waits for discovery hangs until go test -timeout.
	<-localDone
	unblock.Do(func() { close(release) })
	wg.Wait()
	for round := 0; round < 2; round++ {
		now = now.Add(time.Second)
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go fetch()
		}
		wg.Wait()
		if got := reads.Load(); got != int32(round+2) {
			t.Fatalf("concurrent requests triggered %d reads; want %d", got, round+2)
		}
	}
}

func TestSystemProxyRefreshChangesTransportRoute(t *testing.T) {
	for _, mode := range []struct {
		name    string
		http2   bool
		inherit bool
	}{
		{name: "http1"},
		{name: "http2", http2: true},
		{name: "inherited_http1", inherit: true},
		{name: "inherited_http2", http2: true, inherit: true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			var hits, directDials atomic.Int32
			upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if mode.http2 && r.ProtoMajor != 2 {
					t.Errorf("expected HTTP/2, got %s", r.Proto)
				}
				io.WriteString(w, "ok")
			}))
			upstream.EnableHTTP2 = mode.http2
			upstream.StartTLS()
			defer upstream.Close()
			newProxy := func(connects *atomic.Int32) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodConnect || r.Host != "example.com:443" {
						t.Errorf("unexpected proxy request: %s %s", r.Method, r.Host)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					connects.Add(1)
					target, err := net.DialTimeout("tcp", upstream.Listener.Addr().String(), time.Second)
					if err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadGateway)
						return
					}
					defer target.Close()
					conn, buffered, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					defer conn.Close()
					io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
					go func() { io.Copy(target, buffered); target.Close() }()
					io.Copy(conn, target)
				}))
			}
			var firstConnects, secondConnects atomic.Int32
			first, second := newProxy(&firstConnects), newProxy(&secondConnects)
			defer first.Close()
			defer second.Close()
			now := time.Unix(100, 0)
			var config settings
			var readErr error
			transport := upstream.Client().Transport.(*http.Transport).Clone()
			transport.Proxy = refreshingSystemProxy(func() (settings, error) {
				return config, readErr
			}, func() time.Time { return now })
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				if address == "example.com:443" {
					directDials.Add(1)
					address = upstream.Listener.Addr().String()
				}
				return (&net.Dialer{}).DialContext(ctx, network, address)
			}
			previous := http.DefaultTransport
			http.DefaultTransport = WrapTransport(transport)
			defer func() { http.DefaultTransport = previous }()
			client := &http.Client{Timeout: 3 * time.Second}
			defer client.CloseIdleConnections()
			ctx := context.Background()
			if mode.inherit {
				var err error
				ctx, err = BindConfig(ctx, "service_refresh", nil, nil)
				if err != nil {
					t.Fatal(err)
				}
			}
			queryErr := errors.New("query failed")
			for _, test := range []struct {
				name                  string
				config                settings
				err                   error
				first, second, direct int32
				hits                  int32
			}{
				{name: "initial port", config: settings{https: first.URL}, first: 1, hits: 1},
				{name: "new port", config: settings{https: second.URL}, first: 1, second: 1, hits: 2},
				{name: "disabled", first: 1, second: 1, direct: 1, hits: 3},
				{name: "failed discovery", err: queryErr, first: 1, second: 1, direct: 1, hits: 3},
				{name: "recovered", config: settings{https: second.URL}, first: 1, second: 2, direct: 1, hits: 4},
			} {
				t.Run(test.name, func(t *testing.T) {
					now = now.Add(time.Second)
					config, readErr = test.config, test.err
					req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com/v1", nil)
					response, err := client.Do(req)
					if response != nil {
						io.Copy(io.Discard, response.Body)
						response.Body.Close()
					}
					if !errors.Is(err, test.err) {
						t.Fatalf("request error = %v; want %v", err, test.err)
					}
					if firstConnects.Load() != test.first || secondConnects.Load() != test.second || directDials.Load() != test.direct || hits.Load() != test.hits {
						t.Fatalf("proxy connections = %d, %d; direct = %d; requests = %d; want %d, %d, %d, %d",
							firstConnects.Load(), secondConnects.Load(), directDials.Load(), hits.Load(), test.first, test.second, test.direct, test.hits)
					}
				})
			}
		})
	}
}

func TestMacDisabledInvalidAndSOCKSOnlySettings(t *testing.T) {
	for _, output := range []string{"", "bad", strings.Replace(macFixture, "HTTPSPort : 6152", "HTTPSPort : 0", 1)} {
		if _, err := parseMacSettings(output); err == nil {
			t.Fatalf("invalid configuration accepted: %q", output)
		}
	}
	config, err := parseMacSettings(strings.ReplaceAll(strings.ReplaceAll(macFixture, "HTTPEnable : 1", "HTTPEnable : 0"), "HTTPSEnable : 1", "HTTPSEnable : 0"))
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "https://chatgpt.com", nil)
	got, err := fromSettings(config)(req)
	if err != nil || got.String() != "socks5://127.0.0.1:6153" {
		t.Fatalf("SOCKS-only proxy = %v, %v", got, err)
	}
	config, err = parseMacSettings("<dictionary> {\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	if got, err = fromSettings(config)(req); got != nil || err != nil {
		t.Fatalf("empty system settings must connect directly: %v %v", got, err)
	}
}

func TestWindowsProtocolsAndExceptions(t *testing.T) {
	config, err := parseWindowsSettings(true, "http=127.0.0.1:6152;https=127.0.0.1:6154;socks=127.0.0.1:6153", "<local>;*.internal.example;10.0.0.0/8")
	if err != nil || config.http != "http://127.0.0.1:6152" || config.https != "http://127.0.0.1:6154" || config.socks != "socks5://127.0.0.1:6153" || !config.excludeSimple || config.bypass != "*.internal.example,10.0.0.0/8" {
		t.Fatalf("Windows configuration: %+v, %v", config, err)
	}
	config, err = parseWindowsSettings(true, "127.0.0.1:6152", "")
	if err != nil || config.http != config.https || config.http == "" {
		t.Fatalf("shared proxy: %+v %v", config, err)
	}
	if _, err := parseWindowsSettings(true, "", ""); err == nil {
		t.Fatal("empty enabled proxy accepted")
	}
	config, err = parseWindowsSettings(false, "invalid old setting", "")
	if err != nil || config.http != "" || config.https != "" {
		t.Fatalf("disabled system proxy: %+v %v", config, err)
	}
	config, err = parseWindowsSettings(true, "https=proxy.example;", "")
	if err != nil || config.https != "http://proxy.example:80" {
		t.Fatalf("default CONNECT port: %+v %v", config, err)
	}
	for _, server := range []string{"https=ftp://proxy.example:21", "https=http://user:secret@proxy.example:80", "https=http://proxy.example:80/path"} {
		if _, err := parseWindowsSettings(true, server, ""); err == nil {
			t.Fatal("invalid proxy accepted")
		}
	}
}

func TestLinuxEnvironmentIncludesSOCKSFallback(t *testing.T) {
	for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(name, "")
	}
	t.Setenv("ALL_PROXY", "socks5://127.0.0.1:6153")
	t.Setenv("NO_PROXY", "*.internal.example")
	config := environmentSettings()
	req, _ := http.NewRequest(http.MethodGet, "https://chatgpt.com", nil)
	got, err := fromSettings(config)(req)
	if err != nil || got.String() != "socks5://127.0.0.1:6153" {
		t.Fatalf("environment fallback = %v %v", got, err)
	}
}

// Exercise a real CONNECT tunnel with TLS verification enabled, then the same
// destination in direct mode. No external network or account tokens are used.
func TestHTTPSUsesProxyAndDirectModeDisablesIt(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "example.com" || r.URL.Path != "/backend-api/wham/usage" {
			t.Errorf("unexpected upstream request: %s %s", r.Host, r.URL.Path)
		}
		io.WriteString(w, `{"allowed":true}`)
	}))
	defer upstream.Close()
	var connects atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "example.com:443" {
			t.Errorf("unexpected proxy request: %s %s", r.Method, r.Host)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		connects.Add(1)
		target, err := net.DialTimeout("tcp", upstream.Listener.Addr().String(), time.Second)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer target.Close()
		conn, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go func() { io.Copy(target, buffered); target.Close() }()
		io.Copy(conn, target)
	}))
	defer proxy.Close()
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	for _, mode := range []string{"system", "direct"} {
		selector, err := selectProxy(mode, func() (settings, error) { return settings{https: proxy.URL}, nil })
		if err != nil {
			t.Fatal(err)
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = selector
		transport.TLSClientConfig = &tls.Config{RootCAs: roots}
		if mode == "direct" {
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
			}
		}
		client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
		response, err := client.Get("https://example.com/backend-api/wham/usage")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		transport.CloseIdleConnections()
		if err != nil || string(body) != `{"allowed":true}` {
			t.Fatalf("%s response: %s %v", mode, body, err)
		}
	}
	if connects.Load() != 1 {
		t.Fatalf("expected only system mode to proxy, CONNECT count = %d", connects.Load())
	}
}
