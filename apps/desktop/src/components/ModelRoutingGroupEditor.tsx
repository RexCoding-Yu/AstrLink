import {
  createContext,
  useContext,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type RefObject,
} from "react";

import { ConfirmDialog } from "@/components/ConfirmDialog";
import { Field } from "@/components/Field";
import { FormMessage } from "@/components/FormMessage";
import { HelpPopover } from "@/components/HelpPopover";
import { IconButton } from "@/components/IconButton";
import { ModelSelect } from "@/components/ModelSelect";
import { Panel, PanelHeader } from "@/components/Panel";
import { ServiceKindIcon } from "@/components/ServiceKindIcon";
import {
  ChevronDown,
  ChevronUp,
  GripVertical,
  Plus,
  X,
} from "@/components/icons";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { MultiCombobox, type ComboboxOption } from "@/components/ui/combobox";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";
import {
  loadRoutingCustomValues,
  rememberRoutingCustomValue,
  type RoutingCustomValueMap,
} from "@/lib/routing-custom-store";
import {
  emptyModelRoutingGroup,
  isModelRoutingRangeField,
  isModelRoutingTimeField,
  isModelRoutingTimeOfDay,
  modelRoutingPriorityStep,
  modelRoutingTimeStepSeconds,
  modelRoutingTimeWindowLength,
  maxModelRoutingGroups,
  maxModelRoutingKeywords,
  maxModelRoutingListItems,
  maxModelRoutingMembersPerGroup,
  maxModelRoutingRulesPerGroup,
  maxModelRoutingTaskTypeLabels,
  fieldOperators,
  modelRoutingConditionOperator,
  type ModelRoutingDraftIssue,
  modelRoutingFieldValues,
  modelRoutingFields,
  modelRoutingTaskTypes,
  modelRoutingPublicModelID,
  reservedModelRoutingSlugs,
  type ModelRoutingCondition,
  type ModelRoutingField,
  type ModelRoutingGroup,
  type ModelRoutingLeafCondition,
  type ModelRoutingMember,
  type ModelRoutingRangeCondition,
  type ModelRoutingRangeOperator,
  type ModelRoutingRule,
  type ModelRoutingTimeCondition,
  type ModelRoutingValueCondition,
  type ModelRoutingValueOperator,
  type ClassifierDimension,
} from "@/model-routing-group-model";
import {
  modelRoutingFieldLabel,
  modelRoutingFieldValueLabel,
} from "@/request-model-routing-model";
import type { RoutableService } from "@/service-model";
import { useT } from "@/i18n";

/**
 * 用户自建取值的读写入口。叶子控件埋在三层递归下面，逐层透传会让
 * `GroupEditor`/`RuleEditor`/`ConditionNodeEditor` 都多背一个与它们无关的
 * 参数，所以这里用 context 直接下发。
 */
const RoutingCustomValuesContext = createContext<{
  values: RoutingCustomValueMap;
  remember: (field: string, value: string) => void;
}>({ values: {}, remember: () => {} });

const TaskTypeLabelsContext = createContext<{
  labels: readonly string[];
  atCapacity: boolean;
}>({ labels: modelRoutingTaskTypes, atCapacity: false });

const CustomDimensionsContext = createContext<readonly ClassifierDimension[]>(
  [],
);
/**
 * 协议取值域是运行期数据（core capabilities），不是本文件的常量：静态 import
 * `service-presets` 会形成 `service-presets → service-model →
 * failure-policy-model → 本模块` 的环。空数组表示「未提供」，取值列回退为自由输入。
 */
const RoutingProtocolsContext = createContext<readonly string[]>([]);

/**
 * 候选列表 = 内置目录 + 用户自建值。自建值排在后面，且不重复已经存在的项；
 * 开放词汇字段没有内置目录，因此这一列就是它全部的候选。
 */
function routingValueOptions(
  field: ModelRoutingField,
  catalog: readonly string[] | undefined,
  custom: readonly string[],
): ComboboxOption[] {
  const seen = new Set(catalog ?? []);
  const merged = [...(catalog ?? []), ...custom.filter((v) => !seen.has(v))];
  return merged.map((value) => ({
    value,
    label: modelRoutingFieldValueLabel(field, value),
  }));
}

/**
 * 编辑 `model_routing_groups`：模型路由组、成员与规则。
 *
 * 后端 `RoutingSettings.Validate()` 仍是权威；这里只做同一套 grammar 的本地
 * 预检，让明显非法的草稿不会进入自动保存。界面只暴露 V1 字段 —— 语义字段
 * （如 domains/intent）不属于 V1，因此没有目录选择，也没有编辑入口。
 *
 * 本面板不展示任何 credential、URL 或成员凭据。
 */
