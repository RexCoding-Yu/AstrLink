package contract

import (
	"fmt"
	"regexp"
)

// ServicePlugin names the plugin that adapts a provider, and which of the
// kinds of provider it declares.
type ServicePlugin struct {
	Key      string `json:"key"`
	Provider string `json:"provider"`
}

var pluginProviderPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// validate requires a plugin for, and only for, a provider of kind plugin.
func (plugin *ServicePlugin) validate(kind ServiceKind) error {
	switch {
	case kind != ServiceKindPlugin && plugin != nil:
		return fmt.Errorf("plugin is only valid for a provider of kind plugin")
	case kind == ServiceKindPlugin && plugin == nil:
		return fmt.Errorf("a provider of kind plugin requires plugin")
	case plugin == nil:
		return nil
	case !ValidPluginKey(plugin.Key):
		return fmt.Errorf("plugin.key %q is not a plugin key", plugin.Key)
	case !pluginProviderPattern.MatchString(plugin.Provider):
		return fmt.Errorf("plugin.provider %q is not a provider id", plugin.Provider)
	}
	return nil
}

// PluginProvider is a kind of provider a plugin adapts, as the providers
// page offers it.
type PluginProvider struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url,omitempty"`
	// Auth is how the gateway sends the provider's key.
	Auth ServiceAuth `json:"auth"`
	// Protocols are the protocols the plugin serves, the first the one
	// others convert to when Convert is set.
	Protocols []PluginProviderProtocol `json:"protocols"`
	Convert   bool                     `json:"convert,omitempty"`
	Models    []string                 `json:"models,omitempty"`
}

// PluginProviderProtocol is what a plugin serves of one protocol.
type PluginProviderProtocol struct {
	Protocol ProtocolID `json:"protocol"`
	Stream   bool       `json:"stream"`
	Sync     bool       `json:"sync"`
}
