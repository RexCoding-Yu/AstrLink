import { useEffect, useState } from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";

import astrlinkLogo from "./assets/astrlink-logo.svg";
import { LockKeyhole } from "./components/icons";
import { SegmentedControl } from "./components/SegmentedControl";
import { StrengthMeter } from "./components/StrengthMeter";
import {
  adaptivePresets,
  adaptiveRange,
  reasoningLevels,
  reasoningModes,
  type AdaptivePreset,
  type LevelRange,
  type ReasoningLevel,
  type ReasoningMode,
  type RoutingSettings,
} from "./failure-policy-model";
import { useT } from "./i18n";
import { cn } from "./lib/utils";

/**
 * Steps through a looping animation and returns the current step and loop:
 * each step starts at its time in milliseconds, and the last one holds
 * before the loop starts over. Reduced motion rests on the last step.
 */
function useLoopStep(times: readonly number[], hold: number, playing: boolean) {
  const reducedMotion = useReducedMotion();
  const [clock, setClock] = useState({ step: 0, cycle: 0 });
  useEffect(() => {
    if (!playing || reducedMotion) return;
    const timers = times
      .slice(1)
      .map((at, index) =>
        window.setTimeout(
          () => setClock((current) => ({ ...current, step: index + 1 })),
          at,
        ),
      );
    timers.push(
      window.setTimeout(
        () => setClock((current) => ({ step: 0, cycle: current.cycle + 1 })),
        times[times.length - 1] + hold,
      ),
    );
    return () => timers.forEach(window.clearTimeout);
  }, [clock.cycle, hold, playing, reducedMotion, times]);
  if (!playing) return { step: 0, cycle: clock.cycle };
  return {
    step: reducedMotion ? times.length - 1 : clock.step,
    cycle: clock.cycle,
  };
}

const rank = (level: ReasoningLevel) => reasoningLevels.indexOf(level);

/**
 * Where a request with hits signals lands inside range, as the gateway picks
 * it: the lower end without signals, a level higher for each, and the upper
 * end for all three. capped marks a request that would pass the upper end.
 */
export function rangeLevel(range: LevelRange, hits: number) {
  const wanted = rank(range[0]) + hits;
  const top = rank(range[1]);
  return {
    level: reasoningLevels[hits >= 3 ? top : Math.min(wanted, top)],
    capped: wanted > top,
  };
}

// What three clients pick in the passthrough and fixed examples.
const clientPicks = ["low", "high", "medium"] as const;
// Each card sets off at an odd step and comes out of AstrLink at the next
// one, about when it passes the logo.
const flowTimes = [0, 700, 1200, 1400, 1900, 2100, 2600] as const;
const flowHold = 1600;

/**
 * Passthrough and fixed: request cards pass through AstrLink. Passthrough
 * forwards each card's own level. Fixed gives every card the panel's level
 * and highlights the cards whose level changed; off reads as no thinking.
 */
function FlowVisual({ fixed }: { fixed?: ReasoningLevel }) {
  const t = useT();
  const reducedMotion = useReducedMotion();
  const { step, cycle } = useLoopStep(flowTimes, flowHold, true);
  const departed = Math.ceil(step / 2);
  const arrived = Math.floor(step / 2);
  const name = (level: ReasoningLevel) =>
    level === "off"
      ? t("routing.thinkingDemo.noThinking")
      : t(`routing.thinkingLevels.${level}`);
  return (
    <div
      aria-hidden="true"
      className="relative h-28 overflow-hidden rounded-md border bg-card"
      data-arrived={arrived}
      data-flow={fixed ? "fixed" : "passthrough"}
      data-visual="flow"
    >
      <span className="absolute top-1.5 left-2.5 text-micro text-muted-foreground">
        {t("routing.thinkingDemo.client")}
      </span>
      <span className="absolute top-1.5 right-2.5 text-micro text-muted-foreground">
        {t("routing.thinkingDemo.model")}
      </span>
      {/* What each client sent stays outlined where its card started. */}
      {clientPicks.map((pick, index) => (
        <span
          className="absolute left-[5%] flex h-5 items-center gap-1 rounded-sm border border-dashed px-1.5 text-micro whitespace-nowrap text-muted-foreground"
          key={pick}
          style={{ top: 30 + index * 25 }}
        >
          <StrengthMeter level={rank(pick)} size="sm" />
          {name(pick)}
        </span>
      ))}
      {clientPicks.map((pick, index) => {
        const out = index < arrived;
        const level = out && fixed ? fixed : pick;
        const changed = level !== pick;
        return (
          <motion.span
            animate={{ left: index < departed ? "71%" : "5%", opacity: 1 }}
            className={cn(
              "absolute flex h-5 items-center gap-1 rounded-sm border px-1.5 text-micro font-medium whitespace-nowrap",
              changed
                ? "border-primary/40 bg-accent text-primary"
                : "bg-background text-foreground",
            )}
            data-arrived={out || undefined}
            data-card={pick}
            data-changed={changed || undefined}
            initial={reducedMotion ? false : { left: "5%", opacity: 0 }}
            key={`${cycle}-${pick}`}
            style={{ top: 30 + index * 25 }}
            transition={{
              left: { duration: reducedMotion ? 0 : 0.9, ease: "easeInOut" },
              opacity: { duration: reducedMotion ? 0 : 0.25 },
            }}
          >
            <StrengthMeter active level={rank(level)} size="sm" />
            {name(level)}
          </motion.span>
        );
      })}
      {/* AstrLink as the protocol mode help draws it, centered on the middle
          lane and above the cards so they pass through it. */}
      <span
        className="absolute left-1/2 z-10 flex -translate-x-1/2 flex-col items-center gap-1"
        data-gateway
        style={{ top: 43 }}
      >
        <span className="flex size-11 items-center justify-center rounded-md border bg-card">
          <img
            alt=""
            className="size-7"
            height={28}
            src={astrlinkLogo}
            width={28}
          />
        </span>
        <span className="rounded-sm bg-card px-1 text-micro font-medium text-muted-foreground">
          AstrLink
        </span>
      </span>
    </div>
  );
}

