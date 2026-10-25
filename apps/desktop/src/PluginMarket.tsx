import { useCallback, useEffect, useMemo, useState } from "react";

import { EmptyState } from "@/components/EmptyState";
import { ExternalLink } from "@/components/ExternalLink";
import { FormMessage } from "@/components/FormMessage";
import { HelpDisclosure } from "@/components/HelpDisclosure";
import { IconButton } from "@/components/IconButton";
import { Boxes, RefreshCw } from "@/components/icons";
import { LoadingState } from "@/components/LoadingState";
import { Panel } from "@/components/Panel";
import { PluginIcon, pluginAvatar } from "@/components/PluginIcon";
import { SearchInput } from "@/components/SearchInput";
import { StatusBadge } from "@/components/StatusBadge";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input, verbatimTextInput } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

import { getPluginMarket, pluginInstallFailureOf } from "./bridge";
import { controlErrorMessage } from "./control-error";
import { useT } from "./i18n";
import { installFailureText } from "./PluginInstallReview";
import { ModelChips, TaskEndpoints } from "./PluginTaskParts";
import { taskAuthLabel, taskSurfaceLabels } from "./plugin-task-labels";
import type { PluginMarket, PluginMarketEntry } from "./plugin-model";

/** The marketplace the page last opened, kept for this computer. */
const MARKET_INDEX_KEY = "astrlink.plugins.marketIndex";

function rememberedIndex(): string {
  try {
    return window.localStorage.getItem(MARKET_INDEX_KEY) ?? "";
  } catch {
    return "";
  }
}

function rememberIndex(index: string) {
  try {
    if (index) window.localStorage.setItem(MARKET_INDEX_KEY, index);
    else window.localStorage.removeItem(MARKET_INDEX_KEY);
  } catch {
    // The choice is a convenience: without storage the official one opens.
  }
}

/** Whether a version is newer than another, by their numbers. */
function newer(candidate: string, current: string): boolean {
  const parts = (version: string) =>
    version
      .split(/[-+]/)[0]
      .split(".")
      .map((part) => Number.parseInt(part, 10) || 0);
  const a = parts(candidate);
  const b = parts(current);
  for (let index = 0; index < Math.max(a.length, b.length); index++) {
    const left = a[index] ?? 0;
    const right = b[index] ?? 0;
    if (left !== right) return left > right;
  }
  return false;
}

/** What the card's button does for an entry: install, update, or nothing. */
function entryState(
  entry: PluginMarketEntry,
): "install" | "update" | "installed" {
  if (!entry.installed) return "install";
  return newer(entry.latest, entry.installed) ? "update" : "installed";
}

/**
 * A plugin marketplace in new-api's index format: its plugins as cards, a
 * card opening what the plugin serves and its versions. Installing hands
 * the link of a version to the install dialog, which reviews it first.
 * `compact` lists the plugins as rows, inside the install dialog.
 */
