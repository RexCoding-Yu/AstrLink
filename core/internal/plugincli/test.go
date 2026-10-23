package plugincli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing/fstest"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/plugin"
)

// fixture is one case of a project's tests directory: a hook to call with
// arguments, the settings and stored values it runs with, the responses its
// requests get, and what it must return.
type fixture struct {
	Name string            `json:"name"`
	Hook string            `json:"hook"`
	Args []json.RawMessage `json:"args"`
	// Config is the plugin's configuration; Capabilities are those granted,
	// all the plugin asks for when absent.
	Config       json.RawMessage `json:"config"`
	Capabilities []string        `json:"capabilities"`
	// Request is what astrlink.request.info returns.
	Request *plugin.RequestInfo        `json:"request"`
	Store   map[string]json.RawMessage `json:"store"`
	Fetch   []fakeResponse             `json:"fetch"`
	Expect  expectation                `json:"expect"`
	file    string
}

// fakeResponse answers the requests whose method and address match: the
// address exactly, or as a prefix when it ends with *.
type fakeResponse struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	JSON    json.RawMessage   `json:"json"`
	Text    *string           `json:"text"`
	// Error fails the request as the host would: denied, timeout, network.
	Error string `json:"error"`
	// DelayMs holds the response back.
	DelayMs int `json:"delayMs"`
}

// expectation is what a case checks: the result in full or in part, or the
// failure, and the stored values after the call.
type expectation struct {
	Result json.RawMessage            `json:"result"`
	Match  json.RawMessage            `json:"match"`
	Error  string                     `json:"error"`
	Store  map[string]json.RawMessage `json:"store"`
}

