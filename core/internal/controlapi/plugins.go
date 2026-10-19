package controlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/plugin/view"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

const PluginsPath = "/control/v1/plugins"

// pluginPageBudget bounds a plugin page hook: its run and its network wait.
const pluginPageBudget = 10 * time.Second

// PluginRegistry is the plugin registry; *plugin.Manager satisfies it.
type PluginRegistry interface {
	Current() *plugin.Generation
	Dir() string
	Reload(context.Context) error
	Refresh(context.Context) error
}

// pluginsRequiredRole lets observers read the list and a plugin's settings,
// and keeps the routes that run plugin code, as every write, to operators.
func pluginsRequiredRole(request *http.Request) Role {
	if isSafeMethod(request.Method) && !strings.HasSuffix(strings.TrimSuffix(request.URL.Path, "/"), "/status") {
		return RoleObserver
	}
	return RoleOperator
}

func (handler *Handler) pluginsResource(writer http.ResponseWriter, request *http.Request) {
	if handler.plugins == nil || handler.pluginStore == nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "plugins are unavailable")
		return
	}
	rest := strings.Trim(strings.TrimPrefix(request.URL.Path, PluginsPath), "/")
	if rest == "" {
		if !allowMethods(writer, request, http.MethodGet) {
			return
		}
		handler.writePluginList(writer)
		return
	}
	key, action, _ := strings.Cut(rest, "/")
	if key == "reload" && action == "" && request.Method == http.MethodPost {
		if err := handler.plugins.Reload(request.Context()); err != nil {
			writeError(writer, http.StatusInternalServerError, "plugin_reload_failed", err.Error())
			return
		}
		handler.registerEnabledPluginAttributes(request.Context())
		handler.writePluginList(writer)
		return
	}
	if !contract.ValidPluginKey(key) {
		writeError(writer, http.StatusNotFound, "not_found", "plugin not found")
		return
	}
	target := handler.plugins.Current().Plugin(key)
	if target == nil {
		writeError(writer, http.StatusNotFound, "not_found", "plugin not found")
		return
	}
	switch {
	case action == "":
		switch request.Method {
		case http.MethodGet:
			handler.writePluginDetail(writer, request.Context(), target)
		case http.MethodPatch:
			handler.updatePluginSettings(writer, request, target)
		default:
			writer.Header().Set("Allow", "GET, PATCH")
			writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or PATCH")
		}
	case action == "test":
		if allowMethods(writer, request, http.MethodPost) {
			handler.testPlugin(writer, request.Context(), target)
		}
	case action == "status":
		if allowMethods(writer, request, http.MethodGet) {
			handler.pluginStatus(writer, request.Context(), target)
		}
	case strings.HasPrefix(action, "credentials/"):
		handler.pluginCredential(writer, request, target, strings.TrimPrefix(action, "credentials/"))
	default:
		writeError(writer, http.StatusNotFound, "not_found", "plugin endpoint not found")
	}
}

func allowMethods(writer http.ResponseWriter, request *http.Request, method string) bool {
	if request.Method == method {
		return true
	}
	writer.Header().Set("Allow", method)
	writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use "+method)
	return false
}

func (handler *Handler) writePluginList(writer http.ResponseWriter) {
	plugins := handler.plugins.Current().Plugins()
	summaries := make([]contract.PluginSummary, 0, len(plugins))
	for _, target := range plugins {
		summaries = append(summaries, pluginSummary(target))
	}
	writeJSON(writer, http.StatusOK, map[string]any{"directory": handler.plugins.Dir(), "plugins": summaries})
}

func pluginSummary(target *plugin.Plugin) contract.PluginSummary {
	summary := contract.PluginSummary{
		Key:                   target.Key,
		Name:                  target.Key,
		Source:                string(target.Origin),
		File:                  target.File,
		Enabled:               target.Enabled(),
		State:                 contract.PluginStateLoaded,
		RequestedCapabilities: []string{},
		GrantedCapabilities:   []string{},
	}
	if !target.Loaded() {
		summary.State = contract.PluginStateFailed
		if target.Err != nil {
			summary.Error = &contract.PluginLoadError{Code: target.Err.Code, File: target.Err.File, Line: target.Err.Line, Column: target.Err.Column, Message: target.Err.Message}
		}
		return summary
	}
	summary.Name, summary.Version = target.Meta.Name, target.Meta.Version
	summary.RequestedCapabilities = append(summary.RequestedCapabilities, target.Meta.Capabilities...)
	summary.GrantedCapabilities = append(summary.GrantedCapabilities, target.Granted()...)
	summary.NeedsNetwork = target.Meta.NeedsNetwork()
	return summary
}

