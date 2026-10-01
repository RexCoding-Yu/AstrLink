import { useT } from "../i18n";
import type { RequestRoutingDecision } from "../request-record-model";
import {
  routingPlanLabel,
  routingReasonLabel,
  routingSteps,
  type RouteEvent,
  type RoutingStep,
} from "../request-routing-model";
import { cn } from "@/lib/utils";
import { ConversationIndicator } from "./ConversationIndicator";
import { StatusDot, type StatusTone } from "./StatusDot";

const toneByOutcome: Record<RoutingStep["outcome"], StatusTone> = {
  selected: "positive",
  rejected: "negative",
  skipped: "neutral",
};

/**
 * How routing reached a record's API provider: the ones it skipped and why,
 * the ones it picked but could not use, and the one it used. Failed upstream
 * attempts are the recovery chain's to show.
 */
export function RoutingSteps({
  routes,
  decision,
  selectedServiceId,
  serviceNames,
}: {
  routes: readonly RouteEvent[];
  decision?: RequestRoutingDecision;
  /** The record's provider, the one `decision.selected` explains. */
  selectedServiceId: string | null;
  serviceNames: Readonly<Record<string, string>>;
}) {
  const t = useT();
  const steps = routingSteps(routes, decision);
  if (steps.length === 0) return null;
  // Each step's words after the name: why it was skipped or refused, or why
  // and how the used one was called.
  const details = (step: RoutingStep): string[] =>
    step.outcome === "selected"
      ? [
          decision?.selected &&
          decision.selected !== "session_binding" &&
          step.serviceId === selectedServiceId
            ? t(`routingDecision.selections.${decision.selected}`)
            : null,
          routingPlanLabel(step.code),
        ].filter((text): text is string => Boolean(text))
      : [routingReasonLabel(step.code)];
  const paused = steps.some((step) => step.code === "circuit_open");
  return (
    <div className="grid gap-1 text-xs" data-testid="routing-steps">
      <h3 className="text-muted-foreground">{t("routingDecision.title")}</h3>
      <ol className="grid gap-1">
        {steps.map((step, index) => {
          const text = details(step).join(" · ");
          // Stickiness wears the conversation link, as continuation does.
          const sticky =
            step.outcome === "selected" &&
            decision?.selected === "session_binding" &&
            step.serviceId === selectedServiceId;
          return (
            <li
              className="flex items-baseline gap-2"
              data-code={step.code || undefined}
              data-outcome={step.outcome}
              key={`${index}:${step.serviceId}`}
            >
              <StatusDot
                className="relative -top-px"
                tone={toneByOutcome[step.outcome]}
              />
              <span className="min-w-0 break-words">
                <span className="sr-only">
                  {t(`routingDecision.outcomes.${step.outcome}`)}
                </span>
                <span
                  className={cn(
                    "font-medium",
                    step.outcome === "skipped"
                      ? "text-muted-foreground"
                      : "text-foreground",
                  )}
                >
                  {serviceNames[step.serviceId] ?? step.serviceId}
                </span>
                {sticky ? (
                  <span className="ml-1 inline-flex align-middle">
                    <ConversationIndicator kind="stickiness" />
                  </span>
                ) : null}
                {text ? (
                  <span
                    className={cn(
                      step.outcome === "rejected"
                        ? "text-danger-foreground"
                        : "text-muted-foreground",
                    )}
                  >
                    {" · "}
                    {text}
                  </span>
                ) : null}
              </span>
            </li>
          );
        })}
      </ol>
      {paused ? (
        <p className="leading-5 text-muted-foreground">
          {t("routingDecision.pausedHint")}
        </p>
      ) : null}
    </div>
  );
}
