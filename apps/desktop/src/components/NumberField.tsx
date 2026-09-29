import { useId, useState, type ReactNode } from "react";

import { Field } from "@/components/Field";
import { Input } from "@/components/ui/input";

/** A numeric field with a persistent unit and native range validation. */
export function NumberField({
  label,
  hint,
  unit,
  value,
  min,
  max,
  step = 1,
  disabled,
  onChange,
}: {
  label: string;
  hint?: ReactNode;
  unit: string;
  value: number;
  min: number;
  max: number;
  step?: number | "any";
  disabled?: boolean;
  onChange: (value: number) => void;
}) {
  const id = useId();
  const [input, setInput] = useState<string | null>(null);

  return (
    <Field
      label={label}
      htmlFor={id}
      hint={hint ? <span id={`${id}-hint`}>{hint}</span> : undefined}
    >
      <span className="relative block">
        <Input
          id={id}
          aria-label={label}
          aria-describedby={`${id}-unit${hint ? ` ${id}-hint` : ""}`}
          className="h-9 pr-12 tabular-nums [appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
          type="number"
          inputMode={step === 1 ? "numeric" : "decimal"}
          required
          min={min}
          max={max}
          step={step}
          disabled={disabled}
          value={input ?? value}
          onChange={(event) => {
            setInput(event.currentTarget.value);
            const next = event.currentTarget.valueAsNumber;
            if (Number.isFinite(next)) onChange(next);
          }}
          onBlur={(event) => {
            if (event.currentTarget.validity.valid) setInput(null);
          }}
        />
        <span
          id={`${id}-unit`}
          className="pointer-events-none absolute inset-y-0 right-3 flex items-center text-xs font-normal text-muted-foreground"
        >
          {unit}
        </span>
      </span>
    </Field>
  );
}
