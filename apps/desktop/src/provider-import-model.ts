import type {
  HTTPServiceCreateInput,
  HTTPServiceKind,
  ServiceAuth,
  ServiceAuthScheme,
  ServiceCapability,
} from "./service-model";
import {
  httpServicePreset,
  httpServicePresetIDs,
  protocolDescriptors,
  type ProtocolDescriptor,
} from "./service-presets";

/** An `astrlink://v1/providers/import` link as the host parsed it. */
export interface ProviderImportLink {
  kind: string;
  name: string | null;
  base_url: string;
  auth: ServiceAuthScheme | null;
  auth_header: string | null;
  /** Empty lists fall back to the kind's defaults. */
  protocols: string[];
  models: string[];
  has_api_key: boolean;
  api_key_hint: string | null;
}

export type ProviderImportReason =
  | "malformed"
  | "unsupported"
  | "unknown_parameter"
  | "duplicate_parameter"
  | "missing_parameter"
  | "invalid_parameter";

export type ProviderImportNotice =
  | { status: "pending"; id: string; provider: ProviderImportLink }
  | {
      status: "invalid";
      id: string;
      reason: ProviderImportReason;
      field: string | null;
    };

type JsonObject = Record<string, unknown>;

const idPattern = /^[0-9a-f]{32}$/;
const authSchemes = new Set<string>([
  "none",
  "bearer",
  "anthropic_api_key",
  "google_api_key",
  "custom_header",
]);
const reasons = new Set<string>([
  "malformed",
  "unsupported",
  "unknown_parameter",
  "duplicate_parameter",
  "missing_parameter",
  "invalid_parameter",
]);

function invalid(path: string, message: string): never {
  throw new Error(
    `Invalid provider-import IPC response at ${path}: ${message}`,
  );
}

function objectAt(value: unknown, path: string): JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return invalid(path, "expected an object");
  }
  return value as JsonObject;
}

function exactKeys(
  object: JsonObject,
  expected: readonly string[],
  path: string,
): void {
  const expectedSet = new Set(expected);
  for (const key of Object.keys(object)) {
    if (!expectedSet.has(key)) invalid(`${path}.${key}`, "unexpected field");
  }
  for (const key of expected) {
    if (!Object.hasOwn(object, key)) invalid(`${path}.${key}`, "missing field");
  }
}

function stringAt(value: unknown, path: string): string {
  if (typeof value !== "string" || value === "") {
    return invalid(path, "expected a non-empty string");
  }
  return value;
}

function nullableStringAt(value: unknown, path: string): string | null {
  return value === null ? null : stringAt(value, path);
}

function stringsAt(value: unknown, path: string): string[] {
  if (!Array.isArray(value)) return invalid(path, "expected an array");
  return value.map((item, index) => stringAt(item, `${path}[${index}]`));
}

function parseLink(value: unknown, path: string): ProviderImportLink {
  const link = objectAt(value, path);
  exactKeys(
    link,
    [
      "kind",
      "name",
      "base_url",
      "auth",
      "auth_header",
      "protocols",
      "models",
      "has_api_key",
      "api_key_hint",
    ],
    path,
  );
  const auth = nullableStringAt(link.auth, `${path}.auth`);
  if (auth !== null && !authSchemes.has(auth)) {
    invalid(`${path}.auth`, "unknown auth scheme");
  }
  if (typeof link.has_api_key !== "boolean") {
    invalid(`${path}.has_api_key`, "expected a boolean");
  }
  const hint = nullableStringAt(link.api_key_hint, `${path}.api_key_hint`);
  // Core's hint shape: an ellipsis and the key's last four characters.
  if (hint !== null && (!hint.startsWith("…") || [...hint].length !== 5)) {
    invalid(`${path}.api_key_hint`, "must be a non-secret display hint");
  }
  return {
    kind: stringAt(link.kind, `${path}.kind`),
    name: nullableStringAt(link.name, `${path}.name`),
    base_url: stringAt(link.base_url, `${path}.base_url`),
    auth: auth as ServiceAuthScheme | null,
    auth_header: nullableStringAt(link.auth_header, `${path}.auth_header`),
    protocols: stringsAt(link.protocols, `${path}.protocols`),
    models: stringsAt(link.models, `${path}.models`),
    has_api_key: link.has_api_key,
    api_key_hint: hint,
  };
}

