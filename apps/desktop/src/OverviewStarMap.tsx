import { useEffect, useState, type ReactNode } from "react";

import astrlinkLogo from "@/assets/astrlink-logo.svg";
import { ArrowRight } from "@/components/icons";
import { CompactCount } from "@/components/CompactCount";
import { ModelBrandIcon } from "@/components/ModelBrandIcon";
import {
  StarBody,
  StarDetail,
  StarMark,
  StarSphere,
  StarStage,
  bodySize,
  type StageMark,
  type StagePoint,
  type StageRay,
  type StageScene,
  type StageSize,
} from "@/components/StarStage";
import { StatusDot } from "@/components/StatusDot";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import { listPlugins } from "./bridge";
import {
  formatCompactNumber,
  formatExactNumber,
} from "./format-compact-number";
import { useT } from "./i18n";
import {
  STAR_ARC_LIMIT,
  clientPlaces,
  modelPlaces,
  pluginHealth,
  pluginWork,
  sidePlugins,
  starPlugins,
  type StarPlace,
} from "./overview-star-model";
import type { PluginSummary } from "./plugin-model";
import {
  PluginStarDetail,
  pluginStarColor,
  pluginStarFact,
} from "./PluginStarParts";
import { billingAmount, type BillingAmounts } from "./pricing-model";
import type { RecordsTarget } from "./request-live-model";
import type { Service } from "./service-model";
import {
  formatCacheHitPercent,
  type UsageStatus,
  type UsageSummary,
} from "./usage-range";

type Translate = ReturnType<typeof useT>;

// The system, in world units: AstrLink in the middle, the clients on the far
// half of its orbit and the models on the near half, the plugins as its
// satellites on a closer orbit, enhancers to the left, providers to the right.
const WORLD = { width: 1400, height: 900 };
const CENTER: StagePoint = { x: 700, y: 450 };
const ORBIT_RX = 700;
/** Between neighbors on the orbit, and the farthest it puts one to a side. */
const ORBIT_STEP = 240;
const ORBIT_REACH = 566;
const SATELLITES = { rx: 330, ry: 190, step: 100 };
/** A planet's diameter on screen: the least, and the most for the busiest. */
const PLANET = { least: 14, most: 38 };
/** Its name under it, and how wide the name may run. */
const PLANET_LABEL = { width: 124, height: 32 };
/** A satellite with its name beside it. */
const SATELLITE = { width: 176, height: 26, least: 9, most: 15 };
const STAR = { width: 76, height: 76 };
/** The headline over the map and the figures under it. */
const HEADLINE = 40;
const FIGURES = 58;
/** Where a stage is too narrow for its figures in a row. */
const NARROW = 640;
/** One hue per client and per model by rank, the rest in grey. */
const CLIENT_COLORS = [1, 2, 3, 4, 5].map(
  (rank) => `var(--star-client-${rank})`,
);
const MODEL_COLORS = [1, 2, 3, 4, 5].map((rank) => `var(--star-model-${rank})`);

/**
 * How much of the range a map names: the planets on each half of the orbit
 * before the rest merge, the satellites on a side before the rest merge,
 * and how far the orbit reaches toward and away from the viewer.
 */
interface Layout {
  arc: number;
  dock: number;
  depth: number;
}

/** The layouts to try at a width, the fullest first. */
function layouts(width: number): Layout[] {
  const widest =
    width >= 960 ? STAR_ARC_LIMIT : width >= 760 ? 4 : width >= 600 ? 3 : 2;
  const tried: Layout[] = [];
  for (let arc = widest; arc >= 1; arc -= 1)
    for (const depth of [330, 270, 220])
      for (const dock of [3, 2, 1]) tried.push({ arc, dock, depth });
  return tried;
}

/** Places on a half of the orbit: evenly across, far from the viewer or near. */
function orbitPoints(
  count: number,
  half: "far" | "near",
  depth: number,
): StagePoint[] {
  const reach = Math.min(ORBIT_REACH, (ORBIT_STEP * (count - 1)) / 2);
  return Array.from({ length: count }, (_, index) => {
    const dx = count === 1 ? 0 : -reach + ((2 * reach) / (count - 1)) * index;
    const dy = depth * Math.sqrt(1 - (dx / ORBIT_RX) ** 2);
    return { x: CENTER.x + dx, y: CENTER.y + (half === "far" ? -dy : dy) };
  });
}

