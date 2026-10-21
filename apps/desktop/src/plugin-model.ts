import { invalidData, type DataProblem } from "./ipc-data-error";

/** What a plugin asks for; the user grants each one when enabling it. */
export const PLUGIN_CAPABILITIES = [
  "context.inject",
  "request.read",
  "network",
  "storage",
  "ui.views",
  "model.tools",
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
  /** The server setting the origin comes from; the user's own have none. */
  setting?: string;
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
  /** How long a request waits for the plugin's prepare hooks. */
  turn_wait_ms: number;
  credentials: PluginCredentialStatus[];
  metadata: PluginMetadataAttribute[];
  /** The fields of the plugin's settings form, in order. */
  settings: PluginSetting[];
  /** Open-source projects the plugin credits. */
  credits: PluginCredit[];
  /** The client types the plugin is off for. */
  excluded_clients: string[];
  /** A new session takes the value its working directory had. */
  workspace_binding: boolean;
  /** The host swaps a session's resident block when newer data changes it. */
  auto_refresh: boolean;
  /** The model may run the plugin's queries, when model.tools is granted. */
  lookup_enabled: boolean;
  /** The queries the plugin offers the model, in the interface language. */
  tools: PluginTool[];
  /** What the plugin says it keeps in its own store. */
  storage: PluginStorageItem[];
  /** What the host keeps for the plugin, with counts where it can clear them. */
  host_storage: PluginHostStorage[];
}

/** A read-only query a plugin offers the model. */
export interface PluginTool {
  name: string;
  description: string;
  /** It runs before the session has the plugin's values, such as listing projects. */
  unbound?: boolean;
}

/** Something a plugin keeps, as its manifest declares it. */
export interface PluginStorageItem {
  label: string;
  /** Whether it holds conversation text. */
  content: boolean;
  retention: string;
}

/** What the host keeps for a plugin. */
export const PLUGIN_HOST_STORAGE = [
  "session_bindings",
  "workspace_bindings",
  "plugin_sessions",
  "request_records",
  "resident_block",
  "plugin_values",
  "lookup_credentials",
] as const;
export type PluginHostStorageKind = (typeof PLUGIN_HOST_STORAGE)[number];

export interface PluginHostStorage {
  kind: PluginHostStorageKind;
  count?: number;
}

/** What the plugin page clears of what the host keeps for a plugin. */
export type PluginClearTarget = "workspaces" | "values" | "lookups";

/** The host's own settings a plugin may place in its form. */
export const HOST_SETTINGS = [
  "$wait",
  "$trusted",
  "$origins",
  "$clients",
  "$workspace",
  "$refresh",
  "$lookup",
] as const;
export type HostSettingKey = (typeof HOST_SETTINGS)[number];

/** The interface languages, which what plugins inject follows. */
export type PluginLanguage = "en" | "zh-CN" | "ja";

/** The settings of every plugin at once. */
export interface PluginHostSettings {
  /** Every plugin stays out of requests, which go out as they came. */
  paused: boolean;
  /** What all plugins together may inject into one request. */
  injection_budget_bytes: number;
  language: PluginLanguage;
}

export const DEFAULT_INJECTION_BUDGET_BYTES = 16 * 1024;
export const MIN_INJECTION_BUDGET_BYTES = 4 * 1024;
export const MAX_INJECTION_BUDGET_BYTES = 64 * 1024;

export interface PluginHostSettingsUpdate {
  paused?: boolean;
  injection_budget_bytes?: number;
  language?: PluginLanguage;
}

/** What came of a click on a button of a plugin's record. */
export interface PluginActionResult {
  status: "ok" | "failed";
  message?: string;
  refresh: boolean;
  reason?: string;
}

