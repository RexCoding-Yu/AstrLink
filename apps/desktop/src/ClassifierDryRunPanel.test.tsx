// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({
  classifierDryRun: vi.fn(),
  getRoutingSettings: vi.fn(),
}));
vi.mock("./bridge", () => bridge);

import { ClassifierDryRunPanel } from "./ClassifierDryRunPanel";
import { defaultFailurePolicy } from "./failure-policy-model";
import { applyLocale } from "./i18n";

function button(label: string, container: HTMLElement): HTMLButtonElement {
  const match = [...container.querySelectorAll("button")].find(
    (element) => element.textContent?.trim() === label,
  );
  if (!(match instanceof HTMLButtonElement)) {
    throw new Error(`Missing button: ${label}`);
  }
  return match;
}

async function type(
  element: HTMLTextAreaElement | HTMLInputElement,
  value: string,
): Promise<void> {
  await act(async () => {
    const prototype =
      element instanceof HTMLTextAreaElement
        ? HTMLTextAreaElement.prototype
        : HTMLInputElement.prototype;
    const setter = Object.getOwnPropertyDescriptor(prototype, "value")?.set;
    setter?.call(element, value);
    element.dispatchEvent(new Event("input", { bubbles: true }));
    await Promise.resolve();
  });
}

describe("classifier dry run panel", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(async () => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    bridge.classifierDryRun.mockReset().mockResolvedValue({
      configured: true,
      category: "coding",
      confidence: 0.9,
      fallback_reason: null,
      duration_ms: 42,
      sanitized_preview: "write a parser for [EMAIL_1]",
      decision: null,
      // 桥接层用 parseClassifierDryRunResult 解析响应，所以 `fields` 在这里
      // 永远是数组；夹具直接喂原始对象，必须自己补上这个已解析的键。
      fields: [],
      dimensions: [],
    });
    bridge.getRoutingSettings.mockReset().mockResolvedValue({
      default_failure_policy: defaultFailurePolicy(),
      allow_unmatched_failover: false,
      strategy: "retry_first",
      max_attempts: 6,
      model_routing_groups: [],
    });
    await applyLocale("zh-CN");
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  async function render(): Promise<void> {
    await act(async () => {
      root.render(<ClassifierDryRunPanel ready />);
      await Promise.resolve();
      await Promise.resolve();
    });
  }

  it("does not call the classifier until the button is pressed", async () => {
    await render();
    expect(bridge.classifierDryRun).not.toHaveBeenCalled();
  });

  it("runs the classification through the local bridge only", async () => {
    await render();
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    if (textarea === null) throw new Error("Missing textarea");
    await type(textarea, "write a parser");

    await act(async () => {
      button("试运行", container).click();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(bridge.classifierDryRun).toHaveBeenCalledWith({
      text: "write a parser",
    });
    expect(container.textContent).toContain("coding");
    expect(container.textContent).toContain("90%");
    expect(container.textContent).toContain("[EMAIL_1]");
  });

  it("passes the group id through so the production evaluator can run", async () => {
    await render();
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    if (textarea === null) throw new Error("Missing textarea");
    await type(textarea, "write a parser");
    const groupInput = container.querySelector<HTMLInputElement>(
      'input[aria-label="参考路由组（可选）"]',
    );
    if (groupInput === null) throw new Error("Missing group input");
    await type(groupInput, "group_a");

    await act(async () => {
      button("试运行", container).click();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(bridge.classifierDryRun).toHaveBeenCalledWith({
      text: "write a parser",
      group_id: "group_a",
    });
  });

  it("keeps the button disabled while the text is empty", async () => {
    await render();
    expect(button("试运行", container).disabled).toBe(true);
  });

  // The Go handler answers with member_id/service_id/model; a parser that
  // expects a single `member` throws on every group-scoped run.
  it("renders the member, its provider and its model from the wire shape", async () => {
    bridge.classifierDryRun.mockResolvedValue({
      configured: true,
      category: "coding",
      confidence: 0.91,
      fallback_reason: null,
      duration_ms: 58,
      sanitized_preview: "write a parser for [EMAIL_1]",
      decision: {
        member_id: "fast",
        service_id: "service_a8380a7",
        model: "gpt-6.1-sol",
        rule_id: "rule-1",
        used_default: false,
      },
      fields: [],
      dimensions: [],
    });
    await render();
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    if (textarea === null) throw new Error("Missing textarea");
    await type(textarea, "write a parser");

    await act(async () => {
      button("试运行", container).click();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(container.textContent).toContain("fast");
    expect(container.textContent).toContain("service_a8380a7");
    expect(container.textContent).toContain("gpt-6.1-sol");
    expect(container.textContent).toContain("rule-1");
  });

  it("still shows the result when the handler omits empty outcome fields", async () => {
    // `omitempty` drops category/confidence/preview on a skipped or failed run.
    // The panel test injects the bridge result directly, so this mirrors the
    // parsed shape the real bridge would hand over.
    bridge.classifierDryRun.mockResolvedValue({
      configured: false,
      category: "",
      confidence: null,
      duration_ms: 0,
      fallback_reason: "classifier_unavailable",
      sanitized_preview: "",
      decision: null,
      fields: [],
      dimensions: [],
    });
    await render();
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    if (textarea === null) throw new Error("Missing textarea");
    await type(textarea, "write a parser");

    await act(async () => {
      button("试运行", container).click();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(container.textContent).toContain("classifier_unavailable");
    expect(container.textContent).toContain("未知");
  });

  function withFields(fields: unknown): void {
    bridge.classifierDryRun.mockResolvedValue({
      configured: true,
      category: "coding",
      confidence: 0.9,
      fallback_reason: null,
      duration_ms: 42,
      sanitized_preview: "write a parser",
      decision: null,
      fields,
      dimensions: [],
    });
  }

  async function runOnce(): Promise<void> {
    await render();
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    if (textarea === null) throw new Error("Missing textarea");
    await type(textarea, "write a parser");
    await act(async () => {
      button("试运行", container).click();
      await Promise.resolve();
      await Promise.resolve();
    });
  }

  const candidateRows = () => [
    ...container.querySelectorAll<HTMLElement>(
      "[data-classifier-dry-run-candidate]",
    ),
  ];

  it("shows each candidate's rank, value, score and whether it won", async () => {
    withFields([
      {
        field: "task_type",
        source: "classifier",
        known: true,
        match_count: 0,
        classifier_confidence: 0.95,
        classifier_id: "cloud:jev",
        candidates: [
          { value: "coding", score: 0.95, rank: 0, chosen: false },
          { value: "research", score: 0.7, rank: 1, chosen: true },
        ],
      },
    ]);
    await runOnce();

    const rows = candidateRows();
    expect(rows.map((row) => row.dataset.candidateChosen)).toEqual([
      "false",
      "true",
    ]);
    // rank 是 0-based 的 wire 口径，界面显示 rank + 1。
    expect(rows.map((row) => row.dataset.candidateRank)).toEqual(["0", "1"]);
    expect(rows[0].textContent).toContain("1");
    expect(rows[0].textContent).toContain("coding");
    expect(rows[0].textContent).toContain("0.95");
    expect(rows[0].textContent).toContain("淘汰");
    expect(rows[1].textContent).toContain("2");
    expect(rows[1].textContent).toContain("research");
    expect(rows[1].textContent).toContain("0.70");
    expect(rows[1].textContent).toContain("采用");
  });

  it("explains that a lower-scoring candidate can be the one used", async () => {
    withFields([
      {
        field: "task_type",
        source: "classifier",
        known: true,
        match_count: 0,
        candidates: [
          { value: "coding", score: 0.95, rank: 0, chosen: false },
          { value: "research", score: 0.7, rank: 1, chosen: true },
        ],
      },
    ]);
    await runOnce();

    // 名次最高的那条写着"淘汰"、第二条写着"采用"，没有解释会被当成 bug。
    expect(
      container.querySelector('[aria-label="为什么高分候选不一定被采用"]'),
    ).not.toBeNull();
  });

  it("renders no candidate block when the run carried no fields key", async () => {
    // `omitempty`：低置信或回退场景下 Go 侧根本不带 `fields`。
    bridge.classifierDryRun.mockResolvedValue({
      configured: true,
      category: "general",
      confidence: null,
      fallback_reason: "low_confidence",
      duration_ms: 42,
      sanitized_preview: "write a parser",
      decision: null,
      fields: [],
      dimensions: [],
    });
    await runOnce();

    expect(candidateRows()).toHaveLength(0);
    // 不得伪造占位候选，也不得整段不渲染结果。
    expect(container.textContent).not.toContain("候选");
    expect(container.textContent).toContain("low_confidence");
  });

  it("renders no candidate table for a field without candidates", async () => {
    withFields([
      {
        field: "language",
        source: "guess",
        known: false,
        match_count: 0,
        candidates: [],
      },
      {
        field: "task_type",
        source: "classifier",
        known: true,
        match_count: 1,
        candidates: [{ value: "coding", score: 0.9, rank: 0, chosen: true }],
      },
    ]);
    await runOnce();

    expect(
      container.querySelector<HTMLElement>(
        "[data-classifier-dry-run-candidates]",
      )?.dataset.classifierDryRunCandidates,
    ).toBe("task_type");
    expect(
      container.querySelector(
        '[data-classifier-dry-run-candidates="language"]',
      ),
    ).toBeNull();
    expect(candidateRows()).toHaveLength(1);
  });

  it("surfaces a failed run instead of claiming a result", async () => {
    bridge.classifierDryRun.mockRejectedValue(new Error("core unreachable"));
    await render();
    const textarea = container.querySelector<HTMLTextAreaElement>("textarea");
    if (textarea === null) throw new Error("Missing textarea");
    await type(textarea, "write a parser");

    await act(async () => {
      button("试运行", container).click();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(container.textContent).toContain("core unreachable");
  });

  /**
   * The regression this table exists for: a group whose rules all test a
   * custom dimension used to show only `task_type`'s verdict, so the operator
   * could not tell what their own dimension had been classified as.
   */
  it("reports the classifier's verdict for each configured custom dimension", async () => {
    bridge.classifierDryRun.mockResolvedValue({
      configured: true,
      category: "coding",
      confidence: 0.97,
      fallback_reason: null,
      duration_ms: 42,
      sanitized_preview: "write a parser",
      decision: null,
      fields: [],
      dimensions: [
        {
          name: "branch",
          label: "后端",
          score: 0.93,
          known: true,
          produced: true,
        },
      ],
    });
    await runOnce();

    const branch = container.querySelector<HTMLElement>(
      '[data-classifier-dry-run-dimension="branch"]',
    );
    if (branch === null) throw new Error("Missing the branch dimension row");
    expect(branch.dataset.dimensionKnown).toBe("true");
    expect(branch.textContent).toContain("后端");
    expect(branch.textContent).toContain("0.93");
    // A known dimension is usable by a rule, so no caveat badge is rendered.
    expect(branch.textContent).not.toContain("低于阈值");
    expect(branch.textContent).not.toContain("未作答");
  });

  it("names why an unusable dimension had no verdict", async () => {
    bridge.classifierDryRun.mockResolvedValue({
      configured: true,
      category: "coding",
      confidence: 0.9,
      fallback_reason: null,
      duration_ms: 42,
      sanitized_preview: "write a parser",
      decision: null,
      fields: [],
      dimensions: [
        {
          name: "branch",
          label: "后端",
          score: 0.2,
          known: false,
          produced: true,
        },
        { name: "region", label: "", score: 0, known: false, produced: false },
      ],
    });
    await runOnce();

    // Skipped and below-threshold are different problems, so they read
    // differently: one needs the prompt fixed, the other the threshold.
    const weak = container.querySelector<HTMLElement>(
      '[data-classifier-dry-run-dimension="branch"]',
    );
    expect(weak?.textContent).toContain("低于阈值");
    const skipped = container.querySelector<HTMLElement>(
      '[data-classifier-dry-run-dimension="region"]',
    );
    expect(skipped?.textContent).toContain("未作答");
  });

  it("renders no dimension table when only the built-in dimension is configured", async () => {
    withFields([
      {
        field: "task_type",
        source: "classifier",
        known: true,
        match_count: 0,
        candidates: [{ value: "coding", score: 0.9, rank: 0, chosen: true }],
      },
    ]);
    await runOnce();

    expect(
      container.querySelector("[data-classifier-dry-run-dimensions]"),
    ).toBeNull();
  });
});
