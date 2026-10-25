import { useReducedMotion } from "motion/react";
import {
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ComponentProps,
  type ReactNode,
} from "react";

import astrlinkLogo from "@/assets/astrlink-logo.svg";
import { HelpPopover } from "@/components/HelpPopover";
import { Plus } from "@/components/icons";
import { PluginIcon, pluginAvatar } from "@/components/PluginIcon";
import { StatusDot, type StatusTone } from "@/components/StatusDot";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import { useT } from "./i18n";
import { offersProviders, pluginState, pluginTone } from "./PluginList";
import type { PluginSummary } from "./plugin-model";
import type { Service } from "./service-model";

type Translate = ReturnType<typeof useT>;
type Side = "left" | "right";

// The map's geometry, in CSS pixels.
/** A plugin's circle. */
const NODE = 36;
/** The gateway's circle. */
const HUB = 64;
/** A plugin's name and what it did, beside its circle. */
const LABEL = 152;
/** Between a circle and its label. */
const GAP = 10;
/** Between the plugins of a side, top to bottom: as much as the map's
 * height gives them, within these bounds; past them it scrolls. */
const STEP = 56;
const MIN_STEP = 40;
const MARGIN = 20;
/** The row of the sides' headings. */
const HEADING = 36;
/** The shortest and the longest line from the gateway to a side. */
const MIN_REACH = 96;
const MAX_REACH = 300;
/** The size a map lays itself out at before it is measured, as in tests. */
const UNMEASURED = { width: 960, height: 520 };

interface Point {
  x: number;
  y: number;
}

/** A plugin, or the place an empty side offers one, on the map. */
interface Star extends Point {
  side: Side;
  plugin?: PluginSummary;
}

/**
 * Places a side's plugins on an arc around the gateway: evenly spaced top
 * to bottom, a few almost in a column, many bending round it like an orbit.
 */
function orbit(
  plugins: PluginSummary[],
  side: Side,
  center: Point,
  reach: number,
  step: number,
): Star[] {
  const half = ((Math.max(plugins.length, 1) - 1) * step) / 2;
  const radius = Math.max(half / 0.92, reach);
  const place = (dy: number) => {
    const dx = reach * Math.sqrt(Math.max(0, 1 - (dy / radius) ** 2));
    return side === "left" ? center.x - dx : center.x + dx;
  };
  if (plugins.length === 0) return [{ side, x: place(0), y: center.y }];
  return plugins.map((plugin, at) => {
    const dy = at * step - half;
    return { side, plugin, x: place(dy), y: center.y + dy };
  });
}

/** The whole of the arc a side's plugins sit on: half an orbit. */
function orbitPath(
  stars: Star[],
  center: Point,
  reach: number,
  step: number,
): string {
  const half = ((stars.length - 1) * step) / 2;
  const radius = Math.max(half / 0.92, reach);
  const points: string[] = [];
  for (let dy = -radius; dy <= radius + 0.5; dy += 4) {
    const dx = reach * Math.sqrt(Math.max(0, 1 - (dy / radius) ** 2));
    const x = stars[0].side === "left" ? center.x - dx : center.x + dx;
    points.push(`${x.toFixed(1)},${(center.y + dy).toFixed(1)}`);
  }
  return `M${points.join("L")}`;
}

/** A line from one circle's edge to another's. */
function spoke(from: Point, to: Point, fromRadius: number, toRadius: number) {
  const dx = to.x - from.x;
  const dy = to.y - from.y;
  const length = Math.hypot(dx, dy) || 1;
  const ux = dx / length;
  const uy = dy / length;
  return {
    a: { x: from.x + ux * fromRadius, y: from.y + uy * fromRadius },
    b: { x: to.x - ux * toRadius, y: to.y - uy * toRadius },
  };
}

function running(plugin: PluginSummary): boolean {
  return plugin.state === "loaded" && plugin.enabled;
}

/** A plugin's status on the map: its state, amber while it keeps failing. */
function starTone(plugin: PluginSummary): StatusTone {
  if (running(plugin) && (plugin.failures ?? 0) > 0) return "pending";
  return pluginTone(plugin);
}

/**
 * The line under a plugin's name: what it is doing, else its state. A
 * provider plugin says how many providers it runs, none until one is
 * added on the providers page.
 */
function starFact(
  plugin: PluginSummary,
  bound: number | undefined,
  t: Translate,
): string {
  if (!running(plugin)) return pluginState(plugin, t);
  if (plugin.running_tasks)
    return t("plugins.task.runningCount", { count: plugin.running_tasks });
  if (bound !== undefined)
    return bound > 0
      ? t("plugins.map.providers", { count: bound })
      : t("plugins.map.noProviders");
  if (plugin.calls)
    return t("plugins.metrics.callCount", { count: plugin.calls });
  return pluginState(plugin, t);
}

