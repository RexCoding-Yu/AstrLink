package controlapi

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/pluginhost"
)

func TestRequestPluginInstancesAreCheckedWhenSaved(t *testing.T) {
	_, handler, manager, directory := newPluginHandler(t)
	host := pluginhost.New(pluginhost.Options{Plugins: manager})
	t.Cleanup(host.Close)
	handler.pluginHost = host
	text, err := os.ReadFile("../ingress/testdata/newapi/request-filter/plugin.js")
	if err != nil {
		t.Fatal(err)
	}
	writePlugin(t, directory, "request-filter", "plugin.js", string(text))
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if response := approvedRequest(t, handler, http.MethodPatch, PluginsPath+"/request-filter", "application/json", `{"enabled":true}`, ""); response.Code != http.StatusOK {
		t.Fatalf("enable = %d %s", response.Code, response.Body)
	}
	detail := decodeBody[contract.PluginDetail](t, controlRequest(t, handler, http.MethodGet, PluginsPath+"/request-filter", "", "", ""))
	if detail.Kind != contract.PluginKindRequest || detail.Request == nil || strings.Join(detail.Request.Stages, ",") != "ingress,egress,reply" || len(detail.Request.ConfigSchema) == 0 {
		t.Fatalf("detail = %+v %+v", detail.PluginSummary, detail.Request)
	}
	patch := func(body string) *contract.PluginHostSettings {
		response := controlRequest(t, handler, http.MethodPatch, PluginHostPath, "application/json", body, "")
		if response.Code != http.StatusOK {
			t.Logf("PATCH = %d %s", response.Code, response.Body)
			return nil
		}
		settings := decodeBody[contract.PluginHostSettings](t, response)
		return &settings
	}
	saved := patch(`{"request_plugins":[{"name":" 过滤 ","plugin":"request-filter","enabled":true,"models":[" gpt-4o "],"config":{"keywords":"a\nb"}}]}`)
	if saved == nil || len(saved.RequestPlugins) != 1 || !contract.ValidRequestPluginInstanceID(saved.RequestPlugins[0].ID) ||
		saved.RequestPlugins[0].Name != "过滤" || saved.RequestPlugins[0].Models[0] != "gpt-4o" {
		t.Fatalf("saved = %+v", saved)
	}
	for _, body := range []string{
		// The schema refuses a value of another type,
		`{"request_plugins":[{"name":"x","plugin":"request-filter","enabled":true,"config":{"replies":"yes"}}]}`,
		// validateConfig a rule that does not compile,
		`{"request_plugins":[{"name":"x","plugin":"request-filter","enabled":true,"config":{"rules":[{"type":"regex","pattern":"(open"}]}}]}`,
		// and a plugin without a request processing part has no instances.
		`{"request_plugins":[{"name":"x","plugin":"demo","enabled":true}]}`,
	} {
		response := controlRequest(t, handler, http.MethodPatch, PluginHostPath, "application/json", body, "")
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("PATCH %s = %d %s", body, response.Code, response.Body)
		}
	}
}
