package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Calcium-Ion/moejs"
)

// loadTime bounds a module's top level, and the reads of its manifest and
// hooks, in a new runtime. An 8 MiB plugin loads in about 100 ms. Tests
// shorten it.
var loadTime = 2 * time.Second

var errLoadTime = errors.New("plugin load time limit exceeded")

// program is a compiled plugin that loaded and passed its checks: the
// module, the manifest and the hooks it exports. It is immutable, and every
// generation that runs the same source shares it.
type program struct {
	key    string
	file   string
	module *moejs.Module
	meta   Meta
	hooks  map[string]moejs.Hook
}

// load compiles source and loads it once in a runtime of its own, to check
// that its top level runs, that it exports a valid manifest, and that every
// other export is a hook the host calls.
func load(source Source, limits Limits) (*program, error) {
	if source.Err != nil {
		return nil, source.Err
	}
	module, err := compile(source.File, source.Text)
	if err != nil {
		return nil, err
	}
	loaded := &program{key: source.Key, file: source.File, module: module, hooks: make(map[string]moejs.Hook)}
	instance, err := newInstance(loaded, limits)
	if err != nil {
		return nil, loadFailure(source.File, err)
	}
	stop := interruptAfter(instance.runtime, loadTime, errLoadTime)
	defer stop()
	meta, err := readMeta(instance.runtime, source.File)
	if err != nil {
		return nil, err
	}
	if err := validateMeta(meta, source.Key); err != nil {
		err.(*LoadError).File = source.File
		return nil, err
	}
	loaded.meta = meta
	for _, name := range module.Exports() {
		if name == "meta" {
			continue
		}
		if !slices.Contains(knownHooks, name) {
			return nil, &LoadError{Code: CodeUnknownExport, File: source.File, Message: fmt.Sprintf("exports %q, which is neither meta nor a hook the host calls (%s)", name, strings.Join(knownHooks, ", "))}
		}
		hook, err := module.Hook(name)
		if err != nil {
			return nil, &LoadError{Code: CodeNotFunction, File: source.File, Message: fmt.Sprintf("the export %q is not a function", name)}
		}
		if callable, err := instance.runtime.Has(hook); err != nil || !callable {
			return nil, &LoadError{Code: CodeNotFunction, File: source.File, Message: fmt.Sprintf("the export %q is not a function", name)}
		}
		loaded.hooks[name] = hook
	}
	return loaded, nil
}

