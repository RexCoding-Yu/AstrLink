// @vitest-environment happy-dom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  confirmProviderImport: vi.fn(),
  dismissProviderImport: vi.fn(),
}));
vi.mock("./bridge", () => mocks);

import { ProviderImportDialog } from "./ProviderImportDialog";
import {
  planProviderImport,
  type ProviderImportLink,
  type ProviderImportPlan,
} from "./provider-import-model";
import type { Service } from "./service-model";

const id = "0123456789abcdef0123456789abcdef";
const service: Service = {
  id: "service_relay",
  name: "Relay",
  kind: "newapi",
  enabled: true,
  models: [],
  capabilities: [{ protocol: "openai.chat", mode: "native", streaming: true }],
  http: {
    base_url: "http://relay.example.com",
    auth: { scheme: "bearer" },
  },
  created_at: "2026-09-30T00:00:00Z",
  updated_at: "2026-09-30T00:00:00Z",
};
let root: Root;
let container: HTMLDivElement;

function plan(overrides: Partial<ProviderImportLink> = {}): ProviderImportPlan {
  const result = planProviderImport(
    {
      kind: "newapi",
      name: "Relay",
      base_url: "https://relay.example.com/v1",
      auth: null,
      auth_header: null,
      protocols: ["openai.chat"],
      models: [],
      has_api_key: true,
      api_key_hint: "…cdef",
      ...overrides,
    },
    [],
  );
  if (!result.ok) throw new Error(result.reason);
  return result.plan;
}

beforeEach(() => {
  (
    globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  mocks.confirmProviderImport.mockReset();
  mocks.dismissProviderImport.mockReset().mockResolvedValue(undefined);
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  vi.restoreAllMocks();
});

const dialog = () => document.querySelector<HTMLElement>('[role="dialog"]')!;
const button = (text: string) =>
  [...dialog().querySelectorAll<HTMLButtonElement>("button")].find(
    (item) => item.textContent?.trim() === text,
  )!;
const input = (selector: string) =>
  dialog().querySelector<HTMLInputElement>(selector)!;

async function type(field: HTMLInputElement, value: string) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )!.set!.call(field, value);
    field.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

async function render(
  target: ProviderImportPlan,
  handlers: { onAdded?: () => void; onClose?: () => void } = {},
) {
  await act(async () =>
    root.render(
      <ProviderImportDialog
        id={id}
        plan={target}
        onAdded={handlers.onAdded ?? (() => {})}
        onClose={handlers.onClose ?? (() => {})}
      />,
    ),
  );
}

it("shows what the link adds and creates it with the confirmed name", async () => {
  const onAdded = vi.fn();
  mocks.confirmProviderImport.mockResolvedValue({ service, etag: '"1"' });
  await render(plan(), { onAdded });

  expect(dialog().textContent).toContain("relay.example.com");
  expect(dialog().textContent).toContain("https://relay.example.com/v1");
  expect(dialog().textContent).toContain("链接已附带 API Key");
  expect(dialog().textContent).toContain("…cdef");
  expect(dialog().textContent).toContain("OpenAI Chat Completions");
  expect(dialog().textContent).toContain("链接没有指定模型");
  expect(dialog().textContent).not.toContain("未加密");
  expect(input("#provider-import-key")).toBeNull();

  await type(input("#provider-import-name"), "  Team relay ");
  await act(async () => button("添加 API 提供商").click());

  expect(mocks.confirmProviderImport).toHaveBeenCalledWith(id, {
    name: "Team relay",
    kind: "newapi",
    enabled: true,
    models: [],
    http: {
      base_url: "https://relay.example.com/v1",
      auth: { scheme: "bearer" },
    },
    capabilities: [
      { protocol: "openai.chat", mode: "native", streaming: true },
    ],
  });
  expect(onAdded).toHaveBeenCalledWith(service);
  expect(mocks.dismissProviderImport).not.toHaveBeenCalled();
});

it("warns about plain http and asks for a key the link left out", async () => {
  mocks.confirmProviderImport.mockResolvedValue({ service, etag: '"1"' });
  await render(
    plan({
      base_url: "http://relay.example.com",
      has_api_key: false,
      api_key_hint: null,
    }),
  );

  expect(dialog().textContent).toContain("未加密的 http 连接");
  expect(button("添加 API 提供商").disabled).toBe(true);
  await type(input("#provider-import-key"), "sk-typed");
  expect(button("添加 API 提供商").disabled).toBe(false);
  await act(async () => button("添加 API 提供商").click());

  expect(mocks.confirmProviderImport.mock.calls[0][1].http).toEqual({
    base_url: "http://relay.example.com",
    auth: { scheme: "bearer" },
    credential: { secret: "sk-typed" },
  });
});

it("keeps the dialog open with the reason when adding fails", async () => {
  const onAdded = vi.fn();
  mocks.confirmProviderImport.mockRejectedValue(
    new Error("service name already exists"),
  );
  await render(plan(), { onAdded });

  await act(async () => button("添加 API 提供商").click());

  expect(dialog().querySelector('[role="alert"]')?.textContent).toBe(
    "添加失败：service name already exists",
  );
  expect(onAdded).not.toHaveBeenCalled();
  expect(button("添加 API 提供商").disabled).toBe(false);
});

it("dismisses the link when cancelled", async () => {
  const onClose = vi.fn();
  await render(plan(), { onClose });

  await act(async () => button("取消").click());

  expect(mocks.dismissProviderImport).toHaveBeenCalledWith(id);
  expect(onClose).toHaveBeenCalledTimes(1);
  expect(mocks.confirmProviderImport).not.toHaveBeenCalled();
});