export function ModelRoutingGroupEditor({
  value,
  onChange,
  services,
  dimensions = [],
  protocols = [],
  disabled = false,
  onEditingChange,
  validationIssues = [],
}: {
  value: readonly ModelRoutingGroup[];
  onChange: (value: ModelRoutingGroup[]) => void;
  services: readonly RoutableService[];
  /**
   * 自定义分类维度定义。带上选项而不只是名字：编辑器的候选值要直接取自
   * 维度自己的选项集，否则用户能随手填一个分类器永远不会产出的值，写出
   * 一条永不命中的规则。
   */
  dimensions?: readonly ClassifierDimension[];
  /**
   * `protocol` 字段的取值域：core capabilities 报出的协议 id。为空时该字段
   * 回退为自由输入（测试与旧调用点即此路径）。
   */
  protocols?: readonly string[];
  disabled?: boolean;
  /** 通知父级当前是否处于未提交的文本编辑中。 */
  onEditingChange?: (editing: boolean) => void;
  /** 保存前校验发现的草稿错误;编辑器只负责行内呈现与定位。 */
  validationIssues?: readonly ModelRoutingDraftIssue[];
}) {
  const t = useT();
  const id = useId();
  const [selectedID, setSelectedID] = useState<string>(value[0]?.id ?? "");
  const [query, setQuery] = useState("");
  const [removeMember, setRemoveMember] = useState<{
    group: number;
    member: number;
  } | null>(null);
  const [removeRule, setRemoveRule] = useState<{
    group: number;
    rule: number;
  } | null>(null);
  const [customValues, setCustomValues] = useState<RoutingCustomValueMap>({});

  // 自建值是跨会话的记忆，只在挂载时读一次；读失败退化为空表。
  useEffect(() => {
    let live = true;
    void loadRoutingCustomValues().then((stored) => {
      if (live) setCustomValues(stored);
    });
    return () => {
      live = false;
    };
  }, []);

  const routingCustom = useMemo(
    () => ({
      values: customValues,
      remember: (field: string, value: string) => {
        setCustomValues((current) => {
          const existing = current[field] ?? [];
          if (existing.includes(value)) return current;
          return { ...current, [field]: [...existing, value] };
        });
        void rememberRoutingCustomValue(field, value);
      },
    }),
    [customValues],
  );

  const groups = value;
  const enabledServices = useMemo(
    () => services.filter((service) => service.enabled),
    [services],
  );

  const updateGroup = (
    index: number,
    patch: Partial<ModelRoutingGroup>,
  ): void => {
    onChange(
      value.map((group, current) =>
        current === index ? { ...group, ...patch } : group,
      ),
    );
  };

  const shown: readonly ModelRoutingGroup[] = query
    ? groups.filter(
        (group) =>
          group.id.includes(query.toLowerCase()) ||
          group.members.some((member) => member.model.includes(query)),
      )
    : groups;
  const selected = groups.find((group) => group.id === selectedID);
  // 过滤时选中的组可能被隐藏；此时详情跟随首个可见组，避免页签与详情不一致。
  const active: ModelRoutingGroup | undefined =
    selected && shown.includes(selected) ? selected : shown[0];
  const activeIndex = active ? groups.indexOf(active) : 0;
  const activeValidationIssues = validationIssues.filter(
    (issue) => issue.groupIndex === activeIndex,
  );

  useEffect(() => {
    if (active && active.id !== selectedID) setSelectedID(active.id);
  }, [active, selectedID]);

  useEffect(() => {
    const firstIssue = validationIssues[0];
    const issueGroup = firstIssue ? groups[firstIssue.groupIndex] : undefined;
    if (issueGroup && issueGroup.id !== selectedID)
      setSelectedID(issueGroup.id);
  }, [groups, selectedID, validationIssues]);

  useEffect(() => {
    const firstIssue = activeValidationIssues[0];
    if (!firstIssue) return;
    const path = firstIssue.conditionPath.join(".") || "root";
    const target = document.querySelector<HTMLElement>(
      `[data-model-routing-condition-path="${path}"]`,
    );
    if (!target) return;
    target.scrollIntoView?.({ block: "center" });
    const focusTarget =
      target.querySelector<HTMLElement>(
        '[data-model-routing-value-control] input[role="combobox"], input, button, [tabindex]',
      ) ?? target;
    focusTarget.focus({ preventScroll: true });
  }, [activeValidationIssues]);

  const full = groups.length >= maxModelRoutingGroups;

  const addGroup = () => {
    if (disabled || full) return;
    // 用未占用的 slug，避免新组一出现就与既有组 ID 冲突。
    let index = groups.length + 1;
    let slug = `group-${index}`;
    const taken = new Set(groups.map((group) => group.id));
    while (taken.has(slug)) {
      index += 1;
      slug = `group-${index}`;
    }
    const next = emptyModelRoutingGroup(slug);
    onChange([...value, next]);
    setSelectedID(slug);
    setQuery("");
  };

  if (groups.length === 0) {
    return (
      <section
        aria-labelledby={`${id}-title`}
        className="flex min-h-0 min-w-0 flex-1 flex-col gap-2"
      >
        <div className="flex min-w-0 shrink-0 items-center justify-between gap-2">
          <h2 id={`${id}-title`} className="text-sm font-semibold">
            {t("modelRoutingGroup.title")}
          </h2>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={disabled}
            onClick={addGroup}
            data-model-routing-add
          >
            <Plus aria-hidden="true" />
            {t("modelRoutingGroup.addGroup")}
          </Button>
        </div>
        <Panel className="p-6">
          <p className="text-sm font-medium">{t("modelRoutingGroup.empty")}</p>
          <p className="mt-1 text-xs text-muted-foreground">
            {t("modelRoutingGroup.emptyHint")}
          </p>
        </Panel>
      </section>
    );
  }

  return (
    <section
      aria-labelledby={`${id}-title`}
      className="flex min-h-0 min-w-0 flex-1 flex-col gap-2"
    >
      <div className="flex min-w-0 shrink-0 flex-wrap items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-1.5">
          <h2 id={`${id}-title`} className="text-sm font-semibold">
            {t("modelRoutingGroup.title")}
          </h2>
          <Badge className="tabular-nums" variant="secondary">
            {t("modelRoutingGroup.count", {
              count: groups.length,
              max: maxModelRoutingGroups,
            })}
          </Badge>
          <HelpPopover label={t("modelRoutingGroup.helpLabel")}>
            <div className="grid gap-2">
              <p className="font-medium text-foreground">
                {t("modelRoutingGroup.helpTitle")}
              </p>
              <p>{t("modelRoutingGroup.helpWhat")}</p>
              <p>{t("modelRoutingGroup.helpNamespace")}</p>
              <p>{t("modelRoutingGroup.helpV1")}</p>
              <p className="text-muted-foreground">
                {t("modelRoutingGroup.helpScope")}
              </p>
            </div>
          </HelpPopover>
        </div>
        <div className="flex min-w-0 items-center gap-2">
          <Input
            className="h-8 w-40"
            type="search"
            aria-label={t("modelRoutingGroup.searchLabel")}
            placeholder={t("modelRoutingGroup.searchPlaceholder")}
            value={query}
            disabled={disabled}
            onChange={(event) => setQuery(event.target.value)}
          />
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="shrink-0"
            disabled={disabled || full}
            onClick={addGroup}
            data-model-routing-add
          >
            <Plus aria-hidden="true" />
            {t("modelRoutingGroup.addGroup")}
          </Button>
        </div>
      </div>

      <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-y-auto pb-1">
        <div
          className="flex min-w-0 flex-wrap gap-1.5"
          role="tablist"
          aria-label={t("modelRoutingGroup.groupsLabel")}
        >
          {shown.map((group) => {
            const index = groups.indexOf(group);
            return (
              <Button
                key={group.id}
                type="button"
                role="tab"
                size="sm"
                variant={index === activeIndex ? "secondary" : "ghost"}
                aria-selected={index === activeIndex}
                className="font-mono"
                onClick={() => setSelectedID(group.id)}
              >
                {group.id}
                {group.enabled ? null : (
                  <span className="text-muted-foreground">
                    {t("modelRoutingGroup.disabledTag")}
                  </span>
                )}
              </Button>
            );
          })}
        </div>

        {active ? (
          <CustomDimensionsContext.Provider value={dimensions}>
            <RoutingProtocolsContext.Provider value={protocols}>
              <RoutingCustomValuesContext.Provider value={routingCustom}>
                <GroupEditor
                  group={active}
                  index={activeIndex}
                  publicModelID={modelRoutingPublicModelID(active.id)}
                  enabledServices={enabledServices}
                  disabled={disabled}
                  validationIssues={activeValidationIssues}
                  onChange={(patch) => updateGroup(activeIndex, patch)}
                  onEditingChange={onEditingChange}
                  onRemoveGroup={() => {
                    const next = value.filter(
                      (_, current) => current !== activeIndex,
                    );
                    onChange(next);
                    setSelectedID(
                      next[Math.min(activeIndex, next.length - 1)]?.id ?? "",
                    );
                  }}
                  onRequestRemoveMember={(member) =>
                    setRemoveMember({ group: activeIndex, member })
                  }
                  onRequestRemoveRule={(rule) =>
                    setRemoveRule({ group: activeIndex, rule })
                  }
                />
              </RoutingCustomValuesContext.Provider>
            </RoutingProtocolsContext.Provider>
          </CustomDimensionsContext.Provider>
        ) : null}
      </div>

      <ConfirmDialog
        open={removeMember !== null}
        destructive
        title={t("modelRoutingGroup.removeMemberTitle")}
        description={t("modelRoutingGroup.removeMemberBody", {
          member:
            removeMember === null
              ? ""
              : (groups[removeMember.group]?.members[removeMember.member]?.id ??
                ""),
        })}
        confirmLabel={t("common.delete")}
        onCancel={() => setRemoveMember(null)}
        onConfirm={() => {
          const pending = removeMember;
          setRemoveMember(null);
          if (!pending) return;
          const group = value[pending.group];
          if (!group) return;
          const removed = group.members[pending.member];
          const members = group.members.filter(
            (_, current) => current !== pending.member,
          );
          const memberIDs = new Set(members.map((member) => member.id));
          // default 与所有规则目标都必须仍然指向存在的成员，否则整组失效。
          const stillValid =
            members.length > 0 && memberIDs.has(group.default_member_id);
          // 与删规则分支同源：删成员会带走指向它的规则，剩下的行必须重算
          // 行序，否则首行 priority 不再是 10，留下空洞。
          const rules = withDerivedPriorities(
            group.rules.filter((rule) => memberIDs.has(rule.target_member_id)),
          );
          updateGroup(pending.group, {
            members,
            default_member_id: stillValid
              ? group.default_member_id
              : (members[0]?.id ?? ""),
            rules,
          });
          if (removed) onEditingChange?.(false);
        }}
      />
      <ConfirmDialog
        open={removeRule !== null}
        destructive
        title={t("modelRoutingGroup.removeRuleTitle")}
        description={t("modelRoutingGroup.removeRuleBody", {
          rule:
            removeRule === null
              ? ""
              : (groups[removeRule.group]?.rules[removeRule.rule]?.id ?? ""),
        })}
        confirmLabel={t("common.delete")}
        onCancel={() => setRemoveRule(null)}
        onConfirm={() => {
          const pending = removeRule;
          setRemoveRule(null);
          if (!pending) return;
          const group = value[pending.group];
          if (!group) return;
          updateGroup(pending.group, {
            // 删除后重算行序，否则留下 priority 空洞。
            rules: withDerivedPriorities(
              group.rules.filter((_, current) => current !== pending.rule),
            ),
          });
        }}
      />
    </section>
  );
}

