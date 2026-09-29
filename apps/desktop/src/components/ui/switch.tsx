"use client";

import * as React from "react";
import { Switch as SwitchPrimitive } from "radix-ui";

import { cn } from "@/lib/utils";

function Switch({
  className,
  size = "default",
  onCheckedChange,
  ...props
}: React.ComponentProps<typeof SwitchPrimitive.Root> & {
  size?: "sm" | "default";
}) {
  const [armed, setArmed] = React.useState(false);

  return (
    <SwitchPrimitive.Root
      data-slot="switch"
      data-size={size}
      className={cn(
        "peer group/switch inline-flex shrink-0 cursor-pointer items-center justify-center rounded-md border border-input bg-card p-0 text-muted-foreground outline-none focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-45 data-[size=default]:h-6 data-[size=default]:w-11 data-[size=sm]:h-5 data-[size=sm]:w-9 data-[state=checked]:border-primary-fill data-[state=checked]:bg-primary-fill data-[state=checked]:text-primary-fill-foreground data-[state=checked]:enabled:hover:border-primary-hover data-[state=checked]:enabled:hover:bg-primary-hover data-[state=unchecked]:enabled:hover:border-muted-foreground data-[state=unchecked]:enabled:hover:bg-secondary",
        armed
          ? "transition-colors duration-150 motion-reduce:transition-none"
          : "transition-none",
        className,
      )}
      {...props}
      onCheckedChange={(checked) => {
        // Data arriving after mount is initialization, not a user toggle.
        setArmed(true);
        onCheckedChange?.(checked);
      }}
    >
      <svg
        aria-hidden="true"
        focusable="false"
        viewBox="0 0 28 16"
        fill="none"
        className="pointer-events-none shrink-0 transition-transform duration-150 group-[:enabled:active]/switch:scale-90 group-data-[size=default]/switch:h-5 group-data-[size=default]/switch:w-8 group-data-[size=sm]/switch:h-4 group-data-[size=sm]/switch:w-7 motion-reduce:transform-none motion-reduce:transition-none"
      >
        {/* The star and open orbit echo the brand without a sliding thumb. */}
        <g
          data-slot="switch-on"
          className={cn(
            "group-data-[state=checked]/switch:opacity-100 group-data-[state=unchecked]/switch:opacity-0",
            armed
              ? "transition-opacity duration-150 motion-reduce:transition-none"
              : "transition-none",
          )}
        >
          <path
            d="M8.5 5.5C4.8 6.8 2.9 8.5 3.3 9.8C3.7 11.1 6.9 11.4 10.5 10.8M17.5 5.2C21.1 4.6 24.3 4.9 24.7 6.2C25.1 7.5 23.2 9.2 19.5 10.5"
            stroke="currentColor"
            strokeWidth="1.2"
            strokeLinecap="round"
            opacity="0.55"
          />
          <path
            d="M14 1.75C14.9 5.9 16.1 7.1 20.25 8C16.1 8.9 14.9 10.1 14 14.25C13.1 10.1 11.9 8.9 7.75 8C11.9 7.1 13.1 5.9 14 1.75Z"
            fill="currentColor"
          />
        </g>
        <g
          data-slot="switch-off"
          className={cn(
            "group-data-[state=checked]/switch:opacity-0 group-data-[state=unchecked]/switch:opacity-100",
            armed
              ? "transition-opacity duration-150 motion-reduce:transition-none"
              : "transition-none",
          )}
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinecap="round"
        >
          <path d="M10.5 8h7" />
        </g>
      </svg>
    </SwitchPrimitive.Root>
  );
}

export { Switch };
