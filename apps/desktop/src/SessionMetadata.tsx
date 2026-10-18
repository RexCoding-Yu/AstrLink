import { type FormEvent, useEffect, useState } from "react";

import { Field } from "@/components/Field";
import { FormMessage } from "@/components/FormMessage";
import { HelpPopover } from "@/components/HelpPopover";
import { LoadingState } from "@/components/LoadingState";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  getSessionMetadata,
  listMetadataAttributes,
  setSessionMetadata,
  unbindSessionMetadata,
} from "./bridge";
import { controlErrorMessage } from "./control-error";
import { useT } from "./i18n";
import { notify } from "./notify";
import type {
  MetadataAttribute,
  SessionMetadata,
  SessionMetadataValue,
} from "./plugin-model";
import { metadataValueFits } from "./TokenMetadataDialog";

/**
 * A session's bindings in the session detail's header: one chip per
 * attribute a session marker can bind, opening a dialog to change or unbind
 * it. Nothing shows while no attribute has a marker and nothing is bound.
 * refreshKey changes whenever the session does, since a reply may bind it.
 */
export function SessionMetadataChips({
  sessionId,
  refreshKey,
}: {
  sessionId: string;
  refreshKey: string;
}) {
  const t = useT();
  const [attributes, setAttributes] = useState<MetadataAttribute[] | null>(
    null,
  );
  const [bindings, setBindings] = useState<SessionMetadata | null>(null);
  const [open, setOpen] = useState(false);

  useEffect(() => {
    let current = true;
    const load = async () => {
      const [list, values] = await Promise.all([
        listMetadataAttributes(),
        getSessionMetadata(sessionId),
      ]);
      if (!current || !Array.isArray(list) || !values) return;
      setAttributes(list);
      setBindings(values);
    };
    load().catch(() => {
      // The header leaves out what it could not read.
      if (current) setBindings(null);
    });
    return () => {
      current = false;
    };
  }, [sessionId, refreshKey]);

  const shown = (attributes ?? []).filter(
    (attribute) => attribute.marker || bindings?.values[attribute.key],
  );
  if (shown.length === 0 || bindings === null) return null;
  return (
    <>
      {shown.map((attribute) => {
        const bound = bindings.values[attribute.key];
        return (
          <Button
            className="h-6 max-w-44 shrink-0 gap-1 px-1.5 text-xs font-normal"
            data-testid="session-metadata-chip"
            key={attribute.key}
            onClick={() => setOpen(true)}
            size="sm"
            title={bound ? sourceText(bound, t) : undefined}
            type="button"
            variant="ghost"
          >
            <span className="text-muted-foreground">{attribute.label}</span>
            <span className="truncate font-mono">
              {bound
                ? bound.value === "none"
                  ? t("sessionMetadata.none")
                  : bound.value
                : t("sessionMetadata.notBound")}
            </span>
          </Button>
        );
      })}
      <SessionMetadataDialog
        attributes={shown}
        bindings={bindings}
        onChanged={setBindings}
        onClose={() => setOpen(false)}
        open={open}
        sessionId={sessionId}
      />
    </>
  );
}

function sourceText(
  value: SessionMetadataValue,
  t: ReturnType<typeof useT>,
): string {
  const source = t(`sessionMetadata.setBy.${value.set_by}`, {
    defaultValue: value.set_by,
  });
  return value.turn
    ? t("sessionMetadata.turn", { source, turn: value.turn })
    : source;
}

function SessionMetadataDialog({
  attributes,
  bindings,
  onChanged,
  onClose,
  open,
  sessionId,
}: {
  attributes: MetadataAttribute[];
  bindings: SessionMetadata;
  onChanged: (bindings: SessionMetadata) => void;
  onClose: () => void;
  open: boolean;
  sessionId: string;
}) {
  const t = useT();
  const [values, setValues] = useState<Record<string, string>>({});
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!open) return;
    setError(null);
    setValues(
      Object.fromEntries(
        attributes.map((attribute) => [
          attribute.key,
          bindings.values[attribute.key]?.value ?? "",
        ]),
      ),
    );
    // The form starts from the bindings the dialog opened with.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  async function save(event: FormEvent) {
    event.preventDefault();
    const changes: Array<[string, string]> = [];
    for (const attribute of attributes) {
      const value = (values[attribute.key] ?? "").trim();
      if (value === "" || value === bindings.values[attribute.key]?.value)
        continue;
      if (!metadataValueFits(attribute, value)) {
        setError(t("sessionMetadata.invalidValue", { label: attribute.label }));
        return;
      }
      changes.push([attribute.key, value]);
    }
    setSaving(true);
    try {
      let latest = bindings;
      for (const [key, value] of changes) {
        latest = await setSessionMetadata(sessionId, key, value);
      }
      onChanged(latest);
      onClose();
    } catch (reason) {
      setError(controlErrorMessage(reason, t("sessionMetadata.saveFailed")));
    } finally {
      setSaving(false);
    }
  }

  async function unbind(attribute: MetadataAttribute) {
    setSaving(true);
    try {
      const latest = await unbindSessionMetadata(sessionId, attribute.key);
      onChanged(latest);
      setValues((current) => ({ ...current, [attribute.key]: "none" }));
      setError(null);
      notify.success(
        t("sessionMetadata.unboundNotice", { label: attribute.label }),
      );
    } catch (reason) {
      setError(controlErrorMessage(reason, t("sessionMetadata.saveFailed")));
    } finally {
      setSaving(false);
    }
  }

  return (
    <Dialog
      onOpenChange={(next) => {
        if (!next && !saving) onClose();
      }}
      open={open}
    >
      <DialogContent>
        <form className="grid gap-4" onSubmit={save}>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              {t("sessionMetadata.title")}
              <HelpPopover inDialog label={t("sessionMetadata.helpLabel")}>
                <div className="grid gap-2">
                  <p>{t("sessionMetadata.help")}</p>
                  <p>{t("sessionMetadata.unbindHelp")}</p>
                </div>
              </HelpPopover>
            </DialogTitle>
          </DialogHeader>
          {attributes.length === 0 ? (
            <LoadingState label={t("common.loading")} />
          ) : (
            <div className="grid gap-3">
              {attributes.map((attribute) => {
                const bound = bindings.values[attribute.key];
                return (
                  <Field
                    htmlFor={`session-metadata-${attribute.key}`}
                    key={attribute.key}
                    label={
                      bound
                        ? `${attribute.label} · ${sourceText(bound, t)}`
                        : attribute.label
                    }
                  >
                    <div className="flex items-center gap-2">
                      <Input
                        autoComplete="off"
                        className="font-mono"
                        id={`session-metadata-${attribute.key}`}
                        onChange={(event) =>
                          setValues((current) => ({
                            ...current,
                            [attribute.key]: event.target.value,
                          }))
                        }
                        placeholder={t("sessionMetadata.unboundValue")}
                        value={values[attribute.key] ?? ""}
                      />
                      <Button
                        disabled={saving || !bound || bound.value === "none"}
                        onClick={() => void unbind(attribute)}
                        type="button"
                        variant="outline"
                      >
                        {t("sessionMetadata.unbind")}
                      </Button>
                    </div>
                  </Field>
                );
              })}
            </div>
          )}
          {error ? <FormMessage tone="error">{error}</FormMessage> : null}
          <DialogFooter>
            <Button
              disabled={saving}
              onClick={onClose}
              type="button"
              variant="outline"
            >
              {t("common.cancel")}
            </Button>
            <Button disabled={saving} type="submit">
              {t("common.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
