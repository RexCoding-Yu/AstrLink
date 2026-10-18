import { describe, expect, it } from "vitest";

import { i18n } from "./i18n";
import {
  PRIVACY_OUTCOMES,
  privacyListItemKey,
  privacyListLayout,
  requestPrivacyAudit,
} from "./request-privacy-audit";
import {
  emptyTrajectoryFields,
  type RequestRecord,
  type PrivacyDecision,
} from "./request-record-model";

const base: RequestRecord = {
  id: "req_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  parent_request_id: null,
  attempt_index: 1,
  child_count: 0,
  started_at: "2026-08-16T10:00:00Z",
  completed_at: "2026-08-16T10:00:02Z",
  status: "succeeded",
  input_protocol: "openai.responses",
  requested_model: "gpt-4.1",
  streaming: true,
  route_id: "route_01",
  service_id: "service_01",
  local_access_token_id: null,
  http_status: 200,
  latency_ms: 2000,
  usage: { input_tokens: 1, output_tokens: 2, total_tokens: 3 },
  error: null,
  audit: {
    request_body_captured: false,
    response_content_captured: false,
    request_body_truncated: false,
    response_content_truncated: false,
    upstream_request_body_captured: false,
    upstream_response_content_captured: false,
    upstream_request_body_truncated: false,
    upstream_response_content_truncated: false,
  },
  privacy_restore: null,
  ...emptyTrajectoryFields,
};

function record(overrides: Partial<RequestRecord> = {}): RequestRecord {
  return { ...base, ...overrides };
}

function redacted(
  id: string,
  mapping: number,
  restored: number,
  fallback = 0,
): RequestRecord {
  return record({
    id,
    privacy_decision: "redact",
    privacy_restore: {
      enabled: true,
      mapping_count: mapping,
      restored_count: restored,
      visible_restored_count: restored,
      tool_argument_restored_count: 0,
      fallback_count: fallback,
      hits: [
        { kind: "email", count: 1 },
        { kind: "phone", count: 1 },
      ],
    },
  });
}

