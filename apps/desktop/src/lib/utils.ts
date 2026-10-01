import { clsx, type ClassValue } from "clsx";
import { extendTailwindMerge } from "tailwind-merge";

// Register the custom `text-micro` size so it is not mistaken for a text
// colour and dropped when merged with one.
const twMerge = extendTailwindMerge({
  extend: { theme: { text: ["micro"] } },
});

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
