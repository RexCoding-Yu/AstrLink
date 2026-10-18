import { privacyKindLabel, sortPrivacyKinds } from "./privacy-kind";
import type { RequestRecord } from "./request-record-model";
import { splitPrivacyHighlights } from "./request-trajectory-model";

/** One place a replacement sits in the request sent upstream. */
export interface PrivacyReplacementSite {
  /** "input[5].content[0].text"; null when the body is not JSON. */
  path: string | null;
  /** The role of the message it sits in, when the body names one. */
  role: string | null;
  before: string;
  after: string;
}

export interface PrivacyReplacement {
  placeholder: string;
  kind: string;
  label: string;
  /** Times the request sent upstream carries it. */
  count: number;
  /** The first few places it appears, in body order. */
  sites: PrivacyReplacementSite[];
  /** Every distinct path it appears at; empty when the body is not JSON. */
  paths: string[];
}

const CONTEXT_CHARS = 48;
const SITES_PER_REPLACEMENT = 3;
// Core mints token placeholders with a 16-hex suffix. The placeholder notice
// quotes their shape ("<PRIVATE_KIND_hex>"), which replaced nothing.
const MINTED_TOKEN = /_[0-9a-f]{16}>$/i;

/**
 * Every replacement in the request as sent upstream, in the order it first
 * appears, with the text around it. The originals never reach this body, so
 * the context is all a reviewer can check without unlocking raw content.
 *
 * A natural stand-in reads like an ordinary value, and documentation in a
 * request quotes the reserved ranges they come from. With `replacedKinds`,
 * one of a kind the call did not replace is left out as text it carried.
 */
export function privacyReplacements(
  body: string,
  replacedKinds?: ReadonlySet<string>,
): PrivacyReplacement[] {
  const found = new Map<string, PrivacyReplacement>();
  const visit = (text: string, path: string | null, role: string | null) => {
    // Every placeholder shape carries "<", a digit, or the reserved host.
    if (!/[<\d]|private\.invalid/i.test(text)) return;
    let offset = 0;
    for (const span of splitPrivacyHighlights(text)) {
      const start = offset;
      offset += span.text.length;
      if (!span.kind) continue;
      if (span.text.startsWith("<")) {
        if (!MINTED_TOKEN.test(span.text)) continue;
      } else if (replacedKinds && !replacedKinds.has(span.kind)) {
        continue;
      }
      const entry = found.get(span.text) ?? {
        placeholder: span.text,
        kind: span.kind,
        label: privacyKindLabel(span.kind),
        count: 0,
        sites: [],
        paths: [],
      };
      entry.count += 1;
      if (path !== null && !entry.paths.includes(path)) entry.paths.push(path);
      if (entry.sites.length < SITES_PER_REPLACEMENT) {
        entry.sites.push({
          path,
          role,
          before: contextBefore(text, start),
          after: contextAfter(text, offset),
        });
      }
      found.set(span.text, entry);
    }
  };
  let parsed: unknown;
  try {
    parsed = JSON.parse(body);
  } catch {
    visit(body, null, null);
    return [...found.values()];
  }
  walkStrings(parsed, "", null, visit);
  return [...found.values()];
}

/**
 * Separates the stand-ins a call made from markers it only carried. A model
 * that quotes an earlier call's marker in its reasoning or reply has the
 * client send that text back; the call neither replaced the marker nor will
 * restore it. A call's own replacements sit where it recorded a finding.
 *
 * Findings locate the client's request, which conversion or an inserted
 * notice can reshape, so a kind none of whose stand-ins lines up stays made.
 * A natural stand-in away from every finding may be ordinary text and is
 * dropped rather than called carried.
 */
export function splitCarriedReplacements(
  replacements: PrivacyReplacement[],
  findings: RequestRecord["privacy_findings"],
): { made: PrivacyReplacement[]; carried: PrivacyReplacement[] } {
  const located = new Map<string, string[]>();
  for (const { kind, json_path } of findings ?? []) {
    located.set(kind, [
      ...(located.get(kind) ?? []),
      jsonPointerLabel(json_path),
    ]);
  }
  if (located.size === 0) return { made: replacements, carried: [] };
  const atFinding = (replacement: PrivacyReplacement) =>
    replacement.paths.length === 0 ||
    replacement.paths.some((path) =>
      (located.get(replacement.kind) ?? []).some((finding) =>
        samePlace(path, finding),
      ),
    );
  const aligned = new Set(
    replacements.filter(atFinding).map((replacement) => replacement.kind),
  );
  const made: PrivacyReplacement[] = [];
  const carried: PrivacyReplacement[] = [];
  for (const replacement of replacements) {
    if (
      atFinding(replacement) ||
      (located.has(replacement.kind) && !aligned.has(replacement.kind))
    ) {
      made.push(replacement);
    } else if (replacement.placeholder.startsWith("<")) {
      carried.push(replacement);
    }
  }
  return { made, carried };
}

