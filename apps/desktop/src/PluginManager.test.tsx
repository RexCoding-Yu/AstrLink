// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  listPlugins: vi.fn(),
  reloadPlugins: vi.fn(),
  getPlugin: vi.fn(),
  getPluginStatus: vi.fn(),
  getPluginStats: vi.fn(),
  getPluginCreditStars: vi.fn(),
  updatePlugin: vi.fn(),
  testPlugin: vi.fn(),
  savePluginCredential: vi.fn(),
  deletePluginCredential: vi.fn(),
  getPluginHostSettings: vi.fn(),
  updatePluginHostSettings: vi.fn(),
  clearPluginData: vi.fn(),
  getPluginJournal: vi.fn(),
  dryRunPlugin: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

import { PluginManager } from "./PluginManager";
import type {
  PluginDayStats,
  PluginDetail,
  PluginList,
  PluginStats,
} from "./plugin-model";
import {
  finishExitAnimations,
  installDialogAnimations,
} from "./lib/test-dialog-animations";
import { settleClosedLayer } from "./lib/test-layer-settle";

const memory: PluginDetail = {
  key: "powercontext",
  name: "PowerContext 记忆",
  version: "1.0.0",
  source: "builtin",
  file: "plugin.ts",
  enabled: false,
  state: "loaded",
  requested_capabilities: [
    "context.inject",
    "request.read",
    "network",
    "storage",
    "ui.views",
  ],
  granted_capabilities: [],
  needs_network: true,
  network_purpose: "访问 PowerContext 服务器",
  notice: "启用后，新会话的前几轮会请模型询问项目。",
  hooks: [
    "prepareSession",
    "prepareTurn",
    "observeTurn",
    "describeStatus",
    "testConnection",
  ],
  config: {
    serverUrl: "http://192.0.2.1:9090",
    quotas: { "atomic-memory": 4 },
  },
  network_origins: [
    {
      origin: "http://192.0.2.1:9090",
      allow_plaintext: true,
      plaintext: true,
      setting: "serverUrl",
    },
  ],
  privacy_trusted: false,
  turn_wait_ms: 2500,
  credentials: [
    {
      name: "token",
      label: "PowerContext 令牌",
      configured: true,
      hint: "abcd",
      origin: "http://192.0.2.1:9090",
    },
  ],
  metadata: [
    {
      key: "project",
      label: "项目",
      pattern: "^[a-z]+$",
      marker: "memory-project",
    },
  ],
  settings: [
    {
      key: "serverUrl",
      label: "服务器地址",
      type: "server",
      group: "连接",
      required: true,
      credential: "token",
    },
    {
      key: "quotas.atomic-memory",
      label: "原子记忆",
      type: "number",
      group: "召回",
      row: "每轮条数",
      default: 4,
      min: 0,
      max: 20,
      unit: "条",
    },
    {
      key: "quotas.topic-memory",
      label: "主题记忆",
      type: "number",
      group: "召回",
      row: "每轮条数",
      default: 3,
      min: 0,
      max: 20,
      unit: "条",
    },
    { key: "$wait", group: "召回" },
    {
      key: "askProject",
      label: "没有项目时询问",
      type: "switch",
      group: "会话",
      default: true,
      help: "会话还没有项目时附上的询问提示词",
    },
    { key: "$trusted", group: "隐私" },
    {
      key: "minQueryChars",
      label: "最短问题",
      type: "number",
      advanced: true,
      default: 8,
      min: 0,
      max: 200,
      unit: "字",
    },
    { key: "$origins", advanced: true },
  ],
  credits: [
    {
      name: "PowerContext",
      owner: "OceanBase",
      license: "Apache-2.0",
      url: "https://github.com/oceanbase/powercontext",
    },
  ],
  excluded_clients: [],
  workspace_binding: true,
  auto_refresh: false,
  lookup_enabled: true,
  tools: [],
  storage: [
    {
      label: "项目在服务器上的编号",
      content: false,
      retention: "一直保留，可在插件页清除",
    },
  ],
  host_storage: [
    { kind: "session_bindings" },
    { kind: "workspace_bindings", count: 2 },
    { kind: "plugin_sessions", count: 5 },
    { kind: "request_records" },
    { kind: "resident_block" },
    { kind: "plugin_values", count: 3 },
  ],
};

const status = {
  calls: 4,
  failures: { timeout: 1 },
  memory_limits: 0,
  timeouts: 1,
  network: { requests: 3, denied: 1, errors: 0, wait_ms: 20 },
  view: [
    {
      type: "kv" as const,
      items: [{ label: "最近写入", value: "astrlink：已写入 · 已整理成记忆" }],
    },
  ],
  last_test: {
    status: "ok" as const,
    summary: "PowerContext 1.3.0 · 190 ms",
    elapsed_ms: 190,
    network: { requests: 2, denied: 0, errors: 0, wait_ms: 180 },
    view: [
      {
        type: "kv" as const,
        items: [{ label: "可检索", value: "原子记忆 / 主题记忆" }],
      },
    ],
    tested_at: "2026-10-19T08:00:00Z",
  },
  bindings: [
    {
      key: "project",
      label: "项目",
      values: [
        {
          value: "astrlink",
          sessions: 2,
          last_used_at: "2026-10-19T07:58:00Z",
        },
        { value: "none", sessions: 1 },
      ],
    },
  ],
};

