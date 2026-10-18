// PowerContext memory: recalls a project's memory at the start of each turn,
// puts its profile in the system prompt, and captures what the user typed.
// It talks to PowerContext 1.3 or later through astrlink.http.fetch, which
// reaches only the origins the user allows and adds the token itself.

interface Config {
  /** Must be one of the origins allowed on the plugin page. */
  serverUrl?: string;
  /** Results per family; 0 turns a family off. */
  quotas?: Record<string, number>;
  /** 0..1 floors for the families that return scores; atomic-memory has none. */
  minScore?: Record<string, number>;
  /** Cosine floor topic-memory passes to the server's admission. */
  minSimilarity?: number;
  /** Queries shorter than this recall nothing. */
  minQueryChars?: number;
  /** The project's profile in the system prompt once the session has a project. */
  resident?: boolean;
  /** Ask the model to find out the project while the session has none. */
  askProject?: boolean;
  /** Known project names to suggest when asking; they reach the upstream model. */
  suggestProjects?: number;
  /** Ask the server to process a capture at once; PowerContext advises against it. */
  flushOnCapture?: boolean;
}

interface SearchItem {
  artifact_id: string;
  revision: number;
  content: Record<string, string>;
  scores?: { retrieval: number };
}

export const meta = {
  apiVersion: 1,
  key: "powercontext",
  name: "PowerContext 记忆",
  version: "1.0.0",
  capabilities: [
    "context.inject",
    "request.read",
    "network",
    "storage",
    "ui.views",
  ],
  network: { purpose: "访问 PowerContext 服务器，地址由用户在插件页授权" },
  credentials: [
    {
      name: "token",
      label: "PowerContext 令牌",
      header: "Authorization",
      scheme: "Bearer",
    },
  ],
  metadata: [
    {
      key: "project",
      label: "项目",
      pattern: "^[a-z0-9][a-z0-9._-]{0,63}$",
      marker: "memory-project",
    },
  ],
  notice:
    "启用后，新会话还没有项目时，会请模型问一次用户：是否把当前项目纳入记忆、项目名是什么。" +
    "用户回答后，模型的回复里会出现一行 <memory-project>项目名</memory-project>，网关据此绑定本会话。" +
    "之后每轮开始时召回这个项目的记忆，并把用户输入的文字记进 PowerContext。",
};

const DEFAULT_QUOTAS: Record<string, number> = {
  "atomic-memory": 4,
  "topic-memory": 3,
  skill: 2,
};
const SOURCE_LABELS: Record<string, string> = {
  header: "请求头",
  session: "会话标记",
  token: "令牌默认值",
};
const FETCH_ERRORS: Record<string, string> = {
  timeout: "超时",
  network: "连不上",
  denied: "地址未授权",
  budget: "超出等待时间",
  too_large: "响应过大",
};
// Searches and the profile snapshot run while the request waits, at most
// 1.5 s in all; the host cuts a request off sooner when that time runs out.
// Parallel searches queue on the server, so each gets most of the wait.
const WAIT_TIMEOUT_MS = 1400;

function config(): Config {
  return astrlink.config.get() as Config;
}

// Neutral wording: nothing sent upstream names the gateway. The host reads
// the marker line from the reply and binds the session.
function askText(cfg: Config): string {
  const known = astrlink.store
    .keys("scope:")
    .map((key) => key.slice(6))
    .slice(0, cfg.suggestProjects ?? 0);
  return (
    "本会话还没有选定项目记忆。不要打断当前任务，在本轮回复的末尾问用户：是否把当前项目纳入记忆；纳入的话，项目名用什么（只用小写字母、数字、点、下划线、短横线）。" +
    (known.length > 0 ? "已有项目：" + known.join("、") + "。" : "") +
    "用户回答后（或用户已经说了项目名），在回复开头单独一行原样输出 <memory-project>项目名</memory-project>；不纳入就输出 <memory-project>none</memory-project>。" +
    "这一行不要放进代码块，也不要加引号、反引号或加粗。用户还没回答时不要输出；对话里已经有这一行，或者你已经问过一次，就不要再问。" +
    "如果你没法直接问用户（比如你是子代理，或者在无人值守地运行），就不要问，也不要输出这一行。"
  );
}

