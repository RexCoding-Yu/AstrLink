import { useEffect, useRef, useState } from "react";

import { Field } from "./components/Field";
import { FormMessage } from "./components/FormMessage";
import { ModelSelect } from "./components/ModelSelect";
import { Panel } from "./components/Panel";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Switch } from "./components/ui/switch";
import { Textarea } from "./components/ui/textarea";
import {
  classifierCredentialAction,
  getClassifierSettings,
  updateClassifierSettings,
} from "./bridge";
import {
  classifierExistingModelTimeoutMaxMS,
  classifierExistingModelTimeoutMinMS,
  classifierProtocols,
  classifierTimeoutMaxMS,
  classifierTimeoutMinMS,
  defaultClassifierPrompt,
  linkClassifierProtocol,
  type ClassifierProtocol,
  type ClassifierSettings,
} from "./classifier-settings-model";
import {
  calculateClassifierSettingsIssues,
  type ClassifierSettingsIssue,
} from "./classifier-settings-validation";
import { useT } from "./i18n";
import { intelligentRoutingErrorText } from "./intelligent-routing-errors";
import type { RoutableService } from "./service-model";

function issueFor(
  issues: readonly ClassifierSettingsIssue[],
  field: ClassifierSettingsIssue["field"],
): ClassifierSettingsIssue | undefined {
  return issues.find((issue) => issue.field === field);
}

function FieldIssue({
  issue,
  namespace = "classifier.fieldErrors",
}: {
  issue: ClassifierSettingsIssue | undefined;
  namespace?: string;
}) {
  const t = useT();
  if (issue === undefined) return null;
  return (
    <span role="alert" className="text-xs text-danger-foreground">
      {t(`${namespace}.${issue.problem}`)}
    </span>
  );
}

/**
 * An empty stored prompt means "use the built-in one". Show that text instead
 * of a blank box so the user edits a working instruction, `{{labels}}`
 * placeholder included. Both draft and baseline get it, so loading alone never
 * marks the tab dirty; saving it unchanged behaves exactly like the empty value.
 */
function withDefaultPrompt(settings: ClassifierSettings): ClassifierSettings {
  if (settings.inline.prompt.trim() !== "") return settings;
  return {
    ...settings,
    inline: { ...settings.inline, prompt: defaultClassifierPrompt },
  };
}

/**
 * The classifier is its own singleton resource — not a RoutingSettings field and
 * not a Service. It therefore gets its own load/save loop and an explicit save,
 * unlike the auto-saving provider page.
 */
