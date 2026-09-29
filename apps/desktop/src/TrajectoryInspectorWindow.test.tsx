// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const hostMocks = vi.hoisted(() => ({
  invoke: vi.fn(),
  listen: vi.fn(),
  unlisten: vi.fn(),
}));

vi.mock("@tauri-apps/api/core", () => ({ invoke: hostMocks.invoke }));
vi.mock("@tauri-apps/api/event", () => ({ listen: hostMocks.listen }));
vi.mock("@tauri-apps/api/window", () => ({
  getCurrentWindow: () => ({
    label: "trajectory-inspector-2",
    isFocused: async () => true,
    isFullscreen: async () => false,
    isMaximized: async () => false,
    onFocusChanged: async () => () => undefined,
    onResized: async () => () => undefined,
  }),
}));

const bridgeMocks = vi.hoisted(() => ({
  getRequestAuditContent: vi.fn(),
}));
vi.mock("./bridge", () => bridgeMocks);

import type { AuditContent, RequestRecord } from "./request-record-model";
import { emptyTrajectoryFields } from "./request-record-model";
import type { TrajectoryRow } from "./request-trajectory-model";
import { TrajectoryInspectorWindow } from "./TrajectoryInspectorWindow";
import { WindowChrome, WindowChromeProvider } from "./WindowChrome";
import {
  detachedInspectorEnabled,
  isTrajectoryInspectorWindow,
  type TrajectoryInspectorSelection,
  type TrajectoryInspectorWindowState,
} from "./trajectory-inspector-window";

const record: RequestRecord = {
  id: "req_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  parent_request_id: null,
  attempt_index: 1,
  child_count: 0,
  started_at: "2026-07-25T10:00:00Z",
  completed_at: "2026-07-25T10:00:01Z",
  status: "succeeded",
  input_protocol: "openai.responses",
  requested_model: "gpt-4.1",
  streaming: true,
  route_id: "route_primary",
  service_id: "service_9bae092569a028b1f3f38d36",
  local_access_token_id: "token_01",
  http_status: 200,
  latency_ms: 120,
  usage: null,
  error: null,
  audit: {
    request_body_captured: false,
    response_content_captured: false,
    request_body_truncated: false,
    response_content_truncated: false,
    upstream_request_body_captured: false,
    upstream_response_content_captured: true,
    upstream_request_body_truncated: false,
    upstream_response_content_truncated: false,
  },
  privacy_restore: null,
  ...emptyTrajectoryFields,
};

const row: TrajectoryRow = {
  id: `${record.id}:upstream`,
  requestId: record.id,
  chip: "UPSTREAM",
  summary: "HTTP 200",
  result: "成功",
  status: "succeeded",
  tone: "ok",
  startedAt: record.started_at,
  endedAt: record.completed_at,
  lane: "upstream",
  child: false,
  turnIndex: 1,
};

const laterRow: TrajectoryRow = {
  ...row,
  id: `${record.id}:accepted`,
  chip: "CLIENT",
  summary: "gpt-4.1 · openai.responses",
  lane: "client",
};

const auditContent: AuditContent = {
  request_id: record.id,
  http_meta: null,
  request_body: null,
  response_content: null,
  upstream_http_meta: null,
  upstream_request_body: null,
  upstream_response_content: {
    media_type: "application/json",
    content: '{"ok":true}',
    truncated: false,
    captured_bytes: 11,
  },
};

/** What the host reports when this window pulls its state on mount. */
const hostState: { current: TrajectoryInspectorWindowState } = {
  current: { selection: null, pinned: false },
};

function pushSelection(selection: TrajectoryInspectorSelection): void {
  const call = hostMocks.listen.mock.calls.find(
    ([name]) => name === "trajectory-inspector:select",
  );
  if (!call) throw new Error("The inspector window never subscribed");
  (call[1] as (event: { payload: TrajectoryInspectorSelection }) => void)({
    payload: selection,
  });
}

function inspector(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>(
    '[data-testid="trajectory-inspector"]',
  );
}