type caseOutcome struct {
	Name    string `json:"name"`
	File    string `json:"file"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

func runTest(ctx context.Context, env Environment, args []string) int {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	only := flags.String("run", "", "run only the cases whose name contains this text")
	positional, ok := parse(env, "test", flags, args, 1)
	if !ok {
		return 2
	}
	dir := projectDir(positional)
	fixtures, err := readFixtures(filepath.Join(dir, "tests"))
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	if *only != "" {
		kept := fixtures[:0]
		for _, each := range fixtures {
			if strings.Contains(each.Name, *only) {
				kept = append(kept, each)
			}
		}
		fixtures = kept
	}
	if len(fixtures) == 0 {
		fmt.Fprintln(env.Stderr, "error: no test cases in tests/*.json")
		return 1
	}
	project, ok := buildProject(env, dir)
	if !ok {
		return 1
	}
	runner := newRunner(project)
	outcomes := make([]caseOutcome, 0, len(fixtures))
	failed := 0
	for _, each := range fixtures {
		outcome := runner.run(ctx, each)
		outcomes = append(outcomes, outcome)
		status := "PASS"
		if !outcome.OK {
			status = "FAIL"
			failed++
		}
		fmt.Fprintf(env.Stderr, "%s  %s", status, outcome.Name)
		if outcome.Message != "" {
			fmt.Fprintf(env.Stderr, "\n      %s", strings.ReplaceAll(outcome.Message, "\n", "\n      "))
		}
		fmt.Fprintln(env.Stderr)
	}
	printJSON(env.Stdout, map[string]any{"passed": len(outcomes) - failed, "failed": failed, "cases": outcomes})
	if failed > 0 {
		return 1
	}
	return 0
}

// readFixtures reads every case of the *.json files in dir, each file one
// case or an array of them.
func readFixtures(dir string) ([]fixture, error) {
	names, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var fixtures []fixture
	for _, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		data = bytes.TrimSpace(data)
		var cases []fixture
		if bytes.HasPrefix(data, []byte("[")) {
			err = json.Unmarshal(data, &cases)
		} else {
			var single fixture
			err = json.Unmarshal(data, &single)
			cases = []fixture{single}
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %v", filepath.Base(name), err)
		}
		for index := range cases {
			cases[index].file = filepath.Base(name)
			if cases[index].Name == "" {
				cases[index].Name = fmt.Sprintf("%s #%d", filepath.Base(name), index+1)
			}
			if cases[index].Hook == "" {
				return nil, fmt.Errorf("%s: case %q names no hook", filepath.Base(name), cases[index].Name)
			}
		}
		fixtures = append(fixtures, cases...)
	}
	return fixtures, nil
}

// runner runs cases against one build of the plugin.
type runner struct {
	key     string
	manager *plugin.Manager
	// settings and the services are the current case's.
	mutex      sync.Mutex
	settings   plugin.Settings
	responses  []fakeResponse
	unexpected []string
	store      *memoryStore
}

func newRunner(project *built) *runner {
	current := &runner{key: project.checked.Meta.Key}
	files := fstest.MapFS{current.key + "/plugin.js": &fstest.MapFile{Data: project.result.Plugin}}
	if project.result.SourceMap != nil {
		files[current.key+"/plugin.js.map"] = &fstest.MapFile{Data: project.result.SourceMap}
	}
	current.manager = plugin.NewManager(plugin.ManagerOptions{
		Builtin:  files,
		Settings: settingsFunc(func() map[string]plugin.Settings { return map[string]plugin.Settings{current.key: current.settings} }),
		Services: plugin.Services{Fetcher: current, Store: storeFunc(func() *memoryStore { return current.store })},
	})
	return current
}

func (current *runner) run(ctx context.Context, each fixture) caseOutcome {
	outcome := caseOutcome{Name: each.Name, File: each.file}
	current.mutex.Lock()
	current.responses = each.Fetch
	current.unexpected = nil
	current.store = &memoryStore{values: map[string]json.RawMessage{}}
	for key, value := range each.Store {
		current.store.values[key] = value
	}
	current.mutex.Unlock()
	if err := current.manager.Reload(ctx); err != nil {
		outcome.Message = err.Error()
		return outcome
	}
	loaded := current.manager.Current().Plugin(current.key)
	if loaded == nil || !loaded.Loaded() {
		outcome.Message = "the plugin did not load"
		return outcome
	}
	capabilities := each.Capabilities
	if capabilities == nil {
		capabilities = loaded.Meta.Capabilities
	}
	current.settings = plugin.Settings{Enabled: true, Capabilities: capabilities, Config: each.Config}
	if err := current.manager.Refresh(ctx); err != nil {
		outcome.Message = err.Error()
		return outcome
	}
	loaded = current.manager.Current().Plugin(current.key)
	args := make([]any, 0, len(each.Args))
	for _, arg := range each.Args {
		args = append(args, arg)
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, info, err := loaded.CallArgs(callCtx, each.Hook, args, plugin.CallOptions{Request: each.Request, NetworkBudget: 20 * time.Second})
	var problems []string
	current.mutex.Lock()
	problems = append(problems, current.unexpected...)
	current.mutex.Unlock()
	var failure *plugin.CallError
	switch {
	case each.Expect.Error != "":
		if !errors.As(err, &failure) {
			problems = append(problems, fmt.Sprintf("returned %s, want a failure mentioning %q", orUndefined(result), each.Expect.Error))
		} else if !strings.Contains(failure.Message, each.Expect.Error) && failure.Reason != each.Expect.Error {
			problems = append(problems, fmt.Sprintf("failed with %s: %s, want a failure mentioning %q", failure.Reason, failure.Message, each.Expect.Error))
		}
	case err != nil:
		message := err.Error()
		if errors.As(err, &failure) {
			message = failure.Reason + ": " + failure.Message
			if stack := loaded.Stack(failure); stack != "" {
				message += "\n" + stack
			}
		}
		problems = append(problems, "failed: "+message)
	case len(each.Expect.Result) > 0:
		if !jsonEqual(result, each.Expect.Result) {
			problems = append(problems, fmt.Sprintf("returned %s\nwant     %s", orUndefined(result), compact(each.Expect.Result)))
		}
	case len(each.Expect.Match) > 0:
		if !jsonMatches(result, each.Expect.Match) {
			problems = append(problems, fmt.Sprintf("returned %s\nwant it to contain %s", orUndefined(result), compact(each.Expect.Match)))
		}
	}
	for key, want := range each.Expect.Store {
		got, found := current.store.values[key]
		if !found || !jsonEqual(got, want) {
			problems = append(problems, fmt.Sprintf("stored %s = %s, want %s", key, orUndefined(got), compact(want)))
		}
	}
	if len(problems) > 0 && len(info.Logs) > 0 {
		lines := []string{"logs:"}
		for _, line := range info.Logs {
			lines = append(lines, "  "+line.Level+": "+line.Message)
		}
		problems = append(problems, strings.Join(lines, "\n"))
	}
	outcome.OK = len(problems) == 0
	outcome.Message = strings.Join(problems, "\n")
	return outcome
}

// Fetch answers a request with the first fake response that matches it.
func (current *runner) Fetch(ctx context.Context, _ *plugin.Plugin, request plugin.HTTPRequest) plugin.HTTPResponse {
	current.mutex.Lock()
	responses := current.responses
	current.mutex.Unlock()
	for _, response := range responses {
		if response.Method != "" && !strings.EqualFold(response.Method, request.Method) {
			continue
		}
		prefix, wildcard := strings.CutSuffix(response.URL, "*")
		if (!wildcard && response.URL != request.URL) || (wildcard && !strings.HasPrefix(request.URL, prefix)) {
			continue
		}
		if response.DelayMs > 0 {
			select {
			case <-time.After(time.Duration(response.DelayMs) * time.Millisecond):
			case <-ctx.Done():
				return plugin.HTTPResponse{Error: plugin.FetchTimeout}
			}
		}
		if response.Error != "" {
			return plugin.HTTPResponse{Status: response.Status, Error: response.Error}
		}
		status := response.Status
		if status == 0 {
			status = 200
		}
		header := map[string]string{}
		for name, value := range response.Headers {
			header[strings.ToLower(name)] = value
		}
		var body []byte
		switch {
		case len(response.JSON) > 0:
			body = response.JSON
			if header["content-type"] == "" {
				header["content-type"] = "application/json"
			}
		case response.Text != nil:
			body = []byte(*response.Text)
		}
		return plugin.HTTPResponse{Status: status, Header: header, Body: body}
	}
	current.mutex.Lock()
	current.unexpected = append(current.unexpected, fmt.Sprintf("made a request no fixture answers: %s %s", request.Method, request.URL))
	current.mutex.Unlock()
	return plugin.HTTPResponse{Error: plugin.FetchNetwork}
}

func orUndefined(value json.RawMessage) string {
	if len(value) == 0 {
		return "undefined"
	}
	return compact(value)
}

func compact(value json.RawMessage) string {
	var buffer bytes.Buffer
	if json.Compact(&buffer, value) != nil {
		return string(value)
	}
	return buffer.String()
}

func jsonEqual(a, b json.RawMessage) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}

// jsonMatches reports whether every member of want appears in got with the
// same value: objects may hold more members, arrays match item by item.
func jsonMatches(got, want json.RawMessage) bool {
	var left, right any
	if json.Unmarshal(got, &left) != nil || json.Unmarshal(want, &right) != nil {
		return false
	}
	return subset(left, right)
}

func subset(got, want any) bool {
	switch wanted := want.(type) {
	case map[string]any:
		object, ok := got.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range wanted {
			if !subset(object[key], value) {
				return false
			}
		}
		return true
	case []any:
		array, ok := got.([]any)
		if !ok || len(array) != len(wanted) {
			return false
		}
		for index := range wanted {
			if !subset(array[index], wanted[index]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(got, want)
}

type settingsFunc func() map[string]plugin.Settings

func (read settingsFunc) PluginSettings(context.Context) (map[string]plugin.Settings, error) {
	return read(), nil
}

// memoryStore is astrlink.store for a case.
type memoryStore struct {
	mutex  sync.Mutex
	values map[string]json.RawMessage
}

type storeFunc func() *memoryStore

func (current storeFunc) Get(_ context.Context, _, key string) (json.RawMessage, bool, error) {
	store := current()
	store.mutex.Lock()
	defer store.mutex.Unlock()
	value, found := store.values[key]
	return value, found, nil
}

func (current storeFunc) Set(_ context.Context, _, key string, value json.RawMessage) error {
	store := current()
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.values[key] = value
	return nil
}

func (current storeFunc) Delete(_ context.Context, _, key string) error {
	store := current()
	store.mutex.Lock()
	defer store.mutex.Unlock()
	delete(store.values, key)
	return nil
}

func (current storeFunc) Keys(_ context.Context, _, prefix string) ([]string, error) {
	store := current()
	store.mutex.Lock()
	defer store.mutex.Unlock()
	var keys []string
	for key := range store.values {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}
