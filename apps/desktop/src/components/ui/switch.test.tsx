// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { Switch } from "./switch";

describe("Switch", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    vi.restoreAllMocks();
    container.remove();
  });

  it("does not animate initial or asynchronously loaded values", async () => {
    await act(async () =>
      root.render(<Switch aria-label="demo" checked={false} />),
    );
    await act(async () => root.render(<Switch aria-label="demo" checked />));
    const control = container.querySelector("[data-slot='switch']");
    expect(control?.getAttribute("data-state")).toBe("checked");
    expect(control?.className.split(/\s+/)).toContain("transition-none");
    const thumb = container.querySelector("[data-slot='switch-thumb']");
    expect(thumb?.getAttribute("data-state")).toBe("checked");
    for (const slot of ["switch-thumb", "switch-star"]) {
      const part = container.querySelector(`[data-slot='${slot}']`);
      expect(part?.getAttribute("class")?.split(/\s+/)).toContain(
        "transition-none",
      );
    }
  });

  it("animates an intentional toggle and honors reduced motion", async () => {
    const onChange = vi.fn();
    await act(async () =>
      root.render(<Switch aria-label="demo" onCheckedChange={onChange} />),
    );
    const control = container.querySelector<HTMLButtonElement>(
      "[data-slot='switch']",
    )!;
    await act(async () => control.click());
    expect(onChange).toHaveBeenCalledWith(true);
    expect(control.className.split(/\s+/)).toContain("transition-colors");
    const thumb = container.querySelector("[data-slot='switch-thumb']");
    expect(thumb?.getAttribute("data-state")).toBe("checked");
    for (const [slot, transition] of [
      ["switch-thumb", "transition"],
      ["switch-star", "transition-[rotate,opacity]"],
    ]) {
      const part = container.querySelector(`[data-slot='${slot}']`);
      const classes = part?.getAttribute("class")?.split(/\s+/);
      expect(classes).toContain(transition);
      expect(classes).toContain("motion-reduce:transition-none");
    }
  });
});
