import { PluginView } from "@/components/PluginView";
import { StatusBadge } from "@/components/StatusBadge";
import type { StatusTone } from "@/components/StatusDot";
import { useT } from "./i18n";
import type {
  MetadataEvent,
  MetadataValue,
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
  "server_unavailable",
  "timeout",
  "plugin_error",
  "privacy_blocked",
  "privacy_unavailable",
]);

const knownObserve = new Set([
  "queued",
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
  if (records.length === 0) {
    return (
      <p className="text-xs leading-6 text-muted-foreground">
        {t("pluginRecords.none")}
      </p>
    );
  }
  return (
    <div className="grid min-w-0 gap-4" data-testid="plugin-records">
      {records.map((entry) => (
        <section
          className="grid min-w-0 gap-2"
          data-testid="plugin-record"
          key={`${entry.plugin}:${entry.hook}`}
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
        </section>
      ))}
    </div>
  );
}

function valueSource(value: MetadataValue, t: ReturnType<typeof useT>): string {
  const source = t(`pluginRecords.metadataSources.${value.source}`);
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
  return `${event.key}: ${t(`pluginRecords.metadataEvents.${event.event}`)}${value}`;
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
