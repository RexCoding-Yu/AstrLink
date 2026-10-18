// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import {
  PluginRecordDetails,
  RequestMetadataLine,
} from "./PluginRecordDetails";
import {
  emptyTrajectoryFields,
  type RequestRecord,
} from "./request-record-model";

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

describe("PluginRecordDetails", () => {
  let container: HTMLDivElement;
  let reactRoot: Root;

  beforeEach(() => {
    (
      globalThis as typeof globalThis & {
        IS_REACT_ACT_ENVIRONMENT?: boolean;
      }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    container = document.createElement("div");
    document.body.append(container);
    reactRoot = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => reactRoot.unmount());
    container.remove();
  });

  it("shows what each plugin did and what the host did for it", async () => {
    await act(async () => {
      reactRoot.render(
        <PluginRecordDetails
          record={{
            ...record,
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
                view: [
                  { type: "kv", items: [{ label: "项目", value: "astrlink" }] },
                ],
                truncated: true,
              },
              {
                plugin: "team-rules",
                plugin_version: "",
                hook: "prepareSession",
                title: "团队规则",
                status: "skipped",
                summary: "",
                skipped_reason: "short_query",
                elapsed_ms: 1,
                network: { requests: 0, denied: 0, errors: 0, wait_ms: 0 },
              },
            ],
          }}
        />,
      );
    });
    const [memory, rules] = document.querySelectorAll(
      '[data-testid="plugin-record"]',
    );
    expect(memory?.textContent).toContain(
      "本轮用户开口消息开头 · 1.8 KiB · 重放 2 段",
    );
    expect(memory?.textContent).toContain(
      "3 个请求 · 拒绝 0 · 失败 0 · 等待 402 ms",
    );
    expect(memory?.textContent).toContain("已排队");
    expect(memory?.textContent).toContain("astrlink");
    expect(memory?.textContent).toContain("超出上限，已截断");
    expect(rules?.textContent).toContain("已跳过");
    expect(rules?.textContent).toContain("问题太短");
    // A plugin that sent nothing shows no network line.
    expect(rules?.textContent).not.toContain("个请求");
  });

  it("says so when no plugin ran", async () => {
    await act(async () => {
      reactRoot.render(<PluginRecordDetails record={record} />);
    });
    expect(container.textContent).toBe("这个请求没有插件记录。");
  });

  it("puts a request's metadata on one line with where each value came from", async () => {
    await act(async () => {
      reactRoot.render(
        <RequestMetadataLine
          metadata={{
            values: {
              project: {
                value: "astrlink",
                source: "session",
                set_by: "model_marker",
                turn: 2,
              },
              env: { value: "prod", source: "token" },
            },
            events: [
              {
                key: "env",
                event: "ignored",
                value: "Prod",
                set_by: "header",
                reason: "invalid_value",
              },
            ],
          }}
        />,
      );
    });
    const line = document.querySelector('[data-testid="request-metadata"]');
    expect(line?.textContent).toContain(
      "project=astrlink (会话绑定 · 第 2 轮)",
    );
    expect(line?.textContent).toContain("env=prod (令牌默认值)");
    expect(line?.textContent).toContain("已忽略 env=Prod：值不符合校验规则");
    await act(async () => {
      reactRoot.render(
        <RequestMetadataLine metadata={{ values: {}, events: [] }} />,
      );
    });
    expect(
      document.querySelector('[data-testid="request-metadata"]'),
    ).toBeNull();
  });
});
