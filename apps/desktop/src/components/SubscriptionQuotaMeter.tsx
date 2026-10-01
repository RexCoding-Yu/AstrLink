import { UsageMeter, UsageMeterRow } from "./UsageMeter";
import { useT } from "../i18n";
import { useQuotaDisplayMode } from "../quota-display";
import { usageBarPercent, usageWindowTone } from "../subscription-usage-model";

/** All subscription surfaces share the display mode; warnings still follow usage. */
export function SubscriptionQuotaMeter({
  label,
  accessibleLabel,
  caption,
  captionDetail,
  usedPercent,
  limitReached,
  compact = false,
  layout = "stack",
  className,
}: {
  label: string;
  /** Row layout: names the bar when the label leans on a group header. */
  accessibleLabel?: string;
  caption?: string | null;
  /** Row layout: unabbreviated caption for the hover title. */
  captionDetail?: string | null;
  usedPercent: number;
  limitReached?: boolean;
  compact?: boolean;
  /** `row` renders one line of a `UsageMeterGrid` whose header names the mode. */
  layout?: "stack" | "row";
  className?: string;
}) {
  const t = useT();
  const mode = useQuotaDisplayMode();
  const used = usageBarPercent(usedPercent);
  const value = mode === "remaining" ? 100 - used : used;
  const percent = Math.round(value);
  const toneLevel = usageWindowTone(usedPercent, limitReached);
  const meter = {
    caption,
    className,
    label,
    value,
    valueLabel: t(
      mode === "remaining" ? "usage.remainingPercent" : "usage.usedPercent",
      { percent },
    ),
    warning: limitReached || used >= 100 ? t("usage.limitReached") : undefined,
    tone:
      toneLevel === "ok"
        ? ("success" as const)
        : toneLevel === "critical"
          ? ("destructive" as const)
          : ("warning" as const),
  };
  if (layout === "row") {
    return (
      <UsageMeterRow
        {...meter}
        accessibleLabel={accessibleLabel}
        captionDetail={captionDetail}
        valueText={`${percent}%`}
      />
    );
  }
  return <UsageMeter {...meter} compact={compact} />;
}
