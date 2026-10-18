import { describe, expect, it } from "vitest";

import {
  parseAccessTokenMetadata,
  parseMetadataAttributes,
  parsePluginDetail,
  parsePluginList,
  parsePluginStatus,
  parsePluginTestResult,
  parsePluginView,
} from "./plugin-model";
import {
  emptyTrajectoryFields,
  parseRequestRecord,
  type RequestRecord,
} from "./request-record-model";
import {
  inspectorChainRows,
  inspectorPart,
  trajectoryChipLabel,
} from "./request-trajectory-model";

const summary = {
  key: "powercontext",
  name: "PowerContext 记忆",
  version: "1.0.0",
  source: "builtin",
  file: "plugin.ts",
  enabled: true,
  state: "loaded",
  requested_capabilities: ["context.inject", "network"],
  granted_capabilities: ["network"],
  needs_network: true,
};

describe("plugin model", () => {
  it("reads the plugin list, failed plugins included", () => {
    const list = parsePluginList({
      directory: "/data/plugins",
      plugins: [
        summary,
        {
          ...summary,
          key: "team-rules",
          name: "team-rules",
          version: "",
          source: "directory",
          enabled: false,
          state: "failed",
          error: {
            code: "async",
            file: "plugin.ts",
            line: 12,
            column: 8,
            message: "async functions are not supported",
          },
          requested_capabilities: [],
          granted_capabilities: [],
          needs_network: false,
        },
      ],
    });
    expect(list.directory).toBe("/data/plugins");
    expect(list.plugins[1]?.error).toEqual({
      code: "async",
      file: "plugin.ts",
      line: 12,
      column: 8,
      message: "async functions are not supported",
    });
    expect(() =>
      parsePluginList({
        directory: "/d",
        plugins: [{ ...summary, source: "remote" }],
      }),
    ).toThrow("$.plugins[0].source");
  });

  it("reads a plugin's detail", () => {
    const detail = parsePluginDetail({
      ...summary,
      network_purpose: "访问 PowerContext 服务器",
      notice: "启用后会询问项目。",
      hooks: ["prepareTurn", "testConnection"],
      config: { serverUrl: "http://192.0.2.1:9090" },
      network_origins: [
        {
          origin: "http://192.0.2.1:9090",
          allow_plaintext: true,
          plaintext: true,
        },
      ],
      privacy_trusted: false,
      credentials: [
        {
          name: "token",
          label: "令牌",
          configured: true,
          hint: "3f2a",
          origin: "http://192.0.2.1:9090",
        },
      ],
      metadata: [
        {
          key: "project",
          label: "项目",
          pattern: "^[a-z]+$",
          marker: "memory-project",
        },
      ],
    });
    expect(detail.config).toEqual({ serverUrl: "http://192.0.2.1:9090" });
    expect(detail.credentials[0]).toEqual({
      name: "token",
      label: "令牌",
      configured: true,
      hint: "3f2a",
      origin: "http://192.0.2.1:9090",
    });
    expect(() =>
      parsePluginDetail({
        ...summary,
        hooks: [],
        config: [],
        network_origins: [],
        privacy_trusted: false,
        credentials: [],
        metadata: [],
      }),
    ).toThrow("$.config");
  });

  it("reads the views Core cleaned and refuses anything else", () => {
    const view = parsePluginView(
      [
        {
          type: "kv",
          items: [
            { label: "项目", value: "astrlink" },
            { label: "Scope", value: "scp_1", mono: true },
          ],
        },
        {
          type: "table",
          columns: ["家族", "请求"],
          rows: [["atomic-memory", 4]],
        },
        { type: "badges", items: [{ text: "1.3.0", tone: "ok" }] },
        { type: "text", text: "两行\n文字" },
      ],
      "$.view",
    );
    expect(view).toHaveLength(4);
    expect(view[1]).toEqual({
      type: "table",
      columns: ["家族", "请求"],
      rows: [["atomic-memory", 4]],
    });
    expect(() =>
      parsePluginView([{ type: "html", html: "<b>x</b>" }], "$.view"),
    ).toThrow("$.view[0].type");
    expect(() =>
      parsePluginView(
        [{ type: "table", columns: ["a"], rows: [[{}]] }],
        "$.view",
      ),
    ).toThrow("$.view[0].rows[0][0]");
  });

  it("reads test results and status", () => {
    expect(
      parsePluginTestResult({
        status: "failed",
        summary: "the plugin ran JavaScript for longer than 200ms",
        reason: "timeout",
        elapsed_ms: 201,
        network: { requests: 0, denied: 0, errors: 0, wait_ms: 0 },
      }).reason,
    ).toBe("timeout");
    const status = parsePluginStatus({
      calls: 3,
      failures: { timeout: 1 },
      memory_limits: 0,
      timeouts: 1,
      network: { requests: 2, denied: 1, errors: 0, wait_ms: 40 },
      view: [{ type: "text", text: "ok" }],
    });
    expect(status.failures).toEqual({ timeout: 1 });
    expect(() =>
      parsePluginStatus({ ...status, failures: { timeout: -1 } }),
    ).toThrow("$.failures.timeout");
  });

  it("reads metadata attributes and token defaults", () => {
    const [attribute] = parseMetadataAttributes({
      attributes: [
        {
          key: "project",
          label: "项目",
          description: "",
          pattern: "^[a-z]+$",
          marker: "memory-project",
          owner: "powercontext",
          created_at: "2026-10-18T00:00:00Z",
          updated_at: "2026-10-18T00:00:00Z",
          header: "X-Astrlink-Meta-Project",
          owner_missing: true,
        },
      ],
    });
    expect(attribute?.owner_missing).toBe(true);
    expect(parseAccessTokenMetadata({ values: { env: "prod" } })).toEqual({
      env: "prod",
    });
    expect(() => parseAccessTokenMetadata({ values: { env: 1 } })).toThrow(
      "$.values.env",
    );
  });
});

