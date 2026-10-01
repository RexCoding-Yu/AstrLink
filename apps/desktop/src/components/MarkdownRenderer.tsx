import type { ReactNode } from "react";
import Markdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";

import { useT } from "@/i18n";
import { cn } from "@/lib/utils";
import { openInSystemBrowser } from "./ExternalLink";
import { Checkbox } from "./ui/checkbox";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "./ui/table";

const alertTones = {
  note: {
    box: "border-violet bg-violet-wash",
    title: "text-violet-foreground",
  },
  tip: {
    box: "border-success bg-success-wash",
    title: "text-success-foreground",
  },
  important: {
    box: "border-primary bg-accent",
    title: "text-accent-foreground",
  },
  warning: {
    box: "border-warning bg-warning-wash",
    title: "text-warning-foreground",
  },
  caution: {
    box: "border-destructive bg-danger-wash",
    title: "text-danger-foreground",
  },
};
type AlertKind = keyof typeof alertTones;

function isAlertKind(value: unknown): value is AlertKind {
  return typeof value === "string" && Object.hasOwn(alertTones, value);
}

const alertMarker =
  /^\[!(note|tip|important|warning|caution)\][^\S\r\n]*(\r?\n)?/i;

type MarkdownNode = {
  type: string;
  value?: string;
  children?: MarkdownNode[];
  data?: { hProperties?: Record<string, unknown> };
};

// GitHub alerts: a blockquote whose first line is only `[!NOTE]` (or TIP,
// IMPORTANT, WARNING, CAUTION). The marker is removed and the kind is passed
// to the blockquote renderer as `data-alert`.
function remarkAlerts() {
  return function mark(node: MarkdownNode) {
    node.children?.forEach(mark);
    if (node.type !== "blockquote" || !node.children) return;
    const paragraph = node.children[0];
    const lines = paragraph?.type === "paragraph" ? paragraph.children : null;
    const text = lines?.[0];
    if (!lines || text?.type !== "text" || text.value === undefined) return;
    const match = alertMarker.exec(text.value);
    if (!match) return;
    const rest = text.value.slice(match[0].length);
    const lineEnds =
      match[2] !== undefined ||
      (rest === "" && (lines.length === 1 || lines[1].type === "break"));
    if (!lineEnds) return;
    text.value = rest;
    if (rest === "") lines.shift();
    if (lines[0]?.type === "break") lines.shift();
    if (lines.length === 0) node.children.shift();
    node.data = {
      ...node.data,
      hProperties: {
        ...node.data?.hProperties,
        dataAlert: match[1].toLowerCase(),
      },
    };
  };
}

function MarkdownAlert({
  kind,
  children,
}: {
  kind: AlertKind;
  children: ReactNode;
}) {
  const t = useT();
  const tone = alertTones[kind];
  return (
    <div
      data-alert={kind}
      className={cn("space-y-1 rounded-r-md border-l-2 px-3 py-2", tone.box)}
    >
      <p className={cn("text-xs font-semibold", tone.title)}>
        {t(`markdownContent.alert.${kind}`)}
      </p>
      {children}
    </div>
  );
}

const components: Components = {
  h1: ({ children }) => <h3 className="text-base font-semibold">{children}</h3>,
  h2: ({ children }) => <h4 className="text-sm font-semibold">{children}</h4>,
  h3: ({ children }) => <h5 className="text-sm font-semibold">{children}</h5>,
  h4: ({ children }) => <h6 className="text-sm font-semibold">{children}</h6>,
  h5: ({ children }) => <h6 className="text-sm font-semibold">{children}</h6>,
  h6: ({ children }) => <h6 className="text-sm font-semibold">{children}</h6>,
  p: ({ children }) => <p className="whitespace-pre-wrap">{children}</p>,
  ul: ({ children }) => (
    <ul className="list-disc space-y-1 pl-5">{children}</ul>
  ),
  ol: ({ children, start }) => (
    <ol className="list-decimal space-y-1 pl-5" start={start}>
      {children}
    </ol>
  ),
  li: ({ children }) => <li className="[&>p]:inline">{children}</li>,
  blockquote: ({ children, node }) => {
    const alert = node?.properties.dataAlert;
    return isAlertKind(alert) ? (
      <MarkdownAlert kind={alert}>{children}</MarkdownAlert>
    ) : (
      <blockquote className="border-l-2 pl-3 text-muted-foreground">
        {children}
      </blockquote>
    );
  },
  pre: ({ children }) => (
    <pre className="max-w-full overflow-x-auto rounded-md border bg-background p-3 text-xs [&>code]:bg-transparent [&>code]:p-0">
      {children}
    </pre>
  ),
  code: ({ children, className }) => (
    <code
      className={`rounded-sm bg-background px-1 font-mono text-xs ${className ?? ""}`}
    >
      {children}
    </code>
  ),
  a: ({ children, href }) =>
    href ? (
      <a
        href={href}
        target="_blank"
        rel="noopener noreferrer"
        className="text-primary underline underline-offset-2"
        onClick={(event) => openInSystemBrowser(event, href)}
      >
        {children}
      </a>
    ) : (
      <span>{children}</span>
    ),
  // Model output must not fetch remote resources merely by being displayed.
  img: ({ alt, src }) => (
    <span className="text-muted-foreground">{alt || src}</span>
  ),
  input: ({ checked }) => (
    <Checkbox
      checked={checked ?? false}
      disabled
      className="mr-1 inline-flex align-middle"
    />
  ),
  table: ({ children }) => <Table>{children}</Table>,
  thead: ({ children }) => <TableHeader>{children}</TableHeader>,
  tbody: ({ children }) => <TableBody>{children}</TableBody>,
  tr: ({ children }) => <TableRow>{children}</TableRow>,
  th: ({ children, style }) => <TableHead style={style}>{children}</TableHead>,
  td: ({ children, style }) => <TableCell style={style}>{children}</TableCell>,
};

export default function MarkdownRenderer({ content }: { content: string }) {
  return (
    <div className="min-w-0 space-y-3">
      {/* Keep raw HTML as text and retain react-markdown's safe URL handling. */}
      <Markdown
        remarkPlugins={[remarkGfm, remarkAlerts]}
        components={components}
      >
        {content}
      </Markdown>
    </div>
  );
}
