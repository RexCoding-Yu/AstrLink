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
  const motion = armed
    ? "duration-200 ease-out motion-reduce:transition-none"
    : "transition-none";

  return (
    <SwitchPrimitive.Root
      data-slot="switch"
      data-size={size}
      className={cn(
        "peer group/switch inline-flex shrink-0 cursor-pointer items-center rounded-md border border-input bg-card px-0.5 py-0 outline-none focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-45 data-[size=default]:h-6 data-[size=default]:w-11 data-[size=sm]:h-5 data-[size=sm]:w-9 data-[state=checked]:border-primary-fill data-[state=checked]:bg-primary-fill data-[state=checked]:enabled:hover:border-primary-hover data-[state=checked]:enabled:hover:bg-primary-hover data-[state=unchecked]:enabled:hover:border-muted-foreground data-[state=unchecked]:enabled:hover:bg-secondary",
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
      {/* The thumb's side carries the state; its star lights up when on. */}
      <SwitchPrimitive.Thumb
        data-slot="switch-thumb"
        className={cn(
          "pointer-events-none grid shrink-0 place-content-center rounded-sm bg-muted-foreground text-card group-data-[size=default]/switch:size-4.5 group-data-[size=sm]/switch:size-3.5 motion-safe:group-[:enabled:active]/switch:scale-90 data-[state=checked]:bg-primary-fill-foreground data-[state=checked]:text-primary-fill group-data-[size=default]/switch:data-[state=checked]:translate-x-5 group-data-[size=sm]/switch:data-[state=checked]:translate-x-4",
          armed && "transition",
          motion,
        )}
      >
        <svg
          aria-hidden="true"
          focusable="false"
          viewBox="0 0 16 16"
          data-slot="switch-star"
          className={cn(
            // A quarter turn maps the star onto itself, so it rolls as it slides.
            "-rotate-90 opacity-35 group-data-[size=default]/switch:size-3 group-data-[size=sm]/switch:size-2.5 group-data-[state=checked]/switch:rotate-0 group-data-[state=checked]/switch:opacity-100",
            armed && "transition-[rotate,opacity]",
            motion,
          )}
        >
          <path
            d="M8 .75C8.9 5 11 7.1 15.25 8C11 8.9 8.9 11 8 15.25C7.1 11 5 8.9 .75 8C5 7.1 7.1 5 8 .75Z"
            fill="currentColor"
          />
        </svg>
      </SwitchPrimitive.Thumb>
    </SwitchPrimitive.Root>
  );
}

export { Switch };
