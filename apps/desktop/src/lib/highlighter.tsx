import type { CSSProperties, ReactNode } from "react";

import type {
  HighlightedLine,
  HighlightRequest,
  HighlightResponse,
} from "./highlighter-tokens";

/** Longer code stays plain: rendering its tokens costs more than it helps. */
const HIGHLIGHT_LIMIT = 32 * 1024;
/** Highlighted blocks kept, so a row opened again shows colors at once. */
const CACHE_SIZE = 64;

interface Entry {
  promise: Promise<ReactNode[] | null>;
  /** Undefined until the worker answers; null when it cannot highlight. */
  lines?: ReactNode[] | null;
}

const entries = new Map<string, Entry>();
const pending = new Map<number, (lines: HighlightedLine[] | null) => void>();
let worker: Worker | null | undefined;
let nextId = 0;

function getWorker(): Worker | null {
  if (worker !== undefined) {
    return worker;
  }

  let created: Worker;
  try {
    created = new Worker(new URL("./highlighter.worker.ts", import.meta.url));
  } catch {
    // No worker can run here, as under a test DOM: the code stays plain.
    worker = null;
    return worker;
  }

  created.addEventListener(
    "message",
    (event: MessageEvent<HighlightResponse>) => {
      pending.get(event.data.id)?.(event.data.lines);
      pending.delete(event.data.id);
    },
  );
  // A worker that cannot start or crashes leaves the code plain.
  created.addEventListener("error", () => {
    for (const resolve of pending.values()) resolve(null);
    pending.clear();
    created.terminate();
    worker = null;
  });
  worker = created;
  return worker;
}

function tokenize(
  code: string,
  language: string,
): Promise<HighlightedLine[] | null> {
  const target = getWorker();

  if (!target) {
    return Promise.resolve(null);
  }

  const id = ++nextId;
  return new Promise((resolve) => {
    pending.set(id, resolve);
    target.postMessage({ id, code, language } satisfies HighlightRequest);
  });
}

function toLines(tokens: HighlightedLine[]): ReactNode[] {
  return tokens.map((line) =>
    line.map(([text, style], index) => (
      <span
        key={index}
        style={style as CSSProperties}
        className="text-(--shiki-light) dark:text-(--shiki-dark)"
      >
        {text}
      </span>
    )),
  );
}

function cacheKey(code: string, language: string) {
  return `${language.toLowerCase()}\n${code}`;
}

// Returns the highlighted lines already at hand: undefined until the worker
// has answered, null when it could not highlight the code.
export function peekHighlightedCode(
  code: string,
  language: string,
): ReactNode[] | null | undefined {
  return entries.get(cacheKey(code, language))?.lines;
}

// Returns one React node per line, or null for a language that is not listed,
// for code too long to highlight, or where no worker can run.
export function highlightCode(
  code: string,
  language: string,
): Promise<ReactNode[] | null> {
  if (!language || code.length > HIGHLIGHT_LIMIT) {
    return Promise.resolve(null);
  }

  const key = cacheKey(code, language);
  const found = entries.get(key);

  if (found) {
    entries.delete(key);
    entries.set(key, found);
    return found.promise;
  }

  const entry: Entry = {
    promise: tokenize(code, language).then((tokens) => {
      entry.lines = tokens && toLines(tokens);
      return entry.lines;
    }),
  };
  entries.set(key, entry);

  if (entries.size > CACHE_SIZE) {
    entries.delete(entries.keys().next().value!);
  }

  return entry.promise;
}
