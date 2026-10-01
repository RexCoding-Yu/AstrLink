import { describe, expect, it } from "vitest";

import {
  parseProviderImportNotice,
  planProviderImport,
  providerImportInput,
  type ProviderImportLink,
  type ProviderImportPlan,
} from "./provider-import-model";

const id = "0123456789abcdef0123456789abcdef";

function link(overrides: Partial<ProviderImportLink> = {}): ProviderImportLink {
  return {
    kind: "newapi",
    name: null,
    base_url: "https://relay.example.com",
    auth: null,
    auth_header: null,
    protocols: [],
    models: [],
    has_api_key: true,
    api_key_hint: "…cdef",
    ...overrides,
  };
}

function plan(overrides: Partial<ProviderImportLink> = {}): ProviderImportPlan {
  const result = planProviderImport(link(overrides), []);
  if (!result.ok) throw new Error(`unexpected ${result.reason}`);
  return result.plan;
}

describe("provider import notices", () => {
  it("parses pending and invalid notices", () => {
    expect(
      parseProviderImportNotice({ status: "pending", id, provider: link() }),
    ).toEqual({ status: "pending", id, provider: link() });
    expect(
      parseProviderImportNotice({
        status: "invalid",
        id,
        reason: "missing_parameter",
        field: "base_url",
      }),
    ).toEqual({
      status: "invalid",
      id,
      reason: "missing_parameter",
      field: "base_url",
    });
  });

  it("rejects notices that could carry more than a key hint", () => {
    for (const notice of [
      { status: "pending", id, provider: { ...link(), api_key: "sk-secret" } },
      {
        status: "pending",
        id,
        provider: { ...link(), api_key_hint: "sk-secret-value" },
      },
      { status: "pending", id, provider: { ...link(), auth: "basic" } },
      { status: "pending", id: "not-an-id", provider: link() },
      { status: "invalid", id, reason: "rate_limited", field: null },
      { status: "invalid", id, reason: "malformed" },
      { status: "accepted", id },
    ]) {
      expect(() => parseProviderImportNotice(notice)).toThrow(
        /provider-import IPC/,
      );
    }
  });
});

describe("planning a provider import", () => {
  it("fills what the link leaves out from the kind's preset", () => {
    const result = plan();
    expect(result).toMatchObject({
      kind: "newapi",
      // A gateway preset has no vendor name of its own.
      defaultName: "relay.example.com",
      host: "relay.example.com",
      insecure: false,
      auth: { scheme: "bearer" },
      keyIncluded: true,
      keyHint: "…cdef",
      needsKey: false,
      models: [],
    });
    expect(result.capabilities).toHaveLength(8);

    expect(
      plan({ kind: "kimi_coding", base_url: "https://api.kimi.ai/coding" }),
    ).toMatchObject({
      defaultName: "Kimi Coding",
      models: ["kimi-for-coding"],
    });
  });

  it("applies the link's name, sign-in method, protocols and models", () => {
    const result = plan({
      kind: "gemini",
      name: "Team Gemini",
      auth: "custom_header",
      auth_header: "X-Api-Key",
      protocols: ["openai.chat", "google.models"],
      models: ["gemini-3-pro"],
    });
    expect(result).toMatchObject({
      defaultName: "Team Gemini",
      auth: { scheme: "custom_header", header_name: "X-Api-Key" },
      models: ["gemini-3-pro"],
    });
    // The preset's conversion for Chat Completions carries over.
    expect(result.capabilities).toEqual([
      {
        protocol: "openai.chat",
        mode: "native",
        streaming: true,
        convert_to: "google.generate_content",
      },
      { protocol: "google.models", mode: "native", streaming: false },
    ]);
  });

  it("refuses what the preset cannot complete", () => {
    expect(planProviderImport(link({ kind: "acme" }), [])).toEqual({
      ok: false,
      reason: "unsupported",
      field: "kind",
    });
    expect(
      planProviderImport(link({ protocols: ["acme.chat"] }), []),
    ).toMatchObject({ ok: false, reason: "unsupported", field: "protocols" });
    expect(planProviderImport(link({ kind: "custom" }), [])).toMatchObject({
      ok: false,
      reason: "missing_parameter",
      field: "protocols",
    });
    expect(planProviderImport(link({ auth: "none" }), [])).toMatchObject({
      ok: false,
      reason: "invalid_parameter",
      field: "auth",
    });
    expect(
      planProviderImport(
        link({ kind: "custom", protocols: ["openai.chat"] }),
        [],
      ),
    ).toMatchObject({ ok: true });
  });

  it("warns only about plain http that leaves this machine", () => {
    expect(plan({ base_url: "http://relay.example.com/v1" }).insecure).toBe(
      true,
    );
    expect(plan({ base_url: "http://192.168.1.20:3000" }).insecure).toBe(true);
    for (const base_url of [
      "http://127.0.0.1:3000",
      "http://localhost:3000",
      "http://api.localhost",
      "http://[::1]:8080",
    ]) {
      expect(plan({ base_url }).insecure, base_url).toBe(false);
    }
  });

  it("asks for a key only when the sign-in method needs one", () => {
    expect(plan({ has_api_key: false, api_key_hint: null }).needsKey).toBe(
      true,
    );
    expect(
      plan({ has_api_key: false, api_key_hint: null, auth: "none" }).needsKey,
    ).toBe(false);
  });
});

describe("provider import input", () => {
  it("leaves the link's key to the host and sends only a typed one", () => {
    const keyed = plan({ models: ["model-a"] });
    expect(providerImportInput(keyed, "  Relay  ", "typed")).toEqual({
      name: "Relay",
      kind: "newapi",
      enabled: true,
      models: ["model-a"],
      http: {
        base_url: "https://relay.example.com",
        auth: { scheme: "bearer" },
      },
      capabilities: keyed.capabilities,
    });

    const keyless = plan({ has_api_key: false, api_key_hint: null });
    expect(providerImportInput(keyless, "Relay", "sk-typed").http).toEqual({
      base_url: "https://relay.example.com",
      auth: { scheme: "bearer" },
      credential: { secret: "sk-typed" },
    });
    expect(providerImportInput(keyless, "Relay", "  ").http).not.toHaveProperty(
      "credential",
    );
  });
});