// readMeta reads the manifest as strict JSON, so that a misspelt field is an
// error rather than a capability that silently stays off.
func readMeta(runtime *moejs.Runtime, file string) (Meta, error) {
	value, ok := runtime.Export("meta")
	if !ok {
		return Meta{}, &LoadError{Code: CodeMissingMeta, File: file, Message: "the plugin does not export meta"}
	}
	var raw json.RawMessage
	if err := runtime.UnmarshalWith(value, &raw, moejs.DecodeOptions{MaxBytes: 64 << 10, MaxNodes: 4096}); err != nil || len(raw) == 0 {
		return Meta{}, &LoadError{Code: CodeInvalidMeta, File: file, Message: "meta must be plain data of at most 64 KiB"}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var meta Meta
	if err := decoder.Decode(&meta); err != nil {
		return Meta{}, &LoadError{Code: CodeInvalidMeta, File: file, Message: cleanText("meta: "+strings.TrimPrefix(err.Error(), "json: "), maxMessageRunes, "")}
	}
	return meta, nil
}

// loadFailure explains a top level that did not finish.
func loadFailure(file string, err error) error {
	failure := &LoadError{Code: CodeLoadFailed, File: file}
	var exception *moejs.Exception
	switch {
	case errors.Is(err, errLoadTime):
		failure.Message = fmt.Sprintf("the top level ran longer than %s", loadTime)
	case errors.Is(err, moejs.ErrMemoryLimit):
		failure.Message = "the top level used more memory than one call may"
	case errors.As(err, &exception):
		failure.Line, failure.Column = stackPosition(stackOf(err), file)
		failure.Message = cleanText("the top level threw "+exceptionText(exception), maxMessageRunes, "")
	default:
		failure.Message = cleanText(err.Error(), maxMessageRunes, "")
	}
	return failure
}

// instance is one runtime that loaded a program, with the call its host
// functions serve while a hook runs.
type instance struct {
	runtime *moejs.Runtime
	current *call
}

func newInstance(loaded *program, limits Limits) (*instance, error) {
	runtime := moejs.NewRuntime(moejs.Options{
		MemoryLimit:        limits.Memory,
		DisableDynamicCode: true,
		TimeZone:           time.UTC,
	})
	created := &instance{runtime: runtime}
	if err := created.install(); err != nil {
		return nil, err
	}
	stop := interruptAfter(runtime, loadTime, errLoadTime)
	err := runtime.Load(loaded.module)
	stop()
	if err != nil {
		return nil, withStack(runtime, err)
	}
	runtime.ClearInterrupt()
	return created, nil
}

// thrown is an exception with the stack trace that only the runtime it was
// thrown in can render.
type thrown struct {
	err   error
	stack string
}

func (failure *thrown) Error() string { return failure.err.Error() }

func (failure *thrown) Unwrap() error { return failure.err }

func withStack(runtime *moejs.Runtime, err error) error {
	var exception *moejs.Exception
	if !errors.As(err, &exception) {
		return err
	}
	return &thrown{err: err, stack: runtime.StackTrace(exception)}
}

func stackOf(err error) string {
	var failure *thrown
	if errors.As(err, &failure) {
		return failure.stack
	}
	return ""
}

// interruptAfter interrupts runtime with cause after d. Once the returned
// function returns, no interrupt from it is pending or still to come; one
// that came stays pending for the caller to clear.
func interruptAfter(runtime *moejs.Runtime, d time.Duration, cause error) func() {
	var mutex sync.Mutex
	stopped := false
	timer := time.AfterFunc(d, func() {
		mutex.Lock()
		defer mutex.Unlock()
		if !stopped {
			runtime.Interrupt(cause)
		}
	})
	return func() {
		mutex.Lock()
		stopped = true
		mutex.Unlock()
		timer.Stop()
	}
}

// pool keeps the runtimes that loaded one program for one generation of its
// settings. Runtimes never move to another pool, so nothing a plugin keeps
// in module state outlives a change of its settings.
type pool struct {
	program *program
	limits  Limits
	idle    chan *instance
	slots   chan struct{}
}

func newPool(loaded *program, limits Limits) *pool {
	return &pool{
		program: loaded,
		limits:  limits,
		idle:    make(chan *instance, limits.Concurrency),
		slots:   make(chan struct{}, limits.Concurrency),
	}
}

func (pool *pool) acquire(ctx context.Context) error {
	select {
	case pool.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (pool *pool) release() { <-pool.slots }

func (pool *pool) take() (*instance, error) {
	select {
	case taken := <-pool.idle:
		return taken, nil
	default:
	}
	return newInstance(pool.program, pool.limits)
}

func (pool *pool) put(returned *instance) {
	select {
	case pool.idle <- returned:
	default:
	}
}

// run calls hook on a runtime of the pool, with c bound for its host
// functions, and returns the JSON text of the result, nil for undefined. A
// runtime whose call failed is dropped: the failure may have left its
// module state half updated.
func (pool *pool) run(ctx context.Context, c *call, hook moejs.Hook, arg any) (json.RawMessage, error) {
	if err := pool.acquire(ctx); err != nil {
		return nil, err
	}
	c.slot = true
	defer func() {
		if c.slot {
			pool.release()
		}
	}()
	taken, err := pool.take()
	if err != nil {
		return nil, err
	}
	runtime := taken.runtime
	c.watchdog = &watchdog{runtime: runtime, budget: pool.limits.ScriptTime}
	taken.current = c
	interrupted := make(chan struct{})
	stopContext := context.AfterFunc(ctx, func() {
		runtime.Interrupt(context.Cause(ctx))
		close(interrupted)
	})
	c.watchdog.start()
	result, err := invoke(runtime, hook, arg, pool.limits)
	c.watchdog.pause()
	if !stopContext() {
		<-interrupted
	}
	taken.current = nil
	if err != nil {
		return nil, withStack(runtime, err)
	}
	runtime.ClearInterrupt()
	runtime.ReleaseCallData()
	pool.put(taken)
	return result, nil
}

func invoke(runtime *moejs.Runtime, hook moejs.Hook, arg any, limits Limits) (json.RawMessage, error) {
	var args []moejs.Value
	if arg != nil {
		value, err := runtime.FromGo(arg)
		if err != nil {
			return nil, fmt.Errorf("pass the hook's argument: %w", err)
		}
		args = append(args, value)
	}
	value, err := runtime.Call(hook, args...)
	if err != nil {
		return nil, err
	}
	if value.IsUndefined() {
		return nil, nil
	}
	var result json.RawMessage
	if err := runtime.UnmarshalWith(value, &result, moejs.DecodeOptions{MaxBytes: limits.ResultBytes, MaxNodes: limits.ResultNodes}); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		// A function or symbol has no JSON text: no result.
		return nil, nil
	}
	return result, nil
}

// watchdog interrupts a call whose JavaScript runs longer than budget.
// Waits on the host pause it.
type watchdog struct {
	runtime *moejs.Runtime
	budget  time.Duration
	mutex   sync.Mutex
	spent   time.Duration
	since   time.Time // zero while paused
	timer   *time.Timer
}

func (dog *watchdog) start() {
	dog.mutex.Lock()
	defer dog.mutex.Unlock()
	if !dog.since.IsZero() {
		return
	}
	dog.since = time.Now()
	left := dog.budget - dog.spent
	if left <= 0 {
		dog.runtime.Interrupt(errScriptTime)
		return
	}
	dog.timer = time.AfterFunc(left, dog.expire)
}

func (dog *watchdog) pause() {
	dog.mutex.Lock()
	defer dog.mutex.Unlock()
	if dog.since.IsZero() {
		return
	}
	dog.spent += time.Since(dog.since)
	dog.since = time.Time{}
	if dog.timer != nil {
		dog.timer.Stop()
		dog.timer = nil
	}
}

func (dog *watchdog) expire() {
	dog.mutex.Lock()
	defer dog.mutex.Unlock()
	// A timer that fired while a pause stopped it finds the dog paused, or
	// running again on a newer timer with time left.
	if dog.since.IsZero() || dog.spent+time.Since(dog.since) < dog.budget {
		return
	}
	dog.runtime.Interrupt(errScriptTime)
}

func (dog *watchdog) used() time.Duration {
	dog.mutex.Lock()
	defer dog.mutex.Unlock()
	if dog.since.IsZero() {
		return dog.spent
	}
	return dog.spent + time.Since(dog.since)
}

// classify turns what stopped a call into a CallError.
func classify(err error, limits Limits) *CallError {
	var existing *CallError
	if errors.As(err, &existing) {
		return existing
	}
	failure := &CallError{Reason: ReasonPluginError, Err: err}
	var exception *moejs.Exception
	var internal *moejs.InternalError
	var syntaxError *json.SyntaxError
	var typeError *json.UnmarshalTypeError
	switch {
	case errors.Is(err, moejs.ErrMemoryLimit):
		failure.Reason, failure.Message = ReasonMemory, fmt.Sprintf("the plugin allocated more than %d MiB in one call", limits.Memory>>20)
	case errors.Is(err, errScriptTime):
		failure.Reason, failure.Message = ReasonTimeout, fmt.Sprintf("the plugin ran JavaScript for longer than %s", limits.ScriptTime)
	case errors.Is(err, context.DeadlineExceeded):
		failure.Reason, failure.Message = ReasonTimeout, "the call ran out of time"
	case errors.Is(err, context.Canceled):
		failure.Reason, failure.Message = ReasonCanceled, "the request ended before the call"
	case errors.Is(err, moejs.ErrTooLarge):
		failure.Reason, failure.Message = ReasonInvalidResult, fmt.Sprintf("the result is larger than %d KiB or %d values", limits.ResultBytes>>10, limits.ResultNodes)
	case errors.As(err, &exception):
		failure.Message = cleanText("threw "+exceptionText(exception)+stackSuffix(stackOf(err)), maxMessageRunes, "")
	case errors.As(err, &internal):
		failure.Message = "the call failed inside the host"
	case errors.As(err, &syntaxError), errors.As(err, &typeError):
		failure.Reason, failure.Message = ReasonInvalidResult, "the result is not plain data"
	default:
		failure.Message = cleanText(err.Error(), maxMessageRunes, "")
	}
	return failure
}

func exceptionText(exception *moejs.Exception) string {
	name, message := exception.Name(), exception.Message()
	switch {
	case name == "":
		return message
	case message == "":
		return name
	}
	return name + ": " + message
}

// stackFrame matches a V8 stack line's location: "at f (plugin.ts:12:5)" or
// "at plugin.ts:12:5".
var stackFrame = regexp.MustCompile(`\(?([^\s()]+):(\d+):(\d+)\)?\s*$`)

// stackPosition finds the first frame of a stack trace that is in file.
func stackPosition(stack, file string) (line, column int) {
	for _, text := range strings.Split(stack, "\n") {
		match := stackFrame.FindStringSubmatch(strings.TrimSpace(text))
		if match == nil || match[1] != file {
			continue
		}
		line, _ = strconv.Atoi(match[2])
		column, _ = strconv.Atoi(match[3])
		return line, column
	}
	return 0, 0
}

func stackSuffix(stack string) string {
	for _, text := range strings.Split(stack, "\n") {
		text = strings.TrimSpace(text)
		if strings.HasPrefix(text, "at ") {
			return " (" + text + ")"
		}
	}
	return ""
}