describe("request privacy audit", () => {
  it("reads the recorded decision first", () => {
    const decisions: Array<[PrivacyDecision, string]> = [
      ["none", "off"],
      ["trusted", "trusted"],
      ["allow", "clean"],
      ["warn", "warned"],
      ["redact", "redacted"],
      ["block", "blocked"],
      ["fail_open", "unchecked"],
    ];
    for (const [decision, outcome] of decisions) {
      const summary = requestPrivacyAudit([
        record({ privacy_decision: decision }),
      ]);
      expect(summary.outcome).toBe(outcome);
      expect(summary.calls[0].outcome).toBe(outcome);
    }
  });

  it("states trusted calls above clean ones without giving them rows", () => {
    const summary = requestPrivacyAudit([
      record({ id: "req_clean", privacy_decision: "allow" }),
      record({ id: "req_trusted", privacy_decision: "trusted" }),
    ]);
    expect(summary.outcome).toBe("trusted");
    expect(summary.trustedCalls).toBe(1);
    expect(summary.turns[0]).toMatchObject({ calls: [], quietCalls: 2 });
    // Without a decision, Core's summary still names the trusted provider.
    const undecided = requestPrivacyAudit([
      record({
        events: [
          {
            kind: "privacy",
            started_at: base.started_at,
            ended_at: base.completed_at,
            status: "succeeded",
            summary: "allow · trusted",
            attempt_index: 1,
          },
        ],
      }),
    ]);
    expect(undecided.outcome).toBe("trusted");
  });

  it("falls back to the record's evidence for rows without a decision", () => {
    // A blocked row carries no restore summary, only findings.
    const blocked = requestPrivacyAudit([
      record({
        status: "blocked",
        privacy_findings: [
          { kind: "common_secret", json_path: "/*/content", count: 2 },
        ],
      }),
    ]);
    expect(blocked.outcome).toBe("blocked");
    expect(blocked.calls[0].kinds).toEqual([
      {
        kind: "common_secret",
        label: i18n.t("privacy.common_secret"),
        count: 2,
      },
    ]);

    // A restore summary is written only when values were replaced.
    expect(requestPrivacyAudit([redacted("req_a", 2, 1)]).outcome).toBe(
      "redacted",
    );
    // No decision, no summary, no privacy event: nothing was recorded.
    expect(requestPrivacyAudit([record()]).outcome).toBe("unknown");
  });

  it("reads a failed inspection from Core's own summary", () => {
    const failed = requestPrivacyAudit([
      record({
        status: "failed",
        error: {
          category: "runtime",
          code: "safety_engine_unavailable",
          message: "local safety engine is unavailable",
          retryable: true,
        },
        events: [
          {
            kind: "privacy",
            started_at: base.started_at,
            ended_at: base.completed_at,
            status: "failed",
            summary: "safety_engine_unavailable · detector_timeout · batch 2/5",
            attempt_index: 1,
          },
        ],
      }),
    ]);
    expect(failed.outcome).toBe("failed");
    expect(failed.calls[0].detail).toContain("detector_timeout");
  });

  it("totals replacements, restores and unrestored parts across calls", () => {
    const summary = requestPrivacyAudit([
      redacted("req_a", 3, 3),
      record({ id: "req_b", privacy_decision: "allow" }),
      redacted("req_c", 2, 0, 1),
    ]);
    expect(summary.callCount).toBe(3);
    expect(summary.replaced).toBe(5);
    expect(summary.restored).toBe(3);
    expect(summary.unrestored).toBe(1);
    expect(summary.redactedCalls).toBe(2);
    // The worst call names the session, not the last one read.
    expect(summary.outcome).toBe("redacted");
    expect(summary.kinds).toEqual([
      { kind: "email", label: i18n.t("privacy.email"), count: 2 },
      { kind: "phone", label: i18n.t("privacy.phone"), count: 2 },
    ]);
  });

  it("folds calls under their turns and leaves quiet turns as a count", () => {
    const inTurn = (call: RequestRecord, turn: number | null) => ({
      ...call,
      turn_index: turn,
    });
    const summary = requestPrivacyAudit([
      inTurn(redacted("req_a", 1, 1), 1),
      inTurn(record({ id: "req_b", privacy_decision: "allow" }), 1),
      inTurn(redacted("req_c", 2, 0, 1), 1),
      inTurn(record({ id: "req_d", privacy_decision: "allow" }), 2),
      inTurn(redacted("req_e", 1, 1), 3),
    ]);
    expect(
      summary.turns.map(({ key, turnIndex, callCount, quietCalls }) => ({
        key,
        turnIndex,
        callCount,
        quietCalls,
      })),
    ).toEqual([
      { key: "req_a", turnIndex: 1, callCount: 3, quietCalls: 1 },
      { key: "req_d", turnIndex: 2, callCount: 1, quietCalls: 1 },
      { key: "req_e", turnIndex: 3, callCount: 1, quietCalls: 0 },
    ]);
    expect(summary.turns[0]).toMatchObject({
      outcome: "redacted",
      replaced: 3,
      restored: 1,
      unrestored: 1,
    });

    const keys = (overrides: Array<[string, boolean]>, selected: string) =>
      privacyListLayout(summary, new Map(overrides), selected).items.map(
        privacyListItemKey,
      );
    // The latest turn with a row starts open; the quiet turn has no header.
    expect(keys([], "req_e")).toEqual([
      "turn:req_a",
      "turn:req_e",
      "call:req_e",
      "quiet-turns",
    ]);
    // So does the turn holding the selected call, unless folded by hand.
    expect(keys([], "req_c")).toEqual([
      "turn:req_a",
      "call:req_a",
      "call:req_c",
      "quiet:req_a",
      "turn:req_e",
      "call:req_e",
      "quiet-turns",
    ]);
    expect(
      keys(
        [
          ["req_a", false],
          ["req_e", false],
        ],
        "req_c",
      ),
    ).toEqual(["turn:req_a", "turn:req_e", "quiet-turns"]);
  });

  it("lists a single turn without a header", () => {
    const summary = requestPrivacyAudit([
      redacted("req_a", 1, 1),
      record({ id: "req_b", privacy_decision: "allow" }),
      redacted("req_c", 1, 1),
    ]);
    // Calls of a protocol without turns share one group.
    expect(summary.turns).toHaveLength(1);
    const layout = privacyListLayout(summary, new Map(), "req_a");
    expect(layout.items).toEqual([
      expect.objectContaining({ kind: "call", nested: false }),
      expect.objectContaining({ kind: "call", nested: false }),
      { kind: "quiet-calls", key: "req_a", count: 1, nested: false },
    ]);
    expect(layout.turnByRecordId.size).toBe(0);
  });

  it("keeps each call's restore setting and how placeholders were explained", () => {
    const privacyEvent = (summary: string) => ({
      kind: "privacy" as const,
      started_at: base.started_at,
      ended_at: base.started_at,
      status: "succeeded" as const,
      summary,
      attempt_index: 1,
    });
    const skillCall = {
      ...redacted("req_a", 1, 0),
      events: [privacyEvent("redact · 1 · skill")],
    };
    const mixed = requestPrivacyAudit([
      skillCall,
      {
        ...redacted("req_b", 1, 1),
        events: [privacyEvent("redact · 1 · notice")],
      },
      record({ id: "req_c", privacy_decision: "allow" }),
    ]);
    const [skill, notice, plain] = mixed.calls;
    expect(skill).toMatchObject({
      restoreEnabled: true,
      restored: 0,
      explainedBy: "skill",
    });
    expect(notice.explainedBy).toBe("notice");
    expect(plain).toMatchObject({ restoreEnabled: false, explainedBy: null });
    // Only an explanation every redacted call shares belongs to the session.
    expect(mixed.explainedBy).toBeNull();
    expect(
      requestPrivacyAudit([
        skillCall,
        { ...skillCall, id: "req_b" },
        record({ id: "req_c", privacy_decision: "allow" }),
      ]).explainedBy,
    ).toBe("skill");
  });

  it("lets a block outrank a redaction in the header", () => {
    const summary = requestPrivacyAudit([
      redacted("req_a", 3, 3),
      record({ id: "req_b", privacy_decision: "block", status: "blocked" }),
    ]);
    expect(summary.outcome).toBe("blocked");
    expect(summary.blockedCalls).toBe(1);
    expect(summary.redactedCalls).toBe(1);
  });

  it("keeps every outcome named in every locale", () => {
    for (const outcome of PRIVACY_OUTCOMES) {
      for (const locale of ["en", "zh-CN", "ja"] as const) {
        const options = { lng: locale };
        expect(i18n.t(`privacyAudit.outcome.${outcome}`, options)).not.toBe(
          `privacyAudit.outcome.${outcome}`,
        );
        const headline = i18n.t(`privacyAudit.headline.${outcome}`, {
          ...options,
          count: 1,
        });
        expect(headline).not.toBe(`privacyAudit.headline.${outcome}`);
        expect(headline).not.toContain("{{");
      }
    }
  });
});
