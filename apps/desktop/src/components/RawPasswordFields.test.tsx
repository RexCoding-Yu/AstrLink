// @vitest-environment happy-dom

import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { RawPasswordFields } from "./RawPasswordFields";

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  (
    globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
});

function Harness() {
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  return (
    <RawPasswordFields
      confirmation={confirmation}
      onConfirmationChange={setConfirmation}
      onPasswordChange={setPassword}
      password={password}
      policy={{ password_min_length: 8, password_max_length: 20 }}
    />
  );
}

function inputs(): HTMLInputElement[] {
  return [...container.querySelectorAll<HTMLInputElement>("input")];
}

function hint(): HTMLElement {
  const match = container.querySelector<HTMLElement>(
    '[data-slot="raw-password-hint"]',
  );
  if (!match) throw new Error("Missing password hint");
  return match;
}

async function type(input: HTMLInputElement, value: string) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

describe("RawPasswordFields", () => {
  it("states the length rule and suggests a longer passphrase", async () => {
    await act(async () => root.render(<Harness />));
    const [password, confirmation] = inputs();

    expect(password.type).toBe("password");
    expect(password.getAttribute("autocomplete")).toBe("new-password");
    expect(hint().textContent).toBe("8–20 个字符。");

    await type(password, "short pw");
    expect(hint().textContent).toBe("8–20 个字符。 建议 12 位以上或一句短语。");
    expect(password.getAttribute("aria-invalid")).toBeNull();

    await type(password, "a longer phrase");
    expect(hint().textContent).toBe("8–20 个字符。");

    await type(confirmation, "a longer");
    expect(confirmation.getAttribute("aria-invalid")).toBeNull();
    await type(confirmation, "a longer phrasf");
    expect(hint().textContent).toBe("两次输入的口令不一致。");
    expect(confirmation.getAttribute("aria-invalid")).toBe("true");

    await type(confirmation, "a longer phrase");
    expect(hint().textContent).toBe("8–20 个字符。");
  });

  it("flags a password over the maximum", async () => {
    await act(async () => root.render(<Harness />));
    const [password] = inputs();

    await type(password, "x".repeat(21));
    expect(hint().textContent).toBe("口令不能超过 20 个字符。");
    expect(password.getAttribute("aria-invalid")).toBe("true");
  });
});
