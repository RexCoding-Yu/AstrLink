import { afterEach, describe, expect, it, vi } from "vitest";
import { downloadWithRetry } from "./download-with-retry.mjs";

const url = "https://example.invalid/onnxruntime.tgz";
const bytes = Buffer.from("synthetic runtime archive");

function options(...responses: Array<Response | Error>) {
  const fetchAsset = vi.fn(async () => {
    const next = responses.shift();
    if (next instanceof Error) throw next;
    return next ?? new Response(bytes);
  });
  return { fetchAsset, retryDelaysMs: [0, 0] };
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("pinned download retries", () => {
  it("retries gateway errors, rate limits, and dropped connections", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const config = options(
      new Response(null, { status: 504 }),
      new TypeError("socket hang up"),
    );
    await expect(downloadWithRetry(url, "runtime", config)).resolves.toEqual(
      bytes,
    );
    expect(config.fetchAsset).toHaveBeenCalledTimes(3);
    expect(warn).toHaveBeenCalledTimes(2);

    const limited = options(new Response(null, { status: 429 }));
    await expect(downloadWithRetry(url, "runtime", limited)).resolves.toEqual(
      bytes,
    );
    expect(limited.fetchAsset).toHaveBeenCalledTimes(2);
  });

  it("retries a body that fails mid-transfer", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    const truncated = {
      ok: true,
      arrayBuffer: () => Promise.reject(new TypeError("terminated")),
    } as unknown as Response;
    const config = options(truncated);
    await expect(downloadWithRetry(url, "runtime", config)).resolves.toEqual(
      bytes,
    );
    expect(config.fetchAsset).toHaveBeenCalledTimes(2);
  });

  it("reports the last failure once every retry is spent", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    const config = options(
      new Response(null, { status: 502 }),
      new Response(null, { status: 503 }),
      new Response(null, { status: 504 }),
    );
    await expect(downloadWithRetry(url, "runtime", config)).rejects.toThrow(
      "Unable to download runtime: HTTP 504",
    );
    expect(config.fetchAsset).toHaveBeenCalledTimes(3);
  });

  it("fails at once on responses a retry cannot fix", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    for (const status of [403, 404]) {
      const config = options(new Response(null, { status }));
      await expect(downloadWithRetry(url, "runtime", config)).rejects.toThrow(
        `HTTP ${status}`,
      );
      expect(config.fetchAsset).toHaveBeenCalledTimes(1);
    }
    expect(warn).not.toHaveBeenCalled();
  });
});