export function PluginMarketView({
  isReady,
  onInstall,
  compact = false,
}: {
  isReady: boolean;
  /** Reviews the install of the version a link names. */
  onInstall: (link: string) => void;
  compact?: boolean;
}) {
  const t = useT();
  const [index, setIndex] = useState(rememberedIndex);
  const [customOpen, setCustomOpen] = useState(index !== "");
  const [draft, setDraft] = useState(index);
  const [market, setMarket] = useState<PluginMarket | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState<PluginMarketEntry | null>(null);

  const load = useCallback(
    async (target: string) => {
      setLoading(true);
      setError(null);
      try {
        setMarket(await getPluginMarket(target || undefined));
      } catch (cause) {
        const failure = pluginInstallFailureOf(cause);
        setMarket(null);
        setError(
          failure
            ? installFailureText(failure, t)
            : controlErrorMessage(cause, t("plugins.market.failed")),
        );
      } finally {
        setLoading(false);
      }
    },
    [t],
  );

  useEffect(() => {
    if (isReady) void load(index);
  }, [index, isReady, load]);

  const shown = useMemo(() => {
    const needle = query.trim().toLowerCase();
    const plugins = market?.plugins ?? [];
    if (!needle) return plugins;
    return plugins.filter((entry) =>
      [entry.name, entry.key, entry.description ?? "", ...entry.models].some(
        (text) => text.toLowerCase().includes(needle),
      ),
    );
  }, [market, query]);

  const choose = (next: string) => {
    rememberIndex(next);
    setIndex(next);
  };

  return (
    <div
      className="flex min-h-0 min-w-0 flex-1 flex-col gap-3"
      data-testid="plugin-market"
    >
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <Select
          disabled={!isReady}
          onValueChange={(value) => {
            if (value === "official") {
              setCustomOpen(false);
              choose("");
            } else setCustomOpen(true);
          }}
          value={customOpen ? "custom" : "official"}
        >
          <SelectTrigger
            aria-label={t("plugins.market.source")}
            className="w-40"
            size="sm"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="official">
              {t("plugins.market.official")}
            </SelectItem>
            <SelectItem value="custom">{t("plugins.market.custom")}</SelectItem>
          </SelectContent>
        </Select>
        {customOpen ? (
          <form
            className="flex min-w-0 flex-[1_1_18rem] items-center gap-2"
            onSubmit={(event) => {
              event.preventDefault();
              choose(draft.trim());
            }}
          >
            <Input
              {...verbatimTextInput}
              aria-label={t("plugins.market.indexLabel")}
              className="h-8 min-w-0 flex-1 font-mono text-xs"
              onChange={(event) => setDraft(event.target.value)}
              placeholder="https://…/index.json"
              value={draft}
            />
            <Button
              disabled={!draft.trim() || !isReady}
              size="sm"
              type="submit"
              variant="outline"
            >
              {t("plugins.market.open")}
            </Button>
          </form>
        ) : null}
        <SearchInput
          className={cn("min-w-40 flex-[1_1_12rem]", customOpen && "max-w-64")}
          clearLabel={t("common.clearSearch")}
          label={t("plugins.market.search")}
          onValueChange={setQuery}
          placeholder={t("plugins.market.search")}
          value={query}
        />
        <IconButton
          disabled={!isReady || loading}
          label={t("common.refresh")}
          onClick={() => void load(index)}
          type="button"
          variant="outline"
        >
          <RefreshCw
            className={
              loading ? "animate-spin motion-reduce:animate-none" : undefined
            }
          />
        </IconButton>
      </div>
      {market && !compact ? (
        <p className="-mt-1 text-micro text-muted-foreground">
          {t("plugins.market.count", {
            name: market.name || t("plugins.market.official"),
            count: market.plugins.length,
          })}
        </p>
      ) : null}
      {error ? <FormMessage tone="error">{error}</FormMessage> : null}
      {loading && !market ? (
        <LoadingState className="flex-1" label={t("plugins.market.loading")} />
      ) : market && shown.length === 0 ? (
        <EmptyState
          className="flex-1"
          illustration={<Boxes className="size-6 text-muted-foreground" />}
          title={
            query.trim()
              ? t("plugins.market.noMatch")
              : t("plugins.market.empty")
          }
        />
      ) : compact ? (
        <ul
          aria-label={t("plugins.market.listLabel")}
          className="min-h-0 overflow-y-auto overscroll-contain rounded-md border"
        >
          {shown.map((entry) => (
            <li className="border-b last:border-b-0" key={entry.key}>
              <MarketRow
                entry={entry}
                onInstall={onInstall}
                onOpen={() => setOpen(entry)}
              />
            </li>
          ))}
        </ul>
      ) : (
        <ul
          aria-label={t("plugins.market.listLabel")}
          className="grid min-h-0 grid-cols-1 content-start gap-3 overflow-y-auto overscroll-contain pb-1 @[640px]:grid-cols-2 @[1040px]:grid-cols-3"
        >
          {shown.map((entry) => (
            <li className="min-w-0" key={entry.key}>
              <MarketCard
                entry={entry}
                onInstall={onInstall}
                onOpen={() => setOpen(entry)}
              />
            </li>
          ))}
        </ul>
      )}
      {open ? (
        <MarketEntryDialog
          entry={open}
          onClose={() => setOpen(null)}
          onInstall={(link) => {
            setOpen(null);
            onInstall(link);
          }}
        />
      ) : null}
    </div>
  );
}

