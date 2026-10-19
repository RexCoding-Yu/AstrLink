// PowerContext memory: recalls a project's memory at the start of each turn,
// puts its profile in the system prompt, and captures what the user typed.
// It talks to PowerContext 1.3 or later through astrlink.http.fetch, which
// reaches only the origins the user allows and adds the token itself.
//
// PowerContext (https://github.com/oceanbase/powercontext) is open source,
// by OceanBase, under the Apache License 2.0. This is not an official
// PowerContext plugin: it uses the server's public HTTP API and includes none
// of PowerContext's code.

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
}

interface SearchItem {
  artifact_id: string;
  revision: number;
  content: Record<string, string>;
  scores?: { retrieval: number };
}

// The ask's fixed words, also shown on the plugin page.
const ASK_QUESTION =
  "本会话还没有选定项目记忆。不要打断当前任务，在本轮回复的末尾问用户：是否把当前项目纳入记忆；纳入的话，项目名用什么（只用小写字母、数字、点、下划线、短横线）。";
const ASK_ANSWER =
  "用户回答后（或用户已经说了项目名），在回复开头单独一行原样输出 <memory-project>项目名</memory-project>；不纳入就输出 <memory-project>none</memory-project>。" +
  "这一行不要放进代码块，也不要加引号、反引号或加粗。用户还没回答时不要输出；对话里已经有这一行，或者你已经问过一次，就不要再问。" +
  "如果你没法直接问用户（比如你是子代理，或者在无人值守地运行），就不要问，也不要输出这一行。";

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
  settings: [
    {
      key: "serverUrl",
      label: "服务器地址",
      type: "server",
      group: "连接",
      required: true,
      credential: "token",
      placeholder: "https://memory.example.com",
      help: "PowerContext 服务器的地址。插件只访问这个地址，令牌也只发往这里。",
    },
    {
      key: "quotas.atomic-memory",
      label: "原子记忆",
      type: "number",
      group: "召回",
      row: "每轮条数",
      default: 4,
      min: 0,
      max: 20,
      step: 1,
      unit: "条",
      help: "每轮开始时，每类记忆最多取几条放进本轮消息；0 表示不检索这一类。",
    },
    {
      key: "quotas.topic-memory",
      label: "主题记忆",
      type: "number",
      group: "召回",
      row: "每轮条数",
      default: 3,
      min: 0,
      max: 20,
      step: 1,
      unit: "条",
    },
    {
      key: "quotas.skill",
      label: "技能",
      type: "number",
      group: "召回",
      row: "每轮条数",
      default: 2,
      min: 0,
      max: 20,
      step: 1,
      unit: "条",
    },
    {
      key: "quotas.experience",
      label: "经验",
      type: "number",
      group: "召回",
      row: "每轮条数",
      default: 0,
      min: 0,
      max: 20,
      step: 1,
      unit: "条",
    },
    { key: "$wait", group: "召回" },
    {
      key: "askProject",
      label: "没有项目时询问",
      type: "switch",
      group: "会话",
      default: true,
      help:
        "会话还没有项目时，系统提示词末尾附上这段话：\n" +
        ASK_QUESTION +
        ASK_ANSWER,
    },
    {
      key: "resident",
      label: "附上项目简介",
      type: "switch",
      group: "会话",
      default: true,
      help: "会话有项目后，把 PowerContext 为这个项目整理的简介放进系统提示词。",
    },
    { key: "$trusted", group: "隐私" },
    {
      key: "suggestProjects",
      label: "询问时列出已有项目",
      type: "number",
      advanced: true,
      default: 0,
      min: 0,
      max: 20,
      step: 1,
      unit: "个",
      help: "询问项目时附上几个已有的项目名供用户挑选；这些名字会发给模型。",
    },
    {
      key: "minQueryChars",
      label: "最短问题",
      type: "number",
      advanced: true,
      default: 8,
      min: 0,
      max: 200,
      step: 1,
      unit: "字",
      help: "问题少于这么多字时不检索。",
    },
    {
      key: "minScore.topic-memory",
      label: "主题记忆",
      type: "number",
      advanced: true,
      row: "分数下限",
      min: 0,
      max: 1,
      step: 0.05,
      placeholder: "不限",
      help: "检索分数低于下限的结果不放进消息。原子记忆没有分数。",
    },
    {
      key: "minScore.skill",
      label: "技能",
      type: "number",
      advanced: true,
      row: "分数下限",
      min: 0,
      max: 1,
      step: 0.05,
      placeholder: "不限",
    },
    {
      key: "minScore.experience",
      label: "经验",
      type: "number",
      advanced: true,
      row: "分数下限",
      min: 0,
      max: 1,
      step: 0.05,
      placeholder: "不限",
    },
    {
      key: "minSimilarity",
      label: "主题记忆",
      type: "number",
      advanced: true,
      row: "相似度下限",
      min: 0,
      max: 1,
      step: 0.05,
      placeholder: "不限",
      help: "检索主题记忆时，语义相似度低于下限的不要。",
    },
    { key: "$origins", advanced: true },
  ],
  credits: [
    {
      name: "PowerContext",
      owner: "OceanBase",
      license: "Apache-2.0",
      url: "https://github.com/oceanbase/powercontext",
      note: "本插件不是 PowerContext 官方插件，只通过它公开的 HTTP 接口通信，不包含它的代码。",
    },
  ],
};

