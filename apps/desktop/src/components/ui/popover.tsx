import type { ComponentProps } from "react";
import { Popover as PopoverPrimitive } from "radix-ui";

import { ignoreImeEscape } from "@/lib/ime";
import { cn } from "@/lib/utils";

export function Popover(props: ComponentProps<typeof PopoverPrimitive.Root>) {
  return <PopoverPrimitive.Root {...props} />;
}

export function PopoverTrigger(
  props: ComponentProps<typeof PopoverPrimitive.Trigger>,
) {
  return <PopoverPrimitive.Trigger data-slot="popover-trigger" {...props} />;
}

export function PopoverAnchor(
  props: ComponentProps<typeof PopoverPrimitive.Anchor>,
) {
  return <PopoverPrimitive.Anchor {...props} />;
}

/** Points the content at its anchor; filled to blend with the content. */
export function PopoverArrow({
  className,
  ...props
}: ComponentProps<typeof PopoverPrimitive.Arrow>) {
  return (
    <PopoverPrimitive.Arrow
      className={cn("fill-popover", className)}
      {...props}
    />
  );
}

export function PopoverContent({
  className,
  align = "end",
  sideOffset = 8,
  collisionPadding = 12,
  animated = false,
  onWheel,
  onTouchMove,
  onEscapeKeyDown,
  ...props
}: ComponentProps<typeof PopoverPrimitive.Content> & {
  /** Grows from the trigger and fades like menus; reduced motion skips it. */
  animated?: boolean;
}) {
  return (
    <PopoverPrimitive.Portal>
      <PopoverPrimitive.Content
        align={align}
        className={cn(
          "z-50 max-h-(--radix-popover-content-available-height) w-72 max-w-[calc(100vw-24px)] overflow-y-auto overscroll-contain rounded-md border bg-popover p-4 text-popover-foreground shadow-md outline-none",
          animated &&
            "origin-(--radix-popover-content-transform-origin) data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95 data-[state=open]:fade-in-0 data-[state=open]:zoom-in-95 motion-safe:data-[state=closed]:animate-out motion-safe:data-[state=open]:animate-in",
          className,
        )}
        collisionPadding={collisionPadding}
        data-slot="popover-content"
        sideOffset={sideOffset}
        {...props}
        // Keep native scrolling in this portal out of an ancestor dialog's
        // document-level scroll lock; overscroll containment prevents chaining.
        onWheel={(event) => {
          event.stopPropagation();
          onWheel?.(event);
        }}
        onTouchMove={(event) => {
          event.stopPropagation();
          onTouchMove?.(event);
        }}
        onEscapeKeyDown={ignoreImeEscape(onEscapeKeyDown)}
      />
    </PopoverPrimitive.Portal>
  );
}
