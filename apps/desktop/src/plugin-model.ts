import { invalidData, type DataProblem } from "./ipc-data-error";

/** What a plugin asks for; the user grants each one when enabling it. */
export const PLUGIN_CAPABILITIES = [
  "context.inject",
  "request.read",
  "network",
  "storage",
  "ui.views",
] as const;

export type PluginCapability = (typeof PLUGIN_CAPABILITIES)[number];

export type PluginSource = "builtin" | "directory" | "override";

export interface PluginLoadError {
  code: string;
  file?: string;
  line?: number;
  column?: number;
  message: string;
}

export interface PluginSummary {
  key: string;
  name: string;
  version: string;
  source: PluginSource;
  file?: string;
  enabled: boolean;
  state: "loaded" | "failed";
  error?: PluginLoadError;
  requested_capabilities: string[];
  granted_capabilities: string[];
  needs_network: boolean;
}

export interface PluginList {
  directory: string;
  plugins: PluginSummary[];
}

export interface PluginNetworkOrigin {
  origin: string;
  allow_plaintext: boolean;
  /** Plain http to a host other than this machine: it needs the user's consent. */
  plaintext: boolean;
}

export interface PluginCredentialStatus {
  name: string;
  label: string;
  configured: boolean;
  hint?: string;
  origin?: string;
}

export interface PluginMetadataAttribute {
  key: string;
  label: string;
  pattern: string;
  marker?: string;
}

export interface PluginDetail extends PluginSummary {
  network_purpose?: string;
  notice?: string;
  hooks: string[];
  /** The plugin's configuration, a JSON object. */
  config: Record<string, unknown>;
  network_origins: PluginNetworkOrigin[];
  privacy_trusted: boolean;
  credentials: PluginCredentialStatus[];
  metadata: PluginMetadataAttribute[];
}

export interface PluginSettingsUpdate {
  enabled?: boolean;
  granted_capabilities?: string[];
  network_origins?: Array<{ origin: string; allow_plaintext: boolean }>;
  privacy_trusted?: boolean;
  config?: Record<string, unknown>;
}

export type PluginViewItem =
  | { label: string; value: string; mono?: boolean }
  | { text: string; tone?: "ok" | "warn" | "error" };

export type PluginViewBlock =
  | {
      type: "kv";
      items: Array<{ label: string; value: string; mono?: boolean }>;
    }
  | { type: "table"; columns: string[]; rows: Array<Array<string | number>> }
  | {
      type: "badges";
      items: Array<{ text: string; tone?: "ok" | "warn" | "error" }>;
    }
  | { type: "text"; text: string };

export interface PluginNetworkCount {
  requests: number;
  denied: number;
  errors: number;
  wait_ms: number;
}

export interface PluginTestResult {
  /** What the plugin reported, or failed when the call itself failed. */
  status: "ok" | "error" | "failed";
  summary: string;
  view?: PluginViewBlock[];
  truncated?: boolean;
  reason?: string;
  elapsed_ms: number;
  network: PluginNetworkCount;
}

export interface PluginStatus {
  calls: number;
  failures: Record<string, number>;
  memory_limits: number;
  timeouts: number;
  network: PluginNetworkCount;
  view?: PluginViewBlock[];
  truncated?: boolean;
  view_error?: string;
}

export interface PluginInjection {
  placement: "system_append" | "turn_message_prefix";
  injected_bytes: number;
  replayed_blocks: number;
  dropped_items: number;
}

/** A plugin's part in a request: what it reported and what the host did. */
export interface RequestPluginRecord {
  plugin: string;
  plugin_version: string;
  hook: string;
  title: string;
  status: "ok" | "skipped" | "failed";
  summary: string;
  view?: PluginViewBlock[];
  truncated?: boolean;
  skipped_reason?: string;
  elapsed_ms: number;
  injection?: PluginInjection;
  network: PluginNetworkCount;
  observe?: string;
}

export interface MetadataAttribute {
  key: string;
  label: string;
  description: string;
  pattern: string;
  marker?: string;
  /** user, or the key of the plugin that registered it. */
  owner: string;
  created_at: string;
  updated_at: string;
  /** The request header that sets it. */
  header: string;
  /** The plugin that registered it is gone; the user may delete it. */
  owner_missing?: boolean;
}

