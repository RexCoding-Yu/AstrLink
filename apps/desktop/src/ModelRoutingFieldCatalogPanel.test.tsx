// @vitest-environment happy-dom

/**
 * ASTR-18 stage 4.5: the custom-dimension editor on the 「字段类型」 tab.
 *
 * The built-in table stays read-only; the block below it edits
 * `ClassifierSettings.dimensions` and writes back through the classifier
 * singleton. These tests pin the parts a review cannot see by reading the panel:
 * that an edit reaches the request the panel actually sends, that a rejected
 * edit never reaches the wire, and that the picker offered to a rule is the
 * dimension's own option set rather than free text.
 */

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({
  getRoutingSettings: vi.fn(),
  updateRoutingSettings: vi.fn(),
  getClassifierSettings: vi.fn(),
  updateClassifierSettings: vi.fn(),
}));
vi.mock("./bridge", () => bridge);
vi.mock("./notify", () => ({ notify: { success: vi.fn() } }));

import {
  defaultClassifierSettings,
  type ClassifierDimension,
  type ClassifierSettings,
} from "./classifier-settings-model";
import { defaultFailurePolicy } from "./failure-policy-model";
import { applyLocale } from "./i18n";
import { ModelRoutingFieldCatalogPanel } from "./ModelRoutingFieldCatalogPanel";

function routingSettings() {
  return {
    default_failure_policy: defaultFailurePolicy(),
    allow_unmatched_failover: false,
    strategy: "retry_first" as const,
    max_attempts: 6,
  };
}

function classifierSettings(
  dimensions: ClassifierDimension[] = [],
): ClassifierSettings {
  return { ...defaultClassifierSettings(), dimensions };
}

