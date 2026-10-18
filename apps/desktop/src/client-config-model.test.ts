import { describe, expect, it } from "vitest";

import {
  parseClientConfigApplyOutcome,
  parseClientConfigCopied,
  parseClientConfigSnippet,
  parseClientConfigOverview,
  parseClientProxyCheck,
} from "./client-config-model";

function statuses(
  claude: Record<string, unknown> = {},
  codex: Record<string, unknown> = {},
  pi: Record<string, unknown> = {},
): Record<string, unknown>[] {
  return [
    {
      client: "claude",
      wsl: null,
      detected: true,
      paths: ["/Users/me/.claude/settings.json"],
      state: "not_configured",
      token_id: null,
      models: {},
      ...claude,
    },
    {
      client: "codex",
      wsl: null,
      detected: false,
      paths: ["/Users/me/.codex/config.toml"],
      state: "not_configured",
      token_id: null,
      models: {},
      ...codex,
    },
    {
      client: "pi",
      wsl: null,
      detected: true,
      paths: [
        "/Users/me/.pi/agent/models.json",
        "/Users/me/.pi/agent/settings.json",
      ],
      state: "not_configured",
      token_id: null,
      models: {},
      ...pi,
    },
  ];
}

function parseStatuses(clients: unknown[]) {
  return parseClientConfigOverview({
    clients,
    wsl_unchecked: [],
    wsl_localhost: false,
    cc_switch_wsl: { claude: null, codex: null },
  }).clients;
}

function inWSL(name: string) {
  return statuses().map((status) => ({ ...status, wsl: name }));
}

