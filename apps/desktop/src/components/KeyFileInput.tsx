import { useEffect, useRef, useState, type ReactNode } from "react";

import { Key } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";

/**
 * A credential that arrives as a file, such as a JSON key: paste its text or
 * choose the file. Once the page recognises the key it passes `loaded`, and a
 * row naming the key takes the place of the text, which keeps the private key
 * off the screen and out of screenshots. `saved` names a stored key the same
 * way while nothing new is entered. Put it in a `Field` with `group` set.
 */
export function KeyFileInput({
  accept,
  chooseLabel,
  label,
  loaded,
  maxLength,
  onChange,
  placeholder,
  replaceLabel,
  saved,
  value,
}: {
  accept: string;
  chooseLabel: string;
  /** Accessible name of the text area. */
  label: string;
  loaded: ReactNode | null;
  maxLength: number;
  onChange: (value: string) => void;
  placeholder: string;
  replaceLabel: string;
  saved: ReactNode | null;
  value: string;
}) {
  const [replacing, setReplacing] = useState(false);
  const textArea = useRef<HTMLTextAreaElement>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const summary = loaded ?? (value === "" && !replacing ? saved : null);

  useEffect(() => {
    if (replacing && !summary) textArea.current?.focus({ preventScroll: true });
  }, [replacing, summary]);

  if (summary) {
    return (
      <div className="flex min-w-0 items-center gap-2.5 rounded-md border border-input bg-card px-2.5 py-2">
        <Key aria-hidden="true" className="size-4 shrink-0 text-primary" />
        <div className="min-w-0 flex-1 text-sm">{summary}</div>
        <Button
          onClick={() => {
            if (loaded) onChange("");
            setReplacing(true);
          }}
          size="sm"
          type="button"
          variant="ghost"
        >
          {replaceLabel}
        </Button>
      </div>
    );
  }
  return (
    <div className="grid min-w-0 gap-2">
      <Textarea
        ref={textArea}
        aria-label={label}
        autoComplete="off"
        className="min-h-18 resize-none font-mono text-xs field-sizing-fixed placeholder:font-sans"
        maxLength={maxLength}
        placeholder={placeholder}
        rows={3}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
      <input
        ref={fileInput}
        accept={accept}
        hidden
        type="file"
        onChange={(event) => {
          const file = event.currentTarget.files?.[0];
          // Choosing the same file again still fires a change.
          event.currentTarget.value = "";
          if (file) void file.slice(0, maxLength).text().then(onChange);
        }}
      />
      <div>
        <Button
          onClick={() => fileInput.current?.click()}
          size="sm"
          type="button"
          variant="outline"
        >
          {chooseLabel}
        </Button>
      </div>
    </div>
  );
}
