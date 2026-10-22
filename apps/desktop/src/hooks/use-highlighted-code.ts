import * as React from "react";

import { highlightCode, peekHighlightedCode } from "@/lib/highlighter";

/**
 * One node per line of `value`. Lines are plain text until the highlighter
 * answers, and stay plain for a language it does not list. Code highlighted
 * before, such as a row opened again, is colored from the first paint.
 */
export function useHighlightedCode(value: string, language: string) {
  // A trailing newline would render as an extra empty line.
  const code = value.endsWith("\n") ? value.slice(0, -1) : value;
  const [loaded, setLoaded] = React.useState<{
    code: string;
    language: string;
    lines: React.ReactNode[];
  } | null>(null);

  React.useEffect(() => {
    if (peekHighlightedCode(code, language) !== undefined) {
      return;
    }

    let cancelled = false;

    void highlightCode(code, language).then((lines) => {
      if (!cancelled && lines) {
        setLoaded({ code, language, lines });
      }
    });

    return () => {
      cancelled = true;
    };
  }, [code, language]);

  return React.useMemo(() => {
    const cached = peekHighlightedCode(code, language);

    if (cached) {
      return cached;
    }

    if (loaded?.code === code && loaded.language === language) {
      return loaded.lines;
    }

    return code.split("\n");
  }, [code, language, loaded]);
}
