/**
 * The external classifier settings, as the desktop reads and writes them.
 *
 * This is a singleton resource of its own — it is deliberately *not* part of
 * `RoutingSettings`, whose field whitelist is mirrored in the Rust host. The
 * Go contract owns these names; this file declares what the desktop expects so
 * the parser and the cross-layer test cannot drift apart.
 */

/** Wire values of `inline.protocol`. */
export const classifierProtocols = ["jev", "openai_decisions"] as const;
export type ClassifierProtocol = (typeof classifierProtocols)[number];

/**
 * The default endpoint and model each protocol ships with. Switching protocol
 * re-fills a field only while it still holds that protocol's previous default
 * (see `linkClassifierProtocol`); anything the user typed is kept.
 */
export interface ClassifierProtocolPreset {
  endpoint: string;
  model: string;
}

export const classifierProtocolPresets: Record<
  ClassifierProtocol,
  ClassifierProtocolPreset
> = {
  jev: {
    endpoint: "https://api.typesafe.ai/v1/systemone",
    model: "jev-latest",
  },
  openai_decisions: {
    endpoint: "https://api.openai.com/v1/decisions",
    model: "gpt-6-luna",
  },
};

export const classifierFallbacks = ["skip", "general", "error"] as const;
export type ClassifierFallback = (typeof classifierFallbacks)[number];

/** Human confirmation in ASTR-14 fixed these two; the group may only tighten. */
export const classifierTimeoutDefaultMS = 1000;
export const classifierTimeoutMaxMS = 5000;
export const classifierLowConfidenceDefault = 0.5;

/**
 * `existing_model` has its own budget: an ordinary model call is far slower than
 * the 1s inline default, so sharing it would fail open on every real request.
 * These mirror `core/contract/classifier_settings.go:34-36`.
 */
export const classifierExistingModelTimeoutDefaultMS = 15000;
export const classifierExistingModelTimeoutMinMS = 1;
export const classifierExistingModelTimeoutMaxMS = 60000;

/**
 * `provider_ref` stays in the union because the wire accepts it, but it is
 * rejected while enabled (`ErrClassifierSourceNotSupported`) and the panel never
 * offers it. Only `inline` and `existing_model` are selectable.
 */
export type ClassifierSource = "inline" | "provider_ref" | "existing_model";

export interface ClassifierInlineSettings {
  protocol: ClassifierProtocol;
  endpoint: string;
  /** A reference only; the API key itself never reaches this document. */
  credential_ref: string;
  model: string;
  /** Empty means "use the built-in prompt". `{{labels}}` is the only placeholder. */
  prompt: string;
  timeout_ms: number;
  low_confidence_threshold: number;
  fallback: ClassifierFallback;
}

/** Reserved for a later release; shape only, never executed in V1. */
export interface ClassifierProviderRef {
  ref: string;
}

/**
 * Targets a service/model pair that already exists in the gateway. There is no
 * endpoint or key here on purpose: the call reuses that service's own
 * credentials and proxy. Empty strings mean "not chosen yet"; completeness is
 * only required once the source is enabled.
 */
export interface ClassifierExistingModelSettings {
  service_id: string;
  model: string;
  timeout_ms: number;
}

export interface ClassifierSettings {
  enabled: boolean;
  classifier_source: ClassifierSource;
  inline: ClassifierInlineSettings;
  provider_ref: ClassifierProviderRef;
  existing_model: ClassifierExistingModelSettings;
}

/** `POST …/classifier/dry-run` result. Local only — never a real upstream call. */
export interface ClassifierDryRunResult {
  /** False when no endpoint and key are configured; the run is skipped. */
  configured: boolean;
  category: string;
  confidence: number | null;
  fallback_reason: string | null;
  duration_ms: number;
  sanitized_preview: string;
  /** Present only when the request named a group. */
  decision: ClassifierDryRunDecision | null;
  /**
   * Per-field routing observations. `omitempty` on the Go side, so a run that
   * never reached the scored fallback arrives without it — that means "no
   * candidate detail", never "an empty table" and never a synthesized row.
   */
  fields: readonly ClassifierDryRunField[];
}

/**
 * One candidate the classifier produced for a field. Read-only: selection
 * stays row-order first-match, so nothing here feeds back into which member
 * wins. `rank` is 0-based on the wire; the UI adds 1.
 */
export interface ClassifierDryRunCandidate {
  value: string;
  score: number;
  rank: number;
  chosen: boolean;
}

export interface ClassifierDryRunField {
  field: string;
  source: string;
  known: boolean;
  match_count: number;
  classifier_confidence: number | null;
  classifier_id: string | null;
  skip_reason: string | null;
  error_code: string | null;
  /** Empty when the field carries no candidate detail. */
  candidates: readonly ClassifierDryRunCandidate[];
}

/**
 * The member the production evaluator would pick. `member_id` is the group-local
 * member key; the service and model name what that member actually resolves to.
 */
