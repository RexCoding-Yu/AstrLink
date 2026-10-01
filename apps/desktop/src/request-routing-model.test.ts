import { describe, expect, it } from "vitest";

import type { RequestRecord } from "./request-record-model";
import {
  readableRouteRows,
  routeEventStep,
  routeRowSummary,
  routingSteps,
} from "./request-routing-model";
import type { TrajectoryRow } from "./request-trajectory-model";

const at = "2026-10-01T14:08:24Z";

function routed(
  summary: string,
  status: "succeeded" | "failed",
): RequestRecord["events"][number] {
  return {
    kind: "routed",
    started_at: at,
    ended_at: at,
    status,
    summary,
    attempt_index: 0,
  };
}

const services = {
  service_codex: { id: "service_codex", name: "Codex 订阅" },
  service_kimi: { id: "service_kimi", name: "Kimi Coding" },
  service_newapi: { id: "service_newapi", name: "new-api" },
  service_spark: { id: "service_spark", name: "Spark" },
};

// Every provider is out and the one left had its circuit open: Core writes the
// open circuit both as a skip and as a rejected route event.
const unavailable: Pick<
  RequestRecord,
  "events" | "routing_decision" | "service_id"
> = {
  service_id: null,
  events: [routed("service_newapi · circuit_open", "failed")],
  routing_decision: {
    skipped: [
      { service_id: "service_codex", reason: "disabled" },
      { service_id: "service_kimi", reason: "model_not_listed" },
      { service_id: "service_newapi", reason: "circuit_open" },
      { service_id: "service_spark", reason: "model_not_listed" },
      { service_id: "service_gone", reason: "disabled" },
    ],
  },
};

describe("routeEventStep", () => {
  it("reads the provider Core used and the ones it refused", () => {
    expect(routeEventStep(routed("relaykit · service_a", "succeeded"))).toEqual(
      { serviceId: "service_a", outcome: "selected", code: "relaykit" },
    );
    expect(
      routeEventStep(routed("service_b · credential_unavailable", "failed")),
    ).toEqual({
      serviceId: "service_b",
      outcome: "rejected",
      code: "credential_unavailable",
    });
    // Records from before route events carry only the provider.
    expect(routeEventStep(routed("service_c", "succeeded"))).toEqual({
      serviceId: "service_c",
      outcome: "selected",
      code: "",
    });
    expect(routeEventStep(routed("", "failed"))).toBeNull();
  });
});

describe("routingSteps", () => {
  it("lists skipped providers in priority order and folds a rejection into its skip", () => {
    // Providers without the model were never candidates.
    expect(
      routingSteps(unavailable.events, unavailable.routing_decision).map(
        (step) => [step.serviceId, step.outcome, step.code],
      ),
    ).toEqual([
      ["service_codex", "skipped", "disabled"],
      ["service_newapi", "rejected", "circuit_open"],
      ["service_gone", "skipped", "disabled"],
    ]);
  });

  it("ends with the provider routing used", () => {
    expect(
      routingSteps(
        [
          routed("service_kimi · credential_unavailable", "failed"),
          routed("native · service_newapi", "succeeded"),
        ],
        {
          selected: "failover",
          skipped: [{ service_id: "service_codex", reason: "disabled" }],
        },
      ).map((step) => [step.serviceId, step.outcome]),
    ).toEqual([
      ["service_codex", "skipped"],
      ["service_kimi", "rejected"],
      ["service_newapi", "selected"],
    ]);
  });
});

describe("routeRowSummary", () => {
  it("names the refused provider in words and groups the rest by reason", () => {
    expect(
      routeRowSummary(unavailable.events[0]!, unavailable, services, true),
    ).toBe("new-api · 连续失败，暂停使用中 · 另跳过 2 个：已停用 ×2");
    // Only the call's last route row carries the skipped providers.
    expect(
      routeRowSummary(unavailable.events[0]!, unavailable, services, false),
    ).toBe("new-api · 连续失败，暂停使用中");
  });

  it("says why the provider was used and mentions only a conversion", () => {
    const record: Pick<
      RequestRecord,
      "events" | "routing_decision" | "service_id"
    > = {
      service_id: "service_newapi",
      events: [routed("relaykit · service_newapi", "succeeded")],
      routing_decision: {
        selected: "priority",
        skipped: [{ service_id: "service_codex", reason: "disabled" }],
      },
    };
    expect(routeRowSummary(record.events[0]!, record, services, true)).toBe(
      "new-api · 按优先级 · 本地转换协议 · 跳过 1 个：已停用",
    );
    const native = routed("native · service_newapi", "succeeded");
    expect(
      routeRowSummary(
        native,
        { ...record, events: [native], routing_decision: undefined },
        services,
        true,
      ),
    ).toBe("new-api");
  });

  it("stands a call no provider could take on the decision alone", () => {
    expect(
      routeRowSummary(
        { summary: "", status: "failed" },
        { ...unavailable, events: [] },
        services,
        true,
      ),
    ).toBe("无可用 API 提供商 · 跳过 3 个：已停用 ×2、连续失败，暂停使用中");
  });

  it("says so when no provider lists the model", () => {
    expect(
      routeRowSummary(
        { summary: "", status: "failed" },
        {
          service_id: null,
          events: [],
          routing_decision: {
            skipped: [
              { service_id: "service_kimi", reason: "model_not_listed" },
              { service_id: "service_spark", reason: "model_not_listed" },
            ],
          },
        },
        services,
        true,
      ),
    ).toBe("没有 API 提供商列出该模型");
  });
});

describe("readableRouteRows", () => {
  it("rewrites only route rows, and only the last one of a call lists skips", () => {
    const row = (id: string, chip: TrajectoryRow["chip"], summary: string) =>
      ({
        id,
        requestId: "req_1",
        chip,
        summary,
        result: "",
        status: chip === "ROUTE" ? "failed" : "succeeded",
        tone: "ok",
        startedAt: at,
        endedAt: at,
        lane: "gateway",
        child: false,
        turnIndex: null,
      }) satisfies TrajectoryRow;
    const record = {
      ...unavailable,
      events: [
        routed("service_kimi · credential_unavailable", "failed"),
        routed("service_newapi · circuit_open", "failed"),
      ],
    } as RequestRecord;
    expect(
      readableRouteRows(
        [
          row("a", "CLIENT", "gpt-5 · openai.responses"),
          row("b", "ROUTE", "service_kimi · credential_unavailable"),
          row("c", "ROUTE", "service_newapi · circuit_open"),
        ],
        new Map([["req_1", record]]),
        services,
      ).map((item) => item.summary),
    ).toEqual([
      "gpt-5 · openai.responses",
      "Kimi Coding · 凭据不可用",
      "new-api · 连续失败，暂停使用中 · 另跳过 2 个：已停用 ×2",
    ]);
  });
});