/** A plugin's part in one session. */
export interface SessionPlugin {
  plugin: string;
  name: string;
  paused: boolean;
  first_turn?: number;
  /** The resident block the session carries; it lives in Core's memory only. */
  block?: {
    text: string;
    bytes: number;
    trigger: string;
    language: string;
    computed_at: string;
  };
  refresh_pending?: boolean;
  /** How the session's last request told the model to run the plugin's queries, its credential masked. */
  lookup?: string;
}

/** A value sessions are bound to, with how many and when one was last used. */
export interface MetadataValueUsage {
  value: string;
  sessions: number;
  last_used_at?: string;
}

export type PluginSettingType =
  | "server"
  | "text"
  | "number"
  | "switch"
  | "choice";

/**
 * A field of a plugin's settings form. `key` is where the value sits in the
 * configuration, a dot stepping into an object; a key of HOST_SETTINGS
 * places one of the host's settings.
 */
export interface PluginSetting {
  key: string;
  label?: string;
  type?: PluginSettingType;
  group?: string;
  row?: string;
  help?: string;
  default?: unknown;
  required?: boolean;
  advanced?: boolean;
  min?: number;
  max?: number;
  step?: number;
  unit?: string;
  placeholder?: string;
  options?: Array<{ value: string; label: string }>;
  /** The credential slot a server sends. */
  credential?: string;
}

/** An open-source project a plugin credits. */
export interface PluginCredit {
  name: string;
  owner?: string;
  /** An SPDX identifier such as Apache-2.0. */
  license?: string;
  url: string;
  note?: string;
}

/** The waits the plugin page offers, in milliseconds. */
export const PLUGIN_TURN_WAITS = [
  1000, 1500, 2000, 2500, 3000, 3500, 4000, 4500, 5000,
] as const;
export const DEFAULT_PLUGIN_TURN_WAIT_MS = 2500;

export interface PluginSettingsUpdate {
  enabled?: boolean;
  granted_capabilities?: string[];
  network_origins?: Array<{ origin: string; allow_plaintext: boolean }>;
  privacy_trusted?: boolean;
  turn_wait_ms?: number;
  excluded_clients?: string[];
  workspace_binding?: boolean;
  auto_refresh?: boolean;
  lookup_enabled?: boolean;
  config?: Record<string, unknown>;
  /** Plain http server origins the user agrees to reach in the clear. */
  plaintext_consent?: string[];
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
  /** The last connection test since Core started. */
  last_test?: PluginTestResult & { tested_at: string };
  /** The values sessions are bound to, for each attribute the plugin registers. */
  bindings?: PluginBindings[];
}

/** The values sessions are bound to for one attribute, the most recent first. */
export interface PluginBindings {
  key: string;
  label: string;
  values: Array<{ value: string; sessions: number; last_used_at?: string }>;
}

export interface PluginInjection {
  placement: "system_append" | "turn_message_prefix";
  injected_bytes: number;
  replayed_blocks: number;
  dropped_items: number;
}

/** A query the model ran: its size only, never what it asked or got. */
export interface PluginLookup {
  tool: string;
  input_runes: number;
  result_bytes: number;
  /** How many things the result held, when the plugin counts them. */
  hits?: number;
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
  /** Why a resident block was computed, on the request that first carried it. */
  trigger?: string;
  /** The plugin's buttons for the record's session. */
  actions?: PluginRecordAction[];
  /** A query the model ran through the gateway. */
  lookup?: PluginLookup;
  /** The queries whose commands the plugin's turn block carried. */
  commands?: string[];
}

/** A button on a plugin's record; confirm is asked before the click goes out. */
export interface PluginRecordAction {
  id: string;
  label: string;
  confirm?: string;
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
  /** A value no session had and nothing offered: a new project. */
  new?: boolean;
  /** The marker's own spelling, when a known value took its place. */
  normalized?: string;
}

/** One binding of a session: its value, how it was set and in which turn. */
export interface SessionMetadataValue {
  value: string;
  /** model_marker, user_marker, user_answer, workspace or manual. */
  set_by: string;
  turn?: number;
  updated_at: string;
}

