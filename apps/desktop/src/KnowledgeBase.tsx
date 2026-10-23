import { useCallback, useEffect, useState } from "react";

import {
  Check,
  Flask,
  LoaderCircle,
  ScanText,
  Server,
  X,
} from "@/components/icons";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { DataField } from "@/components/DataRow";
import { EmptyState } from "@/components/EmptyState";
import { FormMessage } from "@/components/FormMessage";
import { HelpPopover } from "@/components/HelpPopover";
import { LoadingState } from "@/components/LoadingState";
import { Panel, PanelHeader } from "@/components/Panel";
import { StatusBadge } from "@/components/StatusBadge";
import type { StatusTone } from "@/components/StatusDot";
import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";

import {
  checkKnowledgeModel,
  checkKnowledgeRuntime,
  getKnowledgeEnvironment,
  pickKnowledgePath,
  runKnowledgeAction,
  type KnowledgeEnvironmentAction,
} from "./bridge";
import { controlErrorMessage } from "./control-error";
import { isWebEdition } from "./edition";
import { useT } from "./i18n";
import {
  knowledgeEnvironmentBusy,
  progressFraction,
  type KnowledgeEmbeddingCheck,
  type KnowledgeEmbeddingModel,
  type KnowledgeEnvironment,
  type KnowledgeModelState,
  type KnowledgeProgress,
  type KnowledgeRuntimeCheck,
  type KnowledgeRuntimeState,
} from "./knowledge-model";
import { PageHeader } from "./PageHeader";

type Translate = ReturnType<typeof useT>;

/** How often the page refreshes while a download or start is under way. */
const BUSY_POLL_MS = 1000;

type Confirmation =
  | { kind: "reset_runtime_data" }
  | { kind: "remove_runtime" }
  | { kind: "remove_model"; id: string };

const runtimeTones: Record<KnowledgeRuntimeState, StatusTone> = {
  unsupported: "blocked",
  not_installed: "neutral",
  installing: "pending",
  installed: "neutral",
  starting: "pending",
  running: "positive",
  failed: "negative",
};

const modelTones: Record<KnowledgeModelState, StatusTone> = {
  not_installed: "neutral",
  installing: "pending",
  installed: "positive",
  failed: "negative",
};

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const unit = Math.min(
    Math.floor(Math.log(bytes) / Math.log(1024)),
    units.length - 1,
  );
  const value = bytes / 1024 ** unit;
  return `${value >= 10 || unit === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[unit]}`;
}

function ProgressRow({ progress }: { progress?: KnowledgeProgress }) {
  const fraction = progressFraction(progress);
  return (
    <div className="grid gap-1.5">
      <Progress value={fraction === undefined ? undefined : fraction * 100} />
      {progress ? (
        <span className="text-xs text-muted-foreground tabular-nums">
          {progress.total > 0
            ? `${formatBytes(progress.done)} / ${formatBytes(progress.total)}`
            : formatBytes(progress.done)}
        </span>
      ) : null}
    </div>
  );
}

