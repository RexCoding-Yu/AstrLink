import { useState } from "react";

import { ConfirmDialog } from "@/components/ConfirmDialog";
import { PluginView } from "@/components/PluginView";
import { StatusBadge } from "@/components/StatusBadge";
import type { StatusTone } from "@/components/StatusDot";
import { Button } from "@/components/ui/button";
import { runPluginAction } from "./bridge";
import { controlErrorMessage } from "./control-error";
import { useT } from "./i18n";
import { notify } from "./notify";
import type {
  MetadataEvent,
  MetadataValue,
  PluginRecordAction,
  RequestMetadata,
  RequestPluginRecord,
} from "./plugin-model";
import type { RequestRecord } from "./request-record-model";

const statusTones: Record<RequestPluginRecord["status"], StatusTone> = {
  ok: "positive",
  skipped: "neutral",
  failed: "negative",
};

const knownSkipReasons = new Set([
  "no_project",
  "short_query",
  "no_hits",
  "search_failed",
  "server_unavailable",
  "timeout",
  "plugin_error",
  "privacy_blocked",
  "privacy_unavailable",
  "paused_all",
  "client_disabled",
  "session_paused",
  "over_budget",
  "invalid_result",
  "no_system_channel",
  "no_opening_message",
  "injection_failed",
]);

const knownTriggers = new Set([
  "start",
  "bound",
  "config",
  "compact",
  "manual",
  "auto",
]);

const knownObserve = new Set([
  "queued",
  "awaiting_reply",
  "skipped_unbound",
  "skipped_privacy",
  "skipped_binding_turn",
  "skipped_continuation",
  "queue_full",
  "disabled",
]);

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  return `${(bytes / 1024).toFixed(1)} KiB`;
}

/**
 * The plugins' parts in one request: what each plugin reported, its view,
 * and what the host did for it. The inspector's plugin tab shows them all,
 * grouped by plugin.
 */
export function PluginRecordDetails({ record }: { record: RequestRecord }) {
  const t = useT();
  const records = record.plugin_records ?? [];
  const [confirming, setConfirming] = useState<{
    plugin: string;
    action: PluginRecordAction;
  } | null>(null);
  const [running, setRunning] = useState(false);

  async function run(plugin: string, action: PluginRecordAction) {
    if (!record.session_id) return;
    setRunning(true);
    try {
      const result = await runPluginAction(
        plugin,
        action.id,
        record.session_id,
      );
      const why = result.message || result.reason;
      if (result.status === "ok")
        notify.success(result.message || t("pluginRecords.actionDone"));
      else
        notify.error(
          why
            ? t("pluginRecords.actionFailed", { reason: why })
            : t("pluginRecords.actionFailedPlain"),
        );
    } catch (reason) {
      notify.error(
        controlErrorMessage(reason, t("pluginRecords.actionFailedPlain")),
      );
    } finally {
      setRunning(false);
    }
  }

  if (records.length === 0) {
    return (
      <p className="text-xs leading-6 text-muted-foreground">
        {t("pluginRecords.none")}
      </p>
    );
  }
  return (
    <div className="grid min-w-0 gap-4" data-testid="plugin-records">
      {records.map((entry, index) => (
        <section
          className="grid min-w-0 gap-2"
          data-testid="plugin-record"
          key={`${entry.plugin}:${entry.hook}:${index}`}
        >
          <header className="flex min-w-0 flex-wrap items-center gap-2">
            <strong className="truncate text-xs font-medium">
              {entry.title}
            </strong>
            <StatusBadge tone={statusTones[entry.status]}>
              {t(`pluginRecords.status.${entry.status}`)}
            </StatusBadge>
            <span className="truncate font-mono text-micro text-muted-foreground">
              {entry.plugin} {entry.plugin_version} · {entry.hook}
            </span>
          </header>
          {entry.summary ? <p className="text-xs">{entry.summary}</p> : null}
          <dl className="grid min-w-0 grid-cols-[minmax(5rem,auto)_minmax(0,1fr)] gap-x-4 gap-y-1 text-xs">
            <dt className="text-muted-foreground">
              {t("pluginRecords.elapsed")}
            </dt>
            <dd className="tabular-nums">{entry.elapsed_ms} ms</dd>
            {entry.trigger ? (
              <>
                <dt className="text-muted-foreground">
                  {t("pluginRecords.trigger")}
                </dt>
                <dd>
                  {knownTriggers.has(entry.trigger)
                    ? t(`pluginRecords.triggers.${entry.trigger}`)
                    : entry.trigger}
                </dd>
              </>
            ) : null}
            {entry.skipped_reason ? (
              <>
                <dt className="text-muted-foreground">
                  {t("pluginRecords.skipped")}
                </dt>
                <dd>
                  {knownSkipReasons.has(entry.skipped_reason)
                    ? t(`pluginRecords.skipReasons.${entry.skipped_reason}`)
                    : entry.skipped_reason}
                </dd>
              </>
            ) : null}
            {entry.injection ? (
              <>
                <dt className="text-muted-foreground">
                  {t("pluginRecords.injection")}
                </dt>
                <dd>
                  {t(`pluginRecords.placement.${entry.injection.placement}`)}
                  {" · "}
                  {formatBytes(entry.injection.injected_bytes)}
                  {entry.injection.replayed_blocks > 0
                    ? ` · ${t("pluginRecords.replayed", { count: entry.injection.replayed_blocks })}`
                    : ""}
                  {entry.injection.dropped_items > 0
                    ? ` · ${t("pluginRecords.dropped", { count: entry.injection.dropped_items })}`
                    : ""}
                </dd>
              </>
            ) : null}
            {entry.network.requests > 0 ? (
              <>
                <dt className="text-muted-foreground">
                  {t("pluginRecords.network")}
                </dt>
                <dd className="tabular-nums">
                  {t("pluginRecords.networkCount", {
                    requests: entry.network.requests,
                    denied: entry.network.denied,
                    errors: entry.network.errors,
                    wait: entry.network.wait_ms,
                  })}
                </dd>
              </>
            ) : null}
            {entry.observe ? (
              <>
                <dt className="text-muted-foreground">
                  {t("pluginRecords.observe")}
                </dt>
                <dd>
                  {knownObserve.has(entry.observe)
                    ? t(`pluginRecords.observeStates.${entry.observe}`)
                    : entry.observe}
                </dd>
              </>
            ) : null}
          </dl>
          {entry.view && entry.view.length > 0 ? (
            <PluginView
              blocks={entry.view}
              truncated={entry.truncated}
              truncatedLabel={t("pluginRecords.truncated")}
            />
          ) : null}
          {entry.actions && entry.actions.length > 0 && record.session_id ? (
            <div className="flex flex-wrap gap-2">
              {entry.actions.map((action) => (
                <Button
                  className="h-7 text-xs"
                  data-testid="plugin-record-action"
                  disabled={running}
                  key={action.id}
                  onClick={() => {
                    if (action.confirm)
                      setConfirming({ plugin: entry.plugin, action });
                    else void run(entry.plugin, action);
                  }}
                  size="sm"
                  type="button"
                  variant="outline"
                >
                  {action.label}
                </Button>
              ))}
            </div>
          ) : null}
        </section>
      ))}
      <ConfirmDialog
        confirmLabel={confirming?.action.label ?? ""}
        description={<p>{confirming?.action.confirm}</p>}
        disabled={running}
        onCancel={() => setConfirming(null)}
        onConfirm={() => {
          const target = confirming;
          setConfirming(null);
          if (target) void run(target.plugin, target.action);
        }}
        open={confirming !== null}
        title={confirming?.action.label ?? ""}
      />
    </div>
  );
}

