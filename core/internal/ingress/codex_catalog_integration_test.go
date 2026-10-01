package ingress

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Opt in with ASTRLINK_TEST_CODEX_BIN=/absolute/path/to/codex. This checks the
// real client's catalog parser and picker over HTTP, without inference or any
// real credentials, user configuration, or external provider requests.
func TestCodexCatalogInstalledClient(t *testing.T) {
	binary := os.Getenv("ASTRLINK_TEST_CODEX_BIN")
	if binary == "" {
		t.Skip("ASTRLINK_TEST_CODEX_BIN is not set")
	}
	handler := codexCatalogTestHandler(t)
	var catalogRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("client_version") == "" {
			t.Error("Codex did not send its model catalog version")
		}
		catalogRequests.Add(1)
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	configDir := t.TempDir()
	config := fmt.Sprintf(`model_provider = "catalog-test"
chatgpt_base_url = %q
openai_base_url = %q
[model_providers.catalog-test]
name = "Local catalog test"
base_url = %q
wire_api = "responses"
requires_openai_auth = true
[analytics]
enabled = false
`, server.URL+"/backend-api", server.URL+"/v1", server.URL+"/v1")
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	claims, err := json.Marshal(map[string]any{
		"exp": time.Now().Add(time.Hour).Unix(),
		"https://api.openai.com/auth": map[string]string{
			"chatgpt_account_id": "local-catalog-test", "chatgpt_plan_type": "plus",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	jwt := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + base64.RawURLEncoding.EncodeToString(claims) + ".fake"
	auth, err := json.Marshal(map[string]any{
		"auth_mode": "chatgpt", "last_refresh": time.Now().UTC().Format(time.RFC3339),
		"tokens": map[string]string{
			"id_token": jwt, "access_token": jwt, "refresh_token": "fake-local-token", "account_id": "local-catalog-test",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "auth.json"), auth, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "app-server", "--stdio")
	command.Dir = configDir
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "CODEX_") && !strings.HasPrefix(variable, "OPENAI_") && !strings.HasPrefix(variable, "CHATGPT_") {
			command.Env = append(command.Env, variable)
		}
	}
	command.Env = append(command.Env, "CODEX_HOME="+configDir)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		_ = command.Wait()
		if t.Failed() {
			t.Logf("Codex stderr: %s", stderr.String())
		}
	}()
	decoder := json.NewDecoder(stdout)
	read := func(id int) json.RawMessage {
		t.Helper()
		for {
			var response struct {
				ID     *int            `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if err := decoder.Decode(&response); err != nil {
				t.Fatal(err)
			}
			if response.ID != nil && *response.ID == id {
				if len(response.Error) != 0 {
					t.Fatalf("Codex RPC error: %s", response.Error)
				}
				return response.Result
			}
		}
	}
	if _, err := fmt.Fprintln(stdin, `{"id":1,"method":"initialize","params":{"clientInfo":{"name":"catalog_test","version":"1.0"}}}`); err != nil {
		t.Fatal(err)
	}
	read(1)
	if _, err := fmt.Fprintln(stdin, "{\"method\":\"initialized\"}\n{\"id\":2,\"method\":\"model/list\",\"params\":{}}"); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Data []struct {
			Model string `json:"model"`
		} `json:"data"`
	}
	if err := json.Unmarshal(read(2), &result); err != nil {
		t.Fatal(err)
	}
	var models []string
	for _, model := range result.Data {
		models = append(models, model.Model)
	}
	slices.Sort(models)
	want := []string{"coding-alias", "gpt-model", "kimi-alias", "kimi-for-coding", "manual-model"}
	if !slices.Equal(models, want) || catalogRequests.Load() == 0 {
		t.Fatalf("Codex picker models = %v, want %v; HTTP catalog requests = %d", models, want, catalogRequests.Load())
	}
	t.Logf("Codex accepted gateway catalog over HTTP: %v", models)
}