describe("client-config IPC parsing", () => {
  it("accepts this computer's clients, then each WSL home's", () => {
    const value = {
      clients: [...statuses(), ...inWSL("Ubuntu"), ...inWSL("Debian")],
      wsl_unchecked: ["Arch"],
      wsl_localhost: true,
      cc_switch_wsl: { claude: null, codex: null },
    };
    expect(parseClientConfigOverview(value)).toEqual(value);
    for (const clients of [
      inWSL("Ubuntu"),
      [...statuses(), ...inWSL("Ubuntu"), ...inWSL("Ubuntu")],
      [...statuses(), ...inWSL("Ubuntu").slice(0, 2)],
      [...statuses(), ...statuses()],
    ]) {
      expect(() => parseStatuses(clients)).toThrow(
        "Invalid client-config IPC response",
      );
    }
    expect(() =>
      parseClientConfigOverview({ ...value, wsl_localhost: "yes" }),
    ).toThrow("wsl_localhost");
    expect(() =>
      parseClientConfigOverview({ ...value, wsl_unchecked: [""] }),
    ).toThrow("wsl_unchecked");
  });

  it("accepts the WSL distribution CC Switch writes Claude Code or Codex in", () => {
    const value = {
      clients: statuses(),
      wsl_unchecked: [],
      wsl_localhost: false,
      cc_switch_wsl: { claude: null, codex: "Ubuntu-24.04" },
    };
    expect(parseClientConfigOverview(value)).toEqual(value);
    const { cc_switch_wsl: _, ...missing } = value;
    expect(() => parseClientConfigOverview(missing)).toThrow("cc_switch_wsl");
    for (const ccSwitchWSL of [
      null,
      { claude: null },
      { claude: null, codex: null, pi: null },
      { claude: "", codex: null },
      { claude: null, codex: "Ubuntu\n24.04" },
      { claude: null, codex: true },
    ]) {
      expect(() =>
        parseClientConfigOverview({ ...value, cc_switch_wsl: ccSwitchWSL }),
      ).toThrow("cc_switch_wsl");
    }
  });

  it("accepts one status per direct client in order", () => {
    const value = statuses(
      { state: "configured", token_id: "token_01" },
      { state: "outdated", token_id: "token_02" },
      { state: "modified", token_id: "token_01" },
    );
    expect(parseStatuses(value)).toEqual(value);
    // Another AstrLink's config names the token it was written with.
    expect(
      parseStatuses(statuses({ state: "elsewhere", token_id: "token_09" }))[0],
    ).toMatchObject({ state: "elsewhere", token_id: "token_09" });
    // A client moved to CC Switch holds no config AstrLink wrote.
    expect(parseStatuses(statuses({ state: "cc_switch" }))[0]).toMatchObject({
      state: "cc_switch",
      token_id: null,
    });
    // An unreadable file may or may not have a record behind it.
    for (const tokenID of [null, "token_01"]) {
      expect(
        parseStatuses(statuses({ state: "invalid", token_id: tokenID }))[0],
      ).toMatchObject({ state: "invalid", token_id: tokenID });
    }
  });

  it.each([
    ["a missing client", statuses().slice(0, 1)],
    ["clients out of order", statuses().reverse()],
    ["a client it cannot write", statuses({ client: "gemini" })],
    ["an unknown state", statuses({ state: "stale" })],
    ["an unexpected field", statuses({ token: "astr_x" })],
    ["a multi-line WSL name", statuses({ wsl: "a\nb" })],
    ["a non-boolean detection", statuses({ detected: "yes" })],
    ["no config path", statuses({ paths: [] })],
    ["a multi-line path", statuses({ paths: ["/tmp/a\n/tmp/b"] })],
    ["a malformed token ID", statuses({ state: "configured", token_id: "X" })],
    ["a configured client without a token", statuses({ state: "modified" })],
    [
      "another AstrLink's config without a token",
      statuses({ state: "elsewhere" }),
    ],
    ["an unconfigured client with a token", statuses({ token_id: "token_01" })],
    [
      "a client moved to CC Switch with a token",
      statuses({ state: "cc_switch", token_id: "token_01" }),
    ],
    ["models that are not an object", statuses({ models: ["gpt-5"] })],
    [
      "a model tier outside Claude Code",
      statuses({}, { models: { opusModel: "x" } }),
    ],
    ["an unknown model field", statuses({ models: { name: "x" } })],
    ["an empty model", statuses({ models: { model: "" } })],
    ["a multi-line model", statuses({ models: { model: "a\nb" } })],
    ["an overlong model", statuses({ models: { model: "x".repeat(257) } })],
  ])("rejects %s", (_, value) => {
    expect(() => parseStatuses(value)).toThrow(
      "Invalid client-config IPC response",
    );
  });

  it("accepts the models each client takes", () => {
    const value = statuses(
      {
        state: "configured",
        token_id: "token_01",
        models: { model: "main", haikuModel: "haiku", fableModel: "fable" },
      },
      { state: "configured", token_id: "token_01", models: { model: "gpt-5" } },
      { state: "modified", token_id: "token_01", models: { model: "pi" } },
    );
    expect(parseStatuses(value)).toEqual(value);
  });

  it("accepts applied writes, key-only conflicts and another gateway", () => {
    expect(parseClientConfigApplyOutcome({ status: "applied" })).toEqual({
      status: "applied",
    });
    const conflict = {
      status: "needs_confirmation",
      keys: ["env.ANTHROPIC_API_KEY", "model_providers.astrlink"],
      gateway: null,
    };
    expect(parseClientConfigApplyOutcome(conflict)).toEqual(conflict);
    const elsewhere = {
      status: "needs_confirmation",
      keys: [],
      gateway: "http://localhost:8317/v1",
    };
    expect(parseClientConfigApplyOutcome(elsewhere)).toEqual(elsewhere);
    for (const value of [
      { status: "needs_confirmation", keys: [], gateway: null },
      { status: "needs_confirmation", keys: ["a\nb"], gateway: null },
      { status: "needs_confirmation", keys: ["x".repeat(513)], gateway: null },
      { status: "needs_confirmation", keys: [], gateway: "" },
      { status: "needs_confirmation", keys: [], gateway: "http://a\n/v1" },
      { status: "needs_confirmation", keys: ["model"] },
      { status: "needs_confirmation" },
      { status: "applied", keys: ["model"] },
      { status: "skipped" },
      null,
    ]) {
      expect(() => parseClientConfigApplyOutcome(value)).toThrow(
        "Invalid client-config IPC response",
      );
    }
  });

  it("accepts previews that show only the token hint", () => {
    const preview = '[model_providers.astrlink]\ntoken = "astr_…K8Q2"\n';
    expect(parseClientConfigSnippet(preview)).toBe(preview);
    for (const value of ["", "  \n", 42, `token = "astr_${"A".repeat(43)}"`]) {
      expect(() => parseClientConfigSnippet(value)).toThrow(
        "Invalid client-config IPC response",
      );
    }
    expect(parseClientConfigCopied(false)).toBe(false);
    expect(() => parseClientConfigCopied("true")).toThrow(
      "Invalid client-config IPC response",
    );
  });

  it("accepts each proxy route, naming the proxy by host and port", () => {
    const value = {
      client: { route: "direct" },
      numeric: { route: "blocked", proxy: "127.0.0.1:7892" },
    };
    expect(parseClientProxyCheck(value)).toEqual(value);
    expect(
      parseClientProxyCheck({
        client: { route: "proxied", proxy: "proxy.lan:8080" },
        numeric: null,
      }),
    ).toEqual({
      client: { route: "proxied", proxy: "proxy.lan:8080" },
      numeric: null,
    });
  });

  it.each([
    ["a missing numeric route", { client: { route: "direct" } }],
    ["an unknown route", { client: { route: "tunnel" }, numeric: null }],
    [
      "a direct route naming a proxy",
      { client: { route: "direct", proxy: "127.0.0.1:7892" }, numeric: null },
    ],
    [
      "a blocked route without a proxy",
      { client: { route: "blocked" }, numeric: null },
    ],
    [
      "a multi-line proxy",
      { client: { route: "blocked", proxy: "a\nb" }, numeric: null },
    ],
    [
      "an unexpected field",
      { client: { route: "direct" }, numeric: null, url: "http://x" },
    ],
  ])("rejects %s", (_, value) => {
    expect(() => parseClientProxyCheck(value)).toThrow(
      "Invalid client-config IPC response",
    );
  });
});