function request(
  cfg: Config,
  method: "GET" | "POST",
  path: string,
  json?: unknown,
  timeoutMs = 1000,
): AstrLinkPlugin.HttpRequest {
  const base = (cfg.serverUrl ?? "").replace(/\/+$/, "");
  const sent: AstrLinkPlugin.HttpRequest = {
    method,
    url: base + path,
    credential: "token",
    timeoutMs,
  };
  if (json !== undefined) sent.json = json;
  return sent;
}

function supported(version: string): boolean {
  const [major, minor] = version.split(".").map((part) => parseInt(part, 10));
  return major > 1 || (major === 1 && minor >= 3);
}

// The version the server reported in the last 10 minutes, if any.
function knownVersion(): string | null {
  const cached = astrlink.store.get("server") as {
    version: string;
    at: number;
  } | null;
  if (cached !== null && astrlink.clock.now() - cached.at < 600000)
    return cached.version;
  return null;
}

// Keeps the version a server-info response reports; false when the server
// did not answer or is older than 1.3.
function noteVersion(response: AstrLinkPlugin.HttpResponse): boolean {
  if (response.status !== 200) return false;
  const version = String(
    (response.json as { package_version?: string }).package_version ?? "",
  );
  astrlink.store.set("server", { version, at: astrlink.clock.now() });
  return supported(version);
}

// Only 1.3 and later; the answer is kept for 10 minutes.
function serverReady(cfg: Config): boolean {
  if (!cfg.serverUrl) return false;
  const known = knownVersion();
  if (known !== null) return supported(known);
  return noteVersion(
    astrlink.http.fetch(request(cfg, "GET", "/v1/server-info")),
  );
}

// Project → scope. The server issues scope ids; a retry with the same
// idempotency key returns the same scope.
function scopeFor(cfg: Config, project: string): string | null {
  const cached = astrlink.store.get("scope:" + project);
  if (typeof cached === "string") return cached;
  const response = astrlink.http.fetch(
    request(
      cfg,
      "POST",
      "/v1/scopes",
      {
        title: project,
        summary: "项目 " + project + " 的记忆",
        idempotency_key: "astrlink:project:" + project,
      },
      3000,
    ),
  );
  if (response.status !== 201 && response.status !== 200) return null;
  const scope = (response.json as { scope_id?: string }).scope_id;
  if (typeof scope !== "string" || scope === "") return null;
  astrlink.store.set("scope:" + project, scope);
  return scope;
}

// Computed on a session's first request, again on each turn while the session
// has no project, and when its project changes. The same result keeps the same
// bytes, so the prompt cache holds.
export function prepareSession(
  view: AstrLinkPlugin.SessionView,
): AstrLinkPlugin.PrepareSessionResult | undefined {
  const cfg = config();
  const project = view.meta.project;
  if (project === undefined) {
    // The client's history keeps nothing the gateway adds, and the user may
    // answer any turn later: every turn carries the same ask until the session
    // has a value.
    if (cfg.askProject === false) return undefined;
    return { system: { items: [askText(cfg)] } };
  }
  if (project.value === "none" || cfg.resident === false || !serverReady(cfg))
    return undefined;
  const scope = scopeFor(cfg, project.value);
  if (scope === null) return undefined;
  const response = astrlink.http.fetch(
    request(
      cfg,
      "GET",
      "/v1/scopes/" + scope + "/artifacts/profile?limit=1",
      undefined,
      WAIT_TIMEOUT_MS,
    ),
  );
  const items =
    response.status === 200
      ? ((
          response.json as {
            items?: Array<{ title?: string; summary?: string }>;
          }
        ).items ?? [])
      : [];
  const lines = items
    .filter((profile) => profile.title || profile.summary)
    .map(
      (profile) =>
        "- " + (profile.title ?? "") + "：" + (profile.summary ?? ""),
    );
  if (lines.length === 0) return undefined;
  return {
    system: {
      open: "<project-context>\n以下是本项目的历史记忆，与用户当前指示冲突时以当前指示为准。",
      items: lines,
      close: "</project-context>",
    },
  };
}

function textOf(family: string, content: Record<string, string>): string {
  if (family === "topic-memory")
    return (content.title ?? "") + "：" + (content.summary ?? "");
  if (family === "experience") return content.lesson ?? "";
  if (family === "skill")
    return (content.name ?? "") + "：" + (content.description ?? "");
  return content.text ?? "";
}

