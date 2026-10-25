package taskplugin

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// Artifact is an output of a finished task the plugin lists: a key, its
// kind and media type.
type Artifact struct {
	Key      string `json:"key"`
	Type     string `json:"type"`
	MimeType string `json:"mimeType,omitempty"`
}

// artifactLink is an artifact as ctx.artifacts holds it for a renderer.
type artifactLink struct {
	Key      string `json:"key"`
	Type     string `json:"type"`
	MimeType string `json:"mimeType,omitempty"`
	URL      string `json:"url"`
}

// resultArtifact is the key of the one artifact a task without
// listArtifacts has: the result its parse hook named.
const resultArtifact = "result"

// Artifacts lists a finished task's outputs: what the plugin's
// listArtifacts says, else the result its parse hook named.
func (engine *Engine) Artifacts(ctx context.Context, task storage.PluginTask) ([]Artifact, error) {
	if task.Status != contract.PluginTaskSuccess {
		return []Artifact{}, nil
	}
	target, _, err := engine.plugin(task.PluginKey)
	if err != nil {
		return nil, err
	}
	return engine.listArtifacts(ctx, target, task)
}

func (engine *Engine) listArtifacts(ctx context.Context, target *plugin.Plugin, task storage.PluginTask) ([]Artifact, error) {
	artifacts := []Artifact{}
	if target.HasTaskHook("listArtifacts") {
		argument := map[string]any{"taskId": task.ID, "status": task.Status, "action": task.Action, "data": jsonOrNull(task.Data), "producerVersion": task.PluginVersion}
		raw, err := engine.renderCall(ctx, target, "listArtifacts", argument)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &artifacts); err != nil {
			return nil, failure(http.StatusBadGateway, "plugin_error", "listArtifacts returned something other than a list of artifacts")
		}
		valid := artifacts[:0]
		for _, artifact := range artifacts {
			if artifact.Key != "" && len(artifact.Key) <= 64 && !strings.ContainsAny(artifact.Key, "/?#") {
				valid = append(valid, artifact)
			}
		}
		if len(valid) > 0 || task.ResultURL == "" {
			return valid, nil
		}
		artifacts = valid
	}
	if task.ResultURL != "" {
		kind, mediaType := "file", ""
		if stored, ok := storedResult(task); ok {
			mediaType = stored
		}
		switch {
		case strings.HasPrefix(mediaType, "video/") || task.Surface == plugin.TaskProtocolVideo:
			kind = "video"
		case strings.HasPrefix(mediaType, "image/") || task.Surface == plugin.TaskProtocolImage:
			kind = "image"
		case strings.HasPrefix(mediaType, "audio/"):
			kind = "audio"
		}
		artifacts = append(artifacts, Artifact{Key: resultArtifact, Type: kind, MimeType: mediaType})
	}
	return artifacts, nil
}

// artifactLinks are the artifacts of a finished task as a renderer reads
// them, each with the link links makes; nil when there are none.
func (engine *Engine) artifactLinks(ctx context.Context, target *plugin.Plugin, task storage.PluginTask, links func(key string) string) map[string]artifactLink {
	if links == nil {
		return nil
	}
	artifacts, err := engine.listArtifacts(ctx, target, task)
	if err != nil {
		engine.logf("task %s: listArtifacts: %v", task.ID, err)
		return map[string]artifactLink{}
	}
	result := map[string]artifactLink{}
	for _, artifact := range artifacts {
		result[artifact.Key] = artifactLink{Key: artifact.Key, Type: artifact.Type, MimeType: artifact.MimeType, URL: links(artifact.Key)}
	}
	return result
}