const DEFAULT_QUOTAS: Record<string, number> = {
  "atomic-memory": 4,
  "topic-memory": 3,
  skill: 2,
};
const FAMILY_LABELS: Record<string, string> = {
  "atomic-memory": "原子记忆",
  "topic-memory": "主题记忆",
  experience: "经验",
  skill: "技能",
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
// Searches and the profile snapshot run while the request waits, as long as
// the plugin page says; the host ends them in time, so their own limit only
// bounds a request that hangs.
const SEARCH_TIMEOUT_MS = 5000;
// These families embed the query before they search, which can take seconds;
// a search by words alone runs beside each, for when the embedding is late.
const TEXT_FALLBACK = ["atomic-memory", "topic-memory"];
// PowerContext's text search takes at most 64 analyzed terms. 1.3 counts
// each word of letters or digits once and a run of n Han characters as n
// characters and n - 1 pairs; counting any other character like Han never
// finds fewer, which keeps the query in.
const MAX_QUERY_TERMS = 60;
// A capture has 30 s: the project's scope, the capture itself, then the
// processing that turns it into memory, which takes PowerContext 10 s or more.
const CAPTURE_BUDGET_MS = 28000;
const FLUSH_ATTEMPTS = 3;
// A first processing call often fails where the next succeeds, so one call
// waits at most this long and leaves time to try again.
const FLUSH_TIMEOUT_MS = 15000;

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
    ASK_QUESTION +
    (known.length > 0 ? "已有项目：" + known.join("、") + "。" : "") +
    ASK_ANSWER
  );
}

