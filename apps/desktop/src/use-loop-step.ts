import { useEffect, useState } from "react";
import { useReducedMotion } from "motion/react";

/**
 * Steps through a looping animation and returns the current step and loop:
 * each step starts at its time in milliseconds, and the last one holds
 * before the loop starts over. Reduced motion rests on the last step.
 */
export function useLoopStep(
  times: readonly number[],
  hold: number,
  playing: boolean,
) {
  const reducedMotion = useReducedMotion();
  const [clock, setClock] = useState({ step: 0, cycle: 0 });
  useEffect(() => {
    if (!playing || reducedMotion) return;
    const timers = times
      .slice(1)
      .map((at, index) =>
        window.setTimeout(
          () => setClock((current) => ({ ...current, step: index + 1 })),
          at,
        ),
      );
    timers.push(
      window.setTimeout(
        () => setClock((current) => ({ step: 0, cycle: current.cycle + 1 })),
        times[times.length - 1] + hold,
      ),
    );
    return () => timers.forEach(window.clearTimeout);
  }, [clock.cycle, hold, playing, reducedMotion, times]);
  if (!playing) return { step: 0, cycle: clock.cycle };
  return {
    step: reducedMotion ? times.length - 1 : clock.step,
    cycle: clock.cycle,
  };
}
