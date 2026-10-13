import { DataField } from "@/components/DataRow";
import { Route } from "@/components/icons";
import { EmptyState } from "@/components/EmptyState";
import { Panel, PanelHeader } from "@/components/Panel";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useT } from "@/i18n";
import {
  modelRoutingCodeLabel,
  modelRoutingExecutionStateLabel,
  modelRoutingFieldLabel,
  modelRoutingFieldSkipLabel,
  modelRoutingFieldSourceLabel,
  modelRoutingPrivacyDecisionLabel,
  modelRoutingRuleSummary,
  modelRoutingSelectionSourceLabel,
  sortedFieldObservations,
} from "@/request-model-routing-model";
import type {
  ModelRoutingDecision,
  RequestRecord,
} from "@/request-record-model";
import { cn } from "@/lib/utils";

/**
 * 请求详情「模型路由」页签。
 *
 * 只展示 V1 单路信息：虚拟模型 → group → 命中规则或默认成员 → 元信息来源 →
 * 选中的具体 member/service/model → 逐 attempt 隐私结果 → 执行与回退。
 *
 * V1 隐藏而不是占位 Fusion 项：branch、lead/sidekick、compaction、双路成本
 * 都不出现在这里，也不留空字段。面板不展示 prompt、媒体 URL、命中关键词、
 * classifier 原文或任何 credential —— 那些内容不在 decision payload 里。
 */
export function RequestModelRoutingPanel({
  record,
  onOpenProviderRouting,
}: {
  record: RequestRecord;
  /** 跳到既有调用轨迹，provider 排序/跳过/故障转移在那里展示。 */
  onOpenProviderRouting?: () => void;
}) {
  const t = useT();
  const decision = record.model_routing;
  // 历史记录和未使用虚拟模型的调用没有该字段。这不是错误，也不代表路由失败。
  if (!decision) {
    return (
      <div className="gutter-scroller min-h-0 flex-1 overflow-y-auto py-4">
        <EmptyState
          title={t("modelRouting.empty")}
          description={t("modelRouting.emptyHint")}
        />
      </div>
    );
  }
  return (
    <div
      className="gutter-scroller grid min-h-0 flex-1 auto-rows-max content-start gap-3 overflow-y-auto py-4"
      data-testid="model-routing-panel"
    >
      <SelectionPanel decision={decision} />
      <FieldsPanel decision={decision} />
      <PrivacyPanel decision={decision} />
      <ExecutionPanel
        decision={decision}
        onOpenProviderRouting={onOpenProviderRouting}
      />
    </div>
  );
}

/** 虚拟模型 → group → 快照 → 命中规则或默认成员 → 选中成员。 */
function SelectionPanel({ decision }: { decision: ModelRoutingDecision }) {
  const t = useT();
  return (
    <Panel>
      <PanelHeader>
        <h2 className="text-sm font-semibold">{t("modelRouting.title")}</h2>
        <p className="mt-1 text-xs text-muted-foreground">
          {t("modelRouting.selectionSummary", {
            source: modelRoutingSelectionSourceLabel(decision.selection_source),
          })}
        </p>
      </PanelHeader>
      <dl className="grid min-w-0 gap-3 p-4 @[720px]:grid-cols-3">
        <DataField
          label={t("modelRouting.virtualModel")}
          value={<code className="text-xs">{decision.virtual_model_id}</code>}
        />
        <DataField
          label={t("modelRouting.group")}
          value={<code className="text-xs">{decision.group_id}</code>}
        />
        <DataField
          label={t("modelRouting.snapshot")}
          title={t("modelRouting.snapshotHint")}
          value={
            <code className="text-xs">{decision.config_snapshot_version}</code>
          }
        />
        <DataField
          label={t("modelRouting.ruleTitle")}
          value={modelRoutingRuleSummary(decision)}
        />
        <DataField
          label={t("modelRouting.member.id")}
          value={<code className="text-xs">{decision.member_id}</code>}
        />
        <DataField
          label={t("modelRouting.member.service")}
          value={<code className="text-xs">{decision.member_service_id}</code>}
        />
        <DataField
          label={t("modelRouting.member.model")}
          value={<code className="text-xs">{decision.member_model}</code>}
        />
      </dl>
    </Panel>
  );
}

