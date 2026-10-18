import { useLayoutEffect, useRef, type HTMLAttributes } from "react";
import { Slot } from "radix-ui";

/** Gently reveal changed values without animating layout or replaying on return. */
export function ValueTransition({
  valueKey,
  asChild = false,
  initialOpacity = 0.45,
  duration = 180,
  children,
  ...props
}: HTMLAttributes<HTMLSpanElement> & {
  valueKey: string;
  asChild?: boolean;
  initialOpacity?: number;
  duration?: number;
}) {
  const element = useRef<HTMLSpanElement>(null);
  const previous = useRef(valueKey);
  useLayoutEffect(() => {
    if (previous.current === valueKey) return;
    previous.current = valueKey;
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    // Opacity only, navigation included; it neither remounts nor measures
    // content. While a page slides, WKWebView moves its scrollers in fractions
    // of a pixel and the rest in whole pixels, so the two drift a pixel apart
    // and snap back when the slide ends; see panel-reveal in globals.css.
    const animation = element.current?.animate?.(
      [{ opacity: initialOpacity }, { opacity: 1 }],
      { duration, easing: "ease-out" },
    );
    return () => animation?.cancel();
  }, [valueKey, initialOpacity, duration]);

  const Component = asChild ? Slot.Root : "span";
  return (
    <Component {...props} ref={element} data-slot="value-transition">
      {children}
    </Component>
  );
}
