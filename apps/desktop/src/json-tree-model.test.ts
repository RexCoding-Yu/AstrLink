import { describe, expect, it } from "vitest";

import {
  JSON_CHILD_BATCH,
  JSON_TREE_ROOT,
  JsonTreeCancelledError,
  containerPreview,
  defaultOpenPaths,
  detectBase64,
  expandAllPaths,
  flattenJsonTree,
  parseJsonTree,
  parseJsonTreeIncremental,
  type JsonContainerNode,
  type JsonNode,
} from "./json-tree-model";

function toValue(node: JsonNode | null): unknown {
  if (node === null) return undefined;
  switch (node.kind) {
    case "object":
      return Object.fromEntries(
        node.entries.map(([key, value]) => [key, toValue(value)]),
      );
    case "array":
      return node.items.map(toValue);
    case "string":
      return node.text;
    case "number":
      return Number(node.text);
    case "literal":
      return JSON.parse(node.text);
  }
}

function container(node: JsonNode | null): JsonContainerNode {
  if (!node || (node.kind !== "object" && node.kind !== "array"))
    throw new Error("expected a container");
  return node;
}

describe("parseJsonTree", () => {
  it("reads the same document JSON.parse does", () => {
    const text = JSON.stringify({
      model: "gpt-4.1",
      input: [
        {
          role: "user",
          content: [{ type: "input_text", text: 'hi\n"there"' }],
        },
      ],
      stream: true,
      store: false,
      metadata: null,
      temperature: -0.5e-3,
      tags: [],
      extra: {},
      emoji: "\u{1F600} é \\ /",
    });
    const parsed = parseJsonTree(`  ${text}\n`);
    expect(parsed.errorAt).toBeNull();
    expect(parsed.cut).toBe(false);
    expect(toValue(parsed.root)).toEqual(JSON.parse(text));
  });

  it("keeps number text and duplicate keys as sent", () => {
    const parsed = parseJsonTree('{"id":12345678901234567890,"id":1.10}');
    expect(container(parsed.root)).toMatchObject({
      entries: [
        ["id", { kind: "number", text: "12345678901234567890" }],
        ["id", { kind: "number", text: "1.10" }],
      ],
    });
  });

  it("keeps everything before the cut of a truncated capture", () => {
    const parsed = parseJsonTree(
      '{"model":"gpt","input":[{"role":"user","content":"hel',
    );
    expect(parsed.errorAt).toBeNull();
    expect(parsed.cut).toBe(true);
    const root = container(parsed.root);
    expect(root.complete).toBe(false);
    expect(toValue(root)).toEqual({
      model: "gpt",
      input: [{ role: "user", content: "hel" }],
    });
    const input = container(
      root.kind === "object" ? root.entries[1]![1] : null,
    );
    const message = container(input.kind === "array" ? input.items[0]! : null);
    expect(message.kind === "object" && message.entries[1]![1]).toEqual({
      kind: "string",
      text: "hel",
      complete: false,
    });
  });

  it("drops a value the cut may have shortened", () => {
    expect(toValue(parseJsonTree("[1, 2, 3").root)).toEqual([1, 2]);
    expect(toValue(parseJsonTree("[1, -").root)).toEqual([1]);
    expect(toValue(parseJsonTree("[true, fal").root)).toEqual([true]);
    expect(toValue(parseJsonTree('{"a":1,"b').root)).toEqual({ a: 1 });
    expect(toValue(parseJsonTree('{"a":1,"b":').root)).toEqual({ a: 1 });
    expect(toValue(parseJsonTree('["x\\u00').root)).toEqual(["x"]);
    expect(toValue(parseJsonTree('["x\\').root)).toEqual(["x"]);
    expect(toValue(parseJsonTree('["x\\n y').root)).toEqual(["x\n y"]);
  });

  it("reports where text stops being JSON", () => {
    expect(parseJsonTree('{"a":1,}').errorAt).toBe(7);
    expect(parseJsonTree('{"a" 1}').errorAt).toBe(5);
    expect(parseJsonTree("{} x").errorAt).toBe(3);
    expect(parseJsonTree("[01]").errorAt).toBe(2);
    expect(parseJsonTree('["\\x"]').errorAt).toBe(1);
    expect(parseJsonTree("   ").errorAt).toBe(3);
    expect(parseJsonTree("nope").errorAt).toBe(0);
  });

  it("parses deep nesting without recursion", () => {
    const depth = 20_000;
    const parsed = parseJsonTree(`${"[".repeat(depth)}${"]".repeat(depth)}`);
    expect(parsed.errorAt).toBeNull();
    expect(parsed.cut).toBe(false);
  });
});

describe("parseJsonTreeIncremental", () => {
  const text = JSON.stringify(
    Array.from({ length: 20_000 }, (_, index) => ({
      index,
      text: `message ${index}`,
    })),
  );

  it("yields between slices and matches the one-shot parse", async () => {
    const progress: number[] = [];
    const parsed = await parseJsonTreeIncremental(text, {
      sliceMs: 0,
      onProgress: (value) => progress.push(value),
    });
    expect(progress.length).toBeGreaterThan(1);
    expect(progress.every((value) => value >= 0 && value <= 1)).toBe(true);
    expect(toValue(parsed.root)).toEqual(JSON.parse(text));
  });

  it("stops when cancelled", async () => {
    const controller = new AbortController();
    const parsing = parseJsonTreeIncremental(text, {
      signal: controller.signal,
      sliceMs: 0,
      onProgress: () => controller.abort(),
    });
    await expect(parsing).rejects.toBeInstanceOf(JsonTreeCancelledError);
  });
});