/** 每个参与求值的字段：来源、是否取得、以及跳过时按哪个极性处理。 */
function FieldsPanel({ decision }: { decision: ModelRoutingDecision }) {
  const t = useT();
  const fields = sortedFieldObservations(decision);
  if (fields.length === 0) return null;
  return (
    <Panel>
      <PanelHeader>
        <h2 className="text-sm font-semibold">
          {t("modelRouting.fields.title")}
        </h2>
        <p className="mt-1 text-xs text-muted-foreground">
          {t("modelRouting.fields.hint")}
        </p>
      </PanelHeader>
      <Table containerClassName="min-h-0 overflow-x-auto">
        <TableHeader>
          <TableRow>
            <TableHead>{t("modelRouting.fields.field")}</TableHead>
            <TableHead>{t("modelRouting.fields.source")}</TableHead>
            <TableHead>{t("modelRouting.fields.state")}</TableHead>
            <TableHead>{t("modelRouting.fields.note")}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {fields.map((observation, index) => (
            <TableRow key={`${observation.field}-${index}`}>
              <TableCell className="text-xs">
                {modelRoutingFieldLabel(observation.field)}
                <code className="ml-1.5 text-micro text-muted-foreground">
                  {observation.field}
                </code>
              </TableCell>
              <TableCell className="text-xs">
                {modelRoutingFieldSourceLabel(observation.source)}
              </TableCell>
              <TableCell>
                <Badge variant={observation.known ? "secondary" : "outline"}>
                  {t(
                    observation.known
                      ? "modelRouting.fields.known"
                      : "modelRouting.fields.unknown",
                  )}
                </Badge>
              </TableCell>
              <TableCell className="text-xs text-muted-foreground">
                {/* 未取得时报稳定原因；已取得时只报命中次数，不回显命中内容。 */}
                {observation.known
                  ? observation.match_count === undefined
                    ? ""
                    : t("modelRouting.fields.matchCount", {
                        count: observation.match_count,
                      })
                  : [
                      modelRoutingFieldSkipLabel(observation),
                      observation.error_code
                        ? modelRoutingCodeLabel(observation.error_code)
                        : null,
                    ]
                      .filter(Boolean)
                      .join(" · ")}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Panel>
  );
}

/** 隐私 gate 逐 attempt 的结果；任何一次 reject 都在发送前停止。 */
function PrivacyPanel({ decision }: { decision: ModelRoutingDecision }) {
  const t = useT();
  return (
    <Panel>
      <PanelHeader>
        <h2 className="text-sm font-semibold">
          {t("modelRouting.privacy.title")}
        </h2>
        <p className="mt-1 text-xs text-muted-foreground">
          {t("modelRouting.privacy.hint")}
        </p>
      </PanelHeader>
      {decision.privacy_attempts.length === 0 ? (
        <p className="p-4 text-xs text-muted-foreground">
          {t("modelRouting.privacy.noAttempts")}
        </p>
      ) : (
        <Table containerClassName="min-h-0 overflow-x-auto">
          <TableHeader>
            <TableRow>
              <TableHead>{t("modelRouting.privacy.attemptColumn")}</TableHead>
              <TableHead>{t("modelRouting.member.service")}</TableHead>
              <TableHead>{t("modelRouting.privacy.decisionColumn")}</TableHead>
              <TableHead>{t("modelRouting.privacy.findingsColumn")}</TableHead>
              <TableHead>{t("modelRouting.privacy.durationColumn")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {decision.privacy_attempts.map((attempt) => (
              <TableRow key={attempt.attempt_index}>
                <TableCell className="text-xs tabular-nums">
                  {t("modelRouting.privacy.attempt", {
                    index: attempt.attempt_index,
                  })}
                </TableCell>
                <TableCell className="text-xs">
                  <code>{attempt.service_id}</code>
                </TableCell>
                <TableCell>
                  <Badge
                    variant={
                      attempt.decision === "reject"
                        ? "destructive"
                        : attempt.decision === "allow"
                          ? "secondary"
                          : "outline"
                    }
                  >
                    {modelRoutingPrivacyDecisionLabel(attempt.decision)}
                  </Badge>
                </TableCell>
                <TableCell className="text-xs tabular-nums">
                  {t("modelRouting.privacy.findings", {
                    count: attempt.finding_count,
                  })}
                </TableCell>
                <TableCell className="text-xs tabular-nums text-muted-foreground">
                  {attempt.duration_ms} ms
                  {attempt.error_code
                    ? ` · ${modelRoutingCodeLabel(attempt.error_code)}`
                    : ""}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </Panel>
  );
}

/** 单路执行的最终状态、耗时、回退原因，以及去既有 provider 视图的入口。 */
function ExecutionPanel({
  decision,
  onOpenProviderRouting,
}: {
  decision: ModelRoutingDecision;
  onOpenProviderRouting?: () => void;
}) {
  const t = useT();
  const state = decision.execution.status;
  return (
    <Panel>
      <PanelHeader
        actions={
          onOpenProviderRouting ? (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={onOpenProviderRouting}
            >
              <Route aria-hidden="true" />
              {t("modelRouting.providerLink")}
            </Button>
          ) : undefined
        }
      >
        <h2 className="text-sm font-semibold">
          {t("modelRouting.execution.title")}
        </h2>
        <p className="mt-1 text-xs text-muted-foreground">
          {t("modelRouting.providerLinkHint")}
        </p>
      </PanelHeader>
      <dl className="grid min-w-0 gap-3 p-4 @[720px]:grid-cols-4">
        <DataField
          label={t("modelRouting.execution.stateTitle")}
          value={
            <span
              className={cn(
                state === "failed" && "text-danger-foreground",
                state === "succeeded" && "text-foreground",
              )}
            >
              {modelRoutingExecutionStateLabel(state)}
            </span>
          }
        />
        <DataField
          label={t("modelRouting.execution.attemptsTitle")}
          value={t("modelRouting.execution.attempts", {
            count: decision.execution.attempt_count,
          })}
        />
        <DataField
          label={t("modelRouting.execution.durationTitle")}
          value={t("modelRouting.execution.duration", {
            ms: decision.duration_ms,
          })}
        />
        {decision.classifier_duration_ms === undefined ? null : (
          <DataField
            label={t("modelRouting.execution.classifierTitle")}
            value={t("modelRouting.execution.classifierDuration", {
              ms: decision.classifier_duration_ms,
            })}
          />
        )}
        {decision.fallback_reason ? (
          <DataField
            label={t("modelRouting.fallback")}
            value={decision.fallback_reason}
          />
        ) : null}
        {decision.error_code ? (
          <DataField
            label={t("modelRouting.error")}
            value={
              <>
                {modelRoutingCodeLabel(decision.error_code)}
                <code className="ml-1.5 text-micro text-muted-foreground">
                  {decision.error_code}
                </code>
              </>
            }
          />
        ) : null}
        {decision.execution.error_code ? (
          <DataField
            label={t("modelRouting.execution.errorTitle")}
            value={modelRoutingCodeLabel(decision.execution.error_code)}
          />
        ) : null}
      </dl>
    </Panel>
  );
}