// The adaptive example's requests and the signals each shows.
const requestCards = [
  { id: "quick", hits: 0 },
  { id: "bug", hits: 1 },
  { id: "afternoon", hits: 3 },
] as const;
const scaleTop = 14;
const scaleGap = 26;
const scaleY = (level: ReasoningLevel) =>
  scaleTop + (4 - rank(level)) * scaleGap;
// The brackets settle before the cards move.
const bracketMove = 0.35;
const cardMove = 0.5;

/**
 * Adaptive: a thermometer whose brackets mark the range. Three requests sit
 * where the gateway sends them; when the range changes the brackets move
 * first, then the cards. A card that would pass the upper end stops there
 * and the upper bracket gives a small bump.
 */
function ThermometerVisual({ range }: { range: LevelRange }) {
  const t = useT();
  const reducedMotion = useReducedMotion();
  const [low, high] = range;
  const placed = requestCards.map((card) => ({
    ...card,
    ...rangeLevel(range, card.hits),
  }));
  const bump = placed.some((card) => card.capped);
  const move = (delay: number, duration: number) => ({
    delay: reducedMotion ? 0 : delay,
    duration: reducedMotion ? 0 : duration,
    ease: "easeInOut" as const,
  });
  const bottom = scaleY("off");
  return (
    <div
      aria-hidden="true"
      className="relative h-40 overflow-hidden rounded-md border bg-card"
      data-range={`${low}..${high}`}
      data-visual="thermometer"
    >
      {reasoningLevels.map((stop) => (
        <span key={stop}>
          <span
            className={cn(
              "absolute left-0 w-11 -translate-y-1/2 text-right text-micro whitespace-nowrap",
              rank(stop) >= rank(low) && rank(stop) <= rank(high)
                ? "font-medium text-foreground"
                : "text-muted-foreground",
            )}
            style={{ top: scaleY(stop) }}
          >
            {t(`routing.thinkingLevels.${stop}`)}
          </span>
          <span
            className="absolute right-2 left-20 border-t border-dashed border-border"
            style={{ top: scaleY(stop) }}
          />
        </span>
      ))}
      {/* The tube and its bulb; the column rises to the upper end. */}
      <span
        className="absolute left-[3.25rem] w-2.5 rounded-t-full bg-muted"
        style={{ top: scaleY("max") - 7, height: bottom - scaleY("max") + 14 }}
      />
      <span
        className="absolute left-[2.95rem] size-5 rounded-full bg-primary/35"
        style={{ top: bottom + 5 }}
      />
      <motion.span
        animate={{ top: scaleY(high) }}
        className="absolute left-[3.25rem] w-2.5 rounded-t-full bg-primary/25"
        initial={reducedMotion ? false : { top: bottom }}
        style={{ bottom: 160 - bottom - 12 }}
        transition={move(0, bracketMove)}
      />
      {/* The range, a little past each end so a single level shows too. */}
      <motion.span
        animate={{
          top: scaleY(high) - 3,
          height: scaleY(low) - scaleY(high) + 6,
        }}
        className="absolute left-[3.25rem] w-2.5 rounded-full bg-primary"
        data-range-fill
        initial={reducedMotion ? false : { top: bottom - 3, height: 6 }}
        transition={move(0, bracketMove)}
      />
      {/* The brackets: the upper end and the lower end. */}
      <motion.span
        animate={{ top: scaleY(high) - 1 }}
        className="absolute left-[4.25rem]"
        data-bracket="max"
        initial={reducedMotion ? false : { top: bottom - 1 }}
        transition={move(0, bracketMove)}
      >
        <motion.span
          animate={{ y: bump && !reducedMotion ? [0, -3, 0] : 0 }}
          className="block h-2.5 w-3 rounded-tr-sm border-t-2 border-r-2 border-primary"
          data-bump={bump || undefined}
          key={`${low}-${high}`}
          transition={{
            delay: reducedMotion ? 0 : bracketMove + cardMove,
            duration: reducedMotion ? 0 : 0.3,
          }}
        />
      </motion.span>
      <motion.span
        animate={{ top: scaleY(low) - 9 }}
        className="absolute left-[4.25rem] block h-2.5 w-3 rounded-br-sm border-r-2 border-b-2 border-primary"
        data-bracket="min"
        initial={reducedMotion ? false : { top: bottom - 9 }}
        transition={move(0, bracketMove)}
      />
      <div className="absolute inset-y-0 right-2 left-[5.5rem] grid grid-cols-3 gap-1.5">
        {placed.map((card) => (
          <span className="relative" key={card.id}>
            <motion.span
              animate={{ top: scaleY(card.level) }}
              className={cn(
                "absolute inset-x-0 -translate-y-1/2 rounded-sm border bg-background px-1.5 py-0.5 text-center text-micro leading-tight text-foreground",
                card.capped && "border-primary/50",
              )}
              data-capped={card.capped || undefined}
              data-level={card.level}
              data-request={card.id}
              initial={reducedMotion ? false : { top: bottom }}
              transition={move(bracketMove, cardMove)}
            >
              {t(`routing.thinkingDemo.requests.${card.id}`)}
            </motion.span>
          </span>
        ))}
      </div>
    </div>
  );
}

