import { useCallback, useEffect, useState } from "react";

import { EmptyState } from "@/components/EmptyState";
import { FormMessage } from "@/components/FormMessage";
import { LoadingState } from "@/components/LoadingState";
import { PluginIcon } from "@/components/PluginIcon";
import { Button } from "@/components/ui/button";

import {
  decidePluginApproval,
  getPlugin,
  listenPluginApprovalChanged,
  pendingPluginApproval,
} from "./bridge";
import { useT } from "./i18n";
import { PluginApprovalSummary } from "./PluginApprovalSummary";
import type { PendingPluginApproval, PluginDetail } from "./plugin-model";

/**
 * The one place a change that grants a plugin more is confirmed (plugin
 * platform v2, §2.4): enabling it, capabilities, addresses, plain HTTP, the
 * privacy check skipped. The host opens this window without focus when the
 * main window asks for such a change; plugin interface code runs in the
 * main window and can click nothing here. There is no form, so Enter
 * confirms nothing; closing the window decides nothing.
 */
export function PluginApprovalWindow() {
  const t = useT();
  const [pending, setPending] = useState<PendingPluginApproval | null>(null);
  const [detail, setDetail] = useState<PluginDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [kept, setKept] = useState<string[]>([]);
  const [deciding, setDeciding] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const next = await pendingPluginApproval();
      setPending(next);
      setKept(next?.approval.added_capabilities ?? []);
      setError(null);
      setDetail(null);
      if (next) {
        void getPlugin(next.key)
          .then(setDetail)
          .catch(() => setDetail(null));
      }
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
    let unlisten: (() => void) | undefined;
    let stopped = false;
    void listenPluginApprovalChanged(() => void load()).then((stop) => {
      if (stopped) stop();
      else unlisten = stop;
    });
    return () => {
      stopped = true;
      unlisten?.();
    };
  }, [load]);

  async function decide(approve: boolean) {
    if (!pending) return;
    setDeciding(true);
    setError(null);
    try {
      await decidePluginApproval(
        pending.id,
        approve,
        approve ? kept : undefined,
      );
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
      setDeciding(false);
    }
  }

  if (loading)
    return (
      <LoadingState className="flex-1" label={t("plugins.approval.loading")} />
    );
  if (!pending)
    return (
      <main
        className="flex flex-1 flex-col p-4"
        data-testid="plugin-approval-window"
      >
        <EmptyState title={t("plugins.approval.nothing")} />
        {error ? <FormMessage tone="error">{error}</FormMessage> : null}
      </main>
    );
  const { approval } = pending;
  return (
    <main
      className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto p-4"
      data-testid="plugin-approval-window"
    >
      <header className="flex items-center gap-2">
        <PluginIcon icon={detail?.icon} />
        <h1 className="text-sm font-semibold">
          {approval.enable
            ? t("plugins.approval.enableTitle", { name: approval.name })
            : t("plugins.approval.grantTitle", { name: approval.name })}
        </h1>
      </header>
      <PluginApprovalSummary
        approval={approval}
        detail={detail}
        kept={kept}
        onKept={setKept}
      />
      {error ? <FormMessage tone="error">{error}</FormMessage> : null}
      <footer className="mt-auto flex justify-end gap-2">
        <Button
          disabled={deciding}
          onClick={() => void decide(false)}
          size="sm"
          type="button"
          variant="outline"
        >
          {t("plugins.approval.deny")}
        </Button>
        <Button
          disabled={deciding}
          onClick={() => void decide(true)}
          size="sm"
          type="button"
        >
          {approval.enable
            ? t("plugins.approval.confirmEnable")
            : t("plugins.approval.confirm")}
        </Button>
      </footer>
    </main>
  );
}
