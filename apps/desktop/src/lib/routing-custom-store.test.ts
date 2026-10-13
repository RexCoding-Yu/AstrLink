import { beforeEach, describe, expect, it, vi } from "vitest";

const plugin = vi.hoisted(() => ({
  load: vi.fn(),
  saves: vi.fn(),
}));

vi.mock("@tauri-apps/plugin-store", () => ({ load: plugin.load }));

/** The subset of the plugin's Store the wrapper uses, backed by a plain map. */
function fakeStore(initial: Record<string, unknown> = {}) {
  const data = new Map<string, unknown>(Object.entries(initial));
  return {
    data,
    set: vi.fn(async (key: string, value: unknown) => {
      data.set(key, value);
    }),
    get: vi.fn(async (key: string) => data.get(key)),
    save: vi.fn(async () => {
      plugin.saves();
    }),
  };
}

/** Each test gets a fresh module, since the wrapper caches its store and map. */
async function wrapper() {
  vi.resetModules();
  return await import("./routing-custom-store");
}

beforeEach(() => {
  plugin.load.mockReset();
  plugin.saves.mockReset();
});

describe("routing custom values store", () => {
  it("merges new values, trims them and ignores blanks and duplicates", async () => {
    const store = fakeStore({
      byField: {
        model: ["gpt-5", "  gpt-5  ", "", "o3"],
        "": ["x"],
        "not a field": ["y"],
      },
    });
    plugin.load.mockResolvedValue(store);
    const { loadRoutingCustomValues, rememberRoutingCustomValue } =
      await wrapper();

    expect(await loadRoutingCustomValues()).toEqual({ model: ["gpt-5", "o3"] });

    await rememberRoutingCustomValue("model", "  sonnet  ");
    await rememberRoutingCustomValue("model", "sonnet");
    await rememberRoutingCustomValue("model", "   ");
    await rememberRoutingCustomValue("provider", "anthropic");

    expect(await loadRoutingCustomValues()).toEqual({
      model: ["gpt-5", "o3", "sonnet"],
      provider: ["anthropic"],
    });
    expect(store.data.get("byField")).toEqual({
      model: ["gpt-5", "o3", "sonnet"],
      provider: ["anthropic"],
    });
  });

  it("keeps creation order and caps each field at 64 values, oldest first", async () => {
    const store = fakeStore();
    plugin.load.mockResolvedValue(store);
    const { loadRoutingCustomValues, rememberRoutingCustomValue } =
      await wrapper();

    for (let index = 0; index < 70; index += 1) {
      await rememberRoutingCustomValue("model", `m${index}`);
    }

    const map = await loadRoutingCustomValues();
    expect(map.model).toHaveLength(64);
    expect(map.model?.[0]).toBe("m6");
    expect(map.model?.[63]).toBe("m69");
  });

  it("forgets one value and drops the field once it is empty", async () => {
    const store = fakeStore({
      byField: { model: ["a", "b"], provider: ["x"] },
    });
    plugin.load.mockResolvedValue(store);
    const { loadRoutingCustomValues, forgetRoutingCustomValue } =
      await wrapper();

    await forgetRoutingCustomValue("model", "a");
    await forgetRoutingCustomValue("provider", "x");
    await forgetRoutingCustomValue("missing", "nope");

    expect(await loadRoutingCustomValues()).toEqual({ model: ["b"] });
    expect(store.data.get("byField")).toEqual({ model: ["b"] });
  });

  it("lands both of two rapid concurrent writes", async () => {
    const store = fakeStore();
    plugin.load.mockResolvedValue(store);
    const { loadRoutingCustomValues, rememberRoutingCustomValue } =
      await wrapper();

    await Promise.all([
      rememberRoutingCustomValue("model", "first"),
      rememberRoutingCustomValue("model", "second"),
    ]);

    expect(await loadRoutingCustomValues()).toEqual({
      model: ["first", "second"],
    });
    expect(store.data.get("byField")).toEqual({ model: ["first", "second"] });
  });

  it("rejects field names that are not routing identifiers", async () => {
    const store = fakeStore();
    plugin.load.mockResolvedValue(store);
    const { loadRoutingCustomValues, rememberRoutingCustomValue } =
      await wrapper();

    await rememberRoutingCustomValue("Model Name", "a");
    await rememberRoutingCustomValue("__proto__", "b");
    await rememberRoutingCustomValue("constructor", "c");
    await rememberRoutingCustomValue("model", "kept");

    expect(await loadRoutingCustomValues()).toEqual({ model: ["kept"] });
  });

  it("resolves to {} and writes without effect when the runtime is absent", async () => {
    plugin.load.mockRejectedValue(new Error("no tauri host"));
    const {
      loadRoutingCustomValues,
      rememberRoutingCustomValue,
      forgetRoutingCustomValue,
    } = await wrapper();

    await expect(loadRoutingCustomValues()).resolves.toEqual({});
    await expect(
      rememberRoutingCustomValue("model", "gpt-5"),
    ).resolves.toBeUndefined();
    await expect(
      forgetRoutingCustomValue("model", "gpt-5"),
    ).resolves.toBeUndefined();
    // The second read comes from the cache, which never reached a store.
    expect(await loadRoutingCustomValues()).toEqual({});
    expect(plugin.saves).not.toHaveBeenCalled();
  });

  it("serves repeat reads from the cache without reopening the store", async () => {
    const store = fakeStore({ byField: { model: ["a"] } });
    plugin.load.mockResolvedValue(store);
    const { loadRoutingCustomValues } = await wrapper();

    await loadRoutingCustomValues();
    await loadRoutingCustomValues();

    expect(plugin.load).toHaveBeenCalledOnce();
  });

  it("keeps the cache consistent with writes and survives a failing save", async () => {
    const store = fakeStore();
    store.save.mockRejectedValue(new Error("disk full"));
    plugin.load.mockResolvedValue(store);
    const { loadRoutingCustomValues, rememberRoutingCustomValue } =
      await wrapper();

    await expect(
      rememberRoutingCustomValue("model", "a"),
    ).resolves.toBeUndefined();
    expect(await loadRoutingCustomValues()).toEqual({ model: ["a"] });
  });
});
