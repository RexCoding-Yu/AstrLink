import { HelpPopover } from "@/components/HelpPopover";
import { Panel, PanelHeader } from "@/components/Panel";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useT } from "./i18n";
import type {
  PluginCacheShare,
  PluginDayStats,
  PluginDetail,
  PluginStats,
} from "./plugin-model";

type Translate = ReturnType<typeof useT>;

/** The reasons a block is computed again in a session that has one. */
const REFRESH_TRIGGERS = ["compact", "manual", "auto"];

/** About 3 bytes of injected text make a token, Chinese or English. */
const BYTES_PER_TOKEN = 3;

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

function percent(part: number, whole: number): string | null {
  return whole > 0 ? `${Math.round((100 * part) / whole)}%` : null;
}

function cacheRate(share: PluginCacheShare): string | null {
  return percent(share.cache_read_tokens, share.input_tokens);
}

/** Which parts of the account apply to the plugin. */
function partsOf(detail: PluginDetail) {
  const injects = ["prepareSession", "prepareTurn"].some((hook) =>
    detail.hooks.includes(hook),
  );
  return {
    load: injects,
    lookup: detail.tools.length > 0,
    binding: detail.metadata.length > 0,
    write: detail.hooks.includes("observeTurn"),
    cache: injects,
  };
}

/** Whether a day or the whole has anything to show. */
function active(day: PluginDayStats): boolean {
  const binding = Object.values(day.binding).some((count) => count > 0);
  const problems = Object.values(day.problems).some((count) => count > 0);
  return (
    day.load.requests > 0 ||
    day.load.count > 0 ||
    day.lookup.ran + day.lookup.refused > 0 ||
    binding ||
    day.write.ok + day.write.failed > 0 ||
    problems
  );
}

function loadText(day: PluginDayStats, t: Translate): string {
  const load = day.load;
  const refreshes = REFRESH_TRIGGERS.reduce(
    (sum, trigger) => sum + (load.by_trigger[trigger] ?? 0),
    0,
  );
  return [
    t("plugins.effects.loadValue", {
      count: load.count,
      size: formatSize(load.injected_bytes),
      tokens: Math.round(
        load.injected_bytes / BYTES_PER_TOKEN,
      ).toLocaleString(),
    }),
    refreshes > 0 ? t("plugins.effects.refreshes", { count: refreshes }) : "",
    load.wait_ms
      ? t("plugins.effects.wait", {
          p50: load.wait_ms.p50,
          p95: load.wait_ms.p95,
        })
      : "",
  ]
    .filter(Boolean)
    .join(" · ");
}

function lookupText(day: PluginDayStats, t: Translate): string {
  const lookup = day.lookup;
  if (lookup.ran + lookup.refused === 0) return t("plugins.effects.noLookups");
  const hits = percent(lookup.with_hits, lookup.counted);
  return [
    t("plugins.effects.lookupValue", { count: lookup.ran }),
    hits ? t("plugins.effects.hitRate", { rate: hits }) : "",
    lookup.sessions > 0
      ? t("plugins.effects.lookupSessions", {
          lookingUp: lookup.looking_up,
          sessions: lookup.sessions,
        })
      : "",
    lookup.ms ? t("plugins.effects.lookupMs", { p50: lookup.ms.p50 }) : "",
    lookup.refused > 0
      ? t("plugins.effects.refused", { count: lookup.refused })
      : "",
  ]
    .filter(Boolean)
    .join(" · ");
}

function bindingText(day: PluginDayStats, t: Translate): string {
  const binding = day.binding;
  return [
    t("plugins.effects.bindingValue", binding),
    binding.changed > 0
      ? t("plugins.effects.changed", { count: binding.changed })
      : "",
  ]
    .filter(Boolean)
    .join(" · ");
}

function cacheText(day: PluginDayStats, t: Translate): string {
  return t("plugins.effects.cacheValue", {
    with: cacheRate(day.cache.with) ?? "—",
    without: cacheRate(day.cache.without) ?? "—",
  });
}

/**
 * The plugin's account over the last 7 days on the overview: what its blocks
 * cost, how the model used its lookups, how sessions got their values, what
 * it wrote, what went wrong and how the prompt cache fared.
 */