function searchBody(cfg: Config, family: string, query: string, limit: number) {
  // 1.3 refuses include_scores, min_score and admission for atomic-memory.
  if (family === "atomic-memory") return { query, limit };
  const body: Record<string, unknown> = { query, limit, include_scores: true };
  if (family === "topic-memory" && cfg.minSimilarity !== undefined)
    body.admission = { min_semantic_similarity: cfg.minSimilarity };
  return body;
}

// A short reply ("继续", "好的") searches with the previous input too.
function buildQuery(turn: AstrLinkPlugin.TurnView): string {
  const typed = turn.user.typed.trim();
  const text =
    typed.length < 12 && turn.user.previous
      ? turn.user.previous + "\n" + typed
      : typed;
  return text.slice(0, 2000);
}

// The first call of each turn: one search per family, in parallel. The host
// puts the block at the start of the turn's opening user message and replays
// it there on later requests.
export function prepareTurn(
  turn: AstrLinkPlugin.TurnView,
): AstrLinkPlugin.PrepareTurnResult {
  const cfg = config();
  const project = turn.meta.project;
  if (project === undefined || project.value === "none")
    return { skip: "no_project" };
  const query = buildQuery(turn);
  if (query.length < (cfg.minQueryChars ?? 8)) return { skip: "short_query" };
  if (!cfg.serverUrl) return { skip: "server_unavailable" };
  const known = knownVersion();
  if (known !== null && !supported(known))
    return { skip: "server_unavailable" };
  // A project with a scope searches at once. A version not checked lately is
  // checked in the same batch, so the turn waits for the slowest request
  // instead of one after the other.
  const cached = astrlink.store.get("scope:" + project.value);
  let scope = typeof cached === "string" ? cached : null;
  let checkVersion = known === null;
  if (scope === null) {
    scope = serverReady(cfg) ? scopeFor(cfg, project.value) : null;
    if (scope === null) return { skip: "server_unavailable" };
    checkVersion = false;
  }
  const quotas: Record<string, number> = { ...DEFAULT_QUOTAS, ...cfg.quotas };
  const families = Object.keys(quotas).filter((family) => quotas[family] > 0);
  const batch = families.map((family) =>
    request(
      cfg,
      "POST",
      "/v1/scopes/" + scope + "/artifacts/" + family + "/search",
      searchBody(cfg, family, query, quotas[family]),
      WAIT_TIMEOUT_MS,
    ),
  );
  if (checkVersion) batch.push(request(cfg, "GET", "/v1/server-info"));
  const responses = astrlink.http.fetch(batch);
  if (checkVersion && !noteVersion(responses[families.length]))
    return { skip: "server_unavailable" };
  const minScore = cfg.minScore ?? {};
  const items: string[] = [];
  const rows: Array<Array<string | number>> = [];
  let failed = 0;
  families.forEach((family, index) => {
    const response = responses[index];
    if (response.status !== 200) {
      failed++;
      rows.push([
        family,
        quotas[family],
        0,
        0,
        response.took_ms + " ms",
        response.error
          ? (FETCH_ERRORS[response.error] ?? response.error)
          : "HTTP " + response.status,
      ]);
      return;
    }
    const results = (response.json as { results?: SearchItem[] }).results ?? [];
    // Only families that score can be cut by score; atomic-memory relies on
    // the server's admission and its quota.
    const floor = minScore[family] ?? 0;
    const kept = results.filter(
      (item) => item.scores === undefined || item.scores.retrieval >= floor,
    );
    for (const item of kept) {
      const text = textOf(family, item.content ?? {}).trim();
      if (text !== "")
        items.push(
          "- [" +
            family +
            "] " +
            text +
            " (ref " +
            family +
            "/" +
            item.artifact_id +
            "#" +
            item.revision +
            ")",
        );
    }
    const dropped = results.length - kept.length;
    const note =
      family === "atomic-memory"
        ? "无分数"
        : dropped > 0
          ? "低于阈值 " + floor + "，滤掉 " + dropped + " 条"
          : "";
    rows.push([
      family,
      quotas[family],
      results.length,
      kept.length,
      response.took_ms + " ms",
      note,
    ]);
  });
  const requested = families.reduce((sum, family) => sum + quotas[family], 0);
  const allFailed = families.length > 0 && failed === families.length;
  const record: AstrLinkPlugin.PluginRecord = {
    title: "记忆",
    status: items.length > 0 ? "ok" : allFailed ? "failed" : "skipped",
    summary:
      "召回 " +
      items.length +
      "/" +
      requested +
      (failed > 0 ? " · " + failed + " 个检索失败" : ""),
    view: [
      {
        type: "kv",
        items: [
          {
            label: "项目",
            value:
              project.value +
              "（" +
              (SOURCE_LABELS[project.source] ?? project.source) +
              "）",
          },
          { label: "Scope", value: scope, mono: true },
        ],
      },
      {
        type: "table",
        columns: ["家族", "请求", "返回", "保留", "耗时", "说明"],
        rows,
      },
    ],
  };
  if (items.length === 0)
    return { skip: allFailed ? "server_unavailable" : "no_hits", record };
  return {
    context: { open: "<project-memory>", items, close: "</project-memory>" },
    record,
  };
}