export function KnowledgeBase({
  coreSessionKey,
  isReady,
}: {
  coreSessionKey: string | null;
  isReady: boolean;
}) {
  const t = useT();
  const [environment, setEnvironment] = useState<KnowledgeEnvironment | null>(
    null,
  );
  const [loadError, setLoadError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [pending, setPending] = useState<string | null>(null);
  const [runtimeCheck, setRuntimeCheck] =
    useState<KnowledgeRuntimeCheck | null>(null);
  const [modelChecks, setModelChecks] = useState<
    Record<string, KnowledgeEmbeddingCheck>
  >({});
  const [confirmation, setConfirmation] = useState<Confirmation | null>(null);

  const load = useCallback(async () => {
    try {
      setEnvironment(await getKnowledgeEnvironment());
      setLoadError(null);
    } catch (error) {
      setLoadError(
        t("knowledge.errors.load", {
          message: controlErrorMessage(error, t("knowledge.errors.unknown")),
        }),
      );
    }
  }, [t]);

  useEffect(() => {
    if (!isReady) return;
    void load();
  }, [coreSessionKey, isReady, load]);

  const busy = environment !== null && knowledgeEnvironmentBusy(environment);
  useEffect(() => {
    if (!isReady || !busy) return;
    const timer = setInterval(() => void load(), BUSY_POLL_MS);
    return () => clearInterval(timer);
  }, [busy, isReady, load]);

  async function act(
    key: string,
    run: () => Promise<KnowledgeEnvironment | void>,
  ) {
    setPending(key);
    setActionError(null);
    try {
      const next = await run();
      if (next) setEnvironment(next);
    } catch (error) {
      setActionError(
        t("knowledge.errors.action", {
          message: controlErrorMessage(error, t("knowledge.errors.unknown")),
        }),
      );
      await load();
    } finally {
      setPending(null);
    }
  }

  function environmentAction(
    action: KnowledgeEnvironmentAction,
    options: { id?: string; path?: string } = {},
  ) {
    const key = options.id ? `${action}:${options.id}` : action;
    return act(key, () => runKnowledgeAction(action, options));
  }

  async function importFrom(kind: "runtime" | "model", id?: string) {
    const path = await pickKnowledgePath(kind);
    if (!path) return;
    await environmentAction(
      kind === "runtime" ? "import_runtime" : "import_model",
      {
        id,
        path,
      },
    );
  }

  async function runRuntimeCheck() {
    setRuntimeCheck(null);
    await act("check_runtime", async () => {
      setRuntimeCheck(await checkKnowledgeRuntime());
    });
  }

  async function runModelCheck(id: string) {
    setModelChecks(({ [id]: _, ...rest }) => rest);
    await act(`check_model:${id}`, async () => {
      const check = await checkKnowledgeModel(id);
      setModelChecks((current) => ({ ...current, [id]: check }));
      await load();
    });
  }

  async function confirmAction() {
    const target = confirmation;
    setConfirmation(null);
    if (!target) return;
    if (target.kind === "remove_model") {
      setModelChecks(({ [target.id]: _, ...rest }) => rest);
      await environmentAction("remove_model", { id: target.id });
      return;
    }
    setRuntimeCheck(null);
    await environmentAction(target.kind);
  }

  return (
    <section
      aria-labelledby="knowledge-heading"
      className="@container gutter-frame flex min-h-0 w-full min-w-0 flex-1 flex-col overflow-hidden"
    >
      <PageHeader
        actions={
          <HelpPopover label={t("knowledge.helpLabel")} size="wide">
            <p>{t("knowledge.help")}</p>
          </HelpPopover>
        }
        title={t("knowledge.title")}
        titleId="knowledge-heading"
        variant="compact"
      />
      {loadError ? (
        <FormMessage className="mb-3" tone="error">
          {loadError}
        </FormMessage>
      ) : null}
      {actionError ? (
        <FormMessage className="mb-3" tone="error">
          {actionError}
        </FormMessage>
      ) : null}
      {!isReady && environment === null ? (
        <EmptyState
          className="flex-1"
          description={t("knowledge.blockedHint")}
          illustration={<ScanText className="size-6" />}
          title={t("knowledge.blocked")}
          variant="page"
        />
      ) : environment === null ? (
        loadError ? null : (
          <LoadingState className="flex-1" label={t("knowledge.loading")} />
        )
      ) : (
        <div className="grid min-h-0 flex-1 content-start gap-4 overflow-y-auto overscroll-contain pb-2">
          <h2 className="text-sm font-medium text-muted-foreground">
            {t("knowledge.environment")}
          </h2>
          <RuntimePanel
            check={runtimeCheck}
            disabled={!isReady}
            environment={environment}
            onAction={(action) => void environmentAction(action)}
            onCheck={() => void runRuntimeCheck()}
            onConfirm={setConfirmation}
            onImport={() => void importFrom("runtime")}
            pending={pending}
            t={t}
          />
          <ModelsPanel
            checks={modelChecks}
            disabled={!isReady}
            models={environment.embedding_models}
            onCheck={(id) => void runModelCheck(id)}
            onConfirm={setConfirmation}
            onImport={(id) => void importFrom("model", id)}
            onInstall={(id) => void environmentAction("install_model", { id })}
            pending={pending}
            t={t}
          />
        </div>
      )}
      <ConfirmDialog
        confirmLabel={t("knowledge.confirm")}
        description={
          confirmation?.kind === "reset_runtime_data"
            ? t("knowledge.runtime.confirmResetBody")
            : confirmation?.kind === "remove_runtime"
              ? t("knowledge.runtime.confirmRemoveBody")
              : t("knowledge.models.confirmRemoveBody")
        }
        destructive
        onCancel={() => setConfirmation(null)}
        onConfirm={() => void confirmAction()}
        open={confirmation !== null}
        title={
          confirmation?.kind === "reset_runtime_data"
            ? t("knowledge.runtime.confirmResetTitle")
            : confirmation?.kind === "remove_runtime"
              ? t("knowledge.runtime.confirmRemoveTitle")
              : t("knowledge.models.confirmRemoveTitle")
        }
      />
    </section>
  );
}

function RuntimePanel({
  check,
  disabled,
  environment,
  onAction,
  onCheck,
  onConfirm,
  onImport,
  pending,
  t,
}: {
  check: KnowledgeRuntimeCheck | null;
  disabled: boolean;
  environment: KnowledgeEnvironment;
  onAction: (action: KnowledgeEnvironmentAction) => void;
  onCheck: () => void;
  onConfirm: (confirmation: Confirmation) => void;
  onImport: () => void;
  pending: string | null;
  t: Translate;
}) {
  const runtime = environment.runtime;
  const state = runtime.state;
  const locked = disabled || pending !== null;
  const installed =
    state === "installed" || state === "running" || state === "failed";
  return (
    <Panel>
      <PanelHeader
        actions={
          <>
            <StatusBadge tone={runtimeTones[state]}>
              {t(`knowledge.runtime.state.${state}`)}
            </StatusBadge>
            <HelpPopover label={t("knowledge.runtime.helpLabel")}>
              <p>{t("knowledge.runtime.help")}</p>
            </HelpPopover>
          </>
        }
        icon={<Server className="size-4" />}
      >
        <h3 className="text-sm font-medium">{t("knowledge.runtime.title")}</h3>
        <p className="text-xs text-muted-foreground">
          {t("knowledge.runtime.version", { version: runtime.version })}
        </p>
      </PanelHeader>
      <div className="grid gap-4 p-4">
        {state === "unsupported" ? (
          <FormMessage tone="notice">
            {t(`knowledge.runtime.reason.${runtime.reason ?? "platform"}`)}
          </FormMessage>
        ) : null}
        {state === "installing" ? (
          <ProgressRow progress={runtime.progress} />
        ) : null}
        {runtime.error ? (
          <FormMessage tone="error">{runtime.error}</FormMessage>
        ) : null}
        {state !== "unsupported" && state !== "not_installed" ? (
          <div className="flex flex-wrap gap-x-8 gap-y-3">
            <DataField
              label={t("knowledge.runtime.data")}
              value={
                <span className="tabular-nums">
                  {formatBytes(runtime.data_bytes)}
                </span>
              }
            />
            {runtime.started_at ? (
              <DataField
                label={t("knowledge.runtime.startedAt")}
                value={new Date(runtime.started_at).toLocaleString()}
              />
            ) : null}
          </div>
        ) : null}
        {state !== "unsupported" ? (
          <div className="flex flex-wrap gap-2">
            {state === "not_installed" ? (
              <Button
                disabled={locked}
                onClick={() => onAction("install_runtime")}
                size="sm"
                type="button"
              >
                {t("knowledge.runtime.download", {
                  size: formatBytes(runtime.download_bytes ?? 0),
                })}
              </Button>
            ) : null}
            {state === "not_installed" && !isWebEdition ? (
              <Button
                disabled={locked}
                onClick={onImport}
                size="sm"
                type="button"
                variant="outline"
              >
                {t("knowledge.runtime.import")}
              </Button>
            ) : null}
            {state === "installed" || state === "failed" ? (
              <Button
                disabled={locked}
                onClick={() => onAction("start_runtime")}
                size="sm"
                type="button"
              >
                {pending === "start_runtime" ? (
                  <LoaderCircle className="animate-spin motion-reduce:animate-none" />
                ) : null}
                {t("knowledge.runtime.start")}
              </Button>
            ) : null}
            {state === "running" ? (
              <>
                <Button
                  disabled={locked}
                  onClick={onCheck}
                  size="sm"
                  type="button"
                >
                  {pending === "check_runtime" ? (
                    <LoaderCircle className="animate-spin motion-reduce:animate-none" />
                  ) : (
                    <Flask />
                  )}
                  {pending === "check_runtime"
                    ? t("knowledge.runtime.checking")
                    : t("knowledge.runtime.check")}
                </Button>
                <Button
                  disabled={locked}
                  onClick={() => onAction("stop_runtime")}
                  size="sm"
                  type="button"
                  variant="outline"
                >
                  {t("knowledge.runtime.stop")}
                </Button>
              </>
            ) : null}
            {installed ? (
              <>
                <Button
                  disabled={locked}
                  onClick={() => onConfirm({ kind: "reset_runtime_data" })}
                  size="sm"
                  type="button"
                  variant="outline"
                >
                  {t("knowledge.runtime.reset")}
                </Button>
                <Button
                  disabled={locked}
                  onClick={() => onConfirm({ kind: "remove_runtime" })}
                  size="sm"
                  type="button"
                  variant="ghost"
                >
                  {t("knowledge.runtime.remove")}
                </Button>
              </>
            ) : null}
          </div>
        ) : null}
        {check ? <RuntimeCheckResult check={check} t={t} /> : null}
      </div>
    </Panel>
  );
}

function RuntimeCheckResult({
  check,
  t,
}: {
  check: KnowledgeRuntimeCheck;
  t: Translate;
}) {
  return (
    <div className="grid gap-2 rounded-md border p-3" role="status">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <StatusBadge tone={check.ok ? "positive" : "negative"}>
          {check.ok
            ? t("knowledge.runtime.checkPassed")
            : t("knowledge.runtime.checkFailed")}
        </StatusBadge>
        {check.engine ? (
          <span className="text-xs text-muted-foreground">
            {t("knowledge.runtime.engine")} · {check.engine}
          </span>
        ) : null}
      </div>
      <ul className="grid gap-1 text-sm">
        {check.steps.map((step) => {
          const wait = step.ok
            ? /^index_wait_ms=(\d+)$/.exec(step.detail ?? "")
            : null;
          return (
            <li className="flex min-w-0 items-start gap-2" key={step.name}>
              {step.ok ? (
                <Check
                  aria-hidden="true"
                  className="mt-0.5 size-4 shrink-0 text-success-foreground"
                />
              ) : (
                <X
                  aria-hidden="true"
                  className="mt-0.5 size-4 shrink-0 text-danger-foreground"
                />
              )}
              <span className="shrink-0">
                {t(`knowledge.runtime.steps.${step.name}`, {
                  defaultValue: step.name,
                })}
              </span>
              <span className="text-xs text-muted-foreground tabular-nums">
                {step.duration_ms} ms
              </span>
              {wait ? (
                <span className="text-xs text-muted-foreground">
                  {t("knowledge.runtime.indexWait", { ms: wait[1] })}
                </span>
              ) : !step.ok && step.detail ? (
                <span className="min-w-0 text-xs break-words text-danger-foreground">
                  {step.detail}
                </span>
              ) : null}
            </li>
          );
        })}
      </ul>
    </div>
  );
}

function ModelsPanel({
  checks,
  disabled,
  models,
  onCheck,
  onConfirm,
  onImport,
  onInstall,
  pending,
  t,
}: {
  checks: Record<string, KnowledgeEmbeddingCheck>;
  disabled: boolean;
  models: KnowledgeEmbeddingModel[];
  onCheck: (id: string) => void;
  onConfirm: (confirmation: Confirmation) => void;
  onImport: (id: string) => void;
  onInstall: (id: string) => void;
  pending: string | null;
  t: Translate;
}) {
  const locked = disabled || pending !== null;
  return (
    <Panel>
      <PanelHeader
        actions={
          <HelpPopover label={t("knowledge.models.helpLabel")}>
            <p>{t("knowledge.models.help")}</p>
          </HelpPopover>
        }
        icon={<ScanText className="size-4" />}
      >
        <h3 className="text-sm font-medium">{t("knowledge.models.title")}</h3>
      </PanelHeader>
      <ul className="divide-y">
        {models.map((model) => {
          const check = checks[model.id];
          const installed = model.state === "installed";
          return (
            <li className="grid gap-3 p-4" key={model.id}>
              <div className="flex flex-wrap items-start justify-between gap-2">
                <div className="min-w-0">
                  <p className="text-sm font-medium">{model.display_name}</p>
                  <p className="text-xs text-muted-foreground">
                    {t("knowledge.models.details", {
                      dimensions: model.dimensions,
                      license: model.license,
                    })}
                  </p>
                </div>
                <div className="flex shrink-0 items-center gap-1.5">
                  {model.loaded ? (
                    <StatusBadge tone="positive">
                      {t("knowledge.models.loaded")}
                    </StatusBadge>
                  ) : null}
                  <StatusBadge tone={modelTones[model.state]}>
                    {t(`knowledge.models.state.${model.state}`)}
                  </StatusBadge>
                </div>
              </div>
              {model.state === "installing" ? (
                <ProgressRow progress={model.progress} />
              ) : null}
              {model.error ? (
                <FormMessage tone="error">{model.error}</FormMessage>
              ) : null}
              <div className="flex flex-wrap gap-2">
                {model.state === "not_installed" || model.state === "failed" ? (
                  <>
                    <Button
                      disabled={locked}
                      onClick={() => onInstall(model.id)}
                      size="sm"
                      type="button"
                    >
                      {t("knowledge.models.download", {
                        size: formatBytes(model.download_bytes),
                      })}
                    </Button>
                    {!isWebEdition ? (
                      <Button
                        disabled={locked}
                        onClick={() => onImport(model.id)}
                        size="sm"
                        type="button"
                        variant="outline"
                      >
                        {t("knowledge.models.import")}
                      </Button>
                    ) : null}
                  </>
                ) : null}
                {installed ? (
                  <>
                    <Button
                      disabled={locked}
                      onClick={() => onCheck(model.id)}
                      size="sm"
                      type="button"
                    >
                      {pending === `check_model:${model.id}` ? (
                        <LoaderCircle className="animate-spin motion-reduce:animate-none" />
                      ) : (
                        <Flask />
                      )}
                      {pending === `check_model:${model.id}`
                        ? t("knowledge.models.checking")
                        : t("knowledge.models.check")}
                    </Button>
                    <Button
                      disabled={locked}
                      onClick={() =>
                        onConfirm({ kind: "remove_model", id: model.id })
                      }
                      size="sm"
                      type="button"
                      variant="ghost"
                    >
                      {t("knowledge.models.remove")}
                    </Button>
                  </>
                ) : null}
              </div>
              {check ? <ModelCheckResult check={check} t={t} /> : null}
            </li>
          );
        })}
      </ul>
    </Panel>
  );
}

function ModelCheckResult({
  check,
  t,
}: {
  check: KnowledgeEmbeddingCheck;
  t: Translate;
}) {
  return (
    <div className="grid gap-2 rounded-md border p-3" role="status">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="text-sm font-medium">
          {t("knowledge.models.checkTitle")}
        </span>
        <span className="text-xs text-muted-foreground tabular-nums">
          {t("knowledge.models.timing", {
            load: check.load_ms,
            embed: check.embed_ms,
          })}
        </span>
      </div>
      {check.pairs[0] ? (
        <p className="text-xs text-muted-foreground">
          {t("knowledge.models.reference")} · {check.pairs[0].left}
        </p>
      ) : null}
      <ul className="grid gap-2">
        {check.pairs.map((pair) => (
          <li className="grid gap-1" key={pair.right}>
            <div className="flex min-w-0 items-baseline justify-between gap-3 text-xs">
              <span
                className="min-w-0 truncate"
                title={`${pair.left} ↔ ${pair.right}`}
              >
                {pair.right}
              </span>
              <span className="shrink-0 text-muted-foreground tabular-nums">
                {t("knowledge.models.similarity")} {pair.similarity.toFixed(2)}
              </span>
            </div>
            <Progress value={Math.max(0, pair.similarity) * 100} />
          </li>
        ))}
      </ul>
    </div>
  );
}