function EntryAction({
  entry,
  onInstall,
  size = "xs",
}: {
  entry: PluginMarketEntry;
  onInstall: (link: string) => void;
  size?: "xs" | "sm";
}) {
  const t = useT();
  const state = entryState(entry);
  if (state === "installed")
    return (
      <StatusBadge className="shrink-0" tone="positive">
        {t("plugins.market.installed")}
      </StatusBadge>
    );
  return (
    <Button
      className="shrink-0"
      onClick={(event) => {
        event.stopPropagation();
        onInstall(entry.link);
      }}
      size={size}
      type="button"
      variant={state === "update" ? "default" : "outline"}
    >
      {state === "update"
        ? t("plugins.market.update", { version: entry.latest })
        : t("plugins.market.install")}
    </Button>
  );
}

/** What a marketplace plugin serves and how many models, in one line. */
function EntryFacts({ entry }: { entry: PluginMarketEntry }) {
  const t = useT();
  const surfaces = taskSurfaceLabels(entry.protocols, entry.routes, t);
  return (
    <span className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-1 text-micro text-muted-foreground">
      <span className="tabular-nums">
        {t("plugins.market.models", { count: entry.models.length })}
      </span>
      {surfaces.map((surface) => (
        <Badge
          className="px-1.5 py-0 font-normal"
          key={surface}
          variant="outline"
        >
          {surface}
        </Badge>
      ))}
    </span>
  );
}

function MarketCard({
  entry,
  onInstall,
  onOpen,
}: {
  entry: PluginMarketEntry;
  onInstall: (link: string) => void;
  onOpen: () => void;
}) {
  const t = useT();
  return (
    <Panel
      className="group flex h-full min-h-36 cursor-pointer flex-col gap-2.5 p-3 transition-colors hover:border-ring/40 hover:bg-muted/30"
      data-testid="plugin-market-card"
      onClick={onOpen}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onOpen();
        }
      }}
      role="button"
      tabIndex={0}
    >
      <div className="flex min-w-0 items-start gap-2.5">
        <PluginIcon
          className="size-9 rounded-md"
          icon={entry.icon_url}
          label={pluginAvatar({ name: entry.name, task: { icon: entry.icon } })}
        />
        <div className="grid min-w-0 flex-1 gap-0.5">
          <span className="flex min-w-0 items-center gap-1.5">
            <strong className="truncate text-sm font-semibold">
              {entry.name}
            </strong>
            <span className="shrink-0 text-micro text-muted-foreground tabular-nums">
              {entry.latest}
            </span>
          </span>
        </div>
        <EntryAction entry={entry} onInstall={onInstall} />
      </div>
      <p className="line-clamp-2 min-h-8 text-xs leading-4 text-text-secondary">
        {entry.description || t("plugins.market.noDescription")}
      </p>
      <div className="mt-auto flex min-w-0 items-center justify-between gap-2">
        <EntryFacts entry={entry} />
        {entry.installed && entryState(entry) === "update" ? (
          <span className="shrink-0 text-micro text-muted-foreground">
            {t("plugins.market.installedVersion", { version: entry.installed })}
          </span>
        ) : null}
      </div>
    </Panel>
  );
}

function MarketRow({
  entry,
  onInstall,
  onOpen,
}: {
  entry: PluginMarketEntry;
  onInstall: (link: string) => void;
  onOpen: () => void;
}) {
  return (
    <div
      className="flex min-w-0 cursor-pointer items-center gap-2.5 px-3 py-2 hover:bg-muted/60"
      data-testid="plugin-market-row"
      onClick={onOpen}
      onKeyDown={(event) => {
        if (event.key === "Enter") onOpen();
      }}
      role="button"
      tabIndex={0}
    >
      <PluginIcon
        className="size-7 rounded-md"
        icon={entry.icon_url}
        label={pluginAvatar({ name: entry.name, task: { icon: entry.icon } })}
      />
      <span className="grid min-w-0 flex-1 gap-0.5">
        <span className="flex min-w-0 items-center gap-1.5">
          <strong className="truncate text-sm font-medium">{entry.name}</strong>
          <span className="shrink-0 text-micro text-muted-foreground tabular-nums">
            {entry.latest}
          </span>
        </span>
        <EntryFacts entry={entry} />
      </span>
      <EntryAction entry={entry} onInstall={onInstall} />
    </div>
  );
}

