export type UpdateChannel = "stable" | "preview";
export interface UpdatePreferences {
  auto_check: boolean;
  auto_download: boolean;
  channel: UpdateChannel;
}
export const defaultUpdatePreferences = (): UpdatePreferences => ({
  auto_check: true,
  auto_download: true,
  channel: "stable",
});
export const UPDATE_PHASES = [
  "idle",
  "checking",
  "no_releases",
  "up_to_date",
  "available",
  "manual",
  "downloading",
  "ready",
  "installing",
  "error",
] as const;
export type UpdatePhase = (typeof UPDATE_PHASES)[number];
export interface UpdateRelease {
  version: string;
  notes: string;
  published_at: string | null;
  url: string;
}
export interface UpdateSnapshot {
  revision: number;
  current_version: string;
  latest_version: string | null;
  platform: string;
  arch: string;
  install_supported: boolean;
  configured: boolean;
  development: boolean;
  preferences: UpdatePreferences;
  phase: UpdatePhase;
  release: UpdateRelease | null;
  downloaded_bytes: number;
  total_bytes: number | null;
  last_checked_at: string | null;
  error_code: string | null;
  error_detail: string | null;
}
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error("Invalid update IPC object");
  return value as Record<string, unknown>;
}
export function parseUpdatePreferences(value: unknown): UpdatePreferences {
  const v = object(value);
  if (
    Object.keys(v).sort().join() !== "auto_check,auto_download,channel" ||
    typeof v.auto_check !== "boolean" ||
    typeof v.auto_download !== "boolean" ||
    !["stable", "preview"].includes(String(v.channel))
  )
    throw new Error("Invalid update preferences IPC");
  return v as unknown as UpdatePreferences;
}
export function parseUpdateSnapshot(value: unknown): UpdateSnapshot {
  const v = object(value);
  if (!UPDATE_PHASES.includes(v.phase as UpdatePhase))
    throw new Error("Invalid update phase");
  for (const key of ["current_version", "platform", "arch"])
    if (typeof v[key] !== "string") throw new Error(`Invalid update ${key}`);
  for (const key of ["install_supported", "configured", "development"])
    if (typeof v[key] !== "boolean") throw new Error(`Invalid update ${key}`);
  for (const key of ["revision", "downloaded_bytes", "total_bytes"]) {
    if (key === "total_bytes" && v[key] === null) continue;
    if (!Number.isSafeInteger(v[key]) || (v[key] as number) < 0)
      throw new Error(`Invalid update ${key}`);
  }
  if (
    v.latest_version !== undefined &&
    v.latest_version !== null &&
    typeof v.latest_version !== "string"
  )
    throw new Error("Invalid update latest_version");
  for (const key of ["last_checked_at", "error_code", "error_detail"])
    if (v[key] !== null && typeof v[key] !== "string")
      throw new Error(`Invalid update ${key}`);
  parseUpdatePreferences(v.preferences);
  if (v.release !== null) {
    const r = object(v.release);
    for (const key of ["version", "notes", "url"])
      if (typeof r[key] !== "string")
        throw new Error(`Invalid update release ${key}`);
    if (r.published_at !== null && typeof r.published_at !== "string")
      throw new Error("Invalid update release date");
    const url = new URL(String(r.url));
    if (
      url.origin !== "https://github.com" ||
      !url.pathname.startsWith("/Calcium-Ion/AstrLink/releases/tag/")
    )
      throw new Error("Invalid update release URL");
  }
  return {
    ...(v as unknown as UpdateSnapshot),
    latest_version: (v.latest_version as string | null | undefined) ?? null,
  };
}
export const browserUpdateSnapshot = (): UpdateSnapshot => ({
  revision: 0,
  current_version: "—",
  latest_version: null,
  platform: "browser",
  arch: "—",
  install_supported: false,
  configured: false,
  development: true,
  preferences: defaultUpdatePreferences(),
  phase: "idle",
  release: null,
  downloaded_bytes: 0,
  total_bytes: null,
  last_checked_at: null,
  error_code: null,
  error_detail: null,
});
export function updateBusy(snapshot: UpdateSnapshot): boolean {
  return ["checking", "downloading", "installing"].includes(snapshot.phase);
}
