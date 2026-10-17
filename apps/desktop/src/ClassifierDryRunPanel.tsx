import { useEffect, useState } from "react";

import { EmptyState } from "./components/EmptyState";
import { Field } from "./components/Field";
import { FormMessage } from "./components/FormMessage";
import { HelpPopover } from "./components/HelpPopover";
import { Panel } from "./components/Panel";
import { Badge } from "./components/ui/badge";
import { Button } from "./components/ui/button";
import { Combobox } from "./components/ui/combobox";
import { Input } from "./components/ui/input";
import { Textarea } from "./components/ui/textarea";
import { classifierDryRun, getRoutingSettings } from "./bridge";
import type {
  ClassifierDryRunDecision,
  ClassifierDryRunDimension,
  ClassifierDryRunField,
  ClassifierDryRunResult,
  ClassifierDryRunRule,
} from "./classifier-settings-model";
import { useT } from "./i18n";
import { intelligentRoutingErrorText } from "./intelligent-routing-errors";
import {
  modelRoutingMediaKinds,
  modelRoutingTimeStepSeconds,
  type ModelRoutingGroup,
} from "./model-routing-group-model";
import {
  modelRoutingCodeLabel,
  modelRoutingFieldLabel,
  modelRoutingFieldValueLabel,
} from "./request-model-routing-model";

type RunState =
  | { status: "idle" }
  | { status: "running" }
  | { status: "done"; result: ClassifierDryRunResult }
  | { status: "failed"; message: string };

function percent(value: number): string {
  return `${Math.round(value * 100)}%`;
}

/**
 * A boolean request fact that is allowed to be unspecified.
 *
 * A plain checkbox cannot express three states — unchecked would mean both "no"
 * and "not asked" — and those two outcomes are genuinely different here: `false`
 * satisfies a `not_equals_bool` rule while unknown never satisfies anything.
 * Hence an explicit tri-state select rather than a checkbox with a default.
 */
