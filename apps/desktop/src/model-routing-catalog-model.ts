/**
 * 「字段类型」页签（第四页签）的只读视图模型。
 *
 * 与 `./model-groups.ts` 无关：那个文件是把模型名按厂商分桶的列表浏览助手。
 * 这里描述的是**规则条件可用的元信息字段目录** —— 内建字段 ID、类型、来源与
 * 各字段的合法取值域，供用户在写规则前查。
 *
 * 权威定义在 `./model-routing-group-model.ts` 的操作符矩阵里（它镜像 Go）。
 * 本文件**不新增第三份枚举**：所有字段、来源、取值域都从那里派生，避免目录与
 * 编辑器校验漂移。目录本身是只读的：全局标签的权威编辑资源在后端尚未落地
 * （见交付说明），因此这里只呈现当前生效的值。
 */

import {
  fieldOperators,
  isModelRoutingRangeField,
  isModelRoutingTimeField,
  modelRoutingFieldSource,
  modelRoutingFieldValues,
  modelRoutingFields,
  maxModelRoutingTaskTypeLabels,
  type ClassifierDimension,
  type ModelRoutingCondition,
  type ModelRoutingField,
  type ModelRoutingFieldSource,
  type ModelRoutingGroup,
  type ModelRoutingValueCondition,
} from "./model-routing-group-model";

/**
 * 字段的值形状。它决定编辑器给哪种控件，也是「字段类型」页签真正要展示的
 * 那一列 —— 用户看 §8.3 的说明前，先要知道这个字段是标签、数值还是时间。
 */
export type ModelRoutingFieldKind = "label" | "number" | "boolean" | "time";

/**
 * 判定顺序与 Go 的比较点一致：时间字段既不是区间也不是列表，必须**先判时间**，
 * 否则 `within` 会被 `isModelRoutingRangeField` 的 `gte`/`lte` 分支吃掉。
 */
export function modelRoutingFieldKind(
  field: ModelRoutingField,
): ModelRoutingFieldKind {
  if (isModelRoutingTimeField(field)) return "time";
  if (isModelRoutingRangeField(field)) return "number";
  const operators = fieldOperators(field);
  return operators.includes("equals_bool") ? "boolean" : "label";
}

export interface ModelRoutingCatalogEntry {
  field: ModelRoutingField;
  kind: ModelRoutingFieldKind;
  source: ModelRoutingFieldSource;
  /** 该字段允许的操作符，顺序取自操作符矩阵。 */
  operators: readonly string[];
  /** 固定取值域；开放词汇字段为 `undefined`。 */
  values: readonly string[] | undefined;
}

/** 目录的全部内建字段，顺序稳定（矩阵声明序），不依赖对象遍历以外的因素。 */
export function modelRoutingCatalog(
  extraFields: readonly string[] = [],
  dimensions?: readonly ClassifierDimension[],
  /**
   * `protocol` 的取值域来自 core capabilities，与规则编辑器拿到的是同一份。
   * 省略时该字段列不出候选值——目录只呈现已经知道的东西，不猜一个协议列表。
   */
  protocols?: readonly string[],
): readonly ModelRoutingCatalogEntry[] {
  const builtin = modelRoutingFields.map((field) => ({
    field,
    kind: modelRoutingFieldKind(field),
    source: modelRoutingFieldSource(field),
    operators: fieldOperators(field),
    values: modelRoutingFieldValues(field, dimensions, protocols),
  }));
  const extra = extraFields.map((field) => ({
    field: field as ModelRoutingField,
    kind: modelRoutingFieldKind(field as ModelRoutingField),
    source: modelRoutingFieldSource(field as ModelRoutingField),
    operators: fieldOperators(field as ModelRoutingField),
    values: modelRoutingFieldValues(
      field as ModelRoutingField,
      dimensions,
      protocols,
    ),
  }));
  return [...builtin, ...extra];
}

/**
 * `task_type` 的**当前生效**标签域：组级 `task_type_labels` 覆盖内建默认。
 * 全局标签资源落地前，这里呈现的是默认域，页签据此标注「全局」尚未生效。
 */
export function modelRoutingCatalogLabelDomain(
  overrides: readonly (readonly string[] | undefined)[],
): readonly string[] {
  for (const labels of overrides) {
    if (labels && labels.length > 0) return labels;
  }
  return modelRoutingFieldValues("task_type") ?? [];
}

/** 标签编辑的一个问题；`label` 为出问题的标签，`group` 为引用它的组。 */
export interface TaskTypeLabelIssue {
  problem: "empty" | "tooLong" | "duplicate" | "tooMany" | "none" | "inUse";
  label?: string;
  group?: string;
}

/** 与 Go `MaxModelRoutingValueRunes` 一致：标签也是一个规则取值。 */
export const maxModelRoutingTaskTypeLabelRunes = 128;

function referencedTaskTypeLabels(condition: ModelRoutingCondition): string[] {
  if ("field" in condition) {
    if (condition.field !== "task_type") return [];
    const value = condition as ModelRoutingValueCondition;
    return [
      ...(value.in ?? []),
      ...(value.not_in ?? []),
      ...(value.equals ? [value.equals] : []),
      ...(value.not_equals ? [value.not_equals] : []),
    ];
  }
  if ("not" in condition) return referencedTaskTypeLabels(condition.not);
  const children =
    "all" in condition ? (condition.all ?? []) : (condition.any ?? []);
  return children.flatMap(referencedTaskTypeLabels);
}

/**
 * 「字段类型」页签上编辑的 task_type 标签集合的预检，镜像 Go 的
 * `validateModelRoutingTaskTypeLabels` 与「规则取值必须在组标签集合里」两条。
 *
 * 第二条是这里特有的：页签把同一份标签写进每个组，删掉某个组的规则仍在用的
 * 标签会让整份路由设置被后端拒收，所以在本地就指出是哪个组在用。
 */
export function calculateTaskTypeLabelIssues(
  labels: readonly string[],
  groups: readonly ModelRoutingGroup[],
): TaskTypeLabelIssue[] {
  const issues: TaskTypeLabelIssue[] = [];
  if (labels.length === 0) issues.push({ problem: "none" });
  if (labels.length > maxModelRoutingTaskTypeLabels)
    issues.push({ problem: "tooMany" });
  const seen = new Set<string>();
  for (const label of labels) {
    if (label.trim() === "") issues.push({ problem: "empty" });
    else if ([...label].length > maxModelRoutingTaskTypeLabelRunes)
      issues.push({ problem: "tooLong", label });
    else if (seen.has(label)) issues.push({ problem: "duplicate", label });
    seen.add(label);
  }
  for (const group of groups) {
    const used = new Set(
      group.rules.flatMap((rule) => referencedTaskTypeLabels(rule.when)),
    );
    for (const label of used)
      if (!seen.has(label))
        issues.push({ problem: "inUse", label, group: group.id });
  }
  return issues;
}

/** 各组是否已经共用同一份标签（未覆盖的组按内建默认域计）。 */
export function modelRoutingTaskTypeLabelsDiverge(
  overrides: readonly (readonly string[] | undefined)[],
): boolean {
  const fallback = modelRoutingFieldValues("task_type") ?? [];
  const keys = new Set(
    overrides.map((labels) =>
      JSON.stringify(labels && labels.length > 0 ? labels : fallback),
    ),
  );
  return keys.size > 1;
}
