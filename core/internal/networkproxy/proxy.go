// Package networkproxy selects the global outbound proxy and isolates per-instance overrides.
// It does not alter TLS verification or the isolated local/media transports.
package networkproxy

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http/httpproxy"
)

type ProxyFunc func(*http.Request) (*url.URL, error)

type settings struct {
	http, https, socks string
	bypass             string
	excludeSimple      bool
	automatic          bool
}

// New selects the outbound proxy policy. System settings are refreshed on demand
// with a one-second cache. The CLI keeps its existing environment behavior by
// default; the desktop explicitly selects system or direct, ignoring environment
// overrides on macOS/Windows in system mode.
func New(mode string) (ProxyFunc, error) {
	return selectProxy(mode, systemSettings)
}

func selectProxy(mode string, readSystem func() (settings, error)) (ProxyFunc, error) {
	switch mode {
	case "direct":
		return nil, nil
	case "environment":
		return localBypass(http.ProxyFromEnvironment), nil
	case "system":
		return refreshingSystemProxy(readSystem, time.Now), nil
	default:
		return nil, fmt.Errorf("outbound-proxy must be environment, system, or direct")
	}
}

func refreshingSystemProxy(readSystem func() (settings, error), now func() time.Time) ProxyFunc {
	var mu sync.Mutex
	var nextRefresh time.Time
	var selected ProxyFunc
	// Bypass before discovery so even a slow or failed system query cannot
	// block local gateway traffic. No background polling goroutine is needed.
	return localBypass(func(request *http.Request) (*url.URL, error) {
		mu.Lock()
		if selected == nil || !now().Before(nextRefresh) {
			config, err := readSystem()
			if err != nil {
				// Cache failures too, but retry after expiry. Never silently
				// send external traffic directly when discovery fails.
				selected = func(*http.Request) (*url.URL, error) {
					return nil, fmt.Errorf("read system proxy settings: %w", err)
				}
			} else {
				selected = fromSettings(config)
			}
			// Start the cache lifetime after the potentially slow system query.
			nextRefresh = now().Add(time.Second)
		}
		proxy := selected
		mu.Unlock()
		return proxy(request)
	})
}

func fromSettings(config settings) ProxyFunc {
	if config.http == "" {
		config.http = config.socks
	}
	if config.https == "" {
		config.https = config.socks
	}
	proxy := (&httpproxy.Config{
		HTTPProxy: config.http, HTTPSProxy: config.https, NoProxy: config.bypass,
	}).ProxyFunc()
	// Also use httpproxy's tested CIDR/domain matching when a PAC setup is
	// rejected. Explicit bypass entries should still be reachable directly.
	bypass := (&httpproxy.Config{
		HTTPProxy: "http://proxy.invalid", HTTPSProxy: "http://proxy.invalid", NoProxy: config.bypass,
	}).ProxyFunc()
	return localBypass(func(request *http.Request) (*url.URL, error) {
		if config.excludeSimple && !strings.Contains(request.URL.Hostname(), ".") && net.ParseIP(request.URL.Hostname()) == nil {
			return nil, nil
		}
		if match, _ := bypass(request.URL); match == nil {
			return nil, nil
		}
		if config.automatic {
			return nil, fmt.Errorf("automatic system proxy (PAC/WPAD) is not supported; configure a manual HTTP/HTTPS/SOCKS proxy")
		}
		return proxy(request.URL)
	})
}

func localBypass(proxy ProxyFunc) ProxyFunc {
	return func(request *http.Request) (*url.URL, error) {
		host := strings.ToLower(strings.TrimSuffix(request.URL.Hostname(), "."))
		ipHost, _, _ := strings.Cut(host, "%") // IPv6 zone, if present
		if host == "localhost" || strings.HasSuffix(host, ".localhost") || net.ParseIP(ipHost).IsLoopback() {
			return nil, nil
		}
		return proxy(request)
	}
}

func proxyAddress(scheme, host, port string) (string, error) {
	if scheme != "http" && scheme != "https" && scheme != "socks5" {
		return "", fmt.Errorf("unsupported system proxy scheme")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || strings.TrimSpace(host) == "" || strings.ContainsAny(host, "/@?# \t\r\n") {
		return "", fmt.Errorf("invalid system proxy host or port")
	}
	return scheme + "://" + net.JoinHostPort(strings.Trim(host, "[]"), port), nil
}

func environmentSettings() settings {
	first := func(names ...string) string {
		for _, name := range names {
			if value := os.Getenv(name); value != "" {
				return value
			}
		}
		return ""
	}
	return settings{
		http: first("HTTP_PROXY", "http_proxy"), https: first("HTTPS_PROXY", "https_proxy"),
		socks: first("ALL_PROXY", "all_proxy"), bypass: first("NO_PROXY", "no_proxy"),
	}
}
