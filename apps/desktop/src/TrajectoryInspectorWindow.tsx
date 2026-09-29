import { useCallback, useEffect, useRef, useState } from "react";
import type { UnlistenFn } from "@tauri-apps/api/event";

import { Pin } from "@/components/icons";
import { Button } from "@/components/ui/button";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

import { getRequestAuditContent } from "./bridge";
import { useCopyFeedback } from "./copy-feedback";
import { i18n } from "./i18n";
import { notify } from "./notify";
import type { AuditContent, RequestRecord } from "./request-record-model";
import { TrajectoryInspector } from "./TrajectoryInspector";
import {
  listenInspectorSelection,
  setTrajectoryInspectorPinned,
  trajectoryInspectorState,
  type TrajectoryInspectorSelection,
} from "./trajectory-inspector-window";
import { WindowChromeAccessory } from "./WindowChrome";

/**
 * The whole app in a detached inspector window: one selected call, driven by
 * the main window over the event channel. The clicked chip is only a scroll
 * target; the pane always stacks that request's whole chain.
 *
 * Pinning floats the window above the others and freezes the call. The pin
 * sits in the title bar, where window-level controls belong, so it reads as
 * "keep this window on top" rather than as another control over the content.
 * The host stops routing selections here, and this side ignores any that
 * still arrive, so the two cannot disagree about what a pinned window shows.
 *
 * Audit content is decrypted here rather than forwarded, so captured bodies
 * never cross the channel and the main window's cache stays the main window's.
 */
export function TrajectoryInspectorWindow() {
  const t = i18n.t.bind(i18n);
  const [selection, setSelection] =
    useState<TrajectoryInspectorSelection | null>(null);
  const [pinned, setPinned] = useState(false);
  const pinnedRef = useRef(false);
  const copyFeedback = useCopyFeedback();

  pinnedRef.current = pinned;

  useEffect(() => {
    let active = true;
    let stop: UnlistenFn | null = null;
    void listenInspectorSelection((next) => {
      if (active && !pinnedRef.current) setSelection(next);
    })
      .then((unlisten) => {
        if (!active) {
          unlisten();
          return;
        }
        stop = unlisten;
        // Pulled rather than waited for, so there is no window in which the
        // host has already sent the phase and nobody was listening. This is
        // also what restores a pinned window after the dev host reloads it.
        return trajectoryInspectorState().then((state) => {
          if (!active) return;
          setPinned(state.pinned);
          if (state.selection) setSelection(state.selection);
        });
      })
      .catch((error: unknown) => {
        console.error("AstrLink inspector window cannot subscribe", error);
      });
    return () => {
      active = false;
      stop?.();
    };
  }, []);

  const togglePin = useCallback((next: boolean) => {
    // Optimistic, then corrected by whatever the host settled on: the button
    // has to answer the click even though the window level changes in Rust.
    setPinned(next);
    void setTrajectoryInspectorPinned(next)
      .then(setPinned)
      .catch((error: unknown) => {
        setPinned(!next);
        notify.error(i18n.t("trajectory.pinFailed"));
        console.error("AstrLink inspector window cannot change its pin", error);
      });
  }, []);

  const audit = useRequestAudit(
    selection?.record.id ?? null,
    selection?.record.status ?? null,
    auditCaptureKey(selection?.record ?? null),
  );

  return (
    <main className="flex h-dvh min-h-0 flex-col overflow-hidden pt-[var(--window-chrome-height)]">
      {selection ? (
        <WindowChromeAccessory>
          <PinToggle onToggle={togglePin} pinned={pinned} />
        </WindowChromeAccessory>
      ) : null}
      {selection ? (
        <TrajectoryInspector
          auditContent={
            audit.content?.request_id === selection.record.id
              ? audit.content
              : null
          }
          auditError={audit.error}
          auditLoading={audit.loading}
          copyFeedback={copyFeedback}
          pinned={pinned}
          record={selection.record}
          row={selection.row}
          service={selection.service}
          services={selection.services}
        />
      ) : (
        <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-1.5 p-8 text-center">
          <strong className="text-xs">
            {t("trajectory.inspectorWindowEmpty")}
          </strong>
          <span className="text-xs text-muted-foreground">
            {t("trajectory.inspectorWindowEmptyHint")}
          </span>
        </div>
      )}
    </main>
  );
}

/**
 * A loose, tilted pin while the window follows the list; pressed upright and
 * filled once it floats. Only the pinned state carries a label, so a floating
 * window says so at a glance while an ordinary one keeps a quiet title bar.
 */
function PinToggle({
  onToggle,
  pinned,
}: {
  onToggle: (next: boolean) => void;
  pinned: boolean;
}) {
  const t = i18n.t.bind(i18n);
  return (
    <TooltipProvider delayDuration={300}>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            aria-label={t("trajectory.pin")}
            aria-pressed={pinned}
            className={cn(
              pinned
                ? "bg-accent text-accent-foreground hover:bg-accent/70"
                : "text-muted-foreground hover:bg-foreground/8 hover:text-foreground",
            )}
            data-testid="trajectory-inspector-pin"
            onClick={() => onToggle(!pinned)}
            size={pinned ? "xs" : "icon-xs"}
            type="button"
            variant="ghost"
          >
            <Pin
              className={cn(
                "size-3.5 transition-transform duration-200",
                !pinned && "rotate-45",
              )}
              fill={pinned ? "currentColor" : "none"}
            />
            {pinned ? t("trajectory.pinned") : null}
          </Button>
        </TooltipTrigger>
        <TooltipContent className="max-w-60" side="bottom" sideOffset={6}>
          {pinned ? t("trajectory.unpinHint") : t("trajectory.pinHint")}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

interface AuditState {
  content: AuditContent | null;
  loading: boolean;
  error: string | null;
}

/**
 * Refetches when the request changes, when a running request settles, and
 * when a pending record's captured flags flip — request-side blobs can land
 * before the call finishes.
 */
function useRequestAudit(
  requestId: string | null,
  status: string | null,
  captureKey: string,
): AuditState {
  const [state, setState] = useState<AuditState>({
    content: null,
    loading: false,
    error: null,
  });

  useEffect(() => {
    if (!requestId) {
      setState({ content: null, loading: false, error: null });
      return;
    }
    let active = true;
    setState({ content: null, loading: true, error: null });
    void getRequestAuditContent(requestId)
      .then((content) => {
        if (active) setState({ content, loading: false, error: null });
      })
      .catch((requestError: unknown) => {
        if (!active) return;
        const message =
          requestError instanceof Error
            ? requestError.message
            : i18n.t("records.auditContentFailed");
        setState({
          content: null,
          loading: false,
          error: message.includes("409")
            ? i18n.t("records.auditKeyBroken")
            : message,
        });
      });
    return () => {
      active = false;
    };
  }, [requestId, status, captureKey]);

  return state;
}

function auditCaptureKey(record: RequestRecord | null): string {
  if (!record) return "";
  return [
    record.audit.request_body_captured,
    record.audit.response_content_captured,
    record.audit.upstream_request_body_captured,
    record.audit.upstream_response_content_captured,
  ].join(":");
}
