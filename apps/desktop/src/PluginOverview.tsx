import { ExternalLink } from "@/components/ExternalLink";
import { FormMessage } from "@/components/FormMessage";
import { LoadingState } from "@/components/LoadingState";
import { Panel, PanelHeader } from "@/components/Panel";
import { PluginView } from "@/components/PluginView";
import { StatusBadge } from "@/components/StatusBadge";
import type { StatusTone } from "@/components/StatusDot";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { formatAgo } from "@/lib/format-ago";
import { useT } from "./i18n";
import {
  plaintextOrigin,
  settingValue,
  type PluginBindings,
  type PluginCredit,
  type PluginDetail,
  type PluginStatus,
  type PluginTestResult,
} from "./plugin-model";

const testTones: Record<PluginTestResult["status"], StatusTone> = {
  ok: "positive",
  error: "negative",
  failed: "negative",
};

/** Full names of the licenses plugins credit most; others show as written. */
const licenseNames: Record<string, string> = {
  "Apache-2.0": "Apache License 2.0",
  MIT: "MIT License",
  "BSD-2-Clause": "BSD 2-Clause License",
  "BSD-3-Clause": "BSD 3-Clause License",
  "MPL-2.0": "Mozilla Public License 2.0",
  "GPL-3.0-only": "GNU GPL v3.0",
  "GPL-3.0-or-later": "GNU GPL v3.0 or later",
  "AGPL-3.0-only": "GNU AGPL v3.0",
  "AGPL-3.0-or-later": "GNU AGPL v3.0 or later",
  "LGPL-3.0-only": "GNU LGPL v3.0",
  ISC: "ISC License",
};

