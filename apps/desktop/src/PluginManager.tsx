import { useCallback, useEffect, useMemo, useState } from "react";

import {
  Blocks,
  Plus,
  RefreshCw,
  Shredder as Trash2,
} from "@/components/icons";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { DataField } from "@/components/DataRow";
import { EmptyState } from "@/components/EmptyState";
import { Field } from "@/components/Field";
import { FormMessage } from "@/components/FormMessage";
import { HelpPopover } from "@/components/HelpPopover";
import { IconButton } from "@/components/IconButton";
import { LoadingState } from "@/components/LoadingState";
import { Panel, PanelHeader } from "@/components/Panel";
import { PluginView } from "@/components/PluginView";
import { SplitWorkspace } from "@/components/SplitWorkspace";
import { StatusBadge } from "@/components/StatusBadge";
import type { StatusTone } from "@/components/StatusDot";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";
import {
  deletePluginCredential,
  getPlugin,
  getPluginStatus,
  listPlugins,
  reloadPlugins,
  savePluginCredential,
  testPlugin,
  updatePlugin,
} from "./bridge";
import { controlErrorMessage } from "./control-error";
import { useT } from "./i18n";
import { PageHeader } from "./PageHeader";
import {
  configBytes,
  MAX_PLUGIN_CONFIG_BYTES,
  type PluginDetail,
  type PluginList,
  type PluginLoadError,
  type PluginStatus,
  type PluginSummary,
  type PluginTestResult,
} from "./plugin-model";

type Translate = ReturnType<typeof useT>;
type DetailTab = "overview" | "network" | "config" | "status";

/** i18n keys cannot hold the dots of a capability name. */
function capabilityLabel(capability: string, t: Translate): string {
  const key = capability.replace(/\.([a-z])/g, (_, letter: string) =>
    letter.toUpperCase(),
  );
  return t(`plugins.capabilities.${key}`, { defaultValue: capability });
}

function loadErrorText(error: PluginLoadError, t: Translate): string {
  const reason = t(`plugins.loadErrors.${error.code}`, {
    defaultValue: error.code,
  });
  const where =
    error.file && error.line
      ? t("plugins.errorAt", { file: error.file, line: error.line })
      : (error.file ?? "");
  return [where, reason].filter(Boolean).join(" · ");
}

function pluginTone(plugin: PluginSummary): StatusTone {
  if (plugin.state === "failed") return "negative";
  return plugin.enabled ? "positive" : "neutral";
}

function pluginState(plugin: PluginSummary, t: Translate): string {
  if (plugin.state === "failed") return t("plugins.state.failed");
  return plugin.enabled
    ? t("plugins.state.running")
    : t("plugins.state.disabled");
}

/** Plain http to a host other than this machine, as Core tells it. */
function plaintextOrigin(raw: string): boolean {
  try {
    const url = new URL(raw);
    if (url.protocol !== "http:") return false;
    const host = url.hostname.replace(/^\[|\]$/g, "");
    return !(host === "localhost" || host === "::1" || host.startsWith("127."));
  } catch {
    return false;
  }
}

/** The plugin to show after the list changes: the same one while it is listed, else the first. */
function keptSelection(current: string | null, list: PluginList) {
  return current && list.plugins.some((plugin) => plugin.key === current)
    ? current
    : (list.plugins[0]?.key ?? null);
}

function validOrigin(raw: string): boolean {
  try {
    const url = new URL(raw);
    return (
      (url.protocol === "http:" || url.protocol === "https:") &&
      url.hostname !== "" &&
      url.username === "" &&
      url.password === ""
    );
  } catch {
    return false;
  }
}