export interface MetadataAttributeInput {
  key: string;
  label: string;
  description: string;
  pattern: string;
  marker: string;
}

export type MetadataSource = "header" | "session" | "token";

export interface MetadataValue {
  value: string;
  source: MetadataSource;
  set_by?: string;
  turn?: number;
}

export interface MetadataEvent {
  key: string;
  event: "bound" | "rebound" | "cleared" | "ignored";
  value?: string;
  set_by?: string;
  reason?: string;
}

/** The metadata of one request, kept with its record. */
export interface RequestMetadata {
  values: Record<string, MetadataValue>;
  events: MetadataEvent[];
}

type JsonObject = Record<string, unknown>;

function invalid(
  subject: "plugin" | "metadata",
  path: string,
  problem: DataProblem,
): never {
  return invalidData(subject, path, problem);
}

function reader(subject: "plugin" | "metadata") {
  const object = (value: unknown, path: string): JsonObject => {
    if (typeof value !== "object" || value === null || Array.isArray(value)) {
      return invalid(subject, path, "object");
    }
    return value as JsonObject;
  };
  const string = (value: unknown, path: string): string => {
    if (typeof value !== "string") return invalid(subject, path, "string");
    return value;
  };
  const optionalString = (value: unknown, path: string): string | undefined =>
    value === undefined ? undefined : string(value, path);
  const integer = (value: unknown, path: string): number => {
    if (typeof value !== "number" || !Number.isInteger(value) || value < 0) {
      return invalid(subject, path, "nonNegativeInteger");
    }
    return value;
  };
  const optionalInteger = (value: unknown, path: string): number | undefined =>
    value === undefined ? undefined : integer(value, path);
  const boolean = (value: unknown, path: string): boolean => {
    if (typeof value !== "boolean") return invalid(subject, path, "boolean");
    return value;
  };
  const optionalBoolean = (
    value: unknown,
    path: string,
  ): boolean | undefined =>
    value === undefined ? undefined : boolean(value, path);
  const array = (value: unknown, path: string): unknown[] => {
    if (!Array.isArray(value)) return invalid(subject, path, "array");
    return value;
  };
  const strings = (value: unknown, path: string): string[] =>
    array(value, path).map((item, index) => string(item, `${path}[${index}]`));
  const oneOf = <T extends string>(
    value: unknown,
    allowed: readonly T[],
    path: string,
  ): T => {
    if (typeof value !== "string" || !allowed.includes(value as T)) {
      return invalid(subject, path, "unknownType");
    }
    return value as T;
  };
  return {
    object,
    string,
    optionalString,
    integer,
    optionalInteger,
    boolean,
    optionalBoolean,
    array,
    strings,
    oneOf,
  };
}

const plugin = reader("plugin");
const metadata = reader("metadata");

/** Drops the keys whose value is undefined, so parsed objects compare cleanly. */
function defined<T extends object>(value: T): T {
  return Object.fromEntries(
    Object.entries(value).filter(([, entry]) => entry !== undefined),
  ) as T;
}

function parseLoadError(value: unknown, path: string): PluginLoadError {
  const error = plugin.object(value, path);
  return defined({
    code: plugin.string(error.code, `${path}.code`),
    file: plugin.optionalString(error.file, `${path}.file`),
    line: plugin.optionalInteger(error.line, `${path}.line`),
    column: plugin.optionalInteger(error.column, `${path}.column`),
    message: plugin.string(error.message, `${path}.message`),
  });
}

function parseSummaryAt(value: unknown, path: string): PluginSummary {
  const summary = plugin.object(value, path);
  return defined({
    key: plugin.string(summary.key, `${path}.key`),
    name: plugin.string(summary.name, `${path}.name`),
    version: plugin.string(summary.version, `${path}.version`),
    source: plugin.oneOf(
      summary.source,
      ["builtin", "directory", "override"] as const,
      `${path}.source`,
    ),
    file: plugin.optionalString(summary.file, `${path}.file`),
    enabled: plugin.boolean(summary.enabled, `${path}.enabled`),
    state: plugin.oneOf(
      summary.state,
      ["loaded", "failed"] as const,
      `${path}.state`,
    ),
    error:
      summary.error === undefined
        ? undefined
        : parseLoadError(summary.error, `${path}.error`),
    requested_capabilities: plugin.strings(
      summary.requested_capabilities,
      `${path}.requested_capabilities`,
    ),
    granted_capabilities: plugin.strings(
      summary.granted_capabilities,
      `${path}.granted_capabilities`,
    ),
    needs_network: plugin.boolean(
      summary.needs_network,
      `${path}.needs_network`,
    ),
  });
}

