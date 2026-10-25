package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// A task plugin is a plugin written for new-api's task plugin API v1: a
// single synchronous module whose hooks turn requests for generated media,
// images and videos, into a vendor's task API and back. The gateway selects
// the provider, sends the requests, keeps the tasks and polls them; the
// plugin only transforms data. Its manifest is TaskMeta.

// Protocols a task plugin claims.
const (
	TaskProtocolResponses = "openai_responses"
	TaskProtocolVideo     = "openai_video"
	TaskProtocolImage     = "openai_image"
)

// Modes of the openai_responses protocol.
const (
	TaskModeStream     = "stream"
	TaskModeSync       = "sync"
	TaskModeBackground = "background"
)

// Host capabilities a task plugin may require.
var taskCapabilities = []string{"json-clone@1", "submit-sse-delta@1", "json-order@1", "duration-auto@1"}

// TaskMeta is the manifest of a task plugin.
type TaskMeta struct {
	APIVersion           int                 `json:"apiVersion"`
	Key                  string              `json:"key"`
	Name                 string              `json:"name"`
	Icon                 string              `json:"icon,omitempty"`
	Description          LocalizedText       `json:"description,omitempty"`
	Version              string              `json:"version"`
	Author               TaskAuthor          `json:"author"`
	Website              string              `json:"website,omitempty"`
	SortPriority         int32               `json:"sortPriority,omitempty"`
	BaseURL              string              `json:"baseUrl,omitempty"`
	ChannelTypes         []int               `json:"channelTypes,omitempty"`
	Models               []string            `json:"models"`
	FetchMode            string              `json:"fetchMode"`
	AllowedHosts         []string            `json:"allowedHosts,omitempty"`
	Upstreams            []string            `json:"upstreams,omitempty"`
	Routes               []TaskRoute         `json:"routes,omitempty"`
	Protocols            []TaskProtocolClaim `json:"protocols,omitempty"`
	SubmitResponseTypes  []string            `json:"submitResponseTypes,omitempty"`
	RequiredCapabilities []string            `json:"requiredCapabilities,omitempty"`
	UsageSchema          json.RawMessage     `json:"usageSchema,omitempty"`
	UsageExamples        json.RawMessage     `json:"usageExamples,omitempty"`
	UsageProfiles        json.RawMessage     `json:"usageProfiles,omitempty"`
	Auth                 TaskAuth            `json:"auth,omitempty"`
}

// TaskAuthor is who wrote a task plugin, as it says.
type TaskAuthor struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// LocalizedText is a text in several languages: a plain string reads as
// English.
type LocalizedText map[string]string

func (text *LocalizedText) UnmarshalJSON(data []byte) error {
	var plain string
	if json.Unmarshal(data, &plain) == nil {
		*text = LocalizedText{"en": plain}
		return nil
	}
	var languages map[string]string
	if err := json.Unmarshal(data, &languages); err != nil {
		return errors.New("a localized text is a string or an object of strings by language")
	}
	*text = languages
	return nil
}

// In returns the text for language, its primary language or English.
func (text LocalizedText) In(language string) string {
	if value, ok := text[language]; ok {
		return value
	}
	if primary, _, found := strings.Cut(language, "-"); found {
		if value, ok := text[primary]; ok {
			return value
		}
	}
	return text["en"]
}

// TaskRoute is a vendor-native URL a task plugin serves on the inference
// port.
type TaskRoute struct {
	Method       string   `json:"method"`
	Path         string   `json:"path"`
	Type         string   `json:"type"`
	Action       string   `json:"action,omitempty"`
	TaskIDParam  string   `json:"taskIdParam,omitempty"`
	Decode       string   `json:"decode,omitempty"`
	Render       string   `json:"render"`
	Models       []string `json:"models,omitempty"`
	RetainResult *bool    `json:"retainResult,omitempty"`
}

// Retains reports whether the tasks the route submits keep their result.
func (route TaskRoute) Retains() bool { return route.RetainResult == nil || *route.RetainResult }

