// @vitest-environment happy-dom
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { applyLocale, i18n } from "@/i18n";
import {
  maxModelRoutingConditionNodes,
  maxModelRoutingListItems,
  modelRoutingPriorityStep,
  parseModelRoutingGroups,
  type ModelRoutingCondition,
  type ModelRoutingGroup,
  type ModelRoutingRule,
} from "@/model-routing-group-model";
import type { RoutableService } from "@/service-model";

import {
  ModelRoutingGroupEditor,
  defaultConditionFor,
} from "./ModelRoutingGroupEditor";

const services: RoutableService[] = [
  {
    id: "service_anthropic",
    name: "Anthropic",
    kind: "anthropic",
    enabled: true,
    models: ["claude-sonnet-4-5", "claude-opus-4-1"],
    capabilities: [],
  },
  {
    id: "service_openai",
    name: "OpenAI",
    kind: "newapi",
    enabled: true,
    models: ["gpt-5", "gpt-5-mini"],
    capabilities: [],
  },
  {
    id: "service_off",
    name: "Disabled",
    kind: "newapi",
    enabled: false,
    models: ["ghost-1"],
    capabilities: [],
  },
] as unknown as RoutableService[];

const t = (key: string, options?: Record<string, unknown>): string =>
  String(i18n.t(key, options as never));

/** 源码级断言：有些回归（如布尔树按钮回来）只会在编辑时被看漏。 */
const readSource = (relativePath: string): string =>
  readFileSync(fileURLToPath(new URL(relativePath, import.meta.url)), "utf8");