export function parsePluginList(value: unknown): PluginList {
  const list = plugin.object(value, "$");
  return {
    directory: plugin.string(list.directory, "$.directory"),
    plugins: plugin
      .array(list.plugins, "$.plugins")
      .map((item, index) => parseSummaryAt(item, `$.plugins[${index}]`)),
  };
}

export function parsePluginDetail(value: unknown): PluginDetail {
  const detail = plugin.object(value, "$");
  return defined({
    ...parseSummaryAt(value, "$"),
    network_purpose: plugin.optionalString(
      detail.network_purpose,
      "$.network_purpose",
    ),
    notice: plugin.optionalString(detail.notice, "$.notice"),
    hooks: plugin.strings(detail.hooks, "$.hooks"),
    config: plugin.object(detail.config, "$.config"),
    network_origins: plugin
      .array(detail.network_origins, "$.network_origins")
      .map((item, index) => {
        const path = `$.network_origins[${index}]`;
        const origin = plugin.object(item, path);
        return {
          origin: plugin.string(origin.origin, `${path}.origin`),
          allow_plaintext: plugin.boolean(
            origin.allow_plaintext,
            `${path}.allow_plaintext`,
          ),
          plaintext: plugin.boolean(origin.plaintext, `${path}.plaintext`),
        };
      }),
    privacy_trusted: plugin.boolean(
      detail.privacy_trusted,
      "$.privacy_trusted",
    ),
    credentials: plugin
      .array(detail.credentials, "$.credentials")
      .map((item, index) => {
        const path = `$.credentials[${index}]`;
        const credential = plugin.object(item, path);
        return defined({
          name: plugin.string(credential.name, `${path}.name`),
          label: plugin.string(credential.label, `${path}.label`),
          configured: plugin.boolean(
            credential.configured,
            `${path}.configured`,
          ),
          hint: plugin.optionalString(credential.hint, `${path}.hint`),
          origin: plugin.optionalString(credential.origin, `${path}.origin`),
        });
      }),
    metadata: plugin.array(detail.metadata, "$.metadata").map((item, index) => {
      const path = `$.metadata[${index}]`;
      const attribute = plugin.object(item, path);
      return defined({
        key: plugin.string(attribute.key, `${path}.key`),
        label: plugin.string(attribute.label, `${path}.label`),
        pattern: plugin.string(attribute.pattern, `${path}.pattern`),
        marker: plugin.optionalString(attribute.marker, `${path}.marker`),
      });
    }),
  });
}

function parseNetworkCount(value: unknown, path: string): PluginNetworkCount {
  const network = plugin.object(value, path);
  return {
    requests: plugin.integer(network.requests, `${path}.requests`),
    denied: plugin.integer(network.denied, `${path}.denied`),
    errors: plugin.integer(network.errors, `${path}.errors`),
    wait_ms: plugin.integer(network.wait_ms, `${path}.wait_ms`),
  };
}

const tones = ["ok", "warn", "error"] as const;