/** A side's satellites, top to bottom on their orbit. */
function satellitePoints(count: number, side: "left" | "right"): StagePoint[] {
  return Array.from({ length: count }, (_, index) => {
    const dy = (index - (count - 1) / 2) * SATELLITES.step;
    const dx =
      SATELLITES.rx * Math.sqrt(Math.max(0, 1 - (dy / SATELLITES.ry) ** 2));
    return {
      x: CENTER.x + (side === "left" ? -dx : dx),
      y: CENTER.y + dy,
    };
  });
}

/** The installed plugins, read again whenever the usage is. */
function useInstalledPlugins(
  isReady: boolean,
  revision: unknown,
): PluginSummary[] | null {
  const [plugins, setPlugins] = useState<PluginSummary[] | null>(null);
  useEffect(() => {
    if (!isReady) return;
    let live = true;
    listPlugins()
      .then((list) => {
        if (live) setPlugins(list.plugins);
      })
      .catch(() => {
        // The map leaves out what it could not read.
        if (live) setPlugins(null);
      });
    return () => {
      live = false;
    };
  }, [isReady, revision]);
  return plugins;
}

/**
 * The overview's star map: AstrLink as the star of its own system, the
 * clients that sent it requests over the range and the models it reached
 * as its planets, the larger the more Token they took, and its plugins as
 * the satellites that work beside it; the range's figures lie under it. A
 * client or a model opens its requests, a plugin its page.
 */
