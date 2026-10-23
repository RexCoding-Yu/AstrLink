// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridgeMocks = vi.hoisted(() => ({
  getKnowledgeEnvironment: vi.fn(),
  runKnowledgeAction: vi.fn(),
  checkKnowledgeRuntime: vi.fn(),
  checkKnowledgeModel: vi.fn(),
  pickKnowledgePath: vi.fn(),
}));

vi.mock("./bridge", () => bridgeMocks);

import { KnowledgeBase } from "./KnowledgeBase";
import type {
  KnowledgeEmbeddingModel,
  KnowledgeEnvironment,
  KnowledgeRuntimeStatus,
} from "./knowledge-model";
import {
  finishExitAnimations,
  installDialogAnimations,
} from "./lib/test-dialog-animations";

const granite: KnowledgeEmbeddingModel = {
  id: "granite-embedding-107m-multilingual",
  display_name: "Granite Embedding 107M Multilingual",
  repo_id: "ibm-granite/granite-embedding-107m-multilingual",
  revision: "d6cffd338414d6a1c1f5decfad5fec62eebc90d5",
  license: "Apache-2.0",
  dimensions: 384,
  download_bytes: 437184350,
  state: "not_installed",
  loaded: false,
};

function environment(
  runtime: Partial<KnowledgeRuntimeStatus>,
  model: Partial<KnowledgeEmbeddingModel> = {},
): KnowledgeEnvironment {
  return {
    runtime: {
      state: "not_installed",
      version: "1.4.0.0-r1",
      platform: "linux-amd64",
      download_bytes: 53574813,
      data_bytes: 0,
      ...runtime,
    },
    embedding_models: [{ ...granite, ...model }],
  };
}

async function click(element: Element | undefined | null) {
  if (!element) throw new Error("missing element");
  await act(async () => {
    (element as HTMLElement).click();
  });
}

function button(label: string): HTMLButtonElement | undefined {
  return [...document.querySelectorAll<HTMLButtonElement>("button")].find(
    (candidate) => candidate.textContent?.includes(label),
  );
}