describe("ModelRoutingGroupEditor", () => {
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

  function Harness({
    initial = [],
    change = () => {},
    disabled = false,
    editing,
  }: {
    initial?: ModelRoutingGroup[];
    change?: (value: ModelRoutingGroup[]) => void;
    disabled?: boolean;
    editing?: (value: boolean) => void;
  }) {
    const [value, setValue] = useState(initial);
    return (
      <ModelRoutingGroupEditor
        value={value}
        services={services}
        disabled={disabled}
        onEditingChange={editing}
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
  const input = (label: string) =>
    container.querySelector<HTMLInputElement>(`input[aria-label="${label}"]`);
  const code = () =>
    [...container.querySelectorAll("code")].map((node) => node.textContent);

  async function type(label: string, value: string) {
    await act(async () => {
      const target = input(label)!;
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(target, value);
      target.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }

  async function typeContinuously(label: string, value: string) {
    const target = input(label)!;
    target.focus();
    for (const character of value) {
      await act(async () => {
        const active = document.activeElement as HTMLInputElement;
        Object.getOwnPropertyDescriptor(
          HTMLInputElement.prototype,
          "value",
        )!.set!.call(active, `${active.value}${character}`);
        active.dispatchEvent(new Event("input", { bubbles: true }));
      });
    }
    return input(label)!;
  }

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

  async function chooseField(index: number, field: string) {
    const fields = container.querySelectorAll<HTMLInputElement>(
      '[data-model-routing-field-control] input[role="combobox"]',
    );
    await act(async () => fields[index].click());
    await act(async () => {
      document
        .querySelector<HTMLElement>(
          `[role="option"][data-option-value="${field}"]`,
        )!
        .click();
    });
  }

  it("starts from an empty state that explains the virtual model", async () => {
    await act(async () => root.render(<Harness />));
    expect(container.textContent).toContain(t("modelRoutingGroup.empty"));
    expect(container.textContent).toContain(t("modelRoutingGroup.emptyHint"));
    expect(input(t("modelRoutingGroup.searchLabel"))).toBeNull();
  });

  it("adds a group with a free slug and shows the public model ID", async () => {
    const change = vi.fn();
    await act(async () => root.render(<Harness change={change} />));
    await act(async () => button(t("modelRoutingGroup.addGroup"))!.click());
    expect(change).toHaveBeenCalledTimes(1);
    const [groups] = change.mock.calls[0] as [ModelRoutingGroup[]];
    expect(groups).toHaveLength(1);
    expect(groups[0].id).toBe("group-1");
    expect(code()).toContain("astrlink/group-1");

    // A second group must not reuse the first slug.
    await act(async () => button(t("modelRoutingGroup.addGroup"))!.click());
    const [next] = change.mock.calls[1] as [ModelRoutingGroup[]];
    expect(next.map((group) => group.id)).toEqual(["group-1", "group-2"]);
  });

  it("warns about a missing member and a reserved slug", async () => {
    await act(async () =>
      root.render(
        <Harness
          initial={[
            {
              id: "auto",
              enabled: true,
              members: [
                {
                  id: "a",
                  service_id: "service_anthropic",
                  model: "claude-sonnet-4-5",
                },
                { id: "b", service_id: "service_openai", model: "gpt-5" },
              ],
              default_member_id: "a",
              rules: [
                {
                  id: "r1",
                  priority: 10,
                  target_member_id: "a",
                  when: { field: "streaming", equals_bool: true },
                },
                {
                  id: "r2",
                  priority: 10,
                  target_member_id: "b",
                  when: { field: "protocol", in: ["openai.chat"] },
                },
              ],
            },
          ]}
        />,
      ),
    );
    const alerts = () =>
      [...container.querySelectorAll('[role="alert"]')].map(
        (node) => node.textContent,
      );
    expect(alerts()).toContain(t("modelRoutingGroup.slugReserved"));
    // 行序生成 priority = (i+1)*10，重复优先级已不可达：两行都渲染成表行，
    // 而不是报一个用户无法产生的错误。
    expect(
      container.querySelectorAll("[data-model-routing-rule]"),
    ).toHaveLength(2);
    expect(container.textContent).not.toContain(
      t("modelRoutingGroup.priorityDuplicate"),
    );
  });

  it("warns when a group has no members", async () => {
    await act(async () =>
      root.render(
        <Harness
          initial={[
            {
              id: "solo",
              enabled: true,
              members: [],
              default_member_id: "",
              rules: [],
            },
          ]}
        />,
      ),
    );
    const alerts = [...container.querySelectorAll('[role="alert"]')].map(
      (node) => node.textContent,
    );
    expect(alerts).toContain(t("modelRoutingGroup.needMember"));
    expect(container.textContent).toContain(t("modelRoutingGroup.noMembers"));
    expect(container.textContent).toContain(t("modelRoutingGroup.noRules"));
  });

  it("offers only enabled providers when adding a member", async () => {
    await act(async () =>
      root.render(
        <Harness
          initial={[
            {
              id: "solo",
              enabled: true,
              members: [],
              default_member_id: "",
              rules: [],
            },
          ]}
        />,
      ),
    );
    await act(async () => button(t("modelRoutingGroup.addMember"))!.click());
    const service = container.querySelector<HTMLInputElement>(
      `input[role="combobox"][aria-label="${t("modelRoutingGroup.service")}"]`,
    )!;
    await act(async () => service.click());
    const options = [...document.querySelectorAll('[role="option"]')].map(
      (node) => node.textContent?.trim() ?? "",
    );
    expect(options.some((option) => option.startsWith("Anthropic"))).toBe(true);
    // A disabled provider cannot be a routing target.
    expect(options.some((option) => option.startsWith("Disabled"))).toBe(false);
    // The wire value is the service id, but the reader never sees it.
    expect(options.some((option) => option.includes("service_"))).toBe(false);
  });

  it("lowercases typed identifiers and leaves the field on blur", async () => {
    const editing = vi.fn();
    await act(async () =>
      root.render(
        <Harness
          editing={editing}
          initial={[
            {
              id: "solo",
              enabled: true,
              members: [],
              default_member_id: "",
              rules: [],
            },
          ]}
        />,
      ),
    );
    await type(t("modelRoutingGroup.slug"), "Mixed-Case");
    const slug = input(t("modelRoutingGroup.slug"))!;
    expect(slug.value).toBe("mixed-case");
    expect(slug.getAttribute("aria-invalid")).toBe("false");
    expect(editing).toHaveBeenCalledWith(true);
    // React delegates onBlur through focusout, which is what a blur actually emits.
    await act(async () => {
      slug.dispatchEvent(new FocusEvent("focusout", { bubbles: true }));
    });
    expect(editing).toHaveBeenLastCalledWith(false);
  });

  it("asks before deleting a member and repoints rules to a surviving one", async () => {
    const change = vi.fn();
    await act(async () =>
      root.render(
        <Harness
          change={change}
          initial={[
            {
              id: "g",
              enabled: true,
              members: [
                {
                  id: "a",
                  service_id: "service_anthropic",
                  model: "claude-sonnet-4-5",
                },
                { id: "b", service_id: "service_openai", model: "gpt-5" },
              ],
              default_member_id: "a",
              rules: [
                {
                  id: "r1",
                  priority: 10,
                  target_member_id: "a",
                  when: { field: "streaming", equals_bool: true },
                },
                {
                  id: "r2",
                  priority: 20,
                  target_member_id: "b",
                  when: { field: "protocol", in: ["openai.chat"] },
                },
              ],
            },
          ]}
        />,
      ),
    );
    await act(async () =>
      button(t("modelRoutingGroup.removeMember", { name: "a" }))!.click(),
    );
    // Destructive edits go through the shared dialog, never window.confirm.
    // The dialog renders through a portal, so it is outside the editor subtree.
    const dialog = document.querySelector('[role="alertdialog"]')!;
    expect(dialog.textContent).toContain(
      t("modelRoutingGroup.removeMemberTitle"),
    );
    await act(async () => {
      [...dialog.querySelectorAll("button")]
        .find((node) => node.textContent?.trim() === t("common.delete"))!
        .click();
    });
    const [next] = change.mock.calls.at(-1) as [ModelRoutingGroup[]];
    // The default and the dangling rule both move to the surviving member.
    expect(next[0].members.map((member) => member.id)).toEqual(["b"]);
    expect(next[0].default_member_id).toBe("b");
    expect(next[0].rules.map((rule) => rule.id)).toEqual(["r2"]);
    // 删成员带走了 r1，剩下的行必须重算行序：留下 priority 20 会让首行不再是
    // 10，与"拖拽、删除、新增后不会留下空缺"的自述不变式相矛盾。
    expect(next[0].rules.map((rule) => rule.priority)).toEqual([
      modelRoutingPriorityStep,
    ]);
  });

  it("asks before deleting a rule without touching the members", async () => {
    const change = vi.fn();
    await act(async () =>
      root.render(
        <Harness
          change={change}
          initial={[
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
                  when: { field: "streaming", equals_bool: true },
                },
              ],
            },
          ]}
        />,
      ),
    );
    await act(async () =>
      button(t("modelRoutingGroup.removeRule", { name: "r1" }))!.click(),
    );
    const dialog = document.querySelector('[role="alertdialog"]')!;
    await act(async () => {
      [...dialog.querySelectorAll("button")]
        .find((node) => node.textContent?.trim() === t("common.delete"))!
        .click();
    });
    const [next] = change.mock.calls.at(-1) as [ModelRoutingGroup[]];
    expect(next[0].rules).toEqual([]);
    expect(next[0].members).toHaveLength(1);
  });

  it("filters the group strip by group or member model", async () => {
    await act(async () =>
      root.render(
        <Harness
          initial={[
            {
              id: "alpha",
              enabled: true,
              members: [
                {
                  id: "a",
                  service_id: "service_anthropic",
                  model: "claude-sonnet-4-5",
                },
              ],
              default_member_id: "a",
              rules: [],
            },
            {
              id: "beta",
              enabled: false,
              members: [
                { id: "b", service_id: "service_openai", model: "gpt-5" },
              ],
              default_member_id: "b",
              rules: [],
            },
          ]}
        />,
      ),
    );
    const strip = () =>
      [
        ...container.querySelectorAll<HTMLButtonElement>(
          `[role="tablist"][aria-label="${t("modelRoutingGroup.groupsLabel")}"] [role="tab"]`,
        ),
      ].map((node) => node.textContent?.trim());
    expect(strip()).toHaveLength(2);
    await type(t("modelRoutingGroup.searchLabel"), "gpt-5");
    const filtered = strip();
    expect(filtered).toHaveLength(1);
    expect(filtered[0]).toContain("beta");
    expect(container.textContent).not.toContain("astrlink/alpha");
  });

  it("marks a model the provider does not list", async () => {
    await act(async () =>
      root.render(
        <Harness
          initial={[
            {
              id: "g",
              enabled: true,
              members: [
                {
                  id: "a",
                  service_id: "service_anthropic",
                  model: "not-listed",
                },
              ],
              default_member_id: "a",
              rules: [],
            },
          ]}
        />,
      ),
    );
    expect(container.textContent).toContain(
      t("modelRoutingGroup.modelUnlisted"),
    );
  });

  it("shows a provider by name and keeps the wire value as the service id", async () => {
    const change = vi.fn();
    await act(async () =>
      root.render(
        <Harness
          change={change}
          initial={[
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
              rules: [],
            },
          ]}
        />,
      ),
    );
    const service = container.querySelector<HTMLInputElement>(
      `input[role="combobox"][aria-label="${t("modelRoutingGroup.service")}"]`,
    )!;
    expect(service.value).toBe("Anthropic");
    await act(async () => service.click());
    const options = [...document.querySelectorAll('[role="option"]')].map(
      (node) => node.textContent?.trim() ?? "",
    );
    expect(options.some((option) => option.startsWith("Anthropic"))).toBe(true);
    expect(options.some((option) => option.includes("service_"))).toBe(false);
    // Selecting still commits the id the backend expects.
    await act(async () =>
      [...document.querySelectorAll<HTMLElement>('[role="option"]')]
        .find((node) => node.textContent?.includes("OpenAI"))!
        .click(),
    );
    const [next] = change.mock.calls.at(-1) as [ModelRoutingGroup[]];
    expect(next[0].members[0].service_id).toBe("service_openai");
  });

  it("labels rule fields and closed value sets in the reader's language", async () => {
    await act(async () =>
      root.render(
        <Harness
          initial={[
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
                  when: { field: "task_type", in: ["coding"] },
                },
              ],
            },
          ]}
        />,
      ),
    );
    const field = container.querySelector<HTMLInputElement>(
      `input[role="combobox"][aria-label="${t("modelRoutingGroup.field")}"]`,
    )!;
    expect(field.value).toBe(t("modelRouting.fields.task_type"));
    expect(field.value).not.toBe("task_type");
    expect(
      container.querySelector(
        `button[aria-label="${t("modelRouting.fieldSource.classifier")}"]`,
      ),
    ).toBeTruthy();
    expect(
      container.querySelector("[data-multi-combobox-chip]")?.textContent,
    ).toContain(t("modelRouting.values.task_type.coding"));
    expect(
      container.querySelector(
        `button[aria-label="${t("modelRouting.values.task_type.coding")}"]`,
      ),
    ).toBeNull();
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>(
          '[data-model-routing-value-control] [data-slot="multi-combobox"] button[aria-haspopup="listbox"]',
        )!
        .click(),
    );
    expect(
      document.querySelector('[role="option"][data-option-value="research"]'),
    ).toBeTruthy();
  });

  it("renders every explanatory tooltip as a focusable trigger, not inline text", async () => {
    // The nine explanations the interaction review called necessary live in `?`
    // popovers. A regression that pushes one back into body copy spends working
    // height and is invisible to a text assertion, so each is pinned by its
    // trigger's accessible name and by the fact that it opens on click.
    const withTime = groupWithWhen({
      field: "system_time",
      within: ["22:00", "02:00"],
    });
    await act(async () =>
      root.render(
        <Harness
          initial={[
            {
              ...withTime,
              rules: [
                // One rule per leaf shape: the time window and the numeric
                // range each carry their own `?`, so both must be on screen.
                ...withTime.rules,
                {
                  id: "r2",
                  priority: 20,
                  target_member_id: "a",
                  when: { field: "turn_count", gte: 3 },
                },
              ],
            },
          ]}
        />,
      ),
    );

    const expected = [
      "modelRoutingGroup.rulesHintLabel",
      "modelRoutingGroup.priorityDerivedHintLabel",
      "modelRoutingGroup.timeWindowHintLabel",
      "modelRoutingGroup.unknownHintLabel",
      "modelRoutingGroup.rangeBoundsHintLabel",
    ];
    const triggers = expected.map((key) =>
      container.querySelector<HTMLButtonElement>(
        `button[aria-label="${t(key)}"]`,
      ),
    );
    expect(triggers.every((trigger) => trigger !== null)).toBe(true);
    // A button is focusable and exposes the explanation on keyboard activation;
    // an inline <p> would satisfy neither.
    for (const trigger of triggers) {
      expect(trigger!.tagName).toBe("BUTTON");
      expect(trigger!.disabled).toBe(false);
    }

    await act(async () => triggers[2]!.click());
    expect(document.body.textContent).toContain(
      t("modelRoutingGroup.timeWindowTooltipHalfOpen"),
    );
    expect(document.body.textContent).toContain(
      t("modelRoutingGroup.timeWindowTooltipCrossMidnight"),
    );
  });

  it("keeps the unknown-metadata warning reachable without spending row height", async () => {
    await act(async () =>
      root.render(
        <Harness
          initial={[
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
                  when: { field: "streaming", equals_bool: true },
                },
              ],
            },
          ]}
        />,
      ),
    );
    // The sentence is not a paragraph in the layout any more...
    expect(container.textContent).not.toContain(
      t("modelRoutingGroup.unknownHint"),
    );
    // ...but the trigger still announces and opens it.
    const trigger = container.querySelector<HTMLButtonElement>(
      `button[aria-label="${t("modelRoutingGroup.unknownHintLabel")}"]`,
    )!;
    expect(trigger).toBeTruthy();
    await act(async () => trigger.click());
    expect(document.body.textContent).toContain(
      t("modelRoutingGroup.unknownHint"),
    );
  });

  it("keeps the member ID input focused while typing multiple characters", async () => {
    await act(async () =>
      root.render(
        <Harness
          initial={[
            {
              id: "g",
              enabled: true,
              members: [
                {
                  id: "member",
                  service_id: "service_anthropic",
                  model: "claude-sonnet-4-5",
                },
              ],
              default_member_id: "member",
              rules: [],
            },
          ]}
        />,
      ),
    );

    const target = await typeContinuously(
      t("modelRoutingGroup.memberID"),
      "-abc",
    );
    expect(target.value).toBe("member-abc");
    expect(document.activeElement).toBe(target);
  });

  it("keeps the rule ID input focused while typing multiple characters", async () => {
    await act(async () =>
      root.render(
        <Harness
          initial={[groupWithWhen({ field: "streaming", equals_bool: true })]}
        />,
      ),
    );

    const target = await typeContinuously(
      t("modelRoutingGroup.ruleID"),
      "-abc",
    );
    expect(target.value).toBe("r1-abc");
    expect(document.activeElement).toBe(target);
  });

  it.each([
    ["range", { field: "turn_count", min: 1 }],
    ["enum", { field: "task_type", in: ["coding"] }],
  ] as const)("aligns field and %s controls", async (_kind, when) => {
    await act(async () =>
      root.render(<Harness initial={[groupWithWhen(when)]} />),
    );
    const field = container.querySelector<HTMLElement>(
      "[data-model-routing-field-control]",
    )!;
    const value = container.querySelector<HTMLElement>(
      "[data-model-routing-value-control]",
    )!;
    expect(
      Math.abs(
        field.getBoundingClientRect().top - value.getBoundingClientRect().top,
      ),
    ).toBeLessThanOrEqual(1);
  });

  it("flattens a nested all row into AND pills and writes a parser-accepted shape", async () => {
    const change = vi.fn();
    await act(async () =>
      root.render(
        <Harness
          change={change}
          initial={[
            groupWithWhen({
              all: [
                { field: "task_type", in: ["coding"] },
                { field: "task_type", in: ["research"] },
              ],
            }),
          ]}
        />,
      ),
    );

    // A flat all-of row shows one pill per leaf, with no AND/OR buttons.
    expect(
      container.querySelectorAll("[data-model-routing-condition-node]"),
    ).toHaveLength(2);
    expect(
      container.querySelector(
        '[data-model-routing-condition-node="all"],' +
          '[data-model-routing-condition-node="any"],' +
          "[data-model-routing-add-condition][data-model-routing-wrap]",
      ),
    ).toBeNull();

    // Turn the second pill into not C: negation is a per-pill toggle, and the
    // wire shape stays a single `all` whose second child is wrapped in `not`.
    const pills = container.querySelectorAll<HTMLElement>(
      "[data-model-routing-condition-node]",
    );
    await act(async () => {
      pills[1]
        .querySelector<HTMLButtonElement>("[data-model-routing-pill-negate]")!
        .click();
    });

    const [next] = change.mock.calls.at(-1) as [ModelRoutingGroup[]];
    expect(next[0].rules[0].when).toEqual({
      all: [
        { field: "task_type", in: ["coding"] },
        { not: { field: "task_type", in: ["research"] } },
      ],
    });
    // The value edit above is complete, so the row still parses.
    expect(parseModelRoutingGroups(next)).toEqual(next);
  });

  it("offers no AND/OR switch on a legacy any-of row", async () => {
    const change = vi.fn();
    await act(async () =>
      root.render(
        <Harness
          change={change}
          initial={[
            groupWithWhen({
              any: [
                { field: "task_type", in: ["coding"] },
                { field: "task_type", in: ["research"] },
              ],
            }),
          ]}
        />,
      ),
    );
    // 行内恒 AND 之后，"组合为 OR" 这个开关被拿掉了：旧的 any 规则只读可见，
    // 用户不能把一个 OR 行留在决策表里继续编辑。
    expect(
      container.querySelector("[data-model-routing-branch-operator]"),
    ).toBeNull();
    expect(
      container.querySelector("[data-model-routing-branch-operator-value]"),
    ).toBeNull();
    expect(change).not.toHaveBeenCalled();
  });

  it("shows a deeply nested legacy tree read-only instead of offering tree edits", async () => {
    const when: ModelRoutingCondition = {
      not: {
        not: {
          not: { field: "streaming", equals_bool: true },
        },
      },
    };
    await act(async () =>
      root.render(<Harness initial={[groupWithWhen(when)]} />),
    );
    // 深度嵌套无法用"行内 AND 叶子"表达，所以走只读回退：原样可见，
    // 但没有任何新增/取反/组合为的控件。
    expect(
      container.querySelector("[data-model-routing-row-unrepresentable]"),
    ).toBeTruthy();
    expect(
      container.querySelectorAll("[data-model-routing-condition-summary]")
        .length,
    ).toBeGreaterThan(0);
    expect(container.querySelector("[data-model-routing-wrap]")).toBeNull();
    expect(
      container.querySelector("[data-model-routing-add-condition]"),
    ).toBeNull();
  });

  it("collapses a 64-node legacy tree into read-only summaries without a node budget", async () => {
    const leaf = (): ModelRoutingCondition => ({
      field: "streaming",
      equals_bool: true,
    });
    const ten = (): ModelRoutingCondition => ({
      all: Array.from({ length: 9 }, leaf),
    });
    const twentyOne = (): ModelRoutingCondition => ({
      any: [ten(), ten()],
    });
    const when: ModelRoutingCondition = {
      all: [twentyOne(), twentyOne(), twentyOne()],
    };
    await act(async () =>
      root.render(<Harness initial={[groupWithWhen(when)]} />),
    );
    // 不再有节点预算要显示：整棵树只落成一行只读摘要。
    expect(
      container.querySelector("[data-model-routing-row-unrepresentable]"),
    ).toBeTruthy();
    expect(container.textContent).not.toContain(
      t("modelRoutingGroup.conditionNodeLimit", {
        max: maxModelRoutingConditionNodes,
      }),
    );
  });

  it("renders a 16-leaf row as 16 AND pills without a branch wrapper", async () => {
    const when: ModelRoutingCondition = {
      all: Array.from({ length: maxModelRoutingListItems }, () => ({
        field: "streaming",
        equals_bool: true,
      })),
    };
    await act(async () =>
      root.render(<Harness initial={[groupWithWhen(when)]} />),
    );
    expect(
      container.querySelectorAll('[data-model-routing-condition-node="leaf"]'),
    ).toHaveLength(maxModelRoutingListItems);
    // 行内恒 AND，所以没有分支节点，也没有第二条加法路径。
    expect(
      container.querySelector("[data-model-routing-branch-operator]"),
    ).toBeNull();
  });

  it("deletes a pill and collapses the row to a single leaf", async () => {
    const change = vi.fn();
    await act(async () =>
      root.render(
        <Harness
          change={change}
          initial={[
            groupWithWhen({
              all: [
                { field: "task_type", in: ["coding"] },
                { field: "streaming", equals_bool: true },
              ],
            }),
          ]}
        />,
      ),
    );
    const pills = container.querySelectorAll<HTMLElement>(
      "[data-model-routing-condition-node]",
    );
    await act(async () =>
      pills[0]
        .querySelector<HTMLButtonElement>(
          `button[aria-label="${t("modelRoutingGroup.removeCondition")}"]`,
        )!
        .click(),
    );
    const [next] = change.mock.calls.at(-1) as [ModelRoutingGroup[]];
    // 只剩一个叶子时不再包 all —— 空数组才是非法形状。
    expect(next[0].rules[0].when).toEqual({
      field: "streaming",
      equals_bool: true,
    });
    expect(parseModelRoutingGroups(next)).toEqual(next);
  });

  it("keeps a legacy any-of rule visible as an unrepresentable row", async () => {
    await act(async () =>
      root.render(
        <Harness
          initial={[
            groupWithWhen({
              any: [
                { field: "task_type", in: ["coding"] },
                { field: "task_type", in: ["research"] },
              ],
            }),
          ]}
        />,
      ),
    );
    // 旧格式不能被静默改写成"行内 AND"，所以它走只读回退并明确说明。
    expect(
      container.querySelector("[data-model-routing-row-unrepresentable]"),
    ).toBeTruthy();
    expect(container.textContent).toContain(
      t("modelRoutingGroup.rowUnrepresentable"),
    );
    expect(
      container.querySelector("[data-model-routing-condition-pills]"),
    ).toBeNull();
  });

  it("puts enum values in a dropdown and appends multiple in-values", async () => {
    const change = vi.fn();
    await act(async () =>
      root.render(
        <Harness
          change={change}
          initial={[groupWithWhen({ field: "task_type", in: ["general"] })]}
        />,
      ),
    );

    expect(
      document.querySelector('[role="option"][data-option-value="coding"]'),
    ).toBeNull();
    const chooseValue = async (value: string) => {
      // 取值控件是组合框内的多选列表；行首的操作符下拉也有 listbox 语义，
      // 所以必须按 `data-slot` 限定到多选控件本身。点输入框只展开、不收起，
      // 因此连续加值不会把上一步还开着的列表关掉。
      await act(async () =>
        container
          .querySelector<HTMLInputElement>(
            '[data-model-routing-value-control] [data-slot="multi-combobox"] input[role="combobox"]',
          )!
          .click(),
      );
      await act(async () =>
        document
          .querySelector<HTMLElement>(
            `[role="option"][data-option-value="${value}"]`,
          )!
          .click(),
      );
    };

    await chooseValue("coding");
    await chooseValue("research");

    const [next] = change.mock.calls.at(-1) as [ModelRoutingGroup[]];
    expect(next[0].rules[0].when).toEqual({
      field: "task_type",
      in: ["general", "coding", "research"],
    });
    expect(
      container.querySelectorAll("[data-multi-combobox-chip]"),
    ).toHaveLength(3);
    expect(container.querySelector('[data-option-value="coding"]')).toBeNull();
  });

  it("loads declared task_type labels into that group's value candidates", async () => {
    const group = {
      ...groupWithWhen({ field: "task_type", in: ["general"] }),
      task_type_labels: ["general", "custom-review"],
    };
    await act(async () => root.render(<Harness initial={[group]} />));

    await act(async () =>
      container
        .querySelector<HTMLInputElement>(
          '[data-model-routing-value-control] [data-slot="multi-combobox"] input[role="combobox"]',
        )!
        .click(),
    );
    expect(
      document.querySelector(
        '[role="option"][data-option-value="custom-review"]',
      ),
    ).toBeTruthy();
  });

  it("warns that a group custom task_type label will not match with the offline classifier", async () => {
    const group = {
      ...groupWithWhen({ field: "task_type", in: ["storyboard"] }),
      task_type_labels: ["general", "storyboard"],
    };
    await act(async () => root.render(<Harness initial={[group]} />));

    // The warning lives behind the value list's `?`, not as a paragraph that
    // squeezes the value column into a narrow, tall strip.
    expect(
      container.querySelector("[data-model-routing-custom-hint]"),
    ).toBeNull();
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>(
          `button[aria-label="${t("modelRoutingGroup.fieldValueListHintLabel")}"]`,
        )!
        .click(),
    );
    const hint = document.querySelector<HTMLElement>(
      "[data-model-routing-custom-hint]",
    );
    expect(hint?.textContent).toContain("内置离线分类器不会产出");
    expect(hint?.textContent).toContain("依赖这些标签的规则不会命中");
  });

  it("allows creating task_type values and persists the group label set", async () => {
    const change = vi.fn();
    await act(async () =>
      root.render(
        <Harness
          change={change}
          initial={[groupWithWhen({ field: "task_type", in: ["coding"] })]}
        />,
      ),
    );
    const target = await typeContinuously(
      t("modelRoutingGroup.valueList"),
      "custom-review",
    );
    await act(async () =>
      target.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
      ),
    );

    const [next] = change.mock.calls.at(-1) as [ModelRoutingGroup[]];
    expect(next[0].rules[0].when).toEqual({
      field: "task_type",
      in: ["coding", "custom-review"],
    });
    expect(next[0].task_type_labels).toEqual([
      "general",
      "research",
      "coding",
      "architect",
      "custom-review",
    ]);
  });

  it("blocks new task_type labels when the group vocabulary is full", async () => {
    const change = vi.fn();
    const fullLabels = [
      "general",
      "research",
      "coding",
      "architect",
      ...Array.from({ length: 12 }, (_, index) => `custom-${index}`),
    ];
    await act(async () =>
      root.render(
        <Harness
          change={change}
          initial={[
            {
              ...groupWithWhen({ field: "task_type", in: ["coding"] }),
              task_type_labels: fullLabels,
            },
          ]}
        />,
      ),
    );

    expect(
      container.querySelector("[data-model-routing-task-type-label-limit]"),
    ).toBeTruthy();
    const target = await typeContinuously(
      t("modelRoutingGroup.valueList"),
      "overflow-label",
    );
    await act(async () =>
      target.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
      ),
    );

    expect(change).not.toHaveBeenCalled();
  });

  it("keeps contains_any custom keywords as an appendable array", async () => {
    const change = vi.fn();
    await act(async () =>
      root.render(
        <Harness
          change={change}
          initial={[
            groupWithWhen({ field: "literal_keyword", contains_any: ["*"] }),
          ]}
        />,
      ),
    );

    const keywords = t("modelRoutingGroup.keywords");
    const addKeyword = async (value: string) => {
      const target = await typeContinuously(keywords, value);
      await act(async () =>
        target.dispatchEvent(
          new KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
        ),
      );
    };
    await addKeyword("todo");
    await addKeyword("bug");

    const [next] = change.mock.calls.at(-1) as [ModelRoutingGroup[]];
    expect(next[0].rules[0].when).toEqual({
      field: "literal_keyword",
      contains_any: ["*", "todo", "bug"],
    });
  });

  it("disables the value dropdown and explains the list limit", async () => {
    await act(async () =>
      root.render(
        <Harness
          initial={[
            groupWithWhen({
              field: "task_type",
              in: Array.from(
                { length: maxModelRoutingListItems },
                (_, index) => `value-${index}`,
              ),
            }),
          ]}
        />,
      ),
    );

    const valueInput = container.querySelector<HTMLInputElement>(
      '[data-model-routing-value-control] [data-slot="multi-combobox"] input[role="combobox"]',
    )!;
    expect(valueInput.disabled).toBe(true);
    expect(container.textContent).toContain(
      t("modelRoutingGroup.listLimitReached", {
        max: maxModelRoutingListItems,
      }),
    );
  });

  it("starts new keyword and enum conditions empty without a sentinel", async () => {
    await act(async () =>
      root.render(
        <Harness
          initial={[groupWithWhen({ field: "streaming", equals_bool: true })]}
        />,
      ),
    );
    // 新增条件走行内 pill，不再有「组合为 AND」这一步。
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>("[data-model-routing-add-condition]")!
        .click(),
    );
    await chooseField(1, "literal_keyword");
    expect(
      container.querySelectorAll("[data-multi-combobox-chip]"),
    ).toHaveLength(0);
    expect(container.textContent).not.toContain("*");
  });

  it("keeps V1 field shapes when the operator changes field", () => {
    // A range field must not keep an `in` list, and vice versa.
    expect(defaultConditionFor("turn_count")).toEqual({
      field: "turn_count",
      gte: 0,
    });
    expect(defaultConditionFor("streaming")).toEqual({
      field: "streaming",
      equals_bool: true,
    });
    expect(defaultConditionFor("literal_keyword")).toEqual({
      field: "literal_keyword",
      contains_any: [],
    });
    // Enum and open-vocabulary conditions start as incomplete drafts.
    for (const field of ["task_type", "media_kind", "protocol"] as const) {
      expect(defaultConditionFor(field)).toEqual({ field, in: [] });
    }
  });
});

