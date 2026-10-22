// Artwork from Lucide's `ellipsis` (https://lucide.dev/icons/ellipsis); ISC,
// (c) Lucide Contributors. lucide-animated has no ellipsis, and its `menu`
// turns into an X, which reads as close on a "more actions" button. The motion
// here is local: the dots hop one after another.
import type { Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const DOT_VARIANTS: Variants = {
  normal: {
    y: 0,
  },
  animate: (index: number) => ({
    y: [0, -3, 0],
    transition: {
      duration: 0.4,
      delay: index * 0.08,
      ease: "easeInOut",
    },
  }),
};

export const Ellipsis = createAnimatedIcon("ellipsis", (controls) => (
  <>
    {[5, 12, 19].map((cx, index) => (
      <motion.circle
        animate={controls}
        custom={index}
        cx={cx}
        cy="12"
        initial="normal"
        key={cx}
        r="1"
        variants={DOT_VARIANTS}
      />
    ))}
  </>
));