// TaskIDName is the path parameter that holds the task id of a query route.
func (route TaskRoute) TaskIDName() string {
	if route.TaskIDParam == "" {
		return "task_id"
	}
	return route.TaskIDParam
}

// TaskProtocolClaim is a host protocol a task plugin serves: a bare name, or
// a name with the modes of openai_responses and the models it narrows to.
type TaskProtocolClaim struct {
	Name     string   `json:"name"`
	Supports []string `json:"supports,omitempty"`
	Models   []string `json:"models,omitempty"`
}

func (claim *TaskProtocolClaim) UnmarshalJSON(data []byte) error {
	var name string
	if json.Unmarshal(data, &name) == nil {
		*claim = TaskProtocolClaim{Name: name}
		return nil
	}
	type plain TaskProtocolClaim
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var parsed plain
	if err := decoder.Decode(&parsed); err != nil {
		return fmt.Errorf("a protocol is a name or {name, supports, models}: %v", strings.TrimPrefix(err.Error(), "json: "))
	}
	*claim = TaskProtocolClaim(parsed)
	return nil
}

// Serves reports whether the claim covers model, which the plugin lists.
func (claim TaskProtocolClaim) Serves(model string) bool {
	return len(claim.Models) == 0 || slices.Contains(claim.Models, model)
}

// TaskAuth is how a task plugin's provider authenticates: api_key, the
// default, sends the key; oauth2_jwt exchanges a service account for a
// token; none sends nothing.
type TaskAuth string

// Kinds of TaskAuth.
const (
	TaskAuthAPIKey    TaskAuth = "api_key"
	TaskAuthOAuth2JWT TaskAuth = "oauth2_jwt"
	TaskAuthNone      TaskAuth = "none"
)

func (auth *TaskAuth) UnmarshalJSON(data []byte) error {
	var name string
	if json.Unmarshal(data, &name) != nil {
		var object struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &object); err != nil {
			return errors.New("auth is a name or {type}")
		}
		name = object.Type
	}
	switch name {
	case "", "api_key":
		*auth = TaskAuthAPIKey
	case "none":
		*auth = TaskAuthNone
	case "oauth2_jwt", "vertex_oauth":
		*auth = TaskAuthOAuth2JWT
	default:
		return fmt.Errorf("auth %q is not one of none, api_key and oauth2_jwt", name)
	}
	return nil
}

// Kind returns the auth, api_key when the manifest says none.
func (auth TaskAuth) Kind() TaskAuth {
	if auth == "" {
		return TaskAuthAPIKey
	}
	return auth
}

// Claim returns the plugin's claim on protocol, nil when it makes none.
func (meta *TaskMeta) Claim(protocol string) *TaskProtocolClaim {
	for index := range meta.Protocols {
		if meta.Protocols[index].Name == protocol {
			return &meta.Protocols[index]
		}
	}
	return nil
}

// ServesProtocol reports whether the plugin serves model on protocol.
func (meta *TaskMeta) ServesProtocol(protocol, model string) bool {
	claim := meta.Claim(protocol)
	return claim != nil && slices.Contains(meta.Models, model) && claim.Serves(model)
}

// Has reports whether the plugin requires the host capability.
func (meta *TaskMeta) Has(capability string) bool {
	return slices.Contains(meta.RequiredCapabilities, capability)
}

// SubmitsSSE reports whether the plugin may read a submission as SSE.
func (meta *TaskMeta) SubmitsSSE() bool { return slices.Contains(meta.SubmitResponseTypes, "sse") }

// Batch reports whether the plugin polls its tasks in batches.
func (meta *TaskMeta) Batch() bool { return meta.FetchMode == "batch" }

var (
	taskVersionPattern = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	localePattern      = regexp.MustCompile(`^[a-zA-Z]{2,3}(-[a-zA-Z0-9]{2,8})*$`)
	routeParamPattern  = regexp.MustCompile(`^:[A-Za-z_][A-Za-z0-9_]*$`)
)

