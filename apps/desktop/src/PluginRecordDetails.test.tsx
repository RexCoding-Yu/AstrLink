// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({ runPluginAction: vi.fn() }));
const notify = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("./bridge", () => bridge);
vi.mock("./notify", () => ({ notify }));

import {
  PluginRecordDetails,
  RequestMetadataLine,
} from "./PluginRecordDetails";
import { installDialogAnimations } from "./lib/test-dialog-animations";
import { settleClosedLayer } from "./lib/test-layer-settle";
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

  it("shows a turn waiting for its project and what the capture wrote", async () => {
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
                status: "skipped",
                summary: "召回 0/9 · 1 个检索失败",
                skipped_reason: "search_failed",
                elapsed_ms: 2400,
                network: { requests: 5, denied: 0, errors: 1, wait_ms: 2400 },
                observe: "awaiting_reply",
              },
              {
                plugin: "powercontext",
                plugin_version: "1.0.0",
                hook: "observeTurn",
                title: "记忆写入",
                status: "ok",
                summary: "已写入 · 已整理成记忆",
                elapsed_ms: 16500,
                network: { requests: 2, denied: 0, errors: 0, wait_ms: 16480 },
              },
            ],
          }}
        />,
      );
    });
    const [turn, write] = document.querySelectorAll(
      '[data-testid="plugin-record"]',
    );
    expect(turn?.textContent).toContain("有检索失败，没有命中");
    expect(turn?.textContent).toContain("写入等回复确定项目");
    expect(write?.textContent).toContain("记忆写入");
    expect(write?.textContent).toContain("已写入 · 已整理成记忆");
  });

  it("shows the lookups the model ran by their size, and the ones offered", async () => {
    await act(async () => {
      reactRoot.render(
        <PluginRecordDetails
          record={{
            ...record,
            plugin_records: [
              {
                plugin: "powercontext",
                plugin_version: "2.0.0",
                hook: "prepareTurn",
                title: "询问项目",
                status: "ok",
                summary: "列出 20 个项目（共 35 个）",
                elapsed_ms: 120,
                network: { requests: 2, denied: 0, errors: 0, wait_ms: 110 },
                commands: ["projects"],
              },
              {
                plugin: "powercontext",
                plugin_version: "2.0.0",
                hook: "callTool",
                title: "记忆查询",
                status: "ok",
                summary: "检索 · 命中 3 条",
                elapsed_ms: 820,
                network: { requests: 6, denied: 0, errors: 0, wait_ms: 800 },
                lookup: {
                  tool: "search",
                  input_runes: 12,
                  result_bytes: 2150,
                  hits: 3,
                },
              },
              {
                plugin: "powercontext",
                plugin_version: "2.0.0",
                hook: "callTool",
                title: "PowerContext 记忆",
                status: "skipped",
                summary: "",
                skipped_reason: "turn_limit",
                elapsed_ms: 0,
                network: { requests: 0, denied: 0, errors: 0, wait_ms: 0 },
                lookup: { tool: "read", input_runes: 4, result_bytes: 0 },
              },
            ],
          }}
        />,
      );
    });
    const [ask, search, refused] = document.querySelectorAll(
      '[data-testid="plugin-record"]',
    );
    expect(ask?.textContent).toContain("附上查询projects");
    expect(search?.textContent).toContain(
      "search · 参数 12 字 · 结果 2.1 KiB · 3 条结果",
    );
    expect(refused?.textContent).toContain("这一轮已经查满 3 次");
    expect(
      refused?.querySelector('[data-testid="plugin-record-lookup"]')
        ?.textContent,
    ).toBe("read · 参数 4 字");
  });

  it("says so when no plugin ran", async () => {
    await act(async () => {
      reactRoot.render(<PluginRecordDetails record={record} />);
    });
    expect(container.textContent).toBe("这个请求没有插件记录。");
  });

  it("says why a block was loaded and runs the plugin's buttons after asking", async () => {
    const removeAnimations = installDialogAnimations();
    bridge.runPluginAction.mockResolvedValue({
      status: "ok",
      refresh: true,
      message: "这个会话的下一个请求会重新装载记忆。",
    });
    await act(async () => {
      reactRoot.render(
        <PluginRecordDetails
          record={{
            ...record,
            session_id: "session_1",
            plugin_records: [
              {
                plugin: "powercontext",
                plugin_version: "2.0.0",
                hook: "prepareSession",
                title: "记忆装载",
                status: "ok",
                summary: "画像 · 主题 20 个 · 4.2 KB",
                elapsed_ms: 310,
                network: { requests: 2, denied: 0, errors: 0, wait_ms: 300 },
                trigger: "compact",
                actions: [
                  { id: "refresh", label: "刷新记忆", confirm: "会重写缓存。" },
                ],
              },
              {
                plugin: "team-rules",
                plugin_version: "1.0.0",
                hook: "prepareSession",
                title: "Team rules",
                status: "skipped",
                summary: "",
                elapsed_ms: 0,
                network: { requests: 0, denied: 0, errors: 0, wait_ms: 0 },
                skipped_reason: "over_budget",
              },
            ],
          }}
        />,
      );
    });
    expect(container.textContent).toContain("compact 后刷新");
    expect(container.textContent).toContain("超出注入总预算");
    const action = container.querySelector<HTMLButtonElement>(
      "[data-testid=plugin-record-action]",
    );
    expect(action?.textContent).toBe("刷新记忆");
    await act(async () => action?.click());
    // The plugin asked for a confirmation: nothing runs before it.
    expect(bridge.runPluginAction).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain("会重写缓存。");
    const confirm = [
      ...document.querySelectorAll<HTMLButtonElement>(
        "[role=alertdialog] button",
      ),
    ].find((button) => button.textContent === "刷新记忆");
    await act(async () => confirm?.click());
    await settleClosedLayer();
    expect(bridge.runPluginAction).toHaveBeenCalledWith(
      "powercontext",
      "refresh",
      "session_1",
    );
    expect(notify.success).toHaveBeenCalledWith(
      "这个会话的下一个请求会重新装载记忆。",
    );
    removeAnimations();
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
              {
                key: "project",
                event: "bound",
                value: "astrlink",
                set_by: "model_marker",
                normalized: "AstrLink",
              },
              {
                key: "team",
                event: "bound",
                value: "infra",
                set_by: "user_marker",
                new: true,
              },
            ],
          }}
        />,
      );
    });
    const line = document.querySelector('[data-testid="request-metadata"]');
    // A session binding says how it was set.
    expect(line?.textContent).toContain(
      "project=astrlink (模型标记 · 第 2 轮)",
    );
    expect(line?.textContent).toContain(
      "project: 绑定为 astrlink (模型标记 · 原写法 AstrLink)",
    );
    expect(line?.textContent).toContain(
      "team: 绑定为 infra (用户标记 · 新建项目)",
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
