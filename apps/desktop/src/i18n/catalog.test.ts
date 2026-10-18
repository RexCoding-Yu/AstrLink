import { describe, expect, it } from "vitest";

import en from "./locales/en.json";
import ja from "./locales/ja.json";
import zhCN from "./locales/zh-CN.json";

function flattenKeys(value: unknown, prefix = ""): string[] {
  if (typeof value === "string") return prefix ? [prefix] : [];
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error(
      `catalog value at ${prefix || "$"} must be an object or string`,
    );
  }
  return Object.entries(value).flatMap(([key, nested]) =>
    flattenKeys(nested, prefix ? `${prefix}.${key}` : key),
  );
}

describe("i18n catalogs", () => {
  it("keeps the same keys in every catalog", () => {
    const keys = flattenKeys(en).sort();
    expect(flattenKeys(zhCN).sort()).toEqual(keys);
    expect(flattenKeys(ja).sort()).toEqual(keys);
  });
});
