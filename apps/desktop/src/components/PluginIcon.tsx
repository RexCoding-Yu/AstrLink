import { Blocks } from "@/components/icons";
import { cn } from "@/lib/utils";

/**
 * A plugin's icon as the plugin ships it, or the plugins glyph for one
 * without. It sits beside the plugin's name, so it is decoration. A plugin's
 * art is drawn for light paper: on the dark theme it keeps a light plate.
 */
export function PluginIcon({
  icon,
  className,
}: {
  /** A data URL of a PNG or SVG image. */
  icon?: string;
  className?: string;
}) {
  return icon ? (
    <img
      alt=""
      aria-hidden="true"
      className={cn(
        "size-5 shrink-0 object-contain dark:rounded-sm dark:bg-foreground dark:p-px",
        className,
      )}
      data-slot="plugin-icon"
      draggable={false}
      src={icon}
    />
  ) : (
    <Blocks
      aria-hidden="true"
      className={cn("size-5 shrink-0 text-muted-foreground", className)}
      data-slot="plugin-icon"
    />
  );
}
