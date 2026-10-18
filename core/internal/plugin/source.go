package plugin

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Calcium-Ion/moejs"
	"github.com/Calcium-Ion/moejs/syntax"
)

// MaxSourceBytes bounds a plugin's source. Compiling takes about 85 ms and
// 30 MiB of temporary memory per MiB of source, once per load; a plugin made
// of several files is bundled into one first.
const MaxSourceBytes = 8 << 20

// Codes of LoadError, stable for the desktop to word.
const (
	CodeNotFound      = "not_found"
	CodeAmbiguous     = "ambiguous"
	CodeTooLarge      = "too_large"
	CodeEncoding      = "encoding"
	CodeSyntax        = "syntax"
	CodeAsync         = "async"
	CodeAwait         = "await"
	CodeImport        = "import"
	CodeReexport      = "reexport"
	CodeMissingMeta   = "missing_meta"
	CodeInvalidMeta   = "invalid_meta"
	CodeUnknownExport = "unknown_export"
	CodeNotFunction   = "not_function"
	CodeLoadFailed    = "load_failed"
)

// LoadError explains why a plugin did not load. Line and Column, when set,
// point into File; Message is safe to show.
type LoadError struct {
	Code    string
	File    string
	Line    int
	Column  int
	Message string
}

func (e *LoadError) Error() string {
	switch {
	case e.File != "" && e.Line > 0:
		return fmt.Sprintf("%s:%d:%d: %s", e.File, e.Line, e.Column, e.Message)
	case e.File != "":
		return e.File + ": " + e.Message
	}
	return e.Message
}

// Origin tells where a plugin's source comes from.
type Origin string

const (
	// OriginBuiltin is a plugin compiled into Core.
	OriginBuiltin Origin = "builtin"
	// OriginDirectory is a plugin in the plugin directory.
	OriginDirectory Origin = "directory"
	// OriginOverride is a plugin in the plugin directory that replaces the
	// builtin plugin of the same key.
	OriginOverride Origin = "override"
)

// Source is a plugin before it is compiled. Err is set instead of Text when
// the source could not be read.
type Source struct {
	Key    string
	Origin Origin
	// File is the source's name in errors and stack traces: plugin.ts or
	// plugin.js.
	File string
	Text string
	Err  *LoadError
}

// sourceFiles are the names a plugin's source may have, TypeScript first.
var sourceFiles = []string{"plugin.ts", "plugin.js"}

// Discover lists the plugins of builtin and of dir, each as
// <key>/plugin.ts or <key>/plugin.js, sorted by key. A directory plugin
// replaces the builtin plugin of its key. builtin and dir may each be
// missing; a dir that does not exist holds no plugins.
func Discover(builtin fs.FS, dir string) ([]Source, error) {
	byKey := make(map[string]Source)
	if builtin != nil {
		sources, err := discoverFS(builtin, OriginBuiltin)
		if err != nil {
			return nil, fmt.Errorf("read builtin plugins: %w", err)
		}
		for _, source := range sources {
			byKey[source.Key] = source
		}
	}
	if dir != "" {
		sources, err := discoverFS(os.DirFS(dir), OriginDirectory)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("read plugin directory: %w", err)
		}
		for _, source := range sources {
			if _, builtin := byKey[source.Key]; builtin {
				source.Origin = OriginOverride
			}
			byKey[source.Key] = source
		}
	}
	sources := make([]Source, 0, len(byKey))
	for _, source := range byKey {
		sources = append(sources, source)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Key < sources[j].Key })
	return sources, nil
}

func discoverFS(fsys fs.FS, origin Origin) ([]Source, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	var sources []Source
	for _, entry := range entries {
		if !ValidPluginKey(entry.Name()) {
			continue
		}
		if !entry.IsDir() {
			// A link to a directory, such as a plugin checked out elsewhere,
			// counts as one.
			info, err := fs.Stat(fsys, entry.Name())
			if err != nil || !info.IsDir() {
				continue
			}
		}
		sources = append(sources, readSource(fsys, entry.Name(), origin))
	}
	return sources, nil
}

