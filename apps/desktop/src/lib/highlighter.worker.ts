import {
  tokenizeCode,
  type HighlightRequest,
  type HighlightResponse,
} from "./highlighter-tokens";

// Tokenizing happens here, off the page: the first pass over a large grammar
// would otherwise freeze the window for most of a second.
self.addEventListener("message", (event: MessageEvent<HighlightRequest>) => {
  const { id, code, language } = event.data;
  const reply = (lines: HighlightResponse["lines"]) =>
    self.postMessage({ id, lines } satisfies HighlightResponse);
  tokenizeCode(code, language).then(reply, () => reply(null));
});
