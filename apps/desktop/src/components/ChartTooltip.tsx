import {
  useLayoutEffect,
  useRef,
  useState,
  type HTMLAttributes,
  type ReactNode,
  type RefObject,
} from "react";
import { createPortal } from "react-dom";

import { cn } from "@/lib/utils";

import { placeFloatingCard } from "../place-floating-card";

/**
 * The hover card of a Recharts chart, portalled to the body and kept inside
 * the viewport so a scrolling panel never clips it. `coordinate` is the
 * pointer position Recharts reports inside the chart box `originRef`.
 */
export function ChartTooltipCard({
  active,
  coordinate,
  originRef,
  placementKey,
  children,
  className,
  style,
  ...props
}: Omit<HTMLAttributes<HTMLDivElement>, "children"> & {
  active: boolean;
  coordinate: { x?: number; y?: number } | null | undefined;
  originRef: RefObject<HTMLElement | null>;
  /** Changes with the hovered point, so the card is measured again. */
  placementKey: string;
  children: ReactNode;
}) {
  const cardRef = useRef<HTMLDivElement>(null);
  const [placed, setPlaced] = useState<{ left: number; top: number } | null>(
    null,
  );
  const visible = active && coordinate != null;
  const origin = originRef.current?.getBoundingClientRect();
  const anchorX = (origin?.left ?? 0) + (coordinate?.x ?? 0);
  const anchorY = (origin?.top ?? 0) + (coordinate?.y ?? 0);

  useLayoutEffect(() => {
    if (!visible) {
      setPlaced(null);
      return;
    }
    const card = cardRef.current;
    if (!card) return;
    const rect = card.getBoundingClientRect();
    const next = placeFloatingCard({
      anchorX,
      anchorY,
      height: rect.height,
      viewportHeight: window.innerHeight,
      viewportWidth: window.innerWidth,
      width: rect.width,
    });
    setPlaced((prev) =>
      prev && prev.left === next.left && prev.top === next.top ? prev : next,
    );
  }, [visible, anchorX, anchorY, placementKey]);

  if (!visible) return null;
  return createPortal(
    <div
      className={cn(
        "grid w-max max-w-xs gap-1 rounded-sm border bg-card px-2.5 py-2 text-left text-micro text-foreground",
        className,
      )}
      ref={cardRef}
      style={{
        ...style,
        left: placed?.left ?? anchorX,
        pointerEvents: "none",
        position: "fixed",
        top: placed?.top ?? anchorY,
        visibility: placed ? "visible" : "hidden",
        zIndex: 50,
      }}
      {...props}
    >
      {children}
    </div>,
    document.body,
  );
}
