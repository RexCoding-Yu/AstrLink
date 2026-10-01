const GUTTER_PROPERTY = "--scrollbar-gutter-width";

/**
 * Publish the native scrollbar width, so fixed page chrome can stop where a
 * scrollport's stable gutter begins instead of assuming a platform width.
 */
export function initializeScrollbarGutter() {
  const probe = document.createElement("div");
  probe.setAttribute("aria-hidden", "true");
  // Every scrollport shares the global scrollbar styles, so one probe covers
  // them all, including the system scrollbar under forced colors.
  Object.assign(probe.style, {
    position: "fixed",
    top: "0",
    left: "0",
    width: "100px",
    height: "100px",
    overflowY: "scroll",
    visibility: "hidden",
    pointerEvents: "none",
  });
  document.body.append(probe);

  const root = document.documentElement;
  const sync = () =>
    root.style.setProperty(
      GUTTER_PROPERTY,
      `${probe.offsetWidth - probe.clientWidth}px`,
    );
  sync();
  // The probe's content box resizes whenever the scrollbar width changes.
  const observer = new ResizeObserver(sync);
  observer.observe(probe);

  return () => {
    observer.disconnect();
    probe.remove();
    root.style.removeProperty(GUTTER_PROPERTY);
  };
}