export function OverviewStarMap({
  billingAmountText,
  billingTitle,
  className,
  costs,
  isReady,
  onOpenPlugin,
  onOpenPlugins,
  onOpenRecords,
  services,
  status,
  summary,
  tokens,
}: {
  /** The range's official amount, and what it leaves out. */
  billingAmountText: string;
  billingTitle?: string;
  className?: string;
  costs: {
    byToken: ReadonlyMap<string, BillingAmounts>;
    byModel: ReadonlyMap<string, BillingAmounts>;
  };
  isReady: boolean;
  onOpenPlugin: (key: string) => void;
  onOpenPlugins: () => void;
  onOpenRecords: (target: RecordsTarget) => void;
  services: Service[];
  status: UsageStatus;
  summary: UsageSummary | null;
  tokens: ReadonlyArray<{ id: string; name: string }>;
}) {
  const t = useT();
  const [focus, setFocus] = useState<string | null>(null);
  const installed = useInstalledPlugins(isReady, summary);
  // A summary kept through a failed refresh must not read as current.
  const current = status === "blocked" || status === "error" ? null : summary;
  const plugins = starPlugins(
    installed ?? [],
    current?.by_plugin ?? [],
    services,
    current?.by_service ?? [],
  );
  const health = pluginHealth(plugins);
  const totals = current?.totals;
  const named = new Set(tokens.map((token) => token.id));
  const activeClients = current
    ? current.by_token.filter(
        (group) =>
          group.id !== null &&
          named.has(group.id) &&
          (group.total_tokens > 0 ||
            group.requests + group.failed_requests > 0),
      ).length
    : 0;
  const activeModels = current
    ? current.by_model.filter(
        (group) => group.total_tokens > 0 || group.requests > 0,
      ).length
    : 0;

  const focusProps = (id: string) => ({
    focus:
      focus === null
        ? undefined
        : focus === id
          ? ("lit" as const)
          : ("dim" as const),
    onBlur: () => setFocus(null),
    onFocus: () => setFocus(id),
    onMouseEnter: () => setFocus(id),
    onMouseLeave: () => setFocus(null),
  });

  // One layout of the range's planets and satellites, as a scene.
  const layoutScene = (layout: Layout, narrow: boolean): StageScene => {
    const clients = current ? clientPlaces(current, tokens, layout.arc) : [];
    const models = current
      ? modelPlaces(current, t("today.unknownModel"), layout.arc)
      : [];
    // Planets are sized against the busiest of either half, so a client
    // and the model it mostly used read alike.
    const measure = (place: StarPlace) =>
      totals && totals.total_tokens > 0 ? place.tokens : place.requests;
    const busiest = Math.max(0, ...[...clients, ...models].map(measure));
    const rays: StageRay[] = [];
    const marks: StageMark[] = [];
    const dimmed = (id: string) => focus !== null && focus !== id;
    const callouts = new Map<string, ReactNode>();

    const half = (places: StarPlace[], side: "far" | "near") =>
      orbitPoints(places.length, side, layout.depth).forEach((at, index) => {
        const place = places[index];
        const id = `${side === "far" ? "client" : "model"}:${place.rest ? "rest" : (place.id ?? "unknown")}`;
        const palette = side === "far" ? CLIENT_COLORS : MODEL_COLORS;
        const color = place.rest
          ? `var(--star-${side === "far" ? "client" : "model"}-other)`
          : (palette[index] ?? palette[palette.length - 1]);
        const size = bodySize(measure(place), busiest, PLANET);
        // Requests come in from the clients and go out to the models.
        rays.push({
          id,
          to: at,
          color,
          sparks: side === "far" ? "in" : "out",
          dimmed: dimmed(id),
        });
        const open = place.rest
          ? undefined
          : side === "far"
            ? () => onOpenRecords({ tokenId: place.id ?? undefined })
            : place.id
              ? () => onOpenRecords({ models: [place.id as string] })
              : undefined;
        const name = placeName(place, side, t);
        // A name goes on the side away from the star: over a client, under
        // a model.
        const sphere = size * 1.15;
        const height = sphere + 4 + PLANET_LABEL.height;
        marks.push({
          id,
          at,
          box: { width: PLANET_LABEL.width, height },
          origin: {
            x: PLANET_LABEL.width / 2,
            y: side === "far" ? height - sphere / 2 : sphere / 2,
          },
          children: ({ near, toStar }) => (
            <StarBody
              aria-label={
                open ? t("overview.openRecords", { name }) : undefined
              }
              captionAt={side === "far" ? "above" : "below"}
              fact={t("overview.star.tokens", {
                value: formatCompactNumber(place.tokens),
              })}
              caption={
                <span className="flex items-center gap-1">
                  {side === "near" && place.id && !place.rest ? (
                    <ModelBrandIcon model={place.id} size={12} />
                  ) : null}
                  <span className="truncate">{name}</span>
                </span>
              }
              onClick={open}
              sphere={
                <span
                  className="grid place-items-center"
                  style={{ height: sphere }}
                >
                  <StarSphere
                    color={color}
                    size={size * Math.min(1.15, near)}
                    toStar={toStar}
                  />
                </span>
              }
              {...focusProps(id)}
            />
          ),
        });
        callouts.set(
          id,
          <PlaceDetail
            amount={
              place.rest || !place.id
                ? undefined
                : (side === "far" ? costs.byToken : costs.byModel).get(place.id)
            }
            place={place}
            side={side}
            t={t}
            totalTokens={totals?.total_tokens ?? 0}
          />,
        );
      });
    half(clients, "far");
    half(models, "near");

    const busiestPlugin = Math.max(0, ...plugins.map(pluginWork));
    for (const kind of ["enhancer", "provider"] as const) {
      const side = kind === "enhancer" ? "left" : "right";
      const listed = sidePlugins(plugins, kind);
      const shown =
        listed.length > layout.dock
          ? listed.slice(0, Math.max(0, layout.dock - 1))
          : listed;
      const more = listed.length - shown.length;
      const points = satellitePoints(shown.length + (more > 0 ? 1 : 0), side);
      shown.forEach((plugin, index) => {
        const at = points[index];
        const id = `plugin:${plugin.plugin.key}`;
        const color = pluginStarColor(plugin);
        const size = bodySize(pluginWork(plugin), busiestPlugin, SATELLITE);
        const working =
          plugin.state === "running" || plugin.state === "failing";
        rays.push({
          id,
          to: at,
          color,
          sparks: working ? "out" : undefined,
          dimmed: dimmed(id),
        });
        marks.push({
          id,
          at,
          box: { width: SATELLITE.width, height: SATELLITE.height },
          // The satellite sits on its orbit; its name runs outward.
          origin: {
            x:
              side === "left"
                ? SATELLITE.width - SATELLITE.most / 2
                : SATELLITE.most / 2,
            y: SATELLITE.height / 2,
          },
          children: ({ toStar }) => (
            <StarBody
              aria-label={t("plugins.star.open", {
                name: plugin.plugin.name,
                fact: pluginStarFact(plugin, t),
              })}
              className="h-full"
              fact={pluginStarFact(plugin, t)}
              captionAt={side === "left" ? "before" : "after"}
              caption={plugin.plugin.name}
              onClick={() => onOpenPlugin(plugin.plugin.key)}
              sphere={
                <span
                  className="grid place-items-center"
                  style={{ width: SATELLITE.most }}
                >
                  <StarSphere
                    color={color}
                    quiet={plugin.state === "off"}
                    size={size}
                    toStar={toStar}
                  />
                </span>
              }
              {...focusProps(id)}
            />
          ),
        });
        callouts.set(id, <PluginStarDetail plugin={plugin} />);
      });
      if (more > 0) {
        const id = `plugins:${kind}`;
        marks.push({
          id,
          at: points[points.length - 1],
          box: { width: 96, height: SATELLITE.height },
          origin: { x: side === "left" ? 96 - 8 : 8, y: SATELLITE.height / 2 },
          children: (
            <span
              className={cn(
                "flex h-full items-center",
                side === "left" ? "justify-end" : "justify-start",
              )}
            >
              <StarMark
                className="px-2.5 py-0.5 text-xs text-star-dim"
                onClick={onOpenPlugins}
                {...focusProps(id)}
              >
                {t("plugins.star.more", { count: more })}
              </StarMark>
            </span>
          ),
        });
      }
    }

    return {
      center: CENTER,
      orbits: [
        { rx: ORBIT_RX, ry: layout.depth },
        { rx: SATELLITES.rx, ry: SATELLITES.ry, fine: true },
      ],
      rays,
      marks,
      star: {
        box: STAR,
        children: (
          <img
            alt="AstrLink"
            className="size-10"
            draggable={false}
            src={astrlinkLogo}
          />
        ),
      },
      callout:
        focus !== null && callouts.has(focus)
          ? { mark: focus, children: callouts.get(focus) }
          : undefined,
      inset: {
        top: narrow ? HEADLINE + 16 : HEADLINE,
        right: 12,
        bottom: narrow ? FIGURES * 2 : FIGURES,
        left: 12,
      },
    };
  };

  // The layouts to try at the stage's size, the fullest first.
  function* scenes(size: StageSize): Generator<StageScene> {
    for (const layout of layouts(size.width))
      yield layoutScene(layout, size.width < NARROW);
  }

  const headline = !current
    ? t(status === "error" ? "overview.star.failed" : "overview.star.blocked")
    : current.totals.requests + current.totals.failed_requests === 0
      ? t("overview.star.empty")
      : t(
          activeClients > 0
            ? "overview.star.headline"
            : "overview.star.headlineNoClients",
          {
            clients: t("overview.star.clients", {
              count: activeClients,
              value: formatExactNumber(activeClients),
            }),
            requests: t("overview.star.requests", {
              count: current.totals.requests,
              value: formatExactNumber(current.totals.requests),
            }),
            models: t("overview.star.models", {
              count: activeModels,
              value: formatExactNumber(activeModels),
            }),
          },
        );

  return (
    <StarStage
      aria-label={t("overview.star.label")}
      className={cn("@container/star rounded-md", className)}
      data-testid="overview-star-map"
      role="group"
      scene={scenes}
      world={WORLD}
    >
      <div className="absolute top-3 right-4 left-4 flex items-center justify-between gap-4">
        <p className="line-clamp-2 min-w-0 text-sm font-semibold text-star-ink @min-[640px]/star:line-clamp-1 @min-[640px]/star:text-base @min-[900px]/star:text-lg">
          {headline}
        </p>
        {installed ? (
          <PluginHealth health={health} onOpen={onOpenPlugins} t={t} />
        ) : null}
      </div>
      <div className="absolute inset-x-4 bottom-3">
        <dl className="grid grid-cols-2 gap-x-6 gap-y-2 @min-[640px]/star:flex @min-[640px]/star:gap-x-0">
          <StarFigure
            label={t("overview.requests")}
            value={<CompactCount value={totals?.requests} />}
          />
          <StarFigure
            detail={
              totals && totals.input_tokens + totals.output_tokens > 0 ? (
                <>
                  {t("overview.input")}{" "}
                  <CompactCount value={totals.input_tokens} />
                  {" · "}
                  {t("overview.output")}{" "}
                  <CompactCount value={totals.output_tokens} />
                </>
              ) : undefined
            }
            label={t("overview.totalTokens")}
            value={<CompactCount value={totals?.total_tokens} />}
          />
          <StarFigure
            label={t("overview.cacheHits")}
            value={totals ? formatCacheHitPercent(totals) : "—"}
          />
          <StarFigure
            label={t("pricing.officialAmount")}
            title={billingTitle}
            value={billingAmountText}
          />
        </dl>
      </div>
    </StarStage>
  );
}