export function PluginManager({
  coreSessionKey,
  isReady,
}: {
  coreSessionKey: string | null;
  isReady: boolean;
}) {
  const t = useT();
  const [list, setList] = useState<PluginList | null>(null);
  const [listError, setListError] = useState<string | null>(null);
  const [rescanning, setRescanning] = useState(false);
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [detail, setDetail] = useState<PluginDetail | null>(null);
  const [detailError, setDetailError] = useState<string | null>(null);
  const [status, setStatus] = useState<PluginStatus | null>(null);
  const [statusError, setStatusError] = useState<string | null>(null);
  const [tab, setTab] = useState<DetailTab>("overview");
  const [testResult, setTestResult] = useState<PluginTestResult | null>(null);
  const [testing, setTesting] = useState(false);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [enabling, setEnabling] = useState<PluginDetail | null>(null);
  const [grants, setGrants] = useState<string[]>([]);

  const loadList = useCallback(async () => {
    try {
      const next = await listPlugins();
      setList(next);
      setListError(null);
      setSelectedKey((current) => keptSelection(current, next));
    } catch (error) {
      setListError(controlErrorMessage(error, t("plugins.loadFailed")));
    }
  }, [t]);

  useEffect(() => {
    if (!isReady) return;
    setSelectedKey(null);
    void loadList();
  }, [coreSessionKey, isReady, loadList]);

  const loadDetail = useCallback(
    async (key: string) => {
      try {
        const next = await getPlugin(key);
        setDetail(next);
        setDetailError(null);
        return next;
      } catch (error) {
        setDetailError(controlErrorMessage(error, t("plugins.detailFailed")));
        return null;
      }
    },
    [t],
  );

  const loadStatus = useCallback(
    async (key: string) => {
      try {
        setStatus(await getPluginStatus(key));
        setStatusError(null);
      } catch (error) {
        setStatusError(controlErrorMessage(error, t("plugins.statusFailed")));
      }
    },
    [t],
  );

  useEffect(() => {
    setDetail(null);
    setStatus(null);
    setTestResult(null);
    setActionError(null);
    setTab("overview");
    if (!selectedKey) return;
    void loadDetail(selectedKey).then((next) => {
      if (next?.state === "loaded") void loadStatus(selectedKey);
    });
  }, [selectedKey, loadDetail, loadStatus]);

  async function rescan() {
    setRescanning(true);
    try {
      const next = await reloadPlugins();
      setList(next);
      setListError(null);
      // A new selection loads through the effect; the kept one reloads here.
      const kept = keptSelection(selectedKey, next);
      setSelectedKey(kept);
      if (kept && kept === selectedKey) {
        const reloaded = await loadDetail(kept);
        if (reloaded?.state === "loaded") void loadStatus(kept);
      }
    } catch (error) {
      setListError(controlErrorMessage(error, t("plugins.loadFailed")));
    } finally {
      setRescanning(false);
    }
  }

  /** Applies a change and refreshes the list row that shows its state. */
  async function apply(change: () => Promise<PluginDetail>) {
    setBusy(true);
    setActionError(null);
    try {
      const next = await change();
      setDetail(next);
      setList((current) =>
        current
          ? {
              ...current,
              plugins: current.plugins.map((plugin) =>
                plugin.key === next.key
                  ? { ...plugin, ...summaryOf(next) }
                  : plugin,
              ),
            }
          : current,
      );
      return true;
    } catch (error) {
      setActionError(controlErrorMessage(error, t("plugins.saveFailed")));
      return false;
    } finally {
      setBusy(false);
    }
  }

  async function runTest(key: string) {
    setTesting(true);
    setActionError(null);
    try {
      setTestResult(await testPlugin(key));
      setTab("overview");
      // The test may change what the plugin's overview and counts show.
      void loadStatus(key);
    } catch (error) {
      setActionError(controlErrorMessage(error, t("plugins.testFailed")));
    } finally {
      setTesting(false);
    }
  }

  const plugins = list?.plugins ?? [];
  const selected = plugins.find((plugin) => plugin.key === selectedKey) ?? null;

  return (
    <section
      aria-labelledby="plugin-manager-heading"
      className="@container gutter-frame flex min-h-0 w-full min-w-0 flex-1 flex-col overflow-hidden"
    >
      <PageHeader
        actions={
          <>
            {list ? (
              <HelpPopover label={t("plugins.directoryHelpLabel")}>
                <p>{t("plugins.directoryHelp")}</p>
                <p className="font-mono text-xs break-all">{list.directory}</p>
                <p>{t("plugins.rescanHelp")}</p>
              </HelpPopover>
            ) : null}
            <Button
              disabled={!isReady || rescanning}
              onClick={() => void rescan()}
              size="sm"
              type="button"
              variant="outline"
            >
              <RefreshCw
                className={
                  rescanning
                    ? "animate-spin motion-reduce:animate-none"
                    : undefined
                }
              />
              {rescanning ? t("plugins.rescanning") : t("plugins.rescan")}
            </Button>
          </>
        }
        title={t("plugins.title")}
        titleId="plugin-manager-heading"
        titleSuffix={
          list ? (
            <Badge className="tabular-nums" variant="secondary">
              {plugins.length}
            </Badge>
          ) : null
        }
        variant="compact"
      />
      {listError ? (
        <FormMessage className="mb-3" tone="error">
          {listError}
        </FormMessage>
      ) : null}
      {list === null && !listError ? (
        <LoadingState className="flex-1" label={t("plugins.loading")} />
      ) : list !== null && plugins.length === 0 ? (
        <EmptyState
          className="flex-1"
          description={t("plugins.emptyHint")}
          illustration={<Blocks className="size-6" />}
          title={t("plugins.empty")}
          variant="page"
        />
      ) : list !== null ? (
        <SplitWorkspace className="@[720px]:grid-cols-[minmax(15rem,0.8fr)_minmax(0,1.7fr)]">
          <Panel className="flex min-h-0 flex-col overflow-hidden">
            <ul
              aria-label={t("plugins.listLabel")}
              className="min-h-0 flex-1 overflow-y-auto overscroll-contain"
            >
              {plugins.map((plugin) => (
                <li className="border-b last:border-b-0" key={plugin.key}>
                  <PluginRow
                    onSelect={() => setSelectedKey(plugin.key)}
                    plugin={plugin}
                    selected={plugin.key === selectedKey}
                  />
                </li>
              ))}
            </ul>
          </Panel>
          <Panel className="flex min-h-0 flex-col overflow-hidden">
            {selected === null ? (
              <EmptyState className="flex-1" title={t("plugins.select")} />
            ) : detailError ? (
              <FormMessage className="m-3" tone="error">
                {detailError}
              </FormMessage>
            ) : detail === null || detail.key !== selected.key ? (
              <LoadingState className="flex-1" label={t("plugins.loading")} />
            ) : (
              <PluginDetailPane
                actionError={actionError}
                busy={busy}
                detail={detail}
                onConfig={(config) =>
                  apply(() => updatePlugin(detail.key, { config }))
                }
                onCredentialClear={(name) =>
                  apply(() => deletePluginCredential(detail.key, name))
                }
                onCredentialSave={(name, input) =>
                  apply(() => savePluginCredential(detail.key, name, input))
                }
                onDisable={() =>
                  void apply(() => updatePlugin(detail.key, { enabled: false }))
                }
                onEnable={() => {
                  setGrants(detail.requested_capabilities);
                  setEnabling(detail);
                }}
                onOrigins={(origins) =>
                  apply(() =>
                    updatePlugin(detail.key, { network_origins: origins }),
                  )
                }
                onRefreshStatus={() => void loadStatus(detail.key)}
                onTab={setTab}
                onTest={() => void runTest(detail.key)}
                onTrusted={(trusted) =>
                  void apply(() =>
                    updatePlugin(detail.key, { privacy_trusted: trusted }),
                  )
                }
                status={status}
                statusError={statusError}
                tab={tab}
                testResult={testResult}
                testing={testing}
              />
            )}
          </Panel>
        </SplitWorkspace>
      ) : null}

      <ConfirmDialog
        confirmDisabled={busy}
        confirmLabel={t("plugins.enableDialog.confirm")}
        description={
          enabling ? (
            <EnableSummary
              detail={enabling}
              grants={grants}
              onGrants={setGrants}
            />
          ) : null
        }
        onCancel={() => setEnabling(null)}
        onConfirm={() => {
          const target = enabling;
          setEnabling(null);
          if (target)
            void apply(() =>
              updatePlugin(target.key, {
                enabled: true,
                granted_capabilities: grants,
              }),
            ).then((enabled) => {
              if (enabled && target.hooks.includes("describeStatus"))
                void loadStatus(target.key);
            });
        }}
        open={enabling !== null}
        title={t("plugins.enableDialog.title", { name: enabling?.name ?? "" })}
      />
    </section>
  );
}

