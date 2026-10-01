import { describe, expect, it } from "vitest";

import {
  rawProofOf,
  rawSealingErrorMessage,
  refusalResult,
} from "./raw-sealing-ui";

function controlError(status: string, code: string): Error {
  return new Error(
    `POST /v1/raw-sealing/unlock returned ${status}: {"error":{"code":"${code}","message":"raw"}}`,
  );
}

describe("rawSealingErrorMessage", () => {
  it.each([
    ["409 Conflict", "raw_access_unavailable", "还没设置原文保护。"],
    ["409 Conflict", "raw_password_not_set", "还没设置原文保护。"],
    ["409 Conflict", "raw_sealing_unavailable", "当前 Core 不支持原文封存。"],
    [
      "409 Conflict",
      "raw_password_already_set",
      "原文口令已经设置，请刷新后使用「修改」。",
    ],
    [
      "422 Unprocessable Entity",
      "raw_proof_required",
      "此操作需要先验证身份，当前设备无法完成验证。",
    ],
    ["409 Conflict", "raw_sealing_changed", "原文口令状态已变化，请重试。"],
    ["400 Bad Request", "validation_failed", "口令不符合长度要求。"],
    [
      "500 Internal Server Error",
      "raw_vault_unavailable",
      "原文密钥暂时不可用，请稍后重试。",
    ],
  ])("maps %s %s to readable copy", (status, code, message) => {
    expect(rawSealingErrorMessage(controlError(status, code))).toBe(message);
  });

  it("keeps an unknown failure's own message", () => {
    expect(rawSealingErrorMessage(new Error("Core is not ready"))).toBe(
      "Core is not ready",
    );
    expect(rawSealingErrorMessage(new Error(""))).toBe("操作未完成，请重试。");
  });
});

describe("rawProofOf", () => {
  it("carries a password, and nothing for a confirmation", () => {
    expect(rawProofOf({ kind: "password", password: "correct horse" })).toEqual(
      { kind: "password", password: "correct horse" },
    );
    expect(rawProofOf({ kind: "confirm" })).toBeUndefined();
  });
});

describe("refusalResult", () => {
  it("keeps the dialog open for each recoverable refusal", () => {
    expect(refusalResult({ outcome: "password_invalid" })).toEqual({
      kind: "password_invalid",
    });
    expect(
      refusalResult({ outcome: "backoff", retry_after_seconds: 30 }),
    ).toEqual({ kind: "backoff", retryAfterSeconds: 30 });
  });
});
