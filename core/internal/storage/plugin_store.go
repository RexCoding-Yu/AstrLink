package storage

import (
	"context"
	"encoding/json"
	"time"
)

// Limits of a plugin's own values (astrlink.store).
const (
	MaxPluginValueBytes = 64 << 10
	MaxPluginValuesSize = 1 << 20
)

// PluginSettingsRecord is the user's choices for one plugin.
type PluginSettingsRecord struct {
	Key     string
	Enabled bool
	// Capabilities are the ones the user granted.
	Capabilities []string
	// Origins are the addresses the plugin may reach over the network.
	Origins []PluginOrigin
	// Trusted skips the privacy check of what the host gives the plugin.
	Trusted bool
	// TurnWaitMs is how long a request waits for the plugin's prepare
	// hooks; 0 stores the default.
	TurnWaitMs int
	Config     json.RawMessage
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// PluginOrigin is an address a plugin may reach: scheme, host and port.
type PluginOrigin struct {
	Origin string `json:"origin"`
	// AllowPlaintext allows plain http to a host other than this machine.
	AllowPlaintext bool `json:"allow_plaintext,omitempty"`
}

// PluginCredentialRecord describes a stored credential without its value.
type PluginCredentialRecord struct {
	Plugin string
	Name   string
	// Origin is the one address the host injects the credential for.
	Origin string
	// Hint is the end of the value, for the user to recognize it.
	Hint      string
	UpdatedAt time.Time
}

type PluginStore interface {
	// ListPluginSettings returns the plugins the user changed something
	// for, by key.
	ListPluginSettings(context.Context) ([]PluginSettingsRecord, error)
	// GetPluginSettings returns ErrNotFound for a plugin without settings.
	GetPluginSettings(ctx context.Context, plugin string) (PluginSettingsRecord, error)
	PutPluginSettings(context.Context, PluginSettingsRecord) error

	ListPluginCredentials(ctx context.Context, plugin string) ([]PluginCredentialRecord, error)
	// PutPluginCredential stores secret, sealed, bound to origin. It creates
	// the plugin's settings when it has none.
	PutPluginCredential(ctx context.Context, plugin, name, origin string, secret []byte) error
	// DeletePluginCredential returns ErrNotFound when nothing was stored.
	DeletePluginCredential(ctx context.Context, plugin, name string) error
	// PluginCredential returns the secret and its origin, ErrNotFound when
	// none is stored. The caller clears the secret after use.
	PluginCredential(ctx context.Context, plugin, name string) (secret []byte, origin string, err error)

	// GetPluginValue returns found false for a key without a value.
	GetPluginValue(ctx context.Context, plugin, key string) (value json.RawMessage, found bool, err error)
	// PutPluginValue returns ErrLimitReached when the value or the plugin's
	// values would pass their limits.
	PutPluginValue(ctx context.Context, plugin, key string, value json.RawMessage) error
	DeletePluginValue(ctx context.Context, plugin, key string) error
	PluginValueKeys(ctx context.Context, plugin, prefix string) ([]string, error)
}
