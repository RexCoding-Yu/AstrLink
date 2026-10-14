import { useEffect, useState } from "react";

import { EmptyState } from "./components/EmptyState";
import { Field } from "./components/Field";
import { FormMessage } from "./components/FormMessage";
import { HelpPopover } from "./components/HelpPopover";
import { Panel } from "./components/Panel";
import { Badge } from "./components/ui/badge";
import { Button } from "./components/ui/button";
import { Combobox } from "./components/ui/combobox";
import { Textarea } from "./components/ui/textarea";
import { classifierDryRun, getRoutingSettings } from "./bridge";
import type {
  ClassifierDryRunDecision,
  ClassifierDryRunField,
  ClassifierDryRunResult,
} from "./classifier-settings-model";
import { useT } from "./i18n";
import type { ModelRoutingGroup } from "./model-routing-group-model";

type RunState =
  | { status: "idle" }
  | { status: "running" }
  | { status: "done"; result: ClassifierDryRunResult }
  | { status: "failed"; message: string };

function percent(value: number): string {
  return `${Math.round(value * 100)}%`;
}

function DecisionLine({ decision }: { decision: ClassifierDryRunDecision }) {
  const t = useT();
  return (
    <dl className="m-0 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
      <dt className="text-muted-foreground">{t("classifier.dryRun.member")}</dt>
      <dd className="m-0 font-mono">{decision.member_id}</dd>
      <dt className="text-muted-foreground">
        {t("classifier.dryRun.memberService")}
      </dt>
      <dd className="m-0 font-mono">{decision.service_id}</dd>
      <dt className="text-muted-foreground">
        {t("classifier.dryRun.memberModel")}
      </dt>
      <dd className="m-0 font-mono">{decision.model}</dd>
      <dt className="text-muted-foreground">{t("classifier.dryRun.rule")}</dt>
      <dd className="m-0 font-mono">
        {decision.rule_id ?? t("classifier.dryRun.defaultMember")}
      </dd>
    </dl>
  );
}

/**
 * The classifier's full ranked output for one field. `rank` is 0-based on the
 * wire and is shown 1-based, matching how the ranking reads elsewhere. `chosen`
 * marks the value that was actually substituted; at most one per field.
 */
function CandidateTable({ field }: { field: ClassifierDryRunField }) {
  const t = useT();
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-center gap-1 text-xs font-medium">
        <span>{t("classifier.dryRun.candidates")}</span>
        <span className="font-mono text-muted-foreground">{field.field}</span>
        <HelpPopover label={t("classifier.dryRun.candidatesHintLabel")}>
          {t("classifier.dryRun.candidatesHint")}
        </HelpPopover>
      </div>
      <ol
        className="m-0 grid list-none gap-1 p-0"
        data-classifier-dry-run-candidates={field.field}
      >
        {field.candidates.map((candidate) => (
          <li
            key={`${candidate.rank}-${candidate.value}`}
            className="flex items-center gap-2 text-xs"
            data-classifier-dry-run-candidate={candidate.value}
            data-candidate-rank={candidate.rank}
            data-candidate-chosen={candidate.chosen ? "true" : "false"}
          >
            <span className="w-6 shrink-0 tabular-nums text-muted-foreground">
              {candidate.rank + 1}
            </span>
            <span className="font-mono">{candidate.value}</span>
            <span className="tabular-nums text-muted-foreground">
              {candidate.score.toFixed(2)}
            </span>
            <Badge variant={candidate.chosen ? "accent" : "outline"}>
              {candidate.chosen
                ? t("classifier.dryRun.candidateChosen")
                : t("classifier.dryRun.candidateEliminated")}
            </Badge>
          </li>
        ))}
      </ol>
    </div>
  );
}

/**
 * Runs one classification through the local Core so the user can check their
 * settings before trusting them. It never issues a real upstream inference
 * request: the Go side re-runs the privacy preflight and returns a reason
 * instead of failing when the classifier is unreachable.
 */