/** One path holds the other: a finding may name the string or a field in it. */
function samePlace(left: string, right: string): boolean {
  const [outer, inner] =
    left.length <= right.length ? [left, right] : [right, left];
  return (
    inner === outer ||
    (inner.startsWith(outer) &&
      (inner[outer.length] === "." || inner[outer.length] === "["))
  );
}

function walkStrings(
  value: unknown,
  path: string,
  role: string | null,
  visit: (text: string, path: string, role: string | null) => void,
) {
  if (typeof value === "string") {
    visit(value, path, role);
    return;
  }
  if (Array.isArray(value)) {
    value.forEach((item, index) =>
      walkStrings(item, `${path}[${index}]`, role, visit),
    );
    return;
  }
  if (value === null || typeof value !== "object") return;
  const object = value as Record<string, unknown>;
  const own = typeof object.role === "string" ? object.role : role;
  for (const [key, child] of Object.entries(object)) {
    walkStrings(child, path ? `${path}.${key}` : key, own, visit);
  }
}

function contextBefore(text: string, start: number): string {
  const window = compact(
    text.slice(Math.max(0, start - CONTEXT_CHARS * 4), start),
  );
  if (window.length <= CONTEXT_CHARS) return window;
  const cut = cutOutsidePlaceholder(window, window.length - CONTEXT_CHARS, {
    keep: "tail",
  });
  return `…${window.slice(cut).trimStart()}`;
}

function contextAfter(text: string, end: number): string {
  const window = compact(text.slice(end, end + CONTEXT_CHARS * 4));
  if (window.length <= CONTEXT_CHARS) return window;
  const cut = cutOutsidePlaceholder(window, CONTEXT_CHARS, { keep: "head" });
  return `${window.slice(0, cut).trimEnd()}…`;
}

/**
 * Half a placeholder reads as a different one, so a cut that lands inside a
 * neighbouring placeholder drops it, unless that would leave nothing.
 */
function cutOutsidePlaceholder(
  text: string,
  cut: number,
  { keep }: { keep: "head" | "tail" },
): number {
  let offset = 0;
  for (const span of splitPrivacyHighlights(text)) {
    const end = offset + span.text.length;
    if (span.kind && offset < cut && cut < end) {
      if (keep === "head") return offset > 0 ? offset : end;
      return end < text.length ? end : offset;
    }
    offset = end;
  }
  return cut;
}

function compact(text: string): string {
  return text.replace(/\s+/g, " ");
}

/** "/input/0/content" written the way replacement sites are: "input[0].content". */
export function jsonPointerLabel(pointer: string): string {
  return pointer
    .split("/")
    .slice(1)
    .map((segment) => segment.replaceAll("~1", "/").replaceAll("~0", "~"))
    .reduce(
      (path, segment) =>
        /^\d+$/.test(segment)
          ? `${path}[${segment}]`
          : path
            ? `${path}.${segment}`
            : segment,
      "",
    );
}

export interface PrivacyFindingSite {
  path: string;
  kinds: Array<{ kind: string; label: string; count: number }>;
}

/** Where a warning or a block found its matches, by location. */
export function privacyFindingSites(
  findings: RequestRecord["privacy_findings"],
): PrivacyFindingSite[] {
  const byPath = new Map<string, Map<string, number>>();
  for (const { json_path, kind, count } of findings ?? []) {
    const kinds = byPath.get(json_path) ?? new Map<string, number>();
    kinds.set(kind, (kinds.get(kind) ?? 0) + count);
    byPath.set(json_path, kinds);
  }
  return [...byPath].map(([pointer, kinds]) => ({
    path: jsonPointerLabel(pointer) || pointer,
    kinds: sortPrivacyKinds([...kinds.keys()]).map((kind) => ({
      kind,
      label: privacyKindLabel(kind),
      count: kinds.get(kind) ?? 0,
    })),
  }));
}
