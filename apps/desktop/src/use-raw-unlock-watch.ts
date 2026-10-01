import { useEffect, useRef, useState } from "react";

import { getRawSealingStatus, listenRawSealingChanged } from "./bridge";
import { unlockCheckDelay } from "./raw-sealing-model";

/** Retry for a sealing read that failed while raw parts may be on screen. */
export const UNLOCK_READ_RETRY_MS = 15_000;

/** What the last sealing read said; "unread" before the first one lands. */
type UnlockSeen = "unread" | "open" | "closed" | "unreadable";

/** What the watched window has on screen. */
export interface ShownParts {
  /** Raw parts, which Core shows only to an open unlock once sealed. */
  holdsRaw: () => boolean;
  /** Parts left out until raw reading is unlocked. */
  holdsLocked: () => boolean;
}

/**
 * Watches the operator's raw unlock for a window that shows raw parts but
 * does not own the unlock, such as a pinned inspector. The sealing state is
 * read again when the known unlock should end and whenever any window locks,
 * unlocks, or changes the raw password; reading it never extends the unlock.
 *
 * `onChange` asks the caller to drop the content and fetch it again: when an
 * unlock the raw parts on screen may have been read under is over or cannot
 * be confirmed, and when an unlock opens over locked parts. It runs on a
 * change of the unlock, never on every read, so a part Core keeps showing or
 * keeps leaving out cannot loop.
 *
 * The watch starts once `ready` is first true — when the window's first
 * content has settled, so the first read judges what is on screen — and then
 * keeps running across refetches.
 */
export function useRawUnlockWatch(
  ready: boolean,
  shown: ShownParts,
  onChange: () => void,
): void {
  const shownRef = useRef(shown);
  const onChangeRef = useRef(onChange);
  shownRef.current = shown;
  onChangeRef.current = onChange;
  const [started, setStarted] = useState(false);
  if (ready && !started) setStarted(true);

  useEffect(() => {
    if (!started) return;
    let active = true;
    let timer: number | undefined;
    let seen: UnlockSeen = "unread";
    // Reads can overlap (an event beside the expiry check); the latest wins.
    let latest = 0;

    const settle = (next: UnlockSeen) => {
      const previous = seen;
      seen = next;
      if (next === "open") {
        if (previous !== "open" && shownRef.current.holdsLocked()) {
          onChangeRef.current();
        }
        return;
      }
      // Raw parts on screen before the first read, or refetched while the
      // state could not be read, may have come from an unlock that is gone.
      const readUnlocked =
        previous === "open" ||
        ((previous === "unread" ||
          (previous === "unreadable" && next === "closed")) &&
          shownRef.current.holdsRaw());
      if (readUnlocked) onChangeRef.current();
    };

    const check = () => {
      window.clearTimeout(timer);
      timer = undefined;
      const read = ++latest;
      getRawSealingStatus().then(
        (status) => {
          if (!active || read !== latest) return;
          // Without a raw key, raw parts need no unlock: nothing can end.
          if (!status.configured) {
            settle("open");
            return;
          }
          if (status.unlocked) {
            if (status.unlock_expires_at) {
              timer = window.setTimeout(
                check,
                unlockCheckDelay(status.unlock_expires_at),
              );
            }
            settle("open");
            return;
          }
          settle("closed");
        },
        () => {
          if (!active || read !== latest) return;
          const previous = seen;
          settle("unreadable");
          // Nothing on screen needs a retry once the unlock was seen closed.
          if (previous !== "closed") {
            timer = window.setTimeout(check, UNLOCK_READ_RETRY_MS);
          }
        },
      );
    };

    let stop: (() => void) | null = null;
    listenRawSealingChanged(check).then(
      (unlisten) => {
        if (active) stop = unlisten;
        else unlisten();
      },
      (error: unknown) => {
        console.error("AstrLink cannot watch the raw unlock", error);
      },
    );
    check();
    return () => {
      active = false;
      window.clearTimeout(timer);
      stop?.();
    };
  }, [started]);
}
