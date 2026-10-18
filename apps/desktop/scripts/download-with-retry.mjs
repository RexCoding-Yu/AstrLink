export const downloadRetryDelaysMs = [2_000, 4_000];

// Callers must still verify the returned bytes against their pinned digest.
export async function downloadWithRetry(
  url,
  description,
  { fetchAsset = fetch, retryDelaysMs = downloadRetryDelaysMs } = {},
) {
  for (let attempt = 0; ; attempt += 1) {
    let failure;
    let cause;
    let retryable = true;
    try {
      const response = await fetchAsset(url, { redirect: "follow" });
      if (response.ok) return Buffer.from(await response.arrayBuffer());
      failure = `HTTP ${response.status}`;
      retryable = response.status === 429 || response.status >= 500;
    } catch (error) {
      cause = error;
      failure = error instanceof Error ? error.message : String(error);
    }
    const delay = retryDelaysMs[attempt];
    if (!retryable || delay === undefined) {
      throw new Error(`Unable to download ${description}: ${failure}`, {
        cause,
      });
    }
    console.warn(
      `Download of ${description} failed (${failure}); retrying in ${delay} ms.`,
    );
    await new Promise((resolve) => setTimeout(resolve, delay));
  }
}
