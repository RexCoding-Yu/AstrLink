// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({
  getRoutingSettings: vi.fn(),
  updateRoutingSettings: vi.fn(),
}));
vi.mock("./bridge", () => bridge);
vi.mock("./notify", () => ({ notify: { success: vi.fn() } }));

import { defaultFailurePolicy } from "./failure-policy-model";
import { applyLocale, i18n } from "./i18n";
import { ModelRoutingGroupsPanel } from "./ModelRoutingGroupsPanel";
import type { RoutableService } from "./service-model";

const services: RoutableService[] = [
  {
    id: "service_a",
    name: "A",
    kind: "openai",
    enabled: true,
    models: ["gpt-5", "claude-sonnet-4-5"],
    capabilities: [],
  },
];

function settings() {
  return {
    default_failure_policy: defaultFailurePolicy(),
    allow_unmatched_failover: false,
    strategy: "retry_first" as const,
    max_attempts: 6,
  };
}

describe("model routing groups panel", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(async () => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    bridge.getRoutingSettings.mockReset().mockResolvedValue(settings());
    bridge.updateRoutingSettings
      .mockReset()
      .mockImplementation(async (patch) => ({ ...settings(), ...patch }));
    await applyLocale("zh-CN");
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  function saveButton() {
    return [...container.querySelectorAll("button")].find(
      (element) => element.textContent === "保存",
    );
  }

  async function save() {
    await act(async () => {
      saveButton()?.click();
      await Promise.resolve();
    });
  }

  async function render(onDirtyChange = vi.fn()) {
    await act(async () =>
      root.render(
        <ModelRoutingGroupsPanel
          services={services}
          ready
          onDirtyChange={onDirtyChange}
        />,
      ),
    );
    return onDirtyChange;
  }

  it("loads the groups and submits only the group key", async () => {
    const dirty = await render();
    expect(bridge.getRoutingSettings).toHaveBeenCalledTimes(1);
    expect(dirty).toHaveBeenLastCalledWith(false);
    // An untouched document is not a pending write.
    expect(saveButton()?.disabled).toBe(true);
  });

  it("blocks saving after the last value chip is removed", async () => {
    bridge.getRoutingSettings.mockResolvedValue({
      ...settings(),
      model_routing_groups: [
        {
          id: "g",
          enabled: true,
          members: [{ id: "a", service_id: "service_a", model: "gpt-5" }],
          default_member_id: "a",
          rules: [
            {
              id: "r",
              priority: 1,
              target_member_id: "a",
              when: { field: "literal_keyword", contains_any: ["todo"] },
            },
          ],
        },
      ],
    });
    await render();
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>('[aria-label="移除取值 todo"]')!
        .click(),
    );
    expect(
      container.querySelectorAll("[data-multi-combobox-chip]"),
    ).toHaveLength(0);
    await save();
    expect(bridge.updateRoutingSettings).not.toHaveBeenCalled();
    expect(container.textContent).toContain("请至少添加一个值");
  });

  it("blocks nested task_type without a classifier and saves after configuring one", async () => {
    bridge.getRoutingSettings.mockResolvedValue({
      ...settings(),
      model_routing_groups: [
        {
          id: "g",
          enabled: true,
          members: [{ id: "a", service_id: "service_a", model: "gpt-5" }],
          default_member_id: "a",
          rules: [
            {
              id: "r",
              priority: 1,
              target_member_id: "a",
              when: {
                not: {
                  all: [
                    { field: "streaming", equals_bool: true },
                    { field: "task_type", in: ["coding"] },
                  ],
                },
              },
            },
          ],
        },
      ],
    });
    await render();
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>(
          '[role="switch"][aria-label="启用该组"]',
        )!
        .click(),
    );
    await save();
    expect(bridge.updateRoutingSettings).not.toHaveBeenCalled();
    expect(container.textContent).toContain(
      i18n.t("modelRoutingGroup.taskTypeClassifierError"),
    );
    expect(
      container
        .querySelector('[data-model-routing-classifier="true"]')
        ?.contains(document.activeElement),
    ).toBe(true);

    const prompt = container.querySelector<HTMLTextAreaElement>(
      'textarea[aria-label="分类提示词"]',
    )!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLTextAreaElement.prototype,
        "value",
      )!.set!.call(prompt, "classify the request");
      prompt.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await save();
    expect(bridge.updateRoutingSettings).toHaveBeenCalledExactlyOnceWith({
      model_routing_groups: [
        expect.objectContaining({
          classifier_prompt: "classify the request",
        }),
      ],
    });
  });

  it("keeps an unsaved draft when a reload lands", async () => {
    const dirty = await render();
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>("[data-model-routing-add]")!
        .click(),
    );
    expect(dirty).toHaveBeenLastCalledWith(true);
    // A reconnect must not silently discard the draft.
    expect(saveButton()?.disabled).toBe(false);
  });
});
