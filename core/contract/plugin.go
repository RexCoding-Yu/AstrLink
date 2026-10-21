package contract

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
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

// Bounds of what all plugins together inject into one request, in bytes.
const (
	DefaultPluginInjectionBudgetBytes = 16 << 10
	MinPluginInjectionBudgetBytes     = 4 << 10
	MaxPluginInjectionBudgetBytes     = 64 << 10
)

// Interface languages, which what plugins inject follows. They are the
// desktop's locales.
const (
	PluginLanguageEnglish  = "en"
	PluginLanguageChinese  = "zh-CN"
	PluginLanguageJapanese = "ja"
	// DefaultPluginLanguage is the desktop's default locale, for a Core the
	// desktop has not told its language yet.
	DefaultPluginLanguage = PluginLanguageEnglish
)

// ValidPluginLanguage reports whether language is an interface language.
func ValidPluginLanguage(language string) bool {
	switch language {
	case PluginLanguageEnglish, PluginLanguageChinese, PluginLanguageJapanese:
		return true
	}
	return false
}

// PluginHostSettings are the settings of every plugin at once.
type PluginHostSettings struct {
	// Paused keeps every plugin out of requests, which then go out as they
	// came.
	Paused bool `json:"paused"`
	// InjectionBudgetBytes bounds what all plugins inject into one request
	// together. Past it, the blocks of the plugins later in key order stay
	// out.
	InjectionBudgetBytes int `json:"injection_budget_bytes"`
	// Language is the interface language, en, zh-CN or ja. A session takes
	// it when it starts and keeps it.
	Language string `json:"language"`
}

// DefaultPluginHostSettings are the settings of a Core nobody changed them in.
func DefaultPluginHostSettings() PluginHostSettings {
	return PluginHostSettings{InjectionBudgetBytes: DefaultPluginInjectionBudgetBytes, Language: DefaultPluginLanguage}
}

func (settings PluginHostSettings) Validate() error {
	if settings.InjectionBudgetBytes < MinPluginInjectionBudgetBytes || settings.InjectionBudgetBytes > MaxPluginInjectionBudgetBytes {
		return fmt.Errorf("injection_budget_bytes must be %d to %d", MinPluginInjectionBudgetBytes, MaxPluginInjectionBudgetBytes)
	}
	if !ValidPluginLanguage(settings.Language) {
		return fmt.Errorf("language must be en, zh-CN or ja")
	}
	return nil
}

// PluginHostSettingsUpdate changes what is set and keeps what is absent.
type PluginHostSettingsUpdate struct {
	Paused               *bool   `json:"paused,omitempty"`
	InjectionBudgetBytes *int    `json:"injection_budget_bytes,omitempty"`
	Language             *string `json:"language,omitempty"`
}

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
	TurnWaitMs int `json:"turn_wait_ms"`
	// ExcludedClients are the client types the plugin is off for; it takes
	// part in the requests of every other client, unknown ones included.
	ExcludedClients []string `json:"excluded_clients"`
	// WorkspaceBinding binds a new session to the value of the plugin's
	// attributes its workspace had before.
	WorkspaceBinding bool `json:"workspace_binding"`
	// AutoRefresh checks a session's resident block for newer data now and
	// then, and swaps it when it changed.
	AutoRefresh bool                      `json:"auto_refresh"`
	Credentials []PluginCredentialStatus  `json:"credentials"`
	Metadata    []PluginMetadataAttribute `json:"metadata"`
	// Settings are the fields of the plugin's settings form, in order.
	Settings []PluginSetting `json:"settings"`
	// Credits are the open-source projects the plugin credits.
	Credits []PluginCredit `json:"credits"`
	// Storage is what the plugin keeps, as its manifest declares it, and
	// HostStorage what the host keeps for it.
	Storage     []PluginStorageItem `json:"storage"`
	HostStorage []PluginHostStorage `json:"host_storage"`
}

// PluginStorageItem is something a plugin says it keeps in its own store.
type PluginStorageItem struct {
	Label string `json:"label"`
	// Content tells whether it holds conversation text.
	Content   bool   `json:"content"`
	Retention string `json:"retention"`
}

// What the host keeps for a plugin. The plugin page names each.
const (
	// Session bindings of the plugin's attributes: value, how and in which
	// turn it was set.
	PluginStoredSessionBindings = "session_bindings"
	// Workspaces bound to a value: a keyed fingerprint of the path and its
	// last element.
	PluginStoredWorkspaceBindings = "workspace_bindings"
	// The plugin's records in request records: counts, sizes, times and
	// reasons.
	PluginStoredRecords = "request_records"
	// The plugin's part in each session: the turn it started in and
	// whether the user paused it.
	PluginStoredSessions = "plugin_sessions"
	// The plugin's own values in astrlink.store.
	PluginStoredValues = "plugin_values"
	// The resident block a session carries, in memory only.
	PluginStoredResidentBlock = "resident_block"
)

// PluginHostStorage is one kind of thing the host keeps for a plugin, and
// how many it keeps now when it counts them.
type PluginHostStorage struct {
	Kind  string `json:"kind"`
	Count *int   `json:"count,omitempty"`
}

