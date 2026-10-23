package plugincli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	builtinplugins "github.com/QuantumNous/astrlink/core/plugins"

	"github.com/QuantumNous/astrlink/core/contract"
)

// template is a starting point for a project: its src/plugin.ts and the test
// cases that go with it.
type template struct {
	summary string
	plugin  string
	tests   string
	client  string
}

func runNew(_ context.Context, env Environment, args []string) int {
	flags := flag.NewFlagSet("new", flag.ContinueOnError)
	name := flags.String("template", "basic", "the template to start from: "+strings.Join(templateNames(), ", "))
	parent := flags.String("dir", ".", "the directory to create the project in")
	positional, ok := parse(env, "new", flags, args, 1)
	if !ok {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintf(env.Stderr, "astrlink plugin new: usage: %s\n", usageOf("new"))
		return 2
	}
	key := positional[0]
	if !contract.ValidPluginKey(key) {
		fmt.Fprintf(env.Stderr, "error: %q cannot be a plugin key: a lowercase letter, then up to 31 lowercase letters, digits and hyphens\n", key)
		return 2
	}
	chosen, found := templates()[*name]
	if !found {
		fmt.Fprintf(env.Stderr, "error: no template %q; choose one of %s\n", *name, strings.Join(templateNames(), ", "))
		return 2
	}
	dir := filepath.Join(*parent, key)
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		fmt.Fprintf(env.Stderr, "error: %s already exists and is not empty\n", dir)
		return 1
	}
	files := projectFiles(key, chosen)
	names := make([]string, 0, len(files))
	for file := range files {
		names = append(names, file)
	}
	sort.Strings(names)
	for _, file := range names {
		path := filepath.Join(dir, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fmt.Fprintf(env.Stderr, "error: %v\n", err)
			return 1
		}
		if err := os.WriteFile(path, []byte(files[file]), 0o644); err != nil {
			fmt.Fprintf(env.Stderr, "error: %v\n", err)
			return 1
		}
	}
	fmt.Fprintf(env.Stderr, "created %s from the %s template\nnext: cd %s && astrlink plugin test && astrlink plugin dev\n", dir, *name, dir)
	printJSON(env.Stdout, map[string]any{"key": key, "dir": dir, "template": *name, "files": names})
	return 0
}

func templateNames() []string {
	names := make([]string, 0, len(templates()))
	for name := range templates() {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// projectFiles are the files of a new project of key.
func projectFiles(key string, chosen template) map[string]string {
	replace := strings.NewReplacer("{{key}}", key, "{{name}}", titleOf(key))
	entries := `"plugin": "src/plugin.ts"`
	if chosen.client != "" {
		entries += `, "client": "src/client.tsx"`
	}
	files := map[string]string{
		"package.json": replace.Replace(`{
  "name": "astrlink-plugin-{{key}}",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "engines": { "astrlink": ">=0.1.0" },
  "astrlink": { ` + entries + ` }
}
`),
		"tsconfig.json": `{
  "compilerOptions": {
    "target": "ES2022",
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "lib": ["ES2022", "DOM"],
    "jsx": "react-jsx",
    "types": [],
    "strict": true,
    "noEmit": true,
    "skipLibCheck": true
  },
  "include": ["src", "astrlink-plugin.d.ts"]
}
`,
		".gitignore":           "node_modules/\ndist/\n",
		"astrlink-plugin.d.ts": builtinplugins.TypeDefinitions,
		"src/plugin.ts":        replace.Replace(chosen.plugin),
		"tests/plugin.json":    replace.Replace(chosen.tests),
		"README.md": replace.Replace("# {{name}}\n\nAn AstrLink plugin.\n\n" +
			"- `astrlink plugin test` runs the cases in `tests/`.\n" +
			"- `astrlink plugin dev` keeps the plugin built into the running AstrLink and shows its logs; enable it on the plugin page.\n" +
			"- `astrlink plugin check` checks it as AstrLink loads it.\n\n" +
			"Install it elsewhere by pasting the GitHub address of this repository on the plugin page. AstrLink builds it from source; commit a lock file when you add dependencies.\n"),
	}
	if chosen.client != "" {
		files["src/client.tsx"] = replace.Replace(chosen.client)
	}
	return files
}

// titleOf makes a display name of a key: memory-notes becomes Memory Notes.
func titleOf(key string) string {
	words := strings.Split(key, "-")
	for index, word := range words {
		if word != "" {
			words[index] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}

func templates() map[string]template {
	return map[string]template{
		"basic": {
			summary: "A plugin that keeps a count of the turns it saw and reports it on its page.",
			plugin: `// {{name}}: counts the turns of the conversations it sees.

export const meta = {
  apiVersion: 2,
  key: "{{key}}",
  name: "{{name}}",
  version: "0.1.0",
  capabilities: ["storage"],
  storage: [{ label: "The number of turns seen", retention: "Until the plugin is removed" }],
} satisfies AstrLinkPlugin.Meta;

export async function observeTurn(turn: AstrLinkPlugin.TurnView) {
  const seen = ((await astrlink.store.get("turns")) as number | null) ?? 0;
  await astrlink.store.set("turns", seen + 1);
  console.info("turn", turn.session.turn, "of session", turn.session.id);
}

export async function describeStatus(): Promise<AstrLinkPlugin.ViewBlock[]> {
  const seen = ((await astrlink.store.get("turns")) as number | null) ?? 0;
  return [{ type: "kv", items: [{ label: "Turns seen", value: String(seen) }] }];
}
`,
			tests: `[
  {
    "name": "counts a turn",
    "hook": "observeTurn",
    "args": [{ "session": { "id": "s1", "turn": 3 }, "user": { "text": "hello", "typed": "hello" } }],
    "store": { "turns": 1 },
    "expect": { "store": { "turns": 2 } }
  },
  {
    "name": "reports the count",
    "hook": "describeStatus",
    "store": { "turns": 5 },
    "expect": { "result": [{ "type": "kv", "items": [{ "label": "Turns seen", "value": "5" }] }] }
  }
]
`,
		},
	}
}
