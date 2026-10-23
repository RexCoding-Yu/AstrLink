import { GitHubRepoBadge } from "@/components/GitHubRepoBadge";
import { HelpDisclosure } from "@/components/HelpDisclosure";
import { StatusBadge } from "@/components/StatusBadge";
import { cn } from "@/lib/utils";

import { i18n, useT } from "./i18n";
import { capabilityLabel } from "./plugin-labels";
import {
  HIGH_RISK_CAPABILITIES,
  type PluginInstallFailure,
  type PluginInstallReview,
  type PluginInstallSource,
} from "./plugin-model";

type Translate = ReturnType<typeof useT>;

/** A size in KB or MB. */
export function formatSize(bytes: number): string {
  if (bytes < 1024 * 1024) return `${Math.max(1, Math.round(bytes / 1024))} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

/** A moment as a date in the interface language. */
export function formatDay(iso: string | undefined): string {
  if (!iso) return "";
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return "";
  return new Intl.DateTimeFormat(i18n.language, { dateStyle: "medium" }).format(
    at,
  );
}

/**
 * What a plugin installed from source follows, in words: the tag or the
 * branch and when its commit was made. A commit reads by its date alone.
 */
export function sourceRef(source: PluginInstallSource, t: Translate): string {
  const day = formatDay(source.committed_at);
  const ref =
    source.ref_kind === "commit"
      ? t("plugins.install.refCommit")
      : t(`plugins.install.ref.${source.ref_kind}`, { ref: source.ref ?? "" });
  return day ? t("plugins.install.refAt", { ref, day }) : ref;
}

/** Why an install could not go on, in the user's words. */
export function installFailureText(
  failure: PluginInstallFailure,
  t: Translate,
): string {
  return t(`plugins.install.failures.${failure.reason}`, {
    defaultValue: t("plugins.install.failures.unknown", {
      reason: failure.reason,
    }),
    detail: failure.detail ?? "",
  });
}

/** The repository, and the plugin's directory in it. */
function SourceLine({ source }: { source: PluginInstallSource }) {
  const t = useT();
  const repository = `https://github.com/${source.repository}`;
  return (
    <section className="grid gap-1">
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <GitHubRepoBadge
          href={repository}
          label={t("plugins.install.openRepository", {
            repository: source.repository,
          })}
        />
        <span className="min-w-0 font-medium break-all">
          {source.repository}
          {source.subdir ? ` / ${source.subdir}` : ""}
        </span>
      </div>
      <p className="text-xs text-muted-foreground">{sourceRef(source, t)}</p>
    </section>
  );
}

/**
 * What the user reviews before code goes in place: where it comes from, what
 * it replaces and changes, what it asks for, the interface code it brings,
 * and the packages built into it. What reaches past the sandbox, or
 * replaces another plugin, is in red.
 */
