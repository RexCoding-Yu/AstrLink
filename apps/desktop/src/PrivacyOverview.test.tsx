// @vitest-environment happy-dom

import {
  act,
  cloneElement,
  isValidElement,
  type ReactElement,
  type ReactNode,
} from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("recharts", async (importOriginal) => {
  const actual = await importOriginal<typeof import("recharts")>();
  return {
    ...actual,
    // happy-dom never lays out, so give the chart a fixed plot box.
    ResponsiveContainer: ({ children }: { children?: ReactNode }) => (
      <div data-slot="chart-frame" style={{ width: 320, height: 160 }}>
        {isValidElement(children)
          ? cloneElement(
              children as ReactElement<{ width?: number; height?: number }>,
              { width: 320, height: 160 },
            )
          : children}
      </div>
    ),
  };
});

const bridge = vi.hoisted(() => ({ getPrivacySummary: vi.fn() }));
vi.mock("./bridge", () => bridge);

import { applyLocale } from "./i18n";
import { PrivacyOverview } from "./PrivacyOverview";
import {
  emptyPrivacySummary,
  emptyPrivacyTotals,
  type PrivacySummary,
  type PrivacyTotals,
} from "./privacy-summary-model";
import type { UsageRangePreset, UsageWindow } from "./usage-range";

const busy: PrivacyTotals = {
  ...emptyPrivacyTotals(),
  requests: 20,
  allowed: 10,
  warned: 1,
  redacted: 5,
  blocked: 2,
  fail_open: 1,
  replaced: 12,
  restored: 7,
  restored_visible: 5,
  restored_tool_arguments: 2,
  unrestored: 1,
};

function summaryFor(
  window: UsageWindow,
  totals: PrivacyTotals,
  kinds: PrivacySummary["by_kind"] = [],
): PrivacySummary {
  const summary = emptyPrivacySummary(window);
  summary.totals = totals;
  summary.by_day[summary.by_day.length - 1] = {
    ...summary.by_day[summary.by_day.length - 1],
    ...totals,
  };
  summary.by_kind = kinds;
  return summary;
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((onResolve, onReject) => {
    resolve = onResolve;
    reject = onReject;
  });
  return { promise, resolve, reject };
}

