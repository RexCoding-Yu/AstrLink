export const LOCAL_CLIENT_IDS = ["codex", "claude"] as const;
export type LocalClientId = (typeof LOCAL_CLIENT_IDS)[number];
export const LOCAL_CLIENT_PHASES = [
  "idle",
  "checking",
  "not_installed",
  "available",
  "up_to_date",
  "manual",
  "updating",
  "updated",
  "error",
] as const;
export type LocalClientPhase = (typeof LOCAL_CLIENT_PHASES)[number];
export interface LocalClientStatus {
  id: LocalClientId;
  phase: LocalClientPhase;
  current_version: string | null;
  latest_version: string | null;
  install_method: "native" | "npm" | "homebrew" | "unknown";
  executable: string | null;
  other_installations: string[];
  can_update: boolean;
  checked_at: string | null;
  error_code: string | null;
  error_detail: string | null;
}
export interface LocalClientSnapshot {
  revision: number;
  busy: boolean;
  clients: LocalClientStatus[];
}
export const localClientLabel = (id: LocalClientId) =>
  id === "codex" ? "Codex CLI" : "Claude Code";
export const localClientGuide = (id: LocalClientId) =>
  id === "codex"
    ? "https://developers.openai.com/codex/cli/"
    : "https://code.claude.com/docs/en/setup";

export function emptyLocalClients(): LocalClientSnapshot {
  return {
    revision: 0,
    busy: false,
    clients: LOCAL_CLIENT_IDS.map((id) => ({
      id,
      phase: "idle",
      current_version: null,
      latest_version: null,
      install_method: "unknown",
      executable: null,
      other_installations: [],
      can_update: false,
      checked_at: null,
      error_code: null,
      error_detail: null,
    })),
  };
}

export function parseLocalClients(value: unknown): LocalClientSnapshot {
  function object(item: unknown): Record<string, unknown> {
    if (!item || typeof item !== "object" || Array.isArray(item))
      throw new Error("Invalid local client response");
    return item as Record<string, unknown>;
  }
  const root = object(value);
  if (
    !Number.isSafeInteger(root.revision) ||
    (root.revision as number) < 0 ||
    typeof root.busy !== "boolean" ||
    !Array.isArray(root.clients) ||
    root.clients.length !== 2
  ) {
    throw new Error("Invalid local client snapshot");
  }
  const ids = new Set();
  for (const item of root.clients) {
    const client = object(item);
    if (
      !LOCAL_CLIENT_IDS.includes(client.id as LocalClientId) ||
      ids.has(client.id) ||
      !LOCAL_CLIENT_PHASES.includes(client.phase as LocalClientPhase) ||
      !["native", "npm", "homebrew", "unknown"].includes(
        String(client.install_method),
      ) ||
      typeof client.can_update !== "boolean"
    )
      throw new Error("Invalid local client status");
    ids.add(client.id);
    for (const key of [
      "current_version",
      "latest_version",
      "executable",
      "checked_at",
      "error_code",
      "error_detail",
    ]) {
      if (
        client[key] !== null &&
        (typeof client[key] !== "string" ||
          (client[key] as string).length > 8192)
      )
        throw new Error(`Invalid local client ${key}`);
    }
    if (
      !Array.isArray(client.other_installations) ||
      client.other_installations.length > 256 ||
      client.other_installations.some(
        (p) => typeof p !== "string" || p.length > 8192,
      )
    ) {
      throw new Error("Invalid local client installations");
    }
  }
  return root as unknown as LocalClientSnapshot;
}

export function acceptLocalClients(
  current: LocalClientSnapshot,
  next: LocalClientSnapshot,
): LocalClientSnapshot {
  return next.revision >= current.revision ? next : current;
}
