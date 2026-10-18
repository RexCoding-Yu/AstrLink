// Artwork from Lucide's `image-down` (https://lucide.dev/icons/image-down);
// ISC, (c) Lucide Contributors. lucide-animated has no image icon, so the
// motion here is local: the arrow dips, as an image shrinking on its way out.
import type { Variants } from "motion/react";
import { motion } from "motion/react";
import { createAnimatedIcon } from "./create-animated-icon";

const ARROW_VARIANTS: Variants = {
  normal: { y: 0 },
  animate: {
    y: [0, 2, 0],
    transition: { duration: 0.45, ease: "easeInOut" },
  },
};

export const ImageDown = createAnimatedIcon("image-down", (controls) => (
  <>
    <path d="M10.3 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2v10l-3.1-3.1a2 2 0 0 0-2.814.014L6 21" />
    <circle cx="9" cy="9" r="2" />
    <motion.g animate={controls} initial="normal" variants={ARROW_VARIANTS}>
      <path d="m14 19 3 3v-5.5" />
      <path d="m17 22 3-3" />
    </motion.g>
  </>
));
