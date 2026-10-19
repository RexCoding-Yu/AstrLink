package contract

import (
	"encoding/json"
	"regexp"
	"strings"
)

var (
	pluginKeyPattern            = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	pluginCredentialNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
)

// ValidPluginKey reports whether key can name a plugin: a lowercase letter,
// then up to 31 lowercase letters, digits and hyphens. It is also the name
// of the plugin's directory.
func ValidPluginKey(key string) bool { return pluginKeyPattern.MatchString(key) }

// ValidPluginCredentialName reports whether name can name a credential slot
// of a plugin: a lowercase letter, then up to 31 lowercase letters, digits
// and underscores.
func ValidPluginCredentialName(name string) bool {
	return pluginCredentialNamePattern.MatchString(name)
}

const pluginCredentialRefPrefix = "local://plugin/"

// PluginCredentialRef is where the credential store keeps a plugin's
// credential.
func PluginCredentialRef(plugin, name string) string {
	return pluginCredentialRefPrefix + plugin + "/" + name
}

func validPluginCredentialRef(value string) bool {
	plugin, name, ok := strings.Cut(strings.TrimPrefix(value, pluginCredentialRefPrefix), "/")
	return strings.HasPrefix(value, pluginCredentialRefPrefix) && ok && ValidPluginKey(plugin) && ValidPluginCredentialName(name)
}

// Limits of the settings the plugin page saves.
const (
	MaxPluginConfigBytes  = 64 << 10
	MaxPluginOrigins      = 16
	MaxPluginSecretBytes  = 16384
	MaxPluginOriginLength = 512
)

// Bounds of how long a request waits for a plugin's prepare hooks, the
// turn's privacy check included.
const (
	DefaultPluginTurnWaitMs = 2500
	MinPluginTurnWaitMs     = 1000
	MaxPluginTurnWaitMs     = 5000
)

// States of a plugin in the plugin list.
const (
	PluginStateLoaded = "loaded"
	PluginStateFailed = "failed"
)

// Sources of a plugin: built into Core, from the plugin directory, or from
// the plugin directory in place of the builtin of the same key.
const (
	PluginSourceBuiltin   = "builtin"
	PluginSourceDirectory = "directory"
	PluginSourceOverride  = "override"
)

// Statuses of a connection test: what the plugin reported, or failed when
// the call itself failed.
const (
	PluginTestOK     = "ok"
	PluginTestError  = "error"
	PluginTestFailed = "failed"
)

// PluginSummary is one plugin of the plugin list.
type PluginSummary struct {
	Key string `json:"key"`
	// Name and Version are the manifest's; for a plugin that did not load,
	// Name is its key and Version is empty.
	Name    string `json:"name"`
	Version string `json:"version"`
	// Source is builtin, directory, or override for a directory plugin
	// that replaces the builtin of its key.
	Source string `json:"source"`
	// File is the source's name, plugin.ts or plugin.js.
	File    string `json:"file,omitempty"`
	Enabled bool   `json:"enabled"`
	// State is loaded or failed; Error says why a plugin failed.
	State                 string           `json:"state"`
	Error                 *PluginLoadError `json:"error,omitempty"`
	RequestedCapabilities []string         `json:"requested_capabilities"`
	GrantedCapabilities   []string         `json:"granted_capabilities"`
	NeedsNetwork          bool             `json:"needs_network"`
}

