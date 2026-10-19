// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  listPlugins: vi.fn(),
  reloadPlugins: vi.fn(),
  getPlugin: vi.fn(),
  getPluginStatus: vi.fn(),
  updatePlugin: vi.fn(),
  testPlugin: vi.fn(),
  savePluginCredential: vi.fn(),
  deletePluginCredential: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

import { PluginManager } from "./PluginManager";
import type { PluginDetail, PluginList } from "./plugin-model";
import {
  finishExitAnimations,
  installDialogAnimations,
} from "./lib/test-dialog-animations";

const memory: PluginDetail = {
  key: "powercontext",
  name: "PowerContext 记忆",
  version: "1.0.0",
  source: "builtin",
  file: "plugin.ts",
  enabled: false,
  state: "loaded",
  requested_capabilities: ["context.inject", "network", "storage"],
  granted_capabilities: [],
  needs_network: true,
  network_purpose: "访问 PowerContext 服务器",
  notice: "启用后，新会话的前几轮会请模型询问项目。",
  hooks: ["describeStatus", "testConnection"],
  config: { serverUrl: "http://192.0.2.1:9090" },
  network_origins: [],
  privacy_trusted: false,
  credentials: [
    { name: "token", label: "PowerContext 令牌", configured: false },
  ],
  metadata: [
    {
      key: "project",
      label: "项目",
      pattern: "^[a-z]+$",
      marker: "memory-project",
    },
  ],
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
    bridgeMocks.getPlugin.mockImplementation(async (key: string) =>
      key === memory.key
        ? memory
        : {
            ...list.plugins[1],
            hooks: [],
            config: {},
            network_origins: [],
            privacy_trusted: false,
            credentials: [],
            metadata: [],
          },
    );
    bridgeMocks.getPluginStatus.mockResolvedValue({
      calls: 4,
      failures: { timeout: 1 },
      memory_limits: 0,
      timeouts: 1,
      network: { requests: 3, denied: 1, errors: 0, wait_ms: 20 },
      view: [
        {
          type: "kv",
          items: [{ label: "Scope", value: "scp_7f3a", mono: true }],
        },
      ],
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
    expect(rows[0]?.textContent).toContain("需要网络");
    expect(rows[1]?.textContent).toContain("plugin.ts 第 12 行 · 不支持 async");
    // The first plugin opens with its overview, as the plugin renders it.
    expect(
      document.querySelector('[data-testid="plugin-overview"]')?.textContent,
    ).toContain("scp_7f3a");
    await click(rows[1] as HTMLElement);
    expect(document.body.textContent).toContain(
      "async functions are not supported",
    );
    expect(document.querySelector('[role="tab"]')).toBeNull();
  });

  it("grants only the capabilities left ticked when enabling", async () => {
    bridgeMocks.updatePlugin.mockResolvedValue({
      ...memory,
      enabled: true,
      granted_capabilities: ["context.inject", "network"],
    });
    await render();
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
      granted_capabilities: ["context.inject", "network"],
    });
    expect(
      document.querySelector('[data-testid="plugin-row"]')?.textContent,
    ).toContain("运行中");
  });

  it("asks for consent to plain HTTP beyond this machine", async () => {
    bridgeMocks.updatePlugin.mockImplementation(
      async (
        _key: string,
        update: {
          network_origins: Array<{ origin: string; allow_plaintext: boolean }>;
        },
      ) => ({
        ...memory,
        network_origins: update.network_origins.map((origin) => ({
          ...origin,
          plaintext: true,
        })),
      }),
    );
    await render();
    await openTab("网络与凭据");
    const input = document.querySelector<HTMLInputElement>(
      'input[aria-label="要允许的地址"]',
    );
    if (!input) throw new Error("Missing the origin input");
    await type(input, "ftp://nope");
    await click(button("添加"));
    expect(document.body.textContent).toContain(
      "请输入带主机的 http 或 https 地址",
    );
    await type(input, "http://192.0.2.1:9090");
    await click(button("添加"));
    const origin = document.querySelector('[data-testid="plugin-origin"]');
    expect(origin?.textContent).toContain("明文 HTTP");
    await click(origin?.querySelector('[role="checkbox"]') as HTMLElement);
    await click(button("保存地址"));
    expect(bridgeMocks.updatePlugin).toHaveBeenCalledWith("powercontext", {
      network_origins: [
        { origin: "http://192.0.2.1:9090", allow_plaintext: true },
      ],
    });
  });

  it("keeps the configuration a JSON object", async () => {
    bridgeMocks.updatePlugin.mockResolvedValue({
      ...memory,
      config: { serverUrl: "https://memory.example" },
    });
    await render();
    await openTab("配置");
    const editor = document.querySelector<HTMLTextAreaElement>(
      '[data-testid="plugin-config"] textarea',
    );
    if (!editor) throw new Error("Missing the configuration editor");
    await type(editor, "[1, 2]");
    await click(button("保存配置"));
    expect(document.body.textContent).toContain("配置必须是一个 JSON 对象。");
    expect(bridgeMocks.updatePlugin).not.toHaveBeenCalled();
    await type(editor, '{"serverUrl":"https://memory.example"}');
    await click(button("保存配置"));
    expect(bridgeMocks.updatePlugin).toHaveBeenCalledWith("powercontext", {
      config: { serverUrl: "https://memory.example" },
    });
  });

  it("shows what the connection test returned", async () => {
    bridgeMocks.testPlugin.mockResolvedValue({
      status: "ok",
      summary: "PowerContext 1.3.0 · 412 ms",
      view: [
        {
          type: "kv",
          items: [{ label: "可检索", value: "atomic-memory / topic-memory" }],
        },
      ],
      elapsed_ms: 412,
      network: { requests: 2, denied: 0, errors: 0, wait_ms: 400 },
    });
    await render();
    expect(bridgeMocks.getPluginStatus).toHaveBeenCalledTimes(1);
    await click(button("测试连接"));
    const result = document.querySelector('[data-testid="plugin-test-result"]');
    expect(result?.textContent).toContain("连接正常");
    expect(result?.textContent).toContain("atomic-memory / topic-memory");
    // The overview and the counts may have changed with the test.
    expect(bridgeMocks.getPluginStatus).toHaveBeenCalledTimes(2);
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
