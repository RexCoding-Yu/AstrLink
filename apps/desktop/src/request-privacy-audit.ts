import { i18n } from "./i18n";
import { privacyKindLabel, sortPrivacyKinds } from "./privacy-kind";
import type { RequestRecord, RequestStatus } from "./request-record-model";
import {
  groupTurns,
  parsePolicySummary,
  type PlaceholderExplanation,
} from "./request-trajectory-model";

/**
 * What the privacy policy did to one call, as the audit tab states it. Every
 * value maps to one of Core's decisions, except `off`, `inspecting`, `failed`
 * and `unknown`, which describe why there is no decision to read.
 */
export type PrivacyOutcome =
  | "off"
  | "clean"
  | "trusted"
  | "warned"
  | "redacted"
  | "blocked"
  | "unchecked"
  | "inspecting"
  | "failed"
  | "unknown";

export interface PrivacyKindCount {
  kind: string;
  label: string;
  count: number;
}

/** One call's privacy result, with what it replaced and what it put back. */
export interface PrivacyCallAudit {
  recordId: string;
  /** 1-based position across the whole conversation. */
  index: number;
  /** 1-based user turn, null when the protocol has no turns. */
  turnIndex: number | null;
  /** 1-based position within its turn. */
  turnCallIndex: number;
  startedAt: string;
  status: RequestStatus;
  outcome: PrivacyOutcome;
  /** Why a failed or unfinished check stopped, taken from Core's own summary. */
  detail: string | null;
  kinds: PrivacyKindCount[];
  /** Distinct values replaced before the request went upstream. */
  replaced: number;
  /** The response path was set to put replaced values back. */
  restoreEnabled: boolean;
  restored: number;
  restoredVisible: number;
  restoredTools: number;
  /** Parts of the answer left with replacements still in them. */
  unrestored: number;
  explainedBy: PlaceholderExplanation | null;
}

/** What a set of calls added up to, for the session header or one turn. */
export interface PrivacyTally {
  /** At the severity of the worst call. */
  outcome: PrivacyOutcome;
  replaced: number;
  restored: number;
  restoredVisible: number;
  restoredTools: number;
  unrestored: number;
  kinds: PrivacyKindCount[];
}

/** One user turn's calls, which the audit list folds under one header. */
export interface PrivacyTurnAudit extends PrivacyTally {
  /** The turn's first call, which stays first as later calls land. */
  key: string;
  turnIndex: number | null;
  callCount: number;
  /** The calls worth a row; see `notableCall`. */
  calls: PrivacyCallAudit[];
  /** Calls that replaced nothing, counted instead of listed. */
  quietCalls: number;
}

export interface PrivacyAuditSummary extends PrivacyTally {
  calls: PrivacyCallAudit[];
  turns: PrivacyTurnAudit[];
  callCount: number;
  blockedCalls: number;
  /** Calls that reached the model without a completed inspection. */
  uncheckedCalls: number;
  /** Calls no policy applied to, so the body went upstream untouched. */
  offCalls: number;
  /** Calls that went uninspected to a provider the user trusts. */
  trustedCalls: number;
  warnedCalls: number;
  redactedCalls: number;
  /**
   * How every redacted call explained its placeholders, when they all did it
   * the same way; rows then leave it to the header instead of repeating it.
   */
  explainedBy: PlaceholderExplanation | null;
}

/**
 * A call whose privacy result is worth a row of its own: it changed, stopped,
 * or could not be inspected. Calls with nothing to report, including those a
 * trusted provider received by the user's choice, are counted in the header
 * instead, so a 40-call agent loop does not bury the one that mattered.
 */
export function notableCall(audit: PrivacyCallAudit): boolean {
  return (
    audit.outcome !== "clean" &&
    audit.outcome !== "off" &&
    audit.outcome !== "trusted"
  );
}

export function requestPrivacyAudit(
  turns: RequestRecord[],
): PrivacyAuditSummary {
  const calls: PrivacyCallAudit[] = [];
  const groups: PrivacyCallAudit[][] = [];
  let index = 0;
  for (const group of groupTurns(turns)) {
    const audits = group.records.map((record, position) => {
      index += 1;
      return callAudit(record, index, group.turnIndex, position + 1);
    });
    calls.push(...audits);
    // Calls from a protocol without turns share one group instead of each
    // getting a header of its own.
    const last = groups[groups.length - 1];
    if (group.turnIndex === null && last?.[0]?.turnIndex === null) {
      last.push(...audits);
    } else {
      groups.push(audits);
    }
  }

  const count = (outcome: PrivacyOutcome) =>
    calls.filter((call) => call.outcome === outcome).length;
  const explanations = new Set(
    calls
      .filter((call) => call.outcome === "redacted")
      .map((call) => call.explainedBy),
  );
  return {
    ...tally(calls),
    calls,
    turns: groups.map(turnAudit),
    callCount: calls.length,
    blockedCalls: count("blocked"),
    uncheckedCalls: count("unchecked"),
    offCalls: count("off"),
    trustedCalls: count("trusted"),
    warnedCalls: count("warned"),
    redactedCalls: count("redacted"),
    explainedBy:
      explanations.size === 1 ? ([...explanations][0] ?? null) : null,
  };
}

