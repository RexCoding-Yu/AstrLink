// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  pendingPluginApproval: vi.fn(),
  decidePluginApproval: vi.fn(),
  getPlugin: vi.fn(),
  listenPluginApprovalChanged: vi.fn(),
}));

vi.mock("./bridge", () => mocks);

import { PluginApprovalWindow } from "./PluginApprovalWindow";

let container: HTMLDivElement;
let root: Root;

const pending = {
  id: "approval-1",
  key: "gate",
  approval: {
    plugin: "gate",
    name: "Gate",
    enable: true,
    added_capabilities: ["request.gate", "network", "ui.code"],
    added_origins: ["http://192.0.2.7:80"],
    plaintext: ["http://192.0.2.7:80"],
  },
};

async function render() {
  await act(async () => {
    root.render(<PluginApprovalWindow />);
  });
  await act(async () => {});
}

function button(label: string): HTMLButtonElement {
  const found = [...document.querySelectorAll("button")].find(
    (item) => item.textContent?.trim() === label,
  );
  if (!found) throw new Error(`Missing button ${label}`);
  return found;
}

describe("PluginApprovalWindow", () => {
  beforeEach(() => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    mocks.pendingPluginApproval.mockResolvedValue(pending);
    mocks.decidePluginApproval.mockResolvedValue(undefined);
    mocks.getPlugin.mockRejectedValue(new Error("unavailable"));
    mocks.listenPluginApprovalChanged.mockResolvedValue(() => {});
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  it("lists what enabling grants and keeps only what is left ticked", async () => {
    await render();
    const text = document.body.textContent ?? "";
    expect(text).toContain("启用「Gate」");
    expect(text).toContain("拒绝请求");
    // Interface code and plain HTTP are spelt out.
    expect(text).toContain("界面代码和 AstrLink 界面同等权限");
    expect(text).toContain("http://192.0.2.7:80 · 明文 HTTP");
    const network = [
      ...document.querySelectorAll<HTMLButtonElement>('[role="checkbox"]'),
    ].find((box) => box.parentElement?.textContent?.includes("网络"));
    if (!network) throw new Error("Missing the network capability");
    await act(async () => network.click());
    await act(async () => button("启用").click());
    expect(mocks.decidePluginApproval).toHaveBeenCalledWith(
      "approval-1",
      true,
      ["request.gate", "ui.code"],
    );
  });

  it("denies without sending capabilities", async () => {
    await render();
    await act(async () => button("拒绝").click());
    expect(mocks.decidePluginApproval).toHaveBeenCalledWith(
      "approval-1",
      false,
      undefined,
    );
  });

  it("says so when nothing waits", async () => {
    mocks.pendingPluginApproval.mockResolvedValue(null);
    await render();
    expect(document.body.textContent).toContain("没有等待确认的插件修改");
  });
});
