import { useEffect, useState } from "react";

import { getRawSealingStatus, listenRawSealingChanged } from "./bridge";
import type { RawSealingState } from "./raw-sealing-model";
import { rawSealingErrorMessage } from "./raw-sealing-ui";

/** Retry for a sealing read that failed, so a missing password still shows. */
export const RAW_STATUS_RETRY_MS = 15_000;

/**
 * The raw sealing state for this Core session, read again whenever any
 * window unlocks, locks, or changes the raw password. `null` until the first
 * read lands and after a session change; a failed read keeps the last state,
 * reports `error`, and retries.
 */
export function useRawSealingStatus(
  coreSessionKey: string | null,
  isReady: boolean,
): {
  status: RawSealingState | null;
  error: string | null;
  setStatus: (next: RawSealingState) => void;
} {
  const [status, setStatus] = useState<RawSealingState | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setStatus(null);
    setError(null);
    if (!isReady || coreSessionKey === null) return;
    let active = true;
    let timer: number | undefined;
    // Reads can overlap (an event beside a retry); the latest wins.
    let latest = 0;

    const read = () => {
      window.clearTimeout(timer);
      timer = undefined;
      const current = ++latest;
      getRawSealingStatus().then(
        (next) => {
          if (!active || current !== latest) return;
          setStatus(next);
          setError(null);
        },
        (reason: unknown) => {
          if (!active || current !== latest) return;
          setError(rawSealingErrorMessage(reason));
          timer = window.setTimeout(read, RAW_STATUS_RETRY_MS);
        },
      );
    };

    let stop: (() => void) | null = null;
    listenRawSealingChanged(read).then(
      (unlisten) => {
        if (active) stop = unlisten;
        else unlisten();
      },
      (error: unknown) => {
        console.error("AstrLink cannot watch the raw sealing state", error);
      },
    );
    read();
    return () => {
      active = false;
      window.clearTimeout(timer);
      stop?.();
    };
  }, [coreSessionKey, isReady]);

  return {
    status,
    error,
    setStatus: (next) => {
      setStatus(next);
      setError(null);
    },
  };
}