func (handler *Handler) writePluginDetail(writer http.ResponseWriter, ctx context.Context, target *plugin.Plugin) {
	detail, err := handler.pluginDetail(ctx, target)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "plugin_unavailable", "could not read the plugin's settings")
		return
	}
	writeJSON(writer, http.StatusOK, detail)
}

func (handler *Handler) pluginDetail(ctx context.Context, target *plugin.Plugin) (contract.PluginDetail, error) {
	detail := contract.PluginDetail{
		PluginSummary:  pluginSummary(target),
		Notice:         target.Meta.Notice,
		Hooks:          []string{},
		Config:         json.RawMessage("{}"),
		NetworkOrigins: []contract.PluginNetworkOrigin{},
		Credentials:    []contract.PluginCredentialStatus{},
		Metadata:       []contract.PluginMetadataAttribute{},
	}
	if target.Meta.Network != nil {
		detail.NetworkPurpose = target.Meta.Network.Purpose
	}
	for _, hook := range []string{plugin.HookPrepareSession, plugin.HookPrepareTurn, plugin.HookObserveTurn, plugin.HookDescribeStatus, plugin.HookTestConnection} {
		if target.Has(hook) {
			detail.Hooks = append(detail.Hooks, hook)
		}
	}
	settings, err := handler.pluginStore.GetPluginSettings(ctx, target.Key)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return contract.PluginDetail{}, err
	}
	if len(settings.Config) > 0 {
		detail.Config = settings.Config
	}
	detail.PrivacyTrusted = settings.Trusted
	detail.TurnWaitMs = settings.TurnWaitMs
	if detail.TurnWaitMs == 0 {
		detail.TurnWaitMs = contract.DefaultPluginTurnWaitMs
	}
	for _, origin := range settings.Origins {
		detail.NetworkOrigins = append(detail.NetworkOrigins, contract.PluginNetworkOrigin{Origin: origin.Origin, AllowPlaintext: origin.AllowPlaintext, Plaintext: plugin.Plaintext(origin.Origin)})
	}
	stored, err := handler.pluginStore.ListPluginCredentials(ctx, target.Key)
	if err != nil {
		return contract.PluginDetail{}, err
	}
	for _, slot := range target.Meta.Credentials {
		status := contract.PluginCredentialStatus{Name: slot.Name, Label: slot.Label}
		if index := slices.IndexFunc(stored, func(record storage.PluginCredentialRecord) bool { return record.Name == slot.Name }); index >= 0 {
			status.Configured, status.Hint, status.Origin = true, stored[index].Hint, stored[index].Origin
		}
		detail.Credentials = append(detail.Credentials, status)
	}
	for _, attribute := range target.Meta.Metadata {
		detail.Metadata = append(detail.Metadata, contract.PluginMetadataAttribute{Key: attribute.Key, Label: attribute.Label, Pattern: attribute.Pattern, Marker: attribute.Marker})
	}
	return detail, nil
}