// validateTaskMeta checks a task plugin's manifest against key, the name it
// was found under, as new-api's v1 contract does, and the exports against
// what the manifest declares.
func validateTaskMeta(meta *TaskMeta, key string, exported map[string]bool) error {
	invalid := func(format string, args ...any) error {
		return &LoadError{Code: CodeInvalidMeta, Message: fmt.Sprintf(format, args...)}
	}
	switch {
	case meta.APIVersion != 1:
		return invalid("meta.apiVersion %d is not supported; task plugins use 1", meta.APIVersion)
	case !ValidPluginKey(meta.Key) || len(meta.Key) > 30:
		return invalid("meta.key %q must be a lowercase letter followed by up to 29 lowercase letters, digits or hyphens", meta.Key)
	case key != "" && meta.Key != key:
		return invalid("meta.key %q does not match the plugin directory %q", meta.Key, key)
	case strings.TrimSpace(meta.Name) == "" || utf8.RuneCountInString(meta.Name) > 128:
		return invalid("meta.name must be 1 to 128 characters")
	case utf8.RuneCountInString(meta.Icon) > 128 || strings.HasPrefix(meta.Icon, "data:") || strings.Contains(meta.Icon, "://"):
		return invalid("meta.icon is a LobeHub icon name or text, at most 128 characters")
	case !taskVersionPattern.MatchString(meta.Version):
		return invalid("meta.version %q is not a semantic version", meta.Version)
	case strings.TrimSpace(meta.Author.Name) == "":
		return invalid("meta.author.name is required")
	case meta.Author.URL != "" && !httpURL(meta.Author.URL, false):
		return invalid("meta.author.url must be an absolute http or https URL")
	case meta.Website != "" && !strings.HasPrefix(strings.ToLower(meta.Website), "https://") || meta.Website != "" && !httpURL(meta.Website, false):
		return invalid("meta.website must be an absolute https URL")
	case meta.FetchMode != "per_task" && meta.FetchMode != "batch":
		return invalid("meta.fetchMode must be per_task or batch")
	case len(meta.Models) == 0:
		return invalid("meta.models must list at least one model")
	}
	if err := validateLocalized(meta.Description, 512, "meta.description"); err != nil {
		return invalid("%v", err)
	}
	if meta.BaseURL != "" {
		parsed, err := url.Parse(meta.BaseURL)
		if err != nil || !httpURL(meta.BaseURL, true) || parsed.RawQuery != "" || parsed.Fragment != "" || len(meta.BaseURL) > 191 {
			return invalid("meta.baseUrl must be an absolute http or https URL without credentials, query or fragment, at most 191 characters")
		}
		meta.BaseURL = strings.TrimRight(meta.BaseURL, "/")
	}
	seenModels := map[string]bool{}
	for _, model := range meta.Models {
		if strings.TrimSpace(model) == "" || seenModels[model] {
			return invalid("meta.models must be unique and not empty")
		}
		seenModels[model] = true
	}
	for _, channel := range meta.ChannelTypes {
		if channel < 1 || channel == 60 || channel == 61 {
			return invalid("meta.channelTypes may not hold %d", channel)
		}
	}
	for index, host := range meta.AllowedHosts {
		normalized, ok := normalizeAllowedHost(host)
		if !ok {
			return invalid("meta.allowedHosts[%d] %q must be a host or host:port", index, host)
		}
		if slices.Contains(meta.AllowedHosts[:index], normalized) {
			return invalid("meta.allowedHosts lists %q twice", normalized)
		}
		meta.AllowedHosts[index] = normalized
	}
	for _, upstream := range meta.Upstreams {
		if upstream != "vendor" && upstream != "new_api" {
			return invalid("meta.upstreams may hold vendor and new_api, not %q", upstream)
		}
	}
	for _, capability := range meta.RequiredCapabilities {
		if !slices.Contains(taskCapabilities, capability) {
			return invalid("meta.requiredCapabilities: %q is not a capability this host has", capability)
		}
	}
	for _, kind := range meta.SubmitResponseTypes {
		if kind != "json" && kind != "sse" {
			return invalid("meta.submitResponseTypes may hold json and sse, not %q", kind)
		}
	}
	if meta.SubmitsSSE() && !exported["parseSubmitEvent"] && !exported["parseSubmitEventDelta"] {
		return invalid("a plugin that reads submissions as SSE exports parseSubmitEvent or parseSubmitEventDelta")
	}
	if meta.Has("submit-sse-delta@1") && (!meta.SubmitsSSE() || !exported["parseSubmitEventDelta"]) {
		return invalid("submit-sse-delta@1 needs sse in meta.submitResponseTypes and parseSubmitEventDelta")
	}
	// The hooks every driver needs, and those of its way of polling.
	required := []string{"buildSubmitRequest", "parseSubmitResponse", "parseTaskResult"}
	if meta.Batch() {
		required = append(required, "buildBatchQueryRequest", "parseBatchResult")
	} else {
		required = append(required, "buildQueryRequest")
	}
	for _, hook := range required {
		if !exported[hook] {
			return invalid("a %s plugin exports %s", meta.FetchMode, hook)
		}
	}
	if exported["listArtifacts"] != exported["buildContentRequest"] {
		return invalid("listArtifacts and buildContentRequest are exported together")
	}
	for _, rejected := range []string{"endpoints", "resolveRequest", "renderError", "renderers"} {
		if exported[rejected] {
			return invalid("the export %q belongs to an earlier task plugin API", rejected)
		}
	}
	routes := map[string]bool{}
	for index, route := range meta.Routes {
		where := fmt.Sprintf("meta.routes[%d]", index)
		switch route.Method {
		case "GET", "POST", "PUT", "PATCH", "DELETE":
		default:
			return invalid("%s.method must be GET, POST, PUT, PATCH or DELETE", where)
		}
		if !strings.HasPrefix(route.Path, "/") || strings.HasPrefix(route.Path, "/:") || reservedTaskRoute(route.Path) {
			return invalid("%s.path %q must start with a fixed segment and stay off the gateway's own paths", where, route.Path)
		}
		for _, segment := range strings.Split(strings.Trim(route.Path, "/"), "/") {
			if strings.HasPrefix(segment, ":") && !routeParamPattern.MatchString(segment) {
				return invalid("%s.path has a malformed parameter %q", where, segment)
			}
		}
		if !exported["native."+route.Render] {
			return invalid("%s.render %q is not a function of native", where, route.Render)
		}
		switch route.Type {
		case "submit", "dynamic":
			if route.Decode == "" || !exported["native."+route.Decode] {
				return invalid("%s.decode %q is not a function of native", where, route.Decode)
			}
		case "query":
			if route.Decode != "" || len(route.Models) > 0 || route.RetainResult != nil {
				return invalid("%s: a query route takes no decode, models or retainResult", where)
			}
			if !strings.Contains(route.Path, ":"+route.TaskIDName()) {
				return invalid("%s.path does not hold :%s", where, route.TaskIDName())
			}
		default:
			return invalid("%s.type must be submit, query or dynamic", where)
		}
		for _, model := range route.Models {
			if !seenModels[model] {
				return invalid("%s.models lists %q, which meta.models does not", where, model)
			}
		}
		shape := route.Method + " " + routeShape(route.Path)
		if routes[shape] {
			return invalid("%s repeats the route %s", where, shape)
		}
		routes[shape] = true
	}
	claimed := map[string]bool{}
	for index, claim := range meta.Protocols {
		where := fmt.Sprintf("meta.protocols[%d]", index)
		if claimed[claim.Name] {
			return invalid("%s claims %s twice", where, claim.Name)
		}
		claimed[claim.Name] = true
		for _, model := range claim.Models {
			if !seenModels[model] {
				return invalid("%s.models lists %q, which meta.models does not", where, model)
			}
		}
		member := func(name string) bool { return exported["protocols."+claim.Name+"."+name] }
		switch claim.Name {
		case TaskProtocolResponses:
			if len(claim.Supports) == 0 {
				return invalid("%s: openai_responses names the modes it supports", where)
			}
			for _, mode := range claim.Supports {
				if mode != TaskModeStream && mode != TaskModeSync && mode != TaskModeBackground {
					return invalid("%s.supports may hold stream, sync and background, not %q", where, mode)
				}
			}
			final := slices.Contains(claim.Supports, TaskModeSync) || slices.Contains(claim.Supports, TaskModeBackground)
			events := slices.Contains(claim.Supports, TaskModeStream)
			switch {
			case !member("decodeRequest"):
				return invalid("protocols.openai_responses.decodeRequest is required")
			case events != member("renderEvents"):
				return invalid("protocols.openai_responses.renderEvents goes with the stream mode")
			case final != member("renderFinal"):
				return invalid("protocols.openai_responses.renderFinal goes with the sync and background modes")
			}
		case TaskProtocolVideo, TaskProtocolImage:
			if len(claim.Supports) > 0 {
				return invalid("%s: %s has no modes", where, claim.Name)
			}
			if !member("decodeRequest") || !member("render") {
				return invalid("protocols.%s exports decodeRequest and render", claim.Name)
			}
		default:
			return invalid("%s: %q is not a protocol of this host", where, claim.Name)
		}
	}
	return nil
}