function pinButton(container: HTMLElement): HTMLButtonElement {
  const button = container.querySelector<HTMLButtonElement>(
    '[data-testid="trajectory-inspector-pin"]',
  );
  if (!button) throw new Error("The inspector window has no pin control");
  return button;
}

describe("TrajectoryInspectorWindow", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    vi.clearAllMocks();
    hostState.current = { selection: null, pinned: false };
    hostMocks.listen.mockResolvedValue(hostMocks.unlisten);
    hostMocks.invoke.mockImplementation(
      async (command: string, args?: Record<string, unknown>) => {
        if (command === "trajectory_inspector_state") return hostState.current;
        if (command === "set_trajectory_inspector_pinned") return args?.pinned;
        return undefined;
      },
    );
    bridgeMocks.getRequestAuditContent.mockResolvedValue(auditContent);
    Object.assign(window, {
      __TAURI_INTERNALS__: {},
      __ASTRLINK_DESKTOP_PLATFORM__: "macos",
    });
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });

  afterEach(() => {
    act(() => root.unmount());
    container.remove();
    delete (window as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__;
    delete (window as { __ASTRLINK_DESKTOP_PLATFORM__?: string })
      .__ASTRLINK_DESKTOP_PLATFORM__;
  });

  const flush = async () => {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
  };

  const render = async () => {
    await act(async () => {
      // The pin lives in the title bar, so the window renders with its chrome.
      root.render(
        <WindowChromeProvider>
          <WindowChrome platform="macos" />
          <TrajectoryInspectorWindow />
        </WindowChromeProvider>,
      );
    });
    await flush();
  };

  const clickPin = async () => {
    await act(async () => {
      pinButton(container).click();
    });
    await flush();
  };

  it("marks an identical upstream response and preserves stream failure with HTTP 200", async () => {
    const content =
      'data: {"type":"response.output_text.delta","delta":"Client reply"}\n\n';
    const part = {
      content,
      media_type: "text/event-stream",
      captured_bytes: content.length,
      truncated: false,
    };
    const failed: RequestRecord = {
      ...record,
      status: "failed",
      error: {
        category: "upstream",
        code: "upstream_stream_interrupted",
        message: "Stream ended early",
        retryable: true,
      },
    };
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      ...auditContent,
      response_content: part,
      upstream_response_content: part,
    });
    hostState.current = {
      selection: { record: failed, row: { ...row, chip: "RESULT" } },
      pinned: false,
    };
    await render();
    const preview = container.querySelector(
      '[data-testid="audit-result-preview"]',
    );
    expect(preview?.textContent).toContain("Client reply");
    expect(
      container.querySelector('[data-testid="inspector-http"]')?.textContent,
    ).toBe("HTTP 200");
    // Without a provider error the gateway's code leads the one reason card.
    const diagnosis = preview?.querySelector(
      '[data-testid="inspector-diagnosis"]',
    );
    expect(diagnosis?.textContent).toContain("upstream_stream_interrupted");
    expect(diagnosis?.textContent).toContain("可重试");
    expect(diagnosis?.textContent).toContain("Stream ended early");
    expect(diagnosis?.textContent).toContain("不代表流式输出成功完成");
    expect(
      container.querySelectorAll('[data-testid="inspector-diagnosis"]'),
    ).toHaveLength(1);
    expect(container.querySelector('[data-testid="audit-raw"]')).toBeNull();
    await act(async () => {
      container
        .querySelector<HTMLButtonElement>(
          '[data-testid="inspector-tab"][data-chip="UPSTREAM"]',
        )!
        .click();
    });
    await flush();
    expect(
      container.querySelector('[data-testid="inspector-same-response"]')
        ?.textContent,
    ).toContain("与客户端响应一致");
    expect(
      container.querySelector('[data-testid="audit-result-preview"]'),
    ).toBeNull();
    const body = container.querySelector(
      '[data-testid="inspector-upstream-body"]',
    );
    expect(body?.getAttribute("data-view")).toBe("response");
    // Stream bodies open as events; the original stays one toggle away.
    expect(body?.textContent).toContain("response.output_text.delta");
    expect(container.querySelector('[data-testid="audit-raw"]')).toBeNull();
  });

  it("leads with the provider's error and keeps the gateway verdict as context", async () => {
    const content = [
      'data: {"type":"response.created","response":{"status":"in_progress"}}',
      'data: {"type":"response.failed","response":{"status":"failed","error":{"code":"rate_limit_exceeded","message":"Token rate limit exceeded"}}}',
      "",
    ].join("\n\n");
    const part = {
      content,
      media_type: "text/event-stream",
      captured_bytes: content.length,
      truncated: false,
    };
    const failed: RequestRecord = {
      ...record,
      status: "failed",
      error: {
        category: "upstream",
        code: "upstream_stream_interrupted",
        message: "Stream ended early",
        retryable: true,
      },
    };
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      ...auditContent,
      upstream_http_meta: {
        method: "POST",
        url: "https://api.example.test/v1/responses",
        http_version: "HTTP/2",
        request_headers: [],
        response_status: 200,
        response_headers: [],
      },
      upstream_request_body: {
        content: '{"model":"gpt-4.1"}',
        media_type: "application/json",
        captured_bytes: 19,
        truncated: false,
      },
      response_content: part,
      upstream_response_content: part,
    });
    hostState.current = {
      selection: { record: failed, row: { ...row, chip: "RESULT" } },
      pinned: false,
    };
    await render();
    const diagnosis = container.querySelector(
      '[data-testid="inspector-diagnosis"]',
    );
    expect(diagnosis?.firstElementChild?.textContent).toBe(
      "rate_limit_exceeded",
    );
    expect(diagnosis?.textContent).toContain("Token rate limit exceeded");
    const verdict = diagnosis?.querySelector(
      '[data-testid="inspector-gateway-verdict"]',
    );
    expect(verdict?.textContent).toContain("upstream_stream_interrupted");
    expect(verdict?.textContent).toContain("可重试");
    // The HTTP note already explains an interrupted stream in the UI language.
    expect(verdict?.textContent).not.toContain("Stream ended early");
    expect(verdict?.getAttribute("title")).toBe("Stream ended early");
    // The reason replaces the empty-state card instead of sitting above it.
    expect(
      container.querySelector('[data-testid="audit-no-output"]')?.textContent,
    ).toBe("请求在产生输出前失败");
    expect(container.textContent).not.toContain("未捕获到回复或工具调用");

    await act(async () => {
      container
        .querySelector<HTMLButtonElement>(
          '[data-testid="inspector-tab"][data-chip="UPSTREAM"]',
        )!
        .click();
    });
    await flush();
    expect(
      container.querySelector('[data-testid="inspector-upstream-endpoint"]')
        ?.textContent,
    ).toBe("POST https://api.example.test/v1/responses");
    const body = () =>
      container.querySelector<HTMLElement>(
        '[data-testid="inspector-upstream-body"]',
      )!;
    expect(
      body().querySelector('[data-testid="inspector-diagnosis"]')?.textContent,
    ).toContain("rate_limit_exceeded");
    const view = (label: string) =>
      [
        ...container.querySelectorAll<HTMLButtonElement>(
          '[aria-label="上游内容"] button',
        ),
      ].find((button) => button.textContent === label)!;
    await act(async () => view("请求").click());
    expect(body().getAttribute("data-view")).toBe("request");
    expect(
      body().querySelector('[data-testid="inspector-diagnosis"]'),
    ).toBeNull();
    expect(body().textContent).toContain('"model": "gpt-4.1"');
    await act(async () => view("HTTP").click());
    expect(body().getAttribute("data-view")).toBe("http");
    expect(body().textContent).toContain(
      "POST https://api.example.test/v1/responses HTTP/2",
    );
  });

  it("keeps differing upstream bytes separate from the actual client output", async () => {
    const part = (content: string) => ({
      content,
      media_type: "text/plain",
      captured_bytes: content.length,
      truncated: false,
    });
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      ...auditContent,
      response_content: part("Restored client reply"),
      upstream_response_content: part("Upstream placeholder reply"),
    });
    hostState.current = { selection: { record, row }, pinned: false };
    await render();
    expect(
      container.querySelector('[data-testid="audit-raw"] pre')?.textContent,
    ).toBe("Upstream placeholder reply");
    expect(
      container.querySelector('[data-testid="inspector-same-response"]'),
    ).toBeNull();
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>(
          '[data-testid="inspector-tab"][data-chip="RESULT"]',
        )!
        .click(),
    );
    expect(
      container.querySelector('[data-testid="audit-result-preview"]')
        ?.textContent,
    ).toBe("Restored client reply");
    expect(container.textContent).not.toContain("Upstream placeholder reply");
  });

  it("shows the client body once, beside its request line and HTTP envelope", async () => {
    const content = '{"model":"gpt-4.1","input":"ping"}';
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      ...auditContent,
      http_meta: {
        method: "POST",
        url: "/v1/responses",
        http_version: "HTTP/1.1",
        request_headers: [
          { name: "Content-Type", value: "application/json", redacted: false },
        ],
        response_status: 200,
        response_headers: [],
      },
      request_body: {
        content,
        media_type: "application/json",
        captured_bytes: content.length,
        truncated: false,
      },
    });
    hostState.current = {
      selection: { record, row: laterRow },
      pinned: false,
    };
    await render();

    const section = container.querySelector<HTMLElement>(
      '[data-testid="inspector-section"][data-chip="CLIENT"]',
    );
    expect(
      section?.querySelector('[data-testid="inspector-client-endpoint"]')
        ?.textContent,
    ).toBe("POST /v1/responses");
    expect(
      section?.querySelector('[data-testid="inspector-client-size"]')
        ?.textContent,
    ).toBe(`${content.length} B`);
    // One pane, no card inside a card repeating the title and size.
    expect(section?.textContent?.split("客户端请求体")).toHaveLength(1);
    expect(section?.textContent?.split(`${content.length} B`)).toHaveLength(2);
    const body = () =>
      section!.querySelector<HTMLElement>(
        '[data-testid="inspector-client-body"]',
      )!;
    expect(body().getAttribute("data-view")).toBe("request");
    expect(body().textContent).toContain('"input": "ping"');

    const http = [
      ...section!.querySelectorAll<HTMLButtonElement>(
        '[aria-label="客户端内容"] button',
      ),
    ].find((button) => button.textContent === "HTTP")!;
    await act(async () => http.click());
    expect(body().getAttribute("data-view")).toBe("http");
    expect(body().textContent).toContain("POST /v1/responses HTTP/1.1");
    expect(body().textContent).toContain("Content-Type");
  });

  it("never asks itself to open another inspector window", () => {
    expect(isTrajectoryInspectorWindow()).toBe(true);
    expect(detachedInspectorEnabled()).toBe(false);
  });

  it("waits for a selection before it shows a call", async () => {
    await render();

    expect(inspector(container)).toBeNull();
    expect(container.textContent).toContain("尚未选择链路");
    // Pulled rather than announced: subscribing first and then asking leaves no
    // window in which the host has already sent the phase to nobody.
    expect(hostMocks.listen).toHaveBeenCalled();
    expect(hostMocks.invoke).toHaveBeenCalledWith("trajectory_inspector_state");
  });

  it("restores the phase the host had already stored for it", async () => {
    // How a pinned window comes back after the dev host reloads every webview:
    // its React state is gone but the host still knows what it froze on.
    hostState.current = { selection: { row, record }, pinned: true };

    await render();

    expect(inspector(container)?.getAttribute("data-focus-chip")).toBe(
      "UPSTREAM",
    );
    expect(inspector(container)?.getAttribute("data-request-id")).toBe(
      record.id,
    );
    expect(inspector(container)?.getAttribute("data-pinned")).toBe("true");
    expect(pinButton(container).getAttribute("aria-pressed")).toBe("true");
    // A window-level control: it belongs beside the traffic lights, drawn as a
    // pushpin rather than a map marker, not inside the call's own header.
    expect(
      pinButton(container).closest('[data-slot="window-accessory"]'),
    ).not.toBeNull();
    expect(inspector(container)?.contains(pinButton(container))).toBe(false);
    expect(
      pinButton(container).querySelector('[data-animated-icon="pin"]'),
    ).not.toBeNull();
  });

  it("shows the pushed call and decrypts its own audit content", async () => {
    await render();

    await act(async () => {
      pushSelection({ row, record });
    });
    await flush();

    expect(inspector(container)?.getAttribute("data-focus-chip")).toBe(
      "UPSTREAM",
    );
    expect(inspector(container)?.getAttribute("data-request-id")).toBe(
      record.id,
    );
    expect(
      [
        ...inspector(container)!.querySelectorAll(
          '[data-testid="inspector-tab"]',
        ),
      ].map((tab) => tab.getAttribute("data-chip")),
    ).toEqual(["CLIENT", "ROUTE", "UPSTREAM", "RESULT"]);
    expect(
      inspector(container)
        ?.querySelector('[data-testid="inspector-tab"][data-chip="UPSTREAM"]')
        ?.getAttribute("aria-selected"),
    ).toBe("true");
    expect(
      inspector(container)
        ?.querySelector('[data-testid="inspector-section"]')
        ?.getAttribute("data-chip"),
    ).toBe("UPSTREAM");
    expect(
      inspector(container)?.querySelector('[aria-label="上游响应"]'),
    ).not.toBeNull();
    expect(inspector(container)?.textContent).not.toContain("客户端请求体");
    expect(
      inspector(container)?.querySelector('[data-testid="inspector-http"]')
        ?.textContent,
    ).toBe("HTTP 200");
    // The body is fetched here rather than forwarded, so captured text never
    // crosses the event channel.
    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledWith(record.id);
    expect(inspector(container)?.textContent).toContain('"ok": true');
  });

  it("freezes on its call once pinned and thaws when unpinned", async () => {
    await render();
    await act(async () => {
      pushSelection({ row, record });
    });
    await flush();

    // A following window keeps a quiet, icon-only pin in its title bar.
    expect(pinButton(container).getAttribute("aria-label")).toBe("置顶窗口");
    expect(pinButton(container).getAttribute("aria-pressed")).toBe("false");
    expect(pinButton(container).textContent).toBe("");

    await clickPin();

    expect(hostMocks.invoke).toHaveBeenCalledWith(
      "set_trajectory_inspector_pinned",
      { pinned: true },
    );
    expect(inspector(container)?.getAttribute("data-pinned")).toBe("true");
    // A floating window says so without a hover.
    expect(pinButton(container).getAttribute("aria-pressed")).toBe("true");
    expect(pinButton(container).textContent).toBe("已置顶");

    await act(async () => {
      pushSelection({ row: laterRow, record });
    });
    await flush();

    // The host stops routing here, and a stray push is refused anyway, so the
    // two sides cannot disagree about what a pinned window shows.
    expect(inspector(container)?.getAttribute("data-focus-chip")).toBe(
      "UPSTREAM",
    );

    await clickPin();

    expect(hostMocks.invoke).toHaveBeenCalledWith(
      "set_trajectory_inspector_pinned",
      { pinned: false },
    );

    await act(async () => {
      pushSelection({ row: laterRow, record });
    });
    await flush();

    expect(inspector(container)?.getAttribute("data-focus-chip")).toBe(
      "CLIENT",
    );
  });

  it("puts the pin back when the host refuses to float the window", async () => {
    hostMocks.invoke.mockImplementation(async (command: string) => {
      if (command === "trajectory_inspector_state") return hostState.current;
      throw new Error("the window level cannot be changed");
    });
    await render();
    await act(async () => {
      pushSelection({ row, record });
    });
    await flush();

    await clickPin();

    // A button left reading "pinned" over a window that still follows the list
    // is worse than one that admits the pin did not take.
    expect(inspector(container)?.getAttribute("data-pinned")).toBe("false");
  });

  it("lists every provider the call tried under one route tab, by name", async () => {
    const serviceA = "service_aaaaaaaaaaaaaaaaaaaaaaaa";
    const serviceB = "service_bbbbbbbbbbbbbbbbbbbbbbbb";
    const at = (second: number) => `2026-07-25T10:00:0${second}Z`;
    const failed: RequestRecord = {
      ...record,
      status: "failed",
      service_id: serviceA,
      http_status: null,
      error: {
        category: "upstream",
        code: "upstream_unavailable",
        message: "unexpected EOF",
        retryable: true,
      },
      events: [
        {
          kind: "accepted",
          started_at: at(0),
          ended_at: at(0),
          status: "succeeded",
          summary: "gpt-4.1 · openai.responses",
          attempt_index: 0,
        },
        {
          kind: "routed",
          started_at: at(1),
          ended_at: at(1),
          status: "succeeded",
          summary: `native · ${serviceA}`,
          attempt_index: 1,
        },
        {
          kind: "upstream",
          started_at: at(1),
          ended_at: at(2),
          status: "failed",
          summary: "upstream_unavailable",
          attempt_index: 1,
        },
        {
          kind: "routed",
          started_at: at(2),
          ended_at: at(2),
          status: "failed",
          summary: `${serviceB} · credential_unavailable`,
          attempt_index: 1,
        },
        {
          kind: "completed",
          started_at: at(3),
          ended_at: at(3),
          status: "failed",
          summary: "upstream_unavailable",
          attempt_index: 1,
        },
      ],
    };
    await render();

    await act(async () => {
      pushSelection({
        row: {
          ...row,
          id: `${failed.id}:routed`,
          chip: "ROUTE",
          lane: "gateway",
        },
        record: failed,
        services: {
          [serviceA]: { id: serviceA, name: "Primary" },
          [serviceB]: { id: serviceB, name: "Backup" },
        },
      });
    });
    await flush();

    expect(
      [
        ...inspector(container)!.querySelectorAll(
          '[data-testid="inspector-tab"]',
        ),
      ].map((tab) => tab.getAttribute("data-chip")),
    ).toEqual(["CLIENT", "ROUTE", "UPSTREAM", "RESULT"]);
    expect(
      [
        ...inspector(container)!.querySelectorAll(
          '[data-testid="route-attempts"] li',
        ),
      ].map((item) => [item.textContent, item.getAttribute("data-tone")]),
    ).toEqual([
      ["native · Primary", "ok"],
      ["Backup · credential_unavailable", "failed"],
    ]);
    expect(inspector(container)?.textContent).toContain("尝试过的 API 提供商");
  });

  it("says why routing chose the provider and names the ones it skipped", async () => {
    const skipped = "service_aaaaaaaaaaaaaaaaaaaaaaaa";
    const deleted = "service_bbbbbbbbbbbbbbbbbbbbbbbb";
    const routeRow: TrajectoryRow = {
      ...row,
      id: `${record.id}:routed`,
      chip: "ROUTE",
      lane: "gateway",
    };
    await render();

    await act(async () => {
      pushSelection({
        row: routeRow,
        record: {
          ...record,
          routing_decision: {
            selected: "failover",
            skipped: [
              { service_id: skipped, reason: "model_not_listed" },
              { service_id: deleted, reason: "disabled" },
            ],
          },
        },
        services: { [skipped]: { id: skipped, name: "mly" } },
      });
    });
    await flush();

    const decision = inspector(container)?.querySelector(
      '[data-testid="routing-decision"]',
    );
    expect(
      decision
        ?.querySelector("[data-selection]")
        ?.getAttribute("data-selection"),
    ).toBe("failover");
    expect(decision?.textContent).toContain(
      "故障切换：此前尝试的 API 提供商失败或被拒绝",
    );
    // A provider missing from the list is still named, by its ID.
    expect(
      [
        ...(decision?.querySelectorAll('[data-testid="routing-skipped"] li') ??
          []),
      ].map((item) => [item.textContent, item.getAttribute("data-reason")]),
    ).toEqual([
      ["mly · 未列出该模型", "model_not_listed"],
      [`${deleted} · 已停用`, "disabled"],
    ]);

    // Records from before the gateway explained its choice show nothing.
    await act(async () => {
      pushSelection({ row: routeRow, record });
    });
    await flush();
    expect(
      inspector(container)?.querySelector('[data-testid="routing-decision"]'),
    ).toBeNull();
  });

  it("uses the same icon and hint for continuation and provider stickiness", async () => {
    await render();
    await act(async () => {
      pushSelection({
        row: { ...row, chip: "ROUTE", lane: "gateway" },
        record: {
          ...record,
          session_link: { kind: "explicit", value: "previous-request" },
          routing_decision: { selected: "session_binding", skipped: [] },
        },
      });
    });
    await flush();
    const marks = inspector(container)?.querySelectorAll(
      "[data-conversation-indicator]",
    );
    expect(marks).toHaveLength(2);
    expect(
      Array.from(marks ?? [], (mark) => mark.getAttribute("aria-label")),
    ).toEqual(["对话延续：会话粘性", "对话延续：会话粘性"]);
    for (const mark of marks ?? []) {
      expect(mark.querySelector('[data-animated-icon="link"]')).not.toBeNull();
      expect(mark.textContent).toBe("");
    }
  });

  it("has no close button of its own, because the window frame owns that", async () => {
    await render();
    await act(async () => {
      pushSelection({ row, record });
    });

    expect(
      container.querySelector('[data-testid="trajectory-inspector-close"]'),
    ).toBeNull();
  });

  it("refetches audit when a pending record's captured flags flip", async () => {
    await render();
    const pendingRecord: RequestRecord = {
      ...record,
      status: "pending",
      completed_at: null,
      http_status: null,
      latency_ms: null,
      audit: {
        ...record.audit,
        request_body_captured: false,
        upstream_response_content_captured: false,
      },
    };
    const clientRow: TrajectoryRow = {
      ...laterRow,
      status: "pending",
      tone: "pending",
      endedAt: null,
    };
    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      request_id: record.id,
      http_meta: null,
      request_body: null,
      response_content: null,
      upstream_http_meta: null,
      upstream_request_body: null,
      upstream_response_content: null,
    });

    await act(async () => {
      pushSelection({ row: clientRow, record: pendingRecord });
    });
    await flush();
    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledTimes(1);
    expect(
      inspector(container)?.querySelector(
        '[data-testid="inspector-missing-body"]',
      )?.textContent,
    ).toContain("进行中");

    bridgeMocks.getRequestAuditContent.mockResolvedValue({
      request_id: record.id,
      http_meta: null,
      request_body: {
        media_type: "application/json",
        content: '{"input":"live"}',
        truncated: false,
        captured_bytes: 16,
      },
      response_content: null,
      upstream_http_meta: null,
      upstream_request_body: null,
      upstream_response_content: null,
    });
    await act(async () => {
      pushSelection({
        row: clientRow,
        record: {
          ...pendingRecord,
          audit: { ...pendingRecord.audit, request_body_captured: true },
        },
      });
    });
    await flush();

    expect(bridgeMocks.getRequestAuditContent).toHaveBeenCalledTimes(2);
    expect(inspector(container)?.textContent).toContain("live");
  });

  it("reports a broken audit key instead of the raw transport error", async () => {
    bridgeMocks.getRequestAuditContent.mockRejectedValue(
      new Error("control API returned 409"),
    );
    await render();

    await act(async () => {
      pushSelection({ row, record });
    });
    await flush();

    expect(container.querySelector('[role="alert"]')?.textContent).toContain(
      "审计密钥",
    );
  });
});