function turnAudit(calls: PrivacyCallAudit[]): PrivacyTurnAudit {
  const notable = calls.filter(notableCall);
  return {
    ...tally(calls),
    key: calls[0]?.recordId ?? "",
    turnIndex: calls[0]?.turnIndex ?? null,
    callCount: calls.length,
    calls: notable,
    quietCalls: calls.length - notable.length,
  };
}

function tally(calls: PrivacyCallAudit[]): PrivacyTally {
  const kindCounts = new Map<string, number>();
  const result: PrivacyTally = {
    outcome: "unknown",
    replaced: 0,
    restored: 0,
    restoredVisible: 0,
    restoredTools: 0,
    unrestored: 0,
    kinds: [],
  };
  calls.forEach((call, position) => {
    result.replaced += call.replaced;
    result.restored += call.restored;
    result.restoredVisible += call.restoredVisible;
    result.restoredTools += call.restoredTools;
    result.unrestored += call.unrestored;
    for (const { kind, count } of call.kinds) {
      kindCounts.set(kind, (kindCounts.get(kind) ?? 0) + count);
    }
    if (
      position === 0 ||
      outcomeSeverity(call.outcome) > outcomeSeverity(result.outcome)
    ) {
      result.outcome = call.outcome;
    }
  });
  result.kinds = sortPrivacyKinds([...kindCounts.keys()]).map((kind) => ({
    kind,
    label: privacyKindLabel(kind),
    count: kindCounts.get(kind) ?? 0,
  }));
  return result;
}

export type PrivacyListItem =
  | { kind: "turn"; turn: PrivacyTurnAudit; open: boolean }
  | { kind: "call"; call: PrivacyCallAudit; nested: boolean }
  /** Calls of one open turn that replaced nothing. */
  | { kind: "quiet-calls"; key: string; count: number; nested: boolean }
  /** Turns where nothing was replaced, which get no header. */
  | { kind: "quiet-turns"; count: number };

/** The call list once calls fold under their turns. */
export interface PrivacyListLayout {
  items: PrivacyListItem[];
  openTurns: Set<string>;
  /** The header each call sits under; empty when the list has none. */
  turnByRecordId: Map<string, string>;
}

/**
 * Folds the call list by turn, as the trajectory list does: a turn is open
 * when the operator said so, and otherwise when it holds the selected call or
 * is the latest turn with a row to show. Turns where nothing happened get no
 * header, only a count. A conversation of one turn does not fold.
 */
export function privacyListLayout(
  summary: PrivacyAuditSummary,
  overrides: ReadonlyMap<string, boolean>,
  selectedRequestId: string | null,
): PrivacyListLayout {
  const items: PrivacyListItem[] = [];
  const openTurns = new Set<string>();
  const turnByRecordId = new Map<string, string>();
  if (summary.turns.length <= 1) {
    const [turn] = summary.turns;
    for (const call of turn?.calls ?? []) {
      items.push({ kind: "call", call, nested: false });
    }
    if (turn && turn.quietCalls > 0 && items.length > 0) {
      items.push({
        kind: "quiet-calls",
        key: turn.key,
        count: turn.quietCalls,
        nested: false,
      });
    }
    return { items, openTurns, turnByRecordId };
  }
  const shown = summary.turns.filter((turn) => turn.calls.length > 0);
  for (const turn of summary.turns) {
    for (const call of turn.calls) turnByRecordId.set(call.recordId, turn.key);
  }
  const selectedTurn = turnByRecordId.get(selectedRequestId ?? "");
  const latestTurn = shown[shown.length - 1]?.key;
  for (const turn of shown) {
    const open =
      overrides.get(turn.key) ??
      (turn.key === selectedTurn || turn.key === latestTurn);
    items.push({ kind: "turn", turn, open });
    if (!open) continue;
    openTurns.add(turn.key);
    for (const call of turn.calls) {
      items.push({ kind: "call", call, nested: true });
    }
    if (turn.quietCalls > 0) {
      items.push({
        kind: "quiet-calls",
        key: turn.key,
        count: turn.quietCalls,
        nested: true,
      });
    }
  }
  const quietTurns = summary.turns.length - shown.length;
  if (quietTurns > 0 && shown.length > 0) {
    items.push({ kind: "quiet-turns", count: quietTurns });
  }
  return { items, openTurns, turnByRecordId };
}

export function privacyListItemKey(item: PrivacyListItem): string {
  switch (item.kind) {
    case "turn":
      return `turn:${item.turn.key}`;
    case "call":
      return `call:${item.call.recordId}`;
    case "quiet-calls":
      return `quiet:${item.key}`;
    case "quiet-turns":
      return "quiet-turns";
  }
}

/**
 * Orders outcomes by how much the operator has to look at: a blocked call
 * first, then a call that went out uninspected, then one that was redacted.
 * A trusted call outranks a clean one, so a session that mixes them does not
 * read as if every call was checked.
 */