/**
 * The thinking explanation. It opens on the panel's mode and range; its
 * switchers change only the example.
 */
export function ThinkingDemo({ settings }: { settings: RoutingSettings }) {
  const t = useT();
  const [mode, setMode] = useState<ReasoningMode>(
    settings.reasoning_mode ?? "passthrough",
  );
  const [preset, setPreset] = useState<AdaptivePreset>(
    settings.adaptive_preset ?? "everyday",
  );
  const level = settings.reasoning_level ?? "medium";
  const range = adaptiveRange({ ...settings, adaptive_preset: preset });
  let text = t("routing.thinkingDemo.text.passthrough");
  if (mode === "fixed")
    text =
      level === "off"
        ? t("routing.thinkingDemo.text.fixedOff")
        : t("routing.thinkingDemo.text.fixed", {
            level: t(`routing.thinkingLevels.${level}`),
          });
  else if (mode === "adaptive") text = t(`routing.thinkingDemo.text.${preset}`);
  return (
    <div className="grid gap-2.5" data-thinking-demo={mode}>
      <SegmentedControl
        label={t("routing.thinkingModeLabel")}
        options={reasoningModes.map((value) => ({
          value,
          label: t(`routing.thinkingModes.${value}`),
        }))}
        value={mode}
        onValueChange={setMode}
      />
      {mode === "adaptive" ? (
        <SegmentedControl
          label={t("routing.thinkingRangeLabel")}
          options={adaptivePresets.map((value) => ({
            value,
            label: t(`routing.thinkingPresets.${value}`),
          }))}
          value={preset}
          onValueChange={setPreset}
        />
      ) : null}
      {mode === "adaptive" ? (
        <ThermometerVisual range={range} />
      ) : (
        <FlowVisual fixed={mode === "fixed" ? level : undefined} key={mode} />
      )}
      <p className="m-0 text-xs leading-relaxed text-muted-foreground">
        {text}
      </p>
    </div>
  );
}

// The prompt grows in steps, then holds before starting over.
const promptTimes = [0, 900, 1800] as const;
const promptHold = 2200;

function excerpt(text: string, fallback: string) {
  return text.trim().split("\n")[0].trim() || fallback;
}

function SkeletonLines({ widths }: { widths: readonly string[] }) {
  return (
    <span className="grid gap-1 pt-0.5">
      {widths.map((width) => (
        <span
          className="h-1.5 rounded-full bg-muted-foreground/20"
          key={width}
          style={{ width }}
        />
      ))}
    </span>
  );
}