// Once per turn, after the request: what the user typed, never what was
// injected. The host calls it only after its privacy check, with the checked
// text.
export function observeTurn(
  turn: AstrLinkPlugin.TurnView,
): AstrLinkPlugin.ObserveTurnResult | undefined {
  const cfg = config();
  const project = turn.meta.project;
  const text = turn.user.typed.trim();
  if (
    project === undefined ||
    project.value === "none" ||
    text.length < 4 ||
    text.startsWith("/")
  )
    return undefined;
  if (!serverReady(cfg)) return undefined;
  const scope = scopeFor(cfg, project.value);
  if (scope === null) return undefined;
  const response = astrlink.http.fetch(
    request(
      cfg,
      "POST",
      "/v1/sources/content",
      {
        scope_id: scope,
        // A retry of the same turn is idempotent; the same words in another
        // turn are another source.
        source_id:
          "astrlink:" +
          project.value +
          ":" +
          turn.session.id +
          ":" +
          turn.session.turn +
          ":" +
          astrlink.util.sha256(text),
        content: text,
        metadata: {
          origin: "astrlink",
          client: turn.client,
          session: turn.session.id,
          turn: turn.session.turn,
          project: project.value,
        },
      },
      10000,
    ),
  );
  if (
    cfg.flushOnCapture === true &&
    response.status >= 200 &&
    response.status < 300
  )
    astrlink.http.fetch(
      request(cfg, "POST", "/v1/memory/flush", { scope_id: scope }, 10000),
    );
  return undefined;
}

// The plugin page's overview: projects and their scopes, and the ask the
// model receives.
export function describeStatus(): AstrLinkPlugin.ViewBlock[] {
  const cfg = config();
  const rows = astrlink.store
    .keys("scope:")
    .map((key) => [key.slice(6), String(astrlink.store.get(key))]);
  const blocks: AstrLinkPlugin.ViewBlock[] = [
    {
      type: "kv",
      items: [
        {
          label: "服务器",
          value: cfg.serverUrl ?? "未设置 serverUrl",
          mono: true,
        },
      ],
    },
    rows.length > 0
      ? { type: "table", columns: ["项目", "PowerContext scope"], rows }
      : { type: "text", text: "还没有绑定过项目。" },
  ];
  if (cfg.askProject !== false)
    blocks.push({
      type: "kv",
      items: [{ label: "询问提示词", value: askText(cfg) }],
    });
  return blocks;
}

export function testConnection(): AstrLinkPlugin.TestConnectionResult {
  const cfg = config();
  if (!cfg.serverUrl)
    return { status: "error", summary: "请先在配置里填写 serverUrl" };
  const [info, capabilities] = astrlink.http.fetch([
    request(cfg, "GET", "/v1/server-info", undefined, 3000),
    request(cfg, "GET", "/v1/capabilities", undefined, 3000),
  ]);
  if (info.status !== 200)
    return { status: "error", summary: info.error ?? "HTTP " + info.status };
  const version = String(
    (info.json as { package_version?: string }).package_version ?? "",
  );
  const families =
    capabilities.status === 200
      ? ((capabilities.json as { artifact_families?: string[] })
          .artifact_families ?? [])
      : [];
  const searchable = [
    "atomic-memory",
    "topic-memory",
    "experience",
    "skill",
  ].filter((family) => families.includes(family));
  return {
    status: supported(version) ? "ok" : "error",
    summary: supported(version)
      ? "PowerContext " + version + " · " + info.took_ms + " ms"
      : "PowerContext " + version + " 太旧，需要 1.3 或以上",
    view: [
      {
        type: "kv",
        items: [{ label: "可检索", value: searchable.join(" / ") }],
      },
    ],
  };
}
