import { useId, useState } from "react";
import { isTauri } from "@tauri-apps/api/core";
import { Panel, PanelBody, PanelHeader } from "@/components/Panel";
import { DataField, DataRow } from "@/components/DataRow";
import { Badge } from "@/components/ui/badge";
import { StatusBadge } from "@/components/StatusBadge";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { Label } from "@/components/ui/label";
import { Progress } from "@/components/ui/progress";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  ArrowDown,
  ArrowUpRight,
  ChevronDown,
  RefreshCw,
  SlidersHorizontal,
} from "@/components/icons";
import { FormMessage } from "@/components/FormMessage";
import { MarkdownContent } from "@/components/MarkdownContent";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { ScrollWorkspace } from "@/components/ScrollWorkspace";
import { cn } from "@/lib/utils";
import { PageHeader } from "./PageHeader";
import { LocalClientUpdates } from "./LocalClientUpdates";
import { openExternalURL } from "./bridge";
import {
  checkAppUpdate,
  downloadAppUpdate,
  installAppUpdate,
  saveUpdatePreferences,
} from "./update-bridge";
import {
  updateBusy,
  type UpdatePreferences,
  type UpdateSnapshot,
} from "./update-model";
import { useT } from "./i18n";
import logo from "./assets/astrlink-logo.svg";