// PluginLoadError is why a plugin did not load. Line and Column are 0 when
// the cause has no position in the source.
type PluginLoadError struct {
	Code    string `json:"code"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
	Message string `json:"message"`
}

// PluginDetail is a plugin with its settings, for the plugin page.
type PluginDetail struct {
	PluginSummary
	NetworkPurpose string `json:"network_purpose,omitempty"`
	// Notice is shown as written in the dialog that enables the plugin.
	Notice         string                `json:"notice,omitempty"`
	Hooks          []string              `json:"hooks"`
	Config         json.RawMessage       `json:"config"`
	NetworkOrigins []PluginNetworkOrigin `json:"network_origins"`
	PrivacyTrusted bool                  `json:"privacy_trusted"`
	// TurnWaitMs is how long a request waits for the plugin's prepare hooks.
	TurnWaitMs  int                       `json:"turn_wait_ms"`
	Credentials []PluginCredentialStatus  `json:"credentials"`
	Metadata    []PluginMetadataAttribute `json:"metadata"`
}

// PluginNetworkOrigin is an address a plugin may reach.
type PluginNetworkOrigin struct {
	Origin string `json:"origin"`
	// AllowPlaintext is the user's consent to plain http beyond this
	// machine; Plaintext tells the page that the origin needs it.
	AllowPlaintext bool `json:"allow_plaintext"`
	Plaintext      bool `json:"plaintext"`
}

// PluginCredentialStatus is a credential slot the plugin declares and what
// is stored in it, never the value.
type PluginCredentialStatus struct {
	Name       string `json:"name"`
	Label      string `json:"label"`
	Configured bool   `json:"configured"`
	Hint       string `json:"hint,omitempty"`
	Origin     string `json:"origin,omitempty"`
}

// PluginMetadataAttribute is a request attribute the plugin registers.
type PluginMetadataAttribute struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Pattern string `json:"pattern"`
	Marker  string `json:"marker,omitempty"`
}

// PluginSettingsUpdate changes what is set and keeps what is absent.
type PluginSettingsUpdate struct {
	Enabled             *bool                       `json:"enabled,omitempty"`
	GrantedCapabilities *[]string                   `json:"granted_capabilities,omitempty"`
	NetworkOrigins      *[]PluginNetworkOriginInput `json:"network_origins,omitempty"`
	PrivacyTrusted      *bool                       `json:"privacy_trusted,omitempty"`
	TurnWaitMs          *int                        `json:"turn_wait_ms,omitempty"`
	Config              json.RawMessage             `json:"config,omitempty"`
}

type PluginNetworkOriginInput struct {
	Origin         string `json:"origin"`
	AllowPlaintext bool   `json:"allow_plaintext"`
}

// PluginCredentialInput stores a credential for the one origin the host
// injects it for.
type PluginCredentialInput struct {
	Secret string `json:"secret"`
	Origin string `json:"origin"`
}

// PluginTestResult is the outcome of a plugin's testConnection hook: the
// status and summary it returned, or error and why the call failed.
type PluginTestResult struct {
	Status    string            `json:"status"`
	Summary   string            `json:"summary"`
	View      []PluginViewBlock `json:"view,omitempty"`
	Truncated bool              `json:"truncated,omitempty"`
	// Reason is set when the call failed: timeout, memory_limit and so on.
	Reason    string             `json:"reason,omitempty"`
	ElapsedMs int                `json:"elapsed_ms"`
	Network   PluginNetworkCount `json:"network"`
}

// PluginNetworkCount counts http.fetch requests: made, denied, failed
// otherwise, and the time spent waiting.
type PluginNetworkCount struct {
	Requests int `json:"requests"`
	Denied   int `json:"denied"`
	Errors   int `json:"errors"`
	WaitMs   int `json:"wait_ms"`
}

// PluginStatus is what the host counted for a plugin since Core started,
// with the view its describeStatus hook returned.
type PluginStatus struct {
	Calls        int                `json:"calls"`
	Failures     map[string]int     `json:"failures"`
	MemoryLimits int                `json:"memory_limits"`
	Timeouts     int                `json:"timeouts"`
	Network      PluginNetworkCount `json:"network"`
	View         []PluginViewBlock  `json:"view,omitempty"`
	Truncated    bool               `json:"truncated,omitempty"`
	// ViewError is why describeStatus gave no view.
	ViewError string `json:"view_error,omitempty"`
}
