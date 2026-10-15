import { useEffect, useRef, useState } from "react";

import { Field } from "./components/Field";
import { FormMessage } from "./components/FormMessage";
import { HelpPopover } from "./components/HelpPopover";
import { Panel, PanelHeader } from "./components/Panel";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "./components/ui/table";
import {
  getClassifierSettings,
  getRoutingSettings,
  updateClassifierSettings,
} from "./bridge";
import {
  classifierMaxCustomDimensions,
  type ClassifierDimension,
  type ClassifierSettings,
} from "./classifier-settings-model";
import {
  calculateClassifierDimensionIssues,
  type ClassifierDimensionIssue,
} from "./classifier-settings-validation";
import type { RoutingSettings } from "./failure-policy-model";
import { useT } from "./i18n";
import {
  modelRoutingCatalog,
  modelRoutingCatalogLabelDomain,
} from "./model-routing-catalog-model";
import { useWorkspaceSnapshot } from "./workspace-snapshots";

/** Locates one problem inside the dimension list, for the inline marker. */
function dimensionIssue(
  issues: readonly ClassifierDimensionIssue[],
  index: number,
  target: ClassifierDimensionIssue["target"],
  optionIndex?: number,
): ClassifierDimensionIssue | undefined {
  return issues.find(
    (issue) =>
      issue.index === index &&
      issue.target === target &&
      issue.optionIndex === optionIndex,
  );
}

function DimensionIssue({
  issue,
}: {
  issue: ClassifierDimensionIssue | undefined;
}) {
  const t = useT();
  if (issue === undefined) return null;
  return (
    <span role="alert" className="text-xs text-danger-foreground">
      {t(`modelRoutingGroup.dimensionErrors.${issue.problem}`)}
    </span>
  );
}

/**
 * 「字段类型」页签：规则条件能用的元信息字段一览，加上自定义分类维度的定义。
 *
 * 两个区块并列，权限不同：
 * - **内建字段表**只读。它由代码固定（`modelRoutingFields`），用户不可改。
 * - **自定义维度**可编辑。它本质就是「内建字段之外的路由字段」——一样进
 *   路由事实、一样被规则字段选择器引用，只是由用户自己定义（裁定 6：全局资源）。
 *
 * 维度定义落在本页签是创始人决策（`founder-decisions-r31.md` 决策 1）：用户在
 * 这里看到字段列表，最自然的下一步就是「再定义一个字段」。
 *
 * 保存写回**分类器设置单例**（`ClassifierSettings.dimensions`），因为它与
 * `RoutingSettings` 是两个资源，后者跨 Go/Rust 字段白名单，不得为此扩容。
 * 保存成功后把结果广播到共享快照，`ModelRoutingGroupsPanel` 据此立即刷新
 * 规则编辑器的字段选择器。
 */
