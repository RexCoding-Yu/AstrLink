// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({
  getSessionPlugins: vi.fn(),
  getSessionMetadata: vi.fn(),
  listMetadataAttributes: vi.fn(),
  listMetadataValues: vi.fn(),
  pauseSessionPlugin: vi.fn(),
  refreshSessionPlugin: vi.fn(),
  setSessionMetadata: vi.fn(),
  unbindSessionMetadata: vi.fn(),
}));
vi.mock("./bridge", () => bridge);

import { applyLocale } from "./i18n";
import {
  finishExitAnimations,
  installDialogAnimations,
} from "./lib/test-dialog-animations";
import type { SessionPlugin } from "./plugin-model";
import {
  emptyTrajectoryFields,
  type RequestRecord,
} from "./request-record-model";
import { SessionMemory } from "./SessionMemory";

const part: SessionPlugin = {
  plugin: "powercontext",
  name: "PowerContext 记忆",
  paused: false,
  first_turn: 1,
  block: {
    text: "<project-context>\n项目画像\n- 约定：用中文沟通\n</project-context>",
    bytes: 64,
    trigger: "bound",
    language: "zh-CN",
    computed_at: "2026-10-21T14:05:00Z",
  },
};

const record = {
  id: "req_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  parent_request_id: null,
  attempt_index: 1,
  child_count: 0,
  started_at: "2026-10-21T14:05:00Z",
  completed_at: "2026-10-21T14:05:02Z",
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
      trigger: "bound",
    },
    {
      plugin: "other",
      plugin_version: "1.0.0",
      hook: "observeTurn",
      title: "别的插件",
      status: "ok",
      summary: "",
      elapsed_ms: 1,
      network: { requests: 0, denied: 0, errors: 0, wait_ms: 0 },
    },
  ],
} as RequestRecord;

let root: Root, container: HTMLDivElement, removeAnimations: () => void;
beforeEach(async () => {
  await applyLocale("zh-CN");
  (
    globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  vi.resetAllMocks();
  removeAnimations = installDialogAnimations();
  bridge.getSessionPlugins.mockResolvedValue([part]);
  bridge.getSessionMetadata.mockResolvedValue({
    values: {
      project: {
        value: "astrlink",
        set_by: "workspace",
        turn: 1,
        updated_at: "2026-10-21T14:05:00Z",
      },
    },
    workspace: { name: "astrlink", values: { project: "astrlink" } },
  });
  bridge.listMetadataAttributes.mockResolvedValue([
    {
      key: "project",
      label: "项目",
      description: "",
      pattern: "^[a-z0-9][a-z0-9._-]{0,63}$",
      marker: "memory-project",
      owner: "powercontext",
      created_at: "2026-10-18T00:00:00Z",
      updated_at: "2026-10-18T00:00:00Z",
      header: "X-Astrlink-Meta-Project",
    },
  ]);
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  removeAnimations();
});

async function render(onSelectRequest = vi.fn()) {
  await act(async () => {
    root.render(
      <SessionMemory
        onSelectRequest={onSelectRequest}
        records={[record]}
        refreshKey="1"
        sessionId="session_1"
      />,
    );
  });
  return onSelectRequest;
}

function button(label: string, within: ParentNode = document) {
  const found = [...within.querySelectorAll("button")].find(
    (item) => item.textContent?.trim() === label,
  );
  if (!found) throw new Error(`no ${label} button`);
  return found;
}

it("shows the project, how it was bound, and the block the session carries", async () => {
  const onSelectRequest = await render();
  const memory = container.querySelector("[data-testid=session-memory]");
  expect(memory?.textContent).toContain("astrlink · 工作区 · 第 1 轮");
  expect(memory?.textContent).toContain("工作目录astrlink");
  expect(memory?.textContent).toContain("从第 1 轮开始参与这个会话");
  expect(memory?.textContent).toContain("项目刚绑定");
  expect(
    container.querySelector("[data-testid=session-memory-block]")?.textContent,
  ).toContain("- 约定：用中文沟通");
  // Only the records of the plugins that load blocks, each opening its call.
  expect(memory?.textContent).toContain(
    "记忆装载 · 画像 · 主题 20 个 · 4.2 KB",
  );
  expect(memory?.textContent).not.toContain("别的插件");
  const row = [...container.querySelectorAll("button")].find((item) =>
    item.textContent?.includes("记忆装载 · 画像"),
  );
  await act(async () => row?.click());
  expect(onSelectRequest).toHaveBeenCalledWith(record.id);
});

it("refreshes and pauses only after saying what it costs", async () => {
  bridge.refreshSessionPlugin.mockResolvedValue([
    { ...part, refresh_pending: true },
  ]);
  bridge.pauseSessionPlugin.mockResolvedValue([{ ...part, paused: true }]);
  await render();
  await act(async () => button("刷新记忆").click());
  expect(bridge.refreshSessionPlugin).not.toHaveBeenCalled();
  expect(document.body.textContent).toContain("提示词缓存要重写一次");
  await act(async () =>
    button(
      "刷新",
      document.querySelector("[role=alertdialog]") as HTMLElement,
    ).click(),
  );
  await finishExitAnimations();
  expect(bridge.refreshSessionPlugin).toHaveBeenCalledWith(
    "session_1",
    "powercontext",
  );
  expect(container.textContent).toContain("下一个请求重新装载");

  await act(async () => button("暂停本会话").click());
  await act(async () =>
    button(
      "暂停",
      document.querySelector("[role=alertdialog]") as HTMLElement,
    ).click(),
  );
  await finishExitAnimations();
  expect(bridge.pauseSessionPlugin).toHaveBeenCalledWith(
    "session_1",
    "powercontext",
    true,
  );
  expect(container.textContent).toContain("已暂停");
  expect(button("恢复")).toBeTruthy();
  expect(button("刷新记忆").disabled).toBe(true);
});

it("shows how the session's requests tell the model to look up", async () => {
  bridge.getSessionPlugins.mockResolvedValue([
    {
      ...part,
      lookup:
        '<lookup name="powercontext">\ncurl -s -G http://127.0.0.1:18317/tools/powercontext/search -H "Authorization: Bearer mk-…"\n</lookup>',
    },
  ]);
  await render();
  const lookup = container.querySelector("[data-testid=session-memory-lookup]");
  expect(lookup?.textContent).toContain("Bearer mk-…");
  expect(container.textContent).toContain(
    "附在系统提示词里的查询方法（凭证已隐去）",
  );
});

it("says when the gateway holds no block for the session", async () => {
  bridge.getSessionPlugins.mockResolvedValue([{ ...part, block: undefined }]);
  await render();
  expect(container.textContent).toContain(
    "这个会话现在没有装载内容。网关重启后，要等下一个请求才会重新算出。",
  );
});
