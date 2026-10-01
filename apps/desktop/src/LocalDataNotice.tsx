import { useEffect, useState } from "react";

import { FormMessage } from "@/components/FormMessage";
import { Button } from "@/components/ui/button";

import { getLocalDataStatus } from "./bridge";
import { useT } from "./i18n";

/**
 * Says that saved credentials no longer decrypt on this device (plan §5.7):
 * the data directory came from another device, or the local key is gone.
 * The affected services only need their credentials entered again.
 */
export function LocalDataNotice({
  coreSessionKey,
  dismissed,
  onDismiss,
  onOpenServices,
}: {
  /** Null while Core is not ready; a new key reads the status again. */
  coreSessionKey: string | null;
  dismissed: boolean;
  onDismiss: () => void;
  onOpenServices: () => void;
}) {
  const t = useT();
  const [unreadable, setUnreadable] = useState(0);

  useEffect(() => {
    if (coreSessionKey === null || dismissed) return;
    let current = true;
    getLocalDataStatus()
      .then((status) => {
        if (current) setUnreadable(status.unreadable_credentials);
      })
      // The notice is advisory; a Core without the status simply has none.
      .catch(() => {
        if (current) setUnreadable(0);
      });
    return () => {
      current = false;
    };
  }, [coreSessionKey, dismissed]);

  if (dismissed || coreSessionKey === null || unreadable === 0) return null;
  return (
    <FormMessage
      className="flex flex-wrap items-center gap-x-2 gap-y-1 py-1.5"
      data-slot="local-data-notice"
      tone="warning"
    >
      <span className="min-w-0 flex-1">
        {t("settings.localDataUnreadable", { count: unreadable })}
      </span>
      <span className="ml-auto flex shrink-0 items-center gap-1">
        <Button onClick={onOpenServices} size="xs" type="button">
          {t("settings.localDataOpenServices")}
        </Button>
        <Button onClick={onDismiss} size="xs" type="button" variant="ghost">
          {t("settings.localDataDismiss")}
        </Button>
      </span>
    </FormMessage>
  );
}