/** How the plugins are doing, leading to their page. */
function PluginHealth({
  health,
  onOpen,
  t,
}: {
  health: ReturnType<typeof pluginHealth>;
  onOpen: () => void;
  t: Translate;
}) {
  return (
    <Button
      className="h-auto shrink-0 gap-1.5 px-0 text-xs font-normal text-star-dim hover:bg-transparent hover:text-star-ink"
      onClick={onOpen}
      size="sm"
      type="button"
      variant="ghost"
    >
      <StatusDot
        tone={
          health.problems > 0
            ? "pending"
            : health.running > 0
              ? "positive"
              : "neutral"
        }
      />
      {health.installed === 0 ? (
        t("overview.star.noPlugins")
      ) : health.running === 0 ? (
        t("overview.star.pluginsOff")
      ) : (
        <>
          {/* A narrow map keeps its headline whole: the plugins say less. */}
          <span className="@min-[900px]/star:hidden">
            {health.problems > 0
              ? t("overview.star.pluginsProblemsShort", {
                  count: health.problems,
                })
              : t("overview.star.pluginsHealthyShort")}
          </span>
          <span className="hidden @min-[900px]/star:inline">
            {health.problems > 0
              ? t("overview.star.pluginsProblems", {
                  count: health.running,
                  problems: health.problems,
                })
              : t("overview.star.pluginsHealthy", { count: health.running })}
          </span>
        </>
      )}
      <span aria-hidden="true" className="hidden @min-[900px]/star:inline">
        ·
      </span>
      <span className="sr-only @min-[900px]/star:not-sr-only">
        {t("overview.star.openPlugins")}
      </span>
      <ArrowRight aria-hidden="true" className="size-3.5" />
    </Button>
  );
}