function GroupEditor({
  group,
  index,
  publicModelID,
  enabledServices,
  disabled,
  validationIssues,
  onChange,
  onEditingChange,
  onRemoveGroup,
  onRequestRemoveMember,
  onRequestRemoveRule,
}: {
  group: ModelRoutingGroup;
  index: number;
  publicModelID: string;
  enabledServices: readonly RoutableService[];
  disabled: boolean;
  validationIssues: readonly ModelRoutingDraftIssue[];
  onChange: (patch: Partial<ModelRoutingGroup>) => void;
  onEditingChange?: (editing: boolean) => void;
  onRemoveGroup: () => void;
  onRequestRemoveMember: (member: number) => void;
  onRequestRemoveRule: (rule: number) => void;
}) {
  const t = useT();
  const [taskTypeLabelLimitWarning, setTaskTypeLabelLimitWarning] =
    useState(false);
  const at = index + 1;
  const membersFull = group.members.length >= maxModelRoutingMembersPerGroup;
  const rulesFull = group.rules.length >= maxModelRoutingRulesPerGroup;
  const slugTaken = reservedModelRoutingSlugs.includes(group.id);
  const slugValid = /^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$/.test(group.id);
  // 拖拽源按组记录：落点行与被拖行不是同一个组件实例。
  const dragSource = useRef<number | null>(null);
  // 行序是优先级的唯一来源：任何规则改动都重算一次，不让手填的值漏进来。
  const commitRules = (
    rules: readonly ModelRoutingRule[],
    patch: Partial<ModelRoutingGroup> = {},
  ) => onChange({ ...patch, rules: withDerivedPriorities(rules) });

  return (
    <div className="grid min-w-0 gap-3">
      <Panel>
        <PanelHeader
          actions={
            <IconButton
              type="button"
              className="text-muted-foreground hover:text-destructive"
              label={t("modelRoutingGroup.removeGroup", { name: group.id })}
              disabled={disabled}
              onClick={onRemoveGroup}
            >
              <X aria-hidden="true" />
            </IconButton>
          }
        >
          <h3 className="flex items-center gap-1 text-sm font-semibold">
            {t("modelRoutingGroup.groupTitle", { index: at })}
            <HelpPopover label={t("modelRoutingGroup.groupHintLabel")}>
              {t("modelRoutingGroup.groupHint")}
            </HelpPopover>
          </h3>
        </PanelHeader>
        <div className="grid min-w-0 gap-4 p-4 @[640px]:grid-cols-2">
          <Field
            label={t("modelRoutingGroup.slug")}
            hint={t("modelRoutingGroup.slugHint")}
          >
            <Input
              className="h-8 font-mono"
              value={group.id}
              disabled={disabled}
              maxLength={64}
              aria-label={t("modelRoutingGroup.slug")}
              aria-invalid={!slugValid || slugTaken}
              onChange={(event) => {
                onEditingChange?.(true);
                onChange({ id: event.target.value.toLowerCase() });
              }}
              onBlur={() => onEditingChange?.(false)}
            />
          </Field>
          <Field
            label={t("modelRoutingGroup.publicID")}
            tooltipLabel={t("modelRoutingGroup.publicIDHintLabel")}
            tooltip={t("modelRoutingGroup.publicIDHint")}
          >
            <code className="truncate rounded-md border bg-muted px-2 py-1.5 text-xs">
              {publicModelID}
            </code>
          </Field>
          <Field
            label={t("modelRoutingGroup.enabled")}
            tooltipLabel={t("modelRoutingGroup.enabledHintLabel")}
            tooltip={t("modelRoutingGroup.enabledHint")}
          >
            <span className="flex items-center gap-2">
              <Switch
                size="sm"
                checked={group.enabled}
                disabled={disabled}
                aria-label={t("modelRoutingGroup.enabled")}
                onCheckedChange={(enabled) => onChange({ enabled })}
              />
            </span>
          </Field>
          <Field
            label={t("modelRoutingGroup.defaultMember")}
            tooltipLabel={t("modelRoutingGroup.defaultMemberHintLabel")}
            tooltip={t("modelRoutingGroup.defaultMemberHint")}
          >
            <ModelSelect
              options={group.members.map((member) => member.id)}
              value={group.default_member_id}
              disabled={disabled}
              placeholder={t("modelRoutingGroup.defaultMemberPlaceholder")}
              aria-label={t("modelRoutingGroup.defaultMember")}
              onValueChange={(default_member_id) =>
                onChange({ default_member_id })
              }
            />
          </Field>
        </div>
        {slugTaken ? (
          <FormMessage tone="error" className="px-4 pb-3">
            {t("modelRoutingGroup.slugReserved")}
          </FormMessage>
        ) : null}
        {group.members.length === 0 ? (
          <FormMessage tone="error" className="px-4 pb-3">
            {t("modelRoutingGroup.needMember")}
          </FormMessage>
        ) : null}
      </Panel>

      <Panel>
        <PanelHeader
          actions={
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={disabled || membersFull}
              onClick={() =>
                onChange({
                  members: [
                    ...group.members,
                    {
                      id: nextID(
                        group.members.map((member) => member.id),
                        "member",
                      ),
                      service_id: enabledServices[0]?.id ?? "",
                      model: "",
                    },
                  ],
                })
              }
            >
              <Plus aria-hidden="true" />
              {t("modelRoutingGroup.addMember")}
            </Button>
          }
        >
          <h3 className="text-sm font-semibold">
            {t("modelRoutingGroup.membersTitle")}
          </h3>
          <p className="mt-1 text-xs text-muted-foreground">
            {t("modelRoutingGroup.membersCount", {
              max: maxModelRoutingMembersPerGroup,
            })}
          </p>
        </PanelHeader>
        <div className="divide-y">
          {group.members.length === 0 ? (
            <p className="p-4 text-xs text-muted-foreground">
              {t("modelRoutingGroup.noMembers")}
            </p>
          ) : null}
          {group.members.map((member, memberIndex) => {
            const service = enabledServices.find(
              (item) => item.id === member.service_id,
            );
            const modelListed = service?.models.includes(member.model) ?? false;
            return (
              <div
                key={`member-${memberIndex}`}
                className="grid min-w-0 gap-3 p-4 @[720px]:grid-cols-[minmax(0,8rem)_minmax(0,14rem)_minmax(0,1fr)_auto] @[720px]:items-end"
                data-model-routing-member={memberIndex}
              >
                <Field label={t("modelRoutingGroup.memberID")}>
                  <Input
                    className="h-8 font-mono"
                    value={member.id}
                    disabled={disabled}
                    maxLength={64}
                    aria-label={t("modelRoutingGroup.memberID")}
                    onChange={(event) => {
                      onEditingChange?.(true);
                      const id = event.target.value.toLowerCase();
                      const members = group.members.map((item, current) =>
                        current === memberIndex ? { ...item, id } : item,
                      );
                      // 成员改名要让 default 与规则目标跟着走，否则整组引用失效。
                      const rules = group.rules.map((rule) =>
                        rule.target_member_id === member.id
                          ? { ...rule, target_member_id: id }
                          : rule,
                      );
                      onChange({
                        members,
                        default_member_id:
                          group.default_member_id === member.id
                            ? id
                            : group.default_member_id,
                        rules,
                      });
                    }}
                    onBlur={() => onEditingChange?.(false)}
                  />
                </Field>
                <Field label={t("modelRoutingGroup.service")}>
                  <ModelSelect
                    options={enabledServices.map((item) => item.id)}
                    value={member.service_id}
                    disabled={disabled}
                    allowCustomValue={false}
                    // 下拉显示提供商名；wire 值仍是 service id，未命中时回退显示 id
                    // 以便排查已被删除的提供商。
                    optionLabel={(serviceID) =>
                      enabledServices.find((item) => item.id === serviceID)
                        ?.name ?? serviceID
                    }
                    leadingIcon={null}
                    optionAdornment={(serviceID) => {
                      const kind = enabledServices.find(
                        (item) => item.id === serviceID,
                      )?.kind;
                      return kind ? (
                        <ServiceKindIcon kind={kind} size={16} />
                      ) : null;
                    }}
                    aria-label={t("modelRoutingGroup.service")}
                    onValueChange={(service_id) => {
                      // 换 service 后原 model 未必仍被列出，交给下方提示，不静默改动。
                      onChange({
                        members: group.members.map((item, current) =>
                          current === memberIndex
                            ? { ...item, service_id }
                            : item,
                        ),
                      });
                    }}
                  />
                </Field>
                <Field
                  label={t("modelRoutingGroup.memberModel")}
                  hint={
                    service && !modelListed && member.model
                      ? t("modelRoutingGroup.modelUnlisted")
                      : undefined
                  }
                >
                  <ModelSelect
                    options={service?.models ?? []}
                    value={member.model}
                    disabled={disabled}
                    aria-label={t("modelRoutingGroup.memberModel")}
                    onValueChange={(model) => {
                      onEditingChange?.(true);
                      onChange({
                        members: group.members.map((item, current) =>
                          current === memberIndex ? { ...item, model } : item,
                        ),
                      });
                      onEditingChange?.(false);
                    }}
                  />
                </Field>
                <IconButton
                  type="button"
                  className="text-muted-foreground hover:text-destructive"
                  label={t("modelRoutingGroup.removeMember", {
                    name: member.id,
                  })}
                  disabled={disabled}
                  onClick={() => onRequestRemoveMember(memberIndex)}
                >
                  <X aria-hidden="true" />
                </IconButton>
              </div>
            );
          })}
        </div>
      </Panel>

      <TaskTypeLabelsContext.Provider
        value={{
          labels:
            group.task_type_labels && group.task_type_labels.length > 0
              ? group.task_type_labels
              : modelRoutingTaskTypes,
          atCapacity:
            (group.task_type_labels?.length ?? modelRoutingTaskTypes.length) >=
            maxModelRoutingTaskTypeLabels,
        }}
      >
        <Panel>
          <PanelHeader
            actions={
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={disabled || rulesFull || group.members.length === 0}
                onClick={() =>
                  commitRules([...group.rules, newRuleDraft(group)])
                }
              >
                <Plus aria-hidden="true" />
                {t("modelRoutingGroup.addRule")}
              </Button>
            }
          >
            <h3 className="flex items-center gap-1 text-sm font-semibold">
              {t("modelRoutingGroup.rulesTitle")}
              <HelpPopover label={t("modelRoutingGroup.rulesHintLabel")}>
                <>
                  <p>
                    {t("modelRoutingGroup.rulesHint", {
                      max: maxModelRoutingRulesPerGroup,
                    })}
                  </p>
                  <p className="mt-1.5">
                    {t("modelRoutingGroup.rulesOrderHint")}
                  </p>
                </>
              </HelpPopover>
            </h3>
          </PanelHeader>
          {taskTypeLabelLimitWarning ? (
            <FormMessage
              tone="notice"
              className="mx-4 mb-3"
              data-model-routing-group-task-type-limit
            >
              {t("modelRoutingGroup.taskTypeLabelsLimitReached", {
                max: maxModelRoutingTaskTypeLabels,
              })}
            </FormMessage>
          ) : null}
          {group.rules.length === 0 ? (
            <p className="p-4 text-xs text-muted-foreground">
              {t("modelRoutingGroup.noRules")}
            </p>
          ) : (
            <Table containerClassName="overflow-x-visible">
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead className="w-0">
                    <span className="sr-only">
                      {t("modelRoutingGroup.rowOrder")}
                    </span>
                  </TableHead>
                  <TableHead>
                    <span className="flex items-center gap-1">
                      {t("modelRoutingGroup.priority")}
                      <HelpPopover
                        label={t("modelRoutingGroup.priorityDerivedHintLabel")}
                      >
                        {t("modelRoutingGroup.priorityDerivedHint", {
                          step: modelRoutingPriorityStep,
                        })}
                      </HelpPopover>
                    </span>
                  </TableHead>
                  <TableHead>{t("modelRoutingGroup.ruleID")}</TableHead>
                  <TableHead>
                    <span className="flex items-center gap-1">
                      {t("modelRoutingGroup.conditionsColumn")}
                      <HelpPopover
                        label={t("modelRoutingGroup.rulesOrderHintLabel")}
                      >
                        <>
                          <p>{t("modelRoutingGroup.conditionsAllHint")}</p>
                          <p className="mt-1.5">
                            {t("modelRoutingGroup.rulesOrderHint")}
                          </p>
                        </>
                      </HelpPopover>
                    </span>
                  </TableHead>
                  <TableHead>{t("modelRoutingGroup.targetMember")}</TableHead>
                  <TableHead className="w-0">
                    <span className="sr-only">
                      {t("modelRoutingGroup.rowActions")}
                    </span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {group.rules.map((rule, ruleIndex) => (
                  <DecisionRow
                    key={`rule-${ruleIndex}`}
                    rule={rule}
                    index={ruleIndex}
                    count={group.rules.length}
                    members={group.members}
                    disabled={disabled}
                    validationIssue={validationIssues.some(
                      (issue) =>
                        issue.ruleIndex === ruleIndex &&
                        issue.kind === "empty-values",
                    )}
                    dragSource={dragSource}
                    onEditingChange={onEditingChange}
                    onMove={(from, to) =>
                      commitRules(moveRule(group.rules, from, to))
                    }
                    onChange={(patch) => {
                      const rules = group.rules.map((item, current) =>
                        current === ruleIndex ? { ...item, ...patch } : item,
                      );
                      const taskTypeValues = patch.when
                        ? collectTaskTypeValues(patch.when)
                        : [];
                      const customTaskTypeValues = taskTypeValues.filter(
                        (value) => !modelRoutingTaskTypes.includes(value),
                      );
                      const currentLabels =
                        group.task_type_labels &&
                        group.task_type_labels.length > 0
                          ? group.task_type_labels
                          : modelRoutingTaskTypes;
                      const taskTypeLabels = [
                        ...new Set([...currentLabels, ...customTaskTypeValues]),
                      ];
                      if (
                        taskTypeLabels.length > maxModelRoutingTaskTypeLabels
                      ) {
                        setTaskTypeLabelLimitWarning(true);
                        return;
                      }
                      setTaskTypeLabelLimitWarning(false);
                      commitRules(
                        rules,
                        customTaskTypeValues.length > 0
                          ? { task_type_labels: taskTypeLabels }
                          : {},
                      );
                    }}
                    onRemove={() => onRequestRemoveRule(ruleIndex)}
                  />
                ))}
              </TableBody>
            </Table>
          )}
        </Panel>
      </TaskTypeLabelsContext.Provider>
    </div>
  );
}