function summaryOf(detail: PluginDetail): Partial<PluginSummary> {
  return {
    enabled: detail.enabled,
    granted_capabilities: detail.granted_capabilities,
    state: detail.state,
  };
}

function PluginRow({
  plugin,
  selected,
  onSelect,
}: {
  plugin: PluginSummary;
  selected: boolean;
  onSelect: () => void;
}) {
  const t = useT();
  return (
    <Button
      aria-current={selected ? "true" : undefined}
      className={cn(
        "flex h-auto w-full min-w-0 flex-col items-stretch justify-start gap-1 rounded-none px-3 py-2.5 text-left font-normal whitespace-normal text-foreground hover:bg-muted/60 focus-visible:ring-inset",
        selected && "bg-accent hover:bg-accent",
      )}
      data-testid="plugin-row"
      onClick={onSelect}
      type="button"
      variant="ghost"
    >
      <span className="flex min-w-0 items-center gap-2">
        <strong className="truncate text-sm font-semibold">
          {plugin.name}
        </strong>
        <span className="shrink-0 text-micro text-muted-foreground tabular-nums">
          {plugin.version}
        </span>
      </span>
      <span className="flex min-w-0 flex-wrap items-center gap-1.5">
        <StatusBadge tone={pluginTone(plugin)}>
          {pluginState(plugin, t)}
        </StatusBadge>
        <Badge className="h-5" variant="outline">
          {t(`plugins.source.${plugin.source}`)}
        </Badge>
        {plugin.needs_network ? (
          <Badge className="h-5" variant="outline">
            {t("plugins.needsNetwork")}
          </Badge>
        ) : null}
      </span>
      {plugin.error ? (
        <span
          className="truncate text-micro text-danger-foreground"
          title={plugin.error.message}
        >
          {loadErrorText(plugin.error, t)}
        </span>
      ) : null}
    </Button>
  );
}