const record: RequestRecord = {
  id: "req_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  parent_request_id: null,
  attempt_index: 1,
  child_count: 0,
  started_at: "2026-10-18T10:00:00Z",
  completed_at: "2026-10-18T10:00:02Z",
  status: "succeeded",
  input_protocol: "anthropic.messages",
  requested_model: "claude-opus-4-5",
  streaming: true,
  route_id: null,
  service_id: "service_01",
  local_access_token_id: null,
  http_status: 200,
  latency_ms: 2000,
  usage: null,
  error: null,
  audit: {
    request_body_captured: false,
    response_content_captured: false,
    request_body_truncated: false,
    response_content_truncated: false,
    upstream_request_body_captured: false,
    upstream_response_content_captured: false,
    upstream_request_body_truncated: false,
    upstream_response_content_truncated: false,
  },
  privacy_restore: null,
  ...emptyTrajectoryFields,
};

describe("plugins in request records", () => {
  it("reads plugin events, metadata and plugin records", () => {
    const parsed = parseRequestRecord({
      ...record,
      turn_index: null,
      session_link: null,
      cursors: [],
      events: [
        {
          kind: "accepted",
          started_at: "2026-10-18T10:00:00Z",
          ended_at: null,
          status: "succeeded",
          summary: "claude-opus-4-5",
          attempt_index: 1,
        },
        {
          kind: "plugin",
          plugin: "powercontext",
          title: "记忆",
          started_at: "2026-10-18T10:00:00.1Z",
          ended_at: "2026-10-18T10:00:00.5Z",
          status: "succeeded",
          summary: "召回 3/9",
          attempt_index: 1,
        },
      ],
      metadata: {
        values: {
          project: {
            value: "astrlink",
            source: "session",
            set_by: "model_marker",
            turn: 2,
          },
        },
        events: [
          {
            key: "project",
            event: "ignored",
            value: "Bad",
            set_by: "header",
            reason: "invalid_value",
          },
        ],
      },
      plugin_records: [
        {
          plugin: "powercontext",
          plugin_version: "1.0.0",
          hook: "prepareTurn",
          title: "记忆",
          status: "ok",
          summary: "召回 3/9",
          elapsed_ms: 412,
          injection: {
            placement: "turn_message_prefix",
            injected_bytes: 1843,
            replayed_blocks: 2,
            dropped_items: 0,
          },
          network: { requests: 3, denied: 0, errors: 0, wait_ms: 402 },
          observe: "queued",
          view: [{ type: "kv", items: [{ label: "项目", value: "astrlink" }] }],
        },
      ],
    });
    expect(parsed.events[1]).toMatchObject({
      kind: "plugin",
      plugin: "powercontext",
      title: "记忆",
    });
    expect(parsed.metadata?.values.project).toEqual({
      value: "astrlink",
      source: "session",
      set_by: "model_marker",
      turn: 2,
    });
    expect(parsed.plugin_records?.[0]?.injection?.replayed_blocks).toBe(2);
    expect(() =>
      parseRequestRecord({
        ...record,
        events: [],
        metadata: { values: { project: { value: "x", source: "cookie" } } },
      }),
    ).toThrow("metadata.values.project.source");
  });

  it("puts plugin events in the gateway lane under the plugin's own name", () => {
    const rows = inspectorChainRows({
      ...record,
      events: [
        {
          kind: "accepted",
          started_at: "2026-10-18T10:00:00Z",
          ended_at: "2026-10-18T10:00:00Z",
          status: "succeeded",
          summary: "claude-opus-4-5",
          attempt_index: 1,
        },
        {
          kind: "plugin",
          plugin: "powercontext",
          title: "记忆",
          started_at: "2026-10-18T10:00:00.1Z",
          ended_at: "2026-10-18T10:00:00.5Z",
          status: "succeeded",
          summary: "召回 3/9",
          attempt_index: 1,
        },
      ],
    });
    const plugin = rows.find((row) => row.chip === "PLUGIN");
    expect(plugin).toMatchObject({
      lane: "gateway",
      plugin: "powercontext",
      title: "记忆",
      summary: "召回 3/9",
    });
    expect(trajectoryChipLabel("PLUGIN", plugin?.title)).toBe("记忆");
    expect(trajectoryChipLabel("PLUGIN")).toBe("插件");
    expect(trajectoryChipLabel("ROUTE", "记忆")).toBe("路由");
    expect(inspectorPart("PLUGIN")).toBe("plugin");
  });
});
