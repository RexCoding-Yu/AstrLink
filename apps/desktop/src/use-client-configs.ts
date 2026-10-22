import { useCallback, useEffect, useRef, useState } from "react";

import { getClientConfigStatus } from "./bridge";
import type { ClientConfigStatus, DirectClient } from "./client-config-model";

/**
 * What AstrLink wrote to the local clients, read again whenever the window
 * comes back: clients and CC Switch rewrite their configs outside AstrLink.
 * The marks it feeds only annotate lists, so a failed read hides them.
 */
export function useClientConfigs(inferenceURL: string): {
  configs: ClientConfigStatus[];
  refresh: () => void;
} {
  const [configs, setConfigs] = useState<ClientConfigStatus[]>([]);
  const generation = useRef(0);
  const refresh = useCallback(() => {
    const current = ++generation.current;
    getClientConfigStatus(inferenceURL || null).then(
      (overview) => {
        if (generation.current === current) setConfigs(overview.clients);
      },
      () => {
        if (generation.current === current) setConfigs([]);
      },
    );
  }, [inferenceURL]);

  useEffect(() => {
    refresh();
    window.addEventListener("focus", refresh);
    return () => {
      generation.current += 1;
      window.removeEventListener("focus", refresh);
    };
  }, [refresh]);

  return { configs, refresh };
}

/** A client may hold the token on this computer and in WSL distributions. */
export function configsUsing(
  configs: readonly ClientConfigStatus[],
  tokenId: string | undefined,
): ClientConfigStatus[] {
  return configs.filter(
    (status) =>
      status.token_id === tokenId && status.state !== "not_configured",
  );
}

export function clientsUsing(
  configs: readonly ClientConfigStatus[],
  tokenId: string | undefined,
): DirectClient[] {
  return [
    ...new Set(configsUsing(configs, tokenId).map((status) => status.client)),
  ];
}
