import { describe, expect, it } from "vitest";

import {
  modelRoutingConditionSummary,
  modelRoutingCodeLabel,
  modelRoutingExecutionStateLabel,
  modelRoutingFieldLabel,
  modelRoutingFieldSkipLabel,
  modelRoutingPrivacyDecisionLabel,
  modelRoutingRuleSummary,
  modelRoutingSelectionSourceLabel,
  sortedFieldObservations,
} from "./request-model-routing-model";
import type {
  ModelRoutingDecision,
  ModelRoutingFieldObservation,
} from "./request-record-model";

/** A decision that hit rule `coding`, with one known and one unknown field. */
function decision(
  patch: Partial<ModelRoutingDecision> = {},
): ModelRoutingDecision {
  return {
    schema_version: 1,
    virtual_model_id: "astrlink/superclaude",
    group_id: "superclaude",
    config_snapshot_version: "abc123",
    selection_source: "rule",
    rule_id: "coding",
    rule_priority: 100,
    used_default: false,
    fields: [
      { field: "streaming", source: "local", known: true },
      {
        field: "task_type",
        source: "classifier",
        known: false,
        error_code: "classifier_timeout",
      },
    ],
    member_id: "strong",
    member_service_id: "anthropic",
    member_model: "claude-sonnet",
    execution: { status: "succeeded", attempt_count: 1 },
    privacy_attempts: [],
    duration_ms: 12,
    ...patch,
  };
}

describe("model routing decision labels", () => {
  it("names the five selection sources, which are not provider selections", () => {
    // `group_binding` is the group-scoped reuse; provider `session_binding` is
    // a different namespace and is not a valid value here.
    for (const source of [
      "rule",
      "default_member",
      "group_binding",
      "response_affinity",
      "websocket_connection",
    ] as const) {
      const label = modelRoutingSelectionSourceLabel(source);
      expect(label).toBeTruthy();
      expect(label).not.toBe(source);
    }
  });

  it("labels execution states and privacy decisions", () => {
    for (const state of [
      "pending",
      "selected",
      "succeeded",
      "failed",
      "cancelled",
    ] as const) {
      expect(modelRoutingExecutionStateLabel(state)).not.toBe(state);
    }
    for (const value of ["none", "allow", "reject", "unknown"] as const) {
      expect(modelRoutingPrivacyDecisionLabel(value)).not.toBe(value);
    }
  });

  it("keeps an unknown code readable instead of dropping it", () => {
    expect(modelRoutingCodeLabel("brand_new_code")).toBe("brand_new_code");
    expect(modelRoutingFieldLabel("some_new_field")).toBe("some_new_field");
  });

  it("summarises a rule hit versus a default member", () => {
    const hit = modelRoutingRuleSummary(decision());
    expect(hit).toContain("coding");
    expect(hit).toContain("100");
    const fallback = modelRoutingRuleSummary(
      decision({
        selection_source: "default_member",
        used_default: true,
        rule_id: undefined,
        rule_priority: undefined,
      }),
    );
    expect(fallback).toBeTruthy();
    expect(fallback).not.toContain("coding");
  });

  it("sorts known field observations ahead of unknown ones", () => {
    const sorted = sortedFieldObservations(decision());
    expect(sorted.map((item) => item.known)).toEqual([true, false]);
    expect(sorted[0].field).toBe("streaming");
  });

  it("explains a missing value by the field's own source", () => {
    const observations: ModelRoutingFieldObservation[] = [
      { field: "task_type", source: "classifier", known: false },
      { field: "protocol", source: "local", known: false },
    ];
    const [classifier, local] = observations.map(modelRoutingFieldSkipLabel);
    expect(classifier).toBeTruthy();
    expect(local).toBeTruthy();
    // The two polarities read differently: a classifier gap is not a parse gap.
    expect(classifier).not.toBe(local);
  });

  it("prefers the recorded skip reason when the core gave one", () => {
    expect(
      modelRoutingFieldSkipLabel({
        field: "task_type",
        source: "classifier",
        known: false,
        skip_reason: "classifier_busy",
      }),
    ).toBe("classifier_busy");
  });
});

describe("model routing condition summaries", () => {
  it("describes each leaf shape without echoing matched content", () => {
    expect(
      modelRoutingConditionSummary({ field: "task_type", in: ["coding"] }),
    ).toContain("coding");
    expect(
      modelRoutingConditionSummary({ field: "streaming", equals_bool: true }),
    ).toBeTruthy();
    expect(
      modelRoutingConditionSummary({ field: "turn_count", gte: 2, lte: 8 }),
    ).toContain("8");
    expect(
      modelRoutingConditionSummary({
        field: "literal_keyword",
        contains_any: ["x"],
      }),
    ).toBeTruthy();
    expect(
      modelRoutingConditionSummary({
        all: [{ field: "protocol", in: ["openai"] }],
      }),
    ).toBeTruthy();
    expect(
      modelRoutingConditionSummary({
        not: { field: "streaming", equals_bool: false },
      }),
    ).toBeTruthy();
  });
});
