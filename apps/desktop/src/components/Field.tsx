import { useId, type ReactNode } from "react";

import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";

/**
 * Label + control + optional hint. Pages used to hand-roll this stack with
 * sub-scale font sizes, which is what made dense forms feel cramped; routing
 * them through one component keeps every form field on the token scale.
 *
 * Set `group` when the control is a set of buttons: a wrapping <label> would
 * forward clicks on its text to the first button.
 */
export function Field({
  children,
  className,
  group = false,
  hint,
  htmlFor,
  label,
}: {
  children: ReactNode;
  className?: string;
  group?: boolean;
  hint?: ReactNode;
  htmlFor?: string;
  label: ReactNode;
}) {
  const labelID = useId();
  const content = (
    <>
      <span
        className="text-xs font-medium text-text-secondary"
        id={group ? labelID : undefined}
      >
        {label}
      </span>
      {children}
      {hint ? (
        <span className="text-xs font-normal text-muted-foreground">
          {hint}
        </span>
      ) : null}
    </>
  );
  const layout = "flex min-w-0 flex-col items-stretch gap-1.5";
  if (group) {
    return (
      <div
        aria-labelledby={labelID}
        className={cn(layout, "text-xs leading-none font-medium", className)}
        role="group"
      >
        {content}
      </div>
    );
  }
  return (
    <Label className={cn(layout, className)} htmlFor={htmlFor}>
      {content}
    </Label>
  );
}
