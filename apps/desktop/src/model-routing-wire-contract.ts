/**
 * The model routing wire contract, as the desktop reads it.
 *
 * `core/contract/model_routing.go` owns these names; this file is the desktop's
 * declaration of what it expects to receive, kept in one place so the parser and
 * the cross-layer test cannot drift apart. `model-routing-wire-contract.test.ts`
 * reads the Go struct tags and fails when the two disagree.
 *
 * Required keys are those the sender always writes; optional keys carry
 * `omitempty` on the Go side.
 */
export const modelRoutingWireContract = {
  decision: {
    required: [
      "schema_version",
      "virtual_model_id",
      "group_id",
      "config_snapshot_version",
      "selection_source",
      "used_default",
      "fields",
      "member_id",
      "member_service_id",
      "member_model",
      "execution",
      "privacy_attempts",
      "duration_ms",
    ],
    optional: [
      "rule_id",
      "rule_priority",
      "fallback_reason",
      "classifier_duration_ms",
      "error_code",
    ],
  },
  /** `match_count` is a plain Go int, so it is always on the wire, zero or not. */
  fieldObservation: {
    required: ["field", "source", "known", "match_count"],
    optional: [
      "classifier_confidence",
      "classifier_id",
      "skip_reason",
      "error_code",
      // The classifier's candidate sequence, carried as `omitempty`: absent
      // both on records written before it existed and on fields that took no
      // scored fallback.
      "candidates",
    ],
  },
  /**
   * One entry of a field's candidate sequence. Every member is unconditional on
   * the Go side, so `chosen` is on the wire as `false` rather than being
   * omitted — a reader may not treat a missing `chosen` as "not chosen".
   */
  candidateObservation: {
    required: ["value", "score", "rank", "chosen"],
    optional: [],
  },
  execution: {
    required: ["status", "attempt_count"],
    optional: ["error_code"],
  },
  privacyAttempt: {
    required: [
      "attempt_index",
      "service_id",
      "decision",
      "finding_count",
      "duration_ms",
    ],
    optional: ["error_code"],
  },
} as const;
