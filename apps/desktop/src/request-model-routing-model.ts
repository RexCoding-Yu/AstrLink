import { i18n } from "./i18n";
import {
  isModelRoutingLeaf,
  isModelRoutingRange,
  isModelRoutingTime,
  modelRoutingConditionOperator,
  modelRoutingFieldSource,
  type ModelRoutingCondition,
  type ModelRoutingField,
} from "./model-routing-group-model";
import type {
  ModelRoutingDecision,
  ModelRoutingExecutionState,
  ModelRoutingFieldObservation,
  ModelRoutingPrivacyDecision,
  ModelRoutingSelectionSource,
} from "./request-record-model";

/**
 * 请求详情「模型路由」页签的纯展示映射。
 *
 * 与 `request-routing-model.ts` 分开：那个文件解释 provider selection
 * （`RequestRoutingDecision`），这里解释一次模型组求值（`ModelRoutingDecision`）。
 * 两者是独立命名空间，连原因枚举都不共享。
 *
 * 这些函数只读 decision 里已经受限的字段 —— 从不接触 prompt、媒体 URL、
 * 命中关键词或 classifier 原文，因为那些内容根本不进 V1 payload。
 */

/** 未知 code 原样回显，便于诊断新版本 core 写出的新值。 */
function text(key: string, fallback: string): string {
  return i18n.exists(key) ? i18n.t(key) : fallback;
}

export function modelRoutingSelectionSourceLabel(
  source: ModelRoutingSelectionSource,
): string {
  return text(`modelRouting.selectionSource.${source}`, source);
}

export function modelRoutingExecutionStateLabel(
  state: ModelRoutingExecutionState,
): string {
  return text(`modelRouting.execution.state.${state}`, state);
}

export function modelRoutingPrivacyDecisionLabel(
  decision: ModelRoutingPrivacyDecision,
): string {
  return text(`modelRouting.privacy.decision.${decision}`, decision);
}

export function modelRoutingFieldLabel(field: string): string {
  return text(`modelRouting.fields.${field}`, field);
}

/**
 * 候选取值的中文/英文名，例如 `coding` → 编码。取值域是开放词汇的字段
 * （protocol、language）没有词条，回退为 code 本身。
 */
export function modelRoutingFieldValueLabel(
  field: string,
  value: string,
): string {
  return text(`modelRouting.values.${field}.${value}`, value);
}

export function modelRoutingFieldSourceLabel(source: string): string {
  return text(`modelRouting.fieldSource.${source}`, source);
}

/** 决策记录里的稳定错误码；classifier 的 fail-open code 也走这里。 */
export function modelRoutingCodeLabel(code: string): string {
  return text(`modelRouting.codes.${code}`, code);
}

/**
 * 一条规则的摘要：命中哪条规则、优先级多少，或使用了默认成员。
 * `used_default` 是权威位；`rule_id` 只在 `selection_source=rule` 时存在。
 */
export function modelRoutingRuleSummary(
  decision: ModelRoutingDecision,
): string {
  if (decision.used_default || decision.selection_source !== "rule")
    return i18n.t("modelRouting.rule.default");
  const name = decision.rule_id ?? "";
  return decision.rule_priority === undefined
    ? i18n.t("modelRouting.rule.hit", { rule: name })
    : i18n.t("modelRouting.rule.hitWithPriority", {
        rule: name,
        priority: decision.rule_priority,
      });
}

/**
 * 字段观测的排序：已知优先，其次按字段名，让读者先看到真正参与命中的依据。
 */
export function sortedFieldObservations(
  decision: ModelRoutingDecision,
): ModelRoutingFieldObservation[] {
  return [...decision.fields].sort((left, right) => {
    if (left.known !== right.known) return left.known ? -1 : 1;
    return left.field.localeCompare(right.field);
  });
}

/**
 * 字段没有给出值时的原因：分类器故障还是本地方案取不到，两者对读者意义不同。
 */
export function modelRoutingFieldSkipLabel(
  observation: ModelRoutingFieldObservation,
): string | null {
  if (observation.skip_reason)
    return text(
      `modelRouting.skipReason.${observation.skip_reason}`,
      observation.skip_reason,
    );
  // 依赖分类器的字段在未知时按「分类器未给出结果」解释，其余按本地解析失败解释。
  const field = observation.field as ModelRoutingField;
  return i18n.t(
    modelRoutingFieldSource(field) === "classifier"
      ? "modelRouting.skipReason.classifier_unknown"
      : "modelRouting.skipReason.local_unknown",
  );
}

/** 条件叶子的可读文本；只用于编辑器回显，不含匹配到的原文。 */
export function modelRoutingConditionSummary(
  condition: ModelRoutingCondition,
): string {
  if ("all" in condition)
    return i18n.t("modelRouting.condition.all", {
      count: condition.all?.length ?? 0,
    });
  if ("any" in condition)
    return i18n.t("modelRouting.condition.any", {
      count: condition.any?.length ?? 0,
    });
  if ("not" in condition) return i18n.t("modelRouting.condition.not");
  if (!isModelRoutingLeaf(condition)) return "";
  const field = modelRoutingFieldLabel(condition.field);
  if (isModelRoutingTime(condition)) {
    const within = condition.within ?? [];
    if (within.length !== 2)
      return i18n.t("modelRouting.condition.timeWindowEmpty", { field });
    return i18n.t("modelRouting.condition.timeWindow", {
      field,
      start: within[0],
      end: within[1],
    });
  }
  if (isModelRoutingRange(condition)) {
    // 下界与上界各自可有可无，两侧同时存在时用区间文案。
    const lower = rangeBoundLabel(condition.gt, condition.gte);
    const upper = rangeBoundLabel(condition.lt, condition.lte);
    if (lower !== null && upper !== null)
      return i18n.t("modelRouting.condition.range", {
        field,
        min: lower,
        max: upper,
      });
    if (lower !== null)
      return i18n.t("modelRouting.condition.atLeast", { field, min: lower });
    return i18n.t("modelRouting.condition.atMost", {
      field,
      max: upper ?? 0,
    });
  }
  const operator = modelRoutingConditionOperator(condition);
  if (operator === "equals_bool")
    return i18n.t(
      condition.equals_bool
        ? "modelRouting.condition.isTrue"
        : "modelRouting.condition.isFalse",
      { field },
    );
  if (operator === "equals")
    return i18n.t("modelRouting.condition.equals", {
      field,
      value: condition.equals ?? "",
    });
  if (operator === "not_equals")
    return i18n.t("modelRouting.condition.notEquals", {
      field,
      value: condition.not_equals ?? "",
    });
  const list =
    condition.in ??
    condition.not_in ??
    condition.contains_any ??
    condition.not_contains_any ??
    [];
  const key =
    operator === "contains_any"
      ? "modelRouting.condition.containsAny"
      : operator === "not_contains_any"
        ? "modelRouting.condition.notContainsAny"
        : operator === "not_in"
          ? "modelRouting.condition.notOneOf"
          : "modelRouting.condition.oneOf";
  return i18n.t(key, { field, count: list.length, values: list.join(", ") });
}

/** 比较下界/上界的显示文本：严格比较带符号，缺失返回 null。 */
function rangeBoundLabel(
  strict: number | undefined,
  inclusive: number | undefined,
): string | null {
  if (inclusive !== undefined) return String(inclusive);
  if (strict !== undefined) return `>${strict}`;
  return null;
}