/** What a session is bound to, by attribute key. */
export interface SessionMetadata {
  values: Record<string, SessionMetadataValue>;
  /** The working directory the session's last request named, and what it is bound to. */
  workspace?: { name: string; values: Record<string, string> };
  /** The values plugins offered the session, by attribute key. */
  choices?: Record<string, string[]>;
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
  const optionalNumber = (value: unknown, path: string): number | undefined => {
    if (value === undefined) return undefined;
    if (typeof value !== "number" || !Number.isFinite(value)) {
      return invalid(subject, path, "number");
    }
    return value;
  };
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
    optionalNumber,
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
          ...(origin.setting === undefined
            ? {}
            : { setting: plugin.string(origin.setting, `${path}.setting`) }),
        };
      }),
    privacy_trusted: plugin.boolean(
      detail.privacy_trusted,
      "$.privacy_trusted",
    ),
    turn_wait_ms:
      plugin.optionalInteger(detail.turn_wait_ms, "$.turn_wait_ms") ??
      DEFAULT_PLUGIN_TURN_WAIT_MS,
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
    settings: (detail.settings === undefined
      ? []
      : plugin.array(detail.settings, "$.settings")
    ).map((item, index) => parseSetting(item, `$.settings[${index}]`)),
    credits: (detail.credits === undefined
      ? []
      : plugin.array(detail.credits, "$.credits")
    ).map((item, index) => {
      const path = `$.credits[${index}]`;
      const credit = plugin.object(item, path);
      return defined({
        name: plugin.string(credit.name, `${path}.name`),
        owner: plugin.optionalString(credit.owner, `${path}.owner`),
        license: plugin.optionalString(credit.license, `${path}.license`),
        url: plugin.string(credit.url, `${path}.url`),
        note: plugin.optionalString(credit.note, `${path}.note`),
      });
    }),
    excluded_clients:
      detail.excluded_clients === undefined
        ? []
        : plugin.strings(detail.excluded_clients, "$.excluded_clients"),
    workspace_binding:
      plugin.optionalBoolean(detail.workspace_binding, "$.workspace_binding") ??
      true,
    auto_refresh:
      plugin.optionalBoolean(detail.auto_refresh, "$.auto_refresh") ?? false,
    lookup_enabled:
      plugin.optionalBoolean(detail.lookup_enabled, "$.lookup_enabled") ?? true,
    tools: (detail.tools === undefined
      ? []
      : plugin.array(detail.tools, "$.tools")
    ).map((item, index) => {
      const path = `$.tools[${index}]`;
      const tool = plugin.object(item, path);
      return defined({
        name: plugin.string(tool.name, `${path}.name`),
        description: plugin.string(tool.description, `${path}.description`),
        unbound: plugin.optionalBoolean(tool.unbound, `${path}.unbound`),
      });
    }),
    storage: (detail.storage === undefined
      ? []
      : plugin.array(detail.storage, "$.storage")
    ).map((item, index) => {
      const path = `$.storage[${index}]`;
      const kept = plugin.object(item, path);
      return {
        label: plugin.string(kept.label, `${path}.label`),
        content: plugin.boolean(kept.content, `${path}.content`),
        retention: plugin.string(kept.retention, `${path}.retention`),
      };
    }),
    host_storage: (detail.host_storage === undefined
      ? []
      : plugin.array(detail.host_storage, "$.host_storage")
    ).flatMap((item, index) => {
      const path = `$.host_storage[${index}]`;
      const kept = plugin.object(item, path);
      const kind = plugin.string(kept.kind, `${path}.kind`);
      // A kind a newer Core keeps is not one this page can name.
      if (!(PLUGIN_HOST_STORAGE as readonly string[]).includes(kind)) return [];
      return [
        defined({
          kind: kind as PluginHostStorageKind,
          count: plugin.optionalInteger(kept.count, `${path}.count`),
        }),
      ];
    }),
  });
}

const languages = ["en", "zh-CN", "ja"] as const;

