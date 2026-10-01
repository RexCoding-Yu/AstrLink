import { StatusBadge } from "@/components/StatusBadge";
import { useT } from "../i18n";
import type { ConversionDiagnostic } from "../request-record-model";

/** Tool definitions live under `tools`; everything else is a request field. */
function isToolPath(path: string | undefined): boolean {
  return path === "tools" || /^tools[[.]/.test(path ?? "");
}

/**
 * What converting the call to the provider's protocol dropped or rewrote. The
 * call still went through; this explains why a tool or setting had no effect.
 */
export function ConversionDiagnosticsDetails({
  value,
}: {
  value?: ConversionDiagnostic[];
}) {
  const t = useT();
  if (!value || value.length === 0) return null;
  const groups = [
    { key: "tools", items: value.filter((item) => isToolPath(item.path)) },
    { key: "fields", items: value.filter((item) => !isToolPath(item.path)) },
  ].filter((group) => group.items.length > 0);
  return (
    <dl className="grid gap-2 text-xs" data-testid="conversion-diagnostics">
      {groups.map((group) => (
        <div data-group={group.key} key={group.key}>
          <dt className="text-muted-foreground">
            {t(`conversionDiagnostics.${group.key}`)}
          </dt>
          <dd className="mt-0.5">
            <ol className="grid gap-1.5">
              {group.items.map((item, index) => (
                <li
                  className="grid gap-0.5"
                  data-code={item.code}
                  data-severity={item.severity}
                  key={`${item.phase}:${item.path ?? ""}:${item.code}:${index}`}
                >
                  <span className="flex min-w-0 flex-wrap items-center gap-1.5">
                    <span className="break-all font-mono text-foreground">
                      {t(`conversionDiagnostics.phases.${item.phase}`)}
                      {item.path ? ` · ${item.path}` : ""}
                    </span>
                    <StatusBadge
                      tone={item.severity === "error" ? "pending" : "neutral"}
                    >
                      {t(`conversionDiagnostics.severities.${item.severity}`)}
                    </StatusBadge>
                  </span>
                  <span className="break-words text-muted-foreground">
                    {item.message || item.code}
                  </span>
                </li>
              ))}
            </ol>
          </dd>
        </div>
      ))}
    </dl>
  );
}