function day(date: string, change: Partial<PluginDayStats> = {}) {
  const empty: PluginDayStats = {
    date,
    load: { count: 0, by_trigger: {}, injected_bytes: 0, requests: 0 },
    lookup: {
      ran: 0,
      refused: 0,
      counted: 0,
      with_hits: 0,
      sessions: 0,
      looking_up: 0,
    },
    binding: {
      workspace: 0,
      picked: 0,
      created: 0,
      declined: 0,
      unanswered: 0,
      changed: 0,
    },
    write: { ok: 0, failed: 0, reported: 0 },
    problems: { failed: 0, timeouts: 0, privacy: 0, over_budget: 0 },
    cache: {
      with: { requests: 0, input_tokens: 0, cache_read_tokens: 0 },
      without: { requests: 0, input_tokens: 0, cache_read_tokens: 0 },
    },
  };
  return { ...empty, ...change };
}

const busyDay = day("2026-10-21", {
  load: {
    count: 4,
    by_trigger: { start: 2, bound: 1, compact: 1 },
    injected_bytes: 61440,
    requests: 12,
    wait_ms: { p50: 310, p95: 1200 },
  },
  binding: {
    workspace: 2,
    picked: 1,
    created: 1,
    declined: 0,
    unanswered: 1,
    changed: 1,
  },
  write: { ok: 9, failed: 1, reported: 2 },
  problems: { failed: 1, timeouts: 2, privacy: 0, over_budget: 0 },
  cache: {
    with: { requests: 10, input_tokens: 100000, cache_read_tokens: 82000 },
    without: { requests: 5, input_tokens: 50000, cache_read_tokens: 30000 },
  },
});

const stats: PluginStats = {
  days: [
    ...[15, 16, 17, 18, 19, 20].map((date) => day(`2026-10-${date}`)),
    busyDay,
  ],
  total: { ...busyDay, date: undefined },
};

const list: PluginList = {
  directory: "/data/plugins",
  plugins: [
    memory,
    {
      key: "team-rules",
      name: "team-rules",
      version: "",
      source: "directory",
      file: "plugin.ts",
      enabled: false,
      state: "failed",
      error: {
        code: "async",
        file: "plugin.ts",
        line: 12,
        column: 3,
        message: "async functions are not supported",
      },
      requested_capabilities: [],
      granted_capabilities: [],
      needs_network: false,
    },
  ],
};

function button(label: string, root: ParentNode = document): HTMLButtonElement {
  const match = [...root.querySelectorAll("button")].find(
    (candidate) => candidate.textContent?.trim() === label,
  );
  if (!(match instanceof HTMLButtonElement))
    throw new Error(`Missing button: ${label}`);
  return match;
}

async function click(element: HTMLElement) {
  await act(async () => {
    element.click();
  });
}

async function openTab(label: string) {
  const tab = [
    ...document.querySelectorAll<HTMLButtonElement>('[role="tab"]'),
  ].find((candidate) => candidate.textContent?.trim() === label);
  if (!tab) throw new Error(`Missing tab: ${label}`);
  await act(async () => {
    tab.dispatchEvent(
      new MouseEvent("mousedown", { bubbles: true, button: 0 }),
    );
  });
}

async function chooseOption(selector: string, option: string) {
  const trigger = document.querySelector<HTMLButtonElement>(selector);
  if (trigger === null) throw new Error(`Missing select trigger: ${selector}`);
  await act(async () => {
    trigger.dispatchEvent(
      new PointerEvent("pointerdown", {
        bubbles: true,
        button: 0,
        pointerType: "mouse",
      }),
    );
    await Promise.resolve();
  });
  const item = [
    ...document.querySelectorAll<HTMLElement>('[role="option"]'),
  ].find((candidate) => candidate.textContent?.trim() === option);
  if (!item) throw new Error(`Missing select option: ${option}`);
  await act(async () => {
    item.click();
    await Promise.resolve();
  });
}

/** The control a setting row's label names. */
function control<T extends HTMLElement>(label: string): T {
  const name = [...document.querySelectorAll("label")].find(
    (candidate) => candidate.textContent?.trim() === label,
  );
  const target = name?.htmlFor ? document.getElementById(name.htmlFor) : null;
  if (!target) throw new Error(`Missing the control of ${label}`);
  return target as T;
}