/**
 * The installed plugins as a star map: the gateway in the middle, the
 * plugins that enhance its requests on the left, where requests come in,
 * and those that connect it to upstream providers on the right. A line
 * that carries a signal is a plugin at work; a dashed one is off, a red one
 * did not load. A plugin opens from its star; the gateway opens the
 * settings of every plugin.
 */
export function PluginStarMap({
  plugins,
  services,
  matches,
  paused,
  className,
  onOpen,
  onHub,
  onAddEnhancer,
  onAddProvider,
}: {
  plugins: PluginSummary[];
  /** The providers, for how many each provider plugin runs. */
  services: Service[];
  /** Whether a plugin matches the search; the others fade. */
  matches?: (plugin: PluginSummary) => boolean;
  /** Every plugin is paused: requests pass none of them. */
  paused: boolean;
  className?: string;
  onOpen: (key: string) => void;
  onHub: () => void;
  onAddEnhancer?: () => void;
  onAddProvider?: () => void;
}) {
  const t = useT();
  const reducedMotion = useReducedMotion();
  const glow = `plugin-star-glow-${useId().replace(/[^A-Za-z0-9_-]/g, "")}`;
  const viewport = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState(UNMEASURED);
  const [active, setActive] = useState<string | null>(null);

  useLayoutEffect(() => {
    const element = viewport.current;
    if (!element || typeof ResizeObserver === "undefined") return;
    const measure = () => {
      const width = element.clientWidth;
      const height = element.clientHeight;
      // Hidden, as in a narrow window, it keeps its last size.
      if (width > 0 && height > 0)
        setSize((current) =>
          current.width === width && current.height === height
            ? current
            : { width, height },
        );
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  const enhancers = plugins.filter((plugin) => !offersProviders(plugin));
  const providers = plugins.filter(offersProviders);
  const tallest = Math.max(enhancers.length, providers.length, 1);
  const step =
    tallest > 1
      ? Math.min(
          STEP,
          Math.max(
            MIN_STEP,
            (size.height - HEADING - MARGIN * 2 - NODE) / (tallest - 1),
          ),
        )
      : STEP;
  const height = Math.max(
    size.height,
    HEADING + MARGIN * 2 + NODE + (tallest - 1) * step,
  );
  const center = { x: size.width / 2, y: HEADING + (height - HEADING) / 2 };
  const reach = Math.max(
    MIN_REACH,
    Math.min(size.width / 2 - NODE / 2 - GAP - LABEL - MARGIN, MAX_REACH),
  );
  const left = orbit(enhancers, "left", center, reach, step);
  const right = orbit(providers, "right", center, reach, step);
  const stars = [...left, ...right];
  const faded = (plugin?: PluginSummary) =>
    plugin !== undefined && matches !== undefined && !matches(plugin);
  // A provider plugin carries requests once a provider runs it.
  const bound = (plugin: PluginSummary) =>
    offersProviders(plugin)
      ? services.filter(
          (service) =>
            service.kind === "plugin" && service.plugin?.key === plugin.key,
        ).length
      : undefined;

  return (
    <div
      aria-label={t("plugins.map.label")}
      className={cn(
        "relative min-h-0 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain rounded-md border bg-card",
        className,
      )}
      data-tab-scroller
      data-testid="plugin-star-map"
      ref={viewport}
      role="group"
    >
      {/* A faint field of points, the map's sky. */}
      <div
        className="relative"
        style={{
          height,
          backgroundImage:
            "radial-gradient(circle, var(--border) 1px, transparent 1.25px)",
          backgroundSize: "22px 22px",
        }}
      >
        <svg
          aria-hidden="true"
          className="pointer-events-none absolute inset-0"
          height={height}
          width={size.width}
        >
          <defs>
            <radialGradient id={glow}>
              <stop
                offset="0%"
                style={{ stopColor: "var(--primary)", stopOpacity: 0.14 }}
              />
              <stop
                offset="100%"
                style={{ stopColor: "var(--primary)", stopOpacity: 0 }}
              />
            </radialGradient>
          </defs>
          <circle
            cx={center.x}
            cy={center.y}
            fill={`url(#${glow})`}
            r={HUB * 1.8}
          />
          <circle
            className="fill-none stroke-muted-foreground"
            cx={center.x}
            cy={center.y}
            r={reach * 0.55}
            strokeDasharray="1 6"
            strokeLinecap="round"
            strokeOpacity={0.3}
          />
          {[left, right].map((side) => (
            <path
              className="fill-none stroke-muted-foreground"
              d={orbitPath(side, center, reach, step)}
              key={side[0].side}
              strokeDasharray="1 6"
              strokeLinecap="round"
              strokeOpacity={0.3}
            />
          ))}
          {stars.map((star, index) => {
            const { a, b } = spoke(center, star, HUB / 2 + 6, NODE / 2 + 4);
            const plugin = star.plugin;
            const on = plugin !== undefined && running(plugin) && !paused;
            // A provider plugin no provider runs carries nothing yet.
            const live = on && (bound(plugin) ?? 1) > 0;
            const failed = plugin?.state === "failed";
            const lit = plugin !== undefined && active === plugin.key;
            // Requests come in on the left and go out on the right.
            const route =
              star.side === "left"
                ? `M${b.x},${b.y}L${a.x},${a.y}`
                : `M${a.x},${a.y}L${b.x},${b.y}`;
            return (
              <g
                className={cn(
                  "transition-opacity",
                  (faded(plugin) || (active !== null && !lit)) && "opacity-30",
                )}
                key={plugin?.key ?? `add:${star.side}`}
              >
                <line
                  className={cn(
                    on
                      ? "stroke-primary"
                      : failed
                        ? "stroke-destructive"
                        : "stroke-muted-foreground",
                  )}
                  strokeDasharray={on ? undefined : "4 5"}
                  strokeLinecap="round"
                  strokeOpacity={on ? (lit ? 0.9 : live ? 0.45 : 0.25) : 0.45}
                  strokeWidth={lit ? 2 : 1.25}
                  x1={a.x}
                  x2={b.x}
                  y1={a.y}
                  y2={b.y}
                />
                {live && !reducedMotion ? (
                  // A new route is a new signal: browsers keep a running
                  // motion on the path it started with.
                  <circle className="fill-primary" key={route} r={2.5}>
                    <animateMotion
                      begin={`${(index % 6) * 0.45}s`}
                      dur="2.8s"
                      path={route}
                      repeatCount="indefinite"
                    />
                    <animate
                      attributeName="opacity"
                      begin={`${(index % 6) * 0.45}s`}
                      dur="2.8s"
                      keyTimes="0;0.15;0.85;1"
                      repeatCount="indefinite"
                      values="0;1;1;0"
                    />
                  </circle>
                ) : null}
              </g>
            );
          })}
        </svg>
        <SideHeading
          align="left"
          count={enhancers.length}
          help={t("plugins.map.enhancersHelp")}
          title={t("plugins.groups.extensions")}
        />
        <SideHeading
          align="right"
          count={providers.length}
          help={t("plugins.map.providersHelp")}
          title={t("plugins.groups.providers")}
        />
        <Hub center={center} onOpen={onHub} paused={paused} />
        {stars.map((star) =>
          star.plugin ? (
            <PluginStar
              bound={bound(star.plugin)}
              faded={faded(star.plugin)}
              key={star.plugin.key}
              onActive={setActive}
              onOpen={() => onOpen(star.plugin!.key)}
              plugin={star.plugin}
              star={star}
            />
          ) : (
            <AddStar
              key={`add:${star.side}`}
              label={
                star.side === "left"
                  ? t("plugins.map.addEnhancer")
                  : t("plugins.map.addProvider")
              }
              onAdd={star.side === "left" ? onAddEnhancer : onAddProvider}
              star={star}
            />
          ),
        )}
      </div>
    </div>
  );
}

/** A side's title and count, its kind of plugin explained behind ?. */
function SideHeading({
  align,
  title,
  count,
  help,
}: {
  align: Side;
  title: string;
  count: number;
  help: string;
}) {
  const t = useT();
  return (
    <div
      className={cn(
        "absolute top-3 flex items-center gap-1.5 text-xs font-medium text-muted-foreground",
        align === "left" ? "left-4" : "right-4 flex-row-reverse",
      )}
    >
      <span className="flex items-center gap-1.5">
        {title}
        <Badge className="px-1.5 py-0 tabular-nums" variant="secondary">
          {count}
        </Badge>
      </span>
      <HelpPopover label={t("plugins.map.helpLabel", { kind: title })}>
        <p>{help}</p>
      </HelpPopover>
    </div>
  );
}

/** The gateway: the logo, and whether requests pass the plugins. */
function Hub({
  center,
  paused,
  onOpen,
}: {
  center: Point;
  paused: boolean;
  onOpen: () => void;
}) {
  const t = useT();
  return (
    <Button
      aria-label={t("plugins.host.open")}
      className="group absolute h-auto -translate-x-1/2 flex-col gap-2 rounded-xl px-2 pt-0 pb-1 font-normal hover:bg-transparent"
      data-testid="plugin-star-hub"
      onClick={onOpen}
      style={{ left: center.x, top: center.y - HUB / 2 }}
      type="button"
      variant="ghost"
    >
      <span className="relative grid size-16 place-items-center rounded-full border bg-card shadow-sm transition-shadow group-hover:shadow-md">
        <span
          aria-hidden="true"
          className={cn(
            "absolute -inset-2 rounded-full",
            paused ? "bg-warning/10" : "bg-primary/[0.06]",
          )}
        />
        <img
          alt=""
          className="relative size-9"
          draggable={false}
          src={astrlinkLogo}
        />
      </span>
      <span className="grid justify-items-center gap-0.5">
        <span className="text-xs font-semibold text-foreground">AstrLink</span>
        <span className="flex items-center gap-1 text-micro text-muted-foreground">
          <StatusDot tone={paused ? "pending" : "positive"} />
          {paused ? t("plugins.host.pausedShort") : t("plugins.map.hubRunning")}
        </span>
      </span>
    </Button>
  );
}

/** Where a star's circle sits: its label away from the gateway. */
function starStyle(star: Star) {
  return star.side === "left"
    ? { left: star.x + NODE / 2, top: star.y - NODE / 2 }
    : { left: star.x - NODE / 2, top: star.y - NODE / 2 };
}

function StarButton({
  star,
  faded = false,
  children,
  ...props
}: Omit<ComponentProps<"button">, "style"> & {
  star: Star;
  faded?: boolean;
  children: ReactNode;
}) {
  return (
    <Button
      className={cn(
        "group absolute h-auto gap-2.5 rounded-lg p-0 font-normal transition-opacity hover:bg-transparent",
        star.side === "left"
          ? "-translate-x-full flex-row-reverse text-right"
          : "text-left",
        faded && "opacity-35",
      )}
      data-faded={faded || undefined}
      data-side={star.side}
      style={starStyle(star)}
      type="button"
      variant="ghost"
      {...props}
    >
      {children}
    </Button>
  );
}

function PluginStar({
  plugin,
  star,
  bound,
  faded,
  onOpen,
  onActive,
}: {
  plugin: PluginSummary;
  star: Star;
  /** The providers that run a provider plugin. */
  bound?: number;
  faded: boolean;
  onOpen: () => void;
  onActive: (key: string | null) => void;
}) {
  const t = useT();
  const tone = starTone(plugin);
  const off = !running(plugin);
  return (
    <StarButton
      data-plugin-key={plugin.key}
      data-testid="plugin-star"
      faded={faded}
      onBlur={() => onActive(null)}
      onClick={onOpen}
      onFocus={() => onActive(plugin.key)}
      onMouseEnter={() => onActive(plugin.key)}
      onMouseLeave={() => onActive(null)}
      star={star}
    >
      <span
        className={cn(
          "relative grid size-9 shrink-0 place-items-center rounded-full border bg-card shadow-xs transition-[border-color,box-shadow] group-hover:border-primary/40 group-hover:shadow-sm",
          plugin.state === "failed" && "border-destructive/50",
          off && plugin.state !== "failed" && "border-dashed",
        )}
      >
        <PluginIcon
          className={cn("size-5 rounded-sm", off && "opacity-55 grayscale")}
          icon={plugin.icon}
          label={pluginAvatar(plugin)}
        />
        <StatusDot
          className="absolute right-0 bottom-0 size-2.5 ring-2 ring-card"
          tone={tone}
        />
      </span>
      <span className="grid w-38 min-w-0 gap-0.5">
        <span className="truncate text-xs font-medium text-foreground">
          {plugin.name}
        </span>
        <span
          className={cn(
            "truncate text-micro",
            plugin.state === "failed"
              ? "text-danger-foreground"
              : plugin.running_tasks
                ? "text-warning-foreground"
                : "text-muted-foreground",
          )}
        >
          {starFact(plugin, bound, t)}
        </span>
      </span>
    </StarButton>
  );
}

/** The place of an empty side, which leads to where its plugins come from. */
function AddStar({
  star,
  label,
  onAdd,
}: {
  star: Star;
  label: string;
  onAdd?: () => void;
}) {
  return (
    <StarButton
      data-testid="plugin-star-add"
      disabled={!onAdd}
      onClick={onAdd}
      star={star}
    >
      <span className="grid size-9 shrink-0 place-items-center rounded-full border border-dashed bg-card/70 text-muted-foreground transition-colors group-hover:border-primary/40 group-hover:text-primary">
        <Plus aria-hidden="true" className="size-4" />
      </span>
      <span className="w-38 truncate text-xs text-muted-foreground group-hover:text-foreground">
        {label}
      </span>
    </StarButton>
  );
}
