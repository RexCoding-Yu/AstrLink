import { currentLocale, type Locale } from "./i18n";

interface CompactUnit {
  threshold: number;
  divisor: number;
  suffix: string;
}

/**
 * Compact number rendering, written by hand rather than through
 * `Intl.NumberFormat`'s `notation: "compact"`, because the locales need
 * different grouping (thousands versus myriads) and the engine's rounding of
 * `4664.6695万` is not stable across runtimes.
 */
const UNITS: Readonly<Record<Locale, readonly CompactUnit[]>> = {
  en: [
    { threshold: 1_000_000_000, divisor: 1_000_000_000, suffix: "B" },
    { threshold: 1_000_000, divisor: 1_000_000, suffix: "M" },
    { threshold: 1_000, divisor: 1_000, suffix: "K" },
  ],
  ja: [
    { threshold: 100_000_000, divisor: 100_000_000, suffix: "億" },
    { threshold: 10_000, divisor: 10_000, suffix: "万" },
  ],
  "zh-CN": [
    { threshold: 100_000_000, divisor: 100_000_000, suffix: "亿" },
    { threshold: 10_000, divisor: 10_000, suffix: "万" },
  ],
};

const MAX_FRACTION_DIGITS = 2;

/**
 * Rounds to at most two fraction digits and drops trailing zeros, so `1.00万`
 * renders as `1万`. The mantissa stays ungrouped: `4664.67万`, not `4,664.67万`.
 */
function trimFraction(value: number): string {
  return `${Number(value.toFixed(MAX_FRACTION_DIGITS))}`;
}

/** Full grouped digits, used for exact tooltips beside a compact value. */
export function formatExactNumber(value: number): string {
  if (!Number.isFinite(value)) return "—";
  return value.toLocaleString(currentLocale());
}

/**
 * Abbreviate large counts for the active language: `K`/`M`/`B` in English and
 * `万`/`亿` or `万`/`億` in Chinese and Japanese. Values below the first unit
 * keep their grouped digits.
 */
export function formatCompactNumber(value: number): string {
  if (!Number.isFinite(value)) return "—";
  const magnitude = Math.abs(value);
  for (const unit of UNITS[currentLocale()]) {
    if (magnitude < unit.threshold) continue;
    return `${trimFraction(value / unit.divisor)}${unit.suffix}`;
  }
  return formatExactNumber(value);
}

/** A share too small to round up still reads as present. */
export function formatShare(percent: number): string {
  if (percent > 0 && percent < 1) return "<1%";
  return `${Math.round(percent)}%`;
}
