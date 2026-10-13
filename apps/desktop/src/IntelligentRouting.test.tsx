// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({
  builtinToolAction: vi.fn().mockResolvedValue({ configured: false }),
  classifierCredentialAction: vi.fn().mockResolvedValue({ configured: false }),
  classifierDryRun: vi.fn(),
  getClassifierSettings: vi.fn(),
  getRoutingSettings: vi.fn(),
  updateClassifierSettings: vi.fn(),
  updateRoutingSettings: vi.fn(),
}));
vi.mock("./bridge", () => bridge);
vi.mock("./notify", () => ({ notify: { success: vi.fn() } }));

import { IntelligentRouting } from "./IntelligentRouting";
import { defaultClassifierSettings } from "./classifier-settings-model";
import { applyLocale, i18n } from "./i18n";
import { defaultFailurePolicy } from "./failure-policy-model";

// The group editor renders its own role="tab" chips, so scope to the page bar.
function tabs(): HTMLElement[] {
  return [
    ...document.querySelectorAll<HTMLElement>(
      '[data-slot="tabs-list"] [role="tab"]',
    ),
  ];
}

function tab(label: string): HTMLButtonElement {
  const match = tabs().find((item) => item.textContent?.trim() === label);
  if (!(match instanceof HTMLButtonElement)) {
    throw new Error(`Missing tab: ${label}`);
  }
  return match;
}

// Radix activates a trigger on mousedown, not click.
async function selectTab(label: string): Promise<void> {
  await act(async () => {
    tab(label).dispatchEvent(
      new MouseEvent("mousedown", { bubbles: true, button: 0 }),
    );
    await Promise.resolve();
  });
}