function PluginDetailPane({
  detail,
  status,
  statusError,
  testResult,
  testing,
  busy,
  actionError,
  tab,
  onTab,
  onEnable,
  onDisable,
  onTest,
  onRefreshStatus,
  onOrigins,
  onTrusted,
  onCredentialSave,
  onCredentialClear,
  onConfig,
}: {
  detail: PluginDetail;
  status: PluginStatus | null;
  statusError: string | null;
  testResult: PluginTestResult | null;
  testing: boolean;
  busy: boolean;
  actionError: string | null;
  tab: DetailTab;
  onTab: (tab: DetailTab) => void;
  onEnable: () => void;
  onDisable: () => void;
  onTest: () => void;
  onRefreshStatus: () => void;
  onOrigins: (
    origins: Array<{ origin: string; allow_plaintext: boolean }>,
  ) => Promise<boolean>;
  onTrusted: (trusted: boolean) => void;
  onCredentialSave: (
    name: string,
    input: { secret: string; origin: string },
  ) => Promise<boolean>;
  onCredentialClear: (name: string) => Promise<boolean>;
  onConfig: (config: Record<string, unknown>) => Promise<boolean>;
}) {
  const t = useT();
  const loaded = detail.state === "loaded";
  const tabs: DetailTab[] = loaded
    ? detail.needs_network
      ? ["overview", "network", "config", "status"]
      : ["overview", "config", "status"]
    : [];
  return (
    <>
      <PanelHeader
        actions={
          loaded ? (
            <>
              {detail.hooks.includes("testConnection") ? (
                <Button
                  disabled={testing || busy}
                  onClick={onTest}
                  size="sm"
                  type="button"
                  variant="outline"
                >
                  {testing ? t("plugins.testing") : t("plugins.test")}
                </Button>
              ) : null}
              {detail.enabled ? (
                <Button
                  disabled={busy}
                  onClick={onDisable}
                  size="sm"
                  type="button"
                  variant="outline"
                >
                  {t("plugins.disable")}
                </Button>
              ) : (
                <Button
                  disabled={busy}
                  onClick={onEnable}
                  size="sm"
                  type="button"
                >
                  {t("plugins.enable")}
                </Button>
              )}
            </>
          ) : null
        }
        size="sm"
      >
        <div className="flex min-w-0 items-center gap-2">
          <h2 className="truncate text-sm font-semibold">{detail.name}</h2>
          <StatusBadge tone={pluginTone(detail)}>
            {pluginState(detail, t)}
          </StatusBadge>
        </div>
        <p className="truncate font-mono text-micro text-muted-foreground">
          {detail.key}
          {detail.version ? ` · ${detail.version}` : ""} ·{" "}
          {t(`plugins.source.${detail.source}`)}
        </p>
      </PanelHeader>
      {actionError ? (
        <FormMessage className="m-3 mb-0" tone="error">
          {actionError}
        </FormMessage>
      ) : null}
      {!loaded ? (
        <div className="grid gap-3 p-3">
          {detail.error ? (
            <FormMessage tone="error">
              <span className="block font-medium">
                {loadErrorText(detail.error, t)}
              </span>
              <span className="mt-1 block break-words">
                {detail.error.message}
              </span>
            </FormMessage>
          ) : null}
        </div>
      ) : (
        <Tabs
          className="min-h-0 min-w-0 flex-1 gap-0"
          onValueChange={(value) => onTab(value as DetailTab)}
          value={tabs.includes(tab) ? tab : "overview"}
        >
          <TabsList
            aria-label={t("plugins.tabsLabel")}
            className="mx-3 mt-2 shrink-0"
            scrollable
          >
            {tabs.map((item) => (
              <TabsTrigger key={item} value={item}>
                {t(`plugins.tabs.${item}`)}
              </TabsTrigger>
            ))}
          </TabsList>
          <TabsContent
            className="min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain p-3"
            data-tab-scroller
            value="overview"
          >
            <OverviewTab
              detail={detail}
              onRefresh={onRefreshStatus}
              status={status}
              statusError={statusError}
              testResult={testResult}
            />
          </TabsContent>
          {detail.needs_network ? (
            <TabsContent
              className="min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain p-3"
              data-tab-scroller
              value="network"
            >
              <NetworkTab
                busy={busy}
                detail={detail}
                onCredentialClear={onCredentialClear}
                onCredentialSave={onCredentialSave}
                onOrigins={onOrigins}
                onTrusted={onTrusted}
              />
            </TabsContent>
          ) : null}
          <TabsContent
            className="flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto overscroll-contain p-3"
            data-tab-scroller
            value="config"
          >
            <ConfigTab busy={busy} detail={detail} onSave={onConfig} />
          </TabsContent>
          <TabsContent
            className="min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain p-3"
            data-tab-scroller
            value="status"
          >
            <StatusTab status={status} statusError={statusError} />
          </TabsContent>
        </Tabs>
      )}
    </>
  );
}