describe("KnowledgeBase", () => {
  let container: HTMLDivElement;
  let reactRoot: Root;
  let removeAnimations: () => void;

  beforeEach(() => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    removeAnimations = installDialogAnimations();
    container = document.createElement("div");
    document.body.append(container);
    reactRoot = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => reactRoot.unmount());
    removeAnimations();
    container.remove();
  });

  async function render(isReady = true) {
    await act(async () => {
      reactRoot.render(
        <KnowledgeBase coreSessionKey="session-1" isReady={isReady} />,
      );
    });
  }

  it("waits for the gateway before reading the environment", async () => {
    await render(false);
    expect(bridgeMocks.getKnowledgeEnvironment).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain("等待网关就绪");
  });

  it("downloads the search engine and the model on request", async () => {
    bridgeMocks.getKnowledgeEnvironment.mockResolvedValue(environment({}));
    bridgeMocks.runKnowledgeAction.mockResolvedValue(
      environment({
        state: "installing",
        progress: { done: 0, total: 53574813 },
      }),
    );
    await render();
    expect(document.body.textContent).toContain("检索引擎");
    expect(document.body.textContent).toContain("未安装");
    await click(button("下载 · 51 MB"));
    expect(bridgeMocks.runKnowledgeAction).toHaveBeenCalledWith(
      "install_runtime",
      {},
    );
    expect(document.body.textContent).toContain("下载中");
    expect(document.body.textContent).toContain("0 B / 51 MB");

    await click(button("下载 · 417 MB"));
    expect(bridgeMocks.runKnowledgeAction).toHaveBeenLastCalledWith(
      "install_model",
      { id: granite.id },
    );
  });

  it("imports the picked archive and skips a cancelled pick", async () => {
    bridgeMocks.getKnowledgeEnvironment.mockResolvedValue(environment({}));
    bridgeMocks.pickKnowledgePath.mockResolvedValueOnce(null);
    await render();
    await click(button("导入"));
    expect(bridgeMocks.pickKnowledgePath).toHaveBeenCalledWith("runtime");
    expect(bridgeMocks.runKnowledgeAction).not.toHaveBeenCalled();

    bridgeMocks.pickKnowledgePath.mockResolvedValueOnce(
      "/home/me/bundle.tar.gz",
    );
    bridgeMocks.runKnowledgeAction.mockResolvedValue(
      environment({ state: "installed" }),
    );
    await click(button("导入"));
    expect(bridgeMocks.runKnowledgeAction).toHaveBeenCalledWith(
      "import_runtime",
      { id: undefined, path: "/home/me/bundle.tar.gz" },
    );
    expect(document.body.textContent).toContain("已安装");
  });

  it("runs the self-test of a running engine and shows each step", async () => {
    bridgeMocks.getKnowledgeEnvironment.mockResolvedValue(
      environment({
        state: "running",
        data_bytes: 175 * 1024 * 1024,
        started_at: "2026-10-23T14:00:00Z",
      }),
    );
    bridgeMocks.checkKnowledgeRuntime.mockResolvedValue({
      ok: true,
      engine: "5.7.25-OceanBase seekdb-v1.4.0.0",
      steps: [
        { name: "connect", ok: true, duration_ms: 1 },
        { name: "full_text", ok: true, duration_ms: 3 },
        {
          name: "vector",
          ok: true,
          duration_ms: 108,
          detail: "index_wait_ms=108",
        },
      ],
    });
    await render();
    expect(document.body.textContent).toContain("运行中");
    expect(document.body.textContent).toContain("175 MB");
    await click(button("自检"));
    expect(document.body.textContent).toContain("自检通过");
    expect(document.body.textContent).toContain("全文检索");
    expect(document.body.textContent).toContain("索引同步 108 ms");
    expect(document.body.textContent).toContain("seekdb-v1.4.0.0");
  });

  it("asks before clearing the engine's data", async () => {
    bridgeMocks.getKnowledgeEnvironment.mockResolvedValue(
      environment({ state: "installed" }),
    );
    bridgeMocks.runKnowledgeAction.mockResolvedValue(
      environment({ state: "installed" }),
    );
    await render();
    await click(button("清空数据"));
    expect(document.body.textContent).toContain("清空检索引擎的数据？");
    expect(bridgeMocks.runKnowledgeAction).not.toHaveBeenCalled();
    await click(button("确定"));
    await finishExitAnimations();
    expect(bridgeMocks.runKnowledgeAction).toHaveBeenCalledWith(
      "reset_runtime_data",
      {},
    );
  });

  it("explains a platform the engine does not run on", async () => {
    bridgeMocks.getKnowledgeEnvironment.mockResolvedValue(
      environment({ state: "unsupported", reason: "macos_version" }),
    );
    await render();
    expect(document.body.textContent).toContain("不兼容");
    expect(document.body.textContent).toContain(
      "需要 macOS 15.6.1 或更新的版本。",
    );
    expect(button("下载 · 51 MB")).toBeUndefined();
  });

  it("tests an installed model and shows the similarities", async () => {
    bridgeMocks.getKnowledgeEnvironment.mockResolvedValue(
      environment({ state: "installed" }, { state: "installed" }),
    );
    bridgeMocks.checkKnowledgeModel.mockResolvedValue({
      dimensions: 384,
      load_ms: 2264,
      embed_ms: 25,
      pairs: [
        {
          left: "怎么在 Windows 防火墙里放行一个端口",
          right: "How do I open a port in the Windows firewall?",
          similarity: 0.792,
        },
        {
          left: "怎么在 Windows 防火墙里放行一个端口",
          right: "今天中午吃什么",
          similarity: 0.444,
        },
      ],
    });
    await render();
    expect(document.body.textContent).toContain("384 维 · Apache-2.0");
    await click(button("测试"));
    expect(bridgeMocks.checkKnowledgeModel).toHaveBeenCalledWith(granite.id);
    expect(document.body.textContent).toContain("加载 2264 ms · 嵌入 25 ms");
    expect(document.body.textContent).toContain("相似度 0.79");
    expect(document.body.textContent).toContain(
      "对比句 · 怎么在 Windows 防火墙里放行一个端口",
    );
  });

  it("shows why an action failed", async () => {
    bridgeMocks.getKnowledgeEnvironment.mockResolvedValue(
      environment({ state: "installed" }),
    );
    bridgeMocks.runKnowledgeAction.mockRejectedValue(
      new Error("seekdb exited before it was ready"),
    );
    await render();
    await click(button("启动"));
    expect(document.body.textContent).toContain(
      "操作没有完成：seekdb exited before it was ready",
    );
  });
});