func readSource(fsys fs.FS, key string, origin Origin) Source {
	source := Source{Key: key, Origin: origin}
	var found []string
	for _, name := range sourceFiles {
		if info, err := fs.Stat(fsys, path.Join(key, name)); err == nil && info.Mode().IsRegular() {
			found = append(found, name)
		}
	}
	switch len(found) {
	case 0:
		source.Err = &LoadError{Code: CodeNotFound, Message: "the plugin directory has no plugin.ts or plugin.js"}
		return source
	case 2:
		source.Err = &LoadError{Code: CodeAmbiguous, Message: "the plugin directory has both plugin.ts and plugin.js"}
		return source
	}
	source.File = found[0]
	file, err := fsys.Open(path.Join(key, source.File))
	if err != nil {
		source.Err = &LoadError{Code: CodeNotFound, File: source.File, Message: "cannot read the source"}
		return source
	}
	defer file.Close()
	text, err := io.ReadAll(io.LimitReader(file, MaxSourceBytes+1))
	if err != nil {
		source.Err = &LoadError{Code: CodeNotFound, File: source.File, Message: "cannot read the source"}
		return source
	}
	source.Text = string(text)
	return source
}

// Path returns where a directory plugin's source lives under dir.
func (source Source) Path(dir string) string {
	if source.Origin == OriginBuiltin || source.File == "" {
		return ""
	}
	return filepath.Join(dir, source.Key, source.File)
}

// compile checks a plugin's source against what the host runs and compiles
// it once, for every runtime of its pool to load.
func compile(file, text string) (*moejs.Module, error) {
	if len(text) > MaxSourceBytes {
		return nil, &LoadError{Code: CodeTooLarge, File: file, Message: fmt.Sprintf("the source is larger than %d MiB", MaxSourceBytes>>20)}
	}
	if !utf8.ValidString(text) {
		return nil, &LoadError{Code: CodeEncoding, File: file, Message: "the source is not valid UTF-8"}
	}
	typescript := strings.HasSuffix(file, ".ts")
	parsed, err := syntax.ParseModule(file, text, syntax.Options{TypeScript: typescript})
	if err != nil {
		return nil, syntaxLoadError(file, err)
	}
	if err := checkSynchronous(file, parsed); err != nil {
		return nil, err
	}
	var module *moejs.Module
	if typescript {
		module, err = moejs.CompileTS(file, text)
	} else {
		module, err = moejs.Compile(file, text)
	}
	if err != nil {
		return nil, syntaxLoadError(file, err)
	}
	if requests := module.Requests(); len(requests) > 0 {
		return nil, &LoadError{Code: CodeImport, File: file, Message: fmt.Sprintf("imports %q; bundle the plugin into one file", requests[0])}
	}
	return module, nil
}

// checkSynchronous refuses what a synchronous, single-file plugin cannot
// use: async functions and await, which would need an event loop the host
// does not run, and imports, which have nothing to resolve to. The parse
// tree tells code from comments, strings and regular expressions.
func checkSynchronous(file string, parsed *syntax.Module) error {
	var found *LoadError
	at := func(node syntax.Node, code, message string) {
		pos, _ := node.Range()
		line, column := parsed.File.Position(pos)
		found = &LoadError{Code: code, File: file, Line: line, Column: column, Message: message}
	}
	syntax.Inspect(parsed, func(node syntax.Node) bool {
		if found != nil {
			return false
		}
		switch node := node.(type) {
		case *syntax.Function:
			if node.IsAsync {
				at(node, CodeAsync, "async functions are not supported; plugins are synchronous and http.fetch takes an array to run requests in parallel")
			}
		case *syntax.AwaitExpr:
			at(node, CodeAwait, "await is not supported; plugins are synchronous")
		case *syntax.ForInOfStmt:
			if node.Await {
				at(node, CodeAwait, "for await is not supported; plugins are synchronous")
			}
		case *syntax.ImportDecl, *syntax.ImportCall, *syntax.ImportMeta:
			at(node, CodeImport, "imports are not supported; bundle the plugin into one file")
		case *syntax.ExportNamed:
			if node.Source != nil {
				at(node, CodeReexport, "export … from is not supported; bundle the plugin into one file")
			}
		case *syntax.ExportAll:
			at(node, CodeReexport, "export * from is not supported; bundle the plugin into one file")
		}
		return found == nil
	})
	if found != nil {
		return found
	}
	return nil
}

func syntaxLoadError(file string, err error) error {
	var parse *syntax.Error
	if errors.As(err, &parse) {
		return &LoadError{Code: CodeSyntax, File: file, Line: parse.Line, Column: parse.Col, Message: cleanText(strings.TrimPrefix(parse.Msg, "SyntaxError: "), maxMessageRunes, "")}
	}
	var compiled *moejs.SyntaxError
	if errors.As(err, &compiled) {
		return &LoadError{Code: CodeSyntax, File: file, Line: compiled.Line, Column: compiled.Column, Message: cleanText(compiled.Message, maxMessageRunes, "")}
	}
	return &LoadError{Code: CodeSyntax, File: file, Message: cleanText(err.Error(), maxMessageRunes, "")}
}