function OverviewTab({
  detail,
  status,
  statusError,
  testResult,
  onRefresh,
}: {
  detail: PluginDetail;
  status: PluginStatus | null;
  statusError: string | null;
  testResult: PluginTestResult | null;
  onRefresh: () => void;
}) {
  const t = useT();
  const testTone: Record<PluginTestResult["status"], StatusTone> = {
    ok: "positive",
    error: "negative",
    failed: "negative",
  };
  return (
    <div className="grid gap-4" data-testid="plugin-overview">
      {testResult ? (
        <section className="grid gap-2" data-testid="plugin-test-result">
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <StatusBadge tone={testTone[testResult.status]}>
              {t(`plugins.testStatus.${testResult.status}`)}
            </StatusBadge>
            <span className="min-w-0 text-xs break-words">
              {testResult.summary}
            </span>
            <span className="text-micro text-muted-foreground tabular-nums">
              {testResult.elapsed_ms} ms
            </span>
          </div>
          {testResult.view && testResult.view.length > 0 ? (
            <PluginView
              blocks={testResult.view}
              truncated={testResult.truncated}
              truncatedLabel={t("plugins.truncated")}
            />
          ) : null}
        </section>
      ) : null}
      {detail.hooks.includes("describeStatus") ? (
        <section className="grid gap-2">
          <div className="flex items-center justify-between gap-2">
            <h3 className="text-xs font-medium">
              {t("plugins.overviewTitle")}
            </h3>
            <IconButton
              label={t("common.refresh")}
              onClick={onRefresh}
              size="icon-sm"
              type="button"
              variant="ghost"
            >
              <RefreshCw />
            </IconButton>
          </div>
          {statusError ? (
            <FormMessage tone="error">{statusError}</FormMessage>
          ) : status === null ? (
            <LoadingState label={t("plugins.loading")} />
          ) : status.view_error ? (
            <FormMessage tone="warning">{status.view_error}</FormMessage>
          ) : status.view && status.view.length > 0 ? (
            <PluginView
              blocks={status.view}
              truncated={status.truncated}
              truncatedLabel={t("plugins.truncated")}
            />
          ) : (
            <p className="text-xs text-muted-foreground">
              {/* Core runs describeStatus only with the views capability granted. */}
              {detail.granted_capabilities.includes("ui.views")
                ? t("plugins.noOverview")
                : t("plugins.overviewNeedsViews")}
            </p>
          )}
        </section>
      ) : null}
      <section className="grid gap-2">
        <h3 className="text-xs font-medium">{t("plugins.grantedTitle")}</h3>
        <div className="flex flex-wrap gap-1.5">
          {detail.requested_capabilities.length === 0 ? (
            <span className="text-xs text-muted-foreground">
              {t("plugins.noCapabilities")}
            </span>
          ) : (
            detail.requested_capabilities.map((capability) => (
              <StatusBadge
                key={capability}
                tone={
                  detail.granted_capabilities.includes(capability)
                    ? "positive"
                    : "neutral"
                }
              >
                {capabilityLabel(capability, t)}
              </StatusBadge>
            ))
          )}
        </div>
      </section>
      {detail.notice ? (
        <section className="grid gap-1">
          <h3 className="text-xs font-medium">
            {t("plugins.enableDialog.notice")}
          </h3>
          <p className="text-xs leading-5 whitespace-pre-wrap">
            {detail.notice}
          </p>
        </section>
      ) : null}
    </div>
  );
}

