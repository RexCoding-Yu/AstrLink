import { motion, useReducedMotion } from "motion/react";

/**
 * Decorative magnitude cue that lights bars from through level, so it can
 * show a single strength or a range of them. The surrounding control
 * supplies its label.
 */
export function StrengthMeter({
  level,
  from = 1,
  active = false,
  size = "default",
}: {
  level: number;
  from?: number;
  active?: boolean;
  size?: "default" | "sm";
}) {
  const reducedMotion = useReducedMotion();
  return (
    <span
      aria-hidden="true"
      className={
        size === "sm"
          ? "inline-flex h-3.5 items-end gap-0.5"
          : "inline-flex h-6 items-end gap-1"
      }
      data-strength={`${from}-${level}`}
    >
      {[1, 2, 3, 4].map((bar) => (
        <span
          key={bar}
          className={`relative overflow-hidden rounded-sm bg-primary/15 ${size === "sm" ? "w-0.5" : "w-1.5"}`}
          style={{ height: size === "sm" ? 2 + bar * 3 : 6 + bar * 4 }}
        >
          <motion.span
            className="absolute inset-0 origin-bottom rounded-sm bg-primary"
            initial={false}
            animate={{
              scaleY: bar >= from && bar <= level ? 1 : 0,
              opacity: active ? 1 : 0.65,
            }}
            transition={{ duration: reducedMotion ? 0 : 0.18 }}
          />
        </span>
      ))}
    </span>
  );
}