// PluginSetting is a field of a plugin's settings form, as its manifest
// declares it. A key starting with $ places one of the host's settings.
type PluginSetting struct {
	Key         string                `json:"key"`
	Label       string                `json:"label,omitempty"`
	Type        string                `json:"type,omitempty"`
	Group       string                `json:"group,omitempty"`
	Row         string                `json:"row,omitempty"`
	Help        string                `json:"help,omitempty"`
	Default     json.RawMessage       `json:"default,omitempty"`
	Required    bool                  `json:"required,omitempty"`
	Advanced    bool                  `json:"advanced,omitempty"`
	Min         *float64              `json:"min,omitempty"`
	Max         *float64              `json:"max,omitempty"`
	Step        *float64              `json:"step,omitempty"`
	Unit        string                `json:"unit,omitempty"`
	Placeholder string                `json:"placeholder,omitempty"`
	Options     []PluginSettingOption `json:"options,omitempty"`
	Credential  string                `json:"credential,omitempty"`
}

// PluginSettingOption is one answer of a choice.
type PluginSettingOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// PluginCredit is an open-source project a plugin credits.
type PluginCredit struct {
	Name    string `json:"name"`
	Owner   string `json:"owner,omitempty"`
	License string `json:"license,omitempty"`
	URL     string `json:"url"`
	Note    string `json:"note,omitempty"`
}

// PluginNetworkOrigin is an address a plugin may reach.
type PluginNetworkOrigin struct {
	Origin string `json:"origin"`
	// AllowPlaintext is the user's consent to plain http beyond this
	// machine; Plaintext tells the page that the origin needs it.
	AllowPlaintext bool `json:"allow_plaintext"`
	Plaintext      bool `json:"plaintext"`
	// Setting names the server setting the origin comes from; the user's
	// own origins have none.
	Setting string `json:"setting,omitempty"`
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
	ExcludedClients     *[]string                   `json:"excluded_clients,omitempty"`
	WorkspaceBinding    *bool                       `json:"workspace_binding,omitempty"`
	AutoRefresh         *bool                       `json:"auto_refresh,omitempty"`
	Config              json.RawMessage             `json:"config,omitempty"`
	// PlaintextConsent lists the plain http origins of servers the user
	// agrees to reach in the clear.
	PlaintextConsent []string `json:"plaintext_consent,omitempty"`
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
	// LastTest is the last connection test since Core started.
	LastTest *PluginLastTest `json:"last_test,omitempty"`
	// Bindings are the values sessions are bound to, for each attribute the
	// plugin registers.
	Bindings []PluginBindings `json:"bindings,omitempty"`
}

// PluginLastTest is a connection test and when it ran.
type PluginLastTest struct {
	PluginTestResult
	TestedAt time.Time `json:"tested_at"`
}

// PluginBindings are the values sessions are bound to for one attribute,
// the most recently used first.
type PluginBindings struct {
	Key    string               `json:"key"`
	Label  string               `json:"label"`
	Values []PluginBindingValue `json:"values"`
}

// PluginBindingValue is a value, how many sessions are bound to it and when
// one of them last made a request.
type PluginBindingValue struct {
	Value      string     `json:"value"`
	Sessions   int        `json:"sessions"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// MaxPluginExcludedClients bounds the clients a plugin is off for.
const MaxPluginExcludedClients = 64

// SessionPlugin is a plugin's part in one session, for the session's memory
// page.
type SessionPlugin struct {
	Plugin string `json:"plugin"`
	Name   string `json:"name"`
	// Paused is set when the user paused the plugin in the session.
	Paused bool `json:"paused"`
	// FirstTurn is the turn the plugin first took part in, 0 for none yet.
	FirstTurn int `json:"first_turn,omitempty"`
	// Block is the resident block the session carries. It lives in memory
	// only: nil after a restart until the session's next request.
	Block *SessionPluginBlock `json:"block,omitempty"`
	// RefreshPending is set while a refresh waits for the next request.
	RefreshPending bool `json:"refresh_pending,omitempty"`
}

// SessionPluginBlock is a resident block as it goes into the session's
// requests.
type SessionPluginBlock struct {
	Text       string    `json:"text"`
	Bytes      int       `json:"bytes"`
	Trigger    string    `json:"trigger"`
	Language   string    `json:"language"`
	ComputedAt time.Time `json:"computed_at"`
}

// SessionPluginInput pauses or resumes a plugin in a session.
type SessionPluginInput struct {
	Paused bool `json:"paused"`
}

// PluginActionInput is a click on a button of a plugin's record.
type PluginActionInput struct {
	Action  string `json:"action"`
	Session string `json:"session"`
}

// PluginActionResult is what came of a click: a message from the plugin,
// and whether the session's next request computes its block again.
type PluginActionResult struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Refresh bool   `json:"refresh"`
	// Reason is set when the call failed: timeout, plugin_error and so on.
	Reason string `json:"reason,omitempty"`
}

// PluginClearResult says how many things a clearing removed.
type PluginClearResult struct {
	Removed int `json:"removed"`
}
