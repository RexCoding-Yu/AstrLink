import { describe, expect, it } from "vitest";
import {
  acceptLocalClients,
  emptyLocalClients,
  parseLocalClients,
} from "./local-client-model";

function withWSL() {
  const snapshot = emptyLocalClients();
  snapshot.clients.push({
    ...snapshot.clients[1],
    wsl: "Ubuntu-24.04",
    phase: "available",
  });
  snapshot.wsl_unchecked = ["Debian"];
  return snapshot;
}

describe("local client IPC", () => {
  it("accepts every client and prevents stale responses overwriting progress", () => {
    const initial = emptyLocalClients();
    expect(parseLocalClients(initial)).toEqual(initial);
    const bun = emptyLocalClients();
    bun.clients[3].install_method = "bun";
    expect(parseLocalClients(bun)).toEqual(bun);
    const next = { ...initial, revision: 3, busy: true };
    expect(acceptLocalClients(next, initial)).toBe(next);
    expect(acceptLocalClients(initial, next)).toBe(next);
  });
  it("accepts clients found in WSL beside this computer's", () => {
    const snapshot = withWSL();
    expect(parseLocalClients(snapshot)).toEqual(snapshot);
  });
  it("rejects unknown clients, duplicate clients, invalid phases and malformed values", () => {
    for (const field of [
      { id: "other" },
      { phase: "running-shell" },
      { can_update: "true" },
      { current_version: 123 },
      { other_installations: [true] },
      { install_method: "arbitrary" },
      { wsl: "" },
      { wsl: 1 },
    ]) {
      const snapshot = emptyLocalClients();
      Object.assign(snapshot.clients[0], field);
      expect(() => parseLocalClients(snapshot)).toThrow();
    }
    const duplicate = emptyLocalClients();
    duplicate.clients[1].id = "codex";
    expect(() => parseLocalClients(duplicate)).toThrow();
    const duplicateWSL = withWSL();
    duplicateWSL.clients.push({ ...duplicateWSL.clients[4] });
    expect(() => parseLocalClients(duplicateWSL)).toThrow();
    // A WSL client never stands in for this computer's.
    const moved = emptyLocalClients();
    moved.clients[0].wsl = "Ubuntu";
    expect(() => parseLocalClients(moved)).toThrow();
    expect(() =>
      parseLocalClients({ ...emptyLocalClients(), wsl_unchecked: [""] }),
    ).toThrow();
    expect(() =>
      parseLocalClients({ ...emptyLocalClients(), wsl_unchecked: undefined }),
    ).toThrow();
    expect(() =>
      parseLocalClients({ ...emptyLocalClients(), revision: -1 }),
    ).toThrow();
    expect(() =>
      parseLocalClients({ ...emptyLocalClients(), clients: [] }),
    ).toThrow();
  });
});