export function outcomeSeverity(outcome: PrivacyOutcome): number {
  switch (outcome) {
    case "blocked":
      return 10;
    case "unchecked":
      return 9;
    case "failed":
      return 8;
    case "redacted":
      return 7;
    case "warned":
      return 6;
    case "inspecting":
      return 5;
    case "unknown":
      return 4;
    case "trusted":
      return 3;
    case "clean":
      return 2;
    case "off":
      return 1;
  }
}

function callAudit(
  record: RequestRecord,
  index: number,
  turnIndex: number | null,
  turnCallIndex: number,
): PrivacyCallAudit {
  const restore = record.privacy_restore;
  const outcome = callOutcome(record);
  return {
    recordId: record.id,
    index,
    turnIndex,
    turnCallIndex,
    startedAt: record.started_at,
    status: record.status,
    outcome,
    detail: callDetail(record, outcome),
    kinds: callKinds(record),
    replaced: restore?.mapping_count ?? 0,
    restoreEnabled: restore?.enabled ?? false,
    restored: restore?.restored_count ?? 0,
    restoredVisible: restore?.visible_restored_count ?? 0,
    restoredTools: restore?.tool_argument_restored_count ?? 0,
    unrestored: restore?.fallback_count ?? 0,
    explainedBy: callExplanation(record),
  };
}

function callExplanation(record: RequestRecord): PlaceholderExplanation | null {
  const summary = privacyEventSummary(record);
  return summary === null
    ? null
    : (parsePolicySummary(summary)?.explainedBy ?? null);
}

/**
 * Reads the recorded decision, and falls back to the record's own evidence for
 * rows written before decisions existed. A restore summary is recorded only
 * when values were replaced, and a blocked record has no summary at all.
 */
export function callOutcome(record: RequestRecord): PrivacyOutcome {
  switch (record.privacy_decision) {
    case "none":
      return "off";
    case "trusted":
      return "trusted";
    case "allow":
      return "clean";
    case "warn":
      return "warned";
    case "redact":
      return "redacted";
    case "block":
      return "blocked";
    case "fail_open":
      return "unchecked";
  }
  if (record.status === "blocked") return "blocked";
  if (record.status === "pending") return "inspecting";
  if (record.privacy_restore) return "redacted";
  const summary = privacyEventSummary(record);
  if (summary === null) return "unknown";
  const action = summary.split(" · ")[0];
  if (action === "allow") {
    return parsePolicySummary(summary)?.trusted ? "trusted" : "clean";
  }
  if (action === "warn") return "warned";
  if (action === "block") return "blocked";
  if (action === "redact") return "redacted";
  return "failed";
}

/** What the policy found, by kind. Findings stand in for a blocked call. */
function callKinds(record: RequestRecord): PrivacyKindCount[] {
  const counts = new Map<string, number>();
  for (const hit of record.privacy_restore?.hits ?? []) {
    counts.set(hit.kind, (counts.get(hit.kind) ?? 0) + hit.count);
  }
  if (counts.size === 0) {
    for (const finding of record.privacy_findings ?? []) {
      counts.set(finding.kind, (counts.get(finding.kind) ?? 0) + finding.count);
    }
  }
  return sortPrivacyKinds([...counts.keys()]).map((kind) => ({
    kind,
    label: privacyKindLabel(kind),
    count: counts.get(kind) ?? 0,
  }));
}

/**
 * Core's own words for a stop that is not a decision, so the tab can name what
 * happened without inventing a reason. A decision is left to the badge.
 */
function callDetail(
  record: RequestRecord,
  outcome: PrivacyOutcome,
): string | null {
  if (
    outcome !== "failed" &&
    outcome !== "unchecked" &&
    outcome !== "inspecting"
  ) {
    return null;
  }
  const summary = privacyEventSummary(record);
  if (summary === null) {
    return outcome === "inspecting" ? null : (record.error?.code ?? null);
  }
  return summary;
}

function privacyEventSummary(record: RequestRecord): string | null {
  const event = [...record.events]
    .reverse()
    .find((item) => item.kind === "privacy");
  return event?.summary ?? null;
}

export function privacyOutcomeLabel(outcome: PrivacyOutcome): string {
  return i18n.t(`privacyAudit.outcome.${outcome}`);
}

export function privacyOutcomeTone(
  outcome: PrivacyOutcome,
): "blocked" | "negative" | "neutral" | "pending" {
  switch (outcome) {
    case "blocked":
      return "blocked";
    case "failed":
    case "unchecked":
      return "negative";
    case "redacted":
    case "warned":
    case "inspecting":
      return "pending";
    default:
      return "neutral";
  }
}

export function privacyKindCountLabel({
  label,
  count,
}: PrivacyKindCount): string {
  return i18n.t("privacyAudit.kindCount", { kind: label, count });
}

/** Every outcome the tab can name, so a missing label fails a test, not a render. */
export const PRIVACY_OUTCOMES: readonly PrivacyOutcome[] = [
  "off",
  "clean",
  "trusted",
  "warned",
  "redacted",
  "blocked",
  "unchecked",
  "inspecting",
  "failed",
  "unknown",
];
