// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const motionPreference = vi.hoisted(() => ({ reduced: false }));
vi.mock("motion/react", async (importOriginal) => ({
  ...(await importOriginal<typeof import("motion/react")>()),
  useReducedMotion: () => motionPreference.reduced,
}));

import type { ImageCompression } from "@/failure-policy-model";
import { applyLocale } from "@/i18n";
import { ImageCompressionEditor } from "./ImageCompressionEditor";

let container: HTMLDivElement;
let root: Root;

beforeEach(async () => {
  await applyLocale("zh-CN");
  motionPreference.reduced = false;
  vi.useFakeTimers();
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
  vi.useRealTimers();
});

async function openHelp(
  value: Partial<ImageCompression> = {},
  onChange = vi.fn(),
) {
  await act(async () =>
    root.render(
      <ImageCompressionEditor
        disabled={false}
        onChange={onChange}
        value={{ enabled: true, mode: "lossless", quality: 85, ...value }}
      />,
    ),
  );
  await act(async () =>
    container
      .querySelector<HTMLButtonElement>(
        'button[aria-label="请求图片压缩说明"]',
      )!
      .click(),
  );
  return document.querySelector<HTMLElement>('[data-slot="popover-content"]')!;
}

function demo(popover: HTMLElement) {
  return popover.querySelector<HTMLElement>("[data-compression-demo]")!;
}

/** Each image card as it reads now; * marks one a WebP replaced. */
function cards(popover: HTMLElement, only: "arrived" | "all" = "all") {
  return [
    ...popover.querySelectorAll<HTMLElement>(
      only === "arrived" ? "[data-image][data-arrived]" : "[data-image]",
    ),
  ].map((card) => `${card.textContent}${card.dataset.compressed ? "*" : ""}`);
}

/** The trace row once it shows, or null while its place is empty. */
function record(popover: HTMLElement) {
  const slot = popover.querySelector<HTMLElement>("[data-compression-record]")!;
  return slot.dataset.compressionRecord === "shown"
    ? slot.textContent!.replaceAll(" ", " ")
    : null;
}

function texts(popover: HTMLElement) {
  return [...demo(popover).querySelectorAll("p")].map((p) => p.textContent);
}

const notes =
  "同一张图每次压缩结果相同，提示缓存照常命中。大截图第一次压缩要零点几秒。模型按像素尺寸计算 Token，用量不变。";

it("walks the example request through AstrLink and fills the trace row", async () => {
  const popover = await openHelp();
  expect(demo(popover).dataset.compressionDemo).toBe("lossless");
  expect(
    popover.querySelector('[aria-hidden="true"][data-visual="compression"]'),
  ).not.toBeNull();
  expect(cards(popover)).toEqual([
    "截图.png1.2 MB",
    "照片.jpg646.6 KB",
    "动图.gif300.4 KB",
    "图片链接",
  ]);
  expect(cards(popover, "arrived")).toEqual([]);
  expect(record(popover)).toBeNull();
  expect(texts(popover)).toEqual([
    "截图转成 WebP 后像素不变，体积更小。照片转成无损 WebP 反而更大，按原样发送。",
    notes,
  ]);

  await act(async () => vi.advanceTimersByTime(1100));
  expect(cards(popover, "arrived")).toEqual(["截图.webp386.5 KB*"]);
  await act(async () => vi.advanceTimersByTime(2100));
  expect(cards(popover, "arrived")).toEqual([
    "截图.webp386.5 KB*",
    "照片.jpg646.6 KB",
    "动图.gif300.4 KB",
    "图片链接",
  ]);
  expect(record(popover)).toBeNull();
  await act(async () => vi.advanceTimersByTime(400));
  expect(record(popover)).toBe(
    "压缩1/3 张图片 · 无损 WebP · 2.1 MB → 1.3 MB · 减小 39%",
  );

  // It holds the result, then starts over.
  await act(async () => vi.advanceTimersByTime(2400));
  expect(cards(popover, "arrived")).toEqual([]);
  expect(record(popover)).toBeNull();
  await act(async () => vi.advanceTimersByTime(1100));
  expect(cards(popover, "arrived")).toEqual(["截图.webp386.5 KB*"]);
});

it("switches the example to lossy without changing the setting", async () => {
  const onChange = vi.fn();
  const popover = await openHelp({}, onChange);
  await act(async () => vi.advanceTimersByTime(1800));
  const lossy = [
    ...popover.querySelectorAll<HTMLButtonElement>(
      '[data-slot="segmented-control-item"]',
    ),
  ].find((item) => item.textContent === "有损")!;
  await act(async () => lossy.click());
  expect(demo(popover).dataset.compressionDemo).toBe("lossy");
  // The example starts over in the new mode.
  expect(cards(popover, "arrived")).toEqual([]);
  await act(async () => vi.advanceTimersByTime(3600));
  expect(cards(popover, "arrived")).toEqual([
    "截图.webp386.5 KB*",
    "照片.webp242.5 KB*",
    "动图.gif300.4 KB",
    "图片链接",
  ]);
  expect(record(popover)).toBe(
    "压缩2/3 张图片 · 有损 WebP · 2.1 MB → 929.4 KB · 减小 57%",
  );
  expect(texts(popover)).toEqual([
    "照片也转成 WebP，体积更小，细小文字可能变模糊。截图转无损更小时，按无损发送。",
    notes,
  ]);
  expect(onChange).not.toHaveBeenCalled();
});

it("opens on the saved mode and shows the finished example when motion is reduced", async () => {
  motionPreference.reduced = true;
  const popover = await openHelp({ mode: "lossy" });
  expect(demo(popover).dataset.compressionDemo).toBe("lossy");
  expect(
    popover.querySelector(
      '[data-slot="segmented-control-item"][data-state="on"]',
    )?.textContent,
  ).toBe("有损");
  const finished = [
    "截图.webp386.5 KB*",
    "照片.webp242.5 KB*",
    "动图.gif300.4 KB",
    "图片链接",
  ];
  expect(cards(popover, "arrived")).toEqual(finished);
  expect(record(popover)).toBe(
    "压缩2/3 张图片 · 有损 WebP · 2.1 MB → 929.4 KB · 减小 57%",
  );
  await act(async () => vi.advanceTimersByTime(10_000));
  expect(cards(popover, "arrived")).toEqual(finished);
});
