import {
  memo,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { useVirtualizer } from "@tanstack/react-virtual";

import { DataRow } from "@/components/DataRow";
import { HelpDisclosure } from "@/components/HelpDisclosure";
import { HelpPopover } from "@/components/HelpPopover";
import { ChevronRight } from "@/components/icons";
import { Metric, MetricGroup } from "@/components/Metric";
import { Panel, PanelHeader } from "@/components/Panel";
import { StatusBadge } from "@/components/StatusBadge";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import { withheldHint } from "./AuditReviewer";
import { i18n, useT } from "./i18n";
import { PolicyKindTable } from "./PolicyKindTable";
import {
  privacyFindingSites,
  privacyReplacements,
  splitCarriedReplacements,
  type PrivacyReplacement,
} from "./privacy-replacements";
import {
  notableCall,
  privacyKindCountLabel,
  privacyListItemKey,
  privacyListLayout,
  privacyOutcomeLabel,
  privacyOutcomeTone,
  requestPrivacyAudit,
  type PrivacyAuditSummary,
  type PrivacyCallAudit,
  type PrivacyKindCount,
  type PrivacyListItem,
  type PrivacyTurnAudit,
} from "./request-privacy-audit";
import type { AuditContent, RequestRecord } from "./request-record-model";
import {
  confidencePercent,
  extractPrivacyHits,
  placeholderExplanationText,
  policyIgnoreReasons,
  policyKindTable,
  recordedPrivacyFindings,
  recordedPrivacyHits,
  restoredText,
  type PolicyKindRow,
} from "./request-trajectory-model";

// Folded turns keep most lists short; a turn of a long agent loop can still
// hold hundreds of calls, so past this many lines only the visible ones mount.
const LIST_VIRTUALIZE_MIN_ITEMS = 80;
const LIST_OVERSCAN_ITEMS = 8;

function itemEstimate(item: PrivacyListItem | undefined): number {
  return item?.kind === "call" ? 37 : 33;
}

/**
 * What privacy protection did across one conversation. Every number comes from
 * the record itself: the decision Core recorded, what it replaced, and how
 * much of it the response path put back. The selected call opens in place to
 * show what was replaced and where, from the request as sent upstream.
 */
export function RequestPrivacyAudit({
  turns,
  selectedRequestId,
  auditContent,
  auditLoading,
  auditError,
  onSelectCall,
}: {
  turns: RequestRecord[];
  selectedRequestId: string | null;
  auditContent: AuditContent | null;
  auditLoading: boolean;
  auditError: string | null;
  onSelectCall: (requestId: string) => void;
}) {
  const t = useT();
  const summary = useMemo(() => requestPrivacyAudit(turns), [turns]);
  const records = useMemo(
    () => new Map(turns.map((turn) => [turn.id, turn])),
    [turns],
  );
  const single =
    summary.callCount === 1 ? summary.calls.find(notableCall) : undefined;
  // Only the turns the operator folded or opened by hand; every other turn
  // follows `privacyListLayout`.
  const [turnOverrides, setTurnOverrides] = useState<
    ReadonlyMap<string, boolean>
  >(() => new Map());
  const [collapsedId, setCollapsedId] = useState<string | null>(null);
  const layout = useMemo(
    () => privacyListLayout(summary, turnOverrides, selectedRequestId),
    [summary, turnOverrides, selectedRequestId],
  );
  const sessionKey = turns[0]?.session_id ?? turns[0]?.id ?? "";
  useEffect(() => {
    setTurnOverrides(new Map());
    setCollapsedId(null);
  }, [sessionKey]);

  const { items } = layout;
  const virtualized = items.length >= LIST_VIRTUALIZE_MIN_ITEMS;
  const listRef = useRef<HTMLUListElement>(null);
  const [scroller, setScroller] = useState<HTMLElement | null>(null);
  const [scrollMargin, setScrollMargin] = useState(0);
  // The list scrolls with the tab, below the summary, so the virtual window is
  // measured from where the list starts inside that scroller.
  useLayoutEffect(() => {
    const list = listRef.current;
    if (!virtualized || !list) return;
    const parent = scrollParent(list);
    if (parent !== scroller) setScroller(parent);
    if (!parent) return;
    const margin = Math.round(
      list.getBoundingClientRect().top -
        parent.getBoundingClientRect().top +
        parent.scrollTop,
    );
    if (margin !== scrollMargin) setScrollMargin(margin);
  });
  const virtualizer = useVirtualizer({
    count: virtualized ? items.length : 0,
    estimateSize: (index) => itemEstimate(items[index]),
    getItemKey: (index) =>
      items[index] ? privacyListItemKey(items[index]) : index,
    getScrollElement: () => scroller,
    overscan: LIST_OVERSCAN_ITEMS,
    scrollMargin,
  });

  const anchorRef = useRef<{ id: string; top: number } | null>(null);
  const latestRef = useRef({
    layout,
    selectedRequestId,
    onSelectCall,
    virtualized,
    virtualizer,
  });
  latestRef.current = {
    layout,
    selectedRequestId,
    onSelectCall,
    virtualized,
    virtualizer,
  };

  // Opening a call folds the one above it; the clicked row stays put. A
  // virtual list learns the new sizes a frame later, so until they land any
  // line above the clicked one carries the scroll position with it.
  useLayoutEffect(() => {
    const anchor = anchorRef.current;
    anchorRef.current = null;
    if (!anchor) return;
    const { layout, virtualized, virtualizer } = latestRef.current;
    if (virtualized) {
      const index = layout.items.findIndex(
        (item) => item.kind === "call" && item.call.recordId === anchor.id,
      );
      virtualizer.shouldAdjustScrollPositionOnItemSizeChange = (item) =>
        item.index < index;
      let frame = requestAnimationFrame(() => {
        frame = requestAnimationFrame(() => {
          virtualizer.shouldAdjustScrollPositionOnItemSizeChange = undefined;
        });
      });
      return () => {
        cancelAnimationFrame(frame);
        virtualizer.shouldAdjustScrollPositionOnItemSizeChange = undefined;
      };
    }
    const row = listRef.current?.querySelector<HTMLElement>(
      `[data-call-id="${anchor.id}"]`,
    );
    const parent = row ? scrollParent(row) : null;
    if (!row || !parent) return;
    parent.scrollTop += row.getBoundingClientRect().top - anchor.top;
  }, [selectedRequestId]);

  // Stable, so a selection repaints two rows instead of the whole list.
  const toggleCall = useCallback((call: PrivacyCallAudit, row: HTMLElement) => {
    const { layout, selectedRequestId, onSelectCall } = latestRef.current;
    if (call.recordId === selectedRequestId) {
      setCollapsedId((current) =>
        current === call.recordId ? null : call.recordId,
      );
      return;
    }
    // The turn the last pick held open stays open: folding it would pull
    // the rows below it up under the pointer.
    const previous = layout.turnByRecordId.get(selectedRequestId ?? "");
    if (
      previous !== undefined &&
      previous !== layout.turnByRecordId.get(call.recordId) &&
      layout.openTurns.has(previous)
    ) {
      setTurnOverrides((current) =>
        current.has(previous) ? current : new Map(current).set(previous, true),
      );
    }
    anchorRef.current = {
      id: call.recordId,
      top: row.getBoundingClientRect().top,
    };
    setCollapsedId(null);
    onSelectCall(call.recordId);
  }, []);
  const toggleTurn = useCallback((turn: PrivacyTurnAudit) => {
    const open = latestRef.current.layout.openTurns.has(turn.key);
    setTurnOverrides((current) => new Map(current).set(turn.key, !open));
  }, []);

  const visible: Array<{ index: number; offset: number | null }> = virtualized
    ? virtualizer.getVirtualItems().map((item) => ({
        index: item.index,
        offset: item.start - scrollMargin,
      }))
    : items.map((_, index) => ({ index, offset: null }));

  const renderItem = (item: PrivacyListItem) => {
    switch (item.kind) {
      case "turn":
        return (
          <TurnRow onToggle={toggleTurn} open={item.open} turn={item.turn} />
        );
      case "quiet-calls":
        return (
          <p
            className={cn(
              "py-2 pr-4 text-xs text-muted-foreground",
              item.nested ? "pl-9.5" : "pl-4",
            )}
          >
            {t(
              item.nested
                ? "privacyAudit.quietCallsInTurn"
                : "privacyAudit.cleanCalls",
              { count: item.count },
            )}
          </p>
        );
      case "quiet-turns":
        return (
          <p className="px-4 py-2 text-xs text-muted-foreground">
            {t("privacyAudit.quietTurns", { count: item.count })}
          </p>
        );
      case "call": {
        const { call, nested } = item;
        const open =
          expandable(call) &&
          call.recordId === selectedRequestId &&
          collapsedId !== call.recordId;
        const record = records.get(call.recordId);
        return (
          <>
            <CallRow
              audit={call}
              expanded={open}
              nested={nested}
              onToggle={toggleCall}
              selected={call.recordId === selectedRequestId}
              showExplanation={summary.explainedBy === null}
            />
            {open && record ? (
              <div
                className={cn(
                  "border-t bg-muted/30 py-3 pr-4",
                  nested ? "pl-9.5" : "pl-4",
                )}
              >
                <CallDetail
                  audit={call}
                  auditContent={auditContent}
                  auditError={auditError}
                  auditLoading={auditLoading}
                  record={record}
                />
              </div>
            ) : null}
          </>
        );
      }
    }
  };
  return (
    <div className="space-y-3" data-testid="privacy-audit">
      <Panel>
        <PanelHeader
          actions={
            <HelpPopover label={t("privacyAudit.helpLabel")} size="wide">
              <div className="space-y-2">
                <p>{t("privacyAudit.helpReplace")}</p>
                <p>{t("privacyAudit.helpRestore")}</p>
                <p>{t("privacyAudit.helpUnrestored")}</p>
              </div>
            </HelpPopover>
          }
          size="sm"
        >
          {" "}
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <h3 className="text-sm font-medium">{t("privacyAudit.title")}</h3>
            <StatusBadge tone={privacyOutcomeTone(summary.outcome)}>
              {privacyOutcomeLabel(summary.outcome)}
            </StatusBadge>
          </div>
        </PanelHeader>
        <div className="space-y-3 p-3.5">
          <Headline summary={summary} />
          <Counts summary={summary} />
          {summary.kinds.length > 0 ? (
            <div className="flex flex-wrap items-center gap-x-2 gap-y-1.5">
              <span className="text-xs text-muted-foreground">
                {t("privacyAudit.hitKinds")}
              </span>
              {summary.kinds.map((kind) => (
                <Badge
                  className="bg-muted font-normal text-foreground"
                  data-kind={kind.kind}
                  key={kind.kind}
                  variant="secondary"
                >
                  {privacyKindCountLabel(kind)}
                </Badge>
              ))}
            </div>
          ) : null}
          {summary.explainedBy ? (
            <div
              className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs"
              data-testid="privacy-audit-explanation"
            >
              <span className="text-muted-foreground">
                {t("privacyAudit.explanation")}
              </span>
              <span>{placeholderExplanationText(summary.explainedBy)}</span>
            </div>
          ) : null}
        </div>
      </Panel>

      {single && expandable(single) && records.has(single.recordId) ? (
        <Panel>
          <PanelHeader size="sm">
            <h3 className="text-sm font-medium">
              {t("privacyAudit.callDetail")}
            </h3>
          </PanelHeader>
          <div className="p-3.5">
            <CallDetail
              audit={single}
              auditContent={auditContent}
              auditError={auditError}
              auditLoading={auditLoading}
              record={records.get(single.recordId)!}
            />
          </div>
        </Panel>
      ) : null}

      {items.length > 0 && summary.callCount > 1 ? (
        <Panel>
          <PanelHeader size="sm">
            <h3 className="text-sm font-medium">{t("privacyAudit.calls")}</h3>
          </PanelHeader>
          <ul
            className="relative"
            ref={listRef}
            style={
              virtualized ? { height: virtualizer.getTotalSize() } : undefined
            }
          >
            {visible.map(({ index, offset }) => {
              const item = items[index]!;
              return (
                <li
                  className={cn(index > 0 && "border-t")}
                  data-call-id={
                    item.kind === "call" ? item.call.recordId : undefined
                  }
                  data-index={index}
                  key={privacyListItemKey(item)}
                  ref={virtualized ? virtualizer.measureElement : undefined}
                  style={
                    offset === null
                      ? undefined
                      : {
                          position: "absolute",
                          top: 0,
                          left: 0,
                          width: "100%",
                          transform: `translateY(${offset}px)`,
                        }
                  }
                >
                  {renderItem(item)}
                </li>
              );
            })}
          </ul>
        </Panel>
      ) : null}
    </div>
  );
}

/**
 * One sentence for the whole conversation, before any number is read. A
 * session whose calls all passed untouched is described as such; a mixed one
 * keeps the strongest outcome, because it is the one worth opening.
 */
function Headline({ summary }: { summary: PrivacyAuditSummary }) {
  const t = useT();
  const outcome =
    summary.outcome === "off" && summary.offCalls !== summary.callCount
      ? "clean"
      : summary.outcome;
  return (
    <p
      className="text-sm leading-6 text-text-secondary"
      data-testid="privacy-audit-headline"
    >
      {t(`privacyAudit.headline.${outcome}`, {
        count: summary.replaced,
      })}
    </p>
  );
}

function Counts({ summary }: { summary: PrivacyAuditSummary }) {
  const t = useT();
  const metrics: Array<{ label: string; value: string; hint: string }> = [];
  if (summary.replaced > 0) {
    metrics.push({
      label: t("privacyAudit.replaced"),
      value: String(summary.replaced),
      hint: t("privacyAudit.replacedHint"),
    });
  }
  if (summary.restored > 0) {
    metrics.push({
      label: t("privacyAudit.restored"),
      value: String(summary.restored),
      hint: t("privacyAudit.restoredSplit", {
        visible: summary.restoredVisible,
        tools: summary.restoredTools,
      }),
    });
  }
  if (summary.unrestored > 0) {
    metrics.push({
      label: t("privacyAudit.unrestored"),
      value: String(summary.unrestored),
      hint: t("privacyAudit.unrestoredHint"),
    });
  }
  if (summary.blockedCalls > 0) {
    metrics.push({
      label: t("privacyAudit.blockedCalls"),
      value: String(summary.blockedCalls),
      hint: t("privacyAudit.blockedHint"),
    });
  }
  if (metrics.length === 0) return null;
  // The count varies with what happened, so columns flow instead of leaving
  // the fixed four-column track's empty cells showing as a border band.
  return (
    <MetricGroup className="auto-cols-fr grid-flow-col grid-cols-none rounded-md border @min-[640px]/workspace-surface:grid-cols-none">
      {metrics.map((metric) => (
        <Metric
          key={metric.label}
          label={metric.label}
          size="sm"
          title={metric.hint}
          value={metric.value}
        />
      ))}
    </MetricGroup>
  );
}

/** A failed or unfinished check has nothing more to show than its row. */
function expandable(call: PrivacyCallAudit): boolean {
  return (
    call.outcome === "redacted" ||
    call.outcome === "warned" ||
    call.outcome === "blocked"
  );
}

function scrollParent(element: HTMLElement): HTMLElement | null {
  for (let node = element.parentElement; node; node = node.parentElement) {
    const { overflowY } = getComputedStyle(node);
    if (overflowY === "auto" || overflowY === "scroll") return node;
  }
  return null;
}

/** Every count a row or a turn header states, in one muted run. */
function tallyText(
  tally: Pick<
    PrivacyCallAudit,
    "replaced" | "restored" | "restoredTools" | "unrestored"
  >,
  restoreEnabled: boolean,
): string[] {
  const counts: string[] = [];
  if (tally.replaced > 0) {
    counts.push(i18n.t("privacyAudit.callReplaced", { count: tally.replaced }));
  }
  // With restore on, a zero is the result worth seeing: nothing came back.
  if (restoreEnabled || tally.restored > 0) {
    counts.push(restoredText(tally.restored, tally.restoredTools));
  }
  if (tally.unrestored > 0) {
    counts.push(
      i18n.t("privacyAudit.callUnrestored", { count: tally.unrestored }),
    );
  }
  return counts;
}

function KindCounts({ kinds }: { kinds: PrivacyKindCount[] }) {
  return kinds.map((kind) => (
    <span className="text-xs text-foreground" key={kind.kind}>
      {privacyKindCountLabel(kind)}
    </span>
  ));
}

const TurnRow = memo(function TurnRow({
  turn,
  open,
  onToggle,
}: {
  turn: PrivacyTurnAudit;
  open: boolean;
  onToggle: (turn: PrivacyTurnAudit) => void;
}) {
  const t = useT();
  const counts = [
    t("privacyAudit.turnCalls", { count: turn.callCount }),
    ...tallyText(
      turn,
      turn.calls.some((call) => call.restoreEnabled),
    ),
  ];
  return (
    <DataRow asChild className="border-b-0 px-0 py-0">
      <Button
        aria-expanded={open}
        className="h-auto w-full items-center justify-start gap-2 rounded-none bg-muted/70 px-4 py-2 text-left font-normal whitespace-normal text-foreground hover:bg-muted focus-visible:ring-inset"
        data-testid="privacy-audit-turn"
        data-turn={turn.key}
        onClick={() => onToggle(turn)}
        type="button"
        variant="ghost"
      >
        <ChevronRight
          aria-hidden="true"
          className={cn(
            "size-3.5 shrink-0 text-muted-foreground transition-transform",
            open && "rotate-90",
          )}
        />
        <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          <span className="text-xs font-medium">
            {turn.turnIndex === null
              ? t("privacyAudit.turnUnnumbered")
              : t("privacyAudit.turnLabel", { turn: turn.turnIndex })}
          </span>
          <StatusBadge tone={privacyOutcomeTone(turn.outcome)}>
            {privacyOutcomeLabel(turn.outcome)}
          </StatusBadge>
          <span className="text-xs text-muted-foreground">
            {counts.join(" · ")}
          </span>
          <KindCounts kinds={turn.kinds} />
        </span>
      </Button>
    </DataRow>
  );
});

const CallRow = memo(function CallRow({
  audit,
  selected,
  expanded,
  nested,
  showExplanation,
  onToggle,
}: {
  audit: PrivacyCallAudit;
  selected: boolean;
  expanded: boolean;
  /** Sits under a turn header, which already names the turn. */
  nested: boolean;
  /** Off when the summary already names the one explanation every call used. */
  showExplanation: boolean;
  onToggle: (call: PrivacyCallAudit, row: HTMLElement) => void;
}) {
  const t = useT();
  const label = t("privacyAudit.callLabel", {
    index: audit.turnIndex === null ? audit.index : audit.turnCallIndex,
  });
  const counts = tallyText(audit, audit.restoreEnabled);
  if (showExplanation && audit.explainedBy) {
    counts.push(placeholderExplanationText(audit.explainedBy));
  }
  // One line per call, so a long turn still reads as a list. The chevron
  // keeps its own column, so a wrapped line starts under the label.
  const content = (
    <>
      {expandable(audit) ? (
        <ChevronRight
          aria-hidden="true"
          className={cn(
            "mt-[3px] size-3.5 shrink-0 text-muted-foreground transition-transform",
            expanded && "rotate-90",
          )}
        />
      ) : (
        <span aria-hidden="true" className="size-3.5 shrink-0" />
      )}
      <span className="flex min-w-0 flex-col gap-1">
        <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          <span className="text-xs font-medium">{label}</span>
          <StatusBadge tone={privacyOutcomeTone(audit.outcome)}>
            {privacyOutcomeLabel(audit.outcome)}
          </StatusBadge>
          {counts.length > 0 ? (
            <span className="text-xs text-muted-foreground">
              {counts.join(" · ")}
            </span>
          ) : null}
          <KindCounts kinds={audit.kinds} />
        </span>
        {audit.detail ? (
          <code className="min-w-0 font-mono text-micro text-muted-foreground">
            {audit.detail}
          </code>
        ) : null}
      </span>
    </>
  );
  const indent = nested ? "pl-9.5" : "pl-4";
  if (!expandable(audit)) {
    return (
      <DataRow
        className={cn("items-start gap-2 border-b-0 py-2 pr-4", indent)}
        data-outcome={audit.outcome}
        data-testid="privacy-audit-call"
      >
        {content}
      </DataRow>
    );
  }
  return (
    <DataRow asChild className="border-b-0 px-0 py-0">
      <Button
        aria-current={selected ? "true" : undefined}
        aria-expanded={expanded}
        className={cn(
          "h-auto w-full items-start justify-start gap-2 rounded-none bg-transparent py-2 pr-4 text-left font-normal whitespace-normal text-foreground hover:bg-muted/60 focus-visible:bg-accent focus-visible:ring-inset aria-[current=true]:bg-accent",
          indent,
        )}
        data-outcome={audit.outcome}
        data-testid="privacy-audit-call"
        onClick={(event) => onToggle(audit, event.currentTarget)}
        title={t(
          expanded ? "privacyAudit.collapseCall" : "privacyAudit.expandCall",
        )}
        type="button"
        variant="ghost"
      >
        {content}
      </Button>
    </DataRow>
  );
});

/**
 * The selected call's audit: the kinds the policy weighed, then what it put
 * in place of each value, or where a warning or a block found its matches.
 */
function CallDetail({
  audit,
  record,
  auditContent,
  auditLoading,
  auditError,
}: {
  audit: PrivacyCallAudit;
  record: RequestRecord;
  auditContent: AuditContent | null;
  auditLoading: boolean;
  auditError: string | null;
}) {
  const t = useT();
  const replaced = recordedPrivacyHits(record.privacy_restore);
  const kinds = policyKindTable(
    replaced.length > 0
      ? replaced
      : recordedPrivacyFindings(record.privacy_findings),
    record.privacy_findings,
    record.privacy_ignored,
  );
  const acted = kinds.rows.filter((row) => row.count > 0);
  const left = kinds.rows.filter((row) => Object.keys(row.ignored).length > 0);
  // Without model confidence the replacement list already says what a
  // table of the acted-on kinds would.
  const actedTable = acted.some((row) => row.confidence !== null);
  const threshold =
    kinds.threshold === null ? null : (
      <p className="text-xs text-muted-foreground">
        {t("trajectory.policyThreshold", {
          threshold: confidencePercent(kinds.threshold),
        })}
      </p>
    );
  return (
    <div className="space-y-3" data-testid="privacy-call-detail">
      {actedTable ? (
        <div className="space-y-1">
          {threshold}
          <PolicyKindTable columns="hits" rows={acted} />
        </div>
      ) : null}
      {/* Matches left in place are context, not the result: folded until asked. */}
      {left.length > 0 ? (
        <div data-testid="privacy-call-ignored">
          <HelpDisclosure title={ignoredTitle(left)}>
            {actedTable ? null : threshold}
            <PolicyKindTable columns="ignored" rows={left} />
          </HelpDisclosure>
        </div>
      ) : null}
      {audit.outcome === "redacted" ? (
        <ReplacementList
          audit={audit}
          auditContent={
            auditContent?.request_id === record.id ? auditContent : null
          }
          auditError={auditError}
          auditLoading={auditLoading}
          findings={record.privacy_findings}
          replacedKinds={replaced.map((hit) => hit.kind)}
        />
      ) : (
        <FindingSites record={record} />
      )}
    </div>
  );
}

/** "未替换的匹配 32 处（低于门槛 3 · 类型已关闭 29）" */
function ignoredTitle(rows: PolicyKindRow[]): string {
  let total = 0;
  const reasons = policyIgnoreReasons.flatMap((reason) => {
    const count = rows.reduce(
      (sum, row) => sum + (row.ignored[reason]?.count ?? 0),
      0,
    );
    total += count;
    return count > 0
      ? [`${i18n.t(`trajectory.policyIgnoredReasons.${reason}`)} ${count}`]
      : [];
  });
  return i18n.t("privacyAudit.ignoredTitle", {
    count: total,
    reasons: reasons.join(" · "),
  });
}

function ReplacementList({
  audit,
  auditContent,
  auditLoading,
  auditError,
  findings,
  replacedKinds,
}: {
  audit: PrivacyCallAudit;
  auditContent: AuditContent | null;
  auditLoading: boolean;
  auditError: string | null;
  findings: RequestRecord["privacy_findings"];
  /** Kinds this call replaced; empty on records that predate the split. */
  replacedKinds: string[];
}) {
  const t = useT();
  const body = auditContent?.upstream_request_body?.content ?? null;
  const kindKey = replacedKinds.join(",");
  const { made: replacements, carried } = useMemo(
    () =>
      splitCarriedReplacements(
        body === null
          ? []
          : privacyReplacements(
              body,
              kindKey ? new Set(kindKey.split(",")) : undefined,
            ),
        findings,
      ),
    [body, kindKey, findings],
  );
  const answer =
    audit.unrestored > 0
      ? (auditContent?.response_content?.content ?? null)
      : null;
  const leftover = useMemo(
    () =>
      new Set(
        answer === null
          ? []
          : extractPrivacyHits(answer).flatMap((hit) => hit.placeholders),
      ),
    [answer],
  );
  if (auditError) {
    return (
      <p className="text-xs text-danger-foreground" role="alert">
        {auditError}
      </p>
    );
  }
  if (!auditContent || auditLoading) {
    return (
      <p className="text-xs text-muted-foreground" role="status">
        {t("records.decrypting")}
      </p>
    );
  }
  if (body === null) {
    return (
      <p className="text-xs leading-5 text-muted-foreground">
        {withheldHint(auditContent.withheld.upstream_request_body) ??
          t("trajectory.uncapturedHint")}
      </p>
    );
  }
  return (
    <div className="space-y-2">
      <p className="text-xs text-muted-foreground">
        {t("privacyAudit.replacementsTitle")}
      </p>
      {replacements.length === 0 ? (
        <p className="text-xs leading-5 text-muted-foreground">
          {t("privacyAudit.noReplacementsFound")}
        </p>
      ) : (
        <ul className="divide-y" data-testid="privacy-replacements">
          {replacements.map((replacement) => (
            <ReplacementItem
              key={replacement.placeholder}
              left={leftover.has(replacement.placeholder)}
              replacement={replacement}
            />
          ))}
        </ul>
      )}
      {carried.length > 0 ? (
        <div data-testid="privacy-carried">
          <HelpDisclosure
            title={t("privacyAudit.carriedTitle", { count: carried.length })}
          >
            <p>{t("privacyAudit.carriedHint")}</p>
            <ul className="divide-y">
              {carried.map((replacement) => (
                <ReplacementItem
                  key={replacement.placeholder}
                  left={leftover.has(replacement.placeholder)}
                  replacement={replacement}
                />
              ))}
            </ul>
          </HelpDisclosure>
        </div>
      ) : null}
      <p className="text-xs leading-5 text-muted-foreground">
        {t("privacyAudit.originalsHint")}
      </p>
    </div>
  );
}

function ReplacementItem({
  replacement,
  left,
}: {
  replacement: PrivacyReplacement;
  /** Still in the answer the client received. */
  left: boolean;
}) {
  const t = useT();
  const more = replacement.count - replacement.sites.length;
  return (
    <li
      className="space-y-1.5 py-2 first:pt-0 last:pb-0"
      data-kind={replacement.kind}
      data-placeholder={replacement.placeholder}
      data-testid="privacy-replacement"
    >
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs">
        <span className="font-medium">{replacement.label}</span>
        <code className="min-w-0 rounded-sm bg-warning-wash px-1 font-mono text-micro break-all text-warning-foreground">
          {replacement.placeholder}
        </code>
        <span className="text-muted-foreground">
          {t("privacyAudit.inRequest", { count: replacement.count })}
        </span>
        {left ? (
          <StatusBadge tone="negative">
            {t("privacyAudit.unrestored")}
          </StatusBadge>
        ) : null}
      </div>
      <ul className="space-y-1">
        {replacement.sites.map((site, index) => (
          <li className="min-w-0 rounded-md bg-muted px-2 py-1.5" key={index}>
            {site.path ? (
              <div
                className="truncate font-mono text-micro text-muted-foreground"
                title={site.path}
              >
                {site.role ? `${site.role} · ${site.path}` : site.path}
              </div>
            ) : null}
            <p className="text-xs leading-5 text-text-secondary [overflow-wrap:anywhere]">
              {site.before}
              <mark className="rounded-sm bg-warning-wash px-0.5 text-warning-foreground">
                {replacement.placeholder}
              </mark>
              {site.after}
            </p>
          </li>
        ))}
      </ul>
      {more > 0 ? (
        <p className="text-micro text-muted-foreground">
          {t("privacyAudit.moreSites", { count: more })}
        </p>
      ) : null}
    </li>
  );
}

/** Where a warning or a block found its matches; the values stay in the client body. */
function FindingSites({ record }: { record: RequestRecord }) {
  const t = useT();
  const sites = privacyFindingSites(record.privacy_findings);
  if (sites.length === 0) return null;
  return (
    <div className="space-y-1.5" data-testid="privacy-finding-sites">
      <p className="text-xs text-muted-foreground">
        {t("privacyAudit.findingsTitle")}
      </p>
      <ul className="space-y-1">
        {sites.map((site) => (
          <li
            className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5 text-xs"
            key={site.path}
          >
            <code className="min-w-0 font-mono text-micro break-all text-muted-foreground">
              {site.path}
            </code>
            <span>{site.kinds.map(privacyKindCountLabel).join(" · ")}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}