function NetworkTab({
  detail,
  busy,
  onOrigins,
  onTrusted,
  onCredentialSave,
  onCredentialClear,
}: {
  detail: PluginDetail;
  busy: boolean;
  onOrigins: (
    origins: Array<{ origin: string; allow_plaintext: boolean }>,
  ) => Promise<boolean>;
  onTrusted: (trusted: boolean) => void;
  onCredentialSave: (
    name: string,
    input: { secret: string; origin: string },
  ) => Promise<boolean>;
  onCredentialClear: (name: string) => Promise<boolean>;
}) {
  const t = useT();
  const saved = useMemo(
    () =>
      detail.network_origins.map(({ origin, allow_plaintext }) => ({
        origin,
        allow_plaintext,
      })),
    [detail.network_origins],
  );
  const [origins, setOrigins] = useState(saved);
  const [draft, setDraft] = useState("");
  const [draftError, setDraftError] = useState<string | null>(null);
  useEffect(() => setOrigins(saved), [saved]);
  const dirty = JSON.stringify(origins) !== JSON.stringify(saved);

  function add() {
    const origin = draft.trim();
    if (!validOrigin(origin)) {
      setDraftError(t("plugins.network.invalidOrigin"));
      return;
    }
    setDraftError(null);
    setOrigins((current) => [...current, { origin, allow_plaintext: false }]);
    setDraft("");
  }

  return (
    <div className="grid gap-5" data-testid="plugin-network">
      <section className="grid gap-2">
        <div className="flex items-center gap-1">
          <h3 className="text-xs font-medium">
            {t("plugins.network.origins")}
          </h3>
          <HelpPopover label={t("plugins.network.originsHelpLabel")}>
            <p>{t("plugins.network.originsHelp")}</p>
            {detail.network_purpose ? (
              <p>
                {t("plugins.enableDialog.purpose", {
                  purpose: detail.network_purpose,
                })}
              </p>
            ) : null}
          </HelpPopover>
        </div>
        {origins.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            {t("plugins.network.noOrigins")}
          </p>
        ) : (
          <ul className="grid gap-1.5">
            {origins.map((entry, index) => {
              const plaintext = plaintextOrigin(entry.origin);
              return (
                <li
                  className="flex min-w-0 flex-wrap items-center gap-2 rounded-md border px-2.5 py-1.5"
                  data-testid="plugin-origin"
                  key={`${entry.origin}:${index}`}
                >
                  <code
                    className="min-w-0 flex-1 truncate font-mono text-xs"
                    title={entry.origin}
                  >
                    {entry.origin}
                  </code>
                  {plaintext ? (
                    <>
                      <StatusBadge
                        tone={entry.allow_plaintext ? "pending" : "negative"}
                      >
                        {t("plugins.network.plaintext")}
                      </StatusBadge>
                      <Label className="flex items-center gap-1.5 text-xs font-normal">
                        <Checkbox
                          checked={entry.allow_plaintext}
                          onCheckedChange={(checked) =>
                            setOrigins((current) =>
                              current.map((item, itemIndex) =>
                                itemIndex === index
                                  ? {
                                      ...item,
                                      allow_plaintext: checked === true,
                                    }
                                  : item,
                              ),
                            )
                          }
                        />
                        {t("plugins.network.allowPlaintext")}
                      </Label>
                      <HelpPopover
                        label={t("plugins.network.plaintextHelpLabel")}
                      >
                        <p>{t("plugins.network.plaintextHelp")}</p>
                      </HelpPopover>
                    </>
                  ) : null}
                  <IconButton
                    label={t("plugins.network.remove", {
                      origin: entry.origin,
                    })}
                    onClick={() =>
                      setOrigins((current) =>
                        current.filter((_, itemIndex) => itemIndex !== index),
                      )
                    }
                    size="icon-sm"
                    type="button"
                    variant="ghost"
                  >
                    <Trash2 />
                  </IconButton>
                </li>
              );
            })}
          </ul>
        )}
        <form
          className="flex min-w-0 gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            add();
          }}
        >
          <Input
            aria-label={t("plugins.network.originLabel")}
            className="min-w-0 flex-1 font-mono"
            onChange={(event) => setDraft(event.target.value)}
            placeholder="https://memory.example.com:8443"
            value={draft}
          />
          <Button
            disabled={draft.trim() === ""}
            size="sm"
            type="submit"
            variant="outline"
          >
            <Plus />
            {t("plugins.network.add")}
          </Button>
        </form>
        {draftError ? (
          <FormMessage tone="error">{draftError}</FormMessage>
        ) : null}
        <div>
          <Button
            disabled={!dirty || busy}
            onClick={() => void onOrigins(origins)}
            size="sm"
            type="button"
          >
            {t("plugins.network.save")}
          </Button>
        </div>
      </section>

      <section className="flex items-center justify-between gap-3 rounded-md border px-3 py-2">
        <div className="flex items-center gap-1">
          <Label className="text-xs font-medium" htmlFor="plugin-trusted">
            {t("plugins.network.trusted")}
          </Label>
          <HelpPopover label={t("plugins.network.trustedHelpLabel")}>
            <p>{t("plugins.network.trustedHelp")}</p>
          </HelpPopover>
        </div>
        <Switch
          checked={detail.privacy_trusted}
          disabled={busy}
          id="plugin-trusted"
          onCheckedChange={(checked) => onTrusted(checked)}
        />
      </section>

      {detail.credentials.length > 0 ? (
        <section className="grid gap-3">
          <div className="flex items-center gap-1">
            <h3 className="text-xs font-medium">
              {t("plugins.credentials.title")}
            </h3>
            <HelpPopover label={t("plugins.credentials.helpLabel")}>
              <p>{t("plugins.credentials.help")}</p>
            </HelpPopover>
          </div>
          {detail.credentials.map((credential) => (
            <CredentialEditor
              busy={busy}
              credential={credential}
              key={credential.name}
              onClear={() => onCredentialClear(credential.name)}
              onSave={(input) => onCredentialSave(credential.name, input)}
              origins={detail.network_origins.map((origin) => origin.origin)}
            />
          ))}
        </section>
      ) : null}
    </div>
  );
}