// reservedTaskPaths are the paths of the inference port, with what lies
// below them, that a native route may not take: the gateway's own, and /api,
// which new-api keeps for itself.
var reservedTaskPaths = []string{
	"/api", "/astrlink", "/plugins", "/tools", "/control", "/v1beta/models",
	"/v1/responses", "/v1/videos", "/v1/tasks", "/v1/images", "/v1/models", "/v1/chat", "/v1/completions",
	"/v1/messages", "/v1/embeddings", "/v1/rerank", "/v1/alpha",
}

func reservedTaskRoute(path string) bool {
	path = strings.ToLower(strings.TrimRight(path, "/"))
	if path == "" || path == "/v1" {
		return true
	}
	for _, reserved := range reservedTaskPaths {
		if path == reserved || strings.HasPrefix(path, reserved+"/") {
			return true
		}
	}
	return false
}

// routeShape is a route's path with its parameters made alike, for the
// routes of two plugins or one to be told apart.
func routeShape(path string) string {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	for index, segment := range segments {
		if strings.HasPrefix(segment, ":") {
			segments[index] = ":"
		}
	}
	shape := "/" + strings.Join(segments, "/")
	if strings.HasSuffix(path, "/") && shape != "/" {
		shape += "/"
	}
	return shape
}

// validateLocalized checks a localized text: English when it has any
// language, valid language tags, and texts of at most max characters.
func validateLocalized(text LocalizedText, max int, where string) error {
	if len(text) == 0 {
		return nil
	}
	if len(text) > 16 {
		return fmt.Errorf("%s has more than 16 languages", where)
	}
	if strings.TrimSpace(text["en"]) == "" {
		return fmt.Errorf("%s needs an English text", where)
	}
	for language, value := range text {
		if !localePattern.MatchString(language) {
			return fmt.Errorf("%s: %q is not a language tag", where, language)
		}
		if strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > max {
			return fmt.Errorf("%s: the %s text is empty or longer than %d characters", where, language, max)
		}
	}
	return nil
}

// httpURL reports whether raw is an absolute http or https URL, without
// credentials when credentialless is set.
func httpURL(raw string, credentialless bool) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return false
	}
	return !credentialless || parsed.User == nil
}

// normalizeAllowedHost lowercases a host or host:port of meta.allowedHosts.
func normalizeAllowedHost(host string) (string, bool) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || strings.ContainsAny(host, "/?#@ \t") {
		return "", false
	}
	if name, port, err := net.SplitHostPort(host); err == nil {
		if name == "" || port == "" {
			return "", false
		}
		return host, true
	}
	if strings.HasPrefix(host, "[") || strings.Count(host, ":") > 0 {
		return "", false
	}
	return host, true
}
