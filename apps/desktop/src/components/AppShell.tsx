import type { ReactNode } from "react";

export function AppShell({
  children,
  sidebar,
}: {
  children: ReactNode;
  sidebar: ReactNode;
}) {
  // Collapse before the expanded sidebar leaves less than 640px for content,
  // so container-driven grids do not wrap and then expand again on shrinking.
  // 171px matches the brand text's right inset to the logo mark's visible
  // left inset; the logo tile is near-white, so its box edge does not count.
  return (
    <div className="grid h-dvh overflow-hidden grid-cols-[171px_minmax(0,1fr)] max-[960px]:grid-cols-[56px_minmax(0,1fr)]">
      {sidebar}
      <div className="min-h-0 min-w-0 overflow-hidden">{children}</div>
    </div>
  );
}