export function EffectCard({
  detail,
  stats,
}: {
  detail: PluginDetail;
  stats: PluginStats;
}) {
  const t = useT();
  const total = stats.total;
  const parts = partsOf(detail);
  const rows: Array<[string, string]> = [];
  if (parts.load) rows.push([t("plugins.effects.load"), loadText(total, t)]);
  if (parts.lookup)
    rows.push([t("plugins.effects.lookup"), lookupText(total, t)]);
  if (parts.binding)
    rows.push([t("plugins.effects.binding"), bindingText(total, t)]);
  if (parts.write)
    rows.push([
      t("plugins.effects.write"),
      t("plugins.effects.writeValue", total.write),
    ]);
  rows.push([
    t("plugins.effects.problems"),
    t("plugins.effects.problemsValue", {
      ...total.problems,
      overBudget: total.problems.over_budget,
    }),
  ]);
  if (parts.cache) rows.push([t("plugins.effects.cache"), cacheText(total, t)]);
  return (
    <Panel data-testid="plugin-effects">
      <PanelHeader
        actions={
          <HelpPopover label={t("plugins.effects.helpLabel")}>
            <p>{t("plugins.effects.help")}</p>
          </HelpPopover>
        }
        size="sm"
      >
        <h3 className="text-xs font-semibold">{t("plugins.effects.title")}</h3>
      </PanelHeader>
      {active(total) ? (
        <dl className="grid grid-cols-[5.5rem_minmax(0,1fr)] gap-x-4 gap-y-2 p-3 text-xs">
          {rows.map(([label, value]) => (
            <div className="contents" key={label}>
              <dt className="text-muted-foreground">{label}</dt>
              <dd className="min-w-0 break-words tabular-nums">{value}</dd>
            </div>
          ))}
        </dl>
      ) : (
        <p className="p-3 text-xs text-muted-foreground">
          {t("plugins.effects.empty")}
        </p>
      )}
    </Panel>
  );
}

/** The same account day by day on the status page, today first. */
export function DailyEffects({
  detail,
  stats,
}: {
  detail: PluginDetail;
  stats: PluginStats;
}) {
  const t = useT();
  const parts = partsOf(detail);
  const days = [...stats.days].reverse();
  return (
    <section className="grid gap-1.5" data-testid="plugin-daily-effects">
      <div className="flex items-center gap-1">
        <h3 className="text-xs font-medium">{t("plugins.effects.daily")}</h3>
        <HelpPopover label={t("plugins.effects.dailyHelpLabel")}>
          <p>{t("plugins.effects.dailyHelp")}</p>
        </HelpPopover>
      </div>
      <Table className="text-xs">
        <TableHeader>
          <TableRow>
            <TableHead>{t("plugins.effects.columns.date")}</TableHead>
            {parts.load ? (
              <>
                <TableHead>{t("plugins.effects.columns.loads")}</TableHead>
                <TableHead>{t("plugins.effects.columns.injected")}</TableHead>
              </>
            ) : null}
            {parts.lookup ? (
              <>
                <TableHead>{t("plugins.effects.columns.lookups")}</TableHead>
                <TableHead>{t("plugins.effects.columns.hits")}</TableHead>
              </>
            ) : null}
            {parts.binding ? (
              <TableHead>{t("plugins.effects.columns.bindings")}</TableHead>
            ) : null}
            {parts.write ? (
              <TableHead>{t("plugins.effects.columns.writes")}</TableHead>
            ) : null}
            <TableHead>{t("plugins.effects.columns.problems")}</TableHead>
            {parts.cache ? (
              <TableHead>{t("plugins.effects.columns.cache")}</TableHead>
            ) : null}
          </TableRow>
        </TableHeader>
        <TableBody>
          {days.map((day) => {
            const bindings =
              day.binding.workspace +
              day.binding.picked +
              day.binding.created +
              day.binding.declined;
            const problems =
              day.problems.failed +
              day.problems.timeouts +
              day.problems.privacy +
              day.problems.over_budget;
            return (
              <TableRow key={day.date}>
                <TableCell className="tabular-nums">{day.date}</TableCell>
                {parts.load ? (
                  <>
                    <TableCell className="tabular-nums">
                      {day.load.count}
                    </TableCell>
                    <TableCell className="tabular-nums">
                      {formatSize(day.load.injected_bytes)}
                    </TableCell>
                  </>
                ) : null}
                {parts.lookup ? (
                  <>
                    <TableCell className="tabular-nums">
                      {day.lookup.ran}
                    </TableCell>
                    <TableCell className="tabular-nums">
                      {percent(day.lookup.with_hits, day.lookup.counted) ?? "—"}
                    </TableCell>
                  </>
                ) : null}
                {parts.binding ? (
                  <TableCell className="tabular-nums">{bindings}</TableCell>
                ) : null}
                {parts.write ? (
                  <TableCell className="tabular-nums">
                    {day.write.failed > 0
                      ? t("plugins.effects.writeCell", day.write)
                      : day.write.ok}
                  </TableCell>
                ) : null}
                <TableCell className="tabular-nums">{problems}</TableCell>
                {parts.cache ? (
                  <TableCell className="tabular-nums">
                    {t("plugins.effects.cacheCell", {
                      with: cacheRate(day.cache.with) ?? "—",
                      without: cacheRate(day.cache.without) ?? "—",
                    })}
                  </TableCell>
                ) : null}
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </section>
  );
}
