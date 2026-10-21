package controlapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
)

// PluginHostPath serves the settings of every plugin at once.
const PluginHostPath = "/control/v1/plugin-host"

// PluginHost runs the plugins' part in requests; *pluginhost.Host
// satisfies it.
type PluginHost interface {
	Settings() contract.PluginHostSettings
	UpdateSettings(context.Context, contract.PluginHostSettingsUpdate) (contract.PluginHostSettings, error)
	SessionPlugins(ctx context.Context, session string) ([]contract.SessionPlugin, error)
	PauseSession(ctx context.Context, plugin, session string, paused bool, turn int) error
	Refresh(plugin, session string)
	SessionChoices(session string) map[string][]string
	SessionWorkspace(session string) (name string, ok bool)
	WorkspaceValue(ctx context.Context, session, key string) (value string, ok bool, err error)
	BindWorkspace(ctx context.Context, session, key, value string, replace bool) (bool, error)
	Action(ctx context.Context, target *plugin.Plugin, input contract.PluginActionInput) contract.PluginActionResult
}

// pluginHostRequiredRole lets observers read the settings of every plugin
// and keeps changing them to operators.
func pluginHostRequiredRole(request *http.Request) Role {
	if isSafeMethod(request.Method) {
		return RoleObserver
	}
	return RoleOperator
}

// pluginHostResource serves the pause of every plugin, the injection budget
// and the interface language the desktop tells Core.
func (handler *Handler) pluginHostResource(writer http.ResponseWriter, request *http.Request) {
	if handler.pluginHost == nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "plugins are unavailable")
		return
	}
	switch request.Method {
	case http.MethodGet:
		writeJSON(writer, http.StatusOK, handler.pluginHost.Settings())
	case http.MethodPatch:
		var update contract.PluginHostSettingsUpdate
		if !decodeControlJSON(writer, request, &update) {
			return
		}
		merged := handler.pluginHost.Settings()
		if update.Paused != nil {
			merged.Paused = *update.Paused
		}
		if update.InjectionBudgetBytes != nil {
			merged.InjectionBudgetBytes = *update.InjectionBudgetBytes
		}
		if update.Language != nil {
			merged.Language = *update.Language
		}
		if err := merged.Validate(); err != nil {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_plugin_host_settings", err.Error())
			return
		}
		settings, err := handler.pluginHost.UpdateSettings(request.Context(), update)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "plugin_unavailable", "could not save the settings of the plugins")
			return
		}
		writeJSON(writer, http.StatusOK, settings)
	default:
		writer.Header().Set("Allow", "GET, PATCH")
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or PATCH")
	}
}

// sessionPlugins serves the plugins' part in a session under
// /control/v1/request-sessions/{id}/plugins: the resident block each keeps
// in memory, the pause of one, and the refresh that makes the session's next
// request compute its block again.
func (handler *Handler) sessionPlugins(writer http.ResponseWriter, request *http.Request) {
	if handler.pluginHost == nil || handler.plugins == nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "plugins are unavailable")
		return
	}
	rest := strings.TrimPrefix(request.URL.Path, RequestSessionsPath+"/")
	rawID, path, _ := strings.Cut(rest, "/plugins")
	id := contract.SessionID(rawID)
	key, action, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if id.Validate() != nil || (path != "" && key == "") || (action != "" && action != "refresh") {
		writeError(writer, http.StatusNotFound, "not_found", "control API path not found")
		return
	}
	ctx := request.Context()
	detail, err := handler.requestRecords.GetRequestSession(ctx, string(id))
	if err != nil {
		handler.writeRequestRecordStoreError(writer, err)
		return
	}
	if key == "" {
		if allowMethods(writer, request, http.MethodGet) {
			handler.writeSessionPlugins(writer, ctx, id)
		}
		return
	}
	target := handler.plugins.Current().Plugin(key)
	if !contract.ValidPluginKey(key) || target == nil {
		writeError(writer, http.StatusNotFound, "not_found", "plugin not found")
		return
	}
	switch {
	case action == "refresh":
		if !allowMethods(writer, request, http.MethodPost) {
			return
		}
		handler.pluginHost.Refresh(key, string(id))
	default:
		if !allowMethods(writer, request, http.MethodPut) {
			return
		}
		var input contract.SessionPluginInput
		if !decodeControlJSON(writer, request, &input) {
			return
		}
		turn := detail.TurnCount
		for _, record := range detail.Turns {
			if record.TurnIndex != nil && *record.TurnIndex > turn {
				turn = *record.TurnIndex
			}
		}
		if err := handler.pluginHost.PauseSession(ctx, key, string(id), input.Paused, turn); err != nil {
			writeError(writer, http.StatusInternalServerError, "plugin_unavailable", "could not save the plugin's part in the session")
			return
		}
	}
	handler.writeSessionPlugins(writer, ctx, id)
}

func (handler *Handler) writeSessionPlugins(writer http.ResponseWriter, ctx context.Context, id contract.SessionID) {
	parts, err := handler.pluginHost.SessionPlugins(ctx, string(id))
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "plugin_unavailable", "could not read the plugins' part in the session")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"plugins": parts})
}

// pluginAction runs a plugin's onAction hook for a click on a button of one
// of its records.
func (handler *Handler) pluginAction(writer http.ResponseWriter, request *http.Request, target *plugin.Plugin) {
	if handler.pluginHost == nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "plugins are unavailable")
		return
	}
	if !target.Enabled() || !target.Has(plugin.HookOnAction) {
		writeError(writer, http.StatusNotFound, "not_found", "the plugin takes no actions")
		return
	}
	var input contract.PluginActionInput
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	if !contract.ValidPluginActionID(input.Action) || contract.SessionID(input.Session).Validate() != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_plugin_action", "an action names one of the record's buttons and its session")
		return
	}
	if _, err := handler.requestRecords.GetRequestSession(request.Context(), input.Session); err != nil {
		handler.writeRequestRecordStoreError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, handler.pluginHost.Action(request.Context(), target, input))
}

// clearPluginData removes what the host keeps for a plugin: the workspaces
// bound to its attributes' values, or its own values.
func (handler *Handler) clearPluginData(writer http.ResponseWriter, request *http.Request, target *plugin.Plugin, what string) {
	if !allowMethods(writer, request, http.MethodDelete) {
		return
	}
	ctx := request.Context()
	removed := 0
	var err error
	switch what {
	case "workspaces":
		if handler.metadataStore == nil {
			writeError(writer, http.StatusServiceUnavailable, "unavailable", "metadata is unavailable")
			return
		}
		removed, err = handler.metadataStore.DeleteWorkspaceValues(ctx, pluginAttributeKeys(target))
	case "values":
		removed, err = handler.pluginStore.DeletePluginValues(ctx, target.Key)
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "plugin_unavailable", "could not clear the plugin's data")
		return
	}
	writeJSON(writer, http.StatusOK, contract.PluginClearResult{Removed: removed})
}

func pluginAttributeKeys(target *plugin.Plugin) []string {
	keys := make([]string, 0, len(target.Meta.Metadata))
	for _, attribute := range target.Meta.Metadata {
		keys = append(keys, attribute.Key)
	}
	return keys
}

// pluginPageView is what describeStatus and testConnection read: the
// interface language, for the plugin to write its views in.
type pluginPageView struct {
	Language string `json:"language"`
}

func (handler *Handler) pluginPageView() pluginPageView {
	language := contract.DefaultPluginLanguage
	if handler.pluginHost != nil {
		language = handler.pluginHost.Settings().Language
	}
	return pluginPageView{Language: language}
}
