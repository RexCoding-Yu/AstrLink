package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

// MaxConfigBytes bounds a plugin's configuration.
const MaxConfigBytes = 64 << 10

// Settings are the user's choices for one plugin, kept in the gateway
// settings.
type Settings struct {
	Enabled bool
	// Capabilities are the ones the user granted. Only those the manifest
	// still asks for are in effect.
	Capabilities []string
	// Config is the plugin's configuration: a JSON object of at most
	// MaxConfigBytes, which the plugin reads and cannot change.
	Config json.RawMessage
	// Origins are the addresses http.fetch may reach.
	Origins []NetworkOrigin
	// Trusted skips the privacy check of the text the host hands the
	// plugin, as a trusted provider skips it.
	Trusted bool
	// TurnWait is how long a request waits for the plugin's prepare hooks;
	// zero is the default.
	TurnWait time.Duration
	// ExcludedClients are the client types the plugin is off for.
	ExcludedClients []string
	// WorkspaceBinding binds a new session to the value of the plugin's
	// attributes its workspace had.
	WorkspaceBinding bool
	// AutoRefresh lets the host check a session's resident block for newer
	// data now and then.
	AutoRefresh bool
	// LookupEnabled lets the model run the plugin's queries, when the user
	// granted model.tools.
	LookupEnabled bool
}

// Excludes reports whether the plugin is off for requests of client.
func (settings Settings) Excludes(client string) bool {
	return slices.Contains(settings.ExcludedClients, client)
}

// Wait returns how long a request waits for the plugin's prepare hooks,
// within the bounds the plugin page allows.
func (settings Settings) Wait() time.Duration {
	wait := settings.TurnWait
	if wait == 0 {
		wait = contract.DefaultPluginTurnWaitMs * time.Millisecond
	}
	return min(max(wait, contract.MinPluginTurnWaitMs*time.Millisecond), contract.MaxPluginTurnWaitMs*time.Millisecond)
}

// NetworkOrigin is an address a plugin may reach: scheme, host and port,
// any path. Origin is in the form NormalizeOrigin returns.
type NetworkOrigin struct {
	Origin string
	// AllowPlaintext allows plain http to a host other than this machine.
	AllowPlaintext bool
}

// NormalizeOrigin returns the origin of an absolute http or https URL as
// scheme://host:port, the host in lowercase and the default port spelt out,
// so that two spellings of one address compare equal.
func NormalizeOrigin(raw string) (string, error) {
	target, err := url.Parse(raw)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" || target.User != nil {
		return "", fmt.Errorf("%q is not an absolute http or https address", raw)
	}
	host := strings.TrimSuffix(strings.ToLower(target.Hostname()), ".")
	port := target.Port()
	if port == "" {
		port = "443"
		if target.Scheme == "http" {
			port = "80"
		}
	}
	if host == "" {
		return "", fmt.Errorf("%q has no host", raw)
	}
	return target.Scheme + "://" + net.JoinHostPort(host, port), nil
}

// Plaintext reports whether a normalized origin is plain http to a host other
// than this machine, which a plugin reaches only when the user allows it for
// that origin on its own.
func Plaintext(origin string) bool {
	rest, plain := strings.CutPrefix(origin, "http://")
	if !plain {
		return false
	}
	host, _, err := net.SplitHostPort(rest)
	if err != nil {
		return true
	}
	if host == "localhost" {
		return false
	}
	address := net.ParseIP(host)
	return address == nil || !address.IsLoopback()
}

// SettingsSource reads the settings of every plugin by key. A plugin without
// settings is disabled and granted nothing.
type SettingsSource interface {
	PluginSettings(ctx context.Context) (map[string]Settings, error)
}

// Plugin is one plugin of a generation: where its source came from, how it
// loaded, the user's settings for it, and the runtimes that run it.
type Plugin struct {
	Key    string
	Origin Origin
	File   string
	// Icon is the plugin's icon as a data URL, "" for none.
	Icon string
	// Meta is the manifest, zero when the plugin did not load.
	Meta Meta
	// Err is why the plugin did not load, nil when it did.
	Err      *LoadError
	Settings Settings

	granted []string
	// config is the configuration the plugin reads: the user's, with the
	// defaults of the settings it has no value for.
	config   json.RawMessage
	program  *program
	pool     *pool
	services *Services
	counter  *counter
}

// Stats count a plugin's calls since Core started, across generations.
type Stats struct {
	Calls int
	// Failures counts the calls that failed, by CallError reason.
	Failures map[string]int
	Network  NetworkStats
}

type counter struct {
	mutex sync.Mutex
	stats Stats
}

