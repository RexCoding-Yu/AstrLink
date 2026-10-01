import { describe, expect, it } from "vitest";

import {
  formatRemaining,
  oldestPendingGrant,
  parseRawAccessList,
  parseRawAccessProofOutcome,
  type RawAccessGrant,
} from "./raw-access-model";

const grant = {
  grant_id: "rawgrant_0123456789abcdef",
  request_id: "request_1",
  status: "pending",
  reason: "the upstream rejected the email field",
  client_name: "claude-code",
  created_at: "2026-09-28T10:00:00Z",
  expires_at: "2026-09-28T10:10:00Z",
};

describe("raw access proof outcomes", () => {
  it("parses decisions and every recoverable refusal", () => {
    expect(
      parseRawAccessProofOutcome({
        outcome: "decided",
        grant: { ...grant, status: "approved", decision: "once" },
      }),
    ).toMatchObject({ outcome: "decided", grant: { decision: "once" } });
    for (const outcome of [
      "proof_required",
      "password_invalid",
      "not_pending",
    ]) {
      expect(parseRawAccessProofOutcome({ outcome })).toEqual({ outcome });
    }
    expect(
      parseRawAccessProofOutcome({
        outcome: "backoff",
        retry_after_seconds: 3,
      }),
    ).toEqual({ outcome: "backoff", retry_after_seconds: 3 });
  });

  it("rejects unknown results and malformed backoffs", () => {
    expect(() => parseRawAccessProofOutcome({ outcome: "sealing" })).toThrow(
      "原文申请数据无效（$.outcome）：未知结果",
    );
    expect(() =>
      parseRawAccessProofOutcome({
        outcome: "backoff",
        retry_after_seconds: 0,
      }),
    ).toThrow("$.retry_after_seconds");
  });
});

describe("raw access lists", () => {
  it("reads pending requests, running grants, and the unlock state", () => {
    const running = {
      ...grant,
      grant_id: "rawgrant_1111111111111111",
      status: "approved",
      decision: "window_5m",
      scope: "all_requests",
    };
    expect(
      parseRawAccessList({ items: [grant], active: [running], unlocked: true }),
    ).toMatchObject({
      pending: [{ grant_id: grant.grant_id, scope: null }],
      active: [{ decision: "window_5m", scope: "all_requests" }],
      unlocked: true,
    });
  });

  it("rejects removed decisions, unknown scopes, and missing fields", () => {
    expect(() =>
      parseRawAccessList({
        items: [{ ...grant, decision: "window_15m" }],
        active: [],
        unlocked: false,
      }),
    ).toThrow("$.items[0].decision");
    expect(() =>
      parseRawAccessList({
        items: [],
        active: [{ ...grant, scope: "session" }],
        unlocked: false,
      }),
    ).toThrow("$.active[0].scope");
    expect(() => parseRawAccessList({ items: [], active: [] })).toThrow(
      "$.unlocked",
    );
  });
});

describe("remaining time", () => {
  it("counts down in minutes, then hours", () => {
    const now = Date.parse("2026-09-28T10:00:00Z");
    expect(formatRemaining("2026-09-28T10:04:05Z", now)).toBe("4:05");
    expect(formatRemaining("2026-09-28T11:00:00Z", now)).toBe("1:00:00");
    expect(formatRemaining("2026-09-28T09:59:00Z", now)).toBe("0:00");
  });
});

describe("oldest pending grant", () => {
  it("skips decided grants and picks the earliest request", () => {
    const later = {
      ...grant,
      grant_id: "rawgrant_1111111111111111",
      created_at: "2026-09-28T10:05:00Z",
    } as RawAccessGrant;
    const earlier = { ...grant, decision: null } as RawAccessGrant;
    const decided = {
      ...grant,
      grant_id: "rawgrant_2222222222222222",
      status: "approved",
      created_at: "2026-09-28T09:00:00Z",
    } as RawAccessGrant;
    expect(oldestPendingGrant([later, decided, earlier])).toBe(earlier);
    expect(oldestPendingGrant([decided])).toBeNull();
  });
});
