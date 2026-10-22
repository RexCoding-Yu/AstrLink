// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { installHighlighterWorker } from "@/lib/test-highlighter-worker";
import { useHighlightedCode } from "./use-highlighted-code";

const worker = installHighlighterWorker();

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  (
    globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
  worker.hold();
});

afterEach(async () => {
  worker.release();
  await act(async () => root.unmount());
  container.remove();
});

function Code({ value, language }: { value: string; language: string }) {
  const lines = useHighlightedCode(value, language);
  return (
    <pre>
      {lines.map((line, index) => (
        <span data-line="" key={index}>
          {line}
        </span>
      ))}
    </pre>
  );
}

function lines() {
  return [...container.querySelectorAll("[data-line]")].map(
    (line) => line.textContent,
  );
}

function tokens() {
  return container.querySelectorAll<HTMLElement>("[data-line] span[style]");
}

it("shows the code as plain lines until the highlighter answers", async () => {
  await act(async () =>
    root.render(<Code language="json" value={'{\n  "ok": true\n}\n'} />),
  );
  // The closing newline does not become an empty last line.
  expect(lines()).toEqual(["{", '  "ok": true', "}"]);
  expect(tokens()).toHaveLength(0);
  expect(worker.requests.at(-1)).toMatchObject({
    code: '{\n  "ok": true\n}',
    language: "json",
  });

  await act(async () => {
    worker.release();
    await worker.answered();
  });
  await vi.waitFor(() => expect(tokens().length).toBeGreaterThan(0));
  expect(lines()).toEqual(["{", '  "ok": true', "}"]);
  const key = [...tokens()].find((token) => token.textContent === '"ok"')!;
  expect(key.className).toBe("text-(--shiki-light) dark:text-(--shiki-dark)");
  expect(key.style.getPropertyValue("--shiki-light")).toMatch(/^#/);
  expect(key.style.getPropertyValue("--shiki-dark")).toMatch(/^#/);
});

it("keeps a language it does not know as plain text", async () => {
  await act(async () =>
    root.render(<Code language="brainfuck" value={"+[>+<-]\n.\n"} />),
  );
  await act(async () => {
    worker.release();
    await worker.answered();
  });
  expect(worker.requests.at(-1)?.language).toBe("brainfuck");
  expect(lines()).toEqual(["+[>+<-]", "."]);
  expect(tokens()).toHaveLength(0);

  // Without a language the highlighter is not asked at all.
  const asked = worker.requests.length;
  await act(async () => root.render(<Code language="" value="plain" />));
  expect(worker.requests).toHaveLength(asked);
  expect(lines()).toEqual(["plain"]);
});

it("colors code highlighted before from the first paint", async () => {
  const value = "ls -la\necho done";
  await act(async () => root.render(<Code language="bash" value={value} />));
  await act(async () => {
    worker.release();
    await worker.answered();
  });
  await vi.waitFor(() => expect(tokens().length).toBeGreaterThan(0));
  await act(async () => root.render(<div />));
  expect(tokens()).toHaveLength(0);

  const asked = worker.requests.length;
  act(() => root.render(<Code language="bash" value={value} />));
  expect(tokens().length).toBeGreaterThan(0);
  expect(worker.requests).toHaveLength(asked);
});
