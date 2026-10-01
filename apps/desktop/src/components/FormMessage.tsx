import type { HTMLAttributes } from "react";
import { Slot } from "radix-ui";

import { cn } from "@/lib/utils";

type FormMessageTone = "error" | "notice" | "success" | "warning";

const toneClasses: Record<FormMessageTone, string> = {
  error: "border-destructive/25 bg-danger-wash text-danger-foreground",
  notice: "border-border bg-muted text-text-secondary",
  success: "border-success/25 bg-success-wash text-success-foreground",
  warning: "border-warning/30 bg-warning-wash text-warning-foreground",
};

/** Inline status text. Use `asChild` for block content such as lists. */
export function FormMessage({
  asChild = false,
  className,
  tone = "notice",
  ...props
}: HTMLAttributes<HTMLParagraphElement> & {
  asChild?: boolean;
  tone?: FormMessageTone;
}) {
  const Comp = asChild ? Slot.Root : "p";
  return (
    <Comp
      className={cn(
        "shrink-0 rounded-md border px-3 py-2 text-xs",
        toneClasses[tone],
        className,
      )}
      role={tone === "error" ? "alert" : "status"}
      {...props}
    />
  );
}
