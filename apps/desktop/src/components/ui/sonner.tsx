"use client";

import {
  CircleCheck as CircleCheckIcon,
  CircleHelp as InfoIcon,
  LoaderCircle as Loader2Icon,
  Ban as OctagonXIcon,
  BadgeAlert as TriangleAlertIcon,
  X,
} from "@/components/icons";
import { useEffect } from "react";
import { Toaster as Sonner, type ToasterProps } from "sonner";
import { useResolvedTheme } from "@/theme";

// Passive toasts ignore the pointer (see globals.css), so :hover never matches
// them. Mark the toasts under the pointer so they can fade out of the way.
function useMarkToastsUnderPointer() {
  useEffect(() => {
    let frame = 0;
    let point: { x: number; y: number } | null = null;
    const mark = () => {
      frame = 0;
      for (const toast of document.querySelectorAll<HTMLElement>(
        "[data-sonner-toast]",
      )) {
        const rect = toast.getBoundingClientRect();
        toast.toggleAttribute(
          "data-pointer-over",
          point !== null &&
            point.x >= rect.left &&
            point.x <= rect.right &&
            point.y >= rect.top &&
            point.y <= rect.bottom,
        );
      }
    };
    const schedule = () => {
      if (frame === 0) frame = requestAnimationFrame(mark);
    };
    const move = (event: PointerEvent) => {
      point = { x: event.clientX, y: event.clientY };
      schedule();
    };
    const leave = (event: PointerEvent) => {
      if (event.relatedTarget !== null) return;
      point = null;
      schedule();
    };
    document.addEventListener("pointermove", move, { passive: true });
    document.addEventListener("pointerout", leave);
    return () => {
      cancelAnimationFrame(frame);
      document.removeEventListener("pointermove", move);
      document.removeEventListener("pointerout", leave);
    };
  }, []);
}

const Toaster = ({ ...props }: ToasterProps) => {
  const theme = useResolvedTheme();
  useMarkToastsUnderPointer();
  return (
    <Sonner
      theme={theme}
      className="toaster group"
      icons={{
        close: <X className="size-3" />,
        success: <CircleCheckIcon className="size-4" />,
        info: <InfoIcon className="size-4" />,
        warning: <TriangleAlertIcon className="size-4" />,
        error: <OctagonXIcon className="size-4" />,
        loading: (
          <Loader2Icon
            animateOnHover={false}
            className="size-4 animate-spin motion-reduce:animate-none"
          />
        ),
      }}
      style={
        {
          "--normal-bg": "var(--popover)",
          "--normal-text": "var(--popover-foreground)",
          "--normal-border": "var(--border)",
          "--border-radius": "var(--radius)",
        } as React.CSSProperties
      }
      {...props}
    />
  );
};

export { Toaster };
