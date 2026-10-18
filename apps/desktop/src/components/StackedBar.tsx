import type { HTMLAttributes } from "react";

import { cn } from "@/lib/utils";

export interface StackedBarSegment {
  key: string;
  value: number;
  /** Fill utility for the segment, such as `bg-primary`. */
  className: string;
  /** Hover text for the segment. */
  title?: string;
}

/**
 * One horizontal part-to-whole bar. Segments touch through a 2px gap in the
 * surface colour rather than a stroke, and a small share keeps a sliver so it
 * stays visible. Always pair it with a legend that names each segment.
 */
export function StackedBar({
  className,
  label,
  segments,
  ...props
}: Omit<HTMLAttributes<HTMLDivElement>, "children"> & {
  label: string;
  segments: readonly StackedBarSegment[];
}) {
  const visible = segments.filter((segment) => segment.value > 0);
  return (
    <div
      aria-label={label}
      className={cn(
        "flex h-2.5 w-full min-w-0 gap-0.5 overflow-hidden rounded-full",
        visible.length === 0 && "bg-muted",
        className,
      )}
      data-slot="stacked-bar"
      role="img"
      {...props}
    >
      {visible.map((segment) => (
        <span
          className={cn("h-full min-w-1", segment.className)}
          data-segment={segment.key}
          key={segment.key}
          style={{ flexBasis: 0, flexGrow: segment.value }}
          title={segment.title}
        />
      ))}
    </div>
  );
}
