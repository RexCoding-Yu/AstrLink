// @vitest-environment happy-dom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { initializeScrollbarGutter } from "./scrollbar-gutter";

describe("scrollbar gutter width", () => {
  let scrollbarWidth: number;
  let resize: (() => void) | undefined;
  const disconnect = vi.fn();

  function gutter() {
    return document.documentElement.style.getPropertyValue(
      "--scrollbar-gutter-width",
    );
  }

  beforeEach(() => {
    scrollbarWidth = 10;
    resize = undefined;
    disconnect.mockClear();
    vi.spyOn(HTMLElement.prototype, "offsetWidth", "get").mockReturnValue(100);
    vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockImplementation(
      () => 100 - scrollbarWidth,
    );
    vi.stubGlobal(
      "ResizeObserver",
      class {
        constructor(callback: () => void) {
          resize = callback;
        }
        observe() {}
        disconnect = disconnect;
      },
    );
  });

  afterEach(() => {
    document.body.replaceChildren();
    document.documentElement.style.removeProperty("--scrollbar-gutter-width");
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("publishes the measured scrollbar width and follows later changes", () => {
    const stop = initializeScrollbarGutter();
    expect(gutter()).toBe("10px");

    scrollbarWidth = 0;
    resize?.();
    expect(gutter()).toBe("0px");

    stop();
    expect(disconnect).toHaveBeenCalledOnce();
    expect(document.body.childElementCount).toBe(0);
    expect(gutter()).toBe("");
  });
});