func (count *counter) record(info CallInfo, err error) {
	if count == nil {
		return
	}
	count.mutex.Lock()
	defer count.mutex.Unlock()
	count.stats.Calls++
	var failure *CallError
	if errors.As(err, &failure) {
		if count.stats.Failures == nil {
			count.stats.Failures = make(map[string]int)
		}
		count.stats.Failures[failure.Reason]++
	}
	count.stats.Network.Requests += info.Network.Requests
	count.stats.Network.Denied += info.Network.Denied
	count.stats.Network.Errors += info.Network.Errors
	count.stats.Network.Wait += info.Network.Wait
}

// Stats returns what the host counted for the plugin so far.
func (plugin *Plugin) Stats() Stats {
	if plugin.counter == nil {
		return Stats{}
	}
	plugin.counter.mutex.Lock()
	defer plugin.counter.mutex.Unlock()
	stats := plugin.counter.stats
	stats.Failures = maps.Clone(stats.Failures)
	return stats
}

// Loaded reports whether the plugin compiled and passed its checks.
func (plugin *Plugin) Loaded() bool { return plugin.program != nil }

// Enabled reports whether the plugin loaded and the user enabled it.
func (plugin *Plugin) Enabled() bool { return plugin.program != nil && plugin.Settings.Enabled }

// Granted returns the capabilities in effect.
func (plugin *Plugin) Granted() []string { return slices.Clone(plugin.granted) }

// Can reports whether capability is in effect.
func (plugin *Plugin) Can(capability string) bool { return slices.Contains(plugin.granted, capability) }

// Has reports whether the plugin exports hook.
func (plugin *Plugin) Has(hook string) bool {
	if plugin.program == nil {
		return false
	}
	_, exported := plugin.program.hooks[hook]
	return exported
}

// Call runs hook with arg, a Go value or nil for none, and returns the JSON
// text of what the hook returned, nil for undefined. It does not check that
// the plugin is enabled: request hooks run only for enabled plugins, while
// the plugin page may test one first. Any failure is a *CallError; the
// CallInfo is filled either way.
func (plugin *Plugin) Call(ctx context.Context, hook string, arg any, options CallOptions) (json.RawMessage, CallInfo, error) {
	started := time.Now()
	if plugin.program == nil {
		return nil, CallInfo{}, &CallError{Reason: ReasonUnavailable, Message: "the plugin did not load"}
	}
	exported, ok := plugin.program.hooks[hook]
	if !ok {
		return nil, CallInfo{}, &CallError{Reason: ReasonUnavailable, Message: fmt.Sprintf("the plugin does not export %s", hook)}
	}
	c := &call{ctx: ctx, plugin: plugin, options: options, pool: plugin.pool, networkLeft: options.NetworkBudget}
	result, err := plugin.pool.run(ctx, c, exported, arg)
	c.info.Elapsed = time.Since(started)
	if c.watchdog != nil {
		c.info.Script = c.watchdog.used()
	}
	if err != nil {
		failure := classify(err, plugin.pool.limits)
		plugin.counter.record(c.info, failure)
		return nil, c.info, failure
	}
	plugin.counter.record(c.info, nil)
	return result, c.info, nil
}

// Generation is the registry at one moment: every plugin found, loaded or
// not, with the settings in effect. A request keeps the generation it
// started with while a newer one replaces it.
type Generation struct {
	plugins []*Plugin
}

// Plugins returns every plugin in key order. The slice is shared.
func (generation *Generation) Plugins() []*Plugin { return generation.plugins }

// Plugin returns the plugin with key, nil when there is none.
func (generation *Generation) Plugin(key string) *Plugin {
	index, found := slices.BinarySearchFunc(generation.plugins, key, func(plugin *Plugin, key string) int {
		switch {
		case plugin.Key < key:
			return -1
		case plugin.Key > key:
			return 1
		}
		return 0
	})
	if !found {
		return nil
	}
	return generation.plugins[index]
}

// Enabled returns the enabled plugins that export hook, in key order.
func (generation *Generation) Enabled(hook string) []*Plugin {
	var plugins []*Plugin
	for _, plugin := range generation.plugins {
		if plugin.Enabled() && plugin.Has(hook) {
			plugins = append(plugins, plugin)
		}
	}
	return plugins
}

// ManagerOptions configure a Manager.
type ManagerOptions struct {
	// Builtin holds the plugins compiled into Core as <key>/plugin.ts.
	Builtin fs.FS
	// Dir is the plugin directory, <data-dir>/plugins.
	Dir      string
	Settings SettingsSource
	Services Services
	Limits   Limits
}

// Manager keeps the current generation of plugins. Reload rescans the
// sources and Refresh applies changed settings; both compile only sources
// that changed, and requests read the current generation without waiting
// for either.
type Manager struct {
	builtin  fs.FS
	dir      string
	settings SettingsSource
	services *Services
	limits   Limits

	mutex    sync.Mutex
	sources  []Source
	programs map[[sha256.Size]byte]loadResult
	counters map[string]*counter
	current  atomic.Pointer[Generation]
}

type loadResult struct {
	program *program
	err     *LoadError
}