describe("model routing field catalog panel", () => {
  let container: HTMLDivElement;
  let root: Root;
  let dirty: boolean[];

  beforeEach(async () => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    dirty = [];
    bridge.getRoutingSettings.mockReset().mockResolvedValue(routingSettings());
    bridge.getClassifierSettings
      .mockReset()
      .mockResolvedValue(classifierSettings());
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

  async function render(): Promise<void> {
    await act(async () => {
      root.render(
        <ModelRoutingFieldCatalogPanel
          ready
          onDirtyChange={(next) => dirty.push(next)}
        />,
      );
      await Promise.resolve();
      await Promise.resolve();
    });
  }

  const button = (label: string) =>
    [...container.querySelectorAll<HTMLButtonElement>("button")].find(
      (element) => element.textContent?.trim() === label,
    );

  const saveButton = () =>
    [...container.querySelectorAll<HTMLButtonElement>("button")].find(
      (element) => element.textContent?.trim() === "保存",
    );

  /**
   * Both the dimension and its options have a button reading "移除", so a text
   * match is ambiguous — the dimension's comes first and would drop the whole
   * row. Select by the accessible name instead.
   */
  const removeButton = (label: string) =>
    container.querySelector<HTMLButtonElement>(
      `button[aria-label="${label}"]`,
    );

  const input = (label: string): HTMLInputElement => {
    const match = container.querySelector<HTMLInputElement>(
      `input[aria-label="${label}"]`,
    );
    if (match === null) throw new Error(`Missing input: ${label}`);
    return match;
  };

  /** React tracks input values, so set through the native setter first. */
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

  /** Add one dimension with a name and one option, ready to save. */
  async function fillFirstDimension(): Promise<void> {
    await act(async () => {
      button("添加维度")?.click();
      await Promise.resolve();
    });
    await type(input("维度名 1"), "topic");
    await type(input("取值 1.1"), "billing");
  }

  it("keeps the built-in table read-only and lists the 22 frozen fields", async () => {
    await render();
    const rows = container.querySelectorAll(
      "[data-model-routing-catalog-field]",
    );
    expect(rows.length).toBe(22);
    // The built-in block offers no controls of its own.
    expect(
      container.querySelector("[data-model-routing-catalog-field] input"),
    ).toBeNull();
  });

  it("sends the edited dimension list on save", async () => {
    await render();
    await fillFirstDimension();
    await act(async () => {
      saveButton()?.click();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(bridge.updateClassifierSettings).toHaveBeenCalledTimes(1);
    const submitted = bridge.updateClassifierSettings.mock
      .calls[0][0] as ClassifierSettings;
    expect(submitted.dimensions).toEqual([
      { name: "topic", description: "", options: [{ name: "billing", description: "" }] },
    ]);
    // The rest of the singleton travels with the save untouched: the panel
    // edits one field of a whole-document resource, so dropping the other keys
    // would silently reset the user's classifier configuration.
    const loaded = defaultClassifierSettings();
    expect(submitted.inline).toEqual(loaded.inline);
    expect(submitted.classifier_source).toBe(loaded.classifier_source);
    expect(submitted.enabled).toBe(loaded.enabled);
  });

  it("lists a saved dimension in the catalog table as classifier-sourced", async () => {
    bridge.getClassifierSettings
      .mockReset()
      .mockResolvedValue(
        classifierSettings([
          { name: "topic", options: [{ name: "billing" }] },
        ]),
      );
    await render();

    const row = container.querySelector(
      '[data-model-routing-catalog-field="topic"]',
    );
    expect(row?.textContent).toContain("分类器");
    // Its own option set is the reported value domain, not an open vocabulary.
    expect(row?.textContent).toContain("billing");
  });

  it("refuses a reserved name and never sends the document", async () => {
    await render();
    await act(async () => {
      button("添加维度")?.click();
      await Promise.resolve();
    });
    // `task_type` is built in: its options come from the group label settings,
    // so accepting it here would store a dimension nothing ever reads.
    await type(input("维度名 1"), "task_type");
    await type(input("取值 1.1"), "billing");
    await act(async () => {
      saveButton()?.click();
      await Promise.resolve();
    });

    expect(bridge.updateClassifierSettings).not.toHaveBeenCalled();
    expect(container.textContent).toContain("task_type 是内建维度");
  });

  it("refuses an empty option list and never sends the document", async () => {
    await render();
    await act(async () => {
      button("添加维度")?.click();
      await Promise.resolve();
    });
    await type(input("维度名 1"), "topic");
    await type(input("取值 1.1"), "billing");
    // Drop the only option: a dimension with none can never produce a result.
    // The dimension's own remove button reads the same "移除", so this has to
    // name the option explicitly.
    await act(async () => {
      removeButton("移除取值 billing")?.click();
      await Promise.resolve();
    });
    await act(async () => {
      saveButton()?.click();
      await Promise.resolve();
    });

    expect(bridge.updateClassifierSettings).not.toHaveBeenCalled();
    expect(container.textContent).toContain("请至少添加一个取值");
  });

  it("refuses a duplicate dimension name", async () => {
    await render();
    await act(async () => {
      button("添加维度")?.click();
      await Promise.resolve();
    });
    await type(input("维度名 1"), "topic");
    await type(input("取值 1.1"), "billing");
    await act(async () => {
      button("添加维度")?.click();
      await Promise.resolve();
    });
    await type(input("维度名 2"), "topic");
    await type(input("取值 2.1"), "support");
    await act(async () => {
      saveButton()?.click();
      await Promise.resolve();
    });

    expect(bridge.updateClassifierSettings).not.toHaveBeenCalled();
    expect(container.textContent).toContain("维度名重复");
  });

  it("stops offering new dimensions at the shared 6-dimension budget", async () => {
    // Five custom plus the built-in task_type is the confirmed ceiling.
    bridge.getClassifierSettings.mockReset().mockResolvedValue(
      classifierSettings(
        Array.from({ length: 5 }, (_, index) => ({
          name: `dim_${index}`,
          options: [{ name: "a" }],
        })),
      ),
    );
    await render();

    expect(button("添加维度")?.disabled).toBe(true);
    expect(container.textContent).toContain("最多 5 个自定义维度");
  });

  it("marks the tab dirty only while an unsaved edit is pending", async () => {
    await render();
    // A freshly loaded list is not a pending write.
    expect(dirty.at(-1)).toBe(false);
    expect(saveButton()?.disabled).toBe(true);

    await fillFirstDimension();
    expect(dirty.at(-1)).toBe(true);
    expect(saveButton()?.disabled).toBe(false);
  });

  it("shows the {{dim.X}} placeholder a prompt has to use", async () => {
    await render();
    await act(async () => {
      button("添加维度")?.click();
      await Promise.resolve();
    });
    await type(input("维度名 1"), "topic");

    expect(container.textContent).toContain("{{dim.topic}}");
  });
});