/** Reads a view the host already cleaned, failing on anything else. */
export function parsePluginView(
  value: unknown,
  path: string,
): PluginViewBlock[] {
  return plugin.array(value, path).map((item, index) => {
    const blockPath = `${path}[${index}]`;
    const block = plugin.object(item, blockPath);
    switch (block.type) {
      case "kv":
        return {
          type: "kv",
          items: plugin
            .array(block.items, `${blockPath}.items`)
            .map((entry, entryIndex) => {
              const itemPath = `${blockPath}.items[${entryIndex}]`;
              const kv = plugin.object(entry, itemPath);
              return defined({
                label: plugin.string(kv.label, `${itemPath}.label`),
                value:
                  plugin.optionalString(kv.value, `${itemPath}.value`) ?? "",
                mono: plugin.optionalBoolean(kv.mono, `${itemPath}.mono`),
              });
            }),
        };
      case "badges":
        return {
          type: "badges",
          items: plugin
            .array(block.items, `${blockPath}.items`)
            .map((entry, entryIndex) => {
              const itemPath = `${blockPath}.items[${entryIndex}]`;
              const badge = plugin.object(entry, itemPath);
              return defined({
                text: plugin.string(badge.text, `${itemPath}.text`),
                tone:
                  badge.tone === undefined
                    ? undefined
                    : plugin.oneOf(badge.tone, tones, `${itemPath}.tone`),
              });
            }),
        };
      case "table":
        return {
          type: "table",
          columns: plugin.strings(block.columns, `${blockPath}.columns`),
          rows: plugin
            .array(block.rows, `${blockPath}.rows`)
            .map((row, rowIndex) =>
              plugin
                .array(row, `${blockPath}.rows[${rowIndex}]`)
                .map((cell, cellIndex) => {
                  if (typeof cell === "number" && Number.isFinite(cell)) {
                    return cell;
                  }
                  return plugin.string(
                    cell,
                    `${blockPath}.rows[${rowIndex}][${cellIndex}]`,
                  );
                }),
            ),
        };
      case "text":
        return {
          type: "text",
          text: plugin.string(block.text, `${blockPath}.text`),
        };
      default:
        return invalid("plugin", `${blockPath}.type`, "unknownType");
    }
  });
}

function optionalView(
  value: unknown,
  path: string,
): PluginViewBlock[] | undefined {
  return value === undefined ? undefined : parsePluginView(value, path);
}

export function parsePluginTestResult(value: unknown): PluginTestResult {
  const result = plugin.object(value, "$");
  return defined({
    status: plugin.oneOf(
      result.status,
      ["ok", "error", "failed"] as const,
      "$.status",
    ),
    summary: plugin.string(result.summary, "$.summary"),
    view: optionalView(result.view, "$.view"),
    truncated: plugin.optionalBoolean(result.truncated, "$.truncated"),
    reason: plugin.optionalString(result.reason, "$.reason"),
    elapsed_ms: plugin.integer(result.elapsed_ms, "$.elapsed_ms"),
    network: parseNetworkCount(result.network, "$.network"),
  });
}

export function parsePluginStatus(value: unknown): PluginStatus {
  const status = plugin.object(value, "$");
  const failures = plugin.object(status.failures, "$.failures");
  return defined({
    calls: plugin.integer(status.calls, "$.calls"),
    failures: Object.fromEntries(
      Object.entries(failures).map(([reason, count]) => [
        reason,
        plugin.integer(count, `$.failures.${reason}`),
      ]),
    ),
    memory_limits: plugin.integer(status.memory_limits, "$.memory_limits"),
    timeouts: plugin.integer(status.timeouts, "$.timeouts"),
    network: parseNetworkCount(status.network, "$.network"),
    view: optionalView(status.view, "$.view"),
    truncated: plugin.optionalBoolean(status.truncated, "$.truncated"),
    view_error: plugin.optionalString(status.view_error, "$.view_error"),
  });
}

/** Reads the plugin_records of a request record. */
export function parseRequestPluginRecords(
  value: unknown,
  path: string,
): RequestPluginRecord[] {
  return plugin.array(value, path).map((item, index) => {
    const recordPath = `${path}[${index}]`;
    const record = plugin.object(item, recordPath);
    let injection: PluginInjection | undefined;
    if (record.injection !== undefined) {
      const injectionPath = `${recordPath}.injection`;
      const raw = plugin.object(record.injection, injectionPath);
      injection = {
        placement: plugin.oneOf(
          raw.placement,
          ["system_append", "turn_message_prefix"] as const,
          `${injectionPath}.placement`,
        ),
        injected_bytes: plugin.integer(
          raw.injected_bytes,
          `${injectionPath}.injected_bytes`,
        ),
        replayed_blocks: plugin.integer(
          raw.replayed_blocks,
          `${injectionPath}.replayed_blocks`,
        ),
        dropped_items: plugin.integer(
          raw.dropped_items,
          `${injectionPath}.dropped_items`,
        ),
      };
    }
    return defined({
      plugin: plugin.string(record.plugin, `${recordPath}.plugin`),
      plugin_version: plugin.string(
        record.plugin_version,
        `${recordPath}.plugin_version`,
      ),
      hook: plugin.string(record.hook, `${recordPath}.hook`),
      title: plugin.string(record.title, `${recordPath}.title`),
      status: plugin.oneOf(
        record.status,
        ["ok", "skipped", "failed"] as const,
        `${recordPath}.status`,
      ),
      summary: plugin.string(record.summary, `${recordPath}.summary`),
      view: optionalView(record.view, `${recordPath}.view`),
      truncated: plugin.optionalBoolean(
        record.truncated,
        `${recordPath}.truncated`,
      ),
      skipped_reason: plugin.optionalString(
        record.skipped_reason,
        `${recordPath}.skipped_reason`,
      ),
      elapsed_ms: plugin.integer(record.elapsed_ms, `${recordPath}.elapsed_ms`),
      injection,
      network: parseNetworkCount(record.network, `${recordPath}.network`),
      observe: plugin.optionalString(record.observe, `${recordPath}.observe`),
    });
  });
}

