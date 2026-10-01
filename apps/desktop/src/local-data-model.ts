import { invalidData, type DataProblem } from "./ipc-data-error";

/**
 * Saved data this device can no longer decrypt (plan §5.7). Core reports
 * counts only, never names or values.
 */
export interface LocalDataStatus {
  /** Service, proxy and tool credentials and account sign-ins to enter again. */
  unreadable_credentials: number;
  /** Access tokens whose value can no longer be shown; they still work. */
  unreadable_access_tokens: number;
  /** Bodies captured earlier cannot be opened on this device. */
  audit_key_missing: boolean;
}

type JsonObject = Record<string, unknown>;

const fields = [
  "unreadable_credentials",
  "unreadable_access_tokens",
  "audit_key_missing",
];

function invalid(path: string, problem: DataProblem): never {
  return invalidData("localData", path, problem);
}

function countAt(value: unknown, path: string): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0) {
    return invalid(path, "nonNegativeInteger");
  }
  return value;
}

export function parseLocalDataStatus(value: unknown): LocalDataStatus {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return invalid("$", "object");
  }
  const status = value as JsonObject;
  for (const key of Object.keys(status)) {
    if (!fields.includes(key)) invalid(`$.${key}`, "unsupportedField");
  }
  if (typeof status.audit_key_missing !== "boolean") {
    return invalid("$.audit_key_missing", "boolean");
  }
  return {
    unreadable_credentials: countAt(
      status.unreadable_credentials,
      "$.unreadable_credentials",
    ),
    unreadable_access_tokens: countAt(
      status.unreadable_access_tokens,
      "$.unreadable_access_tokens",
    ),
    audit_key_missing: status.audit_key_missing,
  };
}
