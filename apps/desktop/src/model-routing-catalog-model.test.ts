import { describe, expect, it } from "vitest";

import {
  modelRoutingCatalog,
  modelRoutingCatalogLabelDomain,
  modelRoutingFieldKind,
} from "./model-routing-catalog-model";
import { modelRoutingFields } from "./model-routing-group-model";

describe("model routing field catalog", () => {
  it("covers every field the operator matrix declares, in declaration order", () => {
    // The catalog must not grow a second field list: it derives from the matrix
    // the parser validates against, so a field can never be listed here while
    // being rejected on save (or the reverse).
    expect(modelRoutingCatalog().map((entry) => entry.field)).toEqual([
      ...modelRoutingFields,
    ]);
  });

  it("classifies the time field as time, not as a numeric range", () => {
    // `system_time` carries `within`, which is not a range operator. Deciding
    // time after the range check would hand it to the numeric editor and the
    // save would be rejected for an unsupported operator.
    expect(modelRoutingFieldKind("system_time")).toBe("time");
    expect(modelRoutingFieldKind("turn_count")).toBe("number");
    expect(modelRoutingFieldKind("task_type")).toBe("label");
    expect(modelRoutingFieldKind("streaming")).toBe("boolean");
  });

  it("marks only task_type as classifier-sourced", () => {
    const classifier = modelRoutingCatalog()
      .filter((entry) => entry.source === "classifier")
      .map((entry) => entry.field);
    expect(classifier).toEqual(["task_type"]);
  });

  it("reports the fixed value domain only for enumerated fields", () => {
    const byField = new Map(
      modelRoutingCatalog().map((entry) => [entry.field, entry]),
    );
    expect(byField.get("task_type")?.values).toEqual([
      "general",
      "research",
      "coding",
      "architect",
    ]);
    expect(byField.get("protocol")?.values).toBeUndefined();
    expect(byField.get("system_time")?.operators).toEqual(["within"]);
  });

  it("prefers a group override and falls back to the built-in label domain", () => {
    expect(modelRoutingCatalogLabelDomain([])).toEqual([
      "general",
      "research",
      "coding",
      "architect",
    ]);
    // An empty override array is "not set" in the editor's own reading, so it
    // must not shadow the built-in domain with an empty one.
    expect(modelRoutingCatalogLabelDomain([[], undefined])).toEqual([
      "general",
      "research",
      "coding",
      "architect",
    ]);
    expect(
      modelRoutingCatalogLabelDomain([undefined, ["custom", "coding"]]),
    ).toEqual(["custom", "coding"]);
  });
});
