import { vi } from "vitest";

import {
  tokenizeCode,
  type HighlightRequest,
  type HighlightResponse,
} from "./highlighter-tokens";

/**
 * Stands in for the highlighter worker, which a test DOM cannot start. It
 * tokenizes in-process and holds every answer until `release`, so a test can
 * see the code before it is colored. Install it before anything highlights.
 */
export function installHighlighterWorker() {
  const requests: HighlightRequest[] = [];
  const answers: Promise<void>[] = [];
  let release = () => {};
  let held = Promise.resolve();

  class HighlighterWorker extends EventTarget {
    postMessage(request: HighlightRequest) {
      requests.push(request);
      answers.push(
        held
          .then(() => tokenizeCode(request.code, request.language))
          .then((lines) => {
            const data: HighlightResponse = { id: request.id, lines };
            this.dispatchEvent(new MessageEvent("message", { data }));
          }),
      );
    }

    terminate() {}
  }

  vi.stubGlobal("Worker", HighlighterWorker);
  return {
    requests,
    /** Holds the answers to requests from now on, until `release`. */
    hold() {
      held = new Promise((resolve) => {
        release = resolve;
      });
    },
    release: () => release(),
    /** Settles once every request so far has its answer. */
    answered: () => Promise.all(answers),
  };
}