export function parsePluginHostSettings(value: unknown): PluginHostSettings {
  const settings = plugin.object(value, "$");
  return {
    paused: plugin.boolean(settings.paused, "$.paused"),
    injection_budget_bytes: plugin.integer(
      settings.injection_budget_bytes,
      "$.injection_budget_bytes",
    ),
    language: plugin.oneOf(settings.language, languages, "$.language"),
  };
}

export function parsePluginActionResult(value: unknown): PluginActionResult {
  const result = plugin.object(value, "$");
  return defined({
    status: plugin.oneOf(result.status, ["ok", "failed"] as const, "$.status"),
    message: plugin.optionalString(result.message, "$.message"),
    refresh: plugin.boolean(result.refresh, "$.refresh"),
    reason: plugin.optionalString(result.reason, "$.reason"),
  });
}

/** Reads the plugins' part in a session. */
export function parseSessionPlugins(value: unknown): SessionPlugin[] {
  const list = plugin.object(value, "$");
  return plugin.array(list.plugins, "$.plugins").map((item, index) => {
    const path = `$.plugins[${index}]`;
    const part = plugin.object(item, path);
    let block: SessionPlugin["block"];
    if (part.block !== undefined) {
      const blockPath = `${path}.block`;
      const raw = plugin.object(part.block, blockPath);
      block = {
        text: plugin.string(raw.text, `${blockPath}.text`),
        bytes: plugin.integer(raw.bytes, `${blockPath}.bytes`),
        trigger: plugin.string(raw.trigger, `${blockPath}.trigger`),
        language: plugin.string(raw.language, `${blockPath}.language`),
        computed_at: plugin.string(raw.computed_at, `${blockPath}.computed_at`),
      };
    }
    return defined({
      plugin: plugin.string(part.plugin, `${path}.plugin`),
      name: plugin.string(part.name, `${path}.name`),
      paused: plugin.boolean(part.paused, `${path}.paused`),
      first_turn: plugin.optionalInteger(part.first_turn, `${path}.first_turn`),
      block,
      refresh_pending: plugin.optionalBoolean(
        part.refresh_pending,
        `${path}.refresh_pending`,
      ),
      lookup: plugin.optionalString(part.lookup, `${path}.lookup`),
    });
  });
}

/** Reads how many a clearing removed. */
export function parsePluginClearResult(value: unknown): number {
  const result = plugin.object(value, "$");
  return plugin.integer(result.removed, "$.removed");
}

const settingTypes = ["server", "text", "number", "switch", "choice"] as const;

