package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

const testMeta = `export const meta = { apiVersion: 1, key: "demo", name: "Demo", version: "1.0.0", capabilities: [] };
`

func TestCompileRejectsWhatSynchronousPluginsCannotUse(t *testing.T) {
	for _, test := range []struct {
		name, file, body, code string
		line                   int
	}{
		{"async function", "plugin.js", "\nexport async function prepareTurn() {}", CodeAsync, 3},
		{"async arrow", "plugin.js", "export const prepareTurn = async () => 1;", CodeAsync, 2},
		{"async method", "plugin.js", "class Recall { async run() {} }", CodeAsync, 2},
		{"async object method", "plugin.ts", "const recall = { async run(): Promise<void> {} };", CodeAsync, 2},
		{"top-level await", "plugin.js", "await 1;", CodeAwait, 2},
		{"for await", "plugin.js", "for await (const item of []) {}", CodeAwait, 2},
		{"import", "plugin.js", `import { helper } from "./helper.js";`, CodeImport, 2},
		{"dynamic import", "plugin.js", `export function prepareTurn() { return import("./later.js"); }`, CodeImport, 2},
		{"import.meta", "plugin.js", `export function prepareTurn() { return import.meta; }`, CodeImport, 2},
		{"export from", "plugin.js", `export { helper } from "./helper.js";`, CodeReexport, 2},
		{"export star", "plugin.js", `export * from "./helper.js";`, CodeReexport, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := compile(test.file, testMeta+test.body)
			var loadErr *LoadError
			if !errors.As(err, &loadErr) {
				t.Fatalf("compile error = %v, want a LoadError", err)
			}
			if loadErr.Code != test.code || loadErr.Line != test.line || loadErr.File != test.file {
				t.Fatalf("LoadError = %+v, want code %s at %s:%d", loadErr, test.code, test.file, test.line)
			}
		})
	}
}

func TestCompileTellsCodeFromTextThatMentionsIt(t *testing.T) {
	source := testMeta + `
// async functions, await and import are mentioned here only in a comment.
/* import x from "y"; await z; */
const words = ["async", "await", "import", ` + "`import ${\"x\"} async`" + `];
const pattern = /import|await|async/;
const asyncValue = 1, awaitCount = 2;
const table = { import: 1, await: 2, async: 3 };
export function prepareTurn() { return [words, pattern.test("import"), asyncValue + awaitCount, table.import]; }
`
	if _, err := compile("plugin.js", source); err != nil {
		t.Fatalf("compile: %v", err)
	}
}

func TestCompileAcceptsTypeScriptWithTypeOnlyImports(t *testing.T) {
	source := `import type { TurnView } from "astrlink";
interface Config { serverUrl: string; quotas?: Record<string, number> }
export const meta = { apiVersion: 1, key: "demo", name: "Demo", version: "1.0.0", capabilities: [] as string[] };
export function prepareTurn(turn: TurnView): { skip: string } {
  const config = astrlink.config.get() as Config;
  return { skip: config.serverUrl ?? "none" };
}
`
	if _, err := compile("plugin.ts", source); err != nil {
		t.Fatalf("compile: %v", err)
	}
}

func TestCompileReportsSyntaxErrorsWithPosition(t *testing.T) {
	_, err := compile("plugin.ts", testMeta+"export function prepareTurn() {\n  return {;\n}\n")
	var loadErr *LoadError
	if !errors.As(err, &loadErr) || loadErr.Code != CodeSyntax || loadErr.Line != 3 {
		t.Fatalf("compile error = %#v, want a syntax error on line 3", err)
	}
	if !strings.HasPrefix(loadErr.Error(), "plugin.ts:3:") {
		t.Fatalf("Error() = %q", loadErr.Error())
	}
}

func TestCompileRejectsTypeScriptWithRuntimeSemantics(t *testing.T) {
	_, err := compile("plugin.ts", testMeta+"enum Family { Memory }\n")
	var loadErr *LoadError
	if !errors.As(err, &loadErr) || loadErr.Code != CodeSyntax {
		t.Fatalf("compile error = %#v, want a syntax error for enum", err)
	}
}

func TestCompileBoundsTheSource(t *testing.T) {
	large := testMeta + "//" + strings.Repeat("x", MaxSourceBytes)
	var loadErr *LoadError
	if _, err := compile("plugin.js", large); !errors.As(err, &loadErr) || loadErr.Code != CodeTooLarge {
		t.Fatalf("compile of %d bytes = %v, want too_large", len(large), err)
	}
	if _, err := compile("plugin.js", testMeta+"const text = \"\xff\";"); !errors.As(err, &loadErr) || loadErr.Code != CodeEncoding {
		t.Fatalf("compile of invalid UTF-8 = %v, want encoding", err)
	}
}

func TestDiscoverLetsTheDirectoryOverrideBuiltins(t *testing.T) {
	builtin := fstest.MapFS{
		"alpha/plugin.ts":     {Data: []byte("builtin alpha")},
		"beta/plugin.ts":      {Data: []byte("builtin beta")},
		"Not-A-Key/plugin.ts": {Data: []byte("ignored")},
	}
	dir := t.TempDir()
	write := func(name, text string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("beta/plugin.js", "directory beta")
	write("gamma/plugin.ts", "directory gamma")
	write("gamma/plugin.js", "second gamma")
	write("delta/README.md", "no source")
	write("notes.txt", "not a plugin")
	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "plugin.js"), []byte("linked epsilon"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(dir, "epsilon")); err != nil {
		t.Fatal(err)
	}

	sources, err := Discover(builtin, dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	var got []string
	for _, source := range sources {
		entry := source.Key + " " + string(source.Origin) + " " + source.File + " " + source.Text
		if source.Err != nil {
			entry = source.Key + " " + string(source.Origin) + " error " + source.Err.Code
		}
		got = append(got, entry)
	}
	want := []string{
		"alpha builtin plugin.ts builtin alpha",
		"beta override plugin.js directory beta",
		"delta directory error not_found",
		"epsilon directory plugin.js linked epsilon",
		"gamma directory error ambiguous",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("Discover =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestDiscoverWithoutAPluginDirectory(t *testing.T) {
	sources, err := Discover(nil, filepath.Join(t.TempDir(), "missing"))
	if err != nil || len(sources) != 0 {
		t.Fatalf("Discover = %v, %v; want no plugins", sources, err)
	}
}
