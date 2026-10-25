package taskplugin

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// Route is a native route of an enabled task plugin.
type Route struct {
	Plugin      string
	Method      string
	Path        string
	Type        string
	Action      string
	TaskIDParam string
	Decode      string
	Render      string
	Models      []string
	Retain      bool
	segments    []string
}

// routeIndex is the native routes of one generation of plugins.
type routeIndex struct {
	generation *plugin.Generation
	routes     []Route
}

// Store is where the engine keeps its tasks.
func (engine *Engine) Store() storage.PluginTaskStore { return engine.store }

// TaskPlugin returns the manifest of the enabled task plugin key names,
// nil when there is none.
func (engine *Engine) TaskPlugin(key string) *plugin.TaskMeta {
	_, meta, err := engine.plugin(key)
	if err != nil {
		return nil
	}
	return meta
}

// Claims reports whether an enabled task plugin claims protocol, so that
// the gateway looks at a request's model before routing it as usual.
func (engine *Engine) Claims(protocol string) bool {
	for _, found := range engine.TaskPlugins() {
		if found.Task().Claim(protocol) != nil {
			return true
		}
	}
	return false
}

// Claimants are the enabled task plugins that serve model on protocol, by
// key.
func (engine *Engine) Claimants(protocol, model string) []string {
	var keys []string
	for _, found := range engine.TaskPlugins() {
		if found.Task().ServesProtocol(protocol, model) {
			keys = append(keys, found.Key)
		}
	}
	return keys
}

// ResponseModes are the forms of Responses request the plugins that claim
// model take: stream, sync and background.
func (engine *Engine) ResponseModes(model string) []string {
	var modes []string
	for _, found := range engine.TaskPlugins() {
		meta := found.Task()
		if !meta.ServesProtocol(plugin.TaskProtocolResponses, model) {
			continue
		}
		for _, mode := range meta.Claim(plugin.TaskProtocolResponses).Supports {
			if !slices.Contains(modes, mode) {
				modes = append(modes, mode)
			}
		}
	}
	return modes
}

// nativeRoutes indexes the routes of the enabled task plugins of the
// current generation, the plugin of the lower key taking a route two
// declare.
func (engine *Engine) nativeRoutes() []Route {
	if engine.plugins == nil {
		return nil
	}
	generation := engine.plugins.Current()
	engine.routeMutex.Lock()
	defer engine.routeMutex.Unlock()
	if index := engine.routes; index != nil && index.generation == generation {
		return index.routes
	}
	var indexed []Route
	shapes := map[string]string{}
	for _, found := range generation.Plugins() {
		if !found.Enabled() || found.Task() == nil {
			continue
		}
		for _, route := range found.Task().Routes {
			shape := route.Method + " " + strings.ToLower(normalizedShape(route.Path))
			if owner, taken := shapes[shape]; taken {
				engine.logf("task plugin %s: the route %s %s is %s's", found.Key, route.Method, route.Path, owner)
				continue
			}
			shapes[shape] = found.Key
			indexed = append(indexed, Route{
				Plugin: found.Key, Method: route.Method, Path: route.Path, Type: route.Type, Action: route.Action,
				TaskIDParam: route.TaskIDName(), Decode: route.Decode, Render: route.Render, Models: route.Models,
				Retain: route.Retains(), segments: strings.Split(strings.Trim(route.Path, "/"), "/"),
			})
		}
	}
	engine.routes = &routeIndex{generation: generation, routes: indexed}
	return indexed
}

func normalizedShape(path string) string {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	for index, segment := range segments {
		if strings.HasPrefix(segment, ":") {
			segments[index] = ":"
		}
	}
	return "/" + strings.Join(segments, "/")
}

// MatchRoute finds the native route a request's method and path take, and
// the path's parameters.
func (engine *Engine) MatchRoute(method, path string) (Route, map[string]string, bool) {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	for _, route := range engine.nativeRoutes() {
		if route.Method != method || len(route.segments) != len(segments) {
			continue
		}
		params := map[string]string{}
		matched := true
		for index, segment := range route.segments {
			switch {
			case strings.HasPrefix(segment, ":"):
				if segments[index] == "" {
					matched = false
				}
				params[segment[1:]] = segments[index]
			case segment != segments[index]:
				matched = false
			}
			if !matched {
				break
			}
		}
		if matched {
			return route, params, true
		}
	}
	return Route{}, nil, false
}

// VideoArtifact is the artifact /v1/videos/:id/content serves: the one
// variant names, else the first video, else the first of any kind.
func (engine *Engine) VideoArtifact(ctx context.Context, task storage.PluginTask, variant string) (string, error) {
	artifacts, err := engine.Artifacts(ctx, task)
	if err != nil {
		return "", err
	}
	if variant != "" && variant != "video" {
		for _, artifact := range artifacts {
			if artifact.Key == variant {
				return artifact.Key, nil
			}
		}
		return "", failure(http.StatusNotFound, "artifact_not_found", "the video has no %s", variant)
	}
	for _, artifact := range artifacts {
		if artifact.Type == "video" {
			return artifact.Key, nil
		}
	}
	if len(artifacts) > 0 {
		return artifacts[0].Key, nil
	}
	if task.Status != "SUCCESS" {
		return "", failure(http.StatusNotFound, "video_not_ready", "the video is not ready")
	}
	return "", failure(http.StatusNotFound, "artifact_not_found", "the video has no content to download")
}