export interface ClassifierDryRunDecision {
  member_id: string;
  service_id: string;
  model: string;
  rule_id: string | null;
  used_default: boolean;
}

/**
 * The default source for a document that was never saved is `inline`: the Go
 * side answers with `inline` when `classifier_source` is absent
 * (`core/contract/classifier_settings.go:158-161`), and C7 forbids switching an
 * existing enabled configuration onto a different source implicitly. The panel
 * presents the new "use a standalone model" switch as off, which maps to this
 * same value, so an upgraded document keeps its loaded source untouched.
 */
export function defaultClassifierSettings(): ClassifierSettings {
  const preset = classifierProtocolPresets.jev;
  return {
    enabled: false,
    classifier_source: "inline",
    inline: {
      protocol: "jev",
      endpoint: preset.endpoint,
      credential_ref: "local://classifier/default",
      model: preset.model,
      prompt: "",
      timeout_ms: classifierTimeoutDefaultMS,
      low_confidence_threshold: classifierLowConfidenceDefault,
      fallback: "skip",
    },
    provider_ref: { ref: "" },
    existing_model: {
      service_id: "",
      model: "",
      timeout_ms: classifierExistingModelTimeoutDefaultMS,
    },
  };
}

function asRecord(value: unknown, field: string): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`classifier settings: ${field} must be an object`);
  }
  return value as Record<string, unknown>;
}

function asString(value: unknown, field: string): string {
  if (typeof value !== "string") {
    throw new Error(`classifier settings: ${field} must be a string`);
  }
  return value;
}

function asNumber(value: unknown, field: string): number {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    throw new Error(`classifier settings: ${field} must be a number`);
  }
  return value;
}

function asBoolean(value: unknown, field: string): boolean {
  if (typeof value !== "boolean") {
    throw new Error(`classifier settings: ${field} must be a boolean`);
  }
  return value;
}

function asMember<T extends string>(
  value: unknown,
  allowed: readonly T[],
  field: string,
): T {
  const text = asString(value, field);
  if (!(allowed as readonly string[]).includes(text)) {
    throw new Error(`classifier settings: unknown ${field} ${text}`);
  }
  return text as T;
}

/** Tolerant object read: a key the sender omitted keeps the fallback. */
function optional<T>(
  record: Record<string, unknown>,
  key: string,
  read: (value: unknown, field: string) => T,
  fallback: T,
): T {
  const value = record[key];
  if (value === undefined || value === null) return fallback;
  return read(value, key);
}

export function parseClassifierSettings(value: unknown): ClassifierSettings {
  const root = asRecord(value, "settings");
  const base = defaultClassifierSettings();
  // `inline` and `provider_ref` are pointers on the Go side, so a resource that
  // was never saved answers with neither key: the caller gets the defaults.
  const inline =
    root.inline === undefined || root.inline === null
      ? {}
      : asRecord(root.inline, "inline");
  const providerRef: Record<string, unknown> | null =
    root.provider_ref === undefined || root.provider_ref === null
      ? null
      : asRecord(root.provider_ref, "provider_ref");
  const existingModel: Record<string, unknown> | null =
    root.existing_model === undefined || root.existing_model === null
      ? null
      : asRecord(root.existing_model, "existing_model");
  return {
    enabled: optional(root, "enabled", asBoolean, base.enabled),
    classifier_source: optional(
      root,
      "classifier_source",
      (entry, field) =>
        asMember(entry, ["inline", "provider_ref", "existing_model"], field),
      base.classifier_source,
    ),
    inline: {
      protocol: optional(
        inline,
        "protocol",
        (entry, field) => asMember(entry, classifierProtocols, field),
        base.inline.protocol,
      ),
      endpoint: optional(inline, "endpoint", asString, base.inline.endpoint),
      credential_ref: optional(
        inline,
        "credential_ref",
        asString,
        base.inline.credential_ref,
      ),
      model: optional(inline, "model", asString, base.inline.model),
      prompt: optional(inline, "prompt", asString, base.inline.prompt),
      timeout_ms: optional(
        inline,
        "timeout_ms",
        asNumber,
        base.inline.timeout_ms,
      ),
      low_confidence_threshold: optional(
        inline,
        "low_confidence_threshold",
        asNumber,
        base.inline.low_confidence_threshold,
      ),
      fallback: optional(
        inline,
        "fallback",
        (entry, field) => asMember(entry, classifierFallbacks, field),
        base.inline.fallback,
      ),
    },
    provider_ref: {
      ref: optional(providerRef ?? {}, "ref", asString, base.provider_ref.ref),
    },
    existing_model: {
      service_id: optional(
        existingModel ?? {},
        "service_id",
        asString,
        base.existing_model.service_id,
      ),
      model: optional(
        existingModel ?? {},
        "model",
        asString,
        base.existing_model.model,
      ),
      timeout_ms: optional(
        existingModel ?? {},
        "timeout_ms",
        asNumber,
        base.existing_model.timeout_ms,
      ),
    },
  };
}

