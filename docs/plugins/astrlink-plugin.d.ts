// AstrLink plugin API, version 1.
//
// A plugin is one module, <data-dir>/plugins/<key>/plugin.ts or plugin.js,
// that exports `meta` and any of the hooks below. Plugins are synchronous:
// the host refuses async functions, await and imports before it compiles a
// plugin. A plugin of several files is bundled into one first.
//
// For editor support and type checks, put this file in one TypeScript
// project with the plugin, as docs/plugins/team-rules/tsconfig.json does.
// The host strips types without checking them.

declare namespace AstrLinkPlugin {
  /** What a plugin asks for; the user grants each one when enabling it. */
  type Capability =
    | "context.inject" // prepareSession and prepareTurn may add blocks to the request
    | "request.read" // the hooks see the turn's text and metadata; astrlink.request.info
    | "network" // astrlink.http.fetch, to the origins the user allows
    | "storage" // astrlink.store
    | "ui.views"; // views in request records and on the plugin page

  /** `export const meta`: the manifest the host checks when it loads a plugin. */
  interface Meta {
    apiVersion: 1;
    /** A lowercase letter, then up to 31 lowercase letters, digits and hyphens: the directory's name. */
    key: string;
    /** 1 to 64 characters. */
    name: string;
    /** 1 to 32 characters. */
    version: string;
    capabilities: Capability[];
    /** Required with the network capability; shown when the user enables the plugin. */
    network?: { purpose: string };
    /** Secrets the plugin page asks for. They need the network capability. */
    credentials?: CredentialSlot[];
    /** Request attributes the plugin registers when it is enabled. */
    metadata?: MetadataAttribute[];
    /** Shown as written in the dialog that enables the plugin; at most 1000 characters. */
    notice?: string;
  }

  /**
   * A secret the user enters on the plugin page and binds to one allowed
   * origin. A request naming it gets `header: scheme value` when it goes to
   * that origin; the plugin never reads the value.
   */
  interface CredentialSlot {
    /** A lowercase letter, then up to 31 lowercase letters, digits and underscores. */
    name: string;
    label: string;
    /** Such as Authorization; never Host, Proxy-* or X-AstrLink-*. */
    header: string;
    /** Such as Bearer. */
    scheme?: string;
  }

  /** A request attribute: values come from headers, session markers and token defaults. */
  interface MetadataAttribute {
    /** A lowercase letter, then up to 31 lowercase letters, digits and underscores. */
    key: string;
    label: string;
    /** Validates every value; anchored with ^ and $. `none` is always accepted and leaves a request out. */
    pattern: string;
    /** The line `<marker>value</marker>` a reply carries to bind the session; it must not name the gateway. */
    marker?: string;
  }

  type Protocol =
    | "openai.chat"
    | "openai.responses"
    | "anthropic.messages"
    | "google.generate_content";

  interface MetaValue {
    value: string;
    /** Request header, session binding, or access token default. */
    source: "header" | "session" | "token";
  }

  interface RequestInfo {
    protocol: Protocol;
    /** The model the client asked for, before any redirect. */
    model: string;
    /** claude_code, codex and so on, or unknown. */
    client: string;
    stream: boolean;
    meta: Record<string, MetaValue>;
    session?: { id: string; turn: number };
  }

  /** What prepareSession sees. */
  interface SessionView {
    protocol: Protocol;
    model: string;
    client: string;
    meta: Record<string, MetaValue>;
    session: { id: string; turn: number };
  }

  /** What prepareTurn and observeTurn see: the turn as the client sent it, before anything was injected. */
  interface TurnView extends SessionView {
    stream: boolean;
    user: {
      /** The turn's opening user message, at most 200,000 characters. */
      text: string;
      /** The same without client wrappers such as <system-reminder>. */
      typed: string;
      /** The previous turn's typed text, at most 4 KiB. */
      previous?: string;
    };
  }

  /** A block to inject. Items are added one by one within the budget; the ones past it are dropped. */
  type Block = string | { open?: string; items: string[]; close?: string };

  type ViewBlock =
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

  /** How a hook describes its part in a request: the event in the gateway lane and its details. */
  interface PluginRecord {
    /** The event's name, such as 「记忆」; at most 32 characters. */
    title: string;
    status: "ok" | "skipped" | "failed";
    /** At most 256 characters. */
    summary: string;
    /** At most 16 KiB as JSON. */
    view?: ViewBlock[];
  }

  interface PrepareSessionResult {
    /** Appended to the system prompt. Keep it stable within a session: a change rewrites the prompt cache. */
    system?: Block;
    record?: PluginRecord;
  }

  interface PrepareTurnResult {
    /** Put at the start of the turn's opening user message, and replayed there on later requests. */
    context?: Block;
    /** Why the turn gets nothing, such as no_project or short_query. */
    skip?: string;
    record?: PluginRecord;
  }

  interface ObserveTurnResult {
    record?: PluginRecord;
  }

  interface TestConnectionResult {
    status: "ok" | "error";
    summary: string;
    view?: ViewBlock[];
  }

  interface HttpRequest {
    method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
    /** An absolute http or https URL to an origin the user allows. */
    url: string;
    /** Never Authorization, Cookie, Host, Content-Length, Proxy-* or X-AstrLink-*. */
    headers?: Record<string, string>;
    /** Sent as application/json. A request has json or body, not both; a GET has neither. At most 1 MiB. */
    json?: unknown;
    body?: string;
    /** A slot of meta.credentials. */
    credential?: string;
    /** At most 30000; the hook's network budget can end a request sooner. Default 10000. */
    timeoutMs?: number;
  }

  interface HttpResponse {
    /** 0 when no response arrived; error then says why. HTTP errors come back as they are. */
    status: number;
    /** The first value of each header, keyed in lowercase. */
    headers: Record<string, string>;
    /** The body when it parses as JSON; text otherwise. At most 1 MiB. */
    json?: unknown;
    text?: string;
    error?: "denied" | "timeout" | "network" | "too_large" | "budget";
    took_ms: number;
  }
}

/**
 * The host functions. They work only while the host runs a hook, not in a
 * module's top level, and each throws a TypeError when its capability was
 * not granted.
 */
declare const astrlink: {
  request: {
    /** Needs request.read. null outside a request, as on the plugin page. */
    info(): AstrLinkPlugin.RequestInfo | null;
  };
  http: {
    /**
     * Needs network. An array runs in parallel, at most 8 at once and 16 in
     * one hook call. The wait does not count against the 200 ms script time.
     */
    fetch(request: AstrLinkPlugin.HttpRequest): AstrLinkPlugin.HttpResponse;
    fetch(
      requests: AstrLinkPlugin.HttpRequest[],
    ): AstrLinkPlugin.HttpResponse[];
  };
  /** Needs storage. JSON values of at most 64 KiB each, 1 MiB in all; keys of 1 to 256 characters. */
  store: {
    /** null when the key has no value. */
    get(key: string): unknown;
    set(key: string, value: unknown): void;
    delete(key: string): void;
    /** Sorted. */
    keys(prefix?: string): string[];
  };
  config: {
    /** The plugin's configuration, a fresh copy on each call; plugins cannot change it. */
    get(): Record<string, unknown>;
  };
  util: {
    /** Lowercase hex of the SHA-256 of the UTF-8 text. */
    sha256(text: string): string;
  };
  /** At most 20 lines a call, 1 KiB each. */
  log(level: "debug" | "info" | "warn", message: string): void;
  clock: {
    /** Milliseconds since the Unix epoch. */
    now(): number;
  };
};
