import type { ReactNode } from "react";
import { motion, useReducedMotion } from "motion/react";

import { ChoiceCard } from "@/components/ChoiceCard";
import { RadioGroup } from "@/components/ui/radio-group";

// As tall as an option row, so a label stays beside its first row when the
// options wrap.
export const optionRowLabel =
  "flex h-8 items-center text-xs font-medium text-muted-foreground";

/**
 * One row of compact radio options. Options fade a little when unselected so
 * the choice reads at a glance.
 */
export function OptionRow<T extends string>({
  labelId,
  options,
  value,
  disabled,
  onValueChange,
}: {
  labelId: string;
  options: readonly {
    value: T;
    id: string;
    label: string;
    icon: (selected: boolean) => ReactNode;
  }[];
  value: T;
  disabled?: boolean;
  onValueChange: (value: T) => void;
}) {
  const reducedMotion = useReducedMotion();
  return (
    <RadioGroup
      aria-labelledby={labelId}
      className="inline-flex w-fit max-w-full flex-wrap gap-0.5 rounded-md bg-muted p-0.5"
      disabled={disabled}
      value={value}
      onValueChange={(next) => {
        const option = options.find((item) => item.value === next);
        if (option) onValueChange(option.value);
      }}
    >
      {options.map((option) => {
        const selected = option.value === value;
        return (
          <motion.div
            key={option.value}
            className="min-w-0"
            initial={false}
            animate={{ opacity: selected ? 1 : 0.8 }}
            transition={{ duration: reducedMotion ? 0 : 0.16 }}
          >
            <ChoiceCard
              id={option.id}
              disabled={disabled}
              layout="compact"
              icon={option.icon(selected)}
              label={option.label}
              selected={selected}
              value={option.value}
            />
          </motion.div>
        );
      })}
    </RadioGroup>
  );
}
