// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({
  getClassifierSettings: vi.fn(),
  updateClassifierSettings: vi.fn(),
  classifierCredentialAction: vi.fn(),
}));
vi.mock("./bridge", () => bridge);

import {
  classifierProtocolPresets,
  defaultClassifierSettings,
  type ClassifierSettings,
} from "./classifier-settings-model";
import { ClassifierSettingsPanel } from "./ClassifierSettingsPanel";
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

function field(container: HTMLElement, label: string): HTMLInputElement {
  const match = container.querySelector<HTMLInputElement>(
    `input[aria-label="${label}"]`,
  );
  if (match === null) throw new Error(`Missing input: ${label}`);
  return match;
}

/** React tracks input values, so set through the native setter before dispatch. */
async function type(element: HTMLInputElement, value: string): Promise<void> {
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )?.set;
    setter?.call(element, value);
    element.dispatchEvent(new Event("input", { bubbles: true }));
    await Promise.resolve();
  });
}

describe("classifier settings panel", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(async () => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    bridge.getClassifierSettings
      .mockReset()
      .mockResolvedValue(defaultClassifierSettings());
    bridge.classifierCredentialAction
      .mockReset()
      .mockResolvedValue({ configured: false });
    bridge.updateClassifierSettings
      .mockReset()
      .mockImplementation(async (settings: ClassifierSettings) => settings);
    await applyLocale("zh-CN");
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  async function render(ready = true): Promise<void> {
    await act(async () => {
      root.render(
        <ClassifierSettingsPanel
          ready={ready}
          services={[]}
          onDirtyChange={() => {}}
        />,
      );
      await Promise.resolve();
      await Promise.resolve();
    });
  }

  it("loads the singleton resource and the credential status together", async () => {
    await render();

    expect(bridge.getClassifierSettings).toHaveBeenCalled();
    expect(bridge.classifierCredentialAction).toHaveBeenCalledWith("status");
    expect(field(container, "模型名").value).toBe(
      classifierProtocolPresets.jev.model,
    );
  });

  it("leaves user input alone when the protocol switches", async () => {
    await render();
    const endpointLabel = "端点";
    await type(field(container, endpointLabel), "https://gateway.internal/jev");

    await act(async () => {
      button("OpenAI Decisions", container).click();
      await Promise.resolve();
    });

    expect(field(container, endpointLabel).value).toBe(
      "https://gateway.internal/jev",
    );
    expect(field(container, "模型名").value).toBe(
      classifierProtocolPresets.openai_decisions.model,
    );
  });

  it("blocks the save and marks the endpoint when it embeds credentials", async () => {
    await render();
    // Enable first: the disabled classifier saves an incomplete draft on purpose.
    await act(async () => {
      container.querySelector<HTMLButtonElement>('[role="switch"]')?.click();
      await Promise.resolve();
    });
    await type(field(container, "端点"), "https://user:pass@api.typesafe.ai");

    await act(async () => {
      button("保存", container).click();
      await Promise.resolve();
    });

    expect(bridge.updateClassifierSettings).not.toHaveBeenCalled();
    expect(field(container, "端点").getAttribute("aria-invalid")).toBe("true");
    expect(container.textContent).toContain("端点不得内含凭据（user:pass@）。");
  });

  it("blocks the save when task type rules exist but no key is configured", async () => {
    await render();
    await act(async () => {
      container.querySelector<HTMLButtonElement>('[role="switch"]')?.click();
      await Promise.resolve();
    });

    await act(async () => {
      button("保存", container).click();
      await Promise.resolve();
    });

    expect(bridge.updateClassifierSettings).not.toHaveBeenCalled();
    expect(field(container, "API Key").getAttribute("aria-invalid")).toBe(
      "true",
    );
  });

  it("sends the key to the credential sub-resource and never in the settings document", async () => {
    await render();
    await act(async () => {
      container.querySelector<HTMLButtonElement>('[role="switch"]')?.click();
      await Promise.resolve();
    });
    await type(field(container, "API Key"), "sk-secret-value");

    await act(async () => {
      button("保存", container).click();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(bridge.classifierCredentialAction).toHaveBeenCalledWith(
      "save_key",
      "sk-secret-value",
    );
    const submitted = bridge.updateClassifierSettings.mock.calls[0][0];
    expect(JSON.stringify(submitted)).not.toContain("sk-secret-value");
  });

  it("clears the drafted key after deleting it so it cannot come back", async () => {
    bridge.classifierCredentialAction.mockImplementation(
      async (action: "status" | "save_key" | "delete_key") =>
        action === "status" ? { configured: true } : { configured: false },
    );
    await render();
    // A key is on screen but never saved, so the delete guard is not what stops
    // the save — the draft going away is.
    await type(field(container, "API Key"), "sk-still-drafted");

    await act(async () => {
      button("删除密钥", container).click();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(bridge.classifierCredentialAction).toHaveBeenCalledWith(
      "delete_key",
    );
    expect(field(container, "API Key").value).toBe("");
    expect(button("保存", container).disabled).toBe(true);
  });

  it("treats a saved key as configured even when the settings save fails", async () => {
    // The credential sub-resource is saved first, so a later settings failure
    // must not leave the panel claiming no key is configured.
    bridge.updateClassifierSettings.mockRejectedValue(
      new Error("routing settings rejected"),
    );
    await render();
    await type(field(container, "API Key"), "sk-half-saved");
    let credentialReply = { configured: false };
    bridge.classifierCredentialAction.mockImplementation(
      async (action: "status" | "save_key" | "delete_key") => {
        if (action === "save_key") credentialReply = { configured: true };
        return credentialReply;
      },
    );

    await act(async () => {
      button("保存", container).click();
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(bridge.classifierCredentialAction).toHaveBeenCalledWith(
      "save_key",
      "sk-half-saved",
    );
    expect(container.textContent).toContain("已配置密钥");
    expect(field(container, "API Key").getAttribute("aria-invalid")).toBe(
      "false",
    );
  });

  it("does not re-send a key that is already configured", async () => {
    bridge.classifierCredentialAction.mockResolvedValue({ configured: true });
    await render();
    await act(async () => {
      container.querySelector<HTMLButtonElement>('[role="switch"]')?.click();
      await Promise.resolve();
    });

    await act(async () => {
      button("保存", container).click();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(bridge.updateClassifierSettings).toHaveBeenCalled();
    const saveCalls = bridge.classifierCredentialAction.mock.calls.filter(
      ([action]) => action === "save_key",
    );
    expect(saveCalls).toEqual([]);
  });
});
