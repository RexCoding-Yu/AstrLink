import { useCallback, useEffect, useState } from "react";
import { getAppUpdateStatus, listenAppUpdates } from "./update-bridge";
import { browserUpdateSnapshot, type UpdateSnapshot } from "./update-model";

/** Mounted at the shell so navigation never owns the download or notification. */
export function useAppUpdates() {
  const [snapshot, setSnapshot] = useState(browserUpdateSnapshot);
  const [error, setError] = useState<string | null>(null);
  const accept = useCallback((next: UpdateSnapshot) => {
    setSnapshot((current) =>
      next.revision >= current.revision ? next : current,
    );
    setError(null);
  }, []);
  useEffect(() => {
    let cancelled = false;
    let unlisten: (() => void) | undefined;
    // Subscribe before reading to close the initial snapshot/event race.
    void listenAppUpdates((next) => {
      if (!cancelled) accept(next);
    })
      .then(async (stop) => {
        if (cancelled) {
          stop();
          return;
        }
        unlisten = stop;
        const next = await getAppUpdateStatus();
        if (!cancelled) accept(next);
      })
      .catch((error) => {
        if (!cancelled) setError(String(error));
      });
    return () => {
      cancelled = true;
      unlisten?.();
    };
  }, [accept]);
  return { snapshot, accept, error };
}