describe("ModelRoutingGroupEditor 决策表", () => {
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

  function Harness({
    initial = [],
    change = () => {},
  }: {
    initial?: ModelRoutingGroup[];
    change?: (value: ModelRoutingGroup[]) => void;
  }) {
    const [value, setValue] = useState(initial);
    return (
      <ModelRoutingGroupEditor
        value={value}
        services={services}
        onChange={(next) => {
          setValue(next);
          change(next);
        }}
      />
    );
  }

  function groupWith(rules: ModelRoutingRule[]): ModelRoutingGroup {
    return {
      id: "g",
      enabled: true,
      members: [
        {
          id: "a",
          service_id: "service_anthropic",
          model: "claude-sonnet-4-5",
        },
        { id: "b", service_id: "service_openai", model: "gpt-5" },
      ],
      default_member_id: "a",
      rules,
    };
  }

  const rule = (
    id: string,
    priority: number,
    when: ModelRoutingCondition,
    target = "a",
  ): ModelRoutingRule => ({
    id,
    priority,
    target_member_id: target,
    when,
  });

  const code = () =>
    [...container.querySelectorAll("code")].map((node) => node.textContent);

  it("shows no boolean-tree editing affordances anywhere in the file", () => {
    // 扁平决策表的支点：行内恒 AND、行间恒 OR、行序即优先级。这三样一旦
    // 重新出现，用户又得在脑子里做一次编译，本任务就白做了。
    const source = readSource("../components/ModelRoutingGroupEditor.tsx");
    expect(source).not.toContain("conditionGroupAs");
    expect(source).not.toContain('data-model-routing-branch-operator"');
    expect(source).not.toMatch(/modelRoutingGroup\.conditionAll\b/);
    expect(source).not.toMatch(/modelRoutingGroup\.conditionAny\b/);
    expect(source).not.toContain('data-model-routing-wrap="all"');
    expect(source).not.toContain('data-model-routing-wrap="any"');
  });

  it("renders each rule as a table row with the derived priority and no number box", async () => {
    await act(async () =>
      root.render(
        <Harness
          initial={[
            groupWith([
              rule("r1", 10, {
                all: [
                  { field: "task_type", in: ["coding"] },
                  { field: "turn_count", gte: 3 },
                ],
              }),
              rule("r2", 20, { field: "streaming", equals_bool: true }, "b"),
            ]),
          ]}
        />,
      ),
    );

    const rows = container.querySelectorAll("[data-model-routing-rule]");
    expect(rows).toHaveLength(2);
    expect(container.querySelectorAll("tbody tr")).toHaveLength(2);

    // priority 只读展示，且与行序一致。
    const priorities = [
      ...container.querySelectorAll("[data-model-routing-priority-value]"),
    ].map((node) => node.textContent);
    expect(priorities).toEqual(["10", "20"]);
    expect(
      container.querySelector(
        'input[type="number"][aria-label="' +
          t("modelRoutingGroup.priority") +
          '"]',
      ),
    ).toBeNull();

    // 行内两枚 AND 条件，没有分支包装节点。
    expect(
      rows[0].querySelectorAll("[data-model-routing-condition-node]"),
    ).toHaveLength(2);
    expect(
      rows[0].querySelector("[data-model-routing-branch-operator]"),
    ).toBeNull();
  });

  it("derives priority as (i+1)*10 on reorder and keeps wire rule IDs stable", async () => {
    const change = vi.fn();
    await act(async () =>
      root.render(
        <Harness
          change={change}
          initial={[
            groupWith([
              rule("r1", 10, { field: "streaming", equals_bool: true }),
              rule("r2", 20, { field: "protocol", in: ["openai.chat"] }, "b"),
            ]),
          ]}
        />,
      ),
    );

    // 把第 2 行上移：优先级按新行序重算，ID 跟着行走（不是被重命名）。
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>('[data-model-routing-move-up="1"]')!
        .click(),
    );

    const [next] = change.mock.calls.at(-1) as [ModelRoutingGroup[]];
    expect(next[0].rules.map((item) => item.id)).toEqual(["r2", "r1"]);
    expect(next[0].rules.map((item) => item.priority)).toEqual([
      modelRoutingPriorityStep,
      modelRoutingPriorityStep * 2,
    ]);
    // 重排只改顺序与派生优先级，条件本体原样跟着走。
    expect(next[0].rules[0].when).toEqual({
      field: "protocol",
      in: ["openai.chat"],
    });
    expect(next[0].rules[0].target_member_id).toBe("b");
    expect(parseModelRoutingGroups(next)).toEqual(next);
  });

  it("moves a row with the keyboard handle the same way as the mouse drag", async () => {
    const change = vi.fn();
    await act(async () =>
      root.render(
        <Harness
          change={change}
          initial={[
            groupWith([
              rule("r1", 10, { field: "streaming", equals_bool: true }),
              rule("r2", 20, { field: "protocol", in: ["openai.chat"] }, "b"),
            ]),
          ]}
        />,
      ),
    );

    const handle = container.querySelector<HTMLElement>(
      '[data-model-routing-drag-handle="1"]',
    )!;
    // 句柄必须可聚焦，否则键盘用户没有等价路径（§10.2 第 7 条）。
    expect(handle.tabIndex).toBe(0);
    await act(async () => {
      handle.dispatchEvent(
        new KeyboardEvent("keydown", {
          key: "ArrowUp",
          bubbles: true,
          cancelable: true,
        }),
      );
    });

    const [next] = change.mock.calls.at(-1) as [ModelRoutingGroup[]];
    expect(next[0].rules.map((item) => item.id)).toEqual(["r2", "r1"]);
    expect(next[0].rules.map((item) => item.priority)).toEqual([10, 20]);
  });

  it("reorders on drop and recomputes priority from the new row order", async () => {
    const change = vi.fn();
    await act(async () =>
      root.render(
        <Harness
          change={change}
          initial={[
            groupWith([
              rule("r1", 10, { field: "streaming", equals_bool: true }),
              rule("r2", 20, { field: "protocol", in: ["openai.chat"] }, "b"),
              rule("r3", 30, { field: "has_tools", equals_bool: true }),
            ]),
          ]}
        />,
      ),
    );

    const handles = container.querySelectorAll<HTMLElement>(
      "[data-model-routing-drag-handle]",
    );
    const rows = container.querySelectorAll<HTMLElement>(
      "[data-model-routing-rule]",
    );
    const dataTransfer = {
      data: new Map<string, string>(),
      setData(type: string, value: string) {
        this.data.set(type, value);
      },
      getData(type: string) {
        return this.data.get(type) ?? "";
      },
      effectAllowed: "",
    };
    await act(async () => {
      // dragstart 挂在句柄上（只有句柄可拖），drop 挂在落点行上。
      handles[2].dispatchEvent(
        Object.assign(
          new Event("dragstart", { bubbles: true, cancelable: true }),
          { dataTransfer },
        ),
      );
      rows[0].dispatchEvent(
        Object.assign(new Event("drop", { bubbles: true, cancelable: true }), {
          dataTransfer,
        }),
      );
    });

    const [next] = change.mock.calls.at(-1) as [ModelRoutingGroup[]];
    expect(next[0].rules.map((item) => item.id)).toEqual(["r3", "r1", "r2"]);
    expect(next[0].rules.map((item) => item.priority)).toEqual([10, 20, 30]);
    expect(parseModelRoutingGroups(next)).toEqual(next);
  });

  it("starts a new rule from three preset rows instead of an empty table", async () => {
    const change = vi.fn();
    await act(async () =>
      root.render(
        <Harness
          change={change}
          initial={[
            groupWith([
              rule("r1", 10, { field: "streaming", equals_bool: true }),
            ]),
          ]}
        />,
      ),
    );
    await act(async () =>
      [...container.querySelectorAll<HTMLButtonElement>("button")]
        .find(
          (element) =>
            element.textContent?.trim() === t("modelRoutingGroup.addRule"),
        )!
        .click(),
    );
    const [next] = change.mock.calls.at(-1) as [ModelRoutingGroup[]];
    // 一次点击只加一行，预选 task_type，priority 继续按行序排。
    expect(next[0].rules).toHaveLength(2);
    expect((next[0].rules[1].when as { field: string }).field).toBe(
      "task_type",
    );
    expect(next[0].rules.map((item) => item.priority)).toEqual([10, 20]);
    // 新草稿 ID 不与既有规则重复，否则 wire 上就是两条同名规则。
    expect(new Set(next[0].rules.map((item) => item.id)).size).toBe(2);
  });

  it("moves a row's winner by changing its order (H3 row-order baseline)", async () => {
    // 第 1 行更窄（含打分字段 task_type），第 2 行更宽。行序 first-match 意味着
    // 更窄的行只有在它真的命中时才抢走请求；一旦把它拖到后面，宽行先命中。
    const scoring: ModelRoutingRule = rule("scoring", 10, {
      all: [
        { field: "task_type", in: ["research"] },
        { field: "turn_count", gte: 3 },
      ],
    });
    const broad: ModelRoutingRule = rule(
      "broad",
      20,
      {
        field: "streaming",
        equals_bool: true,
      },
      "b",
    );
    const change = vi.fn();
    await act(async () =>
      root.render(
        <Harness change={change} initial={[groupWith([scoring, broad])]} />,
      ),
    );

    // 先钉住点击前的行序：下面的"翻转"结论全靠这个初始状态，若夹具顺序被
    // 无意改动，用例会继续变绿但验证的东西已经变了。
    expect(
      [
        ...container.querySelectorAll<HTMLElement>(
          "[data-model-routing-priority-value]",
        ),
      ].map((node) => Number(node.dataset.modelRoutingPriorityValue)),
    ).toEqual([modelRoutingPriorityStep, modelRoutingPriorityStep * 2]);
    expect(
      [...container.querySelectorAll<HTMLElement>("[data-model-routing-rule]")]
        .map((row) =>
          row.querySelector<HTMLInputElement>(
            `input[aria-label="${t("modelRoutingGroup.ruleID")}"]`,
          ),
        )
        .map((node) => node?.value),
    ).toEqual(["scoring", "broad"]);

    // 先把宽行上移，让它成为第一行：winner 随之改变。
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>('[data-model-routing-move-up="1"]')!
        .click(),
    );
    const [next] = change.mock.calls.at(-1) as [ModelRoutingGroup[]];
    expect(next[0].rules.map((item) => item.id)).toEqual(["broad", "scoring"]);
    // first-match 的前提是 priority 与行序严格同向：引擎按 priority 升序取
    // 首个命中行，所以行序翻转必须让 priority 跟着翻转，否则"拖动改优先级"
    // 只是视觉谎言。这里断言到具体数值，而不只是相对大小。
    expect(next[0].rules.map((item) => item.priority)).toEqual([
      modelRoutingPriorityStep,
      modelRoutingPriorityStep * 2,
    ]);
    expect(next[0].rules[0].when).toEqual({
      field: "streaming",
      equals_bool: true,
    });
    // 行内仍是 AND：打分行被压到第二行后，它的两个叶子必须还捆在同一个 all 里，
    // 否则"只有低分候选能使本行成立"这个前提就没了。
    expect(next[0].rules[1].when).toEqual({
      all: [
        { field: "task_type", in: ["research"] },
        { field: "turn_count", gte: 3 },
      ],
    });
    // 翻转后的顺序必须仍是可解析的 wire 形状（引擎能直接吃）。
    expect(parseModelRoutingGroups(next)).toEqual(next);
  });

  it("keeps a legacy any-of row read-only instead of silently rewriting it", async () => {
    await act(async () =>
      root.render(
        <Harness
          initial={[
            groupWith([
              rule("legacy", 10, {
                any: [
                  { field: "task_type", in: ["coding"] },
                  { field: "task_type", in: ["research"] },
                ],
              }),
            ]),
          ]}
        />,
      ),
    );
    expect(
      container.querySelector("[data-model-routing-row-unrepresentable]"),
    ).toBeTruthy();
    expect(container.textContent).toContain(
      t("modelRoutingGroup.rowUnrepresentable"),
    );
    // 原本的 any 语义必须还在屏幕上，不能静默变成 AND。
    expect(
      container.querySelector('[data-model-routing-condition-summary="any"]'),
    ).toBeTruthy();
    expect(code()).toContain("astrlink/g");
  });
});

