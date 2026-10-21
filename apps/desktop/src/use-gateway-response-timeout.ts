import { useEffect } from "react";
import { getPreferences } from "./bridge";
import type { SettingsSnapshot } from "./preferences-model";
import { useWorkspaceSnapshot } from "./workspace-snapshots";

/**
 * The saved gateway response-start timeout in seconds, which a failure policy
 * without its own timeout inherits; null while unknown.
 */
export function useGatewayResponseTimeout(): number | null {
  const [settings, setSettings] = useWorkspaceSnapshot<SettingsSnapshot | null>(
    "preferences",
    null,
    "desktop",
  );
  useEffect(() => {
    let active = true;
    void (async () => {
      try {
        const loaded = await getPreferences();
        if (active) setSettings(loaded);
      } catch {
        /* The web console and browser preview have no desktop preferences. */
      }
    })();
    return () => {
      active = false;
    };
  }, [setSettings]);
  return settings?.values.response_start_timeout_seconds ?? null;
}