/** 一个未被占用的 `<prefix>-<n>` 形式 ID。 */
function nextID(taken: readonly string[], prefix: string): string {
  const used = new Set(taken);
  let index = taken.length + 1;
  let candidate = `${prefix}-${index}`;
  while (used.has(candidate)) {
    index += 1;
    candidate = `${prefix}-${index}`;
  }
  return candidate;
}

/**
 * 行序即优先级：`priority = (i+1)*10`（设计 §2.2）。
 *
 * 每次改动规则都会重算整组的 priority，所以拖拽、删除、新增后不会留下
 * 空缺或重复；上限 `1_000_000_000` 远大于 `128*10`，天然唯一。
 */
function withDerivedPriorities(
  rules: readonly ModelRoutingRule[],
): ModelRoutingRule[] {
  return rules.map((rule, index) => ({
    ...rule,
    priority: (index + 1) * modelRoutingPriorityStep,
  }));
}

/** 把一行挪到 `to`，并重算行序优先级；越界或原地不动即原样返回。 */
function moveRule(
  rules: readonly ModelRoutingRule[],
  from: number,
  to: number,
): ModelRoutingRule[] {
  if (
    from === to ||
    from < 0 ||
    to < 0 ||
    from >= rules.length ||
    to >= rules.length
  )
    return [...rules];
  const next = [...rules];
  const [moved] = next.splice(from, 1);
  next.splice(to, 0, moved);
  return withDerivedPriorities(next);
}

/**
 * 「新增规则」插入的一条草稿。
 *
 * 一次点击只加一行：原先一次插入 task_type / turn_count / has_tools 三条预设
 * （ASTR-15 §六），实机反馈是用户点一次得到三条、还得逐条删掉不要的。保留
 * 「不空白起步」的意图：新行预选 task_type 字段，取值留空，保存前必须填齐；
 * 目标成员沿用默认成员，不自动启用、不自动发推理。
 */
function newRuleDraft(group: ModelRoutingGroup): ModelRoutingRule {
  return {
    id: nextID(
      group.rules.map((rule) => rule.id),
      "rule",
    ),
    priority: (group.rules.length + 1) * modelRoutingPriorityStep,
    when: defaultConditionFor("task_type"),
    target_member_id: group.default_member_id || group.members[0]?.id || "",
  };
}

function collectTaskTypeValues(condition: ModelRoutingCondition): string[] {
  if ("field" in condition) {
    if (condition.field !== "task_type") return [];
    const values: string[] = [];
    const valueCondition = condition as ModelRoutingValueCondition;
    for (const operator of ["in", "not_in"] as const)
      values.push(...(valueCondition[operator] ?? []));
    for (const operator of ["equals", "not_equals"] as const) {
      const value = valueCondition[operator];
      if (value) values.push(value);
    }
    return values;
  }
  if ("not" in condition) return collectTaskTypeValues(condition.not);
  const children =
    "all" in condition ? (condition.all ?? []) : (condition.any ?? []);
  return children.flatMap(collectTaskTypeValues);
}