function TriStateSelect({
  value,
  label,
  onChange,
}: {
  value: string;
  label: string;
  onChange: (next: string) => void;
}) {
  const t = useT();
  return (
    <Combobox
      value={value}
      aria-label={label}
      placeholder={t("classifier.dryRun.booleanUnknown")}
      emptyMessage={t("classifier.dryRun.noGroupMatch")}
      options={[
        { value: "true", label: t("classifier.dryRun.booleanTrue") },
        { value: "false", label: t("classifier.dryRun.booleanFalse") },
      ]}
      onValueChange={onChange}
    />
  );
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
 * What the classifier answered for each configured custom dimension.
 *
 * This is the table that answers "did my `branch` rule have anything to match
 * on?". `task_type`'s own verdict is the category line above; a custom
 * dimension is reported here, with its own score and the evaluator's verdict,
 * so a group configured only on a custom field still shows what that field was
 * classified as.
 */
function DimensionTable({
  dimensions,
}: {
  dimensions: readonly ClassifierDryRunDimension[];
}) {
  const t = useT();
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-center gap-1 text-xs font-medium">
        <span>{t("classifier.dryRun.dimensions")}</span>
        <HelpPopover label={t("classifier.dryRun.dimensionsHintLabel")}>
          {t("classifier.dryRun.dimensionsHint")}
        </HelpPopover>
      </div>
      <ul
        className="m-0 flex list-none flex-col gap-1 p-0"
        data-classifier-dry-run-dimensions="true"
      >
        {dimensions.map((dimension) => (
          <li
            key={dimension.name}
            className="flex items-center gap-3 text-xs"
            data-classifier-dry-run-dimension={dimension.name}
            data-dimension-known={dimension.known ? "true" : "false"}
            data-dimension-produced={dimension.produced ? "true" : "false"}
          >
            <span className="w-24 shrink-0 truncate font-mono text-muted-foreground">
              {dimension.name}
            </span>
            <span className="w-20 shrink-0 truncate font-mono">
              {dimension.label === "" ? "—" : dimension.label}
            </span>
            {dimension.produced ? (
              <span className="w-10 shrink-0 tabular-nums text-muted-foreground">
                {dimension.score.toFixed(2)}
              </span>
            ) : null}
            {dimension.known ? null : (
              <Badge variant="outline">
                {dimension.produced
                  ? t("classifier.dryRun.dimensionBelowThreshold")
                  : t("classifier.dryRun.dimensionNotProduced")}
              </Badge>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}

/**
 * Every rule of the group with its own verdict, in evaluation order.
 *
 * The decision line above says which member won. That is not enough to debug a
 * rule set: the evaluator stops at the first true rule, so from the outside a
 * rule that was never reached and one that ran and failed look identical. This
 * table reports each row separately — its own state, the member it names, and
 * the fields its condition read — and marks the row that actually selected.
 */
function RuleTable({
  rules = [],
}: {
  rules?: readonly ClassifierDryRunRule[];
}) {
  const t = useT();
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-center gap-1 text-xs font-medium">
        <span>{t("classifier.dryRun.rules")}</span>
        <HelpPopover label={t("classifier.dryRun.rulesHintLabel")}>
          {t("classifier.dryRun.rulesHint")}
        </HelpPopover>
      </div>
      {rules.length === 0 ? (
        <p className="m-0 text-xs text-muted-foreground">
          {t("classifier.dryRun.rulesEmpty")}
        </p>
      ) : (
        <ul
          className="m-0 flex list-none flex-col gap-1 p-0"
          data-classifier-dry-run-rules="true"
        >
          {rules.map((rule) => (
            <li
              key={rule.rule_id}
              className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs"
              data-classifier-dry-run-rule={rule.rule_id}
              data-rule-state={rule.state}
              data-rule-selected={rule.selected ? "true" : "false"}
            >
              <span className="w-28 shrink-0 truncate font-mono">
                {rule.rule_id}
              </span>
              <Badge
                variant={rule.state === "true" ? "default" : "outline"}
                data-rule-state-badge={rule.state}
              >
                {t(`classifier.dryRun.ruleState.${rule.state}`)}
              </Badge>
              <span className="shrink-0 tabular-nums text-muted-foreground">
                {t("classifier.dryRun.priority")} {rule.priority}
              </span>
              <span className="min-w-0 truncate font-mono text-muted-foreground">
                {t("classifier.dryRun.ruleTarget")} {rule.target_member_id}
                {rule.target_model === "" ? "" : ` · ${rule.target_model}`}
              </span>
              {rule.selected ? (
                <Badge variant="secondary">
                  {t("classifier.dryRun.ruleSelected")}
                </Badge>
              ) : null}
              {rule.target_missing ? (
                <span className="text-danger-foreground">
                  {t("classifier.dryRun.ruleTargetMissing")}
                </span>
              ) : null}
              {rule.state === "unknown" ? (
                <span className="text-muted-foreground">
                  {t("classifier.dryRun.ruleUnknownHint")}
                </span>
              ) : null}
              {rule.state === "not_evaluated" ? (
                <span className="text-muted-foreground">
                  {t("classifier.dryRun.ruleNotEvaluatedHint")}
                </span>
              ) : null}
              {/* The fields this rule's own condition read, so "field unknown"
                  and "no match" point at the field responsible. */}
              {rule.fields.map((field) => (
                <span
                  key={field.field}
                  className="min-w-0 truncate font-mono text-muted-foreground"
                  data-rule-field={field.field}
                  data-rule-field-known={field.known ? "true" : "false"}
                >
                  {modelRoutingFieldLabel(field.field)}
                  {field.known
                    ? ""
                    : ` (${t("classifier.dryRun.unknown")}${
                        field.skip_reason === null
                          ? ""
                          : ` · ${modelRoutingCodeLabel(field.skip_reason)}`
                      })`}
                </span>
              ))}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/**
 * Runs one classification through the local Core so the user can check their
 * settings before trusting them. It never issues a real upstream inference
 * request: the Go side re-runs the privacy preflight and returns a reason
 * instead of failing when the classifier is unreachable.
 */
export function ClassifierDryRunPanel({
  ready,
  protocols = [],
}: {
  ready: boolean;
  /** Protocol IDs from core capabilities; the fact domain, same as the rule editor's. */
  protocols?: readonly string[];
}) {
  const t = useT();
  const [text, setText] = useState("");
  const [groupId, setGroupId] = useState("");
  const [groups, setGroups] = useState<readonly ModelRoutingGroup[]>([]);
  const [state, setState] = useState<RunState>({ status: "idle" });
  // Request-side facts. Empty string is "unspecified", which the Go side turns
  // into an unknown fact rather than a default: a preview that silently assumed
  // a protocol would report a selection for a request nobody described.
  const [protocol, setProtocol] = useState("");
  const [mediaKind, setMediaKind] = useState("");
  const [streaming, setStreaming] = useState("");
  const [continuation, setContinuation] = useState("");
  const [systemTime, setSystemTime] = useState("");

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
      // Only what the user actually filled in travels; an omitted key stays an
      // unknown fact on the Go side.
      ...(protocol === "" ? {} : { protocol }),
      ...(mediaKind === "" ? {} : { media_kind: mediaKind }),
      ...(streaming === "" ? {} : { streaming: streaming === "true" }),
      ...(continuation === "" ? {} : { continuation: continuation === "true" }),
      ...(systemTime === "" ? {} : { system_time: systemTime }),
    })
      .then((result) => setState({ status: "done", result }))
      .catch((caught: unknown) =>
        setState({
          status: "failed",
          message: intelligentRoutingErrorText(
            caught,
            t("classifier.dryRun.failed"),
          ),
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
        <div className="flex items-center gap-1 text-xs font-medium">
          <span>{t("classifier.dryRun.facts")}</span>
          <HelpPopover label={t("classifier.dryRun.factsHintLabel")}>
            {t("classifier.dryRun.factsHint")}
          </HelpPopover>
        </div>
        {/*
          A dry run judges the whole rule table, so a rule written on protocol,
          media kind, streaming or continuation needs the request those fields
          describe. Everything here is optional: leaving one blank keeps the
          fact unknown, which never matches — and the verdict table below says
          "field unknown" for that row rather than the preview inventing a value.
        */}
        <div className="grid min-w-0 gap-3 sm:grid-cols-2">
          <Field
            label={t("classifier.dryRun.protocol")}
            tooltip={t("classifier.dryRun.protocolHint")}
          >
            <Combobox
              value={protocol}
              aria-label={t("classifier.dryRun.protocol")}
              placeholder={t("classifier.dryRun.protocolUnknown")}
              emptyMessage={t("classifier.dryRun.noGroupMatch")}
              options={protocols.map((value) => ({
                value,
                label: modelRoutingFieldValueLabel("protocol", value),
              }))}
              onValueChange={setProtocol}
            />
          </Field>
          <Field
            label={t("classifier.dryRun.mediaKind")}
            tooltip={t("classifier.dryRun.mediaKindHint")}
          >
            <Combobox
              value={mediaKind}
              aria-label={t("classifier.dryRun.mediaKind")}
              placeholder={t("classifier.dryRun.mediaKindUnknown")}
              emptyMessage={t("classifier.dryRun.noGroupMatch")}
              options={modelRoutingMediaKinds.map((value) => ({
                value,
                label: modelRoutingFieldValueLabel("media_kind", value),
              }))}
              onValueChange={setMediaKind}
            />
          </Field>
          <Field
            label={t("classifier.dryRun.streaming")}
            tooltip={t("classifier.dryRun.streamingHint")}
          >
            <TriStateSelect
              value={streaming}
              label={t("classifier.dryRun.streaming")}
              onChange={setStreaming}
            />
          </Field>
          <Field
            label={t("classifier.dryRun.continuation")}
            tooltip={t("classifier.dryRun.continuationHint")}
          >
            <TriStateSelect
              value={continuation}
              label={t("classifier.dryRun.continuation")}
              onChange={setContinuation}
            />
          </Field>
        </div>
        <Field
          label={t("classifier.dryRun.systemTime")}
          tooltip={t("classifier.dryRun.systemTimeHint")}
        >
          <Input
            type="time"
            step={modelRoutingTimeStepSeconds}
            className="h-8 w-32"
            value={systemTime}
            aria-label={t("classifier.dryRun.systemTime")}
            onChange={(event) => setSystemTime(event.target.value)}
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
            Custom dimensions come first: they are what the operator configured
            rules on, and `task_type`'s verdict — the category line above — is
            the built-in one they are comparing against.
          */}
          {state.result.dimensions.length === 0 ? null : (
            <DimensionTable dimensions={state.result.dimensions} />
          )}
          {/*
            Every rule, not just the winner. This is the block that answers
            "why did my rule not fire?", and it covers request-derived rules as
            well as classifier ones — the whole point of judging the rule table
            rather than only the classifier.
          */}
          {state.result.decision === null ? null : (
            <RuleTable rules={state.result.rules} />
          )}
          {/*
            The classifier's own ranked candidates, which the rule table does
            not carry: a row there shows which value matched, this shows the
            full ranking behind it. `candidates` is `omitempty` on the Go side,
            so an absent block means "no scored fallback ran" — never an empty
            table, and never a synthesized row.
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