func (handler *Handler) updatePluginSettings(writer http.ResponseWriter, request *http.Request, target *plugin.Plugin) {
	var update contract.PluginSettingsUpdate
	if !decodeControlJSON(writer, request, &update) {
		return
	}
	ctx := request.Context()
	handler.pluginsMu.Lock()
	defer handler.pluginsMu.Unlock()
	settings, err := handler.pluginStore.GetPluginSettings(ctx, target.Key)
	if errors.Is(err, storage.ErrNotFound) {
		settings, err = storage.PluginSettingsRecord{Key: target.Key}, nil
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "plugin_unavailable", "could not read the plugin's settings")
		return
	}
	invalid := func(message string) {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_plugin_settings", message)
	}
	if update.Enabled != nil {
		if *update.Enabled && !target.Loaded() {
			invalid("a plugin that did not load cannot be enabled")
			return
		}
		// Enabling registers the plugin's attributes; one whose key or
		// marker another owner holds keeps the plugin off.
		if *update.Enabled && handler.metadata != nil {
			if err := handler.metadata.RegisterPlugin(ctx, target.Key, target.Attributes()); err != nil {
				if errors.Is(err, storage.ErrConflict) {
					writeError(writer, http.StatusConflict, "metadata_attribute_conflict", "an attribute this plugin registers has the key or marker of another attribute")
					return
				}
				writeError(writer, http.StatusInternalServerError, "metadata_unavailable", "could not register the plugin's attributes")
				return
			}
		}
		settings.Enabled = *update.Enabled
	}
	if update.GrantedCapabilities != nil {
		granted := []string{}
		for _, capability := range *update.GrantedCapabilities {
			if !slices.Contains(target.Meta.Capabilities, capability) {
				invalid("the plugin does not ask for the capability " + capability)
				return
			}
			if !slices.Contains(granted, capability) {
				granted = append(granted, capability)
			}
		}
		settings.Capabilities = granted
	}
	if update.NetworkOrigins != nil {
		if len(*update.NetworkOrigins) > contract.MaxPluginOrigins {
			invalid("a plugin may reach at most 16 origins")
			return
		}
		origins := []storage.PluginOrigin{}
		for _, input := range *update.NetworkOrigins {
			origin, err := plugin.NormalizeOrigin(input.Origin)
			if err != nil || len(origin) > contract.MaxPluginOriginLength {
				invalid("an origin is an http or https address with a host, such as https://memory.example.com:8443")
				return
			}
			if slices.ContainsFunc(origins, func(existing storage.PluginOrigin) bool { return existing.Origin == origin }) {
				invalid("the origin " + origin + " is listed twice")
				return
			}
			origins = append(origins, storage.PluginOrigin{Origin: origin, AllowPlaintext: input.AllowPlaintext && plugin.Plaintext(origin)})
		}
		settings.Origins = origins
	}
	if update.PrivacyTrusted != nil {
		settings.Trusted = *update.PrivacyTrusted
	}
	if update.TurnWaitMs != nil {
		if *update.TurnWaitMs < contract.MinPluginTurnWaitMs || *update.TurnWaitMs > contract.MaxPluginTurnWaitMs {
			invalid("the wait for a plugin is 1000 to 5000 ms")
			return
		}
		settings.TurnWaitMs = *update.TurnWaitMs
	}
	if update.Config != nil {
		config := bytes.TrimSpace(update.Config)
		if len(config) > contract.MaxPluginConfigBytes || !json.Valid(config) || config[0] != '{' {
			invalid("the configuration is a JSON object of at most 64 KiB")
			return
		}
		settings.Config = json.RawMessage(config)
	}
	if err := handler.pluginStore.PutPluginSettings(ctx, settings); err != nil {
		writeError(writer, http.StatusInternalServerError, "plugin_unavailable", "could not save the plugin's settings")
		return
	}
	if err := handler.plugins.Refresh(ctx); err != nil {
		writeError(writer, http.StatusInternalServerError, "plugin_unavailable", "saved, but the plugins could not apply the settings")
		return
	}
	handler.writePluginDetail(writer, ctx, handler.currentPlugin(target))
}

// currentPlugin returns the plugin as the newest generation has it.
func (handler *Handler) currentPlugin(target *plugin.Plugin) *plugin.Plugin {
	if current := handler.plugins.Current().Plugin(target.Key); current != nil {
		return current
	}
	return target
}

func (handler *Handler) pluginCredential(writer http.ResponseWriter, request *http.Request, target *plugin.Plugin, name string) {
	if _, declared := target.Meta.Credential(name); !declared {
		writeError(writer, http.StatusNotFound, "not_found", "the plugin declares no such credential")
		return
	}
	ctx := request.Context()
	switch request.Method {
	case http.MethodPut:
		var input contract.PluginCredentialInput
		if !decodeControlJSON(writer, request, &input) {
			return
		}
		secret := []byte(input.Secret)
		defer clear(secret)
		if len(secret) == 0 || len(secret) > contract.MaxPluginSecretBytes || strings.ContainsAny(input.Secret, "\r\n") {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_credential", "a credential has 1 to 16384 bytes without line breaks")
			return
		}
		origin, err := plugin.NormalizeOrigin(input.Origin)
		if err != nil || !slices.ContainsFunc(target.Settings.Origins, func(allowed plugin.NetworkOrigin) bool { return allowed.Origin == origin }) {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_credential", "bind the credential to one of the origins the plugin may reach")
			return
		}
		if err := handler.pluginStore.PutPluginCredential(ctx, target.Key, name, origin, secret); err != nil {
			writeError(writer, http.StatusInternalServerError, "credential_unavailable", "could not save the credential")
			return
		}
	case http.MethodDelete:
		if err := handler.pluginStore.DeletePluginCredential(ctx, target.Key, name); err != nil && !errors.Is(err, storage.ErrNotFound) {
			writeError(writer, http.StatusInternalServerError, "credential_unavailable", "could not delete the credential")
			return
		}
	default:
		writer.Header().Set("Allow", "PUT, DELETE")
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use PUT or DELETE")
		return
	}
	handler.writePluginDetail(writer, ctx, target)
}