export function About({
  snapshot,
  onSnapshot,
  loadError,
  hasUnsavedChanges,
}: {
  snapshot: UpdateSnapshot;
  onSnapshot: (snapshot: UpdateSnapshot) => void;
  loadError: string | null;
  hasUnsavedChanges: () => boolean;
}) {
  const t = useT();
  const id = useId();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [confirm, setConfirm] = useState(false);
  const [preferencesOpen, setPreferencesOpen] = useState<boolean | null>(null);
  const native = isTauri();
  const working = busy || updateBusy(snapshot);
  const expanded = preferencesOpen ?? false;
  const preferencesDisabled =
    !native || busy || snapshot.phase === "installing";

  async function run(action: () => Promise<UpdateSnapshot>) {
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      onSnapshot(await action());
    } catch (error) {
      setError(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  }
  function save(patch: Partial<UpdatePreferences>) {
    void run(() =>
      saveUpdatePreferences({ ...snapshot.preferences, ...patch }),
    );
  }
  async function open(url: string) {
    try {
      await openExternalURL(url);
    } catch (error) {
      setError(String(error));
    }
  }

  const progress = snapshot.total_bytes
    ? Math.min(100, (snapshot.downloaded_bytes / snapshot.total_bytes) * 100)
    : null;
  const canInstall =
    native &&
    !snapshot.development &&
    snapshot.install_supported &&
    snapshot.configured;
  const hasDownload =
    canInstall &&
    snapshot.release &&
    ["available", "error"].includes(snapshot.phase);
  const ready = canInstall && snapshot.phase === "ready";
  const successful =
    snapshot.phase === "up_to_date" || snapshot.phase === "ready";
  const platform =
    (
      {
        macos: "macOS",
        windows: "Windows",
        linux: "Linux",
        browser: t("about.browser"),
      } as Record<string, string>
    )[snapshot.platform] ?? snapshot.platform;
  const arch =
    snapshot.arch === "aarch64"
      ? snapshot.platform === "macos"
        ? "Apple Silicon"
        : "ARM64"
      : snapshot.arch === "x86_64"
        ? "x64"
        : snapshot.arch;
  const device =
    snapshot.platform === "browser" || arch === "—"
      ? platform
      : `${platform} · ${arch}`;
  const notice = !native
    ? t("about.browserPreview")
    : snapshot.development
      ? t("about.development")
      : !snapshot.install_supported
        ? t("about.manualInstall")
        : !snapshot.configured
          ? t("about.unconfigured")
          : null;
  const checkButton = (
    <Button
      variant={snapshot.release ? "ghost" : "default"}
      size={snapshot.release ? "icon" : "default"}
      aria-label={t("about.check")}
      title={t("about.check")}
      disabled={!native || working}
      onClick={() => void run(checkAppUpdate)}
    >
      <RefreshCw
        aria-hidden="true"
        className={cn(
          snapshot.phase === "checking" && "motion-safe:animate-spin",
        )}
      />
      {snapshot.release ? null : t("about.check")}
    </Button>
  );

  return (
    <>
      <ScrollWorkspace
        className="gap-0"
        contentSlot="about-workspace"
        data-slot="about-page"
        header={<PageHeader title={t("about.title")} />}
      >
        {/* Center the column inside the full-width scrollport, so its scrollbar
            stays at the workspace edge like every other page. */}
        <div className="mx-auto flex w-full max-w-5xl flex-col gap-3">
          <Panel
            className={cn(
              "flex flex-col",
              snapshot.release ? "min-h-64 flex-1" : "shrink-0",
            )}
            data-slot="about-update-panel"
          >
            <PanelHeader
              className="shrink-0 flex-wrap items-center gap-3 px-5 py-4"
              actions={
                <>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() =>
                      void open("https://github.com/Calcium-Ion/AstrLink")
                    }
                  >
                    {t("about.project")}
                    <ArrowUpRight aria-hidden="true" />
                  </Button>
                  {!snapshot.release ? checkButton : null}
                </>
              }
            >
              <div
                className="flex items-center gap-4"
                aria-label={t("about.application")}
              >
                <img src={logo} alt="" className="size-14 shrink-0" />
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <h2 className="text-xl font-semibold tracking-tight">
                      AstrLink
                    </h2>
                    {snapshot.development ? (
                      <Badge variant="secondary">
                        {t("about.developmentBadge")}
                      </Badge>
                    ) : null}
                  </div>
                  <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
                    <span>{device}</span>
                  </div>
                </div>
              </div>
            </PanelHeader>
            <PanelBody
              className={cn(
                "space-y-3 px-5 pt-3",
                snapshot.release ? "pb-4" : "pb-0",
                !snapshot.release && "flex-none",
              )}
              data-slot="about-update-content"
            >
              <div
                className="grid grid-cols-2 gap-x-6 gap-y-4 @min-[640px]/workspace-surface:grid-cols-3"
                aria-live="polite"
              >
                <DataField
                  label={t("about.currentVersion")}
                  value={
                    <span className="font-mono text-xl font-medium">
                      {snapshot.current_version}
                    </span>
                  }
                />
                <DataField
                  label={t("about.latestVersion")}
                  value={
                    <span className="font-mono text-xl font-medium">
                      {snapshot.latest_version ??
                        snapshot.release?.version ??
                        "—"}
                    </span>
                  }
                />
                <DataField
                  className="col-span-2 @min-[640px]/workspace-surface:col-span-1"
                  label={t("about.updateStatus")}
                  value={
                    <StatusBadge
                      tone={
                        snapshot.phase === "error"
                          ? "negative"
                          : successful
                            ? "positive"
                            : working || snapshot.release
                              ? "pending"
                              : "neutral"
                      }
                    >
                      {t(`about.phase.${snapshot.phase}`)}
                    </StatusBadge>
                  }
                />
              </div>
              {loadError || error ? (
                <FormMessage tone="error">{loadError || error}</FormMessage>
              ) : null}
              {snapshot.error_code ? (
                <FormMessage tone="error">
                  <p>{t(`about.errors.${snapshot.error_code}`)}</p>
                  {snapshot.error_detail ? (
                    <details className="mt-1 text-xs">
                      <summary className="cursor-pointer">
                        {t("about.errorDetails")}
                      </summary>
                      <p className="mt-2 break-words">
                        {snapshot.error_detail}
                      </p>
                    </details>
                  ) : null}
                </FormMessage>
              ) : null}
              {snapshot.phase === "downloading" ? (
                <div className="space-y-2">
                  <Progress
                    value={progress}
                    aria-label={t("about.phase.downloading")}
                  />
                  <p className="font-mono text-xs text-muted-foreground">
                    {(snapshot.downloaded_bytes / 1024 / 1024).toFixed(1)} MB
                    {snapshot.total_bytes
                      ? ` / ${(snapshot.total_bytes / 1024 / 1024).toFixed(1)} MB`
                      : ""}
                  </p>
                </div>
              ) : null}
              {snapshot.release ? (
                <div className="flex flex-wrap items-center gap-2">
                  {ready ? (
                    <Button disabled={working} onClick={() => setConfirm(true)}>
                      {t("about.install")}
                    </Button>
                  ) : null}
                  {hasDownload ? (
                    <Button
                      disabled={working}
                      onClick={() =>
                        void run(
                          [
                            "network",
                            "manifest",
                            "missing_artifact",
                            "rate_limit",
                          ].includes(snapshot.error_code ?? "")
                            ? checkAppUpdate
                            : downloadAppUpdate,
                        )
                      }
                    >
                      <ArrowDown aria-hidden="true" />
                      {t(
                        snapshot.phase === "error"
                          ? "about.retry"
                          : "about.download",
                      )}
                    </Button>
                  ) : null}
                  {snapshot.release ? (
                    <Button
                      variant={canInstall ? "ghost" : "default"}
                      onClick={() => void open(snapshot.release!.url)}
                    >
                      {t("about.releasePage")}
                      <ArrowUpRight aria-hidden="true" />
                    </Button>
                  ) : null}
                  {checkButton}
                </div>
              ) : null}
              <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 text-xs leading-relaxed text-muted-foreground">
                {notice ? <p>{notice}</p> : null}
                {snapshot.last_checked_at ? (
                  <p>
                    {t("about.lastChecked", {
                      time: new Date(snapshot.last_checked_at).toLocaleString(
                        undefined,
                        {
                          month: "short",
                          day: "numeric",
                          hour: "2-digit",
                          minute: "2-digit",
                        },
                      ),
                    })}
                  </p>
                ) : null}
              </div>
              <div className="-mx-5 border-t" data-slot="about-preferences">
                <Button
                  variant="ghost"
                  className="h-auto w-full justify-start gap-2 rounded-none px-5 py-2"
                  aria-label={t("about.preferences")}
                  aria-expanded={expanded}
                  aria-controls={`${id}-preferences`}
                  onClick={() => setPreferencesOpen(!expanded)}
                >
                  <SlidersHorizontal
                    aria-hidden="true"
                    className="text-muted-foreground"
                  />
                  <span className="text-sm font-medium">
                    {t("about.preferences")}
                  </span>
                  {!expanded ? (
                    <span className="ml-auto text-xs font-normal text-muted-foreground">
                      {t(`about.${snapshot.preferences.channel}`)}
                    </span>
                  ) : null}
                  <ChevronDown
                    aria-hidden="true"
                    className={cn(
                      "transition-transform",
                      expanded && "ml-auto rotate-180",
                    )}
                  />
                </Button>
                {expanded ? (
                  <div id={`${id}-preferences`} className="border-t">
                    <DataRow>
                      <div className="min-w-0 flex-1">
                        <Label
                          htmlFor={`${id}-channel`}
                          className="text-sm font-normal"
                        >
                          {t("about.channel")}
                        </Label>
                        <p className="mt-1 text-xs text-muted-foreground">
                          {t(
                            `about.channelHint.${snapshot.preferences.channel}`,
                          )}
                        </p>
                      </div>
                      <Select
                        value={snapshot.preferences.channel}
                        disabled={preferencesDisabled}
                        onValueChange={(channel) =>
                          save({
                            channel: channel as UpdatePreferences["channel"],
                          })
                        }
                      >
                        <SelectTrigger
                          id={`${id}-channel`}
                          aria-label={t("about.channel")}
                          className="w-28 shrink-0"
                        >
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="stable">
                            {t("about.stable")}
                          </SelectItem>
                          <SelectItem value="preview">
                            {t("about.preview")}
                          </SelectItem>
                        </SelectContent>
                      </Select>
                    </DataRow>
                    {(["auto_check", "auto_download"] as const).map((key) => (
                      <DataRow key={key}>
                        <div className="min-w-0 flex-1">
                          <Label
                            htmlFor={`${id}-${key}`}
                            className="text-sm font-normal"
                          >
                            {t(`about.${key}`)}
                          </Label>
                          <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
                            {t(`about.preferenceHint.${key}`)}
                          </p>
                        </div>
                        <Switch
                          id={`${id}-${key}`}
                          checked={snapshot.preferences[key]}
                          disabled={preferencesDisabled}
                          onCheckedChange={(value) => save({ [key]: value })}
                        />
                      </DataRow>
                    ))}
                  </div>
                ) : null}
              </div>{" "}
              {snapshot.release ? (
                <div className="space-y-3 border-t pt-4">
                  <h4 className="text-xs font-medium text-muted-foreground">
                    {t("about.releaseNotes")}
                  </h4>
                  <MarkdownContent
                    content={snapshot.release.notes || t("about.noNotes")}
                  />
                </div>
              ) : null}
            </PanelBody>
          </Panel>
          <LocalClientUpdates compact={Boolean(snapshot.release)} />
        </div>
      </ScrollWorkspace>
      <ConfirmDialog
        open={confirm}
        title={t("about.install")}
        confirmLabel={t("about.install")}
        confirmDisabled={hasUnsavedChanges()}
        description={
          <>
            <p>{t("about.installHint")}</p>
            {hasUnsavedChanges() ? <p>{t("about.unsaved")}</p> : null}
          </>
        }
        onCancel={() => setConfirm(false)}
        onConfirm={() => {
          if (hasUnsavedChanges()) return;
          setConfirm(false);
          void run(installAppUpdate);
        }}
      />
    </>
  );
}
