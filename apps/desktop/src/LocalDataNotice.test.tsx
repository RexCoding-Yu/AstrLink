// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ getLocalDataStatus: vi.fn() }));

vi.mock("./bridge", () => ({ getLocalDataStatus: mocks.getLocalDataStatus }));

import { LocalDataNotice } from "./LocalDataNotice";

let container: HTMLDivElement;
let root: Root;

function status(unreadable: number) {
  return {
    unreadable_credentials: unreadable,
    unreadable_access_tokens: 0,
    audit_key_missing: false,
  };
}

async function render(props: {
  coreSessionKey: string | null;
  dismissed?: boolean;
  onDismiss?: () => void;
  onOpenServices?: () => void;
}) {
  await act(async () => {
    root.render(
      <LocalDataNotice
        coreSessionKey={props.coreSessionKey}
        dismissed={props.dismissed ?? false}
        onDismiss={props.onDismiss ?? (() => {})}
        onOpenServices={props.onOpenServices ?? (() => {})}
      />,
    );
    await Promise.resolve();
  });
}

function notice() {
  return container.querySelector('[data-slot="local-data-notice"]');
}

function button(label: string): HTMLButtonElement {
  const match = [...container.querySelectorAll("button")].find(
    (candidate) => candidate.textContent?.trim() === label,
  );
  if (!match) throw new Error(`Missing button: ${label}`);
  return match;
}

describe("LocalDataNotice", () => {
  beforeEach(() => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    mocks.getLocalDataStatus.mockReset();
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(() => {
    act(() => root.unmount());
    container.remove();
  });

  it("offers the providers page and a way to hide it", async () => {
    mocks.getLocalDataStatus.mockResolvedValue(status(2));
    const onOpenServices = vi.fn();
    const onDismiss = vi.fn();
    await render({ coreSessionKey: "core-1", onDismiss, onOpenServices });

    expect(notice()?.getAttribute("role")).toBe("status");
    expect(notice()?.textContent).toContain("有 2 项本地凭据无法解密");
    expect(notice()?.textContent).not.toContain("密钥");
    act(() => button("前往提供商").click());
    expect(onOpenServices).toHaveBeenCalledTimes(1);
    act(() => button("本次不再提示").click());
    expect(onDismiss).toHaveBeenCalledTimes(1);

    await render({ coreSessionKey: "core-1", dismissed: true });
    expect(notice()).toBeNull();
    expect(mocks.getLocalDataStatus).toHaveBeenCalledTimes(1);
  });

  it("stays hidden while every credential decrypts or Core is not ready", async () => {
    mocks.getLocalDataStatus.mockResolvedValue(status(0));
    await render({ coreSessionKey: null });
    expect(mocks.getLocalDataStatus).not.toHaveBeenCalled();
    await render({ coreSessionKey: "core-1" });
    expect(notice()).toBeNull();

    // A new Core session reads the status again.
    mocks.getLocalDataStatus.mockResolvedValue(status(1));
    await render({ coreSessionKey: "core-2" });
    expect(notice()?.textContent).toContain("有 1 项本地凭据无法解密");
    await render({ coreSessionKey: null });
    expect(notice()).toBeNull();
  });

  it("says nothing when Core cannot report the status", async () => {
    mocks.getLocalDataStatus.mockRejectedValue(new Error("404 not_found"));
    await render({ coreSessionKey: "core-1" });
    expect(notice()).toBeNull();
    expect(container.textContent).toBe("");
  });
});