// Access is the capability that opens one artifact of one task without the
// owner's token: a signature of both, which does not expire.
func (engine *Engine) Access(id, key string) string {
	mac := hmac.New(sha256.New, engine.signingKey)
	mac.Write([]byte("artifact\x00" + id + "\x00" + key))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyAccess reports whether access opens the artifact key of task id.
func (engine *Engine) VerifyAccess(id, key, access string) bool {
	return access != "" && hmac.Equal([]byte(engine.Access(id, key)), []byte(access))
}

// ArtifactURL is the link that serves an artifact under base, the address
// the client reached the gateway at.
func (engine *Engine) ArtifactURL(base, id, key string) string {
	return strings.TrimRight(base, "/") + "/v1/tasks/" + url.PathEscape(id) + "/artifacts/" + url.PathEscape(key) + "/content?access=" + engine.Access(id, key)
}

// contentHeaders are the client headers a download passes on, and the
// answer headers it passes back.
var (
	contentRequestHeaders = []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since", "If-Match", "If-Unmodified-Since"}
	contentAnswerHeaders  = []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Etag", "Last-Modified", "Content-Disposition", "Cache-Control"}
)

// ServeContent writes an artifact of a finished task: through the
// plugin's buildContentRequest, from the result kept beside the task, or
// from the link the vendor named.
func (engine *Engine) ServeContent(ctx context.Context, writer http.ResponseWriter, request *http.Request, task storage.PluginTask, key string) error {
	if task.Status != contract.PluginTaskSuccess {
		return failure(http.StatusNotFound, "artifact_not_found", "the task has not finished")
	}
	target, meta, err := engine.plugin(task.PluginKey)
	if err != nil {
		return err
	}
	artifacts, err := engine.listArtifacts(ctx, target, task)
	if err != nil {
		return err
	}
	var artifact *Artifact
	for index := range artifacts {
		if artifacts[index].Key == key {
			artifact = &artifacts[index]
		}
	}
	if artifact == nil {
		return failure(http.StatusNotFound, "artifact_not_found", "the task has no artifact %s", key)
	}
	method := http.MethodGet
	if request.Method == http.MethodHead {
		method = http.MethodHead
	}
	client := http.Header{}
	for _, name := range contentRequestHeaders {
		if value := request.Header.Get(name); value != "" {
			client.Set(name, value)
		}
	}
	service, err := engine.taskService(ctx, task)
	if err != nil {
		return failure(http.StatusServiceUnavailable, "provider_unavailable", "the provider of the task is gone")
	}
	if key != resultArtifact && target.HasTaskHook("buildContentRequest") {
		found, err := engine.credentialsFor(ctx, service, meta)
		if err != nil {
			return err
		}
		call := &exchange{engine: engine, target: target, service: service, meta: meta, credentials: found}
		driver := map[string]any{
			"requestBody": jsonOrNull(task.Request), "requestHeaders": map[string]string{}, "action": task.Action, "model": task.Model,
			"upstreamModel": task.UpstreamModel, "baseUrl": found.baseURL, "apiKey": found.apiKey, "authHeader": found.authHeader,
			"auth": found.auth, "upstream": found.upstream, "files": []any{}, "publicTaskId": task.ID, "userSetting": settingsOrEmpty(found.settings),
			"artifactKey": key, "data": jsonOrNull(task.Data), "state": jsonOrNull(task.State), "upstreamTaskId": upstreamTaskID(task),
			"clientRequest": map[string]any{"method": method, "headers": firstHeaders(client)},
		}
		raw, err := call.call(ctx, "buildContentRequest", driver)
		if err != nil {
			return err
		}
		desc, err := parseDescriptor("buildContentRequest", raw)
		if err != nil {
			return err
		}
		headers := client
		if !desc.Credentialless {
			// The plugin's own headers carry the key; the client's
			// range comes after.
			headers = client.Clone()
		}
		response, err := call.send(ctx, "buildContentRequest", desc, headers)
		if err != nil {
			return err
		}
		return relayContent(writer, response, artifact)
	}
	if mediaType, ok := storedResult(task); ok {
		file, err := os.Open(engine.resultPath(task.ID))
		if err != nil {
			return failure(http.StatusNotFound, "artifact_not_found", "the result is no longer kept")
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return failure(http.StatusNotFound, "artifact_not_found", "the result is no longer kept")
		}
		writer.Header().Set("Content-Type", mediaType)
		http.ServeContent(writer, request, "", info.ModTime(), file)
		return nil
	}
	address, err := url.Parse(task.ResultURL)
	if err != nil || (address.Scheme != "http" && address.Scheme != "https") {
		return failure(http.StatusNotFound, "artifact_not_found", "the task has no result to download")
	}
	call := &exchange{engine: engine, target: target, service: service, meta: meta}
	response, err := call.download(ctx, "result", method, address, &descriptor{}, false, client)
	if err != nil {
		return err
	}
	return relayContent(writer, response, artifact)
}

// relayContent passes a download back to the client: its status, the
// headers of the content, and the bytes.
func relayContent(writer http.ResponseWriter, response *http.Response, artifact *Artifact) error {
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusNotModified, response.StatusCode == http.StatusRequestedRangeNotSatisfiable:
	case response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone:
		return failure(http.StatusNotFound, "artifact_not_found", "the vendor no longer has the artifact")
	case response.StatusCode < 200 || response.StatusCode > 299:
		body, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorAnswer))
		message := upstreamMessage(answerValue(body))
		if message == "" {
			message = http.StatusText(response.StatusCode)
		}
		return &Error{Status: http.StatusBadGateway, Code: "upstream_error", Message: fmt.Sprintf("the download failed: %s", message)}
	}
	for _, name := range contentAnswerHeaders {
		if value := response.Header.Get(name); value != "" {
			writer.Header().Set(name, value)
		}
	}
	if writer.Header().Get("Content-Type") == "" && artifact.MimeType != "" {
		writer.Header().Set("Content-Type", artifact.MimeType)
	}
	writer.WriteHeader(response.StatusCode)
	if _, err := io.Copy(writer, response.Body); err != nil && !errors.Is(err, context.Canceled) {
		return nil
	}
	return nil
}