// NewManager returns a Manager with no plugins until its first Reload.
func NewManager(options ManagerOptions) *Manager {
	services := options.Services
	manager := &Manager{
		builtin:  options.Builtin,
		dir:      options.Dir,
		settings: options.Settings,
		services: &services,
		limits:   options.Limits.withDefaults(),
		programs: make(map[[sha256.Size]byte]loadResult),
		counters: make(map[string]*counter),
	}
	manager.current.Store(&Generation{})
	return manager
}

// Current returns the generation in effect.
func (manager *Manager) Current() *Generation { return manager.current.Load() }

// Dir returns the plugin directory.
func (manager *Manager) Dir() string { return manager.dir }

// Reload rescans the builtin plugins and the plugin directory, compiles the
// sources that changed and publishes a generation with the current settings.
// When the sources or the settings cannot be read, the current generation
// stays.
func (manager *Manager) Reload(ctx context.Context) error {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	sources, err := Discover(manager.builtin, manager.dir)
	if err != nil {
		return err
	}
	settings, err := manager.readSettings(ctx)
	if err != nil {
		return err
	}
	programs := make(map[[sha256.Size]byte]loadResult, len(sources))
	for _, source := range sources {
		identity := sourceIdentity(source)
		result, compiled := manager.programs[identity]
		if !compiled {
			loaded, err := load(source, manager.limits)
			if err != nil {
				result.err = asLoadError(source, err)
			} else {
				result.program = loaded
			}
		}
		programs[identity] = result
	}
	manager.sources = sources
	manager.programs = programs
	manager.publish(settings)
	return nil
}

// Refresh re-reads the settings and publishes a generation with them. The
// programs stay as they are.
func (manager *Manager) Refresh(ctx context.Context) error {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	settings, err := manager.readSettings(ctx)
	if err != nil {
		return err
	}
	manager.publish(settings)
	return nil
}

func (manager *Manager) readSettings(ctx context.Context) (map[string]Settings, error) {
	if manager.settings == nil {
		return nil, nil
	}
	settings, err := manager.settings.PluginSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("read plugin settings: %w", err)
	}
	return settings, nil
}

// publish replaces the current generation. A plugin whose program and
// settings did not change keeps its runtimes.
func (manager *Manager) publish(settings map[string]Settings) {
	previous := manager.current.Load()
	plugins := make([]*Plugin, 0, len(manager.sources))
	for _, source := range manager.sources {
		result := manager.programs[sourceIdentity(source)]
		current := settings[source.Key]
		if kept := previous.Plugin(source.Key); kept != nil && kept.Origin == source.Origin && kept.Icon == source.Icon && kept.program == result.program && kept.Err == result.err && reflect.DeepEqual(kept.Settings, current) {
			plugins = append(plugins, kept)
			continue
		}
		count := manager.counters[source.Key]
		if count == nil {
			count = &counter{}
			manager.counters[source.Key] = count
		}
		plugin := &Plugin{Key: source.Key, Origin: source.Origin, File: source.File, Icon: source.Icon, Settings: current, services: manager.services, counter: count}
		if result.program == nil {
			plugin.Err = result.err
		} else {
			plugin.program = result.program
			plugin.Meta = result.program.meta
			plugin.granted = result.program.meta.Granted(current.Capabilities)
			plugin.config = result.program.meta.WithDefaults(current.Config)
			plugin.pool = newPool(result.program, manager.limits)
		}
		plugins = append(plugins, plugin)
	}
	manager.current.Store(&Generation{plugins: plugins})
}

// sourceIdentity tells whether a source changed since its last compile.
func sourceIdentity(source Source) [sha256.Size]byte {
	hash := sha256.New()
	fmt.Fprintf(hash, "%s\x00%s\x00%s\x00", source.Key, source.Origin, source.File)
	if source.Err != nil {
		fmt.Fprintf(hash, "error\x00%s\x00%s", source.Err.Code, source.Err.Message)
	} else {
		hash.Write([]byte(source.Text))
	}
	var identity [sha256.Size]byte
	hash.Sum(identity[:0])
	return identity
}

func asLoadError(source Source, err error) *LoadError {
	if loadErr, ok := err.(*LoadError); ok {
		if loadErr.File == "" {
			loadErr.File = source.File
		}
		return loadErr
	}
	return &LoadError{Code: CodeLoadFailed, File: source.File, Message: cleanText(err.Error(), maxMessageRunes, "")}
}

// Attributes returns the metadata attributes a plugin registers, as the
// metadata registry keeps them.
func (plugin *Plugin) Attributes() []contract.MetadataAttribute {
	attributes := make([]contract.MetadataAttribute, 0, len(plugin.Meta.Metadata))
	for _, attribute := range plugin.Meta.Metadata {
		attributes = append(attributes, contract.MetadataAttribute{
			Key: attribute.Key, Label: attribute.Label, Pattern: attribute.Pattern, Marker: attribute.Marker, Owner: plugin.Key,
		})
	}
	return attributes
}