describe("flattenJsonTree", () => {
  it("walks only open containers and pages long ones", () => {
    const root = parseJsonTree(
      JSON.stringify({ items: Array.from({ length: 250 }, (_, i) => i) }),
    ).root!;
    const closed = flattenJsonTree(root, new Set([JSON_TREE_ROOT]), new Map());
    expect(closed.map((row) => row.type)).toEqual(["node", "node", "close"]);

    const itemsPath = `${JSON_TREE_ROOT}/0`;
    const open = new Set([JSON_TREE_ROOT, itemsPath]);
    const paged = flattenJsonTree(root, open, new Map());
    const more = paged.find((row) => row.type === "more");
    expect(more).toMatchObject({ shown: JSON_CHILD_BATCH, total: 250 });
    expect(paged.filter((row) => row.type === "node")).toHaveLength(
      2 + JSON_CHILD_BATCH,
    );

    const all = flattenJsonTree(root, open, new Map([[itemsPath, 250]]));
    expect(all.some((row) => row.type === "more")).toBe(false);
    expect(all.filter((row) => row.type === "node")).toHaveLength(252);
  });
});

describe("defaultOpenPaths", () => {
  const request = {
    model: "claude",
    tools: [{ name: "search", input_schema: { type: "object" } }],
    messages: [
      {
        role: "user",
        content: [
          { type: "text", text: "a long prompt ".repeat(40) },
          { type: "image", source: { data: "iVBORw0KGgo".repeat(100) } },
        ],
      },
    ],
  };
  const root = parseJsonTree(JSON.stringify(request)).root!;

  it("opens the conversation but folds tool definitions and long strings", () => {
    const open = defaultOpenPaths(root);
    // $ / messages(2) / message 0 / content(1) / blocks
    expect(open.has("$")).toBe(true);
    expect(open.has("$/2")).toBe(true);
    expect(open.has("$/2/0/1")).toBe(true);
    expect(open.has("$/2/0/1/0")).toBe(true);
    expect(open.has("$/1")).toBe(false);
    // The long text itself stays folded.
    expect(open.has("$/2/0/1/0/1")).toBe(false);
  });

  it("stops at the depth and row budget", () => {
    expect([...defaultOpenPaths(root, { maxDepth: 1 })]).toEqual(["$", "$/2"]);
    expect([...defaultOpenPaths(root, { budget: 1 })]).toEqual(["$"]);
  });

  it("unfolds revealed strings and their ancestors", () => {
    const open = defaultOpenPaths(root, {
      maxDepth: 0,
      reveal: (text) => text.startsWith("a long prompt"),
    });
    expect(open).toEqual(
      new Set(["$", "$/2", "$/2/0", "$/2/0/1", "$/2/0/1/0", "$/2/0/1/0/1"]),
    );
  });

  it("expands every container for expand all", () => {
    const open = expandAllPaths(root);
    expect(open.has("$/1")).toBe(true);
    expect(open.has("$/1/0/1")).toBe(true);
    expect(open.has("$/2/0/1/1/1")).toBe(true);
  });
});

describe("detectBase64", () => {
  it("recognizes data URLs and bare base64 payloads", () => {
    const payload = "iVBORw0KGgoAAAANSUhEUgAA".repeat(20);
    expect(detectBase64(`data:image/png;base64,${payload}`)).toEqual({
      mediaType: "image/png",
      bytes: Math.floor((payload.length * 3) / 4),
    });
    expect(detectBase64(`${payload}==`)).toMatchObject({ mediaType: null });
  });

  it("leaves prose, URLs and short strings alone", () => {
    expect(detectBase64("word ".repeat(100))).toBeNull();
    expect(
      detectBase64(`https://example.test/${"path/".repeat(60)}?q=1`),
    ).toBeNull();
    expect(detectBase64("QUJD")).toBeNull();
    expect(detectBase64("abcdefgh".repeat(40))).toBeNull();
  });
});

describe("containerPreview", () => {
  it("shows one level in a line", () => {
    const root = parseJsonTree(
      JSON.stringify({
        role: "user",
        content: [{ type: "text" }, { type: "image" }],
        meta: {},
        "odd key": 1,
      }),
    ).root!;
    expect(containerPreview(container(root))).toBe(
      '{role: "user", content: [2], meta: {}, "odd key": 1}',
    );
  });

  it("clips long previews and marks a cut container", () => {
    const long = parseJsonTree(
      JSON.stringify(Array.from({ length: 100 }, (_, i) => i)),
    ).root!;
    expect(containerPreview(container(long), 20)).toBe(
      "[0, 1, 2, 3, 4, 5, 6, …]",
    );
    const cut = parseJsonTree('{"a":1,"b":2').root!;
    expect(containerPreview(container(cut))).toBe("{a: 1, …}");
  });
});