export function ClassifierDryRunPanel({ ready }: { ready: boolean }) {
  const t = useT();
  const [text, setText] = useState("");
  const [groupId, setGroupId] = useState("");
  const [groups, setGroups] = useState<readonly ModelRoutingGroup[]>([]);
  const [state, setState] = useState<RunState>({ status: "idle" });

  useEffect(() => {
    if (!ready) return;
    let active = true;
    void getRoutingSettings()
      .then((settings) => {
        if (active) setGroups(settings.model_routing_groups ?? []);
      })
      // The group list only tailors the decision preview; a failure here must
      // not stop the user from classifying text.
      .catch(() => {
        if (active) setGroups([]);
      });
    return () => {
      active = false;
    };
  }, [ready]);

  const run = (): void => {
    if (!ready || state.status === "running" || text.trim().length === 0)
      return;
    setState({ status: "running" });
    void classifierDryRun({
      text,
      ...(groupId === "" ? {} : { group_id: groupId }),
    })
      .then((result) => setState({ status: "done", result }))
      .catch((caught: unknown) =>
        setState({
          status: "failed",
          message:
            caught instanceof Error
              ? caught.message
              : t("classifier.dryRun.failed"),
        }),
      );
  };

  return (
    <div
      className="gutter-scroller flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-y-auto pb-1"
      data-testid="classifier-dry-run-panel"
    >
      <Panel className="flex shrink-0 flex-col gap-3 p-3">
        <Field
          label={t("classifier.dryRun.text")}
          tooltip={t("classifier.dryRun.textHint")}
        >
          <Textarea
            rows={5}
            value={text}
            aria-label={t("classifier.dryRun.text")}
            onChange={(event) => setText(event.target.value)}
          />
        </Field>
        <Field
          label={t("classifier.dryRun.group")}
          tooltip={t("classifier.dryRun.groupHint")}
        >
          <Combobox
            value={groupId}
            aria-label={t("classifier.dryRun.group")}
            placeholder={t("classifier.dryRun.noGroup")}
            emptyMessage={t("classifier.dryRun.noGroupMatch")}
            options={groups.map((group) => ({
              value: group.id,
              label: group.id,
            }))}
            onValueChange={setGroupId}
          />
        </Field>
        <div className="flex items-center justify-end gap-1.5">
          <HelpPopover label={t("classifier.dryRun.runHintLabel")}>
            {t("classifier.dryRun.runHint")}
          </HelpPopover>
          <Button
            type="button"
            size="sm"
            disabled={
              !ready || state.status === "running" || text.trim() === ""
            }
            onClick={run}
          >
            {state.status === "running"
              ? t("classifier.dryRun.running")
              : t("classifier.dryRun.run")}
          </Button>
        </div>
      </Panel>
      {state.status === "failed" ? (
        <FormMessage tone="error">{state.message}</FormMessage>
      ) : null}
      {state.status === "done" ? (
        <Panel className="flex shrink-0 flex-col gap-3 p-3">
          <dl className="m-0 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
            <dt className="text-muted-foreground">
              {t("classifier.dryRun.category")}
            </dt>
            <dd className="m-0 font-mono">
              {state.result.category === ""
                ? t("classifier.dryRun.unknown")
                : state.result.category}
            </dd>
            <dt className="text-muted-foreground">
              {t("classifier.dryRun.confidence")}
            </dt>
            <dd className="m-0 font-mono">
              {state.result.confidence === null
                ? t("classifier.dryRun.unknown")
                : percent(state.result.confidence)}
            </dd>
            <dt className="text-muted-foreground">
              {t("classifier.dryRun.duration")}
            </dt>
            <dd className="m-0 font-mono">{state.result.duration_ms} ms</dd>
          </dl>
          {/*
            Only fields that actually carry candidates render. `candidates` is
            `omitempty` on the Go side, so an absent block means "no scored
            fallback ran" — never an empty table, and never a synthesized row.
          */}
          {state.result.fields
            .filter((field) => field.candidates.length > 0)
            .map((field) => (
              <CandidateTable key={field.field} field={field} />
            ))}
          {state.result.fallback_reason === null ? null : (
            <FormMessage tone="warning">
              {t("classifier.dryRun.fallbackReason")}{" "}
              <span className="font-mono">{state.result.fallback_reason}</span>
            </FormMessage>
          )}
          {state.result.decision === null ? (
            <EmptyState
              title={t("classifier.dryRun.noDecisionTitle")}
              description={t("classifier.dryRun.noDecision")}
            />
          ) : (
            <DecisionLine decision={state.result.decision} />
          )}
          <Field
            label={t("classifier.dryRun.sanitized")}
            tooltip={t("classifier.dryRun.sanitizedHint")}
          >
            <pre className="m-0 max-h-40 overflow-auto rounded-sm border border-border bg-muted p-2 text-xs whitespace-pre-wrap">
              {state.result.sanitized_preview}
            </pre>
          </Field>
        </Panel>
      ) : null}
    </div>
  );
}