export function ModelRoutingFieldCatalogPanel({
  ready,
  onDirtyChange = () => {},
}: {
  ready: boolean;
  onDirtyChange?: (dirty: boolean) => void;
}) {
  const t = useT();
  const [settings, setSettings] = useWorkspaceSnapshot<RoutingSettings | null>(
    "routing-settings",
    null,
  );
  // The saved dimension list, shared with the routing-groups tab so a defition
  // written here is visible to the rule field selector right away.
  const [savedDimensions, setSavedDimensions] = useWorkspaceSnapshot<
    ClassifierDimension[]
  >("classifier-dimensions", []);
  const [document, setDocument] = useState<ClassifierSettings | null>(null);
  const [draft, setDraft] = useState<ClassifierDimension[] | null>(null);
  const [baseline, setBaseline] = useState("");
  const [loadError, setLoadError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [savedOnce, setSavedOnce] = useState(false);
  const [showIssues, setShowIssues] = useState(false);
  const [reload, setReload] = useState(0);
  const draftRef = useRef(draft);
  draftRef.current = draft;
  const dirty =
    draft !== null && JSON.stringify(draft) !== baseline && baseline !== "";
  const dirtyRef = useRef(dirty);
  dirtyRef.current = dirty;

  useEffect(() => {
    onDirtyChange(dirty);
    return () => onDirtyChange(false);
  }, [dirty, onDirtyChange]);

  useEffect(() => {
    if (!ready) return;
    let active = true;
    void Promise.all([getRoutingSettings(), getClassifierSettings()])
      .then(([loadedRouting, loadedClassifier]) => {
        if (!active) return;
        setSettings(loadedRouting);
        setDocument(loadedClassifier);
        const loaded = loadedClassifier.dimensions ?? [];
        // The saved list is display data, so it always tracks the server.
        setSavedDimensions(loaded);
        // A reconnection must never replace an unsaved draft.
        if (!dirtyRef.current) {
          setDraft(loaded);
          setBaseline(JSON.stringify(loaded));
        }
        setLoadError(null);
      })
      .catch((caught: unknown) => {
        if (active)
          setLoadError(
            caught instanceof Error ? caught.message : t("failure.loadFailed"),
          );
      });
    return () => {
      active = false;
    };
  }, [ready, reload, t, setSettings, setSavedDimensions]);

  const issues =
    draft === null ? [] : calculateClassifierDimensionIssues(draft);
  const blocking = showIssues ? issues : [];

  /** The freshest singleton, re-read immediately before a write. */
  const readClassifierSettings = (): Promise<ClassifierSettings> =>
    getClassifierSettings();

  const patch = (next: ClassifierDimension[]): void => {
    setShowIssues(false);
    setDraft(next);
  };

  const save = (): void => {
    if (draft === null || document === null || !ready || saving) return;
    if (JSON.stringify(draft) === baseline) return;
    if (issues.length > 0) {
      setShowIssues(true);
      return;
    }
    const submitted = draft;
    setError(null);
    setSaving(true);
    void readClassifierSettings()
      .then((latest) =>
        // Read-modify-write, not a patch of the document loaded at mount: the
        // classifier tab edits the same singleton, so writing back a stale copy
        // would silently revert an endpoint or prompt the user saved there
        // while this tab was open. Only `dimensions` is this panel's to set.
        updateClassifierSettings({ ...latest, dimensions: submitted }).then(
          (result) => ({ result, latest }),
        ),
      )
      .then(({ result, latest }) => {
        const saved = result.dimensions ?? [];
        setDocument({ ...latest, ...result });
        setSavedDimensions(saved);
        setBaseline(JSON.stringify(saved));
        setDraft((current) => (current === submitted ? saved : current));
        setSavedOnce(true);
      })
      .catch((caught: unknown) => {
        if (draftRef.current === submitted)
          setError(
            caught instanceof Error ? caught.message : t("classifier.saveFailed"),
          );
      })
      .finally(() => setSaving(false));
  };

  const groups = settings?.model_routing_groups ?? [];
  // 组级覆盖优先；全部为空时回落到内建默认域。这与编辑器里
  // `group.task_type_labels?.length ? … : modelRoutingTaskTypes` 的判定一致。
  const labelDomain = modelRoutingCatalogLabelDomain(
    groups.map((group) => group.task_type_labels),
  );
  // 自定义维度也进目录：它和内建字段一样是规则可以引用的条件字段，只是来源
  // 标注为「分类器」。传入定义（而不只是名字）后，取值域列会列出它自己的选项。
  const definitions = draft ?? savedDimensions;
  const entries = modelRoutingCatalog(
    definitions.map((dimension) => dimension.name),
    definitions,
  );
  const atCapacity = definitions.length >= classifierMaxCustomDimensions;
  const newDimension = (): ClassifierDimension => ({
    name: "",
    description: "",
    options: [{ name: "", description: "" }],
  });

  return (
    <div
      className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-y-clip"
      data-testid="model-routing-field-catalog-panel"
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
      {/* One scroller for the whole tab, every Panel `shrink-0` inside it.
          Splitting growth between a `flex-1` block and a `shrink-0` block worked
          while this page had two panels; a third, variable-height one makes the
          whole column the only thing that can size itself, which is the shape
          ASTR-17 landed on for the classifier tab. */}
      <fieldset
        disabled={!ready}
        className="gutter-scroller flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-y-auto border-0 p-0 pb-1"
      >
      <Panel className="flex min-h-0 shrink-0 flex-col">
        <PanelHeader
          actions={
            <HelpPopover
              label={t("modelRoutingGroup.catalogGlobalLabelHintLabel")}
            >
              {t("modelRoutingGroup.catalogGlobalLabelHint")}
            </HelpPopover>
          }
        >
          <h3 className="text-sm font-semibold">
            {t("modelRoutingGroup.catalogTitle")}
          </h3>
        </PanelHeader>
        <div className="min-h-0 overflow-x-auto">
          <Table className="text-xs">
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead className="px-4">
                  {t("modelRoutingGroup.catalogField")}
                </TableHead>
                <TableHead className="px-4">
                  {t("modelRoutingGroup.catalogKind")}
                </TableHead>
                <TableHead className="px-4">
                  {t("modelRoutingGroup.catalogSource")}
                </TableHead>
                <TableHead className="px-4">
                  {t("modelRoutingGroup.catalogOperators")}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {entries.map((entry) => (
                <TableRow
                  key={entry.field}
                  data-model-routing-catalog-field={entry.field}
                >
                  <TableCell className="px-4">
                    {t(`modelRouting.fields.${entry.field}`)}
                    <span className="ml-1.5 font-mono text-muted-foreground">
                      {entry.field}
                    </span>
                  </TableCell>
                  <TableCell className="px-4">
                    {t(`modelRoutingGroup.fieldKind.${entry.kind}`)}
                    {entry.values ? (
                      <span className="ml-1.5 text-muted-foreground">
                        {entry.values
                          .map((value) =>
                            t(`modelRouting.values.${entry.field}.${value}`),
                          )
                          .join(" / ")}
                      </span>
                    ) : null}
                  </TableCell>
                  <TableCell className="px-4">
                    {t(`modelRouting.fieldSource.${entry.source}`)}
                  </TableCell>
                  <TableCell className="px-4 font-mono text-muted-foreground">
                    {entry.operators.join(", ")}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </Panel>
      <Panel className="flex min-h-0 shrink-0 flex-col">
        <PanelHeader
          actions={
            <HelpPopover
              label={t("modelRoutingGroup.catalogDimensionsHintLabel")}
            >
              {t("modelRoutingGroup.catalogDimensionsHint")}
            </HelpPopover>
          }
        >
          <h3 className="text-sm font-semibold">
            {t("modelRoutingGroup.catalogDimensionsTitle")}
          </h3>
        </PanelHeader>
        <div className="flex min-w-0 flex-col gap-3 px-4 pb-3">
          {draft === null ? (
            <p className="text-xs text-muted-foreground">
              {ready ? t("common.loading") : t("services.gatewayNotReady")}
            </p>
          ) : (
            <>
              {draft.length === 0 ? (
                <p className="text-xs text-muted-foreground">
                  {t("modelRoutingGroup.catalogDimensionEmpty")}
                </p>
              ) : null}
              {draft.map((dimension, index) => (
                <Panel
                  key={index}
                  tone="inset"
                  className="flex min-w-0 flex-col gap-2 p-3"
                  data-model-routing-dimension={index}
                >
                  <div className="flex min-w-0 flex-wrap items-end gap-2">
                    <Field
                      className="min-w-0 flex-1"
                      label={t("modelRoutingGroup.catalogDimensionName")}
                    >
                      <Input
                        value={dimension.name}
                        aria-label={`${t(
                          "modelRoutingGroup.catalogDimensionName",
                        )} ${index + 1}`}
                        aria-invalid={
                          dimensionIssue(blocking, index, "name") !== undefined
                        }
                        onChange={(event) =>
                          patch(
                            draft.map((entry, current) =>
                              current === index
                                ? { ...entry, name: event.target.value }
                                : entry,
                            ),
                          )
                        }
                      />
                    </Field>
                    <Field
                      className="min-w-0 flex-1"
                      label={t("modelRoutingGroup.catalogDimensionDescription")}
                    >
                      <Input
                        value={dimension.description ?? ""}
                        aria-label={`${t(
                          "modelRoutingGroup.catalogDimensionDescription",
                        )} ${index + 1}`}
                        onChange={(event) =>
                          patch(
                            draft.map((entry, current) =>
                              current === index
                                ? { ...entry, description: event.target.value }
                                : entry,
                            ),
                          )
                        }
                      />
                    </Field>
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      aria-label={t(
                        "modelRoutingGroup.catalogDimensionRemove",
                        { name: dimension.name },
                      )}
                      onClick={() =>
                        patch(draft.filter((_, current) => current !== index))
                      }
                    >
                      {t("modelRoutingGroup.dimensionRemove")}
                    </Button>
                  </div>
                  <DimensionIssue issue={dimensionIssue(blocking, index, "name")} />
                  {/* 占位符按字面量拼，不经过 i18n：{{dim.x}} 的 {{ }} 会被
                      当成插值语法解析。 */}
                  <p className="text-xs text-muted-foreground">
                    {t("modelRoutingGroup.catalogDimensionPlaceholder")}{" "}
                    <code className="rounded-sm bg-muted px-1 py-0.5 font-mono">
                      {"{{dim."}
                      {dimension.name === "" ? "…" : dimension.name}
                      {"}}"}
                    </code>
                  </p>
                  <span className="text-xs font-medium text-text-secondary">
                    {t("modelRoutingGroup.catalogDimensionOptions")}
                  </span>
                  {dimension.options.map((option, optionIndex) => (
                    <div
                      key={optionIndex}
                      className="flex min-w-0 flex-wrap items-center gap-2"
                    >
                      <Input
                        className="min-w-0 flex-1"
                        value={option.name}
                        aria-label={`${t(
                          "modelRoutingGroup.catalogDimensionOptionName",
                        )} ${index + 1}.${optionIndex + 1}`}
                        aria-invalid={
                          dimensionIssue(
                            blocking,
                            index,
                            "optionName",
                            optionIndex,
                          ) !== undefined
                        }
                        onChange={(event) =>
                          patch(
                            draft.map((entry, current) =>
                              current === index
                                ? {
                                    ...entry,
                                    options: entry.options.map(
                                      (existing, position) =>
                                        position === optionIndex
                                          ? {
                                              ...existing,
                                              name: event.target.value,
                                            }
                                          : existing,
                                    ),
                                  }
                                : entry,
                            ),
                          )
                        }
                      />
                      <Input
                        className="min-w-0 flex-1"
                        value={option.description ?? ""}
                        aria-label={`${t(
                          "modelRoutingGroup.catalogDimensionOptionDescription",
                        )} ${index + 1}.${optionIndex + 1}`}
                        onChange={(event) =>
                          patch(
                            draft.map((entry, current) =>
                              current === index
                                ? {
                                    ...entry,
                                    options: entry.options.map(
                                      (existing, position) =>
                                        position === optionIndex
                                          ? {
                                              ...existing,
                                              description: event.target.value,
                                            }
                                          : existing,
                                    ),
                                  }
                                : entry,
                            ),
                          )
                        }
                      />
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        aria-label={t(
                          "modelRoutingGroup.catalogDimensionRemoveOption",
                          { name: option.name },
                        )}
                        onClick={() =>
                          patch(
                            draft.map((entry, current) =>
                              current === index
                                ? {
                                    ...entry,
                                    options: entry.options.filter(
                                      (_, position) => position !== optionIndex,
                                    ),
                                  }
                                : entry,
                            ),
                          )
                        }
                      >
                        {t("modelRoutingGroup.dimensionRemove")}
                      </Button>
                      <DimensionIssue
                        issue={dimensionIssue(
                          blocking,
                          index,
                          "optionName",
                          optionIndex,
                        )}
                      />
                    </div>
                  ))}
                  <DimensionIssue
                    issue={dimensionIssue(blocking, index, "options")}
                  />
                  <div className="flex min-w-0 flex-wrap items-center gap-2">
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      onClick={() =>
                        patch(
                          draft.map((entry, current) =>
                            current === index
                              ? {
                                  ...entry,
                                  options: [
                                    ...entry.options,
                                    { name: "", description: "" },
                                  ],
                                }
                              : entry,
                          ),
                        )
                      }
                    >
                      {t("modelRoutingGroup.catalogDimensionAddOption")}
                    </Button>
                  </div>
                </Panel>
              ))}
              <div className="flex min-w-0 flex-wrap items-center gap-2">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={atCapacity}
                  title={
                    atCapacity
                      ? t("modelRoutingGroup.catalogDimensionLimitReached", {
                          max: classifierMaxCustomDimensions,
                        })
                      : undefined
                  }
                  onClick={() => patch([...draft, newDimension()])}
                >
                  {t("modelRoutingGroup.catalogDimensionAdd")}
                </Button>
                <span className="text-xs text-muted-foreground">
                  {t("modelRoutingGroup.catalogDimensionLimitHint", {
                    max: classifierMaxCustomDimensions,
                  })}
                </span>
              </div>
              <DimensionIssue
                issue={dimensionIssue(
                  blocking,
                  classifierMaxCustomDimensions,
                  "name",
                )}
              />
            </>
          )}
        </div>
      </Panel>
      <Panel className="flex min-h-0 shrink-0 flex-col">
        <PanelHeader>
          <h3 className="text-sm font-semibold">
            {t("modelRoutingGroup.catalogLabelsTitle")}
          </h3>
        </PanelHeader>
        <div className="flex flex-wrap gap-1.5 px-4 pb-3">
          {labelDomain.map((label) => (
            <span
              className="rounded-sm bg-muted px-1.5 py-0.5 text-xs"
              key={label}
            >
              {t(`modelRouting.values.task_type.${label}`)}
            </span>
          ))}
        </div>
      </Panel>
      </fieldset>
    </div>
  );
}