/** 决策表一行的草稿：行内条件恒为 AND，行间恒为 OR，行序即优先级。 */
interface DecisionPill {
  key: string;
  /** 叶子的 NOT 包裹；wire 上用 `not` 节点表达，不是第二套操作符。 */
  negated: boolean;
  condition: ModelRoutingLeafCondition;
}

/**
 * 把 wire 的 `when` 编译成决策表一行的叶子序列。
 *
 * 只接受本轮编辑器自己产出的形状：`all` 可扁平化，`not` 只包裹单个叶子。
 * 旧文档里的 `any` 或嵌套 `not` 无法用"行内恒 AND"表达，返回 `null`，
 * 由调用方明确报错而不是静默改写用户数据（设计 §2.3）。
 */
function decisionPills(when: ModelRoutingCondition): DecisionPill[] | null {
  if ("field" in when)
    return [
      {
        key: "p0",
        negated: false,
        condition: when as ModelRoutingLeafCondition,
      },
    ];
  if ("not" in when) {
    if (!("field" in when.not)) return null;
    return [
      {
        key: "p0",
        negated: true,
        condition: when.not as ModelRoutingLeafCondition,
      },
    ];
  }
  if ("any" in when) return null;
  const children = when.all ?? [];
  if (children.length === 0) return null;
  const pills: DecisionPill[] = [];
  for (const [index, child] of children.entries()) {
    const nested = decisionPills(child);
    if (nested === null) return null;
    // 扁平的子 `all` 直接展开；`not` 与叶子的 key 只在本地使用。
    for (const [nestedIndex, pill] of nested.entries())
      pills.push({ ...pill, key: `p${index}-${nestedIndex}` });
  }
  return pills;
}

/** 叶子序列写回 wire：1 个叶子直接写，N 个叶子包成 `all`，`not` 恢复包裹。 */
function decisionWhen(pills: readonly DecisionPill[]): ModelRoutingCondition {
  const leaves = pills.map((pill) =>
    pill.negated ? { not: pill.condition } : pill.condition,
  );
  if (leaves.length === 1) return leaves[0];
  return { all: leaves };
}

function DecisionRow({
  rule,
  index,
  count,
  members,
  disabled,
  validationIssue,
  dragSource,
  onChange,
  onEditingChange,
  onRemove,
  onMove,
}: {
  rule: ModelRoutingRule;
  index: number;
  count: number;
  members: readonly ModelRoutingMember[];
  disabled: boolean;
  validationIssue: boolean;
  /** 整组共用一个拖拽源：落点在另一行，ref 不能挂在被拖的那一行上。 */
  dragSource: RefObject<number | null>;
  onChange: (patch: Partial<ModelRoutingRule>) => void;
  onEditingChange?: (editing: boolean) => void;
  onRemove: () => void;
  onMove: (from: number, to: number) => void;
}) {
  const t = useT();
  const at = index + 1;
  const [dragging, setDragging] = useState(false);
  const pills = decisionPills(rule.when);
  // 行内叶子数就是条件节点数：超过 `all` 的列表上限后不能再加。
  const atLeafLimit = (pills?.length ?? 0) >= maxModelRoutingListItems;
  const move = (to: number) => {
    if (to < 0 || to >= count) return;
    onMove(index, to);
  };

  return (
    <TableRow
      data-model-routing-rule={index}
      className={cn(dragging && "bg-muted/60")}
      // 行序即优先级，所以拖拽只重排本地行，不改 wire 规则 ID（设计 §2.1）。
      onDragOver={(event) => {
        if (disabled) return;
        event.preventDefault();
      }}
      onDrop={(event) => {
        if (disabled) return;
        event.preventDefault();
        const from = dragSource.current;
        dragSource.current = null;
        if (from !== null && from !== index) onMove(from, index);
        setDragging(false);
      }}
      onDragEnd={() => setDragging(false)}
    >
      <TableCell className="w-0 align-top pt-[calc(0.5rem+2.375rem)]">
        <div className="flex h-8 items-center gap-0.5">
          {/* 句柄可聚焦：键盘重排是拖拽的等价路径（验收项 7），不是附加项。 */}
          <span
            role="button"
            tabIndex={disabled ? -1 : 0}
            draggable={!disabled}
            aria-label={t("modelRoutingGroup.dragRow", { index: at })}
            aria-disabled={disabled || undefined}
            data-model-routing-drag-handle={index}
            className={cn(
              "flex size-6 cursor-grab items-center justify-center rounded-sm text-muted-foreground",
              disabled && "cursor-not-allowed opacity-50",
            )}
            onDragStart={(event) => {
              event.dataTransfer?.setData("text/plain", String(index));
              if (event.dataTransfer) event.dataTransfer.effectAllowed = "move";
              dragSource.current = index;
              setDragging(true);
            }}
            onKeyDown={(event) => {
              if (disabled) return;
              if (event.key === "ArrowUp") {
                event.preventDefault();
                move(index - 1);
              }
              if (event.key === "ArrowDown") {
                event.preventDefault();
                move(index + 1);
              }
            }}
          >
            <GripVertical aria-hidden="true" size={14} />
          </span>
          <div className="flex flex-col">
            <IconButton
              type="button"
              size="icon-xs"
              className="size-4 text-muted-foreground"
              label={t("modelRoutingGroup.moveRowUp", { index: at })}
              disabled={disabled || index === 0}
              data-model-routing-move-up={index}
              onClick={() => move(index - 1)}
            >
              <ChevronUp aria-hidden="true" size={12} />
            </IconButton>
            <IconButton
              type="button"
              size="icon-xs"
              className="size-4 text-muted-foreground"
              label={t("modelRoutingGroup.moveRowDown", { index: at })}
              disabled={disabled || index === count - 1}
              data-model-routing-move-down={index}
              onClick={() => move(index + 1)}
            >
              <ChevronDown aria-hidden="true" size={12} />
            </IconButton>
          </div>
        </div>
      </TableCell>
      {/* 列名已在表头（带 `?` 说明），单元格不再重复一遍标签。窄列的
          顶部留白等于条件框的内边距 + 标签行高度，使优先级、规则 ID、目标
          成员与第一条条件的控件落在同一水平线上。 */}
      <TableCell className="w-16 align-top pt-[calc(0.5rem+2.375rem)]">
        {/* 优先级由行序生成，只读展示；数值框会让 tooltip 食言。 */}
        <span
          className="flex h-8 items-center text-sm tabular-nums text-text-secondary"
          data-model-routing-priority-value={rule.priority}
        >
          {rule.priority}
        </span>
      </TableCell>
      <TableCell className="w-36 align-top pt-[calc(0.5rem+2.375rem)]">
        <Input
          className="h-8 font-mono"
          value={rule.id}
          disabled={disabled}
          maxLength={64}
          aria-label={t("modelRoutingGroup.ruleID")}
          onChange={(event) => {
            onEditingChange?.(true);
            onChange({ id: event.target.value.toLowerCase() });
          }}
          onBlur={() => onEditingChange?.(false)}
        />
      </TableCell>
      <TableCell className="whitespace-normal align-top">
        {pills === null ? (
          <div
            className="grid gap-2 rounded-md border border-dashed border-destructive/60 p-2"
            data-model-routing-row-unrepresentable
          >
            <p className="text-xs text-destructive">
              {t("modelRoutingGroup.rowUnrepresentable")}
            </p>
            <ConditionSummary condition={rule.when} />
          </div>
        ) : (
          <div
            className="grid min-w-0 gap-2"
            data-model-routing-condition-pills
          >
            {pills.map((pill, pillIndex) => (
              <ConditionPill
                key={pill.key}
                pill={pill}
                index={pillIndex}
                disabled={disabled}
                invalid={validationIssue}
                onEditingChange={onEditingChange}
                onChange={(next) => {
                  const leaves = pills.map((item, current) =>
                    current === pillIndex ? next : item,
                  );
                  onChange({ when: decisionWhen(leaves) });
                }}
                // 删到最后一枚会写出空 when，那是非法形状：留一枚兜底。
                removable={pills.length > 1}
                onRemove={() =>
                  onChange({
                    when: decisionWhen(
                      pills.filter((_, current) => current !== pillIndex),
                    ),
                  })
                }
              />
            ))}
            <div className="flex min-w-0 flex-wrap items-center gap-2">
              <Button
                type="button"
                size="xs"
                variant="outline"
                disabled={disabled || atLeafLimit}
                data-model-routing-add-condition
                onClick={() =>
                  onChange({
                    when: decisionWhen([
                      ...pills,
                      {
                        key: `p${pills.length}`,
                        negated: false,
                        condition: defaultConditionFor(
                          "task_type",
                        ) as ModelRoutingLeafCondition,
                      },
                    ]),
                  })
                }
              >
                <Plus aria-hidden="true" />
                {t("modelRoutingGroup.addCondition")}
              </Button>
              {atLeafLimit ? (
                <p
                  className="text-xs text-muted-foreground"
                  role="status"
                  data-model-routing-condition-limit
                >
                  {t("modelRoutingGroup.rowLeafLimit", {
                    max: maxModelRoutingListItems,
                  })}
                </p>
              ) : null}
            </div>
          </div>
        )}
      </TableCell>
      <TableCell className="w-48 align-top pt-[calc(0.5rem+2.375rem)]">
        <ModelSelect
          options={members.map((member) => member.id)}
          value={rule.target_member_id}
          disabled={disabled}
          aria-label={t("modelRoutingGroup.targetMember")}
          onValueChange={(target_member_id) => onChange({ target_member_id })}
        />
      </TableCell>
      <TableCell className="w-0 align-top pt-[calc(0.5rem+2.375rem)]">
        <IconButton
          type="button"
          className="text-muted-foreground hover:text-destructive"
          label={t("modelRoutingGroup.removeRule", { name: rule.id || at })}
          disabled={disabled}
          onClick={onRemove}
        >
          <X aria-hidden="true" />
        </IconButton>
      </TableCell>
    </TableRow>
  );
}

