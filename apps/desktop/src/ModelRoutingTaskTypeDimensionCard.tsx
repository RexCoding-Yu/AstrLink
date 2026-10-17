import { useState } from "react";

import { Plus, X } from "@/components/icons";
import { Field } from "@/components/Field";
import { Button } from "@/components/ui/button";
import { IconButton } from "@/components/IconButton";
import { Input } from "@/components/ui/input";
import { useT } from "@/i18n";
import { modelRoutingFieldValueLabel } from "@/request-model-routing-model";
import {
  maxModelRoutingTaskTypeLabelRunes,
  type TaskTypeLabelIssue,
} from "@/model-routing-catalog-model";
import { maxModelRoutingTaskTypeLabels } from "@/model-routing-group-model";

/**
 * `task_type` as the first entry of the custom classification dimension list.
 *
 * It is a dimension like any other — the classifier answers it, rules read it,
 * the field catalog lists it — so giving it its own separate panel asked the
 * user to learn two places for one concept. The differences that *are* real are
 * visible rather than structural:
 *
 *   - the name is fixed, because `task_type` is built into the wire contract
 *     and core rejects it as a custom dimension name;
 *   - it cannot be removed, for the same reason;
 *   - its options carry no per-value description, because the wire shape
 *     (`task_type_labels`) is a bare list of labels.
 *
 * Everything else — add, remove, capacity, the issues — behaves exactly like
 * the custom rows below it.
 */
export function ModelRoutingTaskTypeDimensionCard({
  labels,
  groups,
  issues,
  disabled,
  diverge,
  onChange,
}: {
  labels: readonly string[];
  /** Group count; the label domain is written to every group, so zero groups disables editing. */
  groups: number;
  issues: readonly TaskTypeLabelIssue[];
  disabled: boolean;
  /** The groups currently disagree about their label list, and this is not a draft. */
  diverge: boolean;
  onChange: (next: string[]) => void;
}) {
  const t = useT();
  const [input, setInput] = useState("");
  const atCapacity = labels.length >= maxModelRoutingTaskTypeLabels;
  const add = () => {
    const label = input.trim();
    if (label === "" || labels.includes(label) || atCapacity) return;
    onChange([...labels, label]);
    setInput("");
  };
  const issueText = (issue: TaskTypeLabelIssue) =>
    t(`modelRoutingGroup.taskTypeLabelErrors.${issue.problem}`, {
      label: issue.label ?? "",
      group: issue.group ?? "",
      max: maxModelRoutingTaskTypeLabels,
    });

  return (
    <div
      className="grid min-w-0 gap-3 rounded-md border bg-background p-3"
      data-model-routing-dimension="task_type"
    >
      <div className="flex min-w-0 flex-wrap items-start gap-3">
        <Field
          className="w-56 min-w-0 shrink-0"
          label={t("modelRoutingGroup.catalogDimensionName")}
          hint={
            <span className="flex min-w-0 flex-wrap items-center gap-1">
              {t("modelRoutingGroup.catalogDimensionPlaceholder")}
              <code className="rounded-sm bg-muted px-1 py-0.5 font-mono text-text-secondary">
                {"{{dim.task_type}}"}
              </code>
            </span>
          }
        >
          {/* Read-only, not disabled: a disabled input greys out and reads as
              "temporarily unavailable", while the name is permanently fixed. */}
          <Input
            className="h-8 font-mono"
            value="task_type"
            readOnly
            aria-label={t("modelRoutingGroup.catalogDimensionName")}
          />
        </Field>
        <Field
          className="min-w-[14rem] flex-1"
          label={t("modelRoutingGroup.catalogDimensionDescription")}
        >
          <Input
            className="h-8"
            value={t("modelRoutingGroup.catalogBuiltInDimensionDescription")}
            readOnly
            aria-label={t("modelRoutingGroup.catalogDimensionDescription")}
          />
        </Field>
      </div>

      {diverge ? (
        <span className="text-xs text-muted-foreground">
          {t("modelRoutingGroup.catalogLabelsDiverge")}
        </span>
      ) : null}

      <div className="grid min-w-0 gap-1.5">
        <span className="text-xs font-medium text-text-secondary">
          {t("modelRoutingGroup.catalogDimensionOptions")}
        </span>
        <div className="grid min-w-0 gap-1.5" data-task-type-labels>
          {labels.map((label) => (
            <div
              key={label}
              className="flex min-w-0 items-center gap-2 rounded-md border bg-muted/40 px-2 py-1.5"
              data-task-type-label={label}
            >
              <span className="min-w-0 flex-1 text-xs">
                {modelRoutingFieldValueLabel("task_type", label)}
                {modelRoutingFieldValueLabel("task_type", label) !== label ? (
                  <span className="ml-1.5 font-mono text-muted-foreground">
                    {label}
                  </span>
                ) : null}
              </span>
              <IconButton
                type="button"
                size="icon-xs"
                className="text-muted-foreground hover:text-destructive"
                label={t("modelRoutingGroup.catalogLabelRemove", { label })}
                disabled={disabled || groups === 0}
                onClick={() =>
                  onChange(labels.filter((item) => item !== label))
                }
              >
                <X aria-hidden="true" />
              </IconButton>
            </div>
          ))}
          {/* 不用 <form>：页签可能被嵌进别的表单，回车由输入框自己处理。 */}
          <div className="flex items-center gap-1.5">
            <Input
              className="h-7 w-40 text-xs"
              value={input}
              maxLength={maxModelRoutingTaskTypeLabelRunes}
              disabled={disabled || groups === 0 || atCapacity}
              placeholder={t("modelRoutingGroup.catalogLabelAddPlaceholder")}
              aria-label={t("modelRoutingGroup.catalogLabelAdd")}
              onChange={(event) => setInput(event.target.value)}
              onKeyDown={(event) => {
                if (event.key !== "Enter" || event.nativeEvent.isComposing)
                  return;
                event.preventDefault();
                add();
              }}
            />
            <Button
              type="button"
              onClick={add}
              variant="outline"
              size="xs"
              disabled={
                disabled ||
                groups === 0 ||
                atCapacity ||
                input.trim() === "" ||
                labels.includes(input.trim())
              }
            >
              <Plus aria-hidden="true" />
              {t("modelRoutingGroup.catalogLabelAdd")}
            </Button>
          </div>
        </div>
        <span className="text-xs text-muted-foreground">
          {t("modelRoutingGroup.catalogTaskTypeLabelLimitHint", {
            max: maxModelRoutingTaskTypeLabels,
          })}
        </span>
        {groups === 0 ? (
          <span className="text-xs text-muted-foreground">
            {t("modelRoutingGroup.catalogLabelsNoGroups")}
          </span>
        ) : null}
        {issues.map((issue, index) => (
          <span
            key={index}
            role="alert"
            className="text-xs text-danger-foreground"
          >
            {issueText(issue)}
          </span>
        ))}
      </div>
    </div>
  );
}
