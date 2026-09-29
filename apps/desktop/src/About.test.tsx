// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { browserUpdateSnapshot, type UpdateSnapshot } from "./update-model";
const mocks = vi.hoisted(() => ({
  checkAppUpdate: vi.fn(),
  downloadAppUpdate: vi.fn(),
  installAppUpdate: vi.fn(),
  saveUpdatePreferences: vi.fn(),
}));
vi.mock("./update-bridge", () => mocks);
vi.mock("@tauri-apps/api/core", () => ({ isTauri: () => true }));
vi.mock("./bridge", () => ({ openExternalURL: vi.fn() }));
vi.mock("./LocalClientUpdates", () => ({
  LocalClientUpdates: () => <div data-slot="local-clients" />,
}));
import { About } from "./About";

describe("About updates", () => {
  let container: HTMLDivElement, root: Root;
  const receive = vi.fn();
  const ready: UpdateSnapshot = {
    ...browserUpdateSnapshot(),
    current_version: "1.0.0",
    development: false,
    configured: true,
    install_supported: true,
    phase: "ready",
    release: {
      version: "1.1.0",
      notes: "New release",
      published_at: null,
      url: "https://github.com/Calcium-Ion/AstrLink/releases/tag/v1.1.0",
    },
  };
  beforeEach(() => {
    vi.clearAllMocks();
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });
  async function render(snapshot = ready, dirty = false) {
    await act(async () =>
      root.render(
        <About
          snapshot={snapshot}
          onSnapshot={receive}
          loadError={null}
          hasUnsavedChanges={() => dirty}
        />,
      ),
    );
  }
  function button(label: string) {
    return Array.from(document.querySelectorAll("button"))
      .reverse()
      .find((b) => b.textContent === label)!;
  }
  it("requires in-app confirmation before installation and supports cancellation", async () => {
    await render();
    await act(async () => button("安装并重启").click());
    expect(document.querySelector('[role="alertdialog"]')).not.toBeNull();
    expect(mocks.installAppUpdate).not.toHaveBeenCalled();
    await act(async () => button("取消").click());
    expect(mocks.installAppUpdate).not.toHaveBeenCalled();
    mocks.installAppUpdate.mockResolvedValue({ ...ready, phase: "installing" });
    await act(async () => button("安装并重启").click());
    await act(async () => button("安装并重启").click());
    expect(mocks.installAppUpdate).toHaveBeenCalledOnce();
  });
  it("protects unsaved edits and does not offer install for development or deb", async () => {
    await render(ready, true);
    await act(async () => button("安装并重启").click());
    expect(button("安装并重启").disabled).toBe(true);
    await act(async () => button("取消").click());
    await render({ ...ready, install_supported: false, phase: "manual" });
    expect(button("安装并重启")).toBeUndefined();
    expect(button("查看版本与下载")).toBeDefined();
  });
  it("shows download progress and saves update-only preferences", async () => {
    const downloading = {
      ...ready,
      phase: "downloading" as const,
      downloaded_bytes: 5,
      total_bytes: 10,
    };
    await render(downloading);
    expect(
      container
        .querySelector('[role="progressbar"]')
        ?.getAttribute("aria-valuenow"),
    ).toBe("50");
    await act(async () =>
      (
        container.querySelector("button[aria-controls]") as HTMLButtonElement
      ).click(),
    );
    mocks.saveUpdatePreferences.mockResolvedValue({
      ...ready,
      preferences: { ...ready.preferences, auto_check: false },
    });
    await act(async () =>
      (container.querySelector('[role="switch"]') as HTMLButtonElement).click(),
    );
    expect(mocks.saveUpdatePreferences).toHaveBeenCalledWith({
      auto_check: false,
      auto_download: true,
      channel: "stable",
    });
    expect(receive).toHaveBeenCalled();
  });
  it("reports command failures and retries checks", async () => {
    mocks.checkAppUpdate.mockRejectedValue(new Error("offline"));
    await render({ ...ready, phase: "idle", release: null });
    await act(async () => button("检查更新").click());
    expect(container.textContent).toContain("offline");
    mocks.checkAppUpdate.mockResolvedValue({
      ...ready,
      phase: "up_to_date",
      release: null,
    });
    await act(async () => button("检查更新").click());
    expect(mocks.checkAppUpdate).toHaveBeenCalledTimes(2);
  });
});
