import { clientLabel, clientSetupClients } from "../ClientSetupDialog";
import type { DirectClient } from "../client-config-model";
import { currentLocale, useT } from "../i18n";
import { cn } from "@/lib/utils";

/** The clients a token is configured in, shown by their icons. */
export function TokenClientMarks({
  className,
  clients,
}: {
  className?: string;
  clients: readonly DirectClient[];
}) {
  const t = useT();
  if (clients.length === 0) return null;
  const label = t("clientSetup.configuredIn", {
    clients: clientNames(clients),
  });
  return (
    <span
      aria-label={label}
      className={cn("inline-flex shrink-0 items-center gap-1", className)}
      data-slot="token-client-marks"
      role="img"
      title={label}
    >
      {clientSetupClients
        .filter(({ id }) => clients.some((client) => client === id))
        .map(({ id, Icon }) => (
          <Icon key={id} aria-hidden="true" size={14} />
        ))}
    </span>
  );
}

export function clientNames(clients: readonly DirectClient[]): string {
  return new Intl.ListFormat(currentLocale(), { style: "narrow" }).format(
    clients.map(clientLabel),
  );
}
