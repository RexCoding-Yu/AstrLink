import { useEffect, useRef, useState } from "react";

import { Field } from "./components/Field";
import { FormMessage } from "./components/FormMessage";
import { HelpPopover } from "./components/HelpPopover";
import { IconButton } from "./components/IconButton";
import { Plus, X } from "./components/icons";
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
  updateRoutingSettings,
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
import { intelligentRoutingErrorText } from "./intelligent-routing-errors";
import {
  calculateTaskTypeLabelIssues,
  modelRoutingCatalog,
  modelRoutingCatalogLabelDomain,
  modelRoutingTaskTypeLabelsDiverge,
} from "./model-routing-catalog-model";
import { maxModelRoutingListItems } from "./model-routing-group-model";
import {
  modelRoutingFieldLabel,
  modelRoutingFieldValueLabel,
} from "./request-model-routing-model";
import { ModelRoutingTaskTypeDimensionCard } from "./ModelRoutingTaskTypeDimensionCard";
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
 * 一个自定义维度的编辑卡片。
 *
 * 布局：头部一行是维度名、说明与移除；下面是取值表，两列表头「取值 / 取值
 * 说明」直接告诉用户每个输入框填什么——原来两个并排的空输入框既无表头也无
 * 占位文字，用户看不出右边那格是干什么的。新维度默认只给一行取值。
 */
