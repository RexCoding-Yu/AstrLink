import { useRef, useState, type ReactNode } from "react";

import { DataRow } from "@/components/DataRow";
import { Button } from "@/components/ui/button";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

const ROW =
  "grid h-15 min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 border-b px-4 py-2.5 last:border-b-0";

/**
 * One line of a ranked breakdown: a name over its share bar, and the figure
 * the list is ranked by over a quiet detail such as a call count. Rows with
 * an `onClick` become buttons that open the item; `hint` says what that
 * opens, `labelDetail` shows while the pointer rests on the name, and
 * `action` is a control of its own that appears with the pointer or focus.
 */
export function BreakdownRow({
  action,
  detail,
  detailTitle,
  hint,
  label,
  labelDetail,
  labelTitle = label,
  leading,
  onClick,
  share,
  value,
  valueTitle,
}: {
  action?: ReactNode;
  detail?: ReactNode;
  detailTitle?: string;
  hint?: string;
  label: string;
  labelDetail?: ReactNode;
  labelTitle?: string;
  leading?: ReactNode;
  onClick?: () => void;
  /** Bar length as a percentage of the largest row; null hides the bar. */
  share: number | null;
  value: ReactNode;
  valueTitle?: string;
}) {
  const [hintOpen, setHintOpen] = useState(false);
  const [labelOpen, setLabelOpen] = useState(false);
  const labelOpenRef = useRef(false);
  // The name's detail replaces the hint until the pointer comes back. Both
  // wait out the same delay, so whichever opens second must not show both.
  const showLabelDetail = (open: boolean) => {
    labelOpenRef.current = open;
    setLabelOpen(open);
    if (open) setHintOpen(false);
  };
  const showHint = (open: boolean) =>
    setHintOpen(open && !labelOpenRef.current);

  const name = (
    <strong
      className="max-w-full self-start truncate text-sm font-medium"
      data-slot="breakdown-label"
      // The detail names the row in full, so a native title would repeat it.
      title={labelDetail ? undefined : labelTitle}
    >
      {label}
    </strong>
  );
  const content = (
    <>
      {/* Keeps the name in the second column when a row has no mark. */}
      <span className="flex items-center">{leading}</span>
      <span className="flex min-w-0 flex-col">
        {labelDetail ? (
          <Tooltip onOpenChange={showLabelDetail} open={labelOpen}>
            <TooltipTrigger asChild>{name}</TooltipTrigger>
            <TooltipContent align="start" className="max-w-80" sideOffset={4}>
              {labelDetail}
            </TooltipContent>
          </Tooltip>
        ) : (
          name
        )}
        {share !== null ? (
          <span
            aria-hidden="true"
            className="mt-1.5 block h-1 overflow-hidden rounded-full bg-muted"
            data-slot="breakdown-share"
          >
            <span
              className="block h-full rounded-full bg-primary/40"
              style={{ width: `${share}%` }}
            />
          </span>
        ) : null}
      </span>
      <span className="flex shrink-0 flex-col items-end">
        <span
          className="text-sm font-semibold tracking-tight tabular-nums"
          title={valueTitle}
        >
          {value}
        </span>
        {detail != null ? (
          <span
            className="text-micro text-muted-foreground tabular-nums"
            title={detailTitle}
          >
            {detail}
          </span>
        ) : null}
      </span>
    </>
  );

  // Beside a control the row gives up its own border and hover to the line
  // they share.
  const beside = action ? "min-w-0 flex-1 border-b-0 pr-2" : null;
  let main: ReactNode;
  if (onClick) {
    const button = (
      <Button
        className={cn(
          ROW,
          "w-full rounded-none bg-transparent text-left font-normal text-foreground hover:bg-muted",
          beside && [beside, "hover:bg-transparent"],
        )}
        onClick={onClick}
        type="button"
        variant="ghost"
      >
        {content}
      </Button>
    );
    main = hint ? (
      <Tooltip onOpenChange={showHint} open={hintOpen}>
        <TooltipTrigger asChild>{button}</TooltipTrigger>
        <TooltipContent sideOffset={4}>{hint}</TooltipContent>
      </Tooltip>
    ) : (
      button
    );
  } else {
    main = <DataRow className={cn(ROW, beside)}>{content}</DataRow>;
  }
  if (!action) return main;
  // The row and its control are siblings, so the control is no button
  // nested in the row's, and both share one hover. The control shows with
  // the pointer or focus and keeps its place, so the figures stay in line.
  return (
    <div
      className={cn(
        "group flex h-15 min-w-0 items-center border-b last:border-b-0",
        onClick && "hover:bg-muted",
      )}
      data-slot="breakdown-row"
    >
      {main}
      <span className="shrink-0 pr-3 opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100">
        {action}
      </span>
    </div>
  );
}

/** Bar length of `value` against the largest of `values`, as a percentage. */
export function breakdownShare(value: number, values: number[]): number {
  const max = values.reduce((highest, next) => Math.max(highest, next), 0);
  if (max <= 0 || value <= 0) return 0;
  return Math.round((value / max) * 100);
}
