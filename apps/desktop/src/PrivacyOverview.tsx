import {
  memo,
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
} from "react";
import {
  Area,
  CartesianGrid,
  ComposedChart,
  Line,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import type { TooltipContentProps } from "recharts";

import { ChartTooltipCard } from "@/components/ChartTooltip";
import { CompactCount } from "@/components/CompactCount";
import { EmptyState } from "@/components/EmptyState";
import { FormMessage } from "@/components/FormMessage";
import { HelpPopover } from "@/components/HelpPopover";
import { Eye, EyeOff, Fingerprint, ScanText } from "@/components/icons";
import { Metric, MetricGroup } from "@/components/Metric";
import { Panel, PanelHeader } from "@/components/Panel";
import { StackedBar } from "@/components/StackedBar";
import { UsageMeterGrid, UsageMeterRow } from "@/components/UsageMeter";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import { getPrivacySummary } from "./bridge";
import {
  AXIS_LINE,
  AXIS_TICK,
  chartPointLabel,
  dayLabel,
  hourTickLabel,
  xAxisInterval,
} from "./chart-axis";
import {
  formatCompactNumber,
  formatExactNumber,
} from "./format-compact-number";
import { useT } from "./i18n";
import {
  checkedCalls,
  flaggedCalls,
  hasPrivacyActivity,
  PRIVACY_OUTCOME_SEGMENTS,
  privacyKindShares,
  privacyTrendPoints,
  sharePercent,
  unprotectedCalls,
  type PrivacyKindCount,
  type PrivacyOutcomeSegment,
  type PrivacySummary,
  type PrivacyTotals,
  type PrivacyTrendPoint,
} from "./privacy-summary-model";
import { resolveUsageWindow, type UsageRangePreset } from "./usage-range";
import { useWorkspaceSnapshot } from "./workspace-snapshots";

/** New calls show up without a manual refresh while the tab is open. */
const REVALIDATE_MS = 30_000;

const METRIC_TILES = "gap-3 bg-transparent [&>div]:rounded-md [&>div]:border";

/**
 * Outcome fills. Status tokens carry the meaning of each outcome; a call with
 * nothing found recedes to gray so the outcomes that acted stand out. An
 * ignored call found something but replaced nothing, so it takes a faded
 * redaction fill.
 */
const OUTCOME_FILL: Record<PrivacyOutcomeSegment, string> = {
  redacted: "bg-primary",
  blocked: "bg-blocked",
  warned: "bg-warning",
  fail_open: "bg-destructive",
  ignored: "bg-primary/40",
  allowed: "bg-muted-foreground/30",
};

type OverviewStatus = "loading" | "ready" | "error";

/**
 * What privacy protection did over the chosen range: how many calls it
 * checked, what it found, how much it replaced and restored, and how each
 * call ended. Every number comes from Core's request records.
 */
export function PrivacyOverview({
  active,
  coreSessionKey,
  enabled,
  isReady,
  preset,
}: {
  /** The tab is showing; a hidden tab neither loads nor polls. */
  active: boolean;
  coreSessionKey: string | null;
  /** The policy switch right now, for the empty state. */
  enabled: boolean;
  isReady: boolean;
  preset: UsageRangePreset;
}) {
  const t = useT();
  const [summary, setSummary] = useWorkspaceSnapshot<PrivacySummary | null>(
    `privacy-summary:${coreSessionKey}`,
    null,
  );
  const [status, setStatus] = useState<OverviewStatus>(
    summary ? "ready" : "loading",
  );
  const [error, setError] = useState<string | null>(null);
  const generation = useRef(0);
  const summaryRef = useRef(summary);
  summaryRef.current = summary;

  const load = useCallback(
    async (silent: boolean) => {
      if (!isReady) return;
      const id = generation.current + 1;
      generation.current = id;
      if (!silent) {
        setStatus("loading");
        setError(null);
      }
      try {
        const next = await getPrivacySummary(
          resolveUsageWindow(preset, new Date()),
        );
        if (generation.current !== id) return;
        setSummary(next);
        setStatus("ready");
        setError(null);
      } catch (cause) {
        // A failed background pass keeps the numbers already on screen.
        if (generation.current !== id || silent) return;
        setStatus("error");
        setError(cause instanceof Error ? cause.message : String(cause));
      }
    },
    [isReady, preset, setSummary],
  );

  useEffect(() => {
    if (!active) return;
    // Numbers kept from the last visit stay on screen while they refresh. A
    // new range dims the old ones until its own arrive.
    const cached = summaryRef.current;
    void load(cached !== null && cached.window.preset === preset);
  }, [active, load, preset]);

  useEffect(() => {
    if (!active || !isReady) return;
    const revalidate = () => {
      if (!document.hidden) void load(true);
    };
    const timer = window.setInterval(revalidate, REVALIDATE_MS);
    document.addEventListener("visibilitychange", revalidate);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", revalidate);
    };
  }, [active, isReady, load]);

  const dimmed = status === "loading" && summary !== null;
  return (
    <div
      className="gutter-scroller h-full min-h-0 min-w-0 overflow-y-auto overscroll-contain"
      data-slot="privacy-overview"
      data-tab-scroller
    >
      <div className="grid min-w-0 content-start gap-3 pb-1">
        {summary === null ? (
          status === "error" ? (
            <EmptyState
              action={
                <Button
                  onClick={() => void load(false)}
                  type="button"
                  variant="outline"
                >
                  {t("privacyStats.retry")}
                </Button>
              }
              description={error}
              title={t("privacyStats.failed")}
            />
          ) : (
            <PrivacyOverviewSkeleton />
          )
        ) : (
          <>
            {status === "error" && error ? (
              <FormMessage tone="error">
                {t("privacyStats.failed")} {error}
              </FormMessage>
            ) : null}
            <div
              aria-busy={dimmed || undefined}
              className={cn(
                "grid min-w-0 gap-3 transition-opacity duration-200 motion-reduce:transition-none",
                dimmed && "opacity-60",
              )}
              data-slot="privacy-overview-content"
            >
              <PrivacyOverviewBody enabled={enabled} summary={summary} />
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function PrivacyOverviewBody({
  enabled,
  summary,
}: {
  enabled: boolean;
  summary: PrivacySummary;
}) {
  const t = useT();
  const totals = summary.totals;
  // The tab exists because the last year has checked calls; a narrower range
  // can still hold none.
  if (!hasPrivacyActivity(totals)) {
    const unprotected = unprotectedCalls(totals);
    return (
      <EmptyState
        className="py-14"
        description={
          totals.requests > 0
            ? [
                unprotected > 0
                  ? t("privacyStats.emptyUnprotected", { count: unprotected })
                  : "",
                totals.trusted > 0
                  ? t("privacyStats.emptyTrusted", { count: totals.trusted })
                  : "",
              ]
                .filter(Boolean)
                .join(" ")
            : t(
                enabled
                  ? "privacyStats.emptyBody"
                  : "privacyStats.emptyOffBody",
              )
        }
        title={t(
          enabled ? "privacyStats.emptyTitle" : "privacyStats.emptyOffTitle",
        )}
      />
    );
  }
  return (
    <>
      <PrivacyMetrics totals={totals} />
      <Panel>
        <PrivacyTrendChart summary={summary} />
      </Panel>
      {/* Side by side, the shorter card stretches to the taller one. */}
      <div className="grid min-w-0 gap-3 @[720px]/privacy:grid-cols-2">
        <PrivacyKindsPanel kinds={summary.by_kind} totals={totals} />
        <PrivacyOutcomesPanel totals={totals} />
      </div>
    </>
  );
}

function PrivacyMetrics({ totals }: { totals: PrivacyTotals }) {
  const t = useT();
  const checked = checkedCalls(totals);
  const flagged = flaggedCalls(totals);
  const split = totals.restored_visible + totals.restored_tool_arguments > 0;
  return (
    <MetricGroup className={METRIC_TILES} data-slot="privacy-metrics">
      <Metric
        icon={<ScanText />}
        label={
          <span title={t("privacyStats.checkedHint")}>
            {t("privacyStats.checked")}
          </span>
        }
        value={<CompactCount value={checked} />}
      />
      <Metric
        detail={
          checked > 0
            ? t("privacyStats.flaggedShare", {
                percent: sharePercent(flagged, checked),
              })
            : null
        }
        icon={<Fingerprint />}
        label={
          <span title={t("privacyStats.flaggedHint")}>
            {t("privacyStats.flagged")}
          </span>
        }
        value={<CompactCount value={flagged} />}
      />
      <Metric
        icon={<EyeOff />}
        label={
          <span title={t("privacyStats.replacedHint")}>
            {t("privacyStats.replaced")}
          </span>
        }
        value={<CompactCount value={totals.replaced} />}
      />
      <Metric
        detail={
          split || totals.unrestored > 0 ? (
            <span className="grid gap-0.5">
              {split ? (
                <span>
                  {t("privacyStats.restoredSplit", {
                    visible: formatCompactNumber(totals.restored_visible),
                    tools: formatCompactNumber(totals.restored_tool_arguments),
                  })}
                </span>
              ) : null}
              {totals.unrestored > 0 ? (
                <span
                  className="text-warning-foreground"
                  data-slot="privacy-unrestored"
                >
                  {t("privacyStats.unrestored", { count: totals.unrestored })}
                </span>
              ) : null}
            </span>
          ) : null
        }
        icon={<Eye />}
        label={
          <span title={t("privacyStats.restoredHint")}>
            {t("privacyStats.restored")}
          </span>
        }
        value={<CompactCount value={totals.restored} />}
      />
    </MetricGroup>
  );
}

const SERIES = {
  replaced: { color: "var(--primary)", swatch: "bg-primary" },
  restored: { color: "var(--success)", swatch: "bg-success" },
} as const;

const PrivacyTrendChart = memo(function PrivacyTrendChart({
  summary,
}: {
  summary: PrivacySummary;
}) {
  const t = useT();
  const chartRef = useRef<HTMLDivElement>(null);
  const points = privacyTrendPoints(summary);
  const hourly = summary.window.preset === "1d";
  const empty = !points.some((point) => point.replaced + point.restored > 0);
  return (
    <div className="px-4 py-3.5" data-slot="privacy-trend">
      <div className="flex min-h-6 flex-wrap items-center justify-between gap-x-3 gap-y-2">
        <div className="flex items-center gap-1">
          <h3 className="text-micro font-medium text-muted-foreground">
            {t(hourly ? "privacyStats.trendHour" : "privacyStats.trendDay")}
          </h3>
          <HelpPopover label={t("privacyStats.helpLabel")} size="wide">
            <div className="space-y-2">
              <p>{t("privacyStats.helpReplaced")}</p>
              <p>{t("privacyStats.helpRepeat")}</p>
              <p>{t("privacyStats.helpRestored")}</p>
            </div>
          </HelpPopover>
        </div>
        <div className="flex flex-wrap items-center justify-end gap-3">
          {/* The legend mirrors the marks: a filled area, then a line. */}
          <span className="inline-flex items-center gap-1.5 text-micro text-muted-foreground">
            <span
              aria-hidden="true"
              className="h-2 w-3 rounded-[2px] border-t-2 border-primary bg-primary/15"
            />
            {t("privacyStats.seriesReplaced")}
          </span>
          <span className="inline-flex items-center gap-1.5 text-micro text-muted-foreground">
            <span
              aria-hidden="true"
              className={cn("h-0.5 w-3 rounded-full", SERIES.restored.swatch)}
            />
            {t("privacyStats.seriesRestored")}
          </span>
        </div>
      </div>
      <div
        aria-label={t("privacyStats.trendChart")}
        className="relative mt-3 h-48 [@media(max-height:680px)]:h-36"
        ref={chartRef}
        role="img"
      >
        <ResponsiveContainer height="100%" width="100%">
          <ComposedChart
            data={points}
            margin={{ top: 6, right: 12, left: 0, bottom: 0 }}
          >
            <CartesianGrid stroke="var(--border)" vertical={false} />
            <XAxis
              axisLine={AXIS_LINE}
              dataKey="slot"
              interval={hourly ? 2 : xAxisInterval(points.length)}
              // Lines start and end on the plot edge; the inset keeps the
              // first and last date labels inside the chart.
              padding={{ left: 16, right: 16 }}
              tick={AXIS_TICK}
              tickFormatter={(value: string) =>
                hourly ? hourTickLabel(value) : dayLabel(value, "short")
              }
              tickLine={false}
            />
            <YAxis
              allowDecimals={false}
              axisLine={false}
              tick={AXIS_TICK}
              tickFormatter={(value: number) => formatCompactNumber(value)}
              tickLine={false}
              width={44}
            />
            <Tooltip
              content={(props) => (
                <PrivacyTrendTooltip {...props} originRef={chartRef} />
              )}
              cursor={{ stroke: "var(--muted-foreground)", strokeWidth: 1 }}
              isAnimationActive={false}
              wrapperStyle={{ display: "none" }}
            />
            <Area
              activeDot={{
                fill: SERIES.replaced.color,
                r: 4,
                stroke: "var(--card)",
                strokeWidth: 2,
              }}
              dataKey="replaced"
              dot={false}
              fill={SERIES.replaced.color}
              fillOpacity={0.1}
              isAnimationActive={false}
              stroke={SERIES.replaced.color}
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth={2}
              type="monotone"
            />
            <Line
              activeDot={{
                fill: SERIES.restored.color,
                r: 4,
                stroke: "var(--card)",
                strokeWidth: 2,
              }}
              dataKey="restored"
              dot={false}
              isAnimationActive={false}
              stroke={SERIES.restored.color}
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth={2}
              type="monotone"
            />
          </ComposedChart>
        </ResponsiveContainer>
        {empty ? (
          <div className="pointer-events-none absolute inset-0 flex items-center justify-center pb-6">
            <span className="rounded-md bg-card px-3 py-2 text-xs text-muted-foreground">
              {t("privacyStats.trendEmpty")}
            </span>
          </div>
        ) : null}
      </div>
    </div>
  );
});

function PrivacyTrendTooltip({
  active,
  coordinate,
  originRef,
  payload,
}: TooltipContentProps & { originRef: RefObject<HTMLDivElement | null> }) {
  const t = useT();
  const point = payload[0]?.payload as PrivacyTrendPoint | undefined;
  if (!point) return null;
  return (
    <ChartTooltipCard
      active={Boolean(active) && payload.length > 0}
      className="min-w-40"
      // Anchored to the top of the plot, so the card never covers the lines
      // it describes.
      coordinate={coordinate ? { x: coordinate.x, y: 0 } : coordinate}
      data-slot="privacy-trend-tooltip"
      originRef={originRef}
      placementKey={point.slot}
    >
      <strong className="text-xs font-medium">{chartPointLabel(point)}</strong>
      <TooltipSeries
        label={t("privacyStats.seriesReplaced")}
        swatch={SERIES.replaced.swatch}
        value={point.replaced}
      />
      <TooltipSeries
        label={t("privacyStats.seriesRestored")}
        swatch={SERIES.restored.swatch}
        value={point.restored}
      />
      {point.flagged > 0 ? (
        <span className="text-muted-foreground tabular-nums">
          {t("privacyStats.tooltipFlagged", {
            count: point.flagged,
          })}
        </span>
      ) : null}
      {point.blocked > 0 ? (
        <span className="text-muted-foreground tabular-nums">
          {t("privacyStats.tooltipBlocked", { count: point.blocked })}
        </span>
      ) : null}
    </ChartTooltipCard>
  );
}

/** Values lead in a tooltip; a short stroke keys the series. */
function TooltipSeries({
  label,
  swatch,
  value,
}: {
  label: string;
  swatch: string;
  value: number;
}) {
  return (
    <span className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-1.5">
      <span
        aria-hidden="true"
        className={cn("h-0.5 w-2.5 rounded-full", swatch)}
      />
      <span className="text-muted-foreground">{label}</span>
      <strong className="text-right font-medium tabular-nums">
        {formatExactNumber(value)}
      </strong>
    </span>
  );
}

/** A share too small to round up still reads as present. */
function shareLabel(percent: number): string {
  if (percent > 0 && percent < 1) return "<1%";
  return `${Math.round(percent)}%`;
}

/**
 * Matches the policy found but left in place, by the reason the user can act
 * on. Matches of a kind the user turned off are not mentioned: leaving those
 * is what the setting asks for.
 */
function ignoredNote(
  t: ReturnType<typeof useT>,
  totals: PrivacyTotals,
): string | null {
  const allowlisted = totals.ignored_allowlisted;
  const lowConfidence = totals.ignored_low_confidence;
  if (allowlisted > 0 && lowConfidence > 0) {
    return t("privacyStats.ignoredBoth", {
      total: formatExactNumber(allowlisted + lowConfidence),
      allowlisted: formatExactNumber(allowlisted),
      lowConfidence: formatExactNumber(lowConfidence),
    });
  }
  if (allowlisted > 0) {
    return t("privacyStats.ignoredAllowlisted", {
      count: allowlisted,
      value: formatExactNumber(allowlisted),
    });
  }
  if (lowConfidence > 0) {
    return t("privacyStats.ignoredLowConfidence", {
      count: lowConfidence,
      value: formatExactNumber(lowConfidence),
    });
  }
  return null;
}

function PrivacyKindsPanel({
  kinds,
  totals,
}: {
  kinds: PrivacyKindCount[];
  totals: PrivacyTotals;
}) {
  const t = useT();
  const { rows } = privacyKindShares(kinds);
  const ignored = ignoredNote(t, totals);
  return (
    <Panel className="flex flex-col" data-slot="privacy-kinds">
      <PanelHeader size="sm">
        <h3 className="text-sm font-medium">{t("privacyStats.kinds")}</h3>
      </PanelHeader>
      {rows.length === 0 ? (
        <p className="flex flex-1 items-center justify-center px-4 py-9 text-center text-xs text-muted-foreground">
          {t("privacyStats.kindsEmpty")}
        </p>
      ) : (
        <div className="flex-1 px-4 py-3">
          <UsageMeterGrid
            aria-label={t("privacyStats.kinds")}
            className="gap-y-2"
          >
            {rows.map((row) => (
              <UsageMeterRow
                caption={shareLabel(row.percent)}
                className="min-h-6"
                key={row.kind}
                label={row.label}
                tone="default"
                value={row.percent}
                valueLabel={t("privacyStats.kindCount", {
                  count: formatExactNumber(row.count),
                })}
                valueText={formatCompactNumber(row.count)}
              />
            ))}
          </UsageMeterGrid>
        </div>
      )}
      {ignored ? (
        <p
          className="mx-4 mb-3 border-t pt-2.5 text-xs text-muted-foreground"
          data-slot="privacy-ignored"
        >
          {ignored}
        </p>
      ) : null}
    </Panel>
  );
}

function PrivacyOutcomesPanel({ totals }: { totals: PrivacyTotals }) {
  const t = useT();
  const reached = checkedCalls(totals) + totals.fail_open;
  const unprotected = unprotectedCalls(totals);
  const segments = PRIVACY_OUTCOME_SEGMENTS.map((key) => ({
    key,
    value: totals[key],
    className: OUTCOME_FILL[key],
    title: `${t(`privacyStats.outcome.${key}`)} ${formatExactNumber(totals[key])}`,
  }));
  return (
    <Panel className="flex flex-col" data-slot="privacy-outcomes">
      <PanelHeader
        actions={
          <span className="text-xs text-muted-foreground tabular-nums">
            {t("privacyStats.outcomesTotal", {
              count: formatExactNumber(reached),
            })}
          </span>
        }
        size="sm"
      >
        <h3 className="text-sm font-medium">{t("privacyStats.outcomes")}</h3>
      </PanelHeader>
      <div className="grid flex-1 content-start gap-3 px-4 py-3">
        <StackedBar label={t("privacyStats.outcomeBar")} segments={segments} />
        <ul className="grid gap-2">
          {segments
            .filter((segment) => segment.value > 0)
            .map((segment) => (
              <OutcomeLegendItem
                count={segment.value}
                fill={segment.className}
                help={t(`privacyStats.outcomeHelp.${segment.key}`)}
                key={segment.key}
                label={t(`privacyStats.outcome.${segment.key}`)}
                outcome={segment.key}
                share={shareLabel((segment.value * 100) / reached)}
              />
            ))}
        </ul>
        {unprotected > 0 ? (
          <p
            className="border-t pt-2.5 text-xs text-muted-foreground"
            data-slot="privacy-unprotected"
          >
            {t("privacyStats.unprotected", { count: unprotected })}
          </p>
        ) : null}
        {totals.trusted > 0 ? (
          <p
            className="border-t pt-2.5 text-xs text-muted-foreground"
            data-slot="privacy-trusted"
          >
            {t("privacyStats.trusted", { count: totals.trusted })}
          </p>
        ) : null}
      </div>
    </Panel>
  );
}

function OutcomeLegendItem({
  count,
  fill,
  help,
  label,
  outcome,
  share,
}: {
  count: number;
  fill: string;
  help: ReactNode;
  label: string;
  outcome: PrivacyOutcomeSegment;
  share: string;
}) {
  return (
    <li
      className="grid grid-cols-[auto_minmax(0,1fr)_auto_2.75rem] items-start gap-x-2"
      data-outcome={outcome}
    >
      <span
        aria-hidden="true"
        className={cn("mt-1 size-2 shrink-0 rounded-[2px]", fill)}
      />
      <span className="grid min-w-0 gap-0.5">
        <span className="text-xs text-foreground">{label}</span>
        <span className="text-micro text-muted-foreground">{help}</span>
      </span>
      <span className="text-xs font-medium tabular-nums">
        {formatExactNumber(count)}
      </span>
      <span className="text-right text-micro leading-5 text-muted-foreground tabular-nums">
        {share}
      </span>
    </li>
  );
}

function PrivacyOverviewSkeleton() {
  const t = useT();
  return (
    <div
      aria-label={t("privacyStats.loading")}
      className="grid gap-3"
      data-slot="privacy-overview-skeleton"
      role="status"
    >
      <MetricGroup className={METRIC_TILES}>
        {Array.from({ length: 4 }, (_, index) => (
          <Metric
            key={index}
            label={
              <span className="block h-4 w-14 animate-pulse rounded bg-muted motion-reduce:animate-none" />
            }
            value={
              <span className="block h-7 w-16 animate-pulse rounded bg-muted motion-reduce:animate-none" />
            }
          />
        ))}
      </MetricGroup>
      <Panel className="px-4 py-3.5">
        <span className="block h-4 w-28 animate-pulse rounded bg-muted motion-reduce:animate-none" />
        <div className="mt-3 h-48 animate-pulse rounded-md bg-muted motion-reduce:animate-none [@media(max-height:680px)]:h-36" />
      </Panel>
    </div>
  );
}
