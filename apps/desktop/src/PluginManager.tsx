import { useCallback, useEffect, useRef, useState } from "react";

import { Blocks, Plus, RefreshCw } from "@/components/icons";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { DataField } from "@/components/DataRow";
import { EmptyState } from "@/components/EmptyState";
import { FormMessage } from "@/components/FormMessage";
import { HelpPopover } from "@/components/HelpPopover";
import { IconButton } from "@/components/IconButton";
import { LoadingState } from "@/components/LoadingState";
import { Panel, PanelHeader } from "@/components/Panel";
import { PluginIcon } from "@/components/PluginIcon";
import { SplitWorkspace } from "@/components/SplitWorkspace";
import { StatusBadge } from "@/components/StatusBadge";
import type { StatusTone } from "@/components/StatusDot";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
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
import { cn } from "@/lib/utils";
import {
  clearPluginData,
  deletePluginCredential,
  getPlugin,
  listenPluginApprovalFinished,
  openPluginApproval,
  pluginApprovalOf,
  getPluginCreditStars,
  getPluginHostSettings,
  getPluginStats,
  getPluginStatus,
  listPlugins,
  pluginInstallFailureOf,
  reloadPlugins,
  rollbackPlugin,
  savePluginCredential,
  testPlugin,
  uninstallPlugin,
  updatePlugin,
  updatePluginHostSettings,
} from "./bridge";
import { controlErrorCode, controlErrorMessage } from "./control-error";
import { isWebEdition } from "./edition";
import { PluginApprovalSummary } from "./PluginApprovalSummary";
import { useT } from "./i18n";
import { PageHeader } from "./PageHeader";
import { PluginDevelop } from "./PluginDevelop";
import { PluginInstallDialog } from "./PluginInstall";
import {
  installFailureText,
  PluginInstallReviewView,
} from "./PluginInstallReview";
import { orderedPlugins, PluginOrderDialog } from "./PluginOrder";
import { PluginSourceCard } from "./PluginSource";
import { capabilityLabel } from "./plugin-labels";
import { PluginOverview } from "./PluginOverview";
import { PluginSettingsForm, type CredentialInput } from "./PluginSettingsForm";
import {
  MAX_INJECTION_BUDGET_BYTES,
  MIN_INJECTION_BUDGET_BYTES,
  type PluginApproval,
  type PluginClearTarget,
  type PluginCreditStars,
  type PluginDetail,
  type PluginHostSettings,
  type PluginHostSettingsUpdate,
  type PluginInstalledVersion,
  type PluginInstallReview,
  type PluginList,
  type PluginLoadError,
  type PluginSettingsUpdate,
  type PluginStats,
  type PluginStatus,
  type PluginSummary,
} from "./plugin-model";
import { DailyEffects } from "./PluginEffects";

type Translate = ReturnType<typeof useT>;
type DetailTab = "overview" | "settings" | "status" | "develop";

/** The injection budgets the plugin page offers, in KiB. */
const INJECTION_BUDGETS_KIB = [4, 8, 16, 32, 64].filter(
  (kib) =>
    kib * 1024 >= MIN_INJECTION_BUDGET_BYTES &&
    kib * 1024 <= MAX_INJECTION_BUDGET_BYTES,
);

/** i18n keys cannot hold the dots of a capability name. */
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

/** The plugin to show after the list changes: the same one while it is listed, else the first. */
function keptSelection(current: string | null, list: PluginList) {
  return current && list.plugins.some((plugin) => plugin.key === current)
    ? current
    : (list.plugins[0]?.key ?? null);
}

