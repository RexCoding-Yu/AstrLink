import { useEffect, useRef, useState } from "react";

import { FormMessage } from "./components/FormMessage";
import { ModelRoutingGroupEditor } from "./components/ModelRoutingGroupEditor";
import { Button } from "./components/ui/button";
import { getRoutingSettings, updateRoutingSettings } from "./bridge";
import type { RoutingSettings } from "./failure-policy-model";
import { useT } from "./i18n";
import {
  validateModelRoutingDraft,
  type ModelRoutingDraftIssue,
  type ModelRoutingGroup,
} from "./model-routing-group-model";
import type { RoutableService } from "./service-model";
import { useWorkspaceSnapshot } from "./workspace-snapshots";

function groupsOf(settings: RoutingSettings | null): ModelRoutingGroup[] {
  return settings?.model_routing_groups ?? [];
}

/**
 * The model routing groups moved out of the provider routing page, so this
 * panel owns its own load/save loop and submits only `model_routing_groups` —
 * the sibling provider panel no longer carries the key in its patch.
 */
export function ModelRoutingGroupsPanel({
  ready,
  services,
  onDirtyChange,
}: {
  ready: boolean;
  services: readonly RoutableService[];
  onDirtyChange: (dirty: boolean) => void;
}) {
  const t = useT();
  const [settings, setSettings] = useWorkspaceSnapshot<RoutingSettings | null>(
    "routing-settings",
    null,
  );
  const [draft, setDraft] = useState<ModelRoutingGroup[] | null>(() =>
    settings ? groupsOf(settings) : null,
  );
  const [baseline, setBaseline] = useState(() =>
    settings ? JSON.stringify(groupsOf(settings)) : "",
  );
  const [loadError, setLoadError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [savedOnce, setSavedOnce] = useState(false);
  const [issues, setIssues] = useState<readonly ModelRoutingDraftIssue[]>([]);
  const [reload, setReload] = useState(0);
  const draftRef = useRef(draft);
  draftRef.current = draft;
  const mutationVersion = useRef(0);
  const dirty = draft !== null && JSON.stringify(draft) !== baseline;
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
    void getRoutingSettings()
      .then((loaded) => {
        if (!active || mutationVersion.current !== version) return;
        setSettings(loaded);
        // A reconnection must never replace an unsaved draft.
        if (!dirtyRef.current) {
          setDraft(groupsOf(loaded));
          setBaseline(JSON.stringify(groupsOf(loaded)));
        }
        setLoadError(null);
      })
      .catch((caught) => {
        if (active && mutationVersion.current === version)
          setLoadError(
            caught instanceof Error ? caught.message : t("failure.loadFailed"),
          );
      });
    return () => {
      active = false;
    };
  }, [ready, reload, t, setSettings]);

  const save = () => {
    if (draft === null || !ready || saving) return;
    if (JSON.stringify(draft) === baseline) return;
    const found = validateModelRoutingDraft(draft);
    if (found.length > 0) {
      setIssues(found);
      return;
    }
    const submitted = draft;
    setIssues([]);
    setError(null);
    mutationVersion.current += 1;
    setSaving(true);
    void updateRoutingSettings({ model_routing_groups: submitted })
      .then((result) => {
        const saved = groupsOf(result);
        setSettings(result);
        setBaseline(JSON.stringify(saved));
        setDraft((current) => (current === submitted ? saved : current));
        setSavedOnce(true);
      })
      .catch((caught: unknown) => {
        if (draftRef.current === submitted)
          setError(
            caught instanceof Error ? caught.message : t("routing.saveFailed"),
          );
      })
      .finally(() => setSaving(false));
  };

  return (
    <div
      className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-y-clip"
      data-testid="model-routing-groups-panel"
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
      {error ? (
        <FormMessage tone="error">
          {t("routing.saveFailedNotice")} {error}
          <Button
            type="button"
            variant="ghost"
            size="sm"
            disabled={!ready}
            onClick={() => setError(null)}
          >
            {t("common.retry")}
          </Button>
        </FormMessage>
      ) : null}
      {draft !== null ? (
        <fieldset
          disabled={!ready}
          className="flex min-h-0 min-w-0 flex-1 flex-col"
        >
          <ModelRoutingGroupEditor
            value={draft}
            services={services}
            disabled={!ready}
            validationIssues={issues}
            onChange={(next) => {
              setIssues([]);
              setDraft(next);
            }}
          />
        </fieldset>
      ) : (
        <p className="text-xs text-muted-foreground">
          {ready ? t("common.loading") : t("services.gatewayNotReady")}
        </p>
      )}
    </div>
  );
}
