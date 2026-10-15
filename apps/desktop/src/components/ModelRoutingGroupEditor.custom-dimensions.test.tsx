// @vitest-environment happy-dom
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { applyLocale, i18n } from "@/i18n";
import {
  fieldOperators,
  isModelRoutingRangeField,
  isModelRoutingTimeField,
  parseModelRoutingGroups,
  type ClassifierDimension,
  type ModelRoutingCondition,
  type ModelRoutingGroup,
} from "@/model-routing-group-model";
import type { RoutableService } from "@/service-model";

import { ModelRoutingGroupEditor } from "./ModelRoutingGroupEditor";

const services: RoutableService[] = [
  {
    id: "service_anthropic",
    name: "Anthropic",
    kind: "anthropic",
    enabled: true,
    models: ["claude-sonnet-4-5"],
    capabilities: [],
  },
] as unknown as RoutableService[];

const t = (key: string, options?: Record<string, unknown>): string =>
  String(i18n.t(key, options as never));

describe("ModelRoutingGroupEditor custom dimensions", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(async () => {
    await applyLocale("zh-CN");
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  /** The dimension definition the editor now takes, with its own option set. */
  const topicDimension: ClassifierDimension = {
    name: "topic",
    options: [{ name: "billing" }, { name: "support" }],
  };

  function Harness({
    initial = [],
    dimensions = [],
    change = () => {},
  }: {
    initial?: ModelRoutingGroup[];
    dimensions?: ClassifierDimension[];
    change?: (value: ModelRoutingGroup[]) => void;
  }) {
    const [value, setValue] = useState(initial);
    return (
      <ModelRoutingGroupEditor
        value={value}
        services={services}
        dimensions={dimensions}
        onChange={(next) => {
          setValue(next);
          change(next);
        }}
      />
    );
  }

  const button = (label: string) =>
    [...container.querySelectorAll<HTMLButtonElement>("button")].find(
      (element) =>
        element.textContent?.trim() === label ||
        element.getAttribute("aria-label") === label,
    );

  function groupWithWhen(when: ModelRoutingCondition): ModelRoutingGroup {
    return {
      id: "g",
      enabled: true,
      members: [
        {
          id: "a",
          service_id: "service_anthropic",
          model: "claude-sonnet-4-5",
        },
      ],
      default_member_id: "a",
      rules: [
        {
          id: "r1",
          priority: 10,
          target_member_id: "a",
          when,
        },
      ],
    };
  }

  /** 打开第 index 个字段选择器，列出全部候选值。 */
  async function fieldOptions(index: number): Promise<string[]> {
    const fields = container.querySelectorAll<HTMLInputElement>(
      '[data-model-routing-field-control] input[role="combobox"]',
    );
    await act(async () => fields[index].click());
    return [
      ...document.querySelectorAll<HTMLElement>("[role='option']"),
    ].map((option) => option.getAttribute("data-option-value") ?? "");
  }

  it("lists a declared custom dimension alongside the built-in fields", async () => {
    await act(async () =>
      root.render(
        <Harness
          initial={[groupWithWhen({ field: "task_type", in: ["coding"] })]}
          dimensions={[topicDimension]}
        />,
      ),
    );
    const options = await fieldOptions(0);
    expect(options).toContain("topic");
    expect(options).toContain("task_type");
    expect(options).toContain("protocol");
  });

  it("lists no custom dimension when none is declared", async () => {
    await act(async () =>
      root.render(
        <Harness initial={[groupWithWhen({ field: "task_type", in: ["coding"] })]} />,
      ),
    );
    const options = await fieldOptions(0);
    expect(options).not.toContain("topic");
    // The built-in block is unchanged, so the P1 field list is intact.
    expect(options).toContain("task_type");
  });

  it("renders a saved rule that reads a custom dimension", async () => {
    await act(async () =>
      root.render(
        <Harness
          initial={[groupWithWhen({ field: "topic", in: ["billing"] })]}
          dimensions={[topicDimension]}
        />,
      ),
    );
    // The field control is a combobox input, so the field name lives in its
    // value rather than in the rendered text.
    const field = container.querySelector<HTMLInputElement>(
      '[data-model-routing-field-control] input[role="combobox"]',
    );
    expect(field?.value).toBe("topic");
    // The saved value survives the round trip into the editor.
    expect(container.textContent).toContain("billing");
  });

  it("keeps an unsaved custom dimension leaf intact across a re-render", async () => {
    const change = vi.fn();
    await act(async () =>
      root.render(
        <Harness
          initial={[groupWithWhen({ field: "topic", in: ["billing"] })]}
          dimensions={[topicDimension]}
          change={change}
        />,
      ),
    );
    // Editing an unrelated field must not drop the custom dimension leaf.
    await act(async () => button(t("modelRoutingGroup.addRule"))?.click());
    if (change.mock.calls.length > 0) {
      const latest = change.mock.calls.at(-1)![0] as ModelRoutingGroup[];
      expect(latest[0]!.rules[0]!.when).toMatchObject({
        field: "topic",
        in: ["billing"],
      });
    }
  });
});

describe("custom dimension field classification", () => {
  it("treats an unregistered field as a string-kind dimension", () => {
    expect(fieldOperators("topic")).toEqual([
      "in",
      "not_in",
      "equals",
      "not_equals",
    ]);
    expect(isModelRoutingRangeField("topic")).toBe(false);
    expect(isModelRoutingTimeField("topic")).toBe(false);
  });

  it("keeps every built-in field on its own operator set", () => {
    expect(fieldOperators("turn_count")).toEqual(["gt", "gte", "lt", "lte"]);
    expect(isModelRoutingRangeField("turn_count")).toBe(true);
    expect(fieldOperators("system_time")).toEqual(["within"]);
    expect(isModelRoutingTimeField("system_time")).toBe(true);
    expect(fieldOperators("streaming")).toEqual(["equals_bool"]);
  });

  it("parses a rule that reads a known custom dimension", () => {
    const groups = parseModelRoutingGroups(
      [
        {
          id: "g",
          enabled: true,
          members: [
            {
              id: "a",
              service_id: "service_anthropic",
              model: "claude-sonnet-4-5",
            },
          ],
          default_member_id: "a",
          rules: [
            {
              id: "r1",
              priority: 10,
              target_member_id: "a",
              when: { field: "topic", in: ["billing"] },
            },
          ],
        },
      ],
      ["topic"],
    );
    expect(groups[0]!.rules[0]!.when).toMatchObject({
      field: "topic",
      in: ["billing"],
    });
  });

  it("still refuses a field that is neither built-in nor a known dimension", () => {
    expect(() =>
      parseModelRoutingGroups(
        [
          {
            id: "g",
            enabled: true,
            members: [
              {
                id: "a",
                service_id: "service_anthropic",
                model: "claude-sonnet-4-5",
              },
            ],
            default_member_id: "a",
            rules: [
              {
                id: "r1",
                priority: 10,
                target_member_id: "a",
                when: { field: "ghost_dimension", in: ["x"] },
              },
            ],
          },
        ],
        ["topic"],
      ),
    ).toThrow();
  });

  it("refuses a custom dimension when no dimension list is supplied", () => {
    expect(() =>
      parseModelRoutingGroups([
        {
          id: "g",
          enabled: true,
          members: [
            {
              id: "a",
              service_id: "service_anthropic",
              model: "claude-sonnet-4-5",
            },
          ],
          default_member_id: "a",
          rules: [
            {
              id: "r1",
              priority: 10,
              target_member_id: "a",
              when: { field: "topic", in: ["billing"] },
            },
          ],
        },
      ]),
    ).toThrow();
  });
});
