import { invalidData, type DataProblem } from "./ipc-data-error";

/** Where the knowledge base's search engine (seekdb) stands on this device. */
export const KNOWLEDGE_RUNTIME_STATES = [
  "unsupported",
  "not_installed",
  "installing",
  "installed",
  "starting",
  "running",
  "failed",
] as const;

export type KnowledgeRuntimeState = (typeof KNOWLEDGE_RUNTIME_STATES)[number];

export const KNOWLEDGE_MODEL_STATES = [
  "not_installed",
  "installing",
  "installed",
  "failed",
] as const;

export type KnowledgeModelState = (typeof KNOWLEDGE_MODEL_STATES)[number];

export interface KnowledgeProgress {
  done: number;
  /** 0 while the size is not known yet. */
  total: number;
}

export interface KnowledgeRuntimeStatus {
  state: KnowledgeRuntimeState;
  /** Why an unsupported platform is unsupported: "platform" or "macos_version". */
  reason?: string;
  version: string;
  platform?: string;
  download_bytes?: number;
  progress?: KnowledgeProgress;
  error?: string;
  data_bytes: number;
  started_at?: string;
}

export interface KnowledgeEmbeddingModel {
  id: string;
  display_name: string;
  repo_id: string;
  revision: string;
  license: string;
  dimensions: number;
  download_bytes: number;
  state: KnowledgeModelState;
  progress?: KnowledgeProgress;
  error?: string;
  /** A worker holds the model in memory now. */
  loaded: boolean;
}

export interface KnowledgeEnvironment {
  runtime: KnowledgeRuntimeStatus;
  embedding_models: KnowledgeEmbeddingModel[];
}

export interface KnowledgeCheckStep {
  name: string;
  ok: boolean;
  duration_ms: number;
  detail?: string;
}

export interface KnowledgeRuntimeCheck {
  ok: boolean;
  engine?: string;
  steps: KnowledgeCheckStep[];
}

export interface KnowledgeEmbeddingPair {
  left: string;
  right: string;
  similarity: number;
}

export interface KnowledgeEmbeddingCheck {
  dimensions: number;
  load_ms: number;
  embed_ms: number;
  pairs: KnowledgeEmbeddingPair[];
}

type JsonObject = Record<string, unknown>;

function invalid(path: string, problem: DataProblem): never {
  return invalidData("knowledge", path, problem);
}

function objectAt(value: unknown, path: string): JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return invalid(path, "object");
  }
  return value as JsonObject;
}

function stringAt(value: unknown, path: string): string {
  if (typeof value !== "string") return invalid(path, "string");
  return value;
}

function optionalStringAt(value: unknown, path: string): string | undefined {
  return value === undefined ? undefined : stringAt(value, path);
}

function countAt(value: unknown, path: string): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0) {
    return invalid(path, "nonNegativeInteger");
  }
  return value;
}

function optionalCountAt(value: unknown, path: string): number | undefined {
  return value === undefined ? undefined : countAt(value, path);
}

function booleanAt(value: unknown, path: string): boolean {
  if (typeof value !== "boolean") return invalid(path, "boolean");
  return value;
}

function arrayAt(value: unknown, path: string): unknown[] {
  if (!Array.isArray(value)) return invalid(path, "array");
  return value;
}

function oneOf<T extends string>(
  value: unknown,
  allowed: readonly T[],
  path: string,
): T {
  if (typeof value !== "string" || !allowed.includes(value as T)) {
    return invalid(path, "unknownStatus");
  }
  return value as T;
}

function progressAt(
  value: unknown,
  path: string,
): KnowledgeProgress | undefined {
  if (value === undefined) return undefined;
  const progress = objectAt(value, path);
  return {
    done: countAt(progress.done, `${path}.done`),
    total: countAt(progress.total, `${path}.total`),
  };
}

/** Drops the keys whose value is undefined, so parsed objects compare cleanly. */
function defined<T extends object>(value: T): T {
  return Object.fromEntries(
    Object.entries(value).filter(([, entry]) => entry !== undefined),
  ) as T;
}