function parseSetting(value: unknown, path: string): PluginSetting {
  const setting = plugin.object(value, path);
  return defined({
    key: plugin.string(setting.key, `${path}.key`),
    label: plugin.optionalString(setting.label, `${path}.label`),
    type:
      setting.type === undefined
        ? undefined
        : plugin.oneOf(setting.type, settingTypes, `${path}.type`),
    group: plugin.optionalString(setting.group, `${path}.group`),
    row: plugin.optionalString(setting.row, `${path}.row`),
    help: plugin.optionalString(setting.help, `${path}.help`),
    default: setting.default,
    required: plugin.optionalBoolean(setting.required, `${path}.required`),
    advanced: plugin.optionalBoolean(setting.advanced, `${path}.advanced`),
    min: plugin.optionalNumber(setting.min, `${path}.min`),
    max: plugin.optionalNumber(setting.max, `${path}.max`),
    step: plugin.optionalNumber(setting.step, `${path}.step`),
    unit: plugin.optionalString(setting.unit, `${path}.unit`),
    placeholder: plugin.optionalString(
      setting.placeholder,
      `${path}.placeholder`,
    ),
    options:
      setting.options === undefined
        ? undefined
        : plugin
            .array(setting.options, `${path}.options`)
            .map((item, index) => {
              const optionPath = `${path}.options[${index}]`;
              const option = plugin.object(item, optionPath);
              return {
                value: plugin.string(option.value, `${optionPath}.value`),
                label: plugin.string(option.label, `${optionPath}.label`),
              };
            }),
    credential: plugin.optionalString(setting.credential, `${path}.credential`),
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
    last_test:
      status.last_test === undefined
        ? undefined
        : {
            ...parsePluginTestResult(status.last_test),
            tested_at: plugin.string(
              plugin.object(status.last_test, "$.last_test").tested_at,
              "$.last_test.tested_at",
            ),
          },
    bindings:
      status.bindings === undefined
        ? undefined
        : plugin.array(status.bindings, "$.bindings").map((item, index) => {
            const path = `$.bindings[${index}]`;
            const bindings = plugin.object(item, path);
            return {
              key: plugin.string(bindings.key, `${path}.key`),
              label: plugin.string(bindings.label, `${path}.label`),
              values: plugin
                .array(bindings.values, `${path}.values`)
                .map((entry, valueIndex) => {
                  const valuePath = `${path}.values[${valueIndex}]`;
                  const bound = plugin.object(entry, valuePath);
                  return defined({
                    value: plugin.string(bound.value, `${valuePath}.value`),
                    sessions: plugin.integer(
                      bound.sessions,
                      `${valuePath}.sessions`,
                    ),
                    last_used_at: plugin.optionalString(
                      bound.last_used_at,
                      `${valuePath}.last_used_at`,
                    ),
                  });
                }),
            };
          }),
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
    let lookup: PluginLookup | undefined;
    if (record.lookup !== undefined) {
      const lookupPath = `${recordPath}.lookup`;
      const raw = plugin.object(record.lookup, lookupPath);
      lookup = defined({
        tool: plugin.string(raw.tool, `${lookupPath}.tool`),
        input_runes: plugin.integer(
          raw.input_runes,
          `${lookupPath}.input_runes`,
        ),
        result_bytes: plugin.integer(
          raw.result_bytes,
          `${lookupPath}.result_bytes`,
        ),
        hits: plugin.optionalInteger(raw.hits, `${lookupPath}.hits`),
      });
    }
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
      trigger: plugin.optionalString(record.trigger, `${recordPath}.trigger`),
      actions:
        record.actions === undefined
          ? undefined
          : plugin
              .array(record.actions, `${recordPath}.actions`)
              .map((entry, at) => {
                const actionPath = `${recordPath}.actions[${at}]`;
                const action = plugin.object(entry, actionPath);
                return defined({
                  id: plugin.string(action.id, `${actionPath}.id`),
                  label: plugin.string(action.label, `${actionPath}.label`),
                  confirm: plugin.optionalString(
                    action.confirm,
                    `${actionPath}.confirm`,
                  ),
                });
              }),
      lookup,
      commands:
        record.commands === undefined
          ? undefined
          : plugin.strings(record.commands, `${recordPath}.commands`),
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

/** Reads a session's bindings. */
export function parseSessionMetadata(value: unknown): SessionMetadata {
  const raw = metadata.object(value, "$");
  const values = metadata.object(raw.values, "$.values");
  let workspace: SessionMetadata["workspace"];
  if (raw.workspace !== undefined) {
    const place = metadata.object(raw.workspace, "$.workspace");
    const bound = metadata.object(place.values, "$.workspace.values");
    workspace = {
      name: metadata.string(place.name, "$.workspace.name"),
      values: Object.fromEntries(
        Object.entries(bound).map(([key, entry]) => [
          key,
          metadata.string(entry, `$.workspace.values.${key}`),
        ]),
      ),
    };
  }
  let choices: SessionMetadata["choices"];
  if (raw.choices !== undefined) {
    const offered = metadata.object(raw.choices, "$.choices");
    choices = Object.fromEntries(
      Object.entries(offered).map(([key, entry]) => [
        key,
        metadata.strings(entry, `$.choices.${key}`),
      ]),
    );
  }
  return defined({
    workspace,
    choices,
    values: Object.fromEntries(
      Object.entries(values).map(([key, entry]) => {
        const path = `$.values.${key}`;
        const item = metadata.object(entry, path);
        return [
          key,
          defined({
            value: metadata.string(item.value, `${path}.value`),
            set_by: metadata.string(item.set_by, `${path}.set_by`),
            turn: metadata.optionalInteger(item.turn, `${path}.turn`),
            updated_at: metadata.string(item.updated_at, `${path}.updated_at`),
          }),
        ];
      }),
    ),
  });
}

/** Reads the values of an attribute sessions are bound to. */
export function parseMetadataValues(value: unknown): MetadataValueUsage[] {
  const list = metadata.object(value, "$");
  return metadata.array(list.values, "$.values").map((item, index) => {
    const path = `$.values[${index}]`;
    const usage = metadata.object(item, path);
    return defined({
      value: metadata.string(usage.value, `${path}.value`),
      sessions: metadata.integer(usage.sessions, `${path}.sessions`),
      last_used_at: metadata.optionalString(
        usage.last_used_at,
        `${path}.last_used_at`,
      ),
    });
  });
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
              new: metadata.optionalBoolean(event.new, `${eventPath}.new`),
              normalized: metadata.optionalString(
                event.normalized,
                `${eventPath}.normalized`,
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

/** The value at a setting's key in a configuration; a dot steps into an object. */
export function settingValue(
  config: Record<string, unknown>,
  key: string,
): unknown {
  let current: unknown = config;
  for (const part of key.split(".")) {
    if (
      typeof current !== "object" ||
      current === null ||
      Array.isArray(current)
    )
      return undefined;
    current = (current as Record<string, unknown>)[part];
  }
  return current;
}

/**
 * A copy of config with value at key, or without the key for undefined; an
 * object emptied on the way goes too.
 */
export function withSettingValue(
  config: Record<string, unknown>,
  key: string,
  value: unknown,
): Record<string, unknown> {
  const copy = JSON.parse(JSON.stringify(config)) as Record<string, unknown>;
  const parts = key.split(".");
  const last = parts.pop() as string;
  const trail: Array<[Record<string, unknown>, string]> = [];
  let current = copy;
  for (const part of parts) {
    const next = current[part];
    if (typeof next !== "object" || next === null || Array.isArray(next)) {
      if (value === undefined) return copy;
      current[part] = {};
    }
    trail.push([current, part]);
    current = current[part] as Record<string, unknown>;
  }
  if (value !== undefined) {
    current[last] = value;
    return copy;
  }
  delete current[last];
  for (const [parent, part] of trail.reverse()) {
    if (Object.keys(parent[part] as object).length > 0) break;
    delete parent[part];
  }
  return copy;
}

/** An http or https address with a host and no user info. */
export function validOrigin(raw: string): boolean {
  try {
    const url = new URL(raw);
    return (
      (url.protocol === "http:" || url.protocol === "https:") &&
      url.hostname !== "" &&
      url.username === "" &&
      url.password === ""
    );
  } catch {
    return false;
  }
}

/** Plain http to a host other than this machine, as Core tells it. */
export function plaintextOrigin(raw: string): boolean {
  try {
    const url = new URL(raw);
    if (url.protocol !== "http:") return false;
    const host = url.hostname.replace(/^\[|\]$/g, "");
    return !(host === "localhost" || host === "::1" || host.startsWith("127."));
  } catch {
    return false;
  }
}

/** A JSON text whose objects list their keys in order, to compare values. */
export function stableJSON(value: unknown): string {
  return JSON.stringify(value, (_, item: unknown) =>
    typeof item === "object" && item !== null && !Array.isArray(item)
      ? Object.fromEntries(
          Object.entries(item as Record<string, unknown>).sort(([a], [b]) =>
            a < b ? -1 : a > b ? 1 : 0,
          ),
        )
      : item,
  );
}
