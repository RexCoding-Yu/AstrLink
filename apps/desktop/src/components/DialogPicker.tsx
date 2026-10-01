import { useRef, useState, type ReactNode } from "react";

import { Check, ChevronDown } from "@/components/icons";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { selectTriggerClassName } from "@/components/ui/select";
import { cn } from "@/lib/utils";

export interface DialogPickerOption<T extends string> {
  value: T;
  label: string;
  description?: string;
  icon?: ReactNode;
}

export interface DialogPickerGroup<T extends string> {
  label: string;
  options: readonly DialogPickerOption<T>[];
}

/**
 * A dialog of grouped option cards. Picking a card closes the dialog; the
 * optional `trigger` becomes the dialog's opener and regains focus on close.
 */
export function PickerDialog<T extends string>({
  description,
  groups,
  onOpenChange,
  onValueChange,
  open,
  title,
  trigger,
  value,
}: {
  description?: ReactNode;
  groups: readonly DialogPickerGroup<T>[];
  onOpenChange: (open: boolean) => void;
  onValueChange: (value: T) => void;
  open: boolean;
  title: ReactNode;
  trigger?: ReactNode;
  /** The current choice; omit when the dialog starts a fresh pick. */
  value?: T;
}) {
  const contentRef = useRef<HTMLDivElement>(null);
  // Without a trigger Radix has nowhere to return focus; remember the opener.
  const openerRef = useRef<HTMLElement | null>(null);
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {trigger ? <DialogTrigger asChild>{trigger}</DialogTrigger> : null}
      <DialogContent
        ref={contentRef}
        className="top-[calc(50%+var(--window-chrome-height)/2)] flex max-h-[min(calc(100dvh-var(--window-chrome-height)-2rem),40rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-4xl"
        onCloseAutoFocus={(event) => {
          if (trigger) return;
          event.preventDefault();
          if (openerRef.current?.isConnected) {
            openerRef.current.focus({ preventScroll: true });
          }
          openerRef.current = null;
        }}
        onOpenAutoFocus={(event) => {
          if (!trigger && document.activeElement instanceof HTMLElement) {
            openerRef.current = document.activeElement;
          }
          const current = contentRef.current?.querySelector<HTMLElement>(
            '[aria-current="true"]',
          );
          if (!current) return;
          event.preventDefault();
          current.focus();
        }}
        {...(description ? {} : { "aria-describedby": undefined })}
      >
        <DialogHeader className="shrink-0 border-b px-4 py-3 pr-12">
          <DialogTitle>{title}</DialogTitle>
          {description ? (
            <DialogDescription>{description}</DialogDescription>
          ) : null}
        </DialogHeader>
        <div className="grid min-h-0 flex-1 content-start gap-4 overflow-y-auto p-4">
          {groups.map((group) =>
            group.options.length > 0 ? (
              <section
                aria-label={group.label}
                className="grid gap-1.5"
                key={group.label}
              >
                <h3 className="text-xs font-medium text-muted-foreground">
                  {group.label}
                </h3>
                <div className="grid grid-cols-[repeat(auto-fill,minmax(12rem,1fr))] gap-2">
                  {group.options.map((option) => {
                    const selected = option.value === value;
                    return (
                      <button
                        aria-current={selected ? "true" : undefined}
                        className={cn(
                          "flex min-w-0 items-center gap-2.5 rounded-md border bg-card px-2.5 py-2 text-left transition-colors outline-none hover:border-primary/40 focus-visible:ring-2 focus-visible:ring-ring/25",
                          selected && "border-primary/50 bg-accent",
                        )}
                        key={option.value}
                        onClick={() => {
                          onOpenChange(false);
                          if (!selected) onValueChange(option.value);
                        }}
                        type="button"
                      >
                        {option.icon ? (
                          <span
                            aria-hidden="true"
                            className="inline-flex size-5 shrink-0 items-center justify-center"
                          >
                            {option.icon}
                          </span>
                        ) : null}
                        <span className="grid min-w-0 flex-1 gap-0.5">
                          <span
                            className="truncate text-sm font-medium"
                            data-slot="dialog-picker-label"
                          >
                            {option.label}
                          </span>
                          {option.description ? (
                            <span className="truncate text-xs text-muted-foreground">
                              {option.description}
                            </span>
                          ) : null}
                        </span>
                        {selected ? (
                          <Check
                            aria-hidden="true"
                            className="size-4 shrink-0 text-primary"
                          />
                        ) : null}
                      </button>
                    );
                  })}
                </div>
              </section>
            ) : null,
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

/**
 * A select-style trigger that opens a dialog of grouped option cards, for
 * choices too numerous or too visual to scan in a dropdown list.
 */
export function DialogPicker<T extends string>({
  "aria-label": ariaLabel,
  description,
  disabled,
  groups,
  onValueChange,
  title,
  value,
  valueLabel,
}: {
  "aria-label": string;
  description?: ReactNode;
  disabled?: boolean;
  groups: readonly DialogPickerGroup<T>[];
  onValueChange: (value: T) => void;
  title: ReactNode;
  value: T;
  valueLabel: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  return (
    <PickerDialog
      description={description}
      groups={groups}
      onOpenChange={setOpen}
      onValueChange={onValueChange}
      open={open}
      title={title}
      trigger={
        <button
          aria-label={ariaLabel}
          className={cn(selectTriggerClassName, "w-full")}
          data-size="default"
          disabled={disabled}
          type="button"
        >
          <span data-slot="select-value">{valueLabel}</span>
          <ChevronDown aria-hidden="true" className="size-4 opacity-50" />
        </button>
      }
      value={value}
    />
  );
}