async function type(
  element: HTMLInputElement | HTMLTextAreaElement,
  value: string,
) {
  const prototype =
    element instanceof HTMLTextAreaElement
      ? HTMLTextAreaElement.prototype
      : HTMLInputElement.prototype;
  const setter = Object.getOwnPropertyDescriptor(prototype, "value")?.set;
  await act(async () => {
    setter?.call(element, value);
    element.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

describe("PluginManager", () => {
  let container: HTMLDivElement;
  let reactRoot: Root;
  let removeAnimations: () => void;

  beforeEach(() => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    removeAnimations = installDialogAnimations();
    bridgeMocks.listPlugins.mockResolvedValue(list);
    bridgeMocks.getPluginCreditStars.mockResolvedValue({
      "https://github.com/oceanbase/powercontext": 1367,
    });
    bridgeMocks.getPlugin.mockImplementation(async (key: string) =>
      key === memory.key
        ? memory
        : {
            ...list.plugins[1],
            hooks: [],
            config: {},
            network_origins: [],
            privacy_trusted: false,
            turn_wait_ms: 2500,
            credentials: [],
            metadata: [],
            settings: [],
            credits: [],
            excluded_clients: [],
            workspace_binding: true,
            auto_refresh: false,
            lookup_enabled: true,
            tools: [],
            storage: [],
            host_storage: [],
          },
    );
    bridgeMocks.getPluginStatus.mockResolvedValue(status);
    bridgeMocks.getPluginStats.mockResolvedValue(stats);
    bridgeMocks.getPluginHostSettings.mockResolvedValue({
      paused: false,
      injection_budget_bytes: 16384,
      language: "zh-CN",
    });
    container = document.createElement("div");
    document.body.append(container);
    reactRoot = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => reactRoot.unmount());
    removeAnimations();
    container.remove();
  });

  async function render() {
    await act(async () => {
      reactRoot.render(<PluginManager coreSessionKey="session-1" isReady />);
    });
  }

  it("says the gateway is not running instead of loading forever", async () => {
    await act(async () => {
      reactRoot.render(
        <PluginManager coreSessionKey="session-1" isReady={false} />,
      );
    });
    expect(bridgeMocks.listPlugins).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain("网关没有运行");
    expect(document.body.textContent).not.toContain("正在加载插件");
    // Once the gateway is ready the list loads; when it stops again the list
    // stays, marked as the last one read.
    await act(async () => {
      reactRoot.render(<PluginManager coreSessionKey="session-1" isReady />);
    });
    expect(
      document.querySelectorAll('[data-testid="plugin-row"]'),
    ).toHaveLength(2);
    await act(async () => {
      reactRoot.render(
        <PluginManager coreSessionKey="session-1" isReady={false} />,
      );
    });
    expect(
      document.querySelectorAll('[data-testid="plugin-row"]'),
    ).toHaveLength(2);
    expect(document.body.textContent).toContain(
      "网关尚未就绪，当前显示上次读取的插件。",
    );
  });

  it("lists plugins and shows why one failed to load", async () => {
    await render();
    const rows = document.querySelectorAll('[data-testid="plugin-row"]');
    expect(rows).toHaveLength(2);
    expect(rows[0]?.textContent).toContain("PowerContext 记忆");
    expect(rows[0]?.textContent).not.toContain("内置");
    expect(rows[1]?.textContent).toContain(
      "plugin.ts 第 12 行 · async 需要 apiVersion 2",
    );
    // The title says where the plugin comes from, never its key.
    expect(document.body.textContent).toContain("内置 · 1.0.0");
    expect(document.body.textContent).not.toContain("powercontext · 1.0.0");
    await click(rows[1] as HTMLElement);
    expect(document.body.textContent).toContain(
      "async functions are not supported",
    );
    expect(document.querySelector('[role="tab"]')).toBeNull();
  });

  it("shows the connection, the bound projects, its own status and its credits", async () => {
    await render();
    const connection = document.querySelector(
      '[data-testid="plugin-connection"]',
    );
    // The plugin joined to AstrLink, switched off, with its server under it
    // and the last test on the link.
    const link = connection?.querySelector('[data-testid="plugin-link"]');
    expect(link?.getAttribute("data-state")).toBe("off");
    expect(link?.textContent).toContain("AstrLink本机网关已停用");
    expect(link?.textContent).toContain("PowerContext 1.3.0 · 190 ms");
    expect(link?.textContent).toContain(
      "PowerContext 记忆192.0.2.1:9090明文 HTTP",
    );
    expect(connection?.textContent).toContain("原子记忆 / 主题记忆");
    const bindings = document.querySelector('[data-testid="plugin-bindings"]');
    expect(bindings?.textContent).toContain("已绑定的项目");
    expect(bindings?.textContent).toContain("astrlink");
    expect(bindings?.textContent).toContain("不纳入");
    expect(
      document.querySelector('[data-testid="plugin-own-status"]')?.textContent,
    ).toContain("已整理成记忆");
    // The last 7 days as a tile for each part the plugin has, with its days
    // as small columns: no lookups here, so no tile for them.
    const effects = document.querySelector('[data-testid="plugin-effects"]');
    const tiles = [
      ...(effects?.querySelectorAll('[data-testid="plugin-effect-tile"]') ??
        []),
    ].map((tile) => tile.textContent);
    expect(tiles).toEqual([
      "装载消耗约 2.05万 token装载 4 次 · 等待约 310 ms · 刷新 1 次",
      "写入9 条Agent 上报 2 条 · 失败 1 条",
      "问题3失败 1 · 超时 2",
    ]);
    const sparklines = effects?.querySelectorAll('[data-slot="sparkline"]');
    expect(sparklines).toHaveLength(3);
    const columns = sparklines?.[0]?.querySelectorAll("[data-value]") ?? [];
    expect(columns).toHaveLength(7);
    // Today, on the right, carries the load.
    expect(columns[6]?.getAttribute("data-value")).toBe("20480");
    expect(columns[0]?.getAttribute("data-value")).toBe("0");
    // How sessions got their project, only the ways that happened.
    const ways = document.querySelector(
      '[data-testid="plugin-effect-bindings"]',
    );
    expect(ways?.textContent).toBe(
      "绑定方式按工作区自动2选了已有的1新建1没回答1自动绑定后改掉 1 次",
    );
    expect(
      document.querySelector('[data-testid="plugin-effect-cache"]')
        ?.textContent,
    ).toBe("缓存命中开了记忆82%其他会话60%");
    expect(effects?.textContent).not.toContain("查询");
    // The projects it is built on: a line, with GitHub's stars and mark.
    const credits = document.querySelector('[data-testid="plugin-credits"]');
    expect(credits?.textContent).toContain("使用了开源项目 PowerContext");
    expect(
      credits?.querySelector('[data-slot="github-repo-badge"]')?.textContent,
    ).toContain("1,367");
    expect(credits?.textContent).not.toContain("官方");
    const repository = credits?.querySelector("a");
    expect(repository?.getAttribute("href")).toBe(
      "https://github.com/oceanbase/powercontext",
    );
    expect(repository?.getAttribute("aria-label")).toBe(
      "在 GitHub 上查看 PowerContext，1,367 颗星",
    );
    expect(repository?.getAttribute("title")).toBe(
      "oceanbase/powercontext · Apache-2.0",
    );
    expect(bridgeMocks.getPluginCreditStars).toHaveBeenCalledWith(
      "powercontext",
    );
    // What the plugin does stays behind the ? by its name.
    expect(document.body.textContent).not.toContain(
      "启用后，新会话的前几轮会请模型询问项目。",
    );
    await click(
      document.querySelector(
        'button[aria-label="关于这个插件"]',
      ) as HTMLElement,
    );
    const about = document.querySelector('[data-testid="plugin-about"]');
    expect(about?.textContent).toContain(
      "启用后，新会话的前几轮会请模型询问项目。",
    );
    expect(about?.textContent).toContain("界面视图");
  });

  it("says when the last 7 days have no records, and shows lookups where there are", async () => {
    bridgeMocks.getPluginStats.mockResolvedValueOnce({
      days: [day("2026-10-21")],
      total: day(""),
    });
    await render();
    expect(
      document.querySelector('[data-testid="plugin-effects"]')?.textContent,
    ).toContain("最近 7 天还没有记录。");
    bridgeMocks.getPlugin.mockResolvedValue({
      ...memory,
      tools: [{ name: "search", description: "检索本项目的记忆。" }],
    });
    bridgeMocks.getPluginStats.mockResolvedValueOnce({
      ...stats,
      total: {
        ...stats.total,
        lookup: {
          ran: 8,
          refused: 1,
          counted: 8,
          with_hits: 6,
          ms: { p50: 640, p95: 2100 },
          sessions: 5,
          looking_up: 2,
        },
      },
    });
    bridgeMocks.reloadPlugins.mockResolvedValue(list);
    await click(button("重新扫描"));
    expect(
      document.querySelector('[data-testid="plugin-effects"]')?.textContent,
    ).toContain("查询8 次命中 75% · 2 个会话用过 · 拒绝 1 次");
  });

  it("lists the account day by day on the status page, today first", async () => {
    await render();
    await openTab("运行状态");
    const daily = document.querySelector(
      '[data-testid="plugin-daily-effects"]',
    );
    const rows = daily?.querySelectorAll("tbody tr") ?? [];
    expect(rows).toHaveLength(7);
    expect(rows[0]?.textContent).toContain("2026-10-21");
    expect(rows[0]?.textContent).toContain("60.0 KB");
    expect(rows[0]?.textContent).toContain("9（失败 1）");
    expect(rows[0]?.textContent).toContain("82% / 60%");
    expect(rows[6]?.textContent).toContain("2026-10-15");
    const head = daily?.querySelector("thead")?.textContent;
    expect(head).toContain("消耗");
    expect(head).not.toContain("查询");
    // Days without activity stay, greyed.
    expect(rows[0]?.getAttribute("data-active")).toBe("true");
    expect(rows[1]?.getAttribute("data-active")).toBe("false");
    expect(rows[1]?.className).toContain("text-muted-foreground");
  });

  it("shows hook timings, the log and a dry run on the develop tab", async () => {
    bridgeMocks.getPlugin.mockResolvedValue({
      ...memory,
      development: {
        project: "/home/me/powercontext",
        built_at: "2026-10-23T12:00:00Z",
      },
    });
    bridgeMocks.getPluginJournal.mockResolvedValue({
      entries: [
        {
          seq: 1,
          time: "2026-10-23T12:00:01Z",
          hook: "prepareTurn",
          level: "info",
          message: "recalled 3 memories",
        },
        {
          seq: 2,
          time: "2026-10-23T12:00:02Z",
          hook: "observeTurn",
          level: "error",
          message: "threw TypeError: x is undefined",
          reason: "plugin_error",
          stack:
            "TypeError: x is undefined\n    at observeTurn (src/plugin.ts:40:3)",
        },
      ],
      timings: [
        {
          hook: "prepareTurn",
          calls: 12,
          failures: 1,
          p50_ms: 42.5,
          p95_ms: 310,
          max_ms: 1250,
          script_ms: 3.2,
        },
      ],
      latest: 2,
    });
    bridgeMocks.dryRunPlugin.mockResolvedValue({
      result: [{ type: "text", text: "ok" }],
      elapsed_ms: 12.3,
      script_ms: 1.5,
      logs: [
        {
          seq: 0,
          time: "2026-10-23T12:00:03Z",
          hook: "describeStatus",
          level: "info",
          message: "status read",
          dry_run: true,
        },
      ],
    });
    await render();
    // A development build says so instead of where it came from.
    expect(document.body.textContent).toContain("开发版 · 1.0.0");
    await openTab("开发");
    expect(bridgeMocks.getPluginJournal).toHaveBeenCalledWith(
      "powercontext",
      0,
    );
    const develop = document.querySelector('[data-testid="plugin-develop"]');
    expect(
      develop?.querySelector('[data-testid="plugin-development"]')?.textContent,
    ).toContain("powercontext");
    const timings = develop?.querySelector('[data-testid="plugin-timings"]');
    expect(timings?.textContent).toContain("prepareTurn");
    expect(timings?.textContent).toContain("43 ms");
    expect(timings?.textContent).toContain("310 ms");
    expect(timings?.textContent).toContain("1.25 s");
    const log = develop?.querySelector('[data-testid="plugin-log"]');
    const lines = log?.querySelectorAll("li") ?? [];
    // Newest first, failures with their stack.
    expect(lines[0]?.textContent).toContain("错误");
    expect(lines[0]?.textContent).toContain("src/plugin.ts:40:3");
    expect(lines[1]?.textContent).toContain("recalled 3 memories");

    const run = develop?.querySelector('[data-testid="plugin-dry-run"]');
    const args = run?.querySelector("textarea") as HTMLTextAreaElement;
    expect(JSON.parse(args.value)).toEqual([{ language: "zh-CN" }]);
    await click(button("运行", run as ParentNode));
    expect(bridgeMocks.dryRunPlugin).toHaveBeenCalledWith(
      "powercontext",
      "describeStatus",
      [{ language: "zh-CN" }],
    );
    const outcome = run?.querySelector(
      '[data-testid="plugin-dry-run-outcome"]',
    );
    expect(outcome?.textContent).toContain('"text": "ok"');
    expect(outcome?.textContent).toContain("status read");
  });

  it("grants only the capabilities left ticked when enabling", async () => {
    bridgeMocks.updatePlugin.mockResolvedValue({
      ...memory,
      enabled: true,
      granted_capabilities: [
        "context.inject",
        "request.read",
        "network",
        "ui.views",
      ],
    });
    await render();
    await openTab("设置");
    await click(button("启用"));
    const summary = document.querySelector(
      '[data-testid="plugin-enable-summary"]',
    );
    expect(summary?.textContent).toContain(
      "启用后，新会话的前几轮会请模型询问项目。",
    );
    expect(summary?.textContent).toContain("会话标记 memory-project");
    const storage = [
      ...document.querySelectorAll<HTMLButtonElement>('[role="checkbox"]'),
    ].find((box) => box.parentElement?.textContent?.includes("插件存储"));
    if (!storage) throw new Error("Missing the storage capability");
    await click(storage);
    await click(
      button(
        "启用",
        document.querySelector('[role="alertdialog"]') as HTMLElement,
      ),
    );
    await finishExitAnimations();
    expect(bridgeMocks.updatePlugin).toHaveBeenCalledWith("powercontext", {
      enabled: true,
      granted_capabilities: [
        "context.inject",
        "request.read",
        "network",
        "ui.views",
      ],
    });
    expect(
      document.querySelector('[data-testid="plugin-row"]')?.textContent,
    ).toContain("运行中");
    // Enabling brings the overview back, the plugin joined to AstrLink.
    const link = document.querySelector('[data-testid="plugin-link"]');
    expect(link).not.toBeNull();
    expect(link?.getAttribute("data-state")).not.toBe("off");
  });

  it("offers a row of switches as one choice of several", async () => {
    bridgeMocks.getPlugin.mockResolvedValue({
      ...memory,
      settings: [
        ...memory.settings,
        ...["atomic", "topics", "skills"].map((key, index) => ({
          key: `scopes.${key}`,
          label: ["原子记忆", "主题", "技能"][index],
          type: "switch" as const,
          group: "查询",
          row: "查询范围",
          default: true,
        })),
      ],
    });
    bridgeMocks.updatePlugin.mockResolvedValue(memory);
    await render();
    await openTab("设置");
    const scopes = document.querySelector<HTMLButtonElement>(
      'button[aria-label="查询范围"]',
    );
    if (!scopes) throw new Error("Missing the scope choice");
    expect(scopes.textContent).toBe("全部");
    // No switch of its own stays on the row.
    expect(
      scopes
        .closest('[data-slot="setting-row"]')
        ?.querySelector('[role="switch"]'),
    ).toBeNull();
    await act(async () => {
      scopes.dispatchEvent(
        new PointerEvent("pointerdown", { bubbles: true, button: 0 }),
      );
      scopes.click();
    });
    // Three options need no search.
    expect(
      document.querySelector('input[aria-label="查询范围 search"]'),
    ).toBeNull();
    const skills = document.querySelector<HTMLButtonElement>(
      'button[role="checkbox"][aria-label="技能"]',
    );
    if (!skills) throw new Error("Missing the skills option");
    await click(skills);
    expect(scopes.textContent).toBe("原子记忆、主题");
    await act(async () => {
      document.dispatchEvent(
        new KeyboardEvent("keydown", { bubbles: true, key: "Escape" }),
      );
    });
    await settleClosedLayer();
    await click(button("保存"));
    expect(bridgeMocks.updatePlugin).toHaveBeenCalledWith(
      "powercontext",
      expect.objectContaining({
        config: expect.objectContaining({ scopes: { skills: false } }),
      }),
    );
  });

  it("keeps the help of the header in one ?", async () => {
    await render();
    const controls = document.querySelector(
      '[data-testid="plugin-host-controls"]',
    );
    expect(controls?.querySelector("button[aria-label^='关于']")).toBeNull();
    const help = document.querySelector<HTMLButtonElement>(
      'button[aria-label="关于插件的总设置"]',
    );
    if (!help) throw new Error("Missing the header help");
    await click(help);
    for (const part of [
      "暂停期间所有插件都不运行",
      "所有插件往一次请求里注入的内容合计",
      "每个插件在这里有自己的文件夹",
    ])
      expect(document.body.textContent).toContain(part);
  });

  it("saves the settings form as one configuration", async () => {
    bridgeMocks.updatePlugin.mockResolvedValue(memory);
    await render();
    await openTab("设置");
    const form = document.querySelector('[data-testid="plugin-settings"]');
    expect(form?.textContent).toContain("每轮条数");
    expect(control<HTMLInputElement>("主题记忆").value).toBe("3");
    expect(button("保存").disabled).toBe(true);
    await type(control<HTMLInputElement>("原子记忆"), "25");
    await click(button("保存"));
    expect(document.body.textContent).toContain(
      "「每轮条数 · 原子记忆」要在 0 到 20 之间",
    );
    expect(bridgeMocks.updatePlugin).not.toHaveBeenCalled();
    await type(control<HTMLInputElement>("原子记忆"), "6");
    await click(control<HTMLButtonElement>("没有项目时询问"));
    expect(document.body.textContent).toContain("有改动未保存");
    await click(button("保存"));
    // Untouched defaults stay out; the consented server keeps its consent.
    expect(bridgeMocks.updatePlugin).toHaveBeenCalledWith("powercontext", {
      config: {
        serverUrl: "http://192.0.2.1:9090",
        quotas: { "atomic-memory": 6 },
        askProject: false,
      },
      plaintext_consent: ["http://192.0.2.1:9090"],
    });
  });

  it("asks again for plain HTTP when the server moves", async () => {
    bridgeMocks.updatePlugin.mockResolvedValue(memory);
    await render();
    await openTab("设置");
    await type(
      control<HTMLInputElement>("服务器地址"),
      "http://192.0.2.9:9090",
    );
    await click(button("保存"));
    expect(document.body.textContent).toContain(
      "「服务器地址」是明文 HTTP 地址，勾选「允许明文 HTTP」才能保存",
    );
    expect(bridgeMocks.updatePlugin).not.toHaveBeenCalled();
    const consent = [
      ...document.querySelectorAll<HTMLButtonElement>('[role="checkbox"]'),
    ].find((box) => box.parentElement?.textContent?.includes("允许明文 HTTP"));
    if (!consent) throw new Error("Missing the plain HTTP consent");
    await click(consent);
    await click(button("保存"));
    expect(bridgeMocks.updatePlugin).toHaveBeenCalledWith("powercontext", {
      config: {
        serverUrl: "http://192.0.2.9:9090",
        quotas: { "atomic-memory": 4 },
      },
      plaintext_consent: ["http://192.0.2.9:9090"],
    });
  });

  it("takes an origin allowed before the form as the server's", async () => {
    // A server allowed on the old network tab has no setting mark.
    bridgeMocks.getPlugin.mockResolvedValue({
      ...memory,
      network_origins: [
        {
          origin: "http://192.0.2.1:9090",
          allow_plaintext: true,
          plaintext: true,
        },
      ],
    });
    bridgeMocks.updatePlugin.mockResolvedValue(memory);
    await render();
    await openTab("设置");
    await click(button("更多设置"));
    expect(
      document.querySelectorAll('[data-testid="plugin-origin"]'),
    ).toHaveLength(0);
    const consent = [
      ...document.querySelectorAll<HTMLButtonElement>('[role="checkbox"]'),
    ].find((box) => box.parentElement?.textContent?.includes("允许明文 HTTP"));
    expect(consent?.getAttribute("aria-checked")).toBe("true");
    await type(control<HTMLInputElement>("原子记忆"), "5");
    await click(button("保存"));
    expect(bridgeMocks.updatePlugin).toHaveBeenCalledWith("powercontext", {
      config: {
        serverUrl: "http://192.0.2.1:9090",
        quotas: { "atomic-memory": 5 },
      },
      plaintext_consent: ["http://192.0.2.1:9090"],
    });
  });

  it("sends a new token to its server", async () => {
    bridgeMocks.savePluginCredential.mockResolvedValue(memory);
    await render();
    await openTab("设置");
    expect(document.body.textContent).toContain("已配置 ···· abcd");
    await click(button("更换"));
    await type(control<HTMLInputElement>("PowerContext 令牌"), "pc-new-token");
    await click(button("保存"));
    expect(bridgeMocks.updatePlugin).not.toHaveBeenCalled();
    expect(bridgeMocks.savePluginCredential).toHaveBeenCalledWith(
      "powercontext",
      "token",
      { secret: "pc-new-token", origin: "http://192.0.2.1:9090" },
    );
  });

  it("edits the same configuration as JSON", async () => {
    bridgeMocks.updatePlugin.mockResolvedValue(memory);
    await render();
    await openTab("设置");
    await click(button("更多设置"));
    expect(control<HTMLInputElement>("最短问题").value).toBe("8");
    await click(button("以 JSON 编辑"));
    const editor = document.querySelector<HTMLTextAreaElement>(
      '[role="dialog"] textarea',
    );
    if (!editor) throw new Error("Missing the JSON editor");
    expect(JSON.parse(editor.value)).toEqual(memory.config);
    await type(editor, "[1, 2]");
    await click(button("应用"));
    expect(document.body.textContent).toContain("配置必须是一个 JSON 对象。");
    await type(
      editor,
      '{"serverUrl":"http://192.0.2.1:9090","quotas":{"atomic-memory":7},"extra":true}',
    );
    await click(button("应用"));
    await finishExitAnimations();
    expect(control<HTMLInputElement>("原子记忆").value).toBe("7");
    await click(button("保存"));
    expect(bridgeMocks.updatePlugin).toHaveBeenCalledWith("powercontext", {
      config: {
        serverUrl: "http://192.0.2.1:9090",
        quotas: { "atomic-memory": 7 },
        extra: true,
      },
      plaintext_consent: ["http://192.0.2.1:9090"],
    });
  });

  it("pauses every plugin and sets their injection budget from the header", async () => {
    bridgeMocks.updatePluginHostSettings.mockImplementation(
      async (update: Record<string, unknown>) => ({
        paused: false,
        injection_budget_bytes: 16384,
        language: "zh-CN",
        ...update,
      }),
    );
    await render();
    const controls = document.querySelector(
      '[data-testid="plugin-host-controls"]',
    );
    expect(controls?.textContent).toContain("16 KB");
    expect(document.body.textContent).not.toContain("所有插件已暂停");
    await click(
      document.querySelector(
        'button[aria-label="暂停所有插件"]',
      ) as HTMLElement,
    );
    expect(bridgeMocks.updatePluginHostSettings).toHaveBeenCalledWith({
      paused: true,
    });
    expect(document.body.textContent).toContain(
      "所有插件已暂停，请求原样发出。",
    );
    await chooseOption("#plugin-host-budget", "32 KB");
    await settleClosedLayer();
    expect(bridgeMocks.updatePluginHostSettings).toHaveBeenLastCalledWith({
      injection_budget_bytes: 32768,
    });
  });

  it("voids the lookup credentials it lists only after asking", async () => {
    bridgeMocks.getPlugin.mockResolvedValue({
      ...memory,
      host_storage: [
        ...memory.host_storage,
        { kind: "lookup_credentials", count: 2 },
      ],
    });
    bridgeMocks.clearPluginData.mockResolvedValue(2);
    await render();
    const item = [
      ...document.querySelectorAll('[data-testid="plugin-stored-item"]'),
    ].find((entry) => entry.textContent?.includes("查询凭证")) as HTMLElement;
    // Grouped where they are kept.
    expect(
      item.closest('[data-testid="plugin-stored-place"]')?.textContent,
    ).toContain("只在内存里");
    expect(item.textContent).toContain("2 条");
    await click(button("作废", item));
    expect(bridgeMocks.clearPluginData).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain("作废查询凭证？");
    expect(document.body.textContent).toContain("提示词缓存要重写一次");
    await click(
      button(
        "作废",
        document.querySelector('[role="alertdialog"]') as HTMLElement,
      ),
    );
    await finishExitAnimations();
    expect(bridgeMocks.clearPluginData).toHaveBeenCalledWith(
      "powercontext",
      "lookups",
    );
  });

  it("lists the plugin's lookups behind the switch that turns them off", async () => {
    bridgeMocks.getPlugin.mockResolvedValue({
      ...memory,
      tools: [
        { name: "search", description: "按一句话检索本项目的记忆。" },
        {
          name: "projects",
          description: "按关键词列出项目。",
          unbound: true,
        },
        {
          name: "note",
          description: "把值得记住的项目信息记进记忆。",
          writes: true,
        },
      ],
      settings: [...memory.settings, { key: "$lookup", group: "查询" }],
    });
    bridgeMocks.updatePlugin.mockResolvedValue(memory);
    await render();
    await openTab("设置");
    const lookup = control<HTMLButtonElement>("允许 Agent 查询");
    expect(lookup.getAttribute("aria-checked")).toBe("true");
    const help = document.querySelector<HTMLButtonElement>(
      'button[aria-label="关于允许 Agent 查询"]',
    );
    if (!help) throw new Error("Missing the lookup help");
    await click(help);
    expect(document.body.textContent).toContain(
      "search：按一句话检索本项目的记忆。",
    );
    expect(document.body.textContent).toContain(
      "note（上报）：把值得记住的项目信息记进记忆。",
    );
    await act(async () => {
      document.dispatchEvent(
        new KeyboardEvent("keydown", { bubbles: true, key: "Escape" }),
      );
    });
    await settleClosedLayer();
    await click(lookup);
    await click(button("保存"));
    expect(bridgeMocks.updatePlugin).toHaveBeenCalledWith("powercontext", {
      lookup_enabled: false,
    });
  });

  it("offers no lookup switch for a plugin without lookups", async () => {
    await render();
    await openTab("设置");
    expect(document.body.textContent).not.toContain("允许 Agent 查询");
  });

  it("lists what is kept for the plugin and clears it after asking", async () => {
    bridgeMocks.clearPluginData.mockResolvedValue(2);
    await render();
    const stored = document.querySelector('[data-testid="plugin-stored"]');
    expect(stored?.textContent).toContain("工作区和值的对应");
    expect(stored?.textContent).toContain("2 条");
    expect(stored?.textContent).toContain("当前装载块");
    // A column for each place, each thing with how much and its button;
    // only what holds memory text says so.
    const places = [
      ...document.querySelectorAll('[data-testid="plugin-stored-place"]'),
    ].map((place) => place.querySelector("h4")?.textContent);
    expect(places).toEqual(["AstrLink 数据库", "只在内存里", "请求记录"]);
    const block = [
      ...document.querySelectorAll('[data-testid="plugin-stored-item"]'),
    ].find((item) => item.textContent?.includes("当前装载块"));
    expect(block?.textContent).toBe("当前装载块含记忆内容");
    // What each one is, how long it stays and what the plugin declares
    // all wait behind the card's one ?.
    expect(stored?.querySelectorAll("button[aria-label^='关于']")).toHaveLength(
      1,
    );
    expect(stored?.textContent).not.toContain("网关重启就没了");
    await click(
      stored?.querySelector('button[aria-label="关于数据存储"]') as HTMLElement,
    );
    const help = document.querySelector('[data-testid="plugin-stored-help"]');
    expect(help?.textContent).toContain(
      "当前装载块会话系统提示词里正在用的那份项目记忆。保留：网关重启就没了。",
    );
    expect(help?.textContent).toContain(
      "项目在服务器上的编号（一直保留，可在插件页清除）",
    );
    await act(async () => {
      document.dispatchEvent(
        new KeyboardEvent("keydown", { bubbles: true, key: "Escape" }),
      );
    });
    await settleClosedLayer();
    const items = [
      ...document.querySelectorAll('[data-testid="plugin-stored-item"]'),
    ];
    const workspaces = items.find((item) =>
      item.textContent?.includes("工作区和值的对应"),
    ) as HTMLElement;
    await click(button("清除", workspaces));
    expect(bridgeMocks.clearPluginData).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain("清除工作区记录？");
    await click(
      button(
        "清除",
        document.querySelector('[role="alertdialog"]') as HTMLElement,
      ),
    );
    await finishExitAnimations();
    expect(bridgeMocks.clearPluginData).toHaveBeenCalledWith(
      "powercontext",
      "workspaces",
    );
    // The counts come back from Core afresh.
    expect(bridgeMocks.getPlugin).toHaveBeenCalledTimes(2);
  });

  it("asks before automatic refresh and saves the host's switches", async () => {
    bridgeMocks.updatePlugin.mockResolvedValue(memory);
    await render();
    await openTab("设置");
    expect(
      control<HTMLButtonElement>("按工作区自动绑定").getAttribute(
        "aria-checked",
      ),
    ).toBe("true");
    await click(control<HTMLButtonElement>("自动刷新"));
    expect(document.body.textContent).toContain("打开自动刷新？");
    await click(
      button(
        "打开",
        document.querySelector('[role="alertdialog"]') as HTMLElement,
      ),
    );
    await finishExitAnimations();
    expect(
      control<HTMLButtonElement>("自动刷新").getAttribute("aria-checked"),
    ).toBe("true");
    await click(control<HTMLButtonElement>("按工作区自动绑定"));
    const clients = document.querySelector<HTMLButtonElement>(
      'button[aria-label="对哪些客户端生效"]',
    );
    expect(clients?.textContent).toContain("全部客户端");
    await act(async () => {
      clients?.dispatchEvent(
        new PointerEvent("pointerdown", { bubbles: true, button: 0 }),
      );
      clients?.click();
    });
    const cursor = document.querySelector<HTMLButtonElement>(
      'button[role="checkbox"][aria-label="Cursor"]',
    );
    if (!cursor) throw new Error("Missing the Cursor option");
    await click(cursor);
    await act(async () => {
      document.dispatchEvent(
        new KeyboardEvent("keydown", { bubbles: true, key: "Escape" }),
      );
    });
    await settleClosedLayer();
    await click(button("保存"));
    expect(bridgeMocks.updatePlugin).toHaveBeenCalledWith("powercontext", {
      excluded_clients: ["cursor"],
      workspace_binding: false,
      auto_refresh: true,
    });
  });

  it("lets the user choose how long a request waits for the plugin", async () => {
    bridgeMocks.updatePlugin.mockResolvedValue({
      ...memory,
      turn_wait_ms: 3500,
    });
    await render();
    await openTab("设置");
    const wait = control<HTMLButtonElement>("最长等待");
    expect(wait.textContent).toContain("2.5 秒");
    await chooseOption(`#${CSS.escape(wait.id)}`, "3.5 秒");
    await settleClosedLayer();
    await click(button("保存"));
    expect(bridgeMocks.updatePlugin).toHaveBeenCalledWith("powercontext", {
      turn_wait_ms: 3500,
    });
  });

  it("offers no wait for a plugin that adds nothing to requests", async () => {
    bridgeMocks.getPlugin.mockResolvedValue({
      ...memory,
      hooks: ["observeTurn", "testConnection"],
      settings: [],
    });
    await render();
    await openTab("设置");
    expect(document.body.textContent).not.toContain("最长等待");
    // Without settings of its own the gateway's still show.
    expect(document.body.textContent).toContain("受信任");
  });

  it("shows the connection test it ran", async () => {
    bridgeMocks.getPlugin.mockResolvedValue({ ...memory, enabled: true });
    bridgeMocks.getPluginStatus.mockResolvedValueOnce({
      ...status,
      last_test: undefined,
    });
    bridgeMocks.testPlugin.mockResolvedValue(status.last_test);
    await render();
    const link = () => document.querySelector('[data-testid="plugin-link"]');
    expect(link()?.getAttribute("data-state")).toBe("idle");
    expect(link()?.textContent).toContain("还没测试");
    await click(button("测试连接"));
    expect(bridgeMocks.testPlugin).toHaveBeenCalledWith("powercontext");
    // The overview reads the test back with what it changed in the counts.
    expect(bridgeMocks.getPluginStatus).toHaveBeenCalledTimes(2);
    expect(link()?.getAttribute("data-state")).toBe("connected");
    expect(link()?.textContent).toContain("连接正常");
  });

  it("keeps the plugin's own status with the connection, once each", async () => {
    bridgeMocks.getPluginStatus.mockResolvedValueOnce({
      ...status,
      view: [
        {
          type: "kv" as const,
          items: [
            { label: "可检索", value: "原子记忆" },
            { label: "最近写入", value: "astrlink：已写入" },
          ],
        },
      ],
    });
    await render();
    const connection = document.querySelector(
      '[data-testid="plugin-connection"]',
    );
    // A row the status repeats shows once, as the status says it now.
    expect(connection?.textContent?.match(/可检索/g)).toHaveLength(1);
    expect(connection?.textContent).toContain(
      "可检索原子记忆最近写入astrlink：已写入",
    );
  });

  it("says the plugin's status could not be read, and why behind a ?", async () => {
    bridgeMocks.getPluginStatus.mockResolvedValueOnce({
      ...status,
      view: undefined,
      view_error: "threw TypeError: boom",
    });
    await render();
    expect(
      document.querySelector('[data-testid="plugin-status-error"]')
        ?.textContent,
    ).toBe("没读出来");
    expect(document.body.textContent).not.toContain("threw TypeError");
    await click(
      document.querySelector(
        'button[aria-label="插件返回的错误"]',
      ) as HTMLElement,
    );
    expect(document.body.textContent).toContain("threw TypeError: boom");
  });

  it("opens the first plugin a rescan finds", async () => {
    bridgeMocks.listPlugins.mockResolvedValue({
      directory: "/data/plugins",
      plugins: [],
    });
    bridgeMocks.reloadPlugins.mockResolvedValue(list);
    await render();
    expect(document.body.textContent).toContain("还没有插件");
    await click(button("重新扫描"));
    expect(bridgeMocks.getPlugin).toHaveBeenCalledWith("powercontext");
    expect(
      document.querySelector('[data-testid="plugin-overview"]'),
    ).not.toBeNull();
  });

  it("says the overview needs the views grant", async () => {
    bridgeMocks.getPluginStatus.mockResolvedValue({
      calls: 0,
      failures: {},
      memory_limits: 0,
      timeouts: 0,
      network: { requests: 0, denied: 0, errors: 0, wait_ms: 0 },
    });
    await render();
    expect(
      document.querySelector('[data-testid="plugin-overview"]')?.textContent,
    ).toContain("启用时授予「界面视图」，这里才会显示插件提供的概览。");
  });
});