export function parseKnowledgeEnvironment(
  value: unknown,
): KnowledgeEnvironment {
  const environment = objectAt(value, "$");
  const runtime = objectAt(environment.runtime, "$.runtime");
  return {
    runtime: defined({
      state: oneOf(runtime.state, KNOWLEDGE_RUNTIME_STATES, "$.runtime.state"),
      reason: optionalStringAt(runtime.reason, "$.runtime.reason"),
      version: stringAt(runtime.version, "$.runtime.version"),
      platform: optionalStringAt(runtime.platform, "$.runtime.platform"),
      download_bytes: optionalCountAt(
        runtime.download_bytes,
        "$.runtime.download_bytes",
      ),
      progress: progressAt(runtime.progress, "$.runtime.progress"),
      error: optionalStringAt(runtime.error, "$.runtime.error"),
      data_bytes: countAt(runtime.data_bytes, "$.runtime.data_bytes"),
      started_at: optionalStringAt(runtime.started_at, "$.runtime.started_at"),
    }),
    embedding_models: arrayAt(
      environment.embedding_models,
      "$.embedding_models",
    ).map((item, index) => {
      const path = `$.embedding_models[${index}]`;
      const model = objectAt(item, path);
      return defined({
        id: stringAt(model.id, `${path}.id`),
        display_name: stringAt(model.display_name, `${path}.display_name`),
        repo_id: stringAt(model.repo_id, `${path}.repo_id`),
        revision: stringAt(model.revision, `${path}.revision`),
        license: stringAt(model.license, `${path}.license`),
        dimensions: countAt(model.dimensions, `${path}.dimensions`),
        download_bytes: countAt(model.download_bytes, `${path}.download_bytes`),
        state: oneOf(model.state, KNOWLEDGE_MODEL_STATES, `${path}.state`),
        progress: progressAt(model.progress, `${path}.progress`),
        error: optionalStringAt(model.error, `${path}.error`),
        loaded: booleanAt(model.loaded, `${path}.loaded`),
      });
    }),
  };
}

export function parseKnowledgeRuntimeCheck(
  value: unknown,
): KnowledgeRuntimeCheck {
  const check = objectAt(value, "$");
  return defined({
    ok: booleanAt(check.ok, "$.ok"),
    engine: optionalStringAt(check.engine, "$.engine"),
    steps: arrayAt(check.steps, "$.steps").map((item, index) => {
      const path = `$.steps[${index}]`;
      const step = objectAt(item, path);
      return defined({
        name: stringAt(step.name, `${path}.name`),
        ok: booleanAt(step.ok, `${path}.ok`),
        duration_ms: countAt(step.duration_ms, `${path}.duration_ms`),
        detail: optionalStringAt(step.detail, `${path}.detail`),
      });
    }),
  });
}

export function parseKnowledgeEmbeddingCheck(
  value: unknown,
): KnowledgeEmbeddingCheck {
  const check = objectAt(value, "$");
  return {
    dimensions: countAt(check.dimensions, "$.dimensions"),
    load_ms: countAt(check.load_ms, "$.load_ms"),
    embed_ms: countAt(check.embed_ms, "$.embed_ms"),
    pairs: arrayAt(check.pairs, "$.pairs").map((item, index) => {
      const path = `$.pairs[${index}]`;
      const pair = objectAt(item, path);
      if (
        typeof pair.similarity !== "number" ||
        !Number.isFinite(pair.similarity)
      ) {
        return invalid(`${path}.similarity`, "number");
      }
      return {
        left: stringAt(pair.left, `${path}.left`),
        right: stringAt(pair.right, `${path}.right`),
        similarity: pair.similarity,
      };
    }),
  };
}

/** Whether the environment is still downloading or starting something. */
export function knowledgeEnvironmentBusy(
  environment: KnowledgeEnvironment,
): boolean {
  return (
    environment.runtime.state === "installing" ||
    environment.runtime.state === "starting" ||
    environment.embedding_models.some((model) => model.state === "installing")
  );
}

/** The fraction a progress has reached, or undefined while its size is unknown. */
export function progressFraction(
  progress?: KnowledgeProgress,
): number | undefined {
  if (!progress || progress.total <= 0) return undefined;
  return Math.min(1, progress.done / progress.total);
}
