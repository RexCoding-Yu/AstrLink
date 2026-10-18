import { currentLocale } from "./i18n";

/** Recessive axis ink for Recharts charts. */
export const AXIS_TICK = {
  fill: "var(--muted-foreground)",
  fontSize: 11,
} as const;
export const AXIS_LINE = { stroke: "var(--border)" } as const;

const dayFormats = new Map<string, Intl.DateTimeFormat>();

/** Renders a `YYYY-MM-DD` bucket key in the active locale. */
export function dayLabel(date: string, width: "long" | "short"): string {
  const [year, month, day] = date.split("-").map(Number);
  if (!year || !month || !day) return date;
  const locale = currentLocale();
  const key = `${locale}:${width}`;
  let format = dayFormats.get(key);
  if (!format) {
    format = new Intl.DateTimeFormat(locale, {
      ...(width === "long" ? { year: "numeric" } : {}),
      month: "short",
      day: "numeric",
    });
    dayFormats.set(key, format);
  }
  return format.format(new Date(year, month - 1, day));
}

export function chartPointLabel(point: {
  date: string;
  hour?: number;
}): string {
  const day = dayLabel(point.date, "long");
  if (point.hour == null) return day;
  return `${day} ${String(point.hour).padStart(2, "0")}:00`;
}

export function chartSlot(point: { date: string; hour?: number }): string {
  if (point.hour == null) return point.date;
  return `${point.date}T${String(point.hour).padStart(2, "0")}`;
}

export function hourTickLabel(slot: string): string {
  const hour = slot.split("T")[1];
  if (hour == null) return slot;
  return `${String(Number(hour)).padStart(2, "0")}:00`;
}

/** Day ticks to skip so a long range keeps about five readable labels. */
export function xAxisInterval(count: number): number | "preserveStartEnd" {
  if (count <= 7) return 0;
  return Math.max(1, Math.ceil(count / 5) - 1);
}