export function PluginInstallReviewView({
  review,
  rollback = false,
}: {
  review: PluginInstallReview;
  rollback?: boolean;
}) {
  const t = useT();
  const replaces = review.replaces;
  const update =
    replaces?.origin === "source" &&
    replaces.repository === review.source.repository;
  const risks: string[] = [];
  if (replaces && !update && !rollback)
    risks.push(
      t(`plugins.install.replaces.${replaces.origin}`, {
        name: replaces.name,
        repository: replaces.repository ?? "",
      }),
    );
  if (review.client) risks.push(t("plugins.install.client"));
  const changes = review.changes;
  const changeLines: { text: string; risky?: boolean }[] = [];
  if (changes && (update || rollback)) {
    for (const capability of changes.added_capabilities ?? [])
      changeLines.push({
        text: t("plugins.install.changes.addedCapability", {
          capability: capabilityLabel(capability, t),
        }),
        risky: HIGH_RISK_CAPABILITIES.includes(capability),
      });
    for (const capability of changes.removed_capabilities ?? [])
      changeLines.push({
        text: t("plugins.install.changes.removedCapability", {
          capability: capabilityLabel(capability, t),
        }),
      });
    if (changes.client)
      changeLines.push({
        text: t(`plugins.install.changes.client.${changes.client}`),
        risky: changes.client === "added",
      });
    if (changes.network_purpose_changed)
      changeLines.push({ text: t("plugins.install.changes.network") });
    const dependencies =
      (changes.added_dependencies?.length ?? 0) +
      (changes.changed_dependencies?.length ?? 0) +
      (changes.removed_dependencies?.length ?? 0);
    if (dependencies > 0)
      changeLines.push({
        text: t("plugins.install.changes.dependencies", {
          count: dependencies,
        }),
      });
  }
  const scripts = (review.warnings ?? []).filter(
    (warning) => warning.code === "install_scripts_skipped",
  );
  const otherWarnings = (review.warnings ?? []).filter(
    (warning) => warning.code !== "install_scripts_skipped",
  );
  return (
    <div
      className="grid gap-3 text-sm text-foreground"
      data-testid="plugin-install-review"
    >
      <SourceLine source={review.source} />
      {update || rollback ? (
        <p className="text-xs">
          {t(
            rollback
              ? "plugins.install.rollbackFrom"
              : "plugins.install.updateFrom",
            {
              from: changes?.from_version || replaces?.version || "",
              to: review.version,
            },
          )}
        </p>
      ) : null}
      {risks.map((risk) => (
        <p className="text-xs text-danger-foreground" key={risk}>
          {risk}
        </p>
      ))}
      {changeLines.length > 0 ? (
        <section className="grid gap-1">
          <h3 className="text-xs text-muted-foreground">
            {t("plugins.install.changesTitle")}
          </h3>
          <ul className="grid gap-0.5 text-xs">
            {changeLines.map((line) => (
              <li
                className={cn(line.risky && "text-danger-foreground")}
                key={line.text}
              >
                {line.text}
              </li>
            ))}
          </ul>
        </section>
      ) : null}
      {review.description ? (
        <p className="text-xs leading-5">{review.description}</p>
      ) : null}
      <section className="grid gap-1.5">
        <h3 className="text-xs text-muted-foreground">
          {t("plugins.install.capabilities")}
        </h3>
        {review.capabilities.length === 0 ? (
          <p className="text-xs">{t("plugins.noCapabilities")}</p>
        ) : (
          <div className="flex flex-wrap gap-1.5">
            {review.capabilities.map((capability) => (
              <StatusBadge
                key={capability}
                tone={
                  HIGH_RISK_CAPABILITIES.includes(capability)
                    ? "negative"
                    : "neutral"
                }
              >
                {capabilityLabel(capability, t)}
              </StatusBadge>
            ))}
          </div>
        )}
      </section>
      {review.network_purpose ? (
        <section className="grid gap-1">
          <h3 className="text-xs text-muted-foreground">
            {t("plugins.about.network")}
          </h3>
          <p className="text-xs">{review.network_purpose}</p>
        </section>
      ) : null}
      <section className="grid gap-1">
        <h3 className="text-xs text-muted-foreground">
          {t("plugins.install.files")}
        </h3>
        <p className="text-xs">
          {[
            t("plugins.install.code", {
              size: formatSize(
                review.plugin_bytes + (review.client_bytes ?? 0),
              ),
            }),
            review.dependencies.length > 0
              ? t("plugins.install.dependencies", {
                  count: review.dependencies.length,
                  size: formatSize(review.dependency_bytes),
                })
              : t("plugins.install.noDependencies"),
            review.author
              ? t("plugins.install.author", { author: review.author })
              : "",
          ]
            .filter(Boolean)
            .join(" · ")}
        </p>
        {review.dependencies.length > 0 ? (
          <HelpDisclosure title={t("plugins.install.dependencyList")}>
            <ul className="grid gap-0.5 font-mono">
              {review.dependencies.map((dependency) => (
                <li key={`${dependency.name}@${dependency.version}`}>
                  {dependency.name} {dependency.version}
                </li>
              ))}
            </ul>
          </HelpDisclosure>
        ) : null}
      </section>
      {scripts.length > 0 ? (
        <p className="text-xs text-muted-foreground">
          {t("plugins.install.scripts", {
            packages: scripts.map((warning) => warning.package).join(", "),
          })}
        </p>
      ) : null}
      {otherWarnings.map((warning, index) => (
        <p
          className="text-xs break-words text-warning-foreground"
          key={`${warning.code}-${index}`}
        >
          {t(`plugins.install.warnings.${warning.code}`, {
            defaultValue: warning.detail ?? warning.code,
            detail: warning.detail ?? "",
          })}
        </p>
      ))}
    </div>
  );
}
