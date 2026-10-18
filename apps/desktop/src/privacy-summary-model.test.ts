import { describe, expect, it } from "vitest";

import {
  checkedCalls,
  emptyPrivacyTotals,
  flaggedCalls,
  parsePrivacySummary,
  privacyKindShares,
  privacyTrendPoints,
  sharePercent,
  unprotectedCalls,
  type PrivacyTotals,
} from "./privacy-summary-model";
import { resolveUsageWindow } from "./usage-range";

const now = new Date(2026, 9, 12, 15, 30);

function totals(overrides: Partial<PrivacyTotals> = {}): PrivacyTotals {
  return { ...emptyPrivacyTotals(), ...overrides };
}

const busyDay = totals({
  requests: 20,
  allowed: 8,
  ignored: 2,
  warned: 1,
  redacted: 5,
  blocked: 2,
  fail_open: 1,
  replaced: 12,
  restored: 7,
  restored_visible: 5,
  restored_tool_arguments: 2,
  unrestored: 1,
  ignored_allowlisted: 3,
  ignored_low_confidence: 1,
});

describe("privacy summary IPC contract", () => {
  it("pads Core's sparse day buckets to every day of the window", () => {
    const window = resolveUsageWindow("7d", now);
    const summary = parsePrivacySummary(
      {
        totals: busyDay,
        by_day: [{ date: "2026-10-12", ...busyDay }],
        by_hour: [],
        by_kind: [
          { kind: "email", count: 8 },
          { kind: "common_secret", count: 4 },
        ],
      },
      window,
    );
    expect(summary.by_day).toHaveLength(7);
    expect(summary.by_day.at(-1)).toEqual({ date: "2026-10-12", ...busyDay });
    expect(summary.by_day[0].replaced).toBe(0);
    expect(summary.by_hour).toEqual([]);
    expect(summary.by_kind[0]).toEqual({ kind: "email", count: 8 });
  });

  it("pads a 24-hour window by local hour", () => {
    const window = resolveUsageWindow("1d", now);
    const summary = parsePrivacySummary(
      {
        totals: busyDay,
        by_day: [{ date: "2026-10-12", ...busyDay }],
        by_hour: [{ date: "2026-10-12", hour: 15, ...busyDay }],
        by_kind: [],
      },
      window,
    );
    expect(summary.by_hour).toHaveLength(24);
    expect(summary.by_hour.at(-1)).toMatchObject({ hour: 15, replaced: 12 });
    const points = privacyTrendPoints(summary);
    expect(points).toHaveLength(24);
    expect(points.at(-1)).toEqual({
      slot: "2026-10-12T15",
      date: "2026-10-12",
      hour: 15,
      replaced: 12,
      restored: 7,
      flagged: 8,
      blocked: 2,
    });
  });

  it("rejects payloads that drift from the contract", () => {
    const window = resolveUsageWindow("7d", now);
    const valid = {
      totals: busyDay,
      by_day: [],
      by_hour: [],
      by_kind: [],
    };
    for (const payload of [
      { ...valid, scanned_records: 1 },
      { ...valid, totals: { ...busyDay, replaced: -1 } },
      { ...valid, totals: { ...busyDay, requests: 3 } },
      // Ignored calls are an outcome of their own, not part of allowed.
      { ...valid, totals: { ...busyDay, ignored: 5 } },
      { ...valid, totals: { ...busyDay, ignored_allowlisted: -1 } },
      // Trusted calls are counted in requests like every other outcome.
      { ...valid, totals: { ...busyDay, trusted: 2 } },
      { ...valid, totals: { ...busyDay, restored: 1 } },
      { ...valid, by_day: [{ date: "2020-01-01", ...busyDay }] },
      {
        ...valid,
        by_day: [
          { date: "2026-10-12", ...busyDay },
          { date: "2026-10-12", ...busyDay },
        ],
      },
      { ...valid, by_kind: [{ kind: "Email", count: 1 }] },
      { ...valid, by_kind: [{ kind: "email", count: 0 }] },
      {
        ...valid,
        by_kind: [
          { kind: "email", count: 1 },
          { kind: "email", count: 2 },
        ],
      },
    ]) {
      expect(() => parsePrivacySummary(payload, window)).toThrow();
    }
  });
});

describe("privacy summary figures", () => {
  it("separates checked, flagged and unprotected calls", () => {
    expect(checkedCalls(busyDay)).toBe(18);
    // A call whose matches were all ignored was checked but not flagged.
    expect(flaggedCalls(busyDay)).toBe(8);
    // 20 calls: 18 checked, 1 failed check, 1 with no policy at all.
    expect(unprotectedCalls(busyDay)).toBe(1);
    // Calls to a trusted provider are not counted as protection being off.
    expect(unprotectedCalls({ ...busyDay, requests: 22, trusted: 2 })).toBe(1);
    expect(sharePercent(8, 18)).toBe(44);
    expect(sharePercent(1, 0)).toBe(0);
  });

  it("ranks kinds by share and folds the tail", () => {
    const { rows, hiddenKinds } = privacyKindShares(
      [
        { kind: "phone", count: 1 },
        { kind: "email", count: 6 },
        { kind: "url", count: 1 },
        { kind: "common_secret", count: 2 },
      ],
      2,
    );
    expect(rows.map((row) => [row.kind, row.count, row.percent])).toEqual([
      ["email", 6, 60],
      ["common_secret", 2, 20],
    ]);
    expect(hiddenKinds).toBe(2);
    // A kind Core names that the UI has no label for keeps its own name.
    expect(privacyKindShares([{ kind: "passport", count: 1 }]).rows[0]).toEqual(
      { kind: "passport", label: "passport", count: 1, percent: 100 },
    );
  });
});
