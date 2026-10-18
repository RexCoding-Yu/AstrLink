import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";

import { i18n } from "./i18n";
import {
  confidenceRange,
  policyIgnoreReasons,
  type ConfidenceSpan,
  type PolicyKindRow,
} from "./request-trajectory-model";

/**
 * One row per kind. A count column gains a confidence column beside it when
 * any row has one; a reason column appears only when something was ignored
 * for it. `columns` narrows the table to what the policy acted on or to what
 * it left in place.
 */
export function PolicyKindTable({
  rows,
  columns = "all",
  onSelectKind,
}: {
  rows: PolicyKindRow[];
  columns?: "all" | "hits" | "ignored";
  onSelectKind?: (kind: string) => void;
}) {
  const t = i18n.t.bind(i18n);
  const showHits = columns !== "ignored";
  const hitsRanged = rows.some((row) => row.confidence !== null);
  const reasons =
    columns === "hits"
      ? []
      : policyIgnoreReasons.flatMap((reason) => {
          const used = rows.filter((row) => row.ignored[reason]);
          return used.length > 0
            ? [
                {
                  reason,
                  ranged: used.some((row) => row.ignored[reason]?.confidence),
                },
              ]
            : [];
        });
  return (
    <Table
      className="w-auto text-xs"
      containerClassName="shrink-0"
      data-testid="policy-kinds"
    >
      <TableHeader>
        <TableRow className="hover:bg-transparent">
          <TableHead className="h-7 pl-0">
            {t("trajectory.policyKind")}
          </TableHead>
          {showHits ? (
            <PolicyCountHead ranged={hitsRanged}>
              {t("trajectory.policyHits")}
            </PolicyCountHead>
          ) : null}
          {reasons.map(({ reason, ranged }) => (
            <PolicyCountHead key={reason} ranged={ranged}>
              {t(`trajectory.policyIgnoredReasons.${reason}`)}
            </PolicyCountHead>
          ))}
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((row) => (
          <TableRow data-kind={row.kind} key={row.kind}>
            <TableCell className="py-1 pl-0">
              {onSelectKind && row.count > 0 ? (
                <Button
                  className="h-auto px-0 text-xs"
                  data-kind={row.kind}
                  onClick={() => onSelectKind(row.kind)}
                  type="button"
                  variant="link"
                >
                  {row.label}
                </Button>
              ) : (
                row.label
              )}
            </TableCell>
            {showHits ? (
              <PolicyCountCells
                count={row.count}
                confidence={row.confidence}
                ranged={hitsRanged}
              />
            ) : null}
            {reasons.map(({ reason, ranged }) => (
              <PolicyCountCells
                count={row.ignored[reason]?.count ?? 0}
                confidence={row.ignored[reason]?.confidence ?? null}
                key={reason}
                ranged={ranged}
              />
            ))}
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function PolicyCountHead({
  ranged,
  children,
}: {
  ranged: boolean;
  children: string;
}) {
  return (
    <TableHead
      className={cn("h-7", !ranged && "text-right")}
      colSpan={ranged ? 2 : 1}
    >
      {children}
    </TableHead>
  );
}

function PolicyCountCells({
  count,
  confidence,
  ranged,
}: {
  count: number;
  confidence: ConfidenceSpan | null;
  ranged: boolean;
}) {
  return (
    <>
      <TableCell
        className={cn("py-1 text-right tabular-nums", ranged && "pr-1")}
      >
        {count > 0 ? count : null}
      </TableCell>
      {ranged ? (
        <TableCell className="py-1 pr-4 pl-1 text-muted-foreground tabular-nums">
          {confidence ? confidenceRange(confidence.min, confidence.max) : null}
        </TableCell>
      ) : null}
    </>
  );
}
