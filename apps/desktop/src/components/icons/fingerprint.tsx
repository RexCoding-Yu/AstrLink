// Adapted from https://lucide-animated.com/r/fingerprint.json; MIT, (c) 2024-2026 pqoqubbw.
// See LICENSE and README.md in this directory.
import type { Variants } from "motion/react";
import { motion } from "motion/react";
import { Fragment } from "react";
import { createAnimatedIcon } from "./create-animated-icon";

const RIDGES = [
  "M12 10a2 2 0 0 0-2 2c0 1.02-.1 2.51-.26 4",
  "M14 13.12c0 2.38 0 6.38-1 8.88",
  "M17.29 21.02c.12-.6.43-2.3.5-3.02",
  "M2 12a10 10 0 0 1 18-6",
  "M2 16h.01",
  "M21.8 16c.2-2 .131-5.354 0-6",
  "M5 19.5C5.5 18 6 15 6 12a6 6 0 0 1 .34-2",
  "M8.65 22c.21-.66.45-1.32.57-2",
  "M9 6.8a6 6 0 0 1 9 5.2v2",
];

const PATH_VARIANTS: Variants = {
  normal: { pathLength: 1, opacity: 1 },
  animate: {
    opacity: [0, 0, 1, 1, 1],
    pathLength: [0.1, 0.3, 0.5, 0.7, 0.9, 1],
    transition: {
      opacity: { duration: 0.5 },
      pathLength: {
        duration: 2,
      },
    },
  },
};

// Each ridge keeps a faint copy underneath while its stroke redraws.
export const Fingerprint = createAnimatedIcon("fingerprint", (controls) => (
  <>
    {RIDGES.map((d) => (
      <Fragment key={d}>
        <path d={d} strokeOpacity={0.4} />
        <motion.path animate={controls} d={d} variants={PATH_VARIANTS} />
      </Fragment>
    ))}
  </>
));