/** The origin of the server that sends credential, once Core allows it. */
function serverOrigin(detail: PluginDetail, credential: string) {
  const server = detail.settings.find(
    (setting) => setting.type === "server" && setting.credential === credential,
  );
  return server
    ? detail.network_origins.find((origin) => origin.setting === server.key)
        ?.origin
    : undefined;
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
  const [stats, setStats] = useState<PluginStats | null>(null);
  const [stars, setStars] = useState<PluginCreditStars | null>(null);
  // Counts the times the plugin was joined anew: enabled, or tested fine.
  const [linkPlay, setLinkPlay] = useState(0);
  const [tab, setTab] = useState<DetailTab>("overview");
  const [testing, setTesting] = useState(false);
  // Every detail Core sends starts the settings form afresh.
  const [revision, setRevision] = useState(0);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [enabling, setEnabling] = useState<PluginDetail | null>(null);
  const [grants, setGrants] = useState<string[]>([]);
  // A change that grants a plugin more waits for the user: in the
  // desktop's confirmation window (awaiting), or in the web console's own
  // dialog (confirming). afterApproval finishes what the change began, such
  // as the credentials of a server it added.
  const [awaiting, setAwaiting] = useState<string | null>(null);
  const [confirming, setConfirming] = useState<{
    key: string;
    update: PluginSettingsUpdate;
    approval: PluginApproval;
    kept: string[];
  } | null>(null);
  const [ordering, setOrdering] = useState(false);
  const [savingOrder, setSavingOrder] = useState(false);
  // The install dialog, on the link of an update already.
  const [installing, setInstalling] = useState<{ link?: string } | null>(null);
  // A rollback the web console's own dialog confirms.
  const [confirmingRollback, setConfirmingRollback] = useState<{
    key: string;
    version: PluginInstalledVersion;
    review: PluginInstallReview;
  } | null>(null);
  const [uninstalling, setUninstalling] = useState<PluginDetail | null>(null);
  const afterApproval = useRef<{
    key: string;
    run: (detail: PluginDetail | null) => Promise<void>;
  } | null>(null);
  const selectedRef = useRef<string | null>(null);
  const [host, setHost] = useState<PluginHostSettings | null>(null);
  const [hostError, setHostError] = useState<string | null>(null);

  const loadList = useCallback(async () => {
    try {
      const next = await listPlugins();
      setList(next);
      setListError(null);
      setSelectedKey((current) => keptSelection(current, next));
    } catch (error) {
      setListError(controlErrorMessage(error, t("plugins.loadFailed")));
    }
    try {
      setHost(await getPluginHostSettings());
    } catch {
      // The header leaves out what it could not read.
      setHost(null);
    }
  }, [t]);

  async function changeHost(update: PluginHostSettingsUpdate) {
    setHostError(null);
    try {
      setHost(await updatePluginHostSettings(update));
    } catch (error) {
      setHostError(controlErrorMessage(error, t("plugins.host.saveFailed")));
    }
  }

  /** Clears what the host keeps for the plugin, then shows the new counts. */
  async function clearData(key: string, what: PluginClearTarget) {
    setBusy(true);
    setActionError(null);
    try {
      await clearPluginData(key, what);
      await loadDetail(key);
      return true;
    } catch (error) {
      setActionError(
        controlErrorMessage(error, t("plugins.stored.clearFailed")),
      );
      return false;
    } finally {
      setBusy(false);
    }
  }

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
        setRevision((current) => current + 1);
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
      // The account is numbers from the request records: without it the
      // overview and the status page just leave it out.
      void getPluginStats(key)
        .then(setStats)
        .catch(() => setStats(null));
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
    setStats(null);
    setStars(null);
    setActionError(null);
    setTab("overview");
    if (!selectedKey) return;
    void loadDetail(selectedKey).then((next) => {
      if (next?.state !== "loaded") return;
      void loadStatus(selectedKey);
      // GitHub's answer comes apart from everything else, if at all.
      if (next.credits.length > 0)
        void getPluginCreditStars(selectedKey)
          .then(setStars)
          .catch(() => setStars(null));
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

  /** Shows a detail Core sent and the list row of its state. */
  function show(next: PluginDetail) {
    setDetail(next);
    setRevision((current) => current + 1);
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
  }

  /**
   * Saves a change of a plugin's settings. One that grants the plugin more
   * waits for the user, and comes back as "waiting": in the desktop's
   * confirmation window, or in the web console's own dialog.
   */
  async function save(
    key: string,
    update: PluginSettingsUpdate,
    approved = false,
  ): Promise<PluginDetail | "waiting"> {
    try {
      return approved
        ? await updatePlugin(key, update, true)
        : await updatePlugin(key, update);
    } catch (error) {
      if (controlErrorCode(error) === "approval_pending") {
        setAwaiting(key);
        return "waiting";
      }
      const approval = pluginApprovalOf(error);
      if (approval) {
        setConfirming({
          key,
          update,
          approval,
          kept: approval.added_capabilities ?? [],
        });
        return "waiting";
      }
      throw error;
    }
  }

  /** Shows a plugin again once a change of it was decided elsewhere. */
  const reloadDecided = useCallback(
    async (key: string, approved: boolean) => {
      void loadList();
      const pending = afterApproval.current;
      afterApproval.current = null;
      if (selectedRef.current !== key) return;
      const next = await loadDetail(key);
      if (approved && pending?.key === key) await pending.run(next);
      if (approved && next?.enabled) {
        setTab("overview");
        setLinkPlay((count) => count + 1);
        if (next.hooks.includes("describeStatus")) void loadStatus(key);
      }
    },
    [loadDetail, loadList, loadStatus],
  );

  useEffect(() => {
    selectedRef.current = selectedKey;
  }, [selectedKey]);

  useEffect(() => {
    let stop: (() => void) | undefined;
    let stopped = false;
    void listenPluginApprovalFinished((finished) => {
      setAwaiting((current) => (current === finished.key ? null : current));
      if (finished.error) setActionError(finished.error);
      void reloadDecided(finished.key, finished.approved && !finished.error);
    }).then((unlisten) => {
      if (stopped) unlisten();
      else stop = unlisten;
    });
    return () => {
      stopped = true;
      stop?.();
    };
  }, [reloadDecided]);

  /** Shows the plugin an install put in place. */
  const showInstalled = useCallback(
    async (key: string) => {
      setInstalling(null);
      void loadList();
      if (selectedRef.current !== key) {
        setSelectedKey(key);
        return;
      }
      const next = await loadDetail(key);
      if (next?.state === "loaded") void loadStatus(key);
    },
    [loadDetail, loadList, loadStatus],
  );

  /**
   * Puts back a version an install replaced. It waits for the user as an
   * install does: in the confirmation window, or the web console's dialog.
   */
  async function rollback(
    key: string,
    version: PluginInstalledVersion,
    approved = false,
  ) {
    setBusy(true);
    setActionError(null);
    try {
      show(await rollbackPlugin(key, version.id, approved));
      void loadList();
    } catch (error) {
      if (controlErrorCode(error) === "approval_pending") {
        setAwaiting(key);
        return;
      }
      const approval = pluginApprovalOf(error);
      if (approval?.install) {
        setConfirmingRollback({ key, version, review: approval.install });
        return;
      }
      const failure = pluginInstallFailureOf(error);
      setActionError(
        failure
          ? installFailureText(failure, t)
          : controlErrorMessage(error, t("plugins.install.rollbackFailed")),
      );
    } finally {
      setBusy(false);
    }
  }

  /** Removes a plugin installed from source; a builtin of its key comes back. */
  async function uninstall(key: string) {
    setBusy(true);
    setActionError(null);
    try {
      const next = await uninstallPlugin(key);
      setList(next);
      setSelectedKey((current) => keptSelection(current, next));
      if (next.plugins.some((plugin) => plugin.key === key)) {
        const reloaded = await loadDetail(key);
        if (reloaded?.state === "loaded") void loadStatus(key);
      }
    } catch (error) {
      setActionError(
        controlErrorMessage(error, t("plugins.install.uninstallFailed")),
      );
    } finally {
      setBusy(false);
    }
  }

  /** Applies a change and refreshes the list row that shows its state. */
  async function apply(change: () => Promise<PluginDetail | "waiting">) {
    setBusy(true);
    setActionError(null);
    try {
      const next = await change();
      if (next === "waiting") return false;
      setDetail(next);
      setRevision((current) => current + 1);
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
      const result = await testPlugin(key);
      setTab("overview");
      // The overview shows the test, with what it changed in the counts.
      await loadStatus(key);
      if (result.status === "ok") setLinkPlay((count) => count + 1);
    } catch (error) {
      setActionError(controlErrorMessage(error, t("plugins.testFailed")));
    } finally {
      setTesting(false);
    }
  }

  /**
   * Saves the settings form: the settings first, so the servers' origins
   * exist, then each new credential, to its server's origin.
   */
  async function saveSettings(
    key: string,
    update: PluginSettingsUpdate,
    credentials: CredentialInput[],
  ) {
    setBusy(true);
    setActionError(null);
    const saveCredentials = async (start: PluginDetail | null) => {
      let next = start;
      for (const credential of credentials) {
        const current = next ?? detail;
        const origin =
          credential.origin ??
          (current ? serverOrigin(current, credential.name) : undefined);
        if (!origin) throw new Error(t("plugins.settings.noOrigin"));
        next = await savePluginCredential(key, credential.name, {
          secret: credential.secret,
          origin,
        });
      }
      return next;
    };
    try {
      let next: PluginDetail | null = null;
      if (Object.keys(update).length > 0) {
        const saved = await save(key, update);
        if (saved === "waiting") {
          // The credentials follow the server the change adds, once the
          // user confirmed it.
          afterApproval.current = {
            key,
            run: async (confirmed) => {
              try {
                const after = await saveCredentials(confirmed);
                if (after) show(after);
              } catch (error) {
                setActionError(
                  controlErrorMessage(error, t("plugins.saveFailed")),
                );
              }
            },
          };
          return false;
        }
        next = saved;
      }
      next = await saveCredentials(next);
      if (next) show(next);
      void loadStatus(key);
      return true;
    } catch (error) {
      setActionError(controlErrorMessage(error, t("plugins.saveFailed")));
      return false;
    } finally {
      setBusy(false);
    }
  }

  // The list shows the plugins in the order their extension points run.
  const plugins = orderedPlugins(list?.plugins ?? [], host?.order);
  const selected = plugins.find((plugin) => plugin.key === selectedKey) ?? null;

  return (
    <section
      aria-labelledby="plugin-manager-heading"
      className="@container gutter-frame flex min-h-0 w-full min-w-0 flex-1 flex-col overflow-hidden"
    >
      <PageHeader
        actions={
          <>
            {host ? (
              <PluginHostControls
                disabled={!isReady}
                host={host}
                onChange={(update) => void changeHost(update)}
              />
            ) : null}
            {list || host ? (
              <HelpPopover label={t("plugins.headerHelpLabel")} size="wide">
                <div className="grid gap-3">
                  {host ? (
                    <>
                      <section className="grid gap-1">
                        <h3 className="text-xs font-medium text-muted-foreground">
                          {t("plugins.host.pause")}
                        </h3>
                        <p>{t("plugins.host.pauseHelp")}</p>
                      </section>
                      <section className="grid gap-1">
                        <h3 className="text-xs font-medium text-muted-foreground">
                          {t("plugins.host.budget")}
                        </h3>
                        <p>{t("plugins.host.budgetHelp")}</p>
                      </section>
                    </>
                  ) : null}
                  {list ? (
                    <section className="grid gap-1">
                      <h3 className="text-xs font-medium text-muted-foreground">
                        {t("plugins.directoryHelpLabel")}
                      </h3>
                      <p>{t("plugins.directoryHelp")}</p>
                      <p className="font-mono text-xs break-all">
                        {list.directory}
                      </p>
                      <p>{t("plugins.rescanHelp")}</p>
                    </section>
                  ) : null}
                </div>
              </HelpPopover>
            ) : null}
            <Button
              disabled={!isReady}
              onClick={() => setInstalling({})}
              size="sm"
              type="button"
              variant="outline"
            >
              <Plus />
              {t("plugins.install.open")}
            </Button>
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
      {listError || hostError ? (
        <FormMessage className="mb-3" tone="error">
          {listError ?? hostError}
        </FormMessage>
      ) : null}
      {host?.paused ? (
        <FormMessage className="mb-3" tone="notice">
          {t("plugins.host.pausedNotice")}
        </FormMessage>
      ) : null}
      {!isReady && list !== null ? (
        <FormMessage className="mb-3" tone="notice">
          {t("plugins.stale")}
        </FormMessage>
      ) : null}
      {!isReady && list === null ? (
        <EmptyState
          className="flex-1"
          description={t("plugins.blockedHint")}
          illustration={<Blocks className="size-6" />}
          title={t("plugins.blocked")}
          variant="page"
        />
      ) : list === null && !listError ? (
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
        <SplitWorkspace className="grid-rows-[auto_minmax(360px,1fr)] @[720px]:grid-cols-[minmax(11rem,14rem)_minmax(0,1fr)]">
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
            {plugins.length > 1 && host ? (
              <div className="shrink-0 border-t p-2">
                <Button
                  className="w-full"
                  disabled={!isReady}
                  onClick={() => setOrdering(true)}
                  size="xs"
                  type="button"
                  variant="ghost"
                >
                  {t("plugins.order.open")}
                </Button>
              </div>
            ) : null}
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
                advancedOpen={advancedOpen}
                busy={busy}
                detail={detail}
                onAdvancedOpen={setAdvancedOpen}
                onClearData={(what) => clearData(detail.key, what)}
                onCredentialClear={(name) =>
                  apply(() => deletePluginCredential(detail.key, name))
                }
                onSaveSettings={(update, credentials) =>
                  saveSettings(detail.key, update, credentials)
                }
                onDisable={() =>
                  void apply(() => save(detail.key, { enabled: false }))
                }
                onEnable={() => {
                  if (isWebEdition) {
                    // The web console has no confirmation window: its own
                    // dialog confirms what enabling grants.
                    setGrants(detail.requested_capabilities);
                    setEnabling(detail);
                    return;
                  }
                  // The confirmation window lists the capabilities, each
                  // one the user may untick.
                  void apply(() =>
                    save(detail.key, {
                      enabled: true,
                      granted_capabilities: detail.requested_capabilities,
                    }),
                  ).then((enabled) => {
                    if (!enabled) return;
                    setTab("overview");
                    setLinkPlay((count) => count + 1);
                  });
                }}
                awaiting={awaiting === detail.key}
                onOpenApproval={() => void openPluginApproval()}
                onUpdate={(link) => setInstalling({ link })}
                onRollback={(version) => void rollback(detail.key, version)}
                onUninstall={() => setUninstalling(detail)}
                onRefreshStatus={() => void loadStatus(detail.key)}
                onTab={setTab}
                onTest={() => void runTest(detail.key)}
                linkPlay={linkPlay}
                revision={revision}
                stars={stars}
                stats={stats}
                status={status}
                statusError={statusError}
                tab={tab}
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
              save(
                target.key,
                { enabled: true, granted_capabilities: grants },
                true,
              ),
            ).then((enabled) => {
              if (!enabled) return;
              // Show the plugin joining AstrLink.
              setTab("overview");
              setLinkPlay((count) => count + 1);
              if (target.hooks.includes("describeStatus"))
                void loadStatus(target.key);
            });
        }}
        open={enabling !== null}
        title={t("plugins.enableDialog.title", { name: enabling?.name ?? "" })}
      />
      {installing ? (
        <PluginInstallDialog
          host={host}
          link={installing.link}
          onClose={() => setInstalling(null)}
          onHostChange={(update) => void changeHost(update)}
          onInstalled={(key) => void showInstalled(key)}
        />
      ) : null}
      <ConfirmDialog
        confirmDisabled={busy}
        confirmLabel={t("plugins.install.rollbackTo")}
        description={
          confirmingRollback ? (
            <PluginInstallReviewView
              review={confirmingRollback.review}
              rollback
            />
          ) : null
        }
        onCancel={() => setConfirmingRollback(null)}
        onConfirm={() => {
          const target = confirmingRollback;
          setConfirmingRollback(null);
          if (target) void rollback(target.key, target.version, true);
        }}
        open={confirmingRollback !== null}
        title={t("plugins.install.rollbackTitle", {
          name: confirmingRollback?.review.name ?? "",
        })}
      />
      <ConfirmDialog
        confirmDisabled={busy}
        confirmLabel={t("plugins.install.uninstall")}
        description={
          uninstalling ? (
            <p>
              {uninstalling.source === "override"
                ? t("plugins.install.uninstallBuiltin")
                : t("plugins.install.uninstallDescription")}
            </p>
          ) : null
        }
        destructive
        onCancel={() => setUninstalling(null)}
        onConfirm={() => {
          const target = uninstalling;
          setUninstalling(null);
          if (target) void uninstall(target.key);
        }}
        open={uninstalling !== null}
        title={t("plugins.install.uninstallTitle", {
          name: uninstalling?.name ?? "",
        })}
      />
      {ordering ? (
        <PluginOrderDialog
          onCancel={() => setOrdering(false)}
          onSave={(order) => {
            setSavingOrder(true);
            void updatePluginHostSettings({ order })
              .then((next) => {
                setHost(next);
                setOrdering(false);
              })
              .catch((error) =>
                setHostError(
                  controlErrorMessage(error, t("plugins.host.saveFailed")),
                ),
              )
              .finally(() => setSavingOrder(false));
          }}
          order={host?.order}
          plugins={plugins}
          saving={savingOrder}
        />
      ) : null}
      <ConfirmDialog
        confirmDisabled={busy}
        confirmLabel={t("plugins.approval.confirm")}
        description={
          confirming ? (
            <PluginApprovalSummary
              approval={confirming.approval}
              detail={detail?.key === confirming.key ? detail : null}
              kept={confirming.kept}
              onKept={(kept) =>
                setConfirming((current) =>
                  current ? { ...current, kept } : current,
                )
              }
            />
          ) : null
        }
        onCancel={() => {
          setConfirming(null);
          afterApproval.current = null;
        }}
        onConfirm={() => {
          const target = confirming;
          setConfirming(null);
          if (!target) return;
          const added = target.approval.added_capabilities ?? [];
          const update = target.update.granted_capabilities
            ? {
                ...target.update,
                granted_capabilities: target.update.granted_capabilities.filter(
                  (grant) =>
                    !added.includes(grant) || target.kept.includes(grant),
                ),
              }
            : target.update;
          void apply(() => save(target.key, update, true)).then((saved) =>
            reloadDecided(target.key, saved),
          );
        }}
        open={confirming !== null}
        title={
          confirming?.approval.enable
            ? t("plugins.approval.enableTitle", {
                name: confirming.approval.name,
              })
            : t("plugins.approval.grantTitle", {
                name: confirming?.approval.name ?? "",
              })
        }
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
        <PluginIcon icon={plugin.icon} />
        <strong className="truncate text-sm font-semibold">
          {plugin.name}
        </strong>
        <span className="shrink-0 text-micro text-muted-foreground tabular-nums">
          {plugin.version}
        </span>
      </span>
      {/* Under the name, past the icon. */}
      <span className="flex min-w-0 flex-wrap items-center gap-1.5 pl-7">
        <StatusBadge tone={pluginTone(plugin)}>
          {pluginState(plugin, t)}
        </StatusBadge>
      </span>
      {plugin.error ? (
        <span
          className="truncate pl-7 text-micro text-danger-foreground"
          title={plugin.error.message}
        >
          {loadErrorText(plugin.error, t)}
        </span>
      ) : null}
    </Button>
  );
}

/**
 * The settings of every plugin at once, in the page's header: the pause of
 * them all and what they may inject into one request together.
 */
function PluginHostControls({
  host,
  disabled,
  onChange,
}: {
  host: PluginHostSettings;
  disabled: boolean;
  onChange: (update: PluginHostSettingsUpdate) => void;
}) {
  const t = useT();
  const budgetKib = Math.round(host.injection_budget_bytes / 1024);
  const budgets = INJECTION_BUDGETS_KIB.includes(budgetKib)
    ? INJECTION_BUDGETS_KIB
    : [...INJECTION_BUDGETS_KIB, budgetKib].sort((a, b) => a - b);
  return (
    <div className="flex items-center gap-3" data-testid="plugin-host-controls">
      <div className="flex items-center gap-1.5">
        <Switch
          aria-label={t("plugins.host.pause")}
          checked={host.paused}
          disabled={disabled}
          id="plugin-host-pause"
          onCheckedChange={(paused) => onChange({ paused })}
          size="sm"
        />
        <Label className="text-xs font-normal" htmlFor="plugin-host-pause">
          {t("plugins.host.pause")}
        </Label>
      </div>
      <div className="flex items-center gap-1.5">
        <Label className="text-xs font-normal" htmlFor="plugin-host-budget">
          {t("plugins.host.budget")}
        </Label>
        <Select
          disabled={disabled}
          onValueChange={(value) =>
            onChange({ injection_budget_bytes: Number(value) * 1024 })
          }
          value={String(budgetKib)}
        >
          <SelectTrigger className="w-24" id="plugin-host-budget" size="sm">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {budgets.map((kib) => (
              <SelectItem key={kib} value={String(kib)}>
                {t("plugins.host.budgetValue", { value: kib })}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
    </div>
  );
}

function PluginDetailPane({
  detail,
  stats,
  stars,
  linkPlay,
  status,
  statusError,
  testing,
  busy,
  actionError,
  tab,
  revision,
  advancedOpen,
  onTab,
  onEnable,
  onDisable,
  onTest,
  onRefreshStatus,
  onAdvancedOpen,
  onSaveSettings,
  onCredentialClear,
  onClearData,
  awaiting,
  onOpenApproval,
  onUpdate,
  onRollback,
  onUninstall,
}: {
  detail: PluginDetail;
  stats: PluginStats | null;
  stars: PluginCreditStars | null;
  linkPlay: number;
  status: PluginStatus | null;
  statusError: string | null;
  testing: boolean;
  busy: boolean;
  actionError: string | null;
  tab: DetailTab;
  revision: number;
  advancedOpen: boolean;
  onTab: (tab: DetailTab) => void;
  onEnable: () => void;
  onDisable: () => void;
  onTest: () => void;
  onRefreshStatus: () => void;
  onAdvancedOpen: (open: boolean) => void;
  onSaveSettings: (
    update: PluginSettingsUpdate,
    credentials: CredentialInput[],
  ) => Promise<boolean>;
  onCredentialClear: (name: string) => Promise<boolean>;
  onClearData: (what: PluginClearTarget) => Promise<boolean>;
  awaiting: boolean;
  onOpenApproval: () => void;
  onUpdate: (link: string) => void;
  onRollback: (version: PluginInstalledVersion) => void;
  onUninstall: () => void;
}) {
  const t = useT();
  const loaded = detail.state === "loaded";
  const tabs: DetailTab[] = loaded
    ? ["overview", "settings", "status", "develop"]
    : [];
  const current = tabs.includes(tab) ? tab : "overview";
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
          <PluginIcon icon={detail.icon} />
          <h2 className="truncate text-sm font-semibold">{detail.name}</h2>
          <StatusBadge tone={pluginTone(detail)}>
            {pluginState(detail, t)}
          </StatusBadge>
          {loaded ? (
            <HelpPopover label={t("plugins.about.label")} size="wide">
              <AboutPlugin detail={detail} />
            </HelpPopover>
          ) : null}
        </div>
        <p className="truncate pl-7 text-micro text-muted-foreground">
          {[
            detail.development
              ? t("plugins.develop.build")
              : detail.installed
                ? t("plugins.source.github")
                : t(`plugins.source.${detail.source}`),
            detail.version,
          ]
            .filter(Boolean)
            .join(" · ")}
        </p>
      </PanelHeader>
      {actionError ? (
        <FormMessage className="m-3 mb-0" tone="error">
          {actionError}
        </FormMessage>
      ) : null}
      {awaiting ? (
        <FormMessage
          className="m-3 mb-0 flex flex-wrap items-center gap-2"
          data-testid="plugin-awaiting-approval"
          tone="notice"
        >
          <span className="flex-1">{t("plugins.approval.waiting")}</span>
          <Button
            onClick={onOpenApproval}
            size="xs"
            type="button"
            variant="outline"
          >
            {t("plugins.approval.open")}
          </Button>
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
          value={current}
        >
          <div className="mx-3 mt-2 flex shrink-0 items-center gap-2">
            <TabsList aria-label={t("plugins.tabsLabel")} scrollable>
              {tabs.map((item) => (
                <TabsTrigger key={item} value={item}>
                  {t(`plugins.tabs.${item}`)}
                </TabsTrigger>
              ))}
            </TabsList>
            <span className="flex-1" />
            {current !== "settings" && current !== "develop" ? (
              <IconButton
                label={t("common.refresh")}
                onClick={onRefreshStatus}
                size="icon-sm"
                type="button"
                variant="ghost"
              >
                <RefreshCw />
              </IconButton>
            ) : null}
          </div>
          <TabsContent
            className="min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain p-3"
            data-tab-scroller
            value="overview"
          >
            {detail.installed ? (
              <div className="mb-3">
                <PluginSourceCard
                  busy={busy}
                  detail={detail}
                  onRollback={onRollback}
                  onUninstall={onUninstall}
                  onUpdate={onUpdate}
                  source={detail.installed}
                />
              </div>
            ) : null}
            <PluginOverview
              busy={busy}
              detail={detail}
              linkPlay={linkPlay}
              onClear={onClearData}
              stars={stars}
              stats={stats}
              status={status}
              statusError={statusError}
            />
          </TabsContent>
          <TabsContent
            className="flex min-h-0 min-w-0 flex-1 flex-col"
            value="settings"
          >
            <PluginSettingsForm
              advancedOpen={advancedOpen}
              busy={busy}
              detail={detail}
              key={`${detail.key}:${revision}`}
              onAdvancedOpen={onAdvancedOpen}
              onClearCredential={onCredentialClear}
              onSave={onSaveSettings}
            />
          </TabsContent>
          <TabsContent
            className="min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain p-3"
            data-tab-scroller
            value="status"
          >
            <StatusTab
              detail={detail}
              stats={stats}
              status={status}
              statusError={statusError}
            />
          </TabsContent>
          <TabsContent
            className="min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain p-3"
            data-tab-scroller
            value="develop"
          >
            {current === "develop" ? <PluginDevelop detail={detail} /> : null}
          </TabsContent>
        </Tabs>
      )}
    </>
  );
}

/** What the plugin does and the capabilities it uses, behind the ? by its name. */
function AboutPlugin({ detail }: { detail: PluginDetail }) {
  const t = useT();
  return (
    <div className="grid gap-3" data-testid="plugin-about">
      {detail.notice ? (
        <section className="grid gap-1">
          <h3 className="text-xs font-medium text-muted-foreground">
            {t("plugins.about.does")}
          </h3>
          <p className="leading-6 whitespace-pre-wrap">{detail.notice}</p>
        </section>
      ) : null}
      {detail.network_purpose ? (
        <section className="grid gap-1">
          <h3 className="text-xs font-medium text-muted-foreground">
            {t("plugins.about.network")}
          </h3>
          <p>{detail.network_purpose}</p>
        </section>
      ) : null}
      <section className="grid gap-1.5">
        <h3 className="text-xs font-medium text-muted-foreground">
          {t("plugins.about.capabilities")}
        </h3>
        {detail.requested_capabilities.length === 0 ? (
          <p className="text-muted-foreground">{t("plugins.noCapabilities")}</p>
        ) : (
          <div className="flex flex-wrap gap-1.5">
            {detail.requested_capabilities.map((capability) => (
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
            ))}
          </div>
        )}
      </section>
    </div>
  );
}

function StatusTab({
  detail,
  stats,
  status,
  statusError,
}: {
  detail: PluginDetail;
  stats: PluginStats | null;
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
      {stats ? <DailyEffects detail={detail} stats={stats} /> : null}
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