function parseAttributeAt(value: unknown, path: string): MetadataAttribute {
  const attribute = metadata.object(value, path);
  return defined({
    key: metadata.string(attribute.key, `${path}.key`),
    label: metadata.string(attribute.label, `${path}.label`),
    description: metadata.string(attribute.description, `${path}.description`),
    pattern: metadata.string(attribute.pattern, `${path}.pattern`),
    marker: metadata.optionalString(attribute.marker, `${path}.marker`),
    owner: metadata.string(attribute.owner, `${path}.owner`),
    created_at: metadata.string(attribute.created_at, `${path}.created_at`),
    updated_at: metadata.string(attribute.updated_at, `${path}.updated_at`),
    header: metadata.string(attribute.header, `${path}.header`),
    owner_missing: metadata.optionalBoolean(
      attribute.owner_missing,
      `${path}.owner_missing`,
    ),
  });
}

export function parseMetadataAttributes(value: unknown): MetadataAttribute[] {
  const list = metadata.object(value, "$");
  return metadata
    .array(list.attributes, "$.attributes")
    .map((item, index) => parseAttributeAt(item, `$.attributes[${index}]`));
}

export function parseMetadataAttribute(value: unknown): MetadataAttribute {
  return parseAttributeAt(value, "$");
}

export function parseAccessTokenMetadata(
  value: unknown,
): Record<string, string> {
  const defaults = metadata.object(value, "$");
  const values = metadata.object(defaults.values, "$.values");
  return Object.fromEntries(
    Object.entries(values).map(([key, entry]) => [
      key,
      metadata.string(entry, `$.values.${key}`),
    ]),
  );
}

const sources = ["header", "session", "token"] as const;
const metadataEvents = ["bound", "rebound", "cleared", "ignored"] as const;

/** Reads the metadata of a request record. */
export function parseRequestMetadata(
  value: unknown,
  path: string,
): RequestMetadata {
  const raw = metadata.object(value, path);
  const values =
    raw.values === undefined
      ? {}
      : metadata.object(raw.values, `${path}.values`);
  return {
    values: Object.fromEntries(
      Object.entries(values).map(([key, entry]) => {
        const valuePath = `${path}.values.${key}`;
        const item = metadata.object(entry, valuePath);
        return [
          key,
          defined({
            value: metadata.string(item.value, `${valuePath}.value`),
            source: metadata.oneOf(item.source, sources, `${valuePath}.source`),
            set_by: metadata.optionalString(item.set_by, `${valuePath}.set_by`),
            turn: metadata.optionalInteger(item.turn, `${valuePath}.turn`),
          }),
        ];
      }),
    ),
    events:
      raw.events === undefined
        ? []
        : metadata.array(raw.events, `${path}.events`).map((entry, index) => {
            const eventPath = `${path}.events[${index}]`;
            const event = metadata.object(entry, eventPath);
            return defined({
              key: metadata.string(event.key, `${eventPath}.key`),
              event: metadata.oneOf(
                event.event,
                metadataEvents,
                `${eventPath}.event`,
              ),
              value: metadata.optionalString(event.value, `${eventPath}.value`),
              set_by: metadata.optionalString(
                event.set_by,
                `${eventPath}.set_by`,
              ),
              reason: metadata.optionalString(
                event.reason,
                `${eventPath}.reason`,
              ),
            });
          }),
  };
}

/** The UTF-8 size of a JSON configuration, as the 64 KiB limit counts it. */
export function configBytes(text: string): number {
  return new TextEncoder().encode(text).length;
}

export const MAX_PLUGIN_CONFIG_BYTES = 64 * 1024;
