import {
  classifierEndpointProblem,
  classifierExistingModelTimeoutMaxMS,
  classifierExistingModelTimeoutMinMS,
  classifierPromptProblem,
  classifierTimeoutMaxMS,
  type ClassifierSettings,
} from "./classifier-settings-model";

/**
 * A field-level problem found before saving. `field` doubles as the wire
 * selector for the message and as the anchor for the inline marker.
 */
export interface ClassifierSettingsIssue {
  field:
    | "protocol"
    | "endpoint"
    | "model"
    | "apiKey"
    | "prompt"
    | "timeout_ms"
    | "low_confidence_threshold";
  problem: string;
}

/**
 * Field-level gate run before saving. Kept out of the model module so the
 * validation rules and their message keys are read together.
 *
 * The disabled classifier saves an incomplete draft on purpose — turning it on
 * later must not require filling everything in first.
 */
export function calculateClassifierSettingsIssues(
  settings: ClassifierSettings,
  options: { credentialConfigured: boolean; credentialDraft: string },
): ClassifierSettingsIssue[] {
  if (!settings.enabled) return [];
  const issues: ClassifierSettingsIssue[] = [];
  // Only `inline` and `existing_model` are settable from the panel. A stored
  // `provider_ref` document still reaches here (the wire accepts it), and the
  // backend rejects it with 422 while enabled, so the panel must say so rather
  // than silently rewriting the user's stored source.
  if (settings.classifier_source === "provider_ref") {
    issues.push({ field: "protocol", problem: "unsupportedSource" });
    return issues;
  }
  if (settings.classifier_source === "existing_model") {
    const { existing_model: existing } = settings;
    if (existing.service_id.trim().length === 0) {
      issues.push({ field: "model", problem: "empty" });
    }
    if (existing.model.trim().length === 0) {
      issues.push({ field: "model", problem: "empty" });
    }
    if (
      !Number.isFinite(existing.timeout_ms) ||
      existing.timeout_ms < classifierExistingModelTimeoutMinMS ||
      existing.timeout_ms > classifierExistingModelTimeoutMaxMS
    ) {
      issues.push({ field: "timeout_ms", problem: "outOfRange" });
    }
    return issues;
  }
  const { inline } = settings;
  const endpointProblem = classifierEndpointProblem(inline.endpoint);
  if (endpointProblem !== null) {
    issues.push({ field: "endpoint", problem: endpointProblem });
  }
  if (inline.model.trim().length === 0) {
    issues.push({ field: "model", problem: "empty" });
  }
  if (!options.credentialConfigured && options.credentialDraft.trim() === "") {
    issues.push({ field: "apiKey", problem: "empty" });
  }
  if (
    !Number.isFinite(inline.timeout_ms) ||
    inline.timeout_ms <= 0 ||
    inline.timeout_ms > classifierTimeoutMaxMS
  ) {
    issues.push({ field: "timeout_ms", problem: "outOfRange" });
  }
  if (
    !Number.isFinite(inline.low_confidence_threshold) ||
    inline.low_confidence_threshold < 0 ||
    inline.low_confidence_threshold > 1
  ) {
    issues.push({ field: "low_confidence_threshold", problem: "outOfRange" });
  }
  const promptProblem = classifierPromptProblem(inline.prompt);
  if (promptProblem !== null) {
    issues.push({ field: "prompt", problem: promptProblem });
  }
  return issues;
}
