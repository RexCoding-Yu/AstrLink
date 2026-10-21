package plugin

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
)

// Meta is the manifest a plugin exports as `export const meta`.
type Meta struct {
	APIVersion   int      `json:"apiVersion"`
	Key          string   `json:"key"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
	// Network says what the plugin uses the network for. It is required
	// with CapabilityNetwork and shown when the user enables the plugin.
	Network *NetworkUse `json:"network,omitempty"`
	// Credentials are the secrets the plugin page asks the user for.
	Credentials []CredentialSlot `json:"credentials,omitempty"`
	// Metadata are the request attributes the plugin registers when it is
	// enabled, such as the project a memory belongs to.
	Metadata []MetadataAttribute `json:"metadata,omitempty"`
	// Notice is shown, as written, in the dialog that enables the plugin.
	Notice string `json:"notice,omitempty"`
	// Settings are the fields of the plugin's settings form, in order.
	Settings []Setting `json:"settings,omitempty"`
	// Credits are the open-source projects the plugin page credits.
	Credits []Credit `json:"credits,omitempty"`
	// Storage says what the plugin keeps in astrlink.store, for the plugin
	// page to show beside what the host keeps for it.
	Storage []StorageItem `json:"storage,omitempty"`
}

// StorageItem is one kind of thing a plugin keeps: what it is, whether it
// holds conversation text, and how long it stays.
type StorageItem struct {
	Label     string `json:"label"`
	Content   bool   `json:"content,omitempty"`
	Retention string `json:"retention"`
}

// maxStorageItems bounds what a manifest declares it keeps.
const maxStorageItems = 8

// NetworkUse explains a plugin's use of the network.
type NetworkUse struct {
	Purpose string `json:"purpose"`
}

// CredentialSlot is a secret the user enters on the plugin page. A plugin
// names it in http.fetch; the host writes the value into Header, after
// Scheme and a space when Scheme is set, and the plugin never reads it.
type CredentialSlot struct {
	Name   string `json:"name"`
	Label  string `json:"label"`
	Header string `json:"header"`
	Scheme string `json:"scheme,omitempty"`
}

// MetadataAttribute is a request attribute a plugin registers. Pattern
// validates every value; Marker, when set, names the line a model reply can
// carry to set the value for its session.
type MetadataAttribute struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Pattern string `json:"pattern"`
	Marker  string `json:"marker,omitempty"`
}

var (
	schemePattern     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,31}$`)
	headerNamePattern = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]{1,64}$")
)

// ValidPluginKey reports whether key can name a plugin: a lowercase letter,
// then up to 31 lowercase letters, digits and hyphens.
func ValidPluginKey(key string) bool { return contract.ValidPluginKey(key) }

// credentialHeaderAllowed reports whether the host may write a credential
// into header. Headers the transport owns and the gateway's reserved
// namespace stay out of a plugin's reach, credential or not.
func credentialHeaderAllowed(header string) bool {
	if !headerNamePattern.MatchString(header) {
		return false
	}
	return !hostOwnedHeader(header)
}

// hostOwnedHeader is a header that only the host sets on a plugin's request,
// or never sends.
func hostOwnedHeader(header string) bool {
	lower := strings.ToLower(header)
	switch lower {
	case "host", "content-length", "connection", "keep-alive", "transfer-encoding",
		"te", "trailer", "upgrade", "user-agent", "expect":
		return true
	}
	return strings.HasPrefix(lower, "proxy-") || strings.HasPrefix(lower, "x-astrlink-")
}