export function parseClassifierCredentialStatus(value: unknown): {
  configured: boolean;
} {
  const root = asRecord(value, "credential");
  return {
    configured: optional(root, "configured", asBoolean, false),
  };
}

function optionalNumber(value: unknown, field: string): number | null {
  if (value === undefined || value === null) return null;
  return asNumber(value, field);
}

function optionalString(value: unknown, field: string): string | null {
  if (value === undefined || value === null) return null;
  return asString(value, field);
}

function optionalArray(value: unknown, field: string): readonly unknown[] {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) {
    throw new Error(`classifier settings: ${field} must be an array`);
  }
  return value;
}

export function parseClassifierDryRunResult(
  value: unknown,
): ClassifierDryRunResult {
  const root = asRecord(value, "dry-run");
  const decision = root.decision;
  // The handler marks the outcome fields `omitempty`, so a skipped or failed
  // run arrives without them. A confidence is only meaningful alongside a
  // category: the zero it decays to belongs to "no measurement", not to 0%.
  const category = optional(root, "category", asString, "");
  return {
    configured: optional(root, "configured", asBoolean, true),
    category,
    confidence:
      category === "" ? null : optionalNumber(root.confidence, "confidence"),
    fallback_reason: optionalString(root.fallback_reason, "fallback_reason"),
    duration_ms: asNumber(root.duration_ms, "duration_ms"),
    sanitized_preview: optional(root, "sanitized_preview", asString, ""),
    decision:
      decision === undefined || decision === null
        ? null
        : (() => {
            const record = asRecord(decision, "decision");
            return {
              member_id: asString(record.member_id, "member_id"),
              service_id: optional(record, "service_id", asString, ""),
              model: optional(record, "model", asString, ""),
              rule_id: optionalString(record.rule_id, "rule_id"),
              used_default: asBoolean(record.used_default, "used_default"),
            };
          })(),
    fields: optionalArray(root.fields, "fields").map((entry, index) => {
      const field = asRecord(entry, `fields[${index}]`);
      return {
        field: asString(field.field, "field"),
        source: optional(field, "source", asString, ""),
        known: optional(field, "known", asBoolean, false),
        match_count: optional(field, "match_count", asNumber, 0),
        classifier_confidence: optionalNumber(
          field.classifier_confidence,
          "classifier_confidence",
        ),
        classifier_id: optionalString(field.classifier_id, "classifier_id"),
        skip_reason: optionalString(field.skip_reason, "skip_reason"),
        error_code: optionalString(field.error_code, "error_code"),
        candidates: optionalArray(field.candidates, "candidates").map(
          (candidate, position) => {
            const record = asRecord(candidate, `candidates[${position}]`);
            return {
              value: asString(record.value, "value"),
              score: asNumber(record.score, "score"),
              rank: asNumber(record.rank, "rank"),
              chosen: optional(record, "chosen", asBoolean, false),
            };
          },
        ),
      };
    }),
  };
}

/**
 * Applies a protocol change without silently overwriting user input.
 *
 * A field follows the new protocol only while it still holds the default of the
 * protocol we are leaving — that is the only state in which "the user has not
 * touched this" is knowable. Anything else is their own text and stays.
 */
export function linkClassifierProtocol(
  inline: ClassifierInlineSettings,
  next: ClassifierProtocol,
): ClassifierInlineSettings {
  if (next === inline.protocol) return inline;
  const previous = classifierProtocolPresets[inline.protocol];
  const preset = classifierProtocolPresets[next];
  return {
    ...inline,
    protocol: next,
    endpoint:
      inline.endpoint === previous.endpoint ? preset.endpoint : inline.endpoint,
    model: inline.model === previous.model ? preset.model : inline.model,
  };
}

/** Whether the endpoint is an absolute http(s) URL free of embedded credentials. */
export function classifierEndpointProblem(endpoint: string): string | null {
  const trimmed = endpoint.trim();
  if (trimmed.length === 0) return "empty";
  let parsed: URL;
  try {
    parsed = new URL(trimmed);
  } catch {
    return "invalid-url";
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    return "invalid-scheme";
  }
  if (parsed.username.length > 0 || parsed.password.length > 0) {
    return "embedded-credentials";
  }
  if (parsed.search.length > 0 || parsed.hash.length > 0) return "has-query";
  return null;
}

/** Placeholders a prompt may use. Anything else is rejected before saving. */
const classifierPromptPlaceholders = new Set(["labels"]);

export function classifierPromptProblem(prompt: string): string | null {
  for (const match of prompt.matchAll(/\{\{([^}]*)\}\}/g)) {
    if (!classifierPromptPlaceholders.has(match[1].trim())) return "unknown";
  }
  // A single unmatched brace would render literally; surface it as a typo.
  if (/\{\{?[^}]*$/.test(prompt) && !prompt.endsWith("}}")) return "unclosed";
  return null;
}