/**
 * A place for one of the user's texts. It stays the same height while empty,
 * so the popover keeps its size, and the text moves into it at its step.
 */
function PromptSlot({
  block,
  filled,
  from,
  label,
  text,
}: {
  block: "prefix" | "suffix";
  filled: boolean;
  from: { x?: number; y?: number };
  label: string;
  text: string;
}) {
  const reducedMotion = useReducedMotion();
  return (
    <li
      className="relative h-6 rounded-sm border border-dashed border-primary/25"
      data-prompt-slot={block}
    >
      <AnimatePresence initial={false}>
        {filled ? (
          <motion.span
            animate={{ opacity: 1, x: 0, y: 0 }}
            className="absolute inset-[-1px] flex min-w-0 items-center gap-1.5 rounded-sm border border-primary/30 bg-primary/10 px-1.5 text-micro text-primary"
            data-prompt-block={block}
            exit={{ opacity: 0 }}
            initial={reducedMotion ? false : { opacity: 0, ...from }}
            transition={{ duration: reducedMotion ? 0 : 0.45, ease: "easeOut" }}
          >
            <span className="shrink-0 font-medium">{label}</span>
            <span className="truncate">{text}</span>
          </motion.span>
        ) : null}
      </AnimatePresence>
    </li>
  );
}

/**
 * The system prompt a model receives, assembled block by block. The user's
 * two texts move into their places around the client's own prompt.
 */
export function PromptDemo({
  enabled,
  prefix,
  suffix,
}: {
  enabled: boolean;
  prefix: string;
  suffix: string;
}) {
  const t = useT();
  const { step } = useLoopStep(promptTimes, promptHold, enabled);
  return (
    <div
      className="grid gap-3"
      data-prompt-demo={enabled ? "on" : "off"}
      data-prompt-step={step}
    >
      <div
        aria-hidden="true"
        className="grid grid-cols-[minmax(0,1fr)_8.5rem] items-start gap-2"
      >
        <section
          className="rounded-md border bg-card p-2"
          data-prompt-card="main"
        >
          <h4 className="m-0 text-micro font-medium text-muted-foreground">
            {t("routing.promptDemo.title")}
          </h4>
          <ol className="m-0 mt-1.5 grid list-none gap-1 p-0">
            <li
              className="flex h-6 min-w-0 items-center gap-1 rounded-sm bg-muted px-1.5 text-micro text-muted-foreground"
              data-prompt-block="identity"
            >
              <LockKeyhole
                animateOnHover={false}
                aria-hidden="true"
                className="shrink-0"
                size={11}
              />
              <span className="truncate">
                {t("routing.promptDemo.identity")}
              </span>
            </li>
            <PromptSlot
              block="prefix"
              filled={step >= 1}
              from={{ y: -10 }}
              label={t("routing.promptDemo.prefix")}
              text={excerpt(prefix, t("routing.promptPrefixPlaceholder"))}
            />
            <li
              className="grid min-w-0 rounded-sm border border-dashed px-1.5 py-1 text-micro text-muted-foreground"
              data-prompt-block="client"
            >
              <span>{t("routing.promptDemo.client")}</span>
              <SkeletonLines widths={["88%"]} />
            </li>
            <PromptSlot
              block="suffix"
              filled={step >= 2}
              from={{ x: 12 }}
              label={t("routing.promptDemo.suffix")}
              text={excerpt(suffix, t("routing.promptSuffixPlaceholder"))}
            />
          </ol>
        </section>
        <section
          className="grid gap-1.5 rounded-md border bg-card p-2"
          data-prompt-card="small-tasks"
        >
          <h4 className="m-0 text-micro font-medium text-muted-foreground">
            {t("routing.promptDemo.smallTasks")}
          </h4>
          <span className="grid gap-1 rounded-sm bg-muted px-1.5 py-1 text-micro text-muted-foreground">
            <span className="line-clamp-2">
              {t("routing.promptDemo.titleTask")}
            </span>
            <SkeletonLines widths={["70%"]} />
          </span>
          <span className="w-fit rounded-sm border px-1.5 text-micro text-muted-foreground">
            {t("routing.promptDemo.unchanged")}
          </span>
        </section>
      </div>
      <p className="m-0 text-xs leading-relaxed text-muted-foreground">
        {enabled
          ? t("routing.promptDemo.text.on")
          : t("routing.promptDemo.text.off")}
      </p>
    </div>
  );
}