function CredentialEditor({
  credential,
  origins,
  busy,
  onSave,
  onClear,
}: {
  credential: PluginDetail["credentials"][number];
  origins: string[];
  busy: boolean;
  onSave: (input: { secret: string; origin: string }) => Promise<boolean>;
  onClear: () => Promise<boolean>;
}) {
  const t = useT();
  const [secret, setSecret] = useState("");
  const [origin, setOrigin] = useState(credential.origin ?? origins[0] ?? "");
  useEffect(() => {
    setOrigin(credential.origin ?? origins[0] ?? "");
  }, [credential.origin, origins]);
  return (
    <form
      className="grid gap-2 rounded-md border p-3"
      data-testid="plugin-credential"
      onSubmit={(event) => {
        event.preventDefault();
        void onSave({ secret, origin }).then((saved) => {
          if (saved) setSecret("");
        });
      }}
    >
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <strong className="text-xs font-medium">{credential.label}</strong>
        <StatusBadge tone={credential.configured ? "positive" : "neutral"}>
          {credential.configured
            ? credential.hint
              ? t("plugins.credentials.configured", { hint: credential.hint })
              : t("plugins.credentials.configuredNoHint")
            : t("plugins.credentials.unset")}
        </StatusBadge>
        {credential.origin ? (
          <span className="truncate font-mono text-micro text-muted-foreground">
            {t("plugins.credentials.boundTo", { origin: credential.origin })}
          </span>
        ) : null}
      </div>
      <div className="grid gap-2 @[560px]:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
        <Field label={t("plugins.credentials.origin")}>
          <Select
            disabled={origins.length === 0}
            onValueChange={setOrigin}
            value={origin}
          >
            <SelectTrigger
              aria-label={t("plugins.credentials.origin")}
              className="w-full"
            >
              <SelectValue placeholder={t("plugins.credentials.needOrigin")} />
            </SelectTrigger>
            <SelectContent>
              {origins.map((item) => (
                <SelectItem key={item} value={item}>
                  {item}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field label={t("plugins.credentials.secret")}>
          <Input
            aria-label={t("plugins.credentials.secret")}
            autoComplete="off"
            onChange={(event) => setSecret(event.target.value)}
            type="password"
            value={secret}
          />
        </Field>
      </div>
      <div className="flex gap-2">
        <Button
          disabled={busy || secret === "" || origin === ""}
          size="sm"
          type="submit"
        >
          {t("plugins.credentials.save")}
        </Button>
        {credential.configured ? (
          <Button
            disabled={busy}
            onClick={() => void onClear()}
            size="sm"
            type="button"
            variant="outline"
          >
            {t("plugins.credentials.clear")}
          </Button>
        ) : null}
      </div>
    </form>
  );
}

function ConfigTab({
  detail,
  busy,
  onSave,
}: {
  detail: PluginDetail;
  busy: boolean;
  onSave: (config: Record<string, unknown>) => Promise<boolean>;
}) {
  const t = useT();
  const saved = useMemo(
    () => JSON.stringify(detail.config, null, 2),
    [detail.config],
  );
  const [text, setText] = useState(saved);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => setText(saved), [saved]);
  const bytes = configBytes(text);

  function parse(): Record<string, unknown> | null {
    try {
      const value: unknown = JSON.parse(text);
      if (typeof value !== "object" || value === null || Array.isArray(value)) {
        setError(t("plugins.config.invalid"));
        return null;
      }
      if (bytes > MAX_PLUGIN_CONFIG_BYTES) {
        setError(t("plugins.config.tooLarge"));
        return null;
      }
      setError(null);
      return value as Record<string, unknown>;
    } catch {
      setError(t("plugins.config.invalid"));
      return null;
    }
  }

  return (
    <div
      className="flex min-h-0 flex-1 flex-col gap-2"
      data-testid="plugin-config"
    >
      <div className="flex items-center gap-1">
        <h3 className="text-xs font-medium">{t("plugins.config.title")}</h3>
        <HelpPopover label={t("plugins.config.helpLabel")}>
          <p>{t("plugins.config.help")}</p>
        </HelpPopover>
      </div>
      <Textarea
        aria-label={t("plugins.config.title")}
        className="min-h-48 flex-1 resize-none font-mono text-xs"
        onChange={(event) => setText(event.target.value)}
        spellCheck={false}
        value={text}
      />
      {error ? <FormMessage tone="error">{error}</FormMessage> : null}
      <div className="flex flex-wrap items-center gap-2">
        <span
          className={cn(
            "text-micro tabular-nums text-muted-foreground",
            bytes > MAX_PLUGIN_CONFIG_BYTES && "text-danger-foreground",
          )}
        >
          {t("plugins.config.size", { size: (bytes / 1024).toFixed(1) })}
        </span>
        <span className="flex-1" />
        <Button
          onClick={() => {
            const value = parse();
            if (value) setText(JSON.stringify(value, null, 2));
          }}
          size="sm"
          type="button"
          variant="outline"
        >
          {t("plugins.config.format")}
        </Button>
        <Button
          disabled={busy || text === saved}
          onClick={() => {
            const value = parse();
            if (value) void onSave(value);
          }}
          size="sm"
          type="button"
        >
          {t("plugins.config.save")}
        </Button>
      </div>
    </div>
  );
}

function StatusTab({
  status,
  statusError,
}: {
  status: PluginStatus | null;
  statusError: string | null;
}) {
  const t = useT();
  if (statusError) return <FormMessage tone="error">{statusError}</FormMessage>;
  if (status === null) return <LoadingState label={t("plugins.loading")} />;
  const failures = Object.entries(status.failures);
  return (
    <div className="grid gap-4" data-testid="plugin-status">
      <div className="grid grid-cols-2 gap-3 @[560px]:grid-cols-4">
        <DataField label={t("plugins.status.calls")} value={status.calls} />
        <DataField
          label={t("plugins.status.timeouts")}
          value={status.timeouts}
        />
        <DataField
          label={t("plugins.status.memoryLimits")}
          value={status.memory_limits}
        />
        <DataField
          label={t("plugins.status.network")}
          value={t("plugins.status.networkValue", {
            requests: status.network.requests,
            denied: status.network.denied,
            errors: status.network.errors,
          })}
        />
      </div>
      {failures.length > 0 ? (
        <section className="grid gap-1">
          <h3 className="text-xs font-medium">
            {t("plugins.status.failures")}
          </h3>
          <ul className="grid gap-0.5 text-xs">
            {failures.map(([reason, count]) => (
              <li className="flex justify-between gap-3" key={reason}>
                <span>
                  {t(`plugins.status.reasons.${reason}`, {
                    defaultValue: reason,
                  })}
                </span>
                <span className="tabular-nums">{count}</span>
              </li>
            ))}
          </ul>
        </section>
      ) : null}
    </div>
  );
}

/** What enabling grants: the capabilities, each one the user may untick, the plugin's notice and the attributes it registers. */
function EnableSummary({
  detail,
  grants,
  onGrants,
}: {
  detail: PluginDetail;
  grants: string[];
  onGrants: (grants: string[]) => void;
}) {
  const t = useT();
  return (
    <div
      className="grid gap-3 text-sm text-foreground"
      data-testid="plugin-enable-summary"
    >
      {detail.requested_capabilities.length > 0 ? (
        <fieldset className="grid gap-1.5">
          <legend className="mb-1 text-xs text-muted-foreground">
            {t("plugins.enableDialog.capabilities")}
          </legend>
          {detail.requested_capabilities.map((capability) => (
            <Label
              className="flex items-center gap-2 text-sm font-normal"
              key={capability}
            >
              <Checkbox
                checked={grants.includes(capability)}
                onCheckedChange={(checked) =>
                  onGrants(
                    checked === true
                      ? [...grants, capability]
                      : grants.filter((item) => item !== capability),
                  )
                }
              />
              {capabilityLabel(capability, t)}
            </Label>
          ))}
        </fieldset>
      ) : null}
      {detail.network_purpose ? (
        <p className="text-xs">
          {t("plugins.enableDialog.purpose", {
            purpose: detail.network_purpose,
          })}
        </p>
      ) : null}
      {detail.notice ? (
        <div className="grid gap-1">
          <span className="text-xs font-medium">
            {t("plugins.enableDialog.notice")}
          </span>
          <p className="text-xs leading-5 whitespace-pre-wrap">
            {detail.notice}
          </p>
        </div>
      ) : null}
      {detail.metadata.length > 0 ? (
        <div className="grid gap-1">
          <span className="text-xs font-medium">
            {t("plugins.enableDialog.metadata")}
          </span>
          <ul className="grid gap-0.5 text-xs">
            {detail.metadata.map((attribute) => (
              <li key={attribute.key}>
                {attribute.label}{" "}
                <code className="font-mono">({attribute.key})</code>
                {attribute.marker
                  ? ` · ${t("plugins.enableDialog.marker", { marker: attribute.marker })}`
                  : ""}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </div>
  );
}