describe("ModelRoutingGroupEditor 第二轮控件", () => {
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

  function Harness({
    initial = [],
    change = () => {},
  }: {
    initial?: ModelRoutingGroup[];
    change?: (value: ModelRoutingGroup[]) => void;
  }) {
    const [value, setValue] = useState(initial);
    return (
      <ModelRoutingGroupEditor
        value={value}
        services={services}
        onChange={(next) => {
          setValue(next);
          change(next);
        }}
      />
    );
  }

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
      rules: [{ id: "r1", priority: 10, target_member_id: "a", when }],
    };
  }

  const lastWhen = (change: ReturnType<typeof vi.fn>) => {
    const [next] = change.mock.calls.at(-1) as [ModelRoutingGroup[]];
    return next[0].rules[0].when;
  };

  /** 点击某个 `aria-label` 的下拉触发器后选中列表里的候选项。 */
  async function pick(controlLabel: string, optionValue: string) {
    await act(async () => {
      container
        .querySelector<HTMLButtonElement>(
          `button[aria-haspopup="listbox"][aria-label="${controlLabel}"]`,
        )!
        .click();
    });
    await act(async () => {
      document
        .querySelector<HTMLElement>(
          `[role="option"][data-option-value="${optionValue}"]`,
        )!
        .click();
    });
  }

  it("只给一侧边界时仍可写出区间，且不会丢边界", async () => {
    const change = vi.fn();
    await act(async () =>
      root.render(
        <Harness
          change={change}
          initial={[groupWithWhen({ field: "turn_count", gte: 2 })]}
        />,
      ),
    );
    // 单侧边界不给删除按钮，避免把条件删成 wire 非法形状。
    expect(
      container.querySelector('[data-model-routing-range-side="lower"]'),
    ).toBeTruthy();
    expect(
      container.querySelector('[data-model-routing-add-bound="upper"]'),
    ).toBeTruthy();

    await act(async () => {
      container
        .querySelector<HTMLButtonElement>(
          '[data-model-routing-add-bound="upper"]',
        )!
        .click();
    });
    expect(lastWhen(change)).toEqual({ field: "turn_count", gte: 2, lte: 0 });
  });

  it("区间操作符下拉显示中文标签，且封闭枚举不给清除按钮", async () => {
    await act(async () =>
      root.render(
        <Harness initial={[groupWithWhen({ field: "turn_count", gte: 2 })]} />,
      ),
    );
    const lower = container.querySelector(
      '[data-model-routing-range-side="lower"]',
    )!;
    const operator = lower.querySelector<HTMLInputElement>(
      'input[aria-label="条件组合方式"]',
    )!;
    // 之前 w-24 加上强制的前置图标与清除按钮，把标签挤没了。
    expect(operator.value).toBe("不小于（>=）");
    // 清除会写出 `""` —— 一个读不回来的操作符键，所以封闭枚举不提供清除。
    expect(lower.querySelector('[aria-label="清除搜索"]')).toBeNull();
  });

  it("读旧 min/max 文档时展示成闭区间并写出新键", async () => {
    const change = vi.fn();
    await act(async () =>
      root.render(
        <Harness
          change={change}
          initial={[groupWithWhen({ field: "turn_count", gte: 2, lte: 8 })]}
        />,
      ),
    );
    const lower = container.querySelector<HTMLInputElement>(
      'input[aria-label="下界"]',
    )!;
    expect(lower.value).toBe("2");
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(lower, "3");
      lower.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(lastWhen(change)).toEqual({ field: "turn_count", gte: 3, lte: 8 });
  });

  it("切换比较操作符会改写边界键，而不是留两个下界", async () => {
    const change = vi.fn();
    await act(async () =>
      root.render(
        <Harness
          change={change}
          initial={[groupWithWhen({ field: "turn_count", gte: 2 })]}
        />,
      ),
    );
    await pick(t("modelRoutingGroup.conditionOperator"), "gt");
    expect(lastWhen(change)).toEqual({ field: "turn_count", gt: 2 });
  });

  it("枚举字段可以在包含与排除之间切换并保留取值", async () => {
    const change = vi.fn();
    await act(async () =>
      root.render(
        <Harness
          change={change}
          initial={[groupWithWhen({ field: "task_type", in: ["coding"] })]}
        />,
      ),
    );
    await pick(t("modelRoutingGroup.conditionOperator"), "not_in");
    expect(lastWhen(change)).toEqual({
      field: "task_type",
      not_in: ["coding"],
    });
    // 单选型只留首项。
    await pick(t("modelRoutingGroup.conditionOperator"), "not_equals");
    expect(lastWhen(change)).toEqual({
      field: "task_type",
      not_equals: "coding",
    });
  });

  it("关键词字段支持不包含任意，并写回数组", async () => {
    const change = vi.fn();
    await act(async () =>
      root.render(
        <Harness
          change={change}
          initial={[
            groupWithWhen({ field: "literal_keyword", contains_any: ["todo"] }),
          ]}
        />,
      ),
    );
    await pick(t("modelRoutingGroup.conditionOperator"), "not_contains_any");
    expect(lastWhen(change)).toEqual({
      field: "literal_keyword",
      not_contains_any: ["todo"],
    });
  });

  it("布尔字段没有操作符列，只有是否下拉", async () => {
    const change = vi.fn();
    await act(async () =>
      root.render(
        <Harness
          change={change}
          initial={[groupWithWhen({ field: "streaming", equals_bool: true })]}
        />,
      ),
    );
    const control = container.querySelector<HTMLElement>(
      "[data-model-routing-value-row]",
    );
    // 布尔叶子没有取值行，只有一个下拉控件。
    expect(control).toBeNull();
    await pick(t("modelRoutingGroup.booleanValue"), "false");
    expect(lastWhen(change)).toEqual({
      field: "streaming",
      equals_bool: false,
    });
  });

  it("AND/OR 开关已经不存在，行内恒 AND", async () => {
    const change = vi.fn();
    await act(async () =>
      root.render(
        <Harness
          change={change}
          initial={[
            groupWithWhen({
              all: [
                { field: "task_type", in: ["coding"] },
                { field: "task_type", in: ["research"] },
              ],
            }),
          ]}
        />,
      ),
    );
    // 扁平决策表的支点：行内恒 AND、行间恒 OR。AND/OR 选择器一旦回来，
    // 用户又得在脑子里做一次编译。
    expect(
      container.querySelector("[data-model-routing-branch-operator]"),
    ).toBeNull();
    expect(
      container.querySelector("[data-model-routing-branch-operator-value]"),
    ).toBeNull();
    // 两枚叶子仍然各自可编辑。
    expect(
      container.querySelectorAll("[data-model-routing-condition-node]"),
    ).toHaveLength(2);
  });

  it("NOT 用行内 pill 的表达，不再是虚线红框节点", async () => {
    await act(async () =>
      root.render(
        <Harness
          initial={[
            groupWithWhen({
              not: { field: "streaming", equals_bool: true },
            }),
          ]}
        />,
      ),
    );
    // 单叶子取反仍然是这一行的条件，只是那一枚 pill 处于按下状态。
    const pill = container.querySelector<HTMLElement>(
      '[data-model-routing-condition-node="leaf"]',
    )!;
    const toggle = pill.querySelector<HTMLButtonElement>(
      "[data-model-routing-pill-negate]",
    )!;
    expect(toggle.getAttribute("aria-pressed")).toBe("true");
    expect(pill.className).toContain("border-destructive");
    expect(
      container.querySelector('[data-model-routing-condition-node="not"]'),
    ).toBeNull();
  });

  // `protocol` 现在是封闭取值域，所以这条测试改用仍然开放词汇的
  // `literal_keyword`：被测的是「新建值」这条路径，而不是某个字段名。
  it("开放词汇字段的新建值会写进条件取值", async () => {
    const change = vi.fn();
    await act(async () =>
      root.render(
        <Harness
          change={change}
          initial={[groupWithWhen({ field: "literal_keyword", in: ["*"] })]}
        />,
      ),
    );
    const box = container.querySelector<HTMLInputElement>(
      '[data-slot="multi-combobox"] input[role="combobox"]',
    )!;
    box.focus();
    for (const character of "ja") {
      await act(async () => {
        const active = document.activeElement as HTMLInputElement;
        Object.getOwnPropertyDescriptor(
          HTMLInputElement.prototype,
          "value",
        )!.set!.call(active, `${active.value}${character}`);
        active.dispatchEvent(new Event("input", { bubbles: true }));
      });
    }
    await act(async () => {
      (document.activeElement as HTMLInputElement).dispatchEvent(
        new KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
      );
    });
    expect(lastWhen(change)).toEqual({
      field: "literal_keyword",
      in: ["*", "ja"],
    });
  });
});
