import { i18n } from "./i18n";
import { ConsoleError } from "./web-transport";

/**
 * The Control API error code behind a failed call: inside a desktop host
 * error such as `POST … returned 409 Conflict: {"error":{"code":"…"}}`, or
 * carried by the web console's ConsoleError.
 */
export function controlErrorCode(error: unknown): string | null {
  if (error instanceof ConsoleError)
    return /^[a-z0-9_]{1,64}$/.test(error.code) ? error.code : null;
  const message = error instanceof Error ? error.message : String(error);
  const match = /"code"\s*:\s*"([a-z0-9_]{1,64})"/.exec(message);
  return match ? match[1] : null;
}

/**
 * The message for a failed Control API call. Core words its errors in
 * English; a code with a `controlErrors` entry is shown in the operator's
 * language instead. Other failures keep their own message, or `fallback`
 * when they have none.
 */
export function controlErrorMessage(error: unknown, fallback: string): string {
  const code = controlErrorCode(error);
  if (code && i18n.exists(`controlErrors.${code}`))
    return i18n.t(`controlErrors.${code}`);
  return (error instanceof Error && error.message) || fallback;
}
