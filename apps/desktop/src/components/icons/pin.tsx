// Artwork from Lucide's `pin` (https://lucide.dev/icons/pin); ISC, (c) Lucide
// Contributors. lucide-animated has no pushpin, and a map pin reads as a
// location, so the motion here is local: the pin presses down into the page.
import type { Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const SVG_VARIANTS: Variants = {
  normal: {
    y: 0,
    rotate: 0,
  },
  animate: {
    y: [0, -2, 1.5, 0],
    rotate: [0, -12, 0, 0],
    transition: {
      duration: 0.45,
      times: [0, 0.4, 0.75, 1],
    },
  },
};

export const Pin = createAnimatedIcon("pin", (controls) => (
  <motion.g animate={controls} initial="normal" variants={SVG_VARIANTS}>
    <path d="M12 17v5" />
    <path d="M9 10.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24V16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V7a1 1 0 0 1 1-1 2 2 0 0 0 0-4H8a2 2 0 0 0 0 4 1 1 0 0 1 1 1z" />
  </motion.g>
));