export function parseProviderImportNotice(
  value: unknown,
  path = "$",
): ProviderImportNotice {
  const notice = objectAt(value, path);
  const id = stringAt(notice.id, `${path}.id`);
  if (!idPattern.test(id)) invalid(`${path}.id`, "invalid import ID");
  if (notice.status === "pending") {
    exactKeys(notice, ["status", "id", "provider"], path);
    return {
      status: "pending",
      id,
      provider: parseLink(notice.provider, `${path}.provider`),
    };
  }
  if (notice.status === "invalid") {
    exactKeys(notice, ["status", "id", "reason", "field"], path);
    if (typeof notice.reason !== "string" || !reasons.has(notice.reason)) {
      invalid(`${path}.reason`, "unknown reason");
    }
    return {
      status: "invalid",
      id,
      reason: notice.reason as ProviderImportReason,
      field: nullableStringAt(notice.field, `${path}.field`),
    };
  }
  return invalid(`${path}.status`, "expected pending or invalid");
}

/** What the confirmation dialog shows and creates. */
export interface ProviderImportPlan {
  kind: HTTPServiceKind;
  defaultName: string;
  baseURL: string;
  host: string;
  /** Plain http to another machine: the key would cross the network unencrypted. */
  insecure: boolean;
  auth: ServiceAuth;
  keyIncluded: boolean;
  keyHint: string | null;
  /** The link has no key but the sign-in method needs one. */
  needsKey: boolean;
  capabilities: ServiceCapability[];
  models: string[];
}

export type ProviderImportPlanResult =
  | { ok: true; plan: ProviderImportPlan }
  | {
      ok: false;
      reason: Extract<
        ProviderImportReason,
        "unsupported" | "missing_parameter" | "invalid_parameter"
      >;
      field: "kind" | "auth" | "protocols";
    };

function isLoopback(hostname: string): boolean {
  return (
    hostname === "localhost" ||
    hostname.endsWith(".localhost") ||
    hostname === "[::1]" ||
    /^127(?:\.\d{1,3}){3}$/.test(hostname)
  );
}

/** Resolves a link against the kind's preset, as adding it by hand would. */
export function planProviderImport(
  link: ProviderImportLink,
  discovered: readonly ProtocolDescriptor[],
): ProviderImportPlanResult {
  if (!(httpServicePresetIDs as readonly string[]).includes(link.kind)) {
    return { ok: false, reason: "unsupported", field: "kind" };
  }
  const kind = link.kind as HTTPServiceKind;
  const preset = httpServicePreset(kind, discovered);

  const scheme = link.auth ?? preset.authScheme;
  if (scheme === "none" && link.has_api_key) {
    return { ok: false, reason: "invalid_parameter", field: "auth" };
  }
  const headerName = link.auth_header ?? preset.headerName;
  if (scheme === "custom_header" && !headerName) {
    return { ok: false, reason: "missing_parameter", field: "auth" };
  }
  const auth: ServiceAuth =
    scheme === "custom_header"
      ? { scheme, header_name: headerName }
      : { scheme };

  let capabilities = preset.capabilities;
  if (link.protocols.length > 0) {
    const descriptors = new Map(
      protocolDescriptors(discovered).map((protocol) => [
        protocol.id,
        protocol,
      ]),
    );
    const planned: ServiceCapability[] = [];
    for (const protocol of link.protocols) {
      const descriptor = descriptors.get(protocol);
      if (!descriptor) {
        return { ok: false, reason: "unsupported", field: "protocols" };
      }
      planned.push(
        preset.capabilities.find(
          (capability) => capability.protocol === protocol,
        ) ?? { protocol, mode: "native", streaming: descriptor.streaming },
      );
    }
    capabilities = planned;
  }
  if (capabilities.length === 0) {
    return { ok: false, reason: "missing_parameter", field: "protocols" };
  }

  const url = new URL(link.base_url);
  return {
    ok: true,
    plan: {
      kind,
      // Presets without a vendor site would otherwise all read "Custom".
      defaultName:
        link.name ?? (preset.sites.length > 0 ? preset.defaultName : url.host),
      baseURL: link.base_url,
      host: url.host,
      insecure: url.protocol === "http:" && !isLoopback(url.hostname),
      auth,
      keyIncluded: link.has_api_key,
      keyHint: link.api_key_hint,
      needsKey: scheme !== "none" && !link.has_api_key,
      capabilities: capabilities.map((capability) => ({ ...capability })),
      models:
        link.models.length > 0 ? [...link.models] : [...(preset.models ?? [])],
    },
  };
}

/**
 * The create request the host completes. A key from the link is attached by
 * the host; `secret` is only the one the user typed when the link had none.
 */
export function providerImportInput(
  plan: ProviderImportPlan,
  name: string,
  secret: string,
): HTTPServiceCreateInput {
  return {
    name: name.trim(),
    kind: plan.kind,
    enabled: true,
    models: plan.models,
    http: {
      base_url: plan.baseURL,
      auth: plan.auth,
      ...(plan.needsKey && secret.trim() ? { credential: { secret } } : {}),
    },
    capabilities: plan.capabilities,
  };
}