/**
 * 决策表一行的单个条件叶子。
 *
 * 只有一条操作符路径：字段 -> 取值（或边界/时间窗）。`not` 用行首按钮切换，
 * 不再有「组合为 AND/OR」——行内恒 AND，行间恒 OR（设计 §1）。
 */
function ConditionPill({
  pill,
  index: _index,
  disabled,
  invalid,
  removable,
  onChange,
  onEditingChange,
  onRemove,
}: {
  pill: DecisionPill;
  index: number;
  disabled: boolean;
  invalid: boolean;
  /** 行内至少留一枚叶子，否则会写出非法的空 `when`。 */
  removable: boolean;
  onChange: (next: DecisionPill) => void;
  onEditingChange?: (editing: boolean) => void;
  onRemove: () => void;
}) {
  const t = useT();
  const customDimensions = useContext(CustomDimensionsContext);
  const allFields = useMemo(
    () => [
      ...modelRoutingFields,
      ...customDimensions.map((dimension) => dimension.name),
    ],
    [customDimensions],
  );
  const condition = pill.condition;
  const field = condition.field;
  const isRange = isModelRoutingRangeField(field);
  const isTime = isModelRoutingTimeField(field);
  return (
    <div
      className={cn(
        "grid min-w-0 gap-2 rounded-md border p-2",
        (invalid || pill.negated) && "border-destructive",
      )}
      data-model-routing-condition-node="leaf"
      data-model-routing-condition-path="pill"
    >
      {/* 字段 | 取值 | 操作 三列，flex-wrap 在窄宽度下自然折行。
          标签行统一 `min-h-5.5`（与带 `?` 的标签同高）：有的字段带说明按钮、
          有的不带，原来两列控件因此上下错开几像素。操作列用同高占位对齐到
          控件行，而不是悬在标签旁边。 */}
      <div className="flex min-w-0 flex-wrap items-start gap-x-3 gap-y-2 [&>div>*>span:first-child]:min-h-5.5">
        <div className="w-44 shrink-0" data-model-routing-field-control>
          <Field
            group={field === "task_type"}
            label={t("modelRoutingGroup.field")}
            tooltip={
              field === "task_type"
                ? t("modelRoutingGroup.taskTypeClassifierHint", {
                    source: t("modelRouting.fieldSource.classifier"),
                  })
                : undefined
            }
            tooltipLabel={t("modelRouting.fieldSource.classifier")}
          >
            <ModelSelect
              options={allFields}
              value={field}
              disabled={disabled}
              allowCustomValue={false}
              optionLabel={modelRoutingFieldLabel}
              leadingIcon={null}
              aria-label={t("modelRoutingGroup.field")}
              onValueChange={(next) => {
                onEditingChange?.(true);
                onChange({
                  ...pill,
                  condition: defaultConditionFor(
                    next as ModelRoutingField,
                  ) as ModelRoutingLeafCondition,
                });
              }}
            />
          </Field>
        </div>
        <div className="min-w-[16rem] flex-1" data-model-routing-value-control>
          {isTime ? (
            <TimeBounds
              condition={condition as ModelRoutingTimeCondition}
              disabled={disabled}
              onChange={(next) =>
                onChange({
                  ...pill,
                  condition: next as ModelRoutingLeafCondition,
                })
              }
            />
          ) : isRange ? (
            <RangeBounds
              condition={condition as ModelRoutingRangeCondition}
              disabled={disabled}
              onChange={(next) =>
                onChange({
                  ...pill,
                  condition: next as ModelRoutingLeafCondition,
                })
              }
            />
          ) : (
            <ValueBounds
              condition={condition as ModelRoutingValueCondition}
              disabled={disabled}
              onChange={(next) =>
                onChange({
                  ...pill,
                  condition: next as ModelRoutingLeafCondition,
                })
              }
            />
          )}
        </div>
        <div className="flex shrink-0 flex-col gap-1.5">
          <span aria-hidden="true" className="min-h-5.5" />
          <div className="flex min-w-0 items-center gap-1">
            <Button
              type="button"
              size="xs"
              variant="outline"
              className={cn(
                "h-8",
                pill.negated && "border-destructive text-destructive",
              )}
              aria-pressed={pill.negated}
              disabled={disabled}
              data-model-routing-pill-negate
              onClick={() => onChange({ ...pill, negated: !pill.negated })}
            >
              {t("modelRoutingGroup.conditionNot")}
            </Button>
            <HelpPopover label={t("modelRoutingGroup.unknownHintLabel")}>
              {t("modelRoutingGroup.unknownHint")}
            </HelpPopover>
            <IconButton
              type="button"
              size="icon-xs"
              className="text-muted-foreground hover:text-destructive"
              label={t("modelRoutingGroup.removeCondition")}
              disabled={disabled || !removable}
              onClick={onRemove}
            >
              <X aria-hidden="true" />
            </IconButton>
          </div>
        </div>
      </div>
      {invalid ? (
        <FormMessage tone="error" data-model-routing-validation-error>
          {t("modelRoutingGroup.emptyValueError")}
        </FormMessage>
      ) : null}
    </div>
  );
}

/**
 * 旧文档里无法用扁平决策表表达的 `when` 的只读呈现。
 *
 * H1 已取消兼容层，本轮编辑器只产生"行内 AND 叶子"的形状。但静默改写用户
 * 数据比明确报错更糟（设计 §2.3），所以这类规则仍在表里占一行，条件原样
 * 展示、不给编辑控件；用户改动这一行即迁移到决策表形态。
 */
function ConditionSummary({ condition }: { condition: ModelRoutingCondition }) {
  if ("field" in condition) {
    const leaf = condition as ModelRoutingLeafCondition;
    return (
      <span
        className="text-xs text-text-secondary"
        data-model-routing-condition-summary="leaf"
      >
        {modelRoutingFieldLabel(leaf.field)}
      </span>
    );
  }
  if ("not" in condition)
    return (
      <span
        className="text-xs text-destructive"
        data-model-routing-condition-summary="not"
      >
        {"NOT("}
        <ConditionSummary condition={condition.not} />
        {")"}
      </span>
    );
  const operator = "all" in condition ? "all" : "any";
  const children =
    "all" in condition ? (condition.all ?? []) : (condition.any ?? []);
  return (
    <span
      className="text-xs text-text-secondary"
      data-model-routing-condition-summary={operator}
    >
      {`${operator === "all" ? "AND" : "OR"}[`}
      {children.map((child, index) => (
        <span key={`child-${index}`}>
          {index > 0 ? ", " : null}
          <ConditionSummary condition={child} />
        </span>
      ))}
      {"]"}
    </span>
  );
}

/** 数值字段的一侧边界：严格或闭区间，语义由操作符决定。 */
const rangeLowerOperators = ["gt", "gte"] as const;
const rangeUpperOperators = ["lt", "lte"] as const;

type RangeSide = "lower" | "upper";

