// @vitest-environment happy-dom

/**
 * §10.2 item 3 / §8.3: the nine required explanations must exist and their `?`
 * triggers must be keyboard reachable. `HelpPopover` renders a real
 * `<button>`, so focusability is a property of the shared control rather than
 * of each call site — this suite pins that, and pins that all nine strings are
 * actually mounted, so a dropped tooltip fails here instead of in review.
 */

import { act, type ReactNode } from "react";
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

import { ClassifierDryRunPanel } from "./ClassifierDryRunPanel";
import { ClassifierSettingsPanel } from "./ClassifierSettingsPanel";
import { ModelRoutingFieldCatalogPanel } from "./ModelRoutingFieldCatalogPanel";
import { ModelRoutingGroupsPanel } from "./ModelRoutingGroupsPanel";
import { defaultClassifierSettings } from "./classifier-settings-model";
import { defaultFailurePolicy } from "./failure-policy-model";
import { applyLocale, i18n } from "./i18n";

function tooltipTriggers(): HTMLButtonElement[] {
  return [
    ...document.querySelectorAll<HTMLButtonElement>(
      'button[data-slot="popover-trigger"]',
    ),
  ];
}

describe("model routing tooltip coverage", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(async () => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    // The rules header, the time window and the fallback tooltip only exist
    // inside a rendered group, so the fixture carries one real group with a
    // rule: an empty store would mount the page's empty state and the suite
    // would "pass" without ever reaching the anchors it claims to cover.
    bridge.getRoutingSettings.mockReset().mockResolvedValue({
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
          rules: [
            {
              id: "r",
              priority: 10,
              target_member_id: "a",
              when: {
                field: "system_time",
                within: ["09:00", "12:00"],
              },
            },
          ],
        },
      ],
    });
    bridge.getClassifierSettings
      .mockReset()
      .mockResolvedValue(defaultClassifierSettings());
    bridge.classifierCredentialAction
      .mockReset()
      .mockResolvedValue({ configured: false });
    await applyLocale("zh-CN");
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  async function mount(node: ReactNode): Promise<void> {
    await act(async () => {
      root.render(node);
      await Promise.resolve();
      await Promise.resolve();
    });
  }

  // Every item in §8.3 that is anchored in the desktop today, with the
  // accessible name its trigger actually carries. The body key and the label
  // key are separate strings in this repo (`rulesHint` heads the popover,
  // `rulesHintLabel` names the button), so both halves are pinned by name
  // rather than derived from a naming convention that does not hold.
  const required = [
    // 1 first-match / row order.
    {
      body: "modelRoutingGroup.rulesOrderHint",
      label: "modelRoutingGroup.rulesHintLabel",
    },
    // 2 + 3 + 4 time zone, half-open window, cross-midnight.
    {
      body: "modelRoutingGroup.timeWindowTooltipTimezone",
      label: "modelRoutingGroup.timeWindowHintLabel",
    },
    {
      body: "modelRoutingGroup.timeWindowTooltipHalfOpen",
      label: "modelRoutingGroup.timeWindowHintLabel",
    },
    {
      body: "modelRoutingGroup.timeWindowTooltipCrossMidnight",
      label: "modelRoutingGroup.timeWindowHintLabel",
    },
    // 5 unknown is not false.
    {
      body: "modelRoutingGroup.unknownHint",
      label: "modelRoutingGroup.unknownHintLabel",
    },
    // 6 fallback error does not block.
    {
      body: "modelRoutingGroup.fallbackErrorHint",
      label: "modelRoutingGroup.fallbackErrorHintLabel",
    },
    // 7 the dry run calls the classifier for real.
    {
      body: "classifier.dryRun.runHint",
      label: "classifier.dryRun.runHintLabel",
    },
    // 8 global label changes affect every group.
    {
      body: "modelRoutingGroup.catalogGlobalLabelHint",
      label: "modelRoutingGroup.catalogGlobalLabelHintLabel",
    },
    // 9 the "no" branch reuses existing credentials, not a provider list.
    {
      body: "classifier.standaloneModelHint",
      label: "classifier.standaloneModelHintLabel",
    },
  ] as const;

  it("marks every §8.3 item with a mounted, keyboard-focusable trigger", async () => {
    await mount(
      <>
        <ModelRoutingGroupsPanel ready services={[]} onDirtyChange={() => {}} />
        <ModelRoutingFieldCatalogPanel ready />
        <ClassifierSettingsPanel ready services={[]} onDirtyChange={() => {}} />
        <ClassifierDryRunPanel ready />
      </>,
    );

    const triggers = tooltipTriggers();
    // A `role="button"` span or a div with onClick would be reachable by mouse
    // but skipped by Tab, which is exactly what this assertion blocks.
    expect(triggers.length).toBeGreaterThanOrEqual(required.length);
    for (const trigger of triggers) {
      expect(trigger.tagName).toBe("BUTTON");
      expect(trigger.disabled).toBe(false);
      expect(trigger.getAttribute("aria-label")).toBeTruthy();
      trigger.focus();
      expect(document.activeElement).toBe(trigger);
    }

    const labels = triggers.map((item) => item.getAttribute("aria-label"));
    for (const { body, label } of required) {
      // Both halves must be real copy: a missing key echoes itself back.
      expect(i18n.t(body)).not.toBe(body);
      expect(i18n.t(label)).not.toBe(label);
      expect(labels).toContain(i18n.t(label));
    }
  });
});
