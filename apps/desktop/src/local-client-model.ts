export const LOCAL_CLIENT_IDS = ["codex", "claude", "cursor", "pi"] as const;
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
const LOCAL_CLIENT_INSTALL_METHODS = [
  "native",
  "npm",
  "bun",
  "pnpm",
  "yarn",
  "homebrew",
  "unknown",
] as const;
export type LocalClientInstallMethod =
  (typeof LOCAL_CLIENT_INSTALL_METHODS)[number];
export interface LocalClientStatus {
  id: LocalClientId;
  /** The WSL distribution the client runs in; `null` for this computer. */
  wsl: string | null;
  phase: LocalClientPhase;
  current_version: string | null;
  latest_version: string | null;
  install_method: LocalClientInstallMethod;
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
  /** This computer's four clients, then those found in WSL distributions. */
  clients: LocalClientStatus[];
  /** WSL distributions left unchecked because they were not running. */
  wsl_unchecked: string[];
}
/** A client on this computer (`wsl: null`) or in a WSL distribution. */
export type LocalClientTarget = Pick<LocalClientStatus, "id" | "wsl">;
export const localClientKey = ({ id, wsl }: LocalClientTarget) =>
  `${wsl ?? ""}/${id}`;
const LOCAL_CLIENTS = {
  codex: {
    label: "Codex CLI",
    guide: "https://developers.openai.com/codex/cli/",
  },
  claude: {
    label: "Claude Code",
    guide: "https://code.claude.com/docs/en/setup",
  },
  cursor: {
    label: "Cursor CLI",
    guide: "https://cursor.com/docs/cli/installation",
  },
  pi: { label: "Pi", guide: "https://pi.dev/docs/latest/quickstart" },
} satisfies Record<LocalClientId, { label: string; guide: string }>;
export const localClientLabel = (id: LocalClientId) => LOCAL_CLIENTS[id].label;
export const localClientGuide = (id: LocalClientId) => LOCAL_CLIENTS[id].guide;

export function emptyLocalClients(): LocalClientSnapshot {
  return {
    revision: 0,
    busy: false,
    clients: LOCAL_CLIENT_IDS.map((id) => ({
      id,
      wsl: null,
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
    wsl_unchecked: [],
  };
}

const distributionName = (value: unknown) =>
  typeof value === "string" && value.length > 0 && value.length <= 255;

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
    root.clients.length > 1024 ||
    !Array.isArray(root.wsl_unchecked) ||
    root.wsl_unchecked.length > 256 ||
    !root.wsl_unchecked.every(distributionName)
  ) {
    throw new Error("Invalid local client snapshot");
  }
  const keys = new Set<string>();
  for (const item of root.clients) {
    const client = object(item);
    const key = localClientKey(client as unknown as LocalClientTarget);
    if (
      !LOCAL_CLIENT_IDS.includes(client.id as LocalClientId) ||
      (client.wsl !== null && !distributionName(client.wsl)) ||
      keys.has(key) ||
      !LOCAL_CLIENT_PHASES.includes(client.phase as LocalClientPhase) ||
      !LOCAL_CLIENT_INSTALL_METHODS.includes(
        client.install_method as LocalClientInstallMethod,
      ) ||
      typeof client.can_update !== "boolean"
    )
      throw new Error("Invalid local client status");
    keys.add(key);
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
  // This computer's clients always appear, installed or not.
  if (
    LOCAL_CLIENT_IDS.some((id) => !keys.has(localClientKey({ id, wsl: null })))
  )
    throw new Error("Invalid local client snapshot");
  return root as unknown as LocalClientSnapshot;
}

export function acceptLocalClients(
  current: LocalClientSnapshot,
  next: LocalClientSnapshot,
): LocalClientSnapshot {
  return next.revision >= current.revision ? next : current;
}