/** 该侧当前使用的操作符；旧格式在解析时已归一化，这里只会看到新键。 */
function rangeSideOperator(
  condition: ModelRoutingRangeCondition,
  side: RangeSide,
): ModelRoutingRangeOperator | null {
  const keys: readonly ModelRoutingRangeOperator[] =
    side === "lower" ? rangeLowerOperators : rangeUpperOperators;
  return keys.find((key) => Object.hasOwn(condition, key)) ?? null;
}

/**
 * 数值字段的边界编辑：下界与上界各占一行，行内先选操作符再填数值。
 *
 * `min`/`max` 是旧格式的别名，解析时已归一化成 `gte`/`lte`，所以这里只需要
 * 处理四个新操作符；用户改动任意一侧即写出新格式，旧文档无感知升级。
 */
function RangeBounds({
  condition,
  disabled,
  onChange,
}: {
  condition: ModelRoutingRangeCondition;
  disabled: boolean;
  onChange: (condition: ModelRoutingCondition) => void;
}) {
  const t = useT();
  const lower = rangeSideOperator(condition, "lower");
  const upper = rangeSideOperator(condition, "upper");
  // 两侧同时存在时才是区间；只有一侧时不允许删到没有边界。
  const bothSides = lower !== null && upper !== null;

  const operatorLabel = (operator: string) =>
    t(`modelRoutingGroup.rangeOperator.${operator}`);

  const update = (
    side: RangeSide,
    operator: ModelRoutingRangeOperator,
    value: number | undefined,
  ) => {
    const next: ModelRoutingRangeCondition = { field: condition.field };
    const assign = (
      currentSide: RangeSide,
      currentOperator: ModelRoutingRangeOperator | null,
      currentValue: number | undefined,
    ) => {
      if (currentOperator === null || currentValue === undefined) return;
      next[currentOperator] = currentValue;
    };
    const valueOf = (operator: ModelRoutingRangeOperator | null) =>
      operator === null ? undefined : condition[operator];
    if (side === "lower") {
      assign("lower", operator, value);
      assign("upper", upper, valueOf(upper));
    } else {
      assign("lower", lower, valueOf(lower));
      assign("upper", operator, value);
    }
    onChange(next);
  };

  const removeSide = (side: RangeSide) => {
    // 保留至少一个边界：只有一侧时不给删除按钮。
    const keep = side === "lower" ? upper : lower;
    if (!bothSides || keep === null) return;
    onChange({ field: condition.field, [keep]: condition[keep] });
  };

  const renderSide = (side: RangeSide) => {
    const operators =
      side === "lower" ? rangeLowerOperators : rangeUpperOperators;
    const operator = side === "lower" ? lower : upper;
    const active: ModelRoutingRangeOperator = operator ?? operators[0];
    const value = operator === null ? undefined : condition[operator];
    const label = t(
      side === "lower"
        ? "modelRoutingGroup.rangeLower"
        : "modelRoutingGroup.rangeUpper",
    );
    return (
      <div
        key={side}
        className="flex min-w-0 items-center gap-2"
        data-model-routing-range-side={side}
      >
        <ModelSelect
          options={[...operators]}
          value={active}
          disabled={disabled}
          allowCustomValue={false}
          optionLabel={operatorLabel}
          leadingIcon={null}
          className="w-44 shrink-0"
          aria-label={t("modelRoutingGroup.conditionOperator")}
          onValueChange={(next) =>
            update(side, next as ModelRoutingRangeOperator, value ?? 0)
          }
        />
        <Input
          className="h-8 min-w-0 flex-1 tabular-nums"
          type="number"
          min={0}
          value={value === undefined ? "" : String(value)}
          disabled={disabled}
          aria-label={label}
          onChange={(event) => {
            const text = event.target.value;
            if (text === "") {
              removeSide(side);
              return;
            }
            const parsed = Number(text);
            if (!Number.isSafeInteger(parsed) || parsed < 0) return;
            update(side, active, parsed);
          }}
        />
        {bothSides ? (
          <IconButton
            type="button"
            size="icon-xs"
            className="shrink-0 text-muted-foreground hover:text-destructive"
            label={t("modelRoutingGroup.removeBound", { bound: label })}
            disabled={disabled}
            onClick={() => removeSide(side)}
          >
            <X aria-hidden="true" />
          </IconButton>
        ) : (
          // 未使用的一侧留出等宽占位，两行输入框保持对齐。
          <span className="size-6 shrink-0" aria-hidden="true" />
        )}
      </div>
    );
  };

  const missing: RangeSide | null =
    lower === null ? "lower" : upper === null ? "upper" : null;
  return (
    <Field
      group
      label={t("modelRoutingGroup.rangeBounds")}
      tooltipLabel={t("modelRoutingGroup.rangeBoundsHintLabel")}
      tooltip={t("modelRoutingGroup.rangeBoundsHint")}
    >
      <div className="grid min-w-0 gap-1.5">
        {lower !== null ? renderSide("lower") : null}
        {upper !== null ? renderSide("upper") : null}
        {missing !== null ? (
          <Button
            type="button"
            size="xs"
            variant="outline"
            className="justify-self-start"
            data-model-routing-add-bound={missing}
            disabled={disabled}
            onClick={() =>
              update(missing, missing === "lower" ? "gte" : "lte", 0)
            }
          >
            <Plus aria-hidden="true" />
            {t(
              missing === "lower"
                ? "modelRoutingGroup.addRangeLower"
                : "modelRoutingGroup.addRangeUpper",
            )}
          </Button>
        ) : null}
      </div>
    </Field>
  );
}

/**
 * `system_time` 的窗口编辑：两个 `<input type="time">` 组成一条时间胶囊。
 *
 * 起止相同 = 一个瞬时；起点晚于终点 = 跨午夜。两者都是**合法**输入，所以只在
 * 状态行里说明语义，不当作错误。真正阻断保存的是「未填」（草稿校验的
 * `empty-values`），因为 `within` 必须恰好两个值才能进 wire。
 */
function TimeBounds({
  condition,
  disabled,
  onChange,
}: {
  condition: ModelRoutingTimeCondition;
  disabled: boolean;
  onChange: (condition: ModelRoutingCondition) => void;
}) {
  const t = useT();
  const within = condition.within ?? [];
  const [start = "", end = ""] = within;
  const complete =
    within.length === modelRoutingTimeWindowLength &&
    within.every((value) => isModelRoutingTimeOfDay(value));
  const crossesMidnight = complete && start > end;
  const singleInstant = complete && start === end;

  const update = (index: number, value: string) => {
    const next =
      within.length === modelRoutingTimeWindowLength
        ? [...within]
        : ["", "", ""].slice(0, modelRoutingTimeWindowLength);
    next[index] = value;
    // 两个空字符串等于「未填」，交给草稿校验拦住，不在这里造一个假时段。
    onChange(
      next.every((item) => item === "")
        ? { field: condition.field, within: [] }
        : { field: condition.field, within: next },
    );
  };

  const sideLabel = (side: "start" | "end") =>
    t(
      side === "start"
        ? "modelRoutingGroup.timeWindowStart"
        : "modelRoutingGroup.timeWindowEnd",
    );

  return (
    <Field
      group
      label={t("modelRoutingGroup.timeWindow")}
      tooltipLabel={t("modelRoutingGroup.timeWindowHintLabel")}
      tooltip={
        <>
          <p>{t("modelRoutingGroup.timeWindowTooltipHalfOpen")}</p>
          <p className="mt-1.5">
            {t("modelRoutingGroup.timeWindowTooltipTimezone")}
          </p>
          <p className="mt-1.5">
            {t("modelRoutingGroup.timeWindowTooltipCrossMidnight")}
          </p>
        </>
      }
    >
      <div className="grid min-w-0 gap-1.5">
        <div
          className="flex min-w-0 flex-wrap items-center gap-2"
          data-model-routing-time-window
        >
          <Input
            type="time"
            step={modelRoutingTimeStepSeconds}
            className="h-8 w-32 min-w-0"
            value={start}
            disabled={disabled}
            aria-label={sideLabel("start")}
            data-model-routing-time-start
            onChange={(event) => update(0, event.target.value)}
          />
          <span aria-hidden="true" className="text-xs text-muted-foreground">
            –
          </span>
          <Input
            type="time"
            step={modelRoutingTimeStepSeconds}
            className="h-8 w-32 min-w-0"
            value={end}
            disabled={disabled}
            aria-label={sideLabel("end")}
            data-model-routing-time-end
            onChange={(event) => update(1, event.target.value)}
          />
        </div>
        {crossesMidnight ? (
          <p
            className="text-xs text-muted-foreground"
            role="status"
            data-model-routing-time-crosses-midnight
          >
            {t("modelRoutingGroup.timeWindowCrossesMidnight", {
              start,
              end,
            })}
          </p>
        ) : null}
        {singleInstant ? (
          <p
            className="text-xs text-muted-foreground"
            role="status"
            data-model-routing-time-single-instant
          >
            {t("modelRoutingGroup.timeWindowSingleInstant")}
          </p>
        ) : null}
        {!complete ? (
          <p
            className="text-xs text-muted-foreground"
            role="status"
            data-model-routing-time-incomplete
          >
            {t("modelRoutingGroup.timeWindowIncomplete")}
          </p>
        ) : null}
      </div>
    </Field>
  );
}