function request(
  cfg: Config,
  method: "GET" | "POST",
  path: string,
  json?: unknown,
  timeoutMs = SEARCH_TIMEOUT_MS,
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
    request(cfg, "GET", "/v1/scopes/" + scope + "/artifacts/profile?limit=1"),
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

// textOnly searches by words alone, without the semantic admission that
// needs an embedding.
function searchBody(
  cfg: Config,
  family: string,
  query: string,
  limit: number,
  textOnly = false,
) {
  // 1.3 refuses include_scores, min_score and admission for atomic-memory.
  const body: Record<string, unknown> = { query, limit };
  if (textOnly) body.mode = "text";
  if (family === "atomic-memory") return body;
  body.include_scores = true;
  if (family === "topic-memory" && cfg.minSimilarity !== undefined && !textOnly)
    body.admission = { min_semantic_similarity: cfg.minSimilarity };
  return body;
}

const SEPARATOR =
  /[\s\x21-\x2f\x3a-\x40\x5b-\x60\x7b-\x7e\u2000-\u206f\u3000-\u3004\u3008-\u303f\uff00-\uff0f\uff1a-\uff20\uff3b-\uff40\uff5b-\uff65]/;
const ASCII_WORD = /[0-9A-Za-z]/;

// cost is what one character adds to the terms of text, scanned in either
// direction; run is the kind of the run it continues.
function cost(char: string, run: string): { terms: number; run: string } {
  if (SEPARATOR.test(char)) return { terms: 0, run: "" };
  if (ASCII_WORD.test(char))
    return { terms: run === "word" ? 0 : 1, run: "word" };
  return { terms: run === "han" ? 2 : 1, run: "han" };
}

// bound keeps text within limit terms: its start, and its end, where a long
// message often says what it asks.
function bound(text: string, limit: number): { text: string; terms: number } {
  let head = 0;
  let used = 0;
  let run = "";
  for (; head < text.length; head++) {
    const next = cost(text[head], run);
    if (used + next.terms > Math.ceil(limit / 2)) break;
    used += next.terms;
    run = next.run;
  }
  let total = used;
  for (let index = head; index < text.length; index++) {
    const next = cost(text[index], run);
    total += next.terms;
    run = next.run;
  }
  if (total <= limit) return { text: text.trim(), terms: total };
  let tail = text.length;
  run = "";
  for (let index = text.length - 1; index >= head; index--) {
    const next = cost(text[index], run);
    if (used + next.terms > limit) break;
    used += next.terms;
    run = next.run;
    tail = index;
  }
  const start = text.slice(0, head).trim();
  const end = text.slice(tail).trim();
  return { text: end === "" ? start : start + "\n" + end, terms: used };
}

// A short reply ("继续", "好的") searches with the previous input too, as much
// of it as the terms left allow.
function buildQuery(turn: AstrLinkPlugin.TurnView): string {
  const typed = turn.user.typed.trim();
  const own = bound(typed, MAX_QUERY_TERMS);
  if (typed.length >= 12 || !turn.user.previous) return own.text;
  const before = bound(turn.user.previous, MAX_QUERY_TERMS - own.terms);
  return before.text === "" ? own.text : before.text + "\n" + own.text;
}

// Why a request failed, as the plugin page says it.
function failure(response: AstrLinkPlugin.HttpResponse): string {
  if (response.error) return FETCH_ERRORS[response.error] ?? response.error;
  const error = (
    response.json as {
      error?: {
        message?: string;
        details?: { errors?: Array<{ msg?: string }> };
      };
    } | null
  )?.error;
  const detail = error?.details?.errors?.[0]?.msg ?? error?.message ?? "";
  return "HTTP " + response.status + (detail ? "：" + detail.slice(0, 80) : "");
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
  // Each family's search, and beside the slow ones a search by words alone.
  const batch: AstrLinkPlugin.HttpRequest[] = [];
  const searches = families.map((family) => {
    const path = "/v1/scopes/" + scope + "/artifacts/" + family + "/search";
    const at =
      batch.push(
        request(
          cfg,
          "POST",
          path,
          searchBody(cfg, family, query, quotas[family]),
        ),
      ) - 1;
    const textAt = TEXT_FALLBACK.includes(family)
      ? batch.push(
          request(
            cfg,
            "POST",
            path,
            searchBody(cfg, family, query, quotas[family], true),
          ),
        ) - 1
      : -1;
    return { family, at, textAt };
  });
  const versionAt = checkVersion
    ? batch.push(request(cfg, "GET", "/v1/server-info")) - 1
    : -1;
  const responses = astrlink.http.fetch(batch);
  if (versionAt >= 0 && !noteVersion(responses[versionAt]))
    return { skip: "server_unavailable" };
  const minScore = cfg.minScore ?? {};
  const items: string[] = [];
  const rows: Array<Array<string | number>> = [];
  let failed = 0;
  let byText = 0;
  searches.forEach(({ family, at, textAt }) => {
    const label = FAMILY_LABELS[family] ?? family;
    const full = responses[at];
    const words = textAt >= 0 ? responses[textAt] : undefined;
    const response =
      full.status !== 200 && words?.status === 200 ? words : full;
    if (response.status !== 200) {
      failed++;
      rows.push([
        label,
        quotas[family],
        0,
        0,
        response.took_ms + " ms",
        failure(full),
      ]);
      return;
    }
    const notes: string[] = [];
    if (response === words) {
      byText++;
      notes.push("完整检索" + failure(full) + "，用了关键词检索的结果");
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
    if (dropped > 0)
      notes.push("低于阈值 " + floor + "，滤掉 " + dropped + " 条");
    rows.push([
      label,
      quotas[family],
      results.length,
      kept.length,
      response.took_ms + " ms",
      notes.join("；"),
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
      (failed > 0 ? " · " + failed + " 个检索失败" : "") +
      (byText > 0 ? " · " + byText + " 个只用关键词" : ""),
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
        ],
      },
      {
        type: "table",
        columns: ["类型", "请求", "返回", "保留", "耗时", "说明"],
        rows,
      },
    ],
  };
  if (items.length === 0)
    return {
      skip: allFailed
        ? "server_unavailable"
        : failed > 0
          ? "search_failed"
          : "no_hits",
      record,
    };
  return {
    context: { open: "<project-memory>", items, close: "</project-memory>" },
    record,
  };
}

// Once per turn, after the request: what the user typed, never what was
// injected. The host calls it after its privacy check, with the checked text,
// and for a session without a project only once the reply bound one. The
// project's scope comes first, then the capture, then the processing that
// turns it into memory; what processing leaves undone, the project's next
// capture takes up again.
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
  const started = astrlink.clock.now();
  if (!serverReady(cfg))
    return written(project.value, "failed", "写入失败：服务器不可用");
  const scope = scopeFor(cfg, project.value);
  if (scope === null)
    return written(project.value, "failed", "写入失败：没能在服务器上建项目");
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
  if (response.status < 200 || response.status >= 300)
    return written(project.value, "failed", "写入失败：" + failure(response));
  const position = (response.json as { position?: number } | null)?.position;
  if (typeof position === "number") {
    const pending = astrlink.store.get("pending:" + scope);
    astrlink.store.set(
      "pending:" + scope,
      Math.max(position, typeof pending === "number" ? pending : 0),
    );
  }
  const processed = flush(cfg, scope, started);
  if (processed.done)
    return written(project.value, "ok", "已写入 · 已整理成记忆");
  if (processed.late)
    return written(
      project.value,
      "skipped",
      "已写入 · 整理超时，下一轮接着整理",
    );
  if (processed.error !== "")
    return written(
      project.value,
      "failed",
      "已写入 · 整理失败：" + processed.error,
    );
  return written(
    project.value,
    "skipped",
    "已写入 · 整理还没完成，下一轮接着整理",
  );
}

// flush asks the server to turn the scope's waiting captures into memory,
// until it reports them done or the capture's time runs out.
function flush(
  cfg: Config,
  scope: string,
  started: number,
): { done: boolean; error: string; late: boolean } {
  const pending = astrlink.store.get("pending:" + scope);
  if (typeof pending !== "number")
    return { done: true, error: "", late: false };
  let error = "";
  let late = false;
  for (let attempt = 0; attempt < FLUSH_ATTEMPTS; attempt++) {
    const left = CAPTURE_BUDGET_MS - (astrlink.clock.now() - started);
    if (left < 2000) break;
    const response = astrlink.http.fetch(
      request(
        cfg,
        "POST",
        "/v1/memory/flush",
        { scope_id: scope },
        Math.min(left, FLUSH_TIMEOUT_MS),
      ),
    );
    if (response.status !== 200) {
      error = failure(response);
      late = response.error === "timeout" || response.error === "budget";
      continue;
    }
    error = "";
    late = false;
    const cursor = (response.json as { current_cursor?: number } | null)
      ?.current_cursor;
    if (typeof cursor === "number" && cursor >= pending) {
      astrlink.store.delete("pending:" + scope);
      return { done: true, error: "", late: false };
    }
  }
  return { done: false, error, late };
}

// written is the capture's record, and what the overview says of the last one.
function written(
  project: string,
  status: "ok" | "skipped" | "failed",
  summary: string,
): AstrLinkPlugin.ObserveTurnResult {
  astrlink.store.set("last-write", {
    at: astrlink.clock.now(),
    project,
    status,
    summary,
  });
  return {
    record: {
      title: "记忆写入",
      status,
      summary,
      view: [{ type: "kv", items: [{ label: "项目", value: project }] }],
    },
  };
}

function ago(at: number): string {
  const minutes = Math.floor((astrlink.clock.now() - at) / 60000);
  if (minutes < 1) return "刚刚";
  if (minutes < 60) return minutes + " 分钟前";
  if (minutes < 1440) return Math.floor(minutes / 60) + " 小时前";
  return Math.floor(minutes / 1440) + " 天前";
}

// The plugin page's own part of the overview: the last capture and the
// projects whose captures still wait to become memory. The host shows the
// server, the bound projects and the credits itself.
export function describeStatus(): AstrLinkPlugin.ViewBlock[] {
  const items: Array<{ label: string; value: string; mono?: boolean }> = [];
  const last = astrlink.store.get("last-write") as {
    at: number;
    project: string;
    summary: string;
  } | null;
  if (last !== null)
    items.push({
      label: "最近写入",
      value: last.project + "：" + last.summary + " · " + ago(last.at),
    });
  const waiting = astrlink.store.keys("pending:").length;
  if (waiting > 0)
    items.push({
      label: "待整理",
      value: waiting + " 个项目有记下的原文还没整理成记忆",
    });
  return items.length > 0 ? [{ type: "kv", items }] : [];
}

export function testConnection(): AstrLinkPlugin.TestConnectionResult {
  const cfg = config();
  if (!cfg.serverUrl)
    return { status: "error", summary: "请先在设置里填写服务器地址" };
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
        items: [
          {
            label: "可检索",
            value: searchable
              .map((family) => FAMILY_LABELS[family])
              .join(" / "),
          },
        ],
      },
    ],
  };
}
