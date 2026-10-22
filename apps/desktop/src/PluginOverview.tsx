import { useState } from "react";

import { ConfirmDialog } from "@/components/ConfirmDialog";
import { ExternalLink } from "@/components/ExternalLink";
import { FormMessage } from "@/components/FormMessage";
import { HelpPopover } from "@/components/HelpPopover";
import { LoadingState } from "@/components/LoadingState";
import { Panel, PanelHeader } from "@/components/Panel";
import { PluginView } from "@/components/PluginView";
import { StatusBadge } from "@/components/StatusBadge";
import type { StatusTone } from "@/components/StatusDot";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { formatAgo } from "@/lib/format-ago";
import { cn } from "@/lib/utils";
import { useT } from "./i18n";
import { EffectCard } from "./PluginEffects";
import {
  plaintextOrigin,
  settingValue,
  type PluginBindings,
  type PluginClearTarget,
  type PluginCredit,
  type PluginDetail,
  type PluginHostStorage,
  type PluginStats,
  type PluginStatus,
  type PluginTestResult,
  type PluginViewBlock,
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

/** The label column every label-value card of the overview shares. */
const labelColumns = "grid-cols-[6rem_minmax(0,1fr)] gap-x-4 gap-y-2";

/**
 * What the last test and the plugin's own status say, as one view: their
 * rows in order, a row of the status replacing the test's of the same label,
 * since the status is read anew; their other blocks after.
 */
function statusView(...views: Array<PluginViewBlock[] | undefined>) {
  const items: Array<{ label: string; value: string; mono?: boolean }> = [];
  const others: PluginViewBlock[] = [];
  for (const block of views.flatMap((view) => view ?? [])) {
    if (block.type !== "kv") {
      others.push(block);
      continue;
    }
    for (const item of block.items) {
      const at = items.findIndex((kept) => kept.label === item.label);
      if (at >= 0) items[at] = item;
      else items.push(item);
    }
  }
  const merged: PluginViewBlock[] =
    items.length > 0 ? [{ type: "kv", items }] : [];
  return [...merged, ...others];
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
  busy = false,
  onClear,
  stats = null,
  now = new Date(),
}: {
  detail: PluginDetail;
  stats?: PluginStats | null;
  status: PluginStatus | null;
  statusError: string | null;
  busy?: boolean;
  onClear?: (what: PluginClearTarget) => Promise<boolean>;
  now?: Date;
}) {
  const t = useT();
  const server = detail.settings.find((setting) => setting.type === "server");
  const describes = detail.hooks.includes("describeStatus");
  const tests = detail.hooks.includes("testConnection");
  const view = statusView(
    status?.last_test?.view,
    describes ? status?.view : undefined,
  );
  return (
    <div className="grid gap-3" data-testid="plugin-overview">
      {statusError ? (
        <FormMessage tone="error">{statusError}</FormMessage>
      ) : null}
      {server || tests || describes ? (
        <Panel data-testid="plugin-connection">
          <PanelHeader
            actions={
              !tests ? null : status?.last_test ? (
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
          <div className="grid gap-2 p-3">
            <dl className={cn("grid text-xs empty:hidden", labelColumns)}>
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
              {describes && status?.view_error ? (
                <>
                  <dt className="text-muted-foreground">
                    {t("plugins.overview.pluginStatus")}
                  </dt>
                  <dd
                    className="flex min-w-0 items-center gap-1 text-warning-foreground"
                    data-testid="plugin-status-error"
                  >
                    {t("plugins.overview.statusUnread")}
                    <HelpPopover label={t("plugins.overview.statusErrorLabel")}>
                      <p className="break-words">{status.view_error}</p>
                    </HelpPopover>
                  </dd>
                </>
              ) : null}
            </dl>
            {view.length > 0 ? (
              <div data-testid="plugin-own-status">
                <PluginView
                  blocks={view}
                  kvClassName={labelColumns}
                  truncated={
                    !!status?.truncated || !!status?.last_test?.truncated
                  }
                  truncatedLabel={t("plugins.truncated")}
                />
              </div>
            ) : null}
            {describes && status === null && !statusError ? (
              <LoadingState label={t("plugins.loading")} />
            ) : null}
            {describes &&
            status !== null &&
            !(status.view && status.view.length > 0) &&
            !status.view_error &&
            !detail.granted_capabilities.includes("ui.views") ? (
              <p className="text-xs text-muted-foreground">
                {t("plugins.overviewNeedsViews")}
              </p>
            ) : null}
          </div>
        </Panel>
      ) : null}
      {stats ? <EffectCard detail={detail} stats={stats} /> : null}
      {status?.bindings?.map((bindings) => (
        <BindingsCard bindings={bindings} key={bindings.key} now={now} />
      ))}
      {detail.host_storage.length > 0 ? (
        <StoredCard busy={busy} detail={detail} onClear={onClear} />
      ) : null}
      {detail.credits.length > 0 ? (
        <CreditsCard credits={detail.credits} />
      ) : null}
    </div>
  );
}

/** What the host keeps for a plugin, and what can be cleared of it. */
const clearable: Partial<Record<PluginHostStorage["kind"], PluginClearTarget>> =
  {
    workspace_bindings: "workspaces",
    plugin_values: "values",
    lookup_credentials: "lookups",
  };

/** What holds conversation or memory text of what the host keeps. */
const holdsMemory: Partial<Record<PluginHostStorage["kind"], boolean>> = {
  resident_block: true,
};

/**
 * What is kept for the plugin, one row each: where, whether it holds
 * conversation text, how long, how much, and clearing what can be cleared.
 * What each holds opens from its ?. The plugin's own declarations sit under
 * its store.
 */
function StoredCard({
  detail,
  busy,
  onClear,
}: {
  detail: PluginDetail;
  busy: boolean;
  onClear?: (what: PluginClearTarget) => Promise<boolean>;
}) {
  const t = useT();
  const [clearing, setClearing] = useState<PluginClearTarget | null>(null);
  // Credentials are voided, not cleared: each session gets a new one.
  const actionLabel = (what: PluginClearTarget) =>
    what === "lookups" ? t("plugins.stored.revoke") : t("plugins.stored.clear");
  const contentCell = (holds: boolean, kind: "memory" | "conversation") =>
    holds ? (
      <StatusBadge tone="pending">
        {t(`plugins.stored.holds.${kind}`)}
      </StatusBadge>
    ) : (
      <span className="text-muted-foreground">
        {t("plugins.stored.holds.none")}
      </span>
    );
  const cell = "px-3 py-1.5 align-top";
  // What and how long wrap; the rest keep to one line.
  const wraps = "whitespace-normal";
  return (
    <Panel className="@container/stored" data-testid="plugin-stored">
      <PanelHeader size="sm">
        <CardTitle>{t("plugins.stored.title")}</CardTitle>
      </PanelHeader>
      <Table className="text-xs">
        <TableHeader>
          <TableRow>
            <TableHead className="h-8 px-3 text-xs">
              {t("plugins.stored.columns.what")}
            </TableHead>
            <TableHead className="hidden h-8 px-3 text-xs @[560px]/stored:table-cell">
              {t("plugins.stored.columns.where")}
            </TableHead>
            <TableHead className="h-8 px-3 text-xs">
              {t("plugins.stored.columns.content")}
            </TableHead>
            <TableHead className="h-8 px-3 text-xs">
              {t("plugins.stored.columns.retention")}
            </TableHead>
            <TableHead className="h-8 px-3 text-right text-xs">
              {t("plugins.stored.columns.count")}
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {detail.host_storage.flatMap((kept) => {
            const what = clearable[kept.kind];
            const declared =
              kept.kind === "plugin_values" ? detail.storage : [];
            const row = (
              <TableRow
                className={cn(declared.length > 0 && "border-b-0")}
                data-testid="plugin-stored-item"
                key={kept.kind}
              >
                <TableCell className={cn(cell, wraps, "min-w-32")}>
                  <span className="flex min-w-0 items-start gap-1 font-medium">
                    <span className="min-w-0">
                      {t(`plugins.stored.kinds.${kept.kind}.label`)}
                    </span>
                    <HelpPopover
                      label={t("plugins.stored.whatLabel", {
                        label: t(`plugins.stored.kinds.${kept.kind}.label`),
                      })}
                    >
                      <p>{t(`plugins.stored.kinds.${kept.kind}.what`)}</p>
                    </HelpPopover>
                  </span>
                </TableCell>
                <TableCell
                  className={cn(
                    cell,
                    "hidden text-muted-foreground @[560px]/stored:table-cell",
                  )}
                >
                  {t(`plugins.stored.kinds.${kept.kind}.where`)}
                </TableCell>
                <TableCell className={cell}>
                  {contentCell(!!holdsMemory[kept.kind], "memory")}
                </TableCell>
                <TableCell className={cn(cell, wraps, "text-muted-foreground")}>
                  {t(`plugins.stored.kinds.${kept.kind}.retention`)}
                </TableCell>
                <TableCell className={cn(cell, "text-right")}>
                  <span className="inline-flex items-center justify-end gap-2">
                    {kept.count !== undefined ? (
                      <span className="text-muted-foreground tabular-nums">
                        {t("plugins.stored.count", { count: kept.count })}
                      </span>
                    ) : null}
                    {what && onClear ? (
                      <Button
                        className="h-6 shrink-0 px-2 text-xs"
                        disabled={busy || kept.count === 0}
                        onClick={() => setClearing(what)}
                        size="sm"
                        type="button"
                        variant="outline"
                      >
                        {actionLabel(what)}
                      </Button>
                    ) : null}
                  </span>
                </TableCell>
              </TableRow>
            );
            // What the plugin says it keeps in its store, under it.
            return [
              row,
              ...declared.map((item, index) => (
                <TableRow
                  className={cn(
                    "hover:bg-transparent",
                    index < declared.length - 1 && "border-b-0",
                  )}
                  data-testid="plugin-stored-declared"
                  key={`declared:${item.label}`}
                >
                  <TableCell
                    className={cn(
                      cell,
                      wraps,
                      "pt-0 pl-6 text-muted-foreground",
                    )}
                  >
                    {item.label}
                  </TableCell>
                  <TableCell
                    className={cn(
                      cell,
                      "hidden pt-0 @[560px]/stored:table-cell",
                    )}
                  />
                  <TableCell className={cn(cell, "pt-0")}>
                    {contentCell(!!item.content, "conversation")}
                  </TableCell>
                  <TableCell
                    className={cn(cell, wraps, "pt-0 text-muted-foreground")}
                  >
                    {item.retention}
                  </TableCell>
                  <TableCell className={cn(cell, "pt-0")} />
                </TableRow>
              )),
            ];
          })}
        </TableBody>
      </Table>
      <ConfirmDialog
        confirmLabel={clearing ? actionLabel(clearing) : ""}
        description={
          <p>
            {clearing ? t(`plugins.stored.clearConfirm.${clearing}`) : null}
          </p>
        }
        destructive
        disabled={busy}
        onCancel={() => setClearing(null)}
        onConfirm={() => {
          const what = clearing;
          setClearing(null);
          if (what && onClear) void onClear(what);
        }}
        open={clearing !== null}
        title={clearing ? t(`plugins.stored.clearTitle.${clearing}`) : ""}
      />
    </Panel>
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

/** The projects the plugin credits, as a row of the overview's last card. */
function CreditsCard({ credits }: { credits: PluginCredit[] }) {
  const t = useT();
  return (
    <Panel data-testid="plugin-credits">
      <dl className={cn("grid p-3 text-xs", labelColumns)}>
        <dt className="text-muted-foreground">
          {t("plugins.overview.credits")}
        </dt>
        <dd className="grid min-w-0 gap-2">
          {credits.map((credit) => (
            <div className="grid min-w-0 gap-0.5" key={credit.url}>
              <div className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5">
                <span className="font-medium">{credit.name}</span>
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
                <ExternalLink className="text-xs" href={credit.url}>
                  {displayAddress(credit.url)}
                </ExternalLink>
              </div>
              {credit.note ? (
                <p className="leading-5 text-muted-foreground">{credit.note}</p>
              ) : null}
            </div>
          ))}
        </dd>
      </dl>
    </Panel>
  );
}
