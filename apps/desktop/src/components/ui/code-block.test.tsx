// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import {
  CodeBlock,
  CodeBlockActions,
  CodeBlockContent,
  CodeBlockCopyButton,
} from "./code-block";

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  (
    globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  vi.restoreAllMocks();
});

async function renderBlock() {
  await act(async () =>
    root.render(
      <CodeBlock size="sm">
        <CodeBlockActions>
          <CodeBlockCopyButton value={"ls -la\necho done"} />
        </CodeBlockActions>
        <CodeBlockContent lineNumbers>
          {"ls -la\n\necho done\n"}
        </CodeBlockContent>
      </CodeBlock>,
    ),
  );
  return container.querySelector<HTMLButtonElement>(
    '[data-slot="code-block-copy-button"]',
  )!;
}

function announced() {
  return container.querySelector('[aria-live="polite"]')?.textContent;
}

it("numbers each line and keeps blank lines for copying a selection", async () => {
  await renderBlock();
  const lines = container.querySelectorAll('[data-slot="code-block-line"]');
  expect([...lines].map((line) => line.textContent)).toEqual([
    "ls -la",
    "\n",
    "echo done",
  ]);
  expect(
    container
      .querySelector('[data-slot="code-block-content"]')
      ?.hasAttribute("data-numbered"),
  ).toBe(true);
});

it("copies the code and announces it", async () => {
  const copy = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue();
  const button = await renderBlock();
  expect(button.getAttribute("aria-label")).toBe("复制");
  expect(announced()).toBe("");

  await act(async () => button.click());
  expect(copy).toHaveBeenCalledWith("ls -la\necho done");
  expect(button.getAttribute("aria-label")).toBe("已复制");
  expect(button.hasAttribute("data-copied")).toBe(true);
  expect(announced()).toBe("已复制");
});

it("says so when the clipboard refuses", async () => {
  vi.spyOn(navigator.clipboard, "writeText").mockRejectedValue(
    new Error("clipboard unavailable"),
  );
  const button = await renderBlock();

  await act(async () => button.click());
  expect(button.getAttribute("aria-label")).toBe("复制失败");
  expect(button.hasAttribute("data-copied")).toBe(false);
  expect(announced()).toBe("复制失败");
});
