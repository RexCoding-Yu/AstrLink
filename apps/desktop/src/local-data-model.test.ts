import { describe, expect, it } from "vitest";

import { parseLocalDataStatus } from "./local-data-model";

describe("local data status", () => {
  it("reads the counts Core reports", () => {
    expect(
      parseLocalDataStatus({
        unreadable_credentials: 2,
        unreadable_access_tokens: 0,
        audit_key_missing: true,
      }),
    ).toEqual({
      unreadable_credentials: 2,
      unreadable_access_tokens: 0,
      audit_key_missing: true,
    });
  });

  it("rejects anything but the documented counts", () => {
    const valid = {
      unreadable_credentials: 0,
      unreadable_access_tokens: 0,
      audit_key_missing: false,
    };
    expect(() => parseLocalDataStatus(null)).toThrow("$");
    expect(() => parseLocalDataStatus([])).toThrow("$");
    expect(() =>
      parseLocalDataStatus({ ...valid, unreadable_credentials: -1 }),
    ).toThrow("unreadable_credentials");
    expect(() =>
      parseLocalDataStatus({ ...valid, unreadable_access_tokens: 1.5 }),
    ).toThrow("unreadable_access_tokens");
    expect(() =>
      parseLocalDataStatus({ ...valid, audit_key_missing: "no" }),
    ).toThrow("audit_key_missing");
    expect(() =>
      parseLocalDataStatus({ ...valid, services: ["service_a"] }),
    ).toThrow("services");
    const { unreadable_credentials: _, ...missing } = valid;
    expect(() => parseLocalDataStatus(missing)).toThrow(
      "unreadable_credentials",
    );
  });
});