function DimensionCard({
  dimension,
  index,
  issues,
  onChange,
  onRemove,
}: {
  dimension: ClassifierDimension;
  index: number;
  issues: readonly ClassifierDimensionIssue[];
  onChange: (next: ClassifierDimension) => void;
  onRemove: () => void;
}) {
  const t = useT();
  const nameIssue = dimensionIssue(issues, index, "name");
  const setOption = (
    optionIndex: number,
    patch: Partial<ClassifierDimension["options"][number]>,
  ) =>
    onChange({
      ...dimension,
      options: dimension.options.map((existing, position) =>
        position === optionIndex ? { ...existing, ...patch } : existing,
      ),
    });
  return (
    <div
      className="grid min-w-0 gap-3 rounded-md border bg-background p-3"
      data-model-routing-dimension={index}
    >
      <div className="flex min-w-0 flex-wrap items-start gap-3">
        <Field
          className="w-56 min-w-0 shrink-0"
          label={t("modelRoutingGroup.catalogDimensionName")}
          hint={
            // 占位符按字面量拼，不经过 i18n：{{dim.x}} 的 {{ }} 会被当成插值。
            <span className="flex min-w-0 flex-wrap items-center gap-1">
              {t("modelRoutingGroup.catalogDimensionPlaceholder")}
              <code className="rounded-sm bg-muted px-1 py-0.5 font-mono text-text-secondary">
                {"{{dim."}
                {dimension.name === "" ? "…" : dimension.name}
                {"}}"}
              </code>
            </span>
          }
        >
          <Input
            className="h-8 font-mono"
            value={dimension.name}
            placeholder={t("modelRoutingGroup.catalogDimensionNamePlaceholder")}
            aria-label={`${t("modelRoutingGroup.catalogDimensionName")} ${index + 1}`}
            aria-invalid={nameIssue !== undefined}
            onChange={(event) =>
              onChange({ ...dimension, name: event.target.value })
            }
          />
        </Field>
        <Field
          className="min-w-[14rem] flex-1"
          label={t("modelRoutingGroup.catalogDimensionDescription")}
        >
          <Input
            className="h-8"
            value={dimension.description ?? ""}
            placeholder={t(
              "modelRoutingGroup.catalogDimensionDescriptionPlaceholder",
            )}
            aria-label={`${t("modelRoutingGroup.catalogDimensionDescription")} ${index + 1}`}
            onChange={(event) =>
              onChange({ ...dimension, description: event.target.value })
            }
          />
        </Field>
        {/* 与输入框对齐：跳过标签行的高度。 */}
        <div className="flex shrink-0 flex-col gap-1.5">
          <span aria-hidden="true" className="h-4" />
          <IconButton
            type="button"
            className="text-muted-foreground hover:text-destructive"
            label={t("modelRoutingGroup.catalogDimensionRemove", {
              name: dimension.name,
            })}
            onClick={onRemove}
          >
            <X aria-hidden="true" />
          </IconButton>
        </div>
      </div>
      <DimensionIssue issue={nameIssue} />

      <div className="grid min-w-0 gap-1.5">
        <span className="text-xs font-medium text-text-secondary">
          {t("modelRoutingGroup.catalogDimensionOptions")}
        </span>
        <div className="min-w-0 overflow-hidden rounded-md border">
          <div className="grid grid-cols-[minmax(0,14rem)_minmax(0,1fr)_2rem] gap-2 border-b bg-muted/60 px-2 py-1.5 text-xs font-medium text-muted-foreground">
            <span>{t("modelRoutingGroup.catalogDimensionOptionName")}</span>
            <span>
              {t("modelRoutingGroup.catalogDimensionOptionDescription")}
            </span>
            <span className="sr-only">{t("modelRoutingGroup.rowActions")}</span>
          </div>
          {dimension.options.map((option, optionIndex) => {
            const optionIssue = dimensionIssue(
              issues,
              index,
              "optionName",
              optionIndex,
            );
            return (
              <div
                key={optionIndex}
                className="grid grid-cols-[minmax(0,14rem)_minmax(0,1fr)_2rem] items-start gap-2 border-b px-2 py-1.5 last:border-b-0"
                data-model-routing-dimension-option={optionIndex}
              >
                <div className="grid min-w-0 gap-1">
                  <Input
                    className="h-8 font-mono"
                    value={option.name}
                    placeholder={t(
                      "modelRoutingGroup.catalogDimensionOptionNamePlaceholder",
                    )}
                    aria-label={`${t("modelRoutingGroup.catalogDimensionOptionName")} ${index + 1}.${optionIndex + 1}`}
                    aria-invalid={optionIssue !== undefined}
                    onChange={(event) =>
                      setOption(optionIndex, { name: event.target.value })
                    }
                  />
                  <DimensionIssue issue={optionIssue} />
                </div>
                <Input
                  className="h-8"
                  value={option.description ?? ""}
                  placeholder={t(
                    "modelRoutingGroup.catalogDimensionOptionDescriptionPlaceholder",
                  )}
                  aria-label={`${t("modelRoutingGroup.catalogDimensionOptionDescription")} ${index + 1}.${optionIndex + 1}`}
                  onChange={(event) =>
                    setOption(optionIndex, { description: event.target.value })
                  }
                />
                <IconButton
                  type="button"
                  className="text-muted-foreground hover:text-destructive"
                  label={t("modelRoutingGroup.catalogDimensionRemoveOption", {
                    name: option.name,
                  })}
                  onClick={() =>
                    onChange({
                      ...dimension,
                      options: dimension.options.filter(
                        (_, position) => position !== optionIndex,
                      ),
                    })
                  }
                >
                  <X aria-hidden="true" />
                </IconButton>
              </div>
            );
          })}
          <div className="bg-muted/30 px-2 py-1.5">
            <Button
              type="button"
              variant="ghost"
              size="xs"
              className="text-text-secondary"
              disabled={dimension.options.length >= maxModelRoutingListItems}
              onClick={() =>
                onChange({
                  ...dimension,
                  options: [
                    ...dimension.options,
                    { name: "", description: "" },
                  ],
                })
              }
            >
              <Plus aria-hidden="true" />
              {t("modelRoutingGroup.catalogDimensionAddOption")}
            </Button>
          </div>
        </div>
        <DimensionIssue issue={dimensionIssue(issues, index, "options")} />
      </div>
    </div>
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
  protocols = [],
  onDirtyChange = () => {},
}: {
  ready: boolean;
  /** core capabilities' protocol IDs, so `protocol`'s value domain is listed. */
  protocols?: readonly string[];
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
  // The task_type label list is the first dimension's options, so it is held
  // as a second draft inside the same form: one Save, one dirty flag, one
  // navigation warning. It still writes to a different resource — see save().
  const [labels, setLabels] = useState<string[] | null>(null);
  const [labelBaseline, setLabelBaseline] = useState("");
  const [loadError, setLoadError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [savedOnce, setSavedOnce] = useState(false);
  const [showIssues, setShowIssues] = useState(false);
  const [reload, setReload] = useState(0);
  const draftRef = useRef(draft);
  draftRef.current = draft;
  const dimensionsDirty =
    draft !== null && JSON.stringify(draft) !== baseline && baseline !== "";
  const labelsDirty =
    labels !== null &&
    JSON.stringify(labels) !== labelBaseline &&
    labelBaseline !== "";
  const dirty = dimensionsDirty || labelsDirty;
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
          // The label domain is derived from the groups, which just arrived in
          // the same load. Everything empty means the built-in default domain.
          const domain = modelRoutingCatalogLabelDomain(
            (loadedRouting.model_routing_groups ?? []).map(
              (group) => group.task_type_labels,
            ),
          );
          setLabels([...domain]);
          setLabelBaseline(JSON.stringify(domain));
        }
        setLoadError(null);
      })
      .catch((caught: unknown) => {
        if (active)
          setLoadError(
            intelligentRoutingErrorText(caught, t("failure.loadFailed")),
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

  /**
   * One Save for a form whose two halves live in two different resources.
   *
   * The dimension list is a global resource (`ClassifierSettings.dimensions`);
   * the task_type label list is per-group (`RoutingSettings.model_routing_groups[].task_type_labels`),
   * edited here as one shared list written to every group. Asking the user to
   * press two buttons for one visible block would be a UI detail leaking the
   * storage layout, so both are written under one action — dimensions first,
   * labels second, because a failure there leaves the guard's dirty flags
   * intact for the half that did not land.
   */
  const save = (): void => {
    if (draft === null || document === null || !ready || saving) return;
    if (!dirty) return;
    // Both halves are checked before either is written: the label list is what
    // rules reference, so dropping a label a saved rule still uses would be
    // rejected by Core's own validation and fail the whole document. Catching
    // it here keeps the error next to the row that caused it.
    if (issues.length > 0 || labelIssues.length > 0) {
      setShowIssues(true);
      return;
    }
    const submitted = draft;
    const submittedLabels = labels ?? [];
    const writeDimensions = dimensionsDirty;
    const writeLabels = labelsDirty;
    setError(null);
    setSaving(true);
    // The two writes are independent resources, so they run together: a slow
    // classifier singleton must not serialize behind a routing-settings write.
    void Promise.all([
      writeDimensions
        ? readClassifierSettings().then((latest) =>
            // Read-modify-write, not a patch of the document loaded at mount:
            // the classifier tab edits the same singleton, so writing back a
            // stale copy would silently revert an endpoint or prompt the user
            // saved there while this tab was open. Only `dimensions` is this
            // panel's to set.
            updateClassifierSettings({ ...latest, dimensions: submitted }).then(
              (result) => ({ result, latest }),
            ),
          )
        : Promise.resolve(null),
      writeLabels
        ? // Same read-modify-write discipline: only `task_type_labels` is this
          // panel's to set on each group, everything else comes back verbatim
          // from the server's current document.
          getRoutingSettings().then((latest) =>
            updateRoutingSettings({
              model_routing_groups: (latest.model_routing_groups ?? []).map(
                (group) => ({
                  ...group,
                  task_type_labels: [...submittedLabels],
                }),
              ),
            }),
          )
        : Promise.resolve(null),
    ])
      .then(([dimensionResult, routingResult]) => {
        if (dimensionResult !== null) {
          const saved = dimensionResult.result.dimensions ?? [];
          setDocument({ ...dimensionResult.latest, ...dimensionResult.result });
          setSavedDimensions(saved);
          setBaseline(JSON.stringify(saved));
          setDraft((current) => (current === submitted ? saved : current));
        }
        if (routingResult !== null) {
          setSettings(routingResult);
          setLabelBaseline(JSON.stringify(submittedLabels));
          setLabels((current) =>
            JSON.stringify(current) === JSON.stringify(submittedLabels)
              ? [...submittedLabels]
              : current,
          );
        }
        setSavedOnce(true);
      })
      .catch((caught: unknown) => {
        if (draftRef.current === submitted)
          setError(
            intelligentRoutingErrorText(caught, t("classifier.saveFailed")),
          );
      })
      .finally(() => setSaving(false));
  };

  const groups = settings?.model_routing_groups ?? [];
  // The label list is a draft, so the issues and the divergence notice read the
  // draft rather than the saved domain: a user mid-edit should see the problem
  // with what they typed, not with what is on the server.
  const labelIssues =
    labels === null ? [] : calculateTaskTypeLabelIssues(labels, groups);
  const labelsDiverge = modelRoutingTaskTypeLabelsDiverge(
    groups.map((group) => group.task_type_labels),
  );
  // 自定义维度也进目录：它和内建字段一样是规则可以引用的条件字段，只是来源
  // 标注为「分类器」。传入定义（而不只是名字）后，取值域列会列出它自己的选项。
  const definitions = draft ?? savedDimensions;
  const entries = modelRoutingCatalog(
    definitions.map((dimension) => dimension.name),
    definitions,
    protocols,
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
          data-model-routing-catalog-save
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
                      {/* 自定义维度没有 i18n 词条：回退显示名字本身，不吐出原始 key。 */}
                      {modelRoutingFieldLabel(entry.field)}
                      {modelRoutingFieldLabel(entry.field) !== entry.field ? (
                        <span className="ml-1.5 font-mono text-muted-foreground">
                          {entry.field}
                        </span>
                      ) : null}
                    </TableCell>
                    <TableCell className="px-4">
                      {t(`modelRoutingGroup.fieldKind.${entry.kind}`)}
                      {entry.values ? (
                        <span className="ml-1.5 text-muted-foreground">
                          {entry.values
                            .map((value) =>
                              modelRoutingFieldValueLabel(entry.field, value),
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
                {/*
                task_type leads the list because it is the dimension every group
                already has: the classifier always answers it and the built-in
                rule field reads it. It was a separate panel before, which made
                one concept live in two places.
              */}
                <ModelRoutingTaskTypeDimensionCard
                  labels={labels ?? []}
                  groups={groups.length}
                  issues={labelIssues}
                  disabled={!ready || saving}
                  diverge={labelsDiverge && !labelsDirty}
                  onChange={(next) => {
                    setShowIssues(false);
                    setError(null);
                    setLabels(next);
                  }}
                />
                {draft.length === 0 ? (
                  <p className="text-xs text-muted-foreground">
                    {t("modelRoutingGroup.catalogDimensionEmpty")}
                  </p>
                ) : null}
                {draft.map((dimension, index) => (
                  <DimensionCard
                    key={index}
                    dimension={dimension}
                    index={index}
                    issues={blocking}
                    onChange={(next) =>
                      patch(
                        draft.map((entry, current) =>
                          current === index ? next : entry,
                        ),
                      )
                    }
                    onRemove={() =>
                      patch(draft.filter((_, current) => current !== index))
                    }
                  />
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
      </fieldset>
    </div>
  );
}