function ValueBounds({
  condition,
  disabled,
  onChange,
}: {
  condition: ModelRoutingValueCondition;
  disabled: boolean;
  onChange: (condition: ModelRoutingCondition) => void;
}) {
  const t = useT();
  const custom = useContext(RoutingCustomValuesContext);
  const customDimensions = useContext(CustomDimensionsContext);
  const protocols = useContext(RoutingProtocolsContext);
  const { labels: taskTypes, atCapacity: taskTypeLabelsAtCapacity } =
    useContext(TaskTypeLabelsContext);
  const operators = fieldOperators(
    condition.field,
  ) as readonly ModelRoutingValueOperator[];
  const catalog =
    condition.field === "task_type"
      ? taskTypes
      : modelRoutingFieldValues(condition.field, customDimensions, protocols);
  const operator =
    modelRoutingConditionOperator(condition) ?? operators[0] ?? "in";
  // task_type is a group-scoped vocabulary: existing labels are suggestions,
  // but the user may create a new label and it is persisted on the group.
  const creatable = condition.field === "task_type" || catalog === undefined;
  const multi =
    operator === "in" ||
    operator === "not_in" ||
    operator === "contains_any" ||
    operator === "not_contains_any";
  const list = multi
    ? ((condition[operator] as string[] | undefined) ?? [])
    : [];
  const options = routingValueOptions(
    condition.field,
    catalog,
    custom.values[condition.field] ?? [],
  ).filter(
    (option) =>
      condition.field !== "task_type" ||
      !taskTypeLabelsAtCapacity ||
      taskTypes.includes(option.value),
  );
  const keyword =
    operator === "contains_any" || operator === "not_contains_any";
  const max = keyword ? maxModelRoutingKeywords : maxModelRoutingListItems;
  const operatorLabel = (value: string) =>
    t(`modelRoutingGroup.valueOperator.${value}`);

  /** 列表型取值：候选里没有的即用户新建，顺手记进 Store。 */
  const applyList = (next: string[]) => {
    for (const value of next)
      if (!options.some((option) => option.value === value))
        custom.remember(condition.field, value);
    onChange({ field: condition.field, [operator]: next });
  };

  const applyOperator = (next: ModelRoutingValueOperator) => {
    // 换操作符保留取值：列表型之间直接搬列表，单值型与列表型之间取首项。
    const current =
      operator === "equals_bool"
        ? []
        : (((condition as unknown as Record<string, unknown>)[operator] as
            | string[]
            | string
            | undefined) ?? []);
    const items = Array.isArray(current) ? current : [current];
    if (next === "equals_bool")
      return onChange({ field: condition.field, equals_bool: true });
    if (next === "equals" || next === "not_equals")
      return onChange({ field: condition.field, [next]: items[0] ?? "" });
    // 草稿允许空列表；保存前由统一校验阻止它进入 wire。
    return onChange({
      field: condition.field,
      [next]: items,
    });
  };

  if (operator === "equals_bool") {
    // 布尔型没有操作符列，只有是 / 否。
    return (
      <Field label={t("modelRoutingGroup.booleanValue")}>
        <ModelSelect
          options={["true", "false"]}
          value={condition.equals_bool ? "true" : "false"}
          disabled={disabled}
          allowCustomValue={false}
          optionLabel={(value) =>
            t(
              value === "true"
                ? "modelRoutingGroup.booleanTrue"
                : "modelRoutingGroup.booleanFalse",
            )
          }
          leadingIcon={null}
          aria-label={t("modelRoutingGroup.booleanValue")}
          onValueChange={(next) =>
            onChange({ field: condition.field, equals_bool: next === "true" })
          }
        />
      </Field>
    );
  }

  const fieldLabel =
    operator === "equals" || operator === "not_equals"
      ? t("modelRoutingGroup.singleValue")
      : keyword
        ? t("modelRoutingGroup.keywords")
        : t("modelRoutingGroup.valueList");

  return (
    <Field
      group
      label={fieldLabel}
      tooltipLabel={t("modelRoutingGroup.fieldValueListHintLabel")}
      tooltip={
        // 自定义 task_type 标签的说明收进 `?`：它是一段长文本，放在控件下面会
        // 把取值列挤成一条窄竖栏、整行被拉高（验收截图 2）。
        creatable && condition.field === "task_type" ? (
          <>
            <p>{t("modelRoutingGroup.listHint", { max })}</p>
            <p className="mt-1.5" data-model-routing-custom-hint>
              {t("modelRoutingGroup.customTaskTypeHint")}
            </p>
          </>
        ) : (
          t("modelRoutingGroup.listHint", { max })
        )
      }
    >
      <div className="grid min-w-0 gap-1.5">
        <div
          className="flex min-w-0 flex-wrap items-center gap-2"
          data-model-routing-value-row
        >
          <ModelSelect
            options={operators}
            value={operator}
            disabled={disabled}
            allowCustomValue={false}
            optionLabel={operatorLabel}
            leadingIcon={null}
            className="w-36 shrink-0"
            aria-label={t("modelRoutingGroup.conditionOperator")}
            onValueChange={(next) =>
              applyOperator(next as ModelRoutingValueOperator)
            }
          />
          {operator === "equals" || operator === "not_equals" ? (
            <Input
              className="h-8 min-w-0 flex-1"
              value={(condition[operator] as string | undefined) ?? ""}
              disabled={disabled}
              aria-label={fieldLabel}
              onChange={(event) =>
                onChange({
                  field: condition.field,
                  [operator]: event.target.value,
                })
              }
            />
          ) : (
            <div className="min-w-0 flex-1">
              <MultiCombobox
                values={list}
                options={options}
                disabled={disabled}
                max={max}
                placeholder={t("modelRoutingGroup.selectValue")}
                aria-label={fieldLabel}
                allowCustomValue={
                  creatable &&
                  !(condition.field === "task_type" && taskTypeLabelsAtCapacity)
                }
                valueLabel={(value) =>
                  modelRoutingFieldValueLabel(condition.field, value)
                }
                createLabel={(query) =>
                  t("modelRoutingGroup.createValue", { value: query })
                }
                removeLabel={(value) =>
                  t("modelRoutingGroup.removeValue", { value })
                }
                emptyMessage={t("modelRoutingGroup.emptyValueList")}
                onChange={applyList}
              />
            </div>
          )}
        </div>

        {condition.field === "task_type" && taskTypeLabelsAtCapacity ? (
          <FormMessage
            tone="notice"
            className="py-1.5"
            data-model-routing-task-type-label-limit
          >
            {t("modelRoutingGroup.taskTypeLabelsLimitReached", {
              max: maxModelRoutingTaskTypeLabels,
            })}
          </FormMessage>
        ) : null}
        {list.length >= max ? (
          <FormMessage tone="notice" className="py-1.5">
            {t("modelRoutingGroup.listLimitReached", { max })}
          </FormMessage>
        ) : null}
      </div>
    </Field>
  );
}

/**
 * 换字段时给出该字段的合法初始形状，避免留下上一个字段的键。
 *
 * 时间字段给一个空窗口 `[]`：留空而不是塞一个默认时段，是为了让「未填」在
 * 草稿层可见（保存被 `empty-values` 挡住），而不是静默地替用户选一段时间。
 */
export function defaultConditionFor(
  field: ModelRoutingField,
): ModelRoutingCondition {
  if (isModelRoutingTimeField(field)) return { field, within: [] };
  if (isModelRoutingRangeField(field)) return { field, gte: 0 };
  const operators = fieldOperators(field);
  if (operators.includes("equals_bool")) return { field, equals_bool: true };
  if ((operators as readonly string[]).includes("contains_any"))
    return { field, contains_any: [] };
  // New enum/open-vocabulary conditions start as an intentionally incomplete
  // draft. The save path validates the empty list before the strict wire parser.
  return { field, in: [] };
}