func (handler *Handler) testPlugin(writer http.ResponseWriter, ctx context.Context, target *plugin.Plugin) {
	if !target.Has(plugin.HookTestConnection) {
		writeError(writer, http.StatusNotFound, "not_found", "the plugin has no connection test")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, pluginPageBudget)
	defer cancel()
	raw, info, err := target.Call(ctx, plugin.HookTestConnection, nil, plugin.CallOptions{NetworkBudget: pluginPageBudget})
	result := contract.PluginTestResult{ElapsedMs: int(info.Elapsed.Milliseconds()), Network: networkCount(info.Network)}
	var failure *plugin.CallError
	switch {
	case errors.As(err, &failure):
		result.Status, result.Summary, result.Reason = contract.PluginTestFailed, failure.Message, failure.Reason
	case err != nil:
		result.Status, result.Summary, result.Reason = contract.PluginTestFailed, "the test did not run", plugin.ReasonUnavailable
	default:
		var reported struct {
			Status  string          `json:"status"`
			Summary string          `json:"summary"`
			View    json.RawMessage `json:"view"`
		}
		if json.Unmarshal(raw, &reported) != nil || (reported.Status != contract.PluginTestOK && reported.Status != contract.PluginTestError) {
			result.Status, result.Summary, result.Reason = contract.PluginTestFailed, "the test returned no status of ok or error", plugin.ReasonInvalidResult
			break
		}
		result.Status = reported.Status
		result.Summary = contract.ClampRunes(cleanSummary(reported.Summary), contract.MaxPluginRecordSummaryRunes)
		if target.Can(plugin.CapabilityViews) {
			result.View, result.Truncated = view.Parse(reported.View)
		}
	}
	writeJSON(writer, http.StatusOK, result)
}

func (handler *Handler) pluginStatus(writer http.ResponseWriter, ctx context.Context, target *plugin.Plugin) {
	stats := target.Stats()
	status := contract.PluginStatus{
		Calls:        stats.Calls,
		Failures:     map[string]int{},
		MemoryLimits: stats.Failures[plugin.ReasonMemory],
		Timeouts:     stats.Failures[plugin.ReasonTimeout],
		Network:      networkCount(stats.Network),
	}
	for reason, count := range stats.Failures {
		status.Failures[reason] = count
	}
	if target.Has(plugin.HookDescribeStatus) && target.Can(plugin.CapabilityViews) {
		ctx, cancel := context.WithTimeout(ctx, pluginPageBudget)
		defer cancel()
		raw, _, err := target.Call(ctx, plugin.HookDescribeStatus, nil, plugin.CallOptions{NetworkBudget: pluginPageBudget})
		var failure *plugin.CallError
		switch {
		case errors.As(err, &failure):
			status.ViewError = failure.Message
		case err != nil:
			status.ViewError = "the overview did not run"
		default:
			status.View, status.Truncated = view.Parse(raw)
		}
	}
	writeJSON(writer, http.StatusOK, status)
}

func networkCount(stats plugin.NetworkStats) contract.PluginNetworkCount {
	return contract.PluginNetworkCount{Requests: stats.Requests, Denied: stats.Denied, Errors: stats.Errors, WaitMs: int(stats.Wait.Milliseconds())}
}

// cleanSummary keeps a one-line summary from a plugin as plain text.
func cleanSummary(text string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 0x7f {
			return -1
		}
		return r
	}, strings.ToValidUTF8(text, "�"))
}

// registerEnabledPluginAttributes registers again the attributes of every
// enabled plugin, whose manifest a rescan may have changed. A conflict
// leaves that plugin's attributes as they were.
func (handler *Handler) registerEnabledPluginAttributes(ctx context.Context) {
	if handler.metadata == nil {
		return
	}
	for _, target := range handler.plugins.Current().Plugins() {
		if target.Enabled() {
			_ = handler.metadata.RegisterPlugin(ctx, target.Key, target.Attributes())
		}
	}
}
