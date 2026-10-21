import { useEffect, useState } from "react";
import { useWorkspaceSnapshot } from "./workspace-snapshots";
import { getBillingSummary } from "./pricing-bridge";
import type { BillingSummary } from "./pricing-model";

export type BillingStatus = "idle" | "loading" | "ready" | "error";

/** Loads the range total; a retained summary stays visible while it refreshes. */
export function useBillingSummary({
  from,
  to,
  ready,
  revision,
}: {
  from?: string;
  to?: string;
  ready: boolean;
  revision: unknown;
}): { status: BillingStatus; summary: BillingSummary | null } {
  const [summary, setSummary] = useWorkspaceSnapshot<BillingSummary | null>(
    `billing-summary:${from ?? ""}:${to ?? ""}`,
    null,
  );
  const [status, setStatus] = useState<BillingStatus>("idle");
  useEffect(() => {
    let cancelled = false;
    if (!ready || !from || !to) {
      setSummary(null);
      setStatus("idle");
    } else {
      setStatus("loading");
      void getBillingSummary(from, to)
        .then((next) => {
          if (cancelled) return;
          setSummary(next);
          setStatus("ready");
        })
        .catch(() => {
          if (cancelled) return;
          setSummary(null);
          setStatus("error");
        });
    }
    return () => {
      cancelled = true;
    };
  }, [from, to, ready, revision, setSummary]);
  return { status: summary ? "ready" : status, summary };
}