function valueSource(value: MetadataValue, t: ReturnType<typeof useT>): string {
  const source =
    value.source === "session" && value.set_by
      ? t(`pluginRecords.setBy.${value.set_by}`, {
          defaultValue: t("pluginRecords.metadataSources.session"),
        })
      : t(`pluginRecords.metadataSources.${value.source}`);
  return value.turn
    ? t("pluginRecords.metadataTurn", { source, turn: value.turn })
    : source;
}

function eventText(event: MetadataEvent, t: ReturnType<typeof useT>): string {
  const value = event.value ? ` ${event.value}` : "";
  if (event.event === "ignored") {
    return t("pluginRecords.metadataIgnored", {
      key: event.key,
      value: event.value ?? "",
      reason: event.reason
        ? t(`pluginRecords.ignoredReasons.${event.reason}`, {
            defaultValue: event.reason,
          })
        : "",
    });
  }
  const notes = [
    event.set_by
      ? t(`pluginRecords.setBy.${event.set_by}`, { defaultValue: event.set_by })
      : "",
    event.new ? t("pluginRecords.metadataNew") : "",
    event.normalized
      ? t("pluginRecords.metadataNormalized", { value: event.normalized })
      : "",
  ].filter(Boolean);
  const suffix = notes.length > 0 ? ` (${notes.join(" · ")})` : "";
  return `${event.key}: ${t(`pluginRecords.metadataEvents.${event.event}`)}${value}${suffix}`;
}

/**
 * A request's metadata in one line: each value with where it came from, then
 * what the request changed or tried to.
 */
export function RequestMetadataLine({
  metadata,
}: {
  metadata: RequestMetadata | undefined;
}) {
  const t = useT();
  if (!metadata) return null;
  const values = Object.entries(metadata.values);
  if (values.length === 0 && metadata.events.length === 0) return null;
  return (
    <div
      className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-micro text-muted-foreground"
      data-testid="request-metadata"
    >
      <span className="font-medium">{t("pluginRecords.metadata")}</span>
      {values.map(([key, value]) => (
        <span className="min-w-0" key={key}>
          <span className="font-mono text-foreground">
            {key}={value.value}
          </span>{" "}
          ({valueSource(value, t)})
        </span>
      ))}
      {metadata.events.map((event, index) => (
        <span className="min-w-0 text-warning-foreground" key={index}>
          {eventText(event, t)}
        </span>
      ))}
    </div>
  );
}