/** One of the range's figures under the map, with a split beside its label. */
function StarFigure({
  detail,
  label,
  value,
  title,
}: {
  detail?: ReactNode;
  label: string;
  value: ReactNode;
  title?: string;
}) {
  return (
    <div className="grid gap-0.5 border-star-line @min-[640px]/star:border-l @min-[640px]/star:px-5 @min-[640px]/star:first:border-l-0 @min-[640px]/star:first:pl-0">
      <dt className="flex items-baseline gap-2 text-xs whitespace-nowrap text-star-dim">
        {label}
        {detail ? (
          <span className="text-micro tabular-nums">{detail}</span>
        ) : null}
      </dt>
      <dd
        className="text-xl font-semibold text-star-ink tabular-nums"
        title={title}
      >
        {value}
      </dd>
    </div>
  );
}

function placeName(place: StarPlace, side: "far" | "near", t: Translate) {
  if (!place.rest) return place.name;
  if (side === "far" && place.rest.whole) return t("overview.star.allClients");
  return t("overview.star.others");
}

/** What a client or a model did over the range. */
function PlaceDetail({
  amount,
  place,
  side,
  t,
  totalTokens,
}: {
  amount?: BillingAmounts;
  place: StarPlace;
  side: "far" | "near";
  t: Translate;
  totalTokens: number;
}) {
  const share =
    totalTokens > 0 ? `${Math.round((place.tokens / totalTokens) * 100)}%` : "";
  const kind = place.rest
    ? place.rest.count > 0
      ? t(
          side === "far"
            ? "overview.star.otherClients"
            : "overview.star.otherModels",
          { count: place.rest.count },
        )
      : t("overview.star.noToken")
    : t(side === "far" ? "overview.star.client" : "overview.star.model");
  const rows = [
    {
      label: "Token",
      value: share
        ? t("overview.star.share", {
            value: formatExactNumber(place.tokens),
            share,
          })
        : formatExactNumber(place.tokens),
    },
    {
      label: t("overview.star.requestsLabel"),
      value: t("overview.star.times", {
        value: formatExactNumber(place.requests),
      }),
    },
  ];
  if (place.failed > 0)
    rows.push({
      label: t("overview.star.failedLabel"),
      value: t("overview.star.times", {
        value: formatExactNumber(place.failed),
      }),
    });
  if (amount && amount.priced > 0)
    rows.push({
      label: t("pricing.officialAmount"),
      value: billingAmount(amount),
    });
  return (
    <StarDetail
      className="w-60"
      kind={kind}
      rows={rows}
      title={placeName(place, side, t)}
    />
  );
}
