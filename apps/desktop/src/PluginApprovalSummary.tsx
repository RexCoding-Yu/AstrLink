import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";

import { useT } from "./i18n";
import { capabilityLabel } from "./plugin-labels";
import {
  HIGH_RISK_CAPABILITIES,
  type PluginApproval,
  type PluginDetail,
} from "./plugin-model";

/**
 * What a change grants a plugin, for the user to confirm: the capabilities it
 * adds, each one the user may untick, the addresses it may reach, and what
 * reaches past the sandbox, in red. detail, when it could be read, adds the
 * plugin's own words: its notice, the network's purpose, its attributes.
 */
export function PluginApprovalSummary({
  approval,
  detail,
  kept,
  onKept,
}: {
  approval: PluginApproval;
  detail?: PluginDetail | null;
  kept: string[];
  onKept: (kept: string[]) => void;
}) {
  const t = useT();
  const added = approval.added_capabilities ?? [];
  const origins = approval.added_origins ?? [];
  const plaintext = approval.plaintext ?? [];
  return (
    <div
      className="grid gap-3 text-sm text-foreground"
      data-testid="plugin-approval-summary"
    >
      {added.length > 0 ? (
        <fieldset className="grid gap-1.5">
          <legend className="mb-1 text-xs text-muted-foreground">
            {approval.enable
              ? t("plugins.approval.capabilitiesEnable")
              : t("plugins.approval.capabilitiesAdded")}
          </legend>
          {added.map((capability) => {
            const risky = HIGH_RISK_CAPABILITIES.includes(capability);
            return (
              <Label
                className={cn(
                  "flex items-start gap-2 text-sm font-normal",
                  risky && "text-danger-foreground",
                )}
                key={capability}
              >
                <Checkbox
                  checked={kept.includes(capability)}
                  className="mt-0.5"
                  onCheckedChange={(checked) =>
                    onKept(
                      checked === true
                        ? [...kept, capability]
                        : kept.filter((item) => item !== capability),
                    )
                  }
                />
                <span className="grid gap-0.5">
                  <span>{capabilityLabel(capability, t)}</span>
                  {risky ? (
                    <span className="text-xs">
                      {t(
                        `plugins.approval.risk.${capability.replace(".", "_")}`,
                      )}
                    </span>
                  ) : null}
                </span>
              </Label>
            );
          })}
        </fieldset>
      ) : approval.enable ? (
        <p className="text-xs text-muted-foreground">
          {t("plugins.approval.noCapabilities")}
        </p>
      ) : null}
      {origins.length > 0 ? (
        <section className="grid gap-1">
          <h3 className="text-xs text-muted-foreground">
            {t("plugins.approval.origins")}
          </h3>
          <ul className="grid gap-0.5">
            {origins.map((origin) => (
              <li
                className={cn(
                  "font-mono text-xs break-all",
                  plaintext.includes(origin) && "text-danger-foreground",
                )}
                key={origin}
              >
                {origin}
                {plaintext.includes(origin)
                  ? ` · ${t("plugins.approval.plaintext")}`
                  : ""}
              </li>
            ))}
          </ul>
        </section>
      ) : null}
      {plaintext.some((origin) => !origins.includes(origin)) ? (
        <p className="text-xs text-danger-foreground">
          {t("plugins.approval.plaintextExisting", {
            origins: plaintext
              .filter((origin) => !origins.includes(origin))
              .join(", "),
          })}
        </p>
      ) : null}
      {approval.trusted ? (
        <p className="text-xs text-danger-foreground">
          {t("plugins.approval.trusted")}
        </p>
      ) : null}
      {approval.fail_closed ? (
        <p className="text-xs text-danger-foreground">
          {t("plugins.approval.failClosed")}
        </p>
      ) : null}
      {detail?.network_purpose && (origins.length > 0 || approval.enable) ? (
        <p className="text-xs">
          {t("plugins.enableDialog.purpose", {
            purpose: detail.network_purpose,
          })}
        </p>
      ) : null}
      {detail?.notice && approval.enable ? (
        <div className="grid gap-1">
          <span className="text-xs font-medium">
            {t("plugins.enableDialog.notice")}
          </span>
          <p className="text-xs leading-5 whitespace-pre-wrap">
            {detail.notice}
          </p>
        </div>
      ) : null}
      {detail && detail.metadata.length > 0 && approval.enable ? (
        <div className="grid gap-1">
          <span className="text-xs font-medium">
            {t("plugins.enableDialog.metadata")}
          </span>
          <ul className="grid gap-0.5 text-xs">
            {detail.metadata.map((attribute) => (
              <li key={attribute.key}>
                {attribute.label}
                {attribute.marker
                  ? ` · ${t("plugins.enableDialog.marker", { marker: attribute.marker })}`
                  : ""}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </div>
  );
}