// validateMeta checks a plugin's manifest; key is the name the plugin was
// found under, which meta.key must repeat.
func validateMeta(meta Meta, key string) error {
	invalid := func(format string, args ...any) error {
		return &LoadError{Code: CodeInvalidMeta, Message: fmt.Sprintf(format, args...)}
	}
	if meta.APIVersion != APIVersion {
		return invalid("meta.apiVersion %d is not supported; this host implements %d", meta.APIVersion, APIVersion)
	}
	if !ValidPluginKey(meta.Key) {
		return invalid("meta.key %q must be a lowercase letter followed by up to 31 lowercase letters, digits or hyphens", meta.Key)
	}
	if meta.Key != key {
		return invalid("meta.key %q does not match the plugin directory %q", meta.Key, key)
	}
	if !displayText(meta.Name, 64, "") {
		return invalid("meta.name must be 1 to 64 characters without control characters")
	}
	if !displayText(meta.Version, 32, "") {
		return invalid("meta.version must be 1 to 32 characters without control characters")
	}
	for index, capability := range meta.Capabilities {
		if !slices.Contains(knownCapabilities, capability) {
			return invalid("meta.capabilities: unknown capability %q", capability)
		}
		if slices.Contains(meta.Capabilities[:index], capability) {
			return invalid("meta.capabilities lists %q twice", capability)
		}
	}
	network := slices.Contains(meta.Capabilities, CapabilityNetwork)
	switch {
	case network && (meta.Network == nil || !displayText(meta.Network.Purpose, 200, "")):
		return invalid("meta.network.purpose is required with the network capability, 1 to 200 characters")
	case !network && meta.Network != nil:
		return invalid("meta.network needs the network capability")
	case !network && len(meta.Credentials) > 0:
		return invalid("meta.credentials need the network capability")
	}
	for index, slot := range meta.Credentials {
		if !contract.ValidPluginCredentialName(slot.Name) {
			return invalid("meta.credentials[%d].name %q must be a lowercase letter followed by up to 31 lowercase letters, digits or underscores", index, slot.Name)
		}
		if slices.ContainsFunc(meta.Credentials[:index], func(other CredentialSlot) bool { return other.Name == slot.Name }) {
			return invalid("meta.credentials names %q twice", slot.Name)
		}
		if !displayText(slot.Label, 64, "") {
			return invalid("meta.credentials[%d].label must be 1 to 64 characters without control characters", index)
		}
		if !credentialHeaderAllowed(slot.Header) {
			return invalid("meta.credentials[%d].header %q is not a header the host writes credentials into", index, slot.Header)
		}
		if slot.Scheme != "" && !schemePattern.MatchString(slot.Scheme) {
			return invalid("meta.credentials[%d].scheme %q must be a token such as Bearer", index, slot.Scheme)
		}
	}
	for index, attribute := range meta.Metadata {
		if err := validateAttribute(attribute); err != nil {
			return invalid("meta.metadata[%d]: %v", index, err)
		}
		if slices.ContainsFunc(meta.Metadata[:index], func(other MetadataAttribute) bool { return other.Key == attribute.Key }) {
			return invalid("meta.metadata registers %q twice", attribute.Key)
		}
		if attribute.Marker != "" && slices.ContainsFunc(meta.Metadata[:index], func(other MetadataAttribute) bool { return other.Marker == attribute.Marker }) {
			return invalid("meta.metadata uses the marker %q twice", attribute.Marker)
		}
	}
	if meta.Notice != "" && !displayText(meta.Notice, 1000, "\n") {
		return invalid("meta.notice must be at most 1000 characters without control characters other than line breaks")
	}
	if err := validateSettings(meta); err != nil {
		return invalid("%v", err)
	}
	if err := validateCredits(meta.Credits); err != nil {
		return invalid("%v", err)
	}
	if len(meta.Storage) > maxStorageItems {
		return invalid("meta.storage has more than %d entries", maxStorageItems)
	}
	if len(meta.Storage) > 0 && !slices.Contains(meta.Capabilities, CapabilityStorage) {
		return invalid("meta.storage needs the storage capability")
	}
	for index, item := range meta.Storage {
		if !displayText(item.Label, 100, "") || !displayText(item.Retention, 64, "") {
			return invalid("meta.storage[%d] needs a label of 1 to 100 and a retention of 1 to 64 characters", index)
		}
	}
	return nil
}

// validateAttribute checks one registered metadata attribute with the same
// rules as the user's own. Its marker reaches the upstream model in prompts
// and replies, so like everything a plugin injects it must not name the
// gateway.
func validateAttribute(attribute MetadataAttribute) error {
	return contract.MetadataAttribute{
		Key:     attribute.Key,
		Label:   attribute.Label,
		Pattern: attribute.Pattern,
		Marker:  attribute.Marker,
		Owner:   contract.MetadataOwnerUser,
	}.Validate()
}

// displayText reports whether text is 1 to max characters of valid UTF-8
// without control characters other than those in keep.
func displayText(text string, max int, keep string) bool {
	if text == "" || !utf8.ValidString(text) || utf8.RuneCountInString(text) > max {
		return false
	}
	return cleanText(text, max, keep) == text
}

// Granted returns the capabilities in effect for a plugin: the ones the user
// granted that its manifest still asks for.
func (meta Meta) Granted(granted []string) []string {
	var effective []string
	for _, capability := range meta.Capabilities {
		if slices.Contains(granted, capability) {
			effective = append(effective, capability)
		}
	}
	return effective
}

// NeedsNetwork reports whether the plugin asks for the network, which the
// plugin list shows on its own.
func (meta Meta) NeedsNetwork() bool { return slices.Contains(meta.Capabilities, CapabilityNetwork) }

// Credential returns the slot named name.
func (meta Meta) Credential(name string) (CredentialSlot, bool) {
	for _, slot := range meta.Credentials {
		if slot.Name == name {
			return slot, true
		}
	}
	return CredentialSlot{}, false
}
