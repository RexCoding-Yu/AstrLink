import {
  Boxes,
  ChevronRight,
  ScanText,
  Search,
  SquarePen,
} from "@/components/icons";

import { toolNameKind } from "../tool-call-summary";

// File tools that change a file rather than read it.
const EDIT_TOOL =
  /^(write|edit|multiedit|edit_file|write_file|writefile|apply_patch|str_replace_based_edit_tool)$/;

/**
 * The mark for what a tool does: reading or changing files, commands,
 * searches, or anything else.
 */
export function ToolKindIcon({
  className,
  name,
}: {
  className?: string;
  name: string;
}) {
  const kind = toolNameKind(name);
  const base =
    name
      .split(/__|[./]/)
      .at(-1)
      ?.toLowerCase() ?? "";
  const Icon = EDIT_TOOL.test(base)
    ? SquarePen
    : kind === "file"
      ? ScanText
      : kind === "search"
        ? Search
        : kind === "command"
          ? ChevronRight
          : Boxes;
  return <Icon aria-hidden="true" className={className} />;
}
