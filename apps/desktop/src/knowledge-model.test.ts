import { describe, expect, it } from "vitest";

import {
  knowledgeEnvironmentBusy,
  parseKnowledgeEmbeddingCheck,
  parseKnowledgeEnvironment,
  parseKnowledgeRuntimeCheck,
  progressFraction,
} from "./knowledge-model";

const environment = {
  runtime: {
    state: "installing",
    version: "1.4.0.0-r1",
    platform: "linux-amd64",
    download_bytes: 53574813,
    progress: { done: 1024, total: 53574813 },
    data_bytes: 0,
  },
  embedding_models: [
    {
      id: "granite-embedding-107m-multilingual",
      display_name: "Granite Embedding 107M Multilingual",
      repo_id: "ibm-granite/granite-embedding-107m-multilingual",
      revision: "d6cffd338414d6a1c1f5decfad5fec62eebc90d5",
      license: "Apache-2.0",
      dimensions: 384,
      download_bytes: 437184350,
      state: "installed",
      loaded: false,
    },
  ],
};

describe("knowledge environment", () => {
  it("parses the environment Core reports", () => {
    const parsed = parseKnowledgeEnvironment(environment);
    expect(parsed.runtime.state).toBe("installing");
    expect(parsed.runtime.progress).toEqual({ done: 1024, total: 53574813 });
    expect(parsed.embedding_models[0]?.state).toBe("installed");
    expect("error" in parsed.runtime).toBe(false);
    expect(knowledgeEnvironmentBusy(parsed)).toBe(true);
    expect(
      knowledgeEnvironmentBusy({
        ...parsed,
        runtime: { ...parsed.runtime, state: "running", progress: undefined },
      }),
    ).toBe(false);
  });

  it("rejects states and counts it does not know", () => {
    expect(() =>
      parseKnowledgeEnvironment({
        ...environment,
        runtime: { ...environment.runtime, state: "melting" },
      }),
    ).toThrow("$.runtime.state");
    expect(() =>
      parseKnowledgeEnvironment({
        ...environment,
        runtime: { ...environment.runtime, data_bytes: -1 },
      }),
    ).toThrow("$.runtime.data_bytes");
    expect(() =>
      parseKnowledgeEnvironment({
        ...environment,
        embedding_models: [
          { ...environment.embedding_models[0], loaded: "no" },
        ],
      }),
    ).toThrow("$.embedding_models[0].loaded");
  });

  it("parses self-test and model check results", () => {
    expect(
      parseKnowledgeRuntimeCheck({
        ok: false,
        engine: "5.7.25-OceanBase seekdb-v1.4.0.0",
        steps: [
          { name: "connect", ok: true, duration_ms: 1 },
          { name: "vector", ok: false, duration_ms: 3, detail: "no rows" },
        ],
      }).steps[1],
    ).toEqual({ name: "vector", ok: false, duration_ms: 3, detail: "no rows" });
    expect(() =>
      parseKnowledgeEmbeddingCheck({
        dimensions: 384,
        load_ms: 2000,
        embed_ms: 20,
        pairs: [{ left: "a", right: "b", similarity: "high" }],
      }),
    ).toThrow("$.pairs[0].similarity");
  });

  it("reports progress only once the size is known", () => {
    expect(progressFraction(undefined)).toBeUndefined();
    expect(progressFraction({ done: 10, total: 0 })).toBeUndefined();
    expect(progressFraction({ done: 50, total: 200 })).toBe(0.25);
    expect(progressFraction({ done: 300, total: 200 })).toBe(1);
  });
});
