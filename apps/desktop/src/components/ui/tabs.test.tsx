// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { Tabs, TabsContent, TabsList, TabsTrigger } from "./tabs";

describe("TabsContent", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  it("restores ancestor workspace scroll when a panel is focused", async () => {
    await act(async () => {
      root.render(
        <div data-slot="workspace">
          <Tabs defaultValue="models">
            <TabsList>
              <TabsTrigger type="button" value="models">
                支持模型
              </TabsTrigger>
              <TabsTrigger type="button" value="protocols">
                入口协议
              </TabsTrigger>
            </TabsList>
            <TabsContent
              className="overflow-y-auto"
              data-tab-scroller=""
              value="models"
            >
              models
            </TabsContent>
            <TabsContent
              className="overflow-y-auto"
              data-tab-scroller=""
              value="protocols"
            >
              protocols
            </TabsContent>
          </Tabs>
        </div>,
      );
    });

    const workspace = container.querySelector(
      "[data-slot='workspace']",
    ) as HTMLElement;
    workspace.scrollTop = 96;
    workspace.scrollLeft = 12;

    const panel = container.querySelector(
      "[data-slot='tabs-content']",
    ) as HTMLElement;
    await act(async () => {
      panel.dispatchEvent(new FocusEvent("focus", { bubbles: true }));
      await new Promise<void>((resolve) => {
        requestAnimationFrame(() => resolve());
      });
    });

    expect(workspace.scrollTop).toBe(96);
    expect(workspace.scrollLeft).toBe(12);
  });

  async function renderNestedPanels() {
    await act(async () => {
      root.render(
        <Tabs defaultValue="models">
          <TabsList>
            <TabsTrigger type="button" value="models">
              模型
            </TabsTrigger>
          </TabsList>
          <TabsContent value="models">
            <Tabs defaultValue="catalog">
              <TabsList>
                <TabsTrigger type="button" value="catalog">
                  内置
                </TabsTrigger>
              </TabsList>
              <TabsContent value="catalog">catalog</TabsContent>
            </Tabs>
          </TabsContent>
        </Tabs>,
      );
    });
    const [outer, inner] = container.querySelectorAll<HTMLElement>(
      "[data-slot='tabs-content']",
    );
    const finish = vi.fn();
    inner.getAnimations = () => [
      { animationName: "panel-reveal", finish } as unknown as Animation,
    ];
    return { outer, inner, finish };
  }

  function startReveal(panel: HTMLElement) {
    act(() => {
      panel.dispatchEvent(
        new AnimationEvent("animationstart", {
          animationName: "panel-reveal",
          bubbles: true,
        }),
      );
    });
  }

  it("lets an outer panel's reveal carry a nested panel", async () => {
    const { outer, inner, finish } = await renderNestedPanels();
    outer.getAnimations = () => [
      { animationName: "panel-reveal" } as unknown as Animation,
    ];

    startReveal(inner);

    expect(finish).toHaveBeenCalledOnce();
  });

  it("reveals a nested panel when only its own tabs change", async () => {
    const { outer, inner, finish } = await renderNestedPanels();
    outer.getAnimations = () => [];

    startReveal(inner);

    expect(finish).not.toHaveBeenCalled();
  });
});
