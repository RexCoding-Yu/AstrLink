import { describe, expect, it } from "vitest";

import {
  compactFilePaths,
  compactTitle,
  splitFilePaths,
} from "./file-path-spans";

describe("splitFilePaths", () => {
  it("names a dropped file glued to the question after it", () => {
    const path = "/Users/me/Desktop/Snapzy/Snapzy_2026-10-12_22-45-20_345.png";
    expect(splitFilePaths(`${path}这是什么`)).toEqual([
      { text: path, file: "Snapzy_2026-10-12_22-45-20_345.png" },
      { text: "这是什么" },
    ]);
  });

  it("unescapes a shell-escaped name and keeps CJK in it", () => {
    const path = "/Users/me/Desktop/截屏2026-10-12\\ 22.45.20.png";
    expect(splitFilePaths(`看看 ${path} 是什么`)).toEqual([
      { text: "看看 " },
      { text: path, file: "截屏2026-10-12 22.45.20.png" },
      { text: " 是什么" },
    ]);
  });

  it("leaves line references and punctuation outside the file", () => {
    expect(
      splitFilePaths("see /repo/src/main.ts:12 and /repo/README.md."),
    ).toEqual([
      { text: "see " },
      { text: "/repo/src/main.ts", file: "main.ts" },
      { text: ":12 and " },
      { text: "/repo/README.md", file: "README.md" },
      { text: "." },
    ]);
  });
});

describe("compactFilePaths", () => {
  it.each([
    ["~/notes/todo.md 和 C:\\Users\\me\\report.docx", "todo.md 和 report.docx"],
    ["对比/tmp/a.png和/tmp/b.png", "对比a.png和b.png"],
    ["unpack /tmp/build/out.tar.gz now", "unpack out.tar.gz now"],
  ])("shortens %s", (text, compact) => {
    expect(compactFilePaths(text)).toBe(compact);
  });

  it.each([
    "/review the /v1/chat/completions handler",
    "src/app.ts ./a/b.ts https://example.com/a/b.png",
    "/a.png /usr/lib/python3.12 /home/u/.bashrc",
    "",
  ])("leaves %j as text", (text) => {
    expect(compactFilePaths(text)).toBe(text);
  });
});

describe("compactTitle", () => {
  it("names a file whose extension the title's cut took", () => {
    const title = "/Users/me/Desktop/Snapzy/Snapzy_2026-10-12_22-45-20_345…";
    expect(compactTitle(title)).toBe("Snapzy_2026-10-12_22-45-20_345…");
    // A message body has no cut, so the same text is no file there.
    expect(compactFilePaths(title)).toBe(title);
  });

  it.each([
    "Snapzy_2026-10-12_22-45-20_345.png这是什么",
    "/review the /v1/chat/completions handler",
    "/v1/chat/completions 返回 404，帮我看看…",
  ])("leaves %j as it is", (title) => {
    expect(compactTitle(title)).toBe(title);
  });
});