export function ClassifierSettingsPanel({
  ready,
  services,
  onDirtyChange,
}: {
  ready: boolean;
  services: readonly RoutableService[];
  onDirtyChange: (dirty: boolean) => void;
}) {
  const t = useT();
  const [draft, setDraft] = useState<ClassifierSettings | null>(null);
  const [baseline, setBaseline] = useState("");
  const [credentialConfigured, setCredentialConfigured] = useState(false);
  const [credentialDraft, setCredentialDraft] = useState("");
  const [loadError, setLoadError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [savedOnce, setSavedOnce] = useState(false);
  const [showIssues, setShowIssues] = useState(false);
  const [reload, setReload] = useState(0);
  const draftRef = useRef(draft);
  draftRef.current = draft;
  const credentialDraftRef = useRef(credentialDraft);
  credentialDraftRef.current = credentialDraft;
  const mutationVersion = useRef(0);
  const dirty =
    draft !== null &&
    (JSON.stringify(draft) !== baseline || credentialDraft.trim() !== "");
  const dirtyRef = useRef(dirty);
  dirtyRef.current = dirty;

  useEffect(() => {
    onDirtyChange(dirty);
    return () => onDirtyChange(false);
  }, [dirty, onDirtyChange]);

  useEffect(() => {
    if (!ready) return;
    let active = true;
    const version = mutationVersion.current;
    void Promise.all([
      getClassifierSettings(),
      classifierCredentialAction("status"),
    ])
      .then(([loaded, credential]) => {
        if (!active || mutationVersion.current !== version) return;
        setCredentialConfigured(credential.configured);
        // A reconnection must never replace an unsaved draft.
        if (!dirtyRef.current) {
          const shown = withDefaultPrompt(loaded);
          setDraft(shown);
          setBaseline(JSON.stringify(shown));
        }
        setLoadError(null);
      })
      .catch((caught) => {
        if (active && mutationVersion.current === version)
          setLoadError(
            intelligentRoutingErrorText(caught, t("classifier.loadFailed")),
          );
      });
    return () => {
      active = false;
    };
  }, [ready, reload, t]);

  const issues =
    draft === null
      ? []
      : calculateClassifierSettingsIssues(draft, {
          credentialConfigured,
          credentialDraft,
        });
  const blocking = showIssues ? issues : [];
  const usesExistingModel = draft?.classifier_source === "existing_model";

  const patch = (next: Partial<ClassifierSettings>): void => {
    setShowIssues(false);
    setDraft((current) =>
      current === null ? current : { ...current, ...next },
    );
  };

  const save = (): void => {
    if (draft === null || !ready || saving) return;
    if (JSON.stringify(draft) === baseline && credentialDraft.trim() === "")
      return;
    if (issues.length > 0) {
      setShowIssues(true);
      return;
    }
    const submitted = draft;
    const submittedKey = credentialDraft;
    setError(null);
    mutationVersion.current += 1;
    setSaving(true);
    void (async () => {
      // The key goes only to the credential sub-resource; the settings
      // document must never carry it.
      if (submittedKey.trim() !== "") {
        const credential = await classifierCredentialAction(
          "save_key",
          submittedKey,
        );
        // The key is stored from here on, so the panel must say so even if the
        // settings document below is rejected — otherwise the UI would report
        // "not configured" while the backend holds a working key.
        setCredentialConfigured(credential.configured);
        if (credentialDraftRef.current === submittedKey) setCredentialDraft("");
      }
      const result = withDefaultPrompt(
        await updateClassifierSettings(submitted),
      );
      const status = await classifierCredentialAction("status");
      setBaseline(JSON.stringify(result));
      setDraft((current) => (current === submitted ? result : current));
      setCredentialConfigured(status.configured);
      if (credentialDraftRef.current === submittedKey) setCredentialDraft("");
      setSavedOnce(true);
    })()
      .catch((caught: unknown) => {
        if (draftRef.current === submitted)
          setError(
            intelligentRoutingErrorText(caught, t("classifier.saveFailed")),
          );
      })
      .finally(() => setSaving(false));
  };

  const deleteKey = (): void => {
    // Deleting also drops the local draft: leaving it in place kept the panel
    // dirty, so the next save would quietly write the just-deleted key back.
    setError(null);
    void classifierCredentialAction("delete_key")
      .then((status) => {
        setCredentialConfigured(status.configured);
        setCredentialDraft("");
      })
      .catch((caught: unknown) =>
        setError(
          intelligentRoutingErrorText(caught, t("classifier.saveFailed")),
        ),
      );
  };

  return (
    <div
      className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-y-clip"
      data-testid="classifier-settings-panel"
    >
      <div className="flex min-w-0 shrink-0 items-center justify-end gap-2">
        <span
          role="status"
          aria-live="polite"
          className="text-xs text-muted-foreground"
        >
          {!ready && dirty
            ? t("routing.waitingConnection")
            : saving
              ? t("common.saving")
              : dirty
                ? t("routing.notSaved")
                : savedOnce
                  ? t("routing.saved")
                  : ""}
        </span>
        <Button
          type="button"
          size="sm"
          disabled={!ready || !dirty || saving}
          onClick={save}
        >
          {t("routing.save")}
        </Button>
      </div>
      {loadError ? (
        <FormMessage tone="error">
          {loadError}
          <Button
            type="button"
            variant="ghost"
            onClick={() => setReload((value) => value + 1)}
          >
            {t("common.retry")}
          </Button>
        </FormMessage>
      ) : null}
      {error || (showIssues && issues.length > 0) ? (
        <FormMessage tone="error">
          {error ? t("classifier.saveFailedNotice") : t("classifier.fixErrors")}
          {error ? (
            <span className="text-danger-foreground"> {error}</span>
          ) : null}
        </FormMessage>
      ) : null}
      {draft === null ? (
        <p className="text-xs text-muted-foreground">
          {ready ? t("common.loading") : t("services.gatewayNotReady")}
        </p>
      ) : (
        <fieldset
          disabled={!ready}
          className="gutter-scroller flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-y-auto border-0 p-0 pb-1"
        >
          <Panel className="shrink-0 p-3">
            <Field
              group
              label={t("classifier.enabled")}
              tooltip={t("classifier.enabledHint")}
            >
              <div className="flex items-center gap-2">
                <Switch
                  checked={draft.enabled}
                  aria-label={t("classifier.enabled")}
                  onCheckedChange={(checked) => patch({ enabled: checked })}
                />
                <span className="text-xs text-text-secondary">
                  {t(draft.enabled ? "classifier.on" : "classifier.off")}
                </span>
              </div>
            </Field>
          </Panel>
          <Panel className="flex shrink-0 flex-col gap-3 p-3">
            <Field
              group
              label={t("classifier.standaloneModel")}
              tooltip={t("classifier.standaloneModelHint")}
              tooltipLabel={t("classifier.standaloneModelHintLabel")}
            >
              <div className="flex items-center gap-2">
                <Switch
                  // "Use a standalone classifier model" is OFF for the reuse
                  // branch: off = pick a model the gateway already has
                  // (`existing_model`), on = configure the separate Jev /
                  // Decisions endpoint (`inline`). See design §5.1.
                  checked={draft.classifier_source === "inline"}
                  aria-label={t("classifier.standaloneModel")}
                  onCheckedChange={(checked) =>
                    // Switching source keeps both branches' drafts intact, so
                    // toggling back and forth never discards typed input.
                    patch({
                      classifier_source: checked ? "inline" : "existing_model",
                    })
                  }
                />
                <span className="text-xs text-text-secondary">
                  {t(
                    draft.classifier_source === "inline"
                      ? "classifier.standaloneModelOn"
                      : "classifier.standaloneModelOff",
                  )}
                </span>
              </div>
            </Field>
          </Panel>
          {draft.classifier_source === "existing_model" ? (
            <Panel className="flex shrink-0 flex-col gap-3 p-3">
              <Field
                label={t("classifier.existingModelService")}
                tooltip={t("classifier.existingModelServiceHint")}
              >
                <ModelSelect
                  options={services
                    .filter((item) => item.enabled)
                    .map((item) => item.id)}
                  value={draft.existing_model.service_id}
                  disabled={!ready}
                  allowCustomValue={false}
                  optionLabel={(serviceID) =>
                    services.find((item) => item.id === serviceID)?.name ??
                    serviceID
                  }
                  leadingIcon={null}
                  placeholder={t("classifier.existingModelServicePlaceholder")}
                  aria-label={t("classifier.existingModelService")}
                  onValueChange={(service_id) =>
                    // Changing service keeps the model text: the name may still
                    // be valid on the new service, and clearing it silently
                    // would look like the panel dropped the user's input.
                    patch({
                      existing_model: { ...draft.existing_model, service_id },
                    })
                  }
                />
              </Field>
              <Field
                label={t("classifier.existingModelName")}
                tooltip={t("classifier.existingModelNameHint")}
              >
                <ModelSelect
                  options={
                    services.find(
                      (item) => item.id === draft.existing_model.service_id,
                    )?.models ?? []
                  }
                  value={draft.existing_model.model}
                  disabled={!ready}
                  allowCustomValue
                  leadingIcon={null}
                  placeholder={t("classifier.existingModelNamePlaceholder")}
                  aria-label={t("classifier.existingModelName")}
                  onValueChange={(model) =>
                    patch({
                      existing_model: { ...draft.existing_model, model },
                    })
                  }
                />
              </Field>
            </Panel>
          ) : null}
          <Panel className="flex shrink-0 flex-col gap-3 p-3">
            <Field
              group
              label={t("classifier.protocol")}
              tooltip={t("classifier.protocolHint")}
            >
              <div className="flex min-w-0 flex-wrap gap-1.5">
                {classifierProtocols.map((value) => (
                  <Button
                    key={value}
                    type="button"
                    size="sm"
                    variant={
                      draft.inline.protocol === value ? "default" : "outline"
                    }
                    aria-pressed={draft.inline.protocol === value}
                    onClick={() =>
                      setDraft((current) =>
                        current === null
                          ? current
                          : {
                              ...current,
                              inline: linkClassifierProtocol(
                                current.inline,
                                value as ClassifierProtocol,
                              ),
                            },
                      )
                    }
                  >
                    {t(`classifier.protocols.${value}`)}
                  </Button>
                ))}
              </div>
            </Field>
            <Field
              label={t("classifier.endpoint")}
              tooltip={t("classifier.endpointHint")}
            >
              <Input
                value={draft.inline.endpoint}
                aria-label={t("classifier.endpoint")}
                aria-invalid={issueFor(blocking, "endpoint") !== undefined}
                onChange={(event) =>
                  patch({
                    inline: { ...draft.inline, endpoint: event.target.value },
                  })
                }
              />
              <FieldIssue
                issue={issueFor(blocking, "endpoint")}
                namespace="classifier.endpointErrors"
              />
            </Field>
            <Field
              label={t("classifier.model")}
              tooltip={t("classifier.modelHint")}
            >
              <Input
                value={draft.inline.model}
                aria-label={t("classifier.model")}
                aria-invalid={issueFor(blocking, "model") !== undefined}
                onChange={(event) =>
                  patch({
                    inline: { ...draft.inline, model: event.target.value },
                  })
                }
              />
              <FieldIssue issue={issueFor(blocking, "model")} />
            </Field>
            <Field
              label={t("classifier.apiKey")}
              tooltip={t("classifier.apiKeyHint")}
            >
              <Input
                type="password"
                autoComplete="off"
                value={credentialDraft}
                aria-label={t("classifier.apiKey")}
                aria-invalid={issueFor(blocking, "apiKey") !== undefined}
                placeholder={
                  credentialConfigured
                    ? t("classifier.apiKeyStored")
                    : t("classifier.apiKeyPlaceholder")
                }
                onChange={(event) => {
                  setShowIssues(false);
                  setCredentialDraft(event.target.value);
                }}
              />
              <span className="flex items-center gap-2">
                <span className="text-xs text-muted-foreground">
                  {credentialConfigured
                    ? t("classifier.credentialConfigured")
                    : t("classifier.credentialMissing")}
                </span>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  disabled={!ready || !credentialConfigured}
                  onClick={deleteKey}
                >
                  {t("classifier.deleteKey")}
                </Button>
              </span>
              <FieldIssue issue={issueFor(blocking, "apiKey")} />
            </Field>
          </Panel>
          <Panel className="flex shrink-0 flex-col gap-3 p-3">
            <Field
              label={t("classifier.prompt")}
              tooltip={t("classifier.promptHint")}
            >
              <Textarea
                rows={4}
                value={draft.inline.prompt}
                aria-label={t("classifier.prompt")}
                aria-invalid={issueFor(blocking, "prompt") !== undefined}
                onChange={(event) =>
                  patch({
                    inline: { ...draft.inline, prompt: event.target.value },
                  })
                }
              />
              <FieldIssue issue={issueFor(blocking, "prompt")} />
            </Field>
          </Panel>
          <Panel className="flex shrink-0 flex-col gap-3 p-3">
            <h3 className="text-xs font-medium text-text-secondary">
              {t("classifier.advanced")}
            </h3>
            {/*
              The budget belongs to the source that spends it: `inline` reads
              its own 1–5000 envelope and `existing_model` its independent
              1–60000 budget, because a real model call takes far longer than
              the inline one. Editing `inline.timeout_ms` while the reuse
              branch is selected would change a value nothing reads.
            */}
            <Field
              label={t("classifier.timeout")}
              tooltip={
                usesExistingModel
                  ? t("classifier.timeoutHintExistingModel", {
                      max: classifierExistingModelTimeoutMaxMS,
                    })
                  : t("classifier.timeoutHint", {
                      max: classifierTimeoutMaxMS,
                    })
              }
            >
              <Input
                type="number"
                min={
                  usesExistingModel
                    ? classifierExistingModelTimeoutMinMS
                    : classifierTimeoutMinMS
                }
                max={
                  usesExistingModel
                    ? classifierExistingModelTimeoutMaxMS
                    : classifierTimeoutMaxMS
                }
                value={String(
                  usesExistingModel
                    ? draft.existing_model.timeout_ms
                    : draft.inline.timeout_ms,
                )}
                aria-label={t("classifier.timeout")}
                aria-invalid={issueFor(blocking, "timeout_ms") !== undefined}
                onChange={(event) =>
                  patch(
                    usesExistingModel
                      ? {
                          existing_model: {
                            ...draft.existing_model,
                            timeout_ms: event.target.valueAsNumber,
                          },
                        }
                      : {
                          inline: {
                            ...draft.inline,
                            timeout_ms: event.target.valueAsNumber,
                          },
                        },
                  )
                }
              />
              <FieldIssue issue={issueFor(blocking, "timeout_ms")} />
            </Field>
            <Field
              label={t("classifier.threshold")}
              tooltip={t("classifier.thresholdHint")}
            >
              <Input
                type="number"
                min={0}
                max={1}
                step={0.05}
                value={String(draft.inline.low_confidence_threshold)}
                aria-label={t("classifier.threshold")}
                aria-invalid={
                  issueFor(blocking, "low_confidence_threshold") !== undefined
                }
                onChange={(event) =>
                  patch({
                    inline: {
                      ...draft.inline,
                      low_confidence_threshold: event.target.valueAsNumber,
                    },
                  })
                }
              />
              <FieldIssue
                issue={issueFor(blocking, "low_confidence_threshold")}
              />
            </Field>
          </Panel>
        </fieldset>
      )}
    </div>
  );
}