function displayAddress(url: string): string {
  return url.replace(/^https?:\/\//, "").replace(/\/$/, "");
}

function CardTitle({ children }: { children: string }) {
  return <h3 className="text-xs font-semibold">{children}</h3>;
}

/**
 * The plugin page's overview: the plugin's server and its last test, the
 * values sessions are bound to, what the plugin says of itself and the
 * projects it credits.
 */
export function PluginOverview({
  detail,
  status,
  statusError,
  now = new Date(),
}: {
  detail: PluginDetail;
  status: PluginStatus | null;
  statusError: string | null;
  now?: Date;
}) {
  const t = useT();
  const server = detail.settings.find((setting) => setting.type === "server");
  const describes = detail.hooks.includes("describeStatus");
  return (
    <div className="grid gap-3" data-testid="plugin-overview">
      {statusError ? (
        <FormMessage tone="error">{statusError}</FormMessage>
      ) : null}
      {server || detail.hooks.includes("testConnection") ? (
        <Panel data-testid="plugin-connection">
          <PanelHeader
            actions={
              status?.last_test ? (
                <StatusBadge tone={testTones[status.last_test.status]}>
                  {t(`plugins.testStatus.${status.last_test.status}`)}
                </StatusBadge>
              ) : (
                <StatusBadge tone="neutral">
                  {t("plugins.overview.notTested")}
                </StatusBadge>
              )
            }
            size="sm"
          >
            <CardTitle>{t("plugins.overview.connection")}</CardTitle>
          </PanelHeader>
          <dl className="grid grid-cols-[5.5rem_minmax(0,1fr)] gap-x-4 gap-y-2 p-3 text-xs">
            {server ? (
              <ServerRow detail={detail} serverKey={server.key} />
            ) : null}
            {status?.last_test ? (
              <>
                <dt className="text-muted-foreground">
                  {t("plugins.overview.lastTest")}
                </dt>
                <dd className="min-w-0 break-words">
                  {status.last_test.summary}
                  <span className="text-muted-foreground">
                    {" · "}
                    {formatAgo(new Date(status.last_test.tested_at), now)}
                  </span>
                </dd>
              </>
            ) : null}
          </dl>
          {status?.last_test?.view && status.last_test.view.length > 0 ? (
            <div className="px-3 pb-3">
              <PluginView
                blocks={status.last_test.view}
                truncated={status.last_test.truncated}
                truncatedLabel={t("plugins.truncated")}
              />
            </div>
          ) : null}
        </Panel>
      ) : null}
      {status?.bindings?.map((bindings) => (
        <BindingsCard bindings={bindings} key={bindings.key} now={now} />
      ))}
      {describes && status === null && !statusError ? (
        <LoadingState label={t("plugins.loading")} />
      ) : null}
      {describes && status?.view_error ? (
        <FormMessage tone="warning">{status.view_error}</FormMessage>
      ) : null}
      {describes && status?.view && status.view.length > 0 ? (
        <Panel data-testid="plugin-own-status">
          <PanelHeader size="sm">
            <CardTitle>{t("plugins.overview.pluginStatus")}</CardTitle>
          </PanelHeader>
          <div className="p-3">
            <PluginView
              blocks={status.view}
              truncated={status.truncated}
              truncatedLabel={t("plugins.truncated")}
            />
          </div>
        </Panel>
      ) : null}
      {describes &&
      status !== null &&
      !(status.view && status.view.length > 0) &&
      !detail.granted_capabilities.includes("ui.views") ? (
        <p className="text-xs text-muted-foreground">
          {t("plugins.overviewNeedsViews")}
        </p>
      ) : null}
      {detail.credits.length > 0 ? (
        <CreditsCard credits={detail.credits} />
      ) : null}
    </div>
  );
}

function ServerRow({
  detail,
  serverKey,
}: {
  detail: PluginDetail;
  serverKey: string;
}) {
  const t = useT();
  const address = settingValue(detail.config, serverKey);
  return (
    <>
      <dt className="text-muted-foreground">{t("plugins.overview.server")}</dt>
      <dd className="flex min-w-0 flex-wrap items-center gap-2">
        {typeof address === "string" && address !== "" ? (
          <>
            <span className="font-mono break-all">{address}</span>
            {plaintextOrigin(address) ? (
              <StatusBadge tone="pending">
                {t("plugins.network.plaintext")}
              </StatusBadge>
            ) : null}
          </>
        ) : (
          <span className="text-muted-foreground">
            {t("plugins.overview.noServer")}
          </span>
        )}
      </dd>
    </>
  );
}

function BindingsCard({
  bindings,
  now,
}: {
  bindings: PluginBindings;
  now: Date;
}) {
  const t = useT();
  const bound = bindings.values.filter((entry) => entry.value !== "none");
  return (
    <Panel data-testid="plugin-bindings">
      <PanelHeader
        actions={
          <Badge className="tabular-nums" variant="secondary">
            {bound.length}
          </Badge>
        }
        size="sm"
      >
        <CardTitle>
          {t("plugins.overview.bindings", { label: bindings.label })}
        </CardTitle>
      </PanelHeader>
      {bindings.values.length === 0 ? (
        <p className="p-3 text-xs text-muted-foreground">
          {t("plugins.overview.noBindings", { label: bindings.label })}
        </p>
      ) : (
        <Table className="text-xs">
          <TableHeader>
            <TableRow>
              <TableHead className="h-8 px-3 text-xs">
                {bindings.label}
              </TableHead>
              <TableHead className="h-8 w-20 px-3 text-right text-xs">
                {t("plugins.overview.sessions")}
              </TableHead>
              <TableHead className="h-8 w-32 px-3 text-xs">
                {t("plugins.overview.lastUsed")}
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {bindings.values.map((entry) => (
              <TableRow key={entry.value}>
                <TableCell className="px-3 py-1.5">
                  {entry.value === "none" ? (
                    <span className="text-muted-foreground">
                      {t("plugins.overview.none")}
                    </span>
                  ) : (
                    <span className="font-mono">{entry.value}</span>
                  )}
                </TableCell>
                <TableCell className="px-3 py-1.5 text-right tabular-nums">
                  {entry.sessions}
                </TableCell>
                <TableCell className="px-3 py-1.5 text-muted-foreground">
                  {entry.last_used_at
                    ? formatAgo(new Date(entry.last_used_at), now)
                    : "—"}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </Panel>
  );
}

function CreditsCard({ credits }: { credits: PluginCredit[] }) {
  const t = useT();
  return (
    <Panel data-testid="plugin-credits">
      <PanelHeader size="sm">
        <CardTitle>{t("plugins.overview.credits")}</CardTitle>
      </PanelHeader>
      <ul className="divide-y">
        {credits.map((credit) => (
          <li className="grid gap-1.5 p-3 text-xs" key={credit.url}>
            <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
              <strong className="text-sm font-semibold">{credit.name}</strong>
              <span className="text-muted-foreground">
                {[
                  credit.owner,
                  credit.license
                    ? (licenseNames[credit.license] ?? credit.license)
                    : undefined,
                ]
                  .filter(Boolean)
                  .join(" · ")}
              </span>
              <span className="flex-1" />
              <ExternalLink className="text-xs" href={credit.url}>
                {displayAddress(credit.url)}
              </ExternalLink>
            </div>
            {credit.note ? <p className="leading-5">{credit.note}</p> : null}
          </li>
        ))}
      </ul>
    </Panel>
  );
}