/** What a marketplace plugin serves, its models and its versions. */
function MarketEntryDialog({
  entry,
  onClose,
  onInstall,
}: {
  entry: PluginMarketEntry;
  onClose: () => void;
  onInstall: (link: string) => void;
}) {
  const t = useT();
  const latest = entry.versions.find(
    (version) => version.version === entry.latest,
  );
  const state = entryState(entry);
  return (
    <Dialog open onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="sm:max-w-xl" data-testid="plugin-market-entry">
        <DialogHeader className="text-left">
          <div className="flex min-w-0 items-center gap-3">
            <PluginIcon
              className="size-10 rounded-md"
              icon={entry.icon_url}
              label={pluginAvatar({
                name: entry.name,
                task: { icon: entry.icon },
              })}
            />
            <div className="grid min-w-0 gap-0.5">
              <DialogTitle className="flex min-w-0 items-center gap-2">
                <span className="truncate">{entry.name}</span>
                <span className="text-xs font-normal text-muted-foreground tabular-nums">
                  {entry.latest}
                </span>
              </DialogTitle>
              <DialogDescription className="line-clamp-3 text-xs leading-5">
                {entry.description || t("plugins.market.noDescription")}
              </DialogDescription>
            </div>
          </div>
        </DialogHeader>
        <div className="grid max-h-[60vh] min-h-0 gap-4 overflow-y-auto overscroll-contain text-sm">
          <section className="grid gap-1.5">
            <h3 className="text-xs font-medium text-muted-foreground">
              {t("plugins.task.endpoints")}
            </h3>
            <TaskEndpoints protocols={entry.protocols} routes={entry.routes} />
          </section>
          {latest?.base_url || latest?.auth ? (
            <section className="grid gap-1 text-xs">
              <h3 className="text-xs font-medium text-muted-foreground">
                {t("plugins.task.connection")}
              </h3>
              <dl className="grid grid-cols-[6rem_minmax(0,1fr)] gap-x-3 gap-y-1">
                {latest.base_url ? (
                  <>
                    <dt className="text-muted-foreground">
                      {t("plugins.task.baseUrl")}
                    </dt>
                    <dd className="truncate font-mono">{latest.base_url}</dd>
                  </>
                ) : null}
                {latest.auth ? (
                  <>
                    <dt className="text-muted-foreground">
                      {t("plugins.task.authLabel")}
                    </dt>
                    <dd>{taskAuthLabel(latest.auth, t)}</dd>
                  </>
                ) : null}
                {latest.allowed_hosts?.length ? (
                  <>
                    <dt className="text-muted-foreground">
                      {t("plugins.task.allowedHosts")}
                    </dt>
                    <dd className="font-mono">
                      {latest.allowed_hosts.join(", ")}
                    </dd>
                  </>
                ) : null}
              </dl>
            </section>
          ) : null}
          <section className="grid gap-1.5">
            <h3 className="text-xs font-medium text-muted-foreground">
              {t("plugins.market.models", { count: entry.models.length })}
            </h3>
            <ModelChips models={entry.models} />
          </section>
          {entry.versions.length > 1 ? (
            <HelpDisclosure title={t("plugins.market.versions")}>
              <ul className="grid gap-0.5" data-testid="plugin-market-versions">
                {entry.versions.map((version) => (
                  <li
                    className="flex items-center justify-between gap-2 py-0.5"
                    key={version.version}
                  >
                    <span className="tabular-nums text-foreground">
                      {version.version}
                      {version.version === entry.installed ? (
                        <span className="ml-2 text-muted-foreground">
                          {t("plugins.market.installed")}
                        </span>
                      ) : null}
                    </span>
                    {version.version !== entry.installed ? (
                      <Button
                        onClick={() => onInstall(version.link)}
                        size="xs"
                        type="button"
                        variant="ghost"
                      >
                        {t("plugins.market.installVersion")}
                      </Button>
                    ) : null}
                  </li>
                ))}
              </ul>
            </HelpDisclosure>
          ) : null}
        </div>
        <DialogFooter>
          {entry.website ? (
            <ExternalLink
              className="mr-auto self-center text-xs"
              href={entry.website}
            >
              {t("plugins.market.website")}
            </ExternalLink>
          ) : null}
          <Button onClick={onClose} size="sm" type="button" variant="outline">
            {t("common.close")}
          </Button>
          {state !== "installed" ? (
            <Button
              onClick={() => onInstall(entry.link)}
              size="sm"
              type="button"
            >
              {state === "update"
                ? t("plugins.market.update", { version: entry.latest })
                : t("plugins.market.install")}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
