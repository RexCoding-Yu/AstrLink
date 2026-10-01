import type { HTMLAttributes, ReactNode } from "react";
import { Slot } from "radix-ui";

import { cn } from "@/lib/utils";

/**
 * A fixed header aligned with the content inside a separate native scrollport.
 * The scrollport always reserves its gutter, so revealing overflow never
 * narrows the content, and the header stops where that gutter begins.
 */
export function ScrollWorkspace({
  children,
  className,
  contentAsChild = false,
  contentClassName,
  contentSlot = "scroll-workspace-content",
  header,
  headerClassName,
  ...props
}: HTMLAttributes<HTMLDivElement> & {
  header: ReactNode;
  headerClassName?: string;
  contentAsChild?: boolean;
  contentClassName?: string;
  contentSlot?: string;
}) {
  const Content = contentAsChild ? Slot.Root : "div";

  return (
    <div
      className={cn(
        "flex h-full min-h-0 min-w-0 flex-col gap-3 overflow-hidden",
        className,
      )}
      data-slot="scroll-workspace"
      {...props}
    >
      <div
        className={cn(
          "flex shrink-0 flex-col gap-3 pe-(--scrollbar-gutter-width)",
          headerClassName,
        )}
        data-slot="scroll-workspace-header"
      >
        {header}
      </div>
      <Content
        className={cn(
          "min-h-0 flex-1 overflow-y-auto overscroll-contain [scrollbar-gutter:stable]",
          contentClassName,
        )}
        data-slot={contentSlot}
      >
        {children}
      </Content>
    </div>
  );
}
