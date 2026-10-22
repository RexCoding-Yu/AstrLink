import {
  createHighlighterCore,
  type HighlighterCore,
  type ThemedToken,
} from "shiki/core";
import { createJavaScriptRegexEngine } from "shiki/engine/javascript";

/** A request to the highlighter worker. */
export interface HighlightRequest {
  id: number;
  code: string;
  language: string;
}

/** The worker's answer: null for a language it does not know. */
export interface HighlightResponse {
  id: number;
  lines: HighlightedLine[] | null;
}

/** One line of tokens: each token's text and its light and dark colors. */
export type HighlightedLine = Array<
  [text: string, style: Record<string, string>]
>;

// Add the languages you need. Each one loads the first time it is used.
const languages = {
  bash: () => import("shiki/langs/shellscript.mjs"),
  c: () => import("shiki/langs/c.mjs"),
  cpp: () => import("shiki/langs/cpp.mjs"),
  csharp: () => import("shiki/langs/csharp.mjs"),
  css: () => import("shiki/langs/css.mjs"),
  diff: () => import("shiki/langs/diff.mjs"),
  docker: () => import("shiki/langs/docker.mjs"),
  go: () => import("shiki/langs/go.mjs"),
  html: () => import("shiki/langs/html.mjs"),
  ini: () => import("shiki/langs/ini.mjs"),
  java: () => import("shiki/langs/java.mjs"),
  javascript: () => import("shiki/langs/javascript.mjs"),
  json: () => import("shiki/langs/json.mjs"),
  jsonc: () => import("shiki/langs/jsonc.mjs"),
  jsx: () => import("shiki/langs/jsx.mjs"),
  kotlin: () => import("shiki/langs/kotlin.mjs"),
  lua: () => import("shiki/langs/lua.mjs"),
  make: () => import("shiki/langs/make.mjs"),
  markdown: () => import("shiki/langs/markdown.mjs"),
  php: () => import("shiki/langs/php.mjs"),
  powershell: () => import("shiki/langs/powershell.mjs"),
  python: () => import("shiki/langs/python.mjs"),
  ruby: () => import("shiki/langs/ruby.mjs"),
  rust: () => import("shiki/langs/rust.mjs"),
  scss: () => import("shiki/langs/scss.mjs"),
  sql: () => import("shiki/langs/sql.mjs"),
  swift: () => import("shiki/langs/swift.mjs"),
  toml: () => import("shiki/langs/toml.mjs"),
  tsx: () => import("shiki/langs/tsx.mjs"),
  typescript: () => import("shiki/langs/typescript.mjs"),
  xml: () => import("shiki/langs/xml.mjs"),
  yaml: () => import("shiki/langs/yaml.mjs"),
};

type Language = keyof typeof languages;

const aliases: Record<string, Language> = {
  "c#": "csharp",
  "c++": "cpp",
  cc: "cpp",
  cjs: "javascript",
  cs: "csharp",
  cts: "typescript",
  dockerfile: "docker",
  golang: "go",
  js: "javascript",
  kt: "kotlin",
  kts: "kotlin",
  makefile: "make",
  md: "markdown",
  mjs: "javascript",
  mts: "typescript",
  patch: "diff",
  properties: "ini",
  ps1: "powershell",
  pwsh: "powershell",
  py: "python",
  rb: "ruby",
  rs: "rust",
  sh: "bash",
  shell: "bash",
  shellscript: "bash",
  svg: "xml",
  ts: "typescript",
  yml: "yaml",
  zsh: "bash",
};

let highlighter: Promise<HighlighterCore> | undefined;
const loaded = new Map<Language, Promise<void>>();

function getHighlighter() {
  highlighter ??= createHighlighterCore({
    themes: [
      import("shiki/themes/github-light.mjs"),
      import("shiki/themes/github-dark.mjs"),
    ],
    langs: [],
    engine: createJavaScriptRegexEngine({ forgiving: true }),
  });

  return highlighter;
}

function getLanguage(language: string): Language | "ansi" | null {
  const name = language.toLowerCase();

  if (name === "ansi") {
    return name;
  }

  if (Object.hasOwn(languages, name)) {
    return name as Language;
  }

  return Object.hasOwn(aliases, name) ? aliases[name] : null;
}

/**
 * Tokenizes `code` with light and dark colors per token, or returns null for
 * a language that is not listed. Runs in the highlighter worker: the first
 * pass over a large grammar such as TypeScript takes most of a second.
 */
export async function tokenizeCode(
  code: string,
  language: string,
): Promise<HighlightedLine[] | null> {
  const lang = getLanguage(language);

  if (!lang) {
    return null;
  }

  const shiki = await getHighlighter();

  if (lang !== "ansi") {
    let load = loaded.get(lang);
    if (!load) {
      load = shiki.loadLanguage(languages[lang]);
      loaded.set(lang, load);
    }
    await load;
  }

  const tokenize = (text: string): ThemedToken[][] =>
    shiki.codeToTokens(text, {
      lang,
      themes: { light: "github-light", dark: "github-dark" },
      defaultColor: false,
    }).tokens;

  // ANSI has no grammar state, so each line is tokenized on its own and
  // gets the same colors wherever it starts.
  const tokens =
    lang === "ansi"
      ? code.split("\n").flatMap((line) => tokenize(line))
      : tokenize(code);

  return tokens.map((line) =>
    line.map((token) => [token.content, token.htmlStyle ?? {}]),
  );
}
