import type { MouseEventHandler, ReactNode, Ref } from "react";
import { CircleHelp } from "@/components/icons";
import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";

/** Supporting explanations stay available without consuming working height. */
export function HelpPopover({
  label,
  children,
  inDialog = false,
  open,
  onOpenChange,
  onTriggerClick,
  triggerClassName,
  triggerRef,
}: {
  label: string;
  children: ReactNode;
  inDialog?: boolean;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  onTriggerClick?: MouseEventHandler<HTMLButtonElement>;
  /** Extra classes for the trigger, e.g. to align it with a control's baseline. */
  triggerClassName?: string;
  triggerRef?: Ref<HTMLButtonElement>;
}) {
  return (
    <Popover open={open} onOpenChange={onOpenChange}>
      <PopoverTrigger asChild>
        <Button
          ref={triggerRef}
          aria-label={label}
          onClick={onTriggerClick}
          size="icon-xs"
          type="button"
          variant="ghost"
          className={triggerClassName}
        >
          <CircleHelp aria-hidden="true" className="text-muted-foreground" />
        </Button>
      </PopoverTrigger>
      <PopoverContent
        align="start"
        className={
          inDialog ? "z-110 text-xs leading-relaxed" : "text-xs leading-relaxed"
        }
      >
        {children}
      </PopoverContent>
    </Popover>
  );
}
