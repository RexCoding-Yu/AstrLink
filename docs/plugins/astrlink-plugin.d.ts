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
    /** The fields of the settings form, in order; at most 64. */
    settings?: Setting[];
    /** Open-source projects the plugin page credits; at most 8. */
    credits?: Credit[];
    /** What the plugin keeps in astrlink.store, shown on the plugin page; at most 8. Needs storage. */
    storage?: StorageItem[];
  }

  /** One kind of thing a plugin keeps. */
  interface StorageItem {
    /** 1 to 100 characters. */
    label: string;
    /** Whether it holds conversation text. */
    content?: boolean;
    /** How long it stays, such as 「停用插件时可以清除」; 1 to 64 characters. */
    retention: string;
  }

  /**
   * A field of the settings form. `key` is where the value sits in the
   * configuration, a dot stepping into an object, such as `quotas.skill`.
   * The keys starting with $ place the host's own settings and take only
   * `group` and `advanced`: `$wait` how long a request waits for the plugin,
   * `$trusted` whether its text skips the privacy check, `$origins` the
   * addresses it may reach (needs network), `$clients` the clients it is off
   * for, `$workspace` whether a new session takes the value its working
   * directory had (needs metadata), `$refresh` whether the host checks a
   * session's resident block for newer data (needs context.inject).
   */
  type Setting =
    | {
        key:
          | "$wait"
          | "$trusted"
          | "$origins"
          | "$clients"
          | "$workspace"
          | "$refresh";
        group?: string;
        advanced?: boolean;
      }
    | {
        key: string;
        /** 1 to 32 characters. */
        label: string;
        /**
         * server is an address the plugin reaches: the host lets the plugin
         * reach its origin and sends `credential` there. It has no default.
         */
        type: "server" | "text" | "number" | "switch" | "choice";
        /** The heading the field sits under; at most 16 characters. */
        group?: string;
        /** Fields of one row sit on one line under its name; they follow one another. */
        row?: string;
        /** Shown behind the field's help button; at most 500 characters. */
        help?: string;
        /** What astrlink.config.get() returns while the configuration has no value. */
        default?: string | number | boolean;
        required?: boolean;
        /** Under「更多设置」. */
        advanced?: boolean;
        min?: number;
        max?: number;
        step?: number;
        unit?: string;
        placeholder?: string;
        options?: Array<{ value: string; label: string }>;
        credential?: string;
      };

  /** An open-source project the plugin builds on or talks to. */
  interface Credit {
    name: string;
    owner?: string;
    /** An SPDX identifier, such as Apache-2.0. */
    license?: string;
    /** An https address. */
    url: string;
    /** At most 200 characters. */
    note?: string;
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

  /** The session and turn of a request. */
  interface SessionRef {
    id: string;
    turn: number;
    /** Set in the first turn the plugin takes part in: the session's first, or the first since the plugin was enabled. */
    first?: boolean;
  }

  interface RequestInfo {
    protocol: Protocol;
    /** The model the client asked for, before any redirect. */
    model: string;
    /** claude_code, codex and so on, or unknown. */
    client: string;
    stream: boolean;
    meta: Record<string, MetaValue>;
    session?: SessionRef;
  }

  /** The interface language, which everything a plugin injects is written in. */
  type Language = "en" | "zh-CN" | "ja";

  /**
   * Why prepareSession runs: the session's first request since the gateway
   * started, a new binding, new settings or version, a compacted history, the
   * user's refresh, or the automatic check for newer data.
   */
  type SessionReason =
    | "start"
    | "bound"
    | "config"
    | "compact"
    | "manual"
    | "auto";

  /** What prepareSession sees. */
  interface SessionView {
    protocol: Protocol;
    model: string;
    client: string;
    meta: Record<string, MetaValue>;
    session: SessionRef;
    /** The interface language the session started with; it does not change within the session. */
    language: Language;
    /** Only in prepareSession. */
    reason?: SessionReason;
    /** The last element of the working directory the client named, never the whole path. Needs request.read. */
    workspace?: { name: string };
    /** Only in prepareSession: the privacy policy replaces what it finds with placeholders, so the plugin's data holds them too. */
    redacted?: boolean;
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
    /** Buttons for the record's session, at most 3. A click calls onAction. */
    actions?: RecordAction[];
  }

  /** A button on a record. */
  interface RecordAction {
    /** A lowercase letter, then up to 31 lowercase letters, digits and underscores. */
    id: string;
    /** At most 16 characters. */
    label: string;
    /** Asked before the click goes to the plugin; at most 300 characters. */
    confirm?: string;
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
    /**
     * Values the block offers for the plugin's attributes, at most 50 each.
     * A later turn whose whole message is one of them, case aside, binds the
     * session to it there and then.
     */
    choices?: Record<string, string[]>;
    record?: PluginRecord;
  }

  /** What onAction sees: the button clicked and the record's session with its bindings. */
  interface ActionView {
    action: string;
    session: SessionRef;
    /** The session's bindings; needs request.read. */
    meta: Record<string, MetaValue>;
    language: Language;
  }

  interface ActionResult {
    /** The session's next request computes the resident block again. */
    refresh?: boolean;
    /** Shown after the click; at most 256 characters. */
    message?: string;
  }

  /** What describeStatus and testConnection see. */
  interface PageView {
    language: Language;
  }

  /** A value sessions are bound to. */
  interface ValueStat {
    value: string;
    sessions: number;
    /** When one of those sessions last made a request, in milliseconds since the Unix epoch; 0 for never. */
    lastUsedAt: number;
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
  meta: {
    /**
     * Needs request.read. The values sessions are bound to for one of the
     * attributes in meta.metadata, the most recently used first; limit is 1
     * to 200, 50 by default.
     */
    values(key: string, limit?: number): AstrLinkPlugin.ValueStat[];
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
    /**
     * How many minutes the gateway's time zone is ahead of UTC at a moment
     * in milliseconds since the Unix epoch, now by default. Date keeps to
     * UTC in a plugin; this writes dates as the user reads them.
     */
    offset(at?: number): number;
  };
};
