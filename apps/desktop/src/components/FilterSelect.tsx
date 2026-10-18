import type { ReactNode } from "react";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

/** Compact, labelled select for list toolbars; long values stay inside the control. */
export function FilterSelect({
  ariaLabel,
  className,
  disabled,
  label,
  value,
  onChange,
  options,
  variant = "outline",
}: {
  ariaLabel: string;
  className?: string;
  disabled?: boolean;
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: Array<{ label: string; value: string; displayLabel?: ReactNode }>;
  /** `filled` sets the filter that scopes the whole list apart from the outlined ones beside it. */
  variant?: "outline" | "filled";
}) {
  return (
    <Select
      disabled={disabled}
      onValueChange={(next) => onChange(next === "__all__" ? "" : next)}
      value={value || "__all__"}
    >
      <SelectTrigger
        aria-label={ariaLabel}
        className={cn(
          "min-w-0 text-xs",
          variant === "filled" && "border-transparent bg-secondary",
          className,
        )}
        data-variant={variant}
        size="sm"
        title={options.find((option) => option.value === value)?.label}
      >
        <span className="flex min-w-0 items-center gap-2 *:data-[slot=select-value]:flex *:data-[slot=select-value]:min-w-0 *:data-[slot=select-value]:items-center *:data-[slot=select-value]:overflow-hidden">
          {label ? (
            <span className="shrink-0 font-normal text-muted-foreground">
              {label}
            </span>
          ) : null}
          <SelectValue />
        </span>
      </SelectTrigger>
      <SelectContent>
        {options.map((option) => (
          <SelectItem
            key={option.value || "__all__"}
            value={option.value || "__all__"}
            textValue={option.label}
          >
            {option.displayLabel ?? (
              <span className="truncate">{option.label}</span>
            )}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