describe("intelligent routing workspaces", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(async () => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    bridge.getRoutingSettings.mockReset().mockResolvedValue({
      default_failure_policy: defaultFailurePolicy(),
      allow_unmatched_failover: false,
      strategy: "retry_first",
      max_attempts: 6,
    });
    bridge.updateRoutingSettings.mockReset();
    bridge.getClassifierSettings
      .mockReset()
      .mockResolvedValue(defaultClassifierSettings());
    bridge.classifierCredentialAction
      .mockReset()
      .mockResolvedValue({ configured: false });
    bridge.updateClassifierSettings.mockReset();
    bridge.classifierDryRun.mockReset();
    await applyLocale("zh-CN");
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  async function render(
    view?: "groups" | "fieldCatalog" | "classifier" | "dryRun",
    onDirtyChange: (dirty: boolean) => void = vi.fn(),
  ) {
    await act(async () => {
      root.render(
        <IntelligentRouting
          initialView={view}
          services={[]}
          ready
          onDirtyChange={onDirtyChange}
        />,
      );
      await Promise.resolve();
      await Promise.resolve();
    });
  }

  it("shows the four in-page tabs and nothing else", async () => {
    await render();
    expect(tabs().map((item) => item.textContent?.trim())).toEqual([
      "模型路由组",
      "字段类型",
      "分类器配置",
      "试运行",
    ]);
  });

  it("opens the groups tab by default and switches without a request", async () => {
    await render();
    expect(tab("模型路由组").getAttribute("data-state")).toBe("active");

    await selectTab("分类器配置");
    expect(tab("分类器配置").getAttribute("data-state")).toBe("active");
    expect(container.textContent).toContain("分类器配置");
  });

  it("honours an explicit view so a tray jump can land on a tab", async () => {
    await render("dryRun");
    expect(tab("试运行").getAttribute("data-state")).toBe("active");
  });

  it("hosts the migrated group editor on the first tab", async () => {
    await render();
    // B3 moved the editor here; it must render, not a placeholder.
    expect(
      container.querySelector('[data-testid="model-routing-groups-panel"]'),
    ).not.toBeNull();
    expect(container.textContent).toContain(i18n.t("modelRoutingGroup.title"));
    expect(bridge.getRoutingSettings).toHaveBeenCalled();
    // Nothing is written until the user saves.
    expect(bridge.updateRoutingSettings).not.toHaveBeenCalled();
  });

  it("keeps an unsaved group draft alive across a tab switch", async () => {
    bridge.getRoutingSettings.mockResolvedValue({
      default_failure_policy: defaultFailurePolicy(),
      allow_unmatched_failover: false,
      strategy: "retry_first",
      max_attempts: 6,
      model_routing_groups: [
        {
          id: "g",
          enabled: true,
          members: [{ id: "a", service_id: "service_a", model: "gpt-5" }],
          default_member_id: "a",
          rules: [],
        },
      ],
    });
    await render();
    const panel = container.querySelector(
      '[data-testid="model-routing-groups-panel"]',
    );
    expect(panel?.textContent).toContain("g");

    await selectTab("试运行");
    await selectTab("模型路由组");
    // forceMount keeps the editor mounted, so its state survives the switch.
    expect(
      container.querySelector('[data-testid="model-routing-groups-panel"]'),
    ).toBe(panel);
  });

  it("keeps an unsaved classifier draft alive across a tab switch", async () => {
    await render("classifier");
    const apiKey = container.querySelector<HTMLInputElement>(
      'input[aria-label="API Key"]',
    );
    expect(apiKey).not.toBeNull();
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )?.set;
      setter?.call(apiKey, "sk-typed-once");
      apiKey?.dispatchEvent(new Event("input", { bubbles: true }));
      await Promise.resolve();
    });
    const panel = container.querySelector(
      '[data-testid="classifier-settings-panel"]',
    );

    await selectTab("试运行");
    await selectTab("分类器配置");

    // forceMount keeps the classifier mounted, so a typed key is not lost when
    // the user steps over to another tab to check something.
    expect(
      container.querySelector('[data-testid="classifier-settings-panel"]'),
    ).toBe(panel);
    expect(
      container.querySelector<HTMLInputElement>('input[aria-label="API Key"]')
        ?.value,
    ).toBe("sk-typed-once");
  });

  it("keeps a groups dirty flag when switching to the classifier tab", async () => {
    const dirty = vi.fn();
    bridge.getRoutingSettings.mockResolvedValue({
      default_failure_policy: defaultFailurePolicy(),
      allow_unmatched_failover: false,
      strategy: "retry_first",
      max_attempts: 6,
      model_routing_groups: [
        {
          id: "g",
          enabled: true,
          members: [{ id: "a", service_id: "service_a", model: "gpt-5" }],
          default_member_id: "a",
          rules: [],
        },
      ],
    });
    await render("groups", dirty);

    await act(async () => {
      [...container.querySelectorAll("button")]
        .find((item) => item.textContent?.trim() === "新增规则")
        ?.click();
      await Promise.resolve();
    });
    expect(dirty.mock.calls.at(-1)).toEqual([true]);

    // The classifier panel must not reset the groups dirty state when it is
    // entered. This is the exact groups -> classifier regression scenario.
    await selectTab("分类器配置");
    expect(dirty.mock.calls.at(-1)).toEqual([true]);
  });

  it("keeps groups dirty when the classifier tab becomes clean", async () => {
    const dirty = vi.fn();
    bridge.getRoutingSettings.mockResolvedValue({
      default_failure_policy: defaultFailurePolicy(),
      allow_unmatched_failover: false,
      strategy: "retry_first",
      max_attempts: 6,
      model_routing_groups: [
        {
          id: "g",
          enabled: true,
          members: [{ id: "a", service_id: "service_a", model: "gpt-5" }],
          default_member_id: "a",
          rules: [],
        },
      ],
    });
    await render("groups", dirty);

    await act(async () => {
      [...container.querySelectorAll("button")]
        .find((item) => item.textContent?.trim() === "新增规则")
        ?.click();
      await Promise.resolve();
    });
    await selectTab("分类器配置");
    const apiKey = container.querySelector<HTMLInputElement>(
      'input[aria-label="API Key"]',
    );
    expect(apiKey).not.toBeNull();

    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )?.set;
      setter?.call(apiKey, "sk-temporary");
      apiKey?.dispatchEvent(new Event("input", { bubbles: true }));
      await Promise.resolve();
    });
    expect(dirty.mock.calls.at(-1)).toEqual([true]);

    // A shared slot would let the classifier's clean transition clear the
    // still-dirty groups draft. Per-tab aggregation must remain true.
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )?.set;
      setter?.call(apiKey, "");
      apiKey?.dispatchEvent(new Event("input", { bubbles: true }));
      await Promise.resolve();
    });
    expect(dirty.mock.calls.at(-1)).toEqual([true]);
  });

  it("hosts the classifier settings on the second tab without an upstream call", async () => {
    await render("classifier");
    // B4 replaced the placeholder; the real panel must render, and the tab must
    // never reach a gateway: the classifier is a local singleton resource.
    expect(
      container.querySelector('[data-testid="classifier-settings-panel"]'),
    ).not.toBeNull();
    expect(bridge.getClassifierSettings).toHaveBeenCalled();
    expect(bridge.updateClassifierSettings).not.toHaveBeenCalled();
    expect(bridge.classifierDryRun).not.toHaveBeenCalled();
  });

  it("hosts the dry run on the third tab without an upstream call", async () => {
    await render("dryRun");
    expect(
      container.querySelector('[data-testid="classifier-dry-run-panel"]'),
    ).not.toBeNull();
    // Nothing runs until the user presses the button.
    expect(bridge.classifierDryRun).not.toHaveBeenCalled();
  });
});
