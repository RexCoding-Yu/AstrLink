import type { ComponentProps } from "react";

import astrlinkLogo from "@/assets/astrlink-logo.svg";
import { cn } from "@/lib/utils";

/**
 * The stage of a help animation where cards travel from the client on the
 * left to the model on the right. AstrLink sits in the middle as the protocol
 * mode help draws it, above the cards so they pass through it; gatewayTop
 * centers its box on the lanes.
 */
export function FlowDemoStage({
  children,
  className,
  from,
  gatewayTop,
  to,
  ...props
}: ComponentProps<"div"> & { from: string; gatewayTop: number; to: string }) {
  return (
    <div
      aria-hidden="true"
      className={cn(
        "relative overflow-hidden rounded-md border bg-card",
        className,
      )}
      {...props}
    >
      <span className="absolute top-1.5 left-2.5 text-micro text-muted-foreground">
        {from}
      </span>
      <span className="absolute top-1.5 right-2.5 text-micro text-muted-foreground">
        {to}
      </span>
      {children}
      <span
        className="absolute left-1/2 z-10 flex -translate-x-1/2 flex-col items-center gap-1"
        data-gateway
        style={{ top: gatewayTop }}
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
