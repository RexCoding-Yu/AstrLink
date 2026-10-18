/**
 * Absolute file paths in a user message, so the view can name a file instead
 * of spelling out its directories. A terminal pastes a dropped file as its
 * full path, often glued to the question that follows, and the directories
 * crowd the question out. Only a path that ends in a file name with an
 * extension counts: directories, API routes, slash commands and relative
 * paths read as text. Keep the rules in step with Core's `shortenFilePaths`,
 * which shortens the request preview the session title is cut from.
 */
export interface FilePathSpan {
  text: string;
  /** The file name, when `text` is a path. */
  file?: string;
}

const EXTENSION_LIMIT = 10;
/** Characters before a path that make it relative or part of a URL. */
const START_BLOCKERS = /[A-Za-z0-9_.~/\\-]/;
/** Shell and markup delimiters, and the punctuation of CJK prose. */
const SEGMENT_BREAKS = new Set(
  ":*?\"'`<>|,;，。？！：；、（）【】「」『』《》“”‘’",
);

function isAsciiAlnum(char: string | undefined): boolean {
  return char !== undefined && /[A-Za-z0-9]/.test(char);
}

/** `text[start, end)` ends in `.ext` after a non-empty name. */
function extensionEndsAt(text: string, start: number, end: number): boolean {
  let dot = end - 1;
  let letter = false;
  while (dot > start && isAsciiAlnum(text[dot])) {
    if (/[A-Za-z]/.test(text[dot]!)) letter = true;
    dot -= 1;
  }
  const length = end - 1 - dot;
  return (
    dot > start &&
    text[dot] === "." &&
    letter &&
    length >= 1 &&
    length <= EXTENSION_LIMIT
  );
}

function pathAt(
  text: string,
  start: number,
  cut: boolean,
): { end: number; name: string } | null {
  const windows =
    /[A-Za-z]/.test(text[start] ?? "") &&
    text[start + 1] === ":" &&
    (text[start + 2] === "\\" || text[start + 2] === "/");
  let index: number;
  let minSegments = 1;
  if (windows) {
    index = start + 3;
  } else if (text[start] === "~" && text[start + 1] === "/") {
    index = start + 2;
  } else if (text[start] === "/") {
    // A root path needs a directory, so `/review` stays a command.
    index = start + 1;
    minSegments = 2;
  } else {
    return null;
  }

  let segments = 0;
  let segmentStart = index;
  let segmentEnd = index;
  let glued = false;
  for (;;) {
    let cursor = index;
    while (cursor < text.length) {
      const char = text[cursor]!;
      if (
        !windows &&
        char === "\\" &&
        cursor + 1 < text.length &&
        text[cursor + 1] !== "\n"
      ) {
        cursor += 2;
        continue;
      }
      if (/\s/.test(char) || char === "/" || char === "\\") break;
      if (SEGMENT_BREAKS.has(char)) break;
      // A file name glued to CJK prose ends at its extension.
      if (char > "\u007f" && extensionEndsAt(text, index, cursor)) {
        glued = true;
        break;
      }
      cursor += 1;
    }
    if (cursor === index) break;
    segments += 1;
    segmentStart = index;
    segmentEnd = cursor;
    if (glued) break;
    const separator = text[cursor];
    if (separator !== "/" && !(windows && separator === "\\")) break;
    index = cursor + 1;
  }
  if (segments < minSegments) return null;

  // Trailing text such as `:12`, `#L4` or a full stop is not the file's.
  let end = segmentEnd;
  while (
    end > segmentStart &&
    !(
      extensionEndsAt(text, segmentStart, end) &&
      (end === segmentEnd || !isAsciiAlnum(text[end]))
    )
  ) {
    end -= 1;
  }
  if (end === segmentStart) {
    if (!cut || segmentEnd !== text.length || !text.endsWith("…")) return null;
    end = segmentEnd - 1;
    if (end === segmentStart) return null;
  }
  const raw = text.slice(segmentStart, end);
  return { end, name: windows ? raw : raw.replace(/\\(.)/g, "$1") };
}

export function splitFilePaths(text: string): FilePathSpan[] {
  return spansOf(text, false);
}

function spansOf(text: string, cut: boolean): FilePathSpan[] {
  const spans: FilePathSpan[] = [];
  let plain = 0;
  let index = 0;
  while (index < text.length) {
    const found =
      index === 0 || !START_BLOCKERS.test(text[index - 1]!)
        ? pathAt(text, index, cut)
        : null;
    if (!found) {
      index += 1;
      continue;
    }
    if (index > plain) spans.push({ text: text.slice(plain, index) });
    spans.push({ text: text.slice(index, found.end), file: found.name });
    plain = found.end;
    index = found.end;
  }
  if (plain < text.length) spans.push({ text: text.slice(plain) });
  return spans;
}

function joinSpans(spans: FilePathSpan[]): string {
  return spans.map((span) => span.file ?? span.text).join("");
}

/** The text with each file path replaced by its file name. */
export function compactFilePaths(text: string): string {
  return joinSpans(spansOf(text, false));
}

/**
 * A title Core cut from the user's text, with its file paths named. Titles
 * recorded before Core named files keep the full path, and the "…" Core ends
 * a cut title with can fall inside the file name and take its extension, so
 * a path that runs into that ellipsis is named by what is left of it.
 */
export function compactTitle(title: string): string {
  return joinSpans(spansOf(title, true));
}