describe("PrivacyOverview", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(async () => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    await applyLocale("zh-CN");
    bridge.getPrivacySummary.mockReset();
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  async function render({
    active = true,
    enabled = true,
    preset = "7d",
  }: {
    active?: boolean;
    enabled?: boolean;
    preset?: UsageRangePreset;
  } = {}) {
    await act(async () => {
      root.render(
        <PrivacyOverview
          active={active}
          coreSessionKey="session-1"
          enabled={enabled}
          isReady
          preset={preset}
        />,
      );
      await Promise.resolve();
    });
    await act(async () => {
      await Promise.resolve();
    });
  }

  it("draws the totals, the kinds found and how calls ended", async () => {
    bridge.getPrivacySummary.mockImplementation(async (window: UsageWindow) =>
      summaryFor(window, busy, [
        { kind: "email", count: 6 },
        { kind: "common_secret", count: 3 },
        { kind: "phone", count: 1 },
      ]),
    );
    await render();

    expect(bridge.getPrivacySummary.mock.calls[0][0].preset).toBe("7d");
    const metrics = container.querySelector('[data-slot="privacy-metrics"]')!;
    const tiles = [...metrics.querySelectorAll("dt")].map((term) => [
      term.textContent,
      term.nextElementSibling?.textContent,
    ]);
    expect(tiles).toEqual([
      ["已检查请求", "18"],
      ["发现隐私内容", "8"],
      ["已替换", "12"],
      ["已还原", "7"],
    ]);
    expect(metrics.textContent).toContain("占已检查的 44%");
    expect(metrics.textContent).toContain("正文 5 · 工具参数 2");
    expect(
      metrics.querySelector('[data-slot="privacy-unrestored"]')?.textContent,
    ).toBe("1 段回复留着替身值");

    expect(
      container.querySelector('[role="img"][aria-label="替换与还原趋势"]'),
    ).not.toBeNull();

    const kinds = container.querySelector('[data-slot="privacy-kinds"]')!;
    expect(
      [...kinds.querySelectorAll('[role="progressbar"]')].map((bar) =>
        bar.getAttribute("aria-label"),
      ),
    ).toEqual(["邮箱", "常见密钥", "电话"]);
    expect(kinds.textContent).toContain("60%");

    const outcomes = container.querySelector('[data-slot="privacy-outcomes"]')!;
    expect(outcomes.textContent).toContain("共 19 次");
    expect(
      [...outcomes.querySelectorAll("[data-outcome]")].map((item) =>
        item.getAttribute("data-outcome"),
      ),
    ).toEqual(["redacted", "blocked", "warned", "fail_open", "allowed"]);
    expect(
      [...outcomes.querySelectorAll("[data-segment]")].map((segment) =>
        segment.getAttribute("data-segment"),
      ),
    ).toEqual(["redacted", "blocked", "warned", "fail_open", "allowed"]);
    expect(
      outcomes.querySelector('[data-slot="privacy-unprotected"]')?.textContent,
    ).toBe("另有 1 次请求发出时隐私保护已关闭，模型收到的是原文。");
    expect(outcomes.querySelector('[data-slot="privacy-trusted"]')).toBeNull();
    expect(container.querySelector('[data-slot="privacy-ignored"]')).toBeNull();
  });

  it("counts calls to trusted providers apart from protection being off", async () => {
    bridge.getPrivacySummary.mockImplementation(async (window: UsageWindow) =>
      summaryFor(window, { ...busy, requests: 23, trusted: 3 }),
    );
    await render();
    const outcomes = container.querySelector('[data-slot="privacy-outcomes"]')!;
    expect(outcomes.textContent).toContain("共 19 次");
    expect(
      outcomes.querySelector('[data-slot="privacy-unprotected"]')?.textContent,
    ).toBe("另有 1 次请求发出时隐私保护已关闭，模型收到的是原文。");
    expect(
      outcomes.querySelector('[data-slot="privacy-trusted"]')?.textContent,
    ).toBe("另有 3 次请求发往信任的提供商，未经隐私检查，提供商收到的是原文。");

    await act(async () => root.unmount());
    root = createRoot(container);
    bridge.getPrivacySummary.mockImplementation(async (window: UsageWindow) =>
      summaryFor(window, { ...emptyPrivacyTotals(), requests: 2, trusted: 2 }),
    );
    await render();
    expect(container.textContent).toContain("这段时间没有请求经过隐私检查");
    expect(container.textContent).toContain(
      "这段时间有 2 次请求发往信任的提供商，未经隐私检查，提供商收到的是原文。",
    );
    expect(container.textContent).not.toContain("隐私保护已关闭");
  });

  it("counts ignored calls apart and names the matches left in place", async () => {
    const ignoredBusy: PrivacyTotals = {
      ...busy,
      allowed: 7,
      ignored: 3,
      ignored_allowlisted: 1200,
      ignored_low_confidence: 4,
    };
    bridge.getPrivacySummary.mockImplementation(async (window: UsageWindow) =>
      summaryFor(window, ignoredBusy, [{ kind: "email", count: 6 }]),
    );
    await render();

    const metrics = container.querySelector('[data-slot="privacy-metrics"]')!;
    expect(metrics.querySelector("dd")?.textContent).toBe("18");
    const outcomes = container.querySelector('[data-slot="privacy-outcomes"]')!;
    expect(
      [...outcomes.querySelectorAll("[data-outcome]")].map((item) =>
        item.getAttribute("data-outcome"),
      ),
    ).toEqual([
      "redacted",
      "blocked",
      "warned",
      "fail_open",
      "ignored",
      "allowed",
    ]);
    const ignored = outcomes.querySelector('[data-outcome="ignored"]')!;
    expect(ignored.textContent).toContain("已忽略");
    expect(ignored.textContent).toContain(
      "发现的内容命中免脱敏名单或低于置信度门槛，请求按原文发给了模型",
    );
    expect(
      container.querySelector('[data-slot="privacy-ignored"]')?.textContent,
    ).toBe(
      "另有 1,204 处已忽略，模型收到的是原文：1,200 处命中免脱敏名单，4 处低于置信度门槛。",
    );

    await act(async () => root.unmount());
    root = createRoot(container);
    bridge.getPrivacySummary.mockImplementation(async (window: UsageWindow) =>
      summaryFor(window, { ...ignoredBusy, ignored_low_confidence: 0 }),
    );
    await render();
    expect(
      container.querySelector('[data-slot="privacy-ignored"]')?.textContent,
    ).toBe("另有 1,200 处命中免脱敏名单，已忽略，模型收到的是原文。");

    await act(async () => root.unmount());
    root = createRoot(container);
    await applyLocale("en");
    bridge.getPrivacySummary.mockImplementation(async (window: UsageWindow) =>
      summaryFor(window, {
        ...ignoredBusy,
        ignored_allowlisted: 0,
        ignored_low_confidence: 1,
      }),
    );
    await render();
    expect(
      container.querySelector('[data-slot="privacy-ignored"]')?.textContent,
    ).toBe(
      "1 more match was below the confidence threshold and ignored. The model received it as written.",
    );
  });

  it("explains an empty range by what was switched on", async () => {
    bridge.getPrivacySummary.mockImplementation(async (window: UsageWindow) =>
      emptyPrivacySummary(window),
    );
    await render({ enabled: false });
    expect(container.textContent).toContain("隐私保护已关闭");
    expect(container.textContent).toContain(
      "开启后，这里会显示替换、还原和阻止了多少隐私内容。",
    );
    expect(container.querySelector('[data-slot="privacy-metrics"]')).toBeNull();

    await act(async () => root.unmount());
    root = createRoot(container);
    bridge.getPrivacySummary.mockImplementation(async (window: UsageWindow) =>
      summaryFor(window, { ...emptyPrivacyTotals(), requests: 5 }),
    );
    await render({ enabled: true });
    expect(container.textContent).toContain("这段时间没有请求经过隐私检查");
    expect(container.textContent).toContain(
      "这段时间有 5 次请求发出时隐私保护已关闭，模型收到的是原文。",
    );
  });

  it("dims the old range while a new one loads and retries a failed read", async () => {
    bridge.getPrivacySummary.mockImplementation(async (window: UsageWindow) =>
      summaryFor(window, busy),
    );
    await render({ preset: "7d" });
    const pending = deferred<PrivacySummary>();
    bridge.getPrivacySummary.mockReturnValueOnce(pending.promise);
    await render({ preset: "30d" });
    const content = container.querySelector(
      '[data-slot="privacy-overview-content"]',
    )!;
    expect(content.getAttribute("aria-busy")).toBe("true");
    expect(content.className).toContain("opacity-60");
    expect(container.textContent).toContain("12");

    const window = bridge.getPrivacySummary.mock.calls.at(-1)![0];
    expect(window.preset).toBe("30d");
    await act(async () => {
      pending.resolve(summaryFor(window, { ...busy, replaced: 40 }));
      await Promise.resolve();
    });
    expect(content.getAttribute("aria-busy")).toBeNull();
    expect(container.textContent).toContain("40");

    await act(async () => root.unmount());
    root = createRoot(container);
    bridge.getPrivacySummary.mockRejectedValueOnce(new Error("control down"));
    await render();
    expect(container.textContent).toContain("读取隐私保护统计失败。");
    expect(container.textContent).toContain("control down");
    bridge.getPrivacySummary.mockImplementation(async (next: UsageWindow) =>
      summaryFor(next, busy),
    );
    const retry = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "重试",
    )!;
    await act(async () => {
      retry.click();
      await Promise.resolve();
    });
    await act(async () => {
      await Promise.resolve();
    });
    expect(
      container.querySelector('[data-slot="privacy-metrics"]'),
    ).not.toBeNull();
  });

  it("stays idle while its tab is hidden", async () => {
    bridge.getPrivacySummary.mockImplementation(async (window: UsageWindow) =>
      summaryFor(window, busy),
    );
    await render({ active: false });
    expect(bridge.getPrivacySummary).not.toHaveBeenCalled();
    await render({ active: true });
    expect(bridge.getPrivacySummary).toHaveBeenCalledTimes(1);
  });
});
