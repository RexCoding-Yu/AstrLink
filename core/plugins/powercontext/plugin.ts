// PowerContext memory: loads a project's profile and topic directory into the
// system prompt when a session has a project, asks once, in the first turn,
// which project a new session belongs to, and records what the user typed.
// Turning records into memory and generating profiles is left to the server's
// own schedules. It talks to PowerContext 1.3 or later through
// astrlink.http.fetch, which reaches only the origins the user allows and adds
// the token itself.
//
// PowerContext (https://github.com/oceanbase/powercontext) is open source,
// by OceanBase, under the Apache License 2.0. This is not an official
// PowerContext plugin: it uses the server's public HTTP API and includes none
// of PowerContext's code.

interface Config {
  /** Must be one of the origins allowed on the plugin page. */
  serverUrl?: string;
  /** The profile and topic directory in the system prompt once the session has a project. */
  resident?: boolean;
  /** Topics the directory lists, the most recently updated first; 0 lists none. */
  topicCount?: number;
  /** Ask in the first turn which project a session without one belongs to. */
  askProject?: boolean;
  /** Existing projects the ask lists; they reach the upstream model. */
  listProjects?: number;
}

interface Listed {
  title?: string | null;
  summary?: string | null;
  published_at?: string | null;
}

interface Page<T> {
  items?: T[];
  next_cursor?: string | null;
}

interface Scope {
  scope_id: string;
  title: string;
}

interface Extraction {
  background?: { automatic_processing_enabled?: boolean | null };
  observation?: {
    last_success_at?: string | null;
    last_failure?: { code: string; occurred_at: string } | null;
  };
}

type Language = AstrLinkPlugin.Language;

// A project name, as the attribute's pattern accepts it.
const PROJECT_NAME = /^[a-z0-9][a-z0-9._-]{0,63}$/;

export const meta = {
  apiVersion: 1,
  key: "powercontext",
  name: "PowerContext 记忆",
  version: "2.0.0",
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
    "启用后，会话有了项目，就把这个项目的画像和最近更新的主题目录放进系统提示词，会话内不变。" +
    "新会话还没有项目时，只在第一轮问一次用户要纳入哪个项目，并列出已有的项目；同一个目录里的新会话会自动接上上次的项目。" +
    "每轮结束时把用户输入的文字记进 PowerContext，整理成记忆、生成画像由服务器定时完成。",
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
      key: "resident",
      label: "装载画像和主题目录",
      type: "switch",
      group: "装载",
      default: true,
      help:
        "会话有了项目后，把项目画像和最近更新的主题（只有标题和摘要）放进系统提示词末尾。" +
        "会话内一直用同一份，提示词缓存不受影响；compact 之后、手动刷新或打开自动刷新时才换新的。",
    },
    {
      key: "topicCount",
      label: "主题目录条数",
      type: "number",
      group: "装载",
      default: 20,
      min: 0,
      max: 50,
      step: 1,
      unit: "个",
      help: "按更新时间取最近的几个主题，摘要超过 100 字的截断；0 表示不列主题。",
    },
    { key: "$refresh", group: "装载" },
    {
      key: "askProject",
      label: "没有项目时询问",
      type: "switch",
      group: "会话",
      default: true,
      help:
        "会话还没有项目时，在网关看到的第一轮用户消息开头附上询问，请模型在回复末尾问用户纳入哪个项目。" +
        "只附在这一轮，不进系统提示词。",
    },
    {
      key: "listProjects",
      label: "询问时列出几个项目",
      type: "number",
      group: "会话",
      default: 20,
      min: 0,
      max: 50,
      step: 1,
      unit: "个",
      help:
        "询问时附上已有的项目供用户挑选：本机最近用过的在前，再加上同一台 PowerContext 服务器上的其他项目。" +
        "这些名字会随请求发给模型；0 表示不列。",
    },
    { key: "$workspace", group: "会话" },
    { key: "$clients", group: "会话" },
    { key: "$wait", group: "会话" },
    { key: "$trusted", group: "隐私" },
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
  storage: [
    {
      label: "项目在服务器上的编号",
      retention: "一直保留，可在插件页清除",
    },
    {
      label: "哪些项目已经打开了画像生成",
      retention: "一直保留，可在插件页清除",
    },
    {
      label: "服务器版本",
      retention: "10 分钟内有效",
    },
    {
      label: "最近一次写入的项目、时间和结果",
      retention: "下一次写入时替换",
    },
  ],
};

// What the plugin injects and shows, in each interface language. What goes to
// the upstream model never names the gateway.
interface Texts {
  ask: string;
  askNamed: string;
  listed: (total: number, shown: number, more: boolean) => string;
  current: string;
  lastUsed: (date: string) => string;
  answer: string;
  answerNamed: string;
  pick: string;
  create: string;
  createNamed: string;
  decline: string;
  format: string;
  similar: string;
  once: string;
  date: (year: number, month: number, day: number, withYear: boolean) => string;
  colon: string;
  context: string;
  placeholders: string;
  profile: string;
  topics: (count: number) => string;
  moreTopics: string;
  loadTitle: string;
  askTitle: string;
  writeTitle: string;
  refresh: string;
  refreshConfirm: string;
  refreshQueued: string;
  loaded: (
    profile: boolean,
    topics: number,
    all: number,
    bytes: number,
  ) => string;
  noProject: string;
  declined: string;
  residentOff: string;
  empty: string;
  serverDown: (reason: string) => string;
  askedWith: (shown: number, total: number) => string;
  askedWithout: string;
  written: string;
  writeFailed: (reason: string) => string;
  project: string;
  source: Record<string, string>;
  profileKept: string;
  yes: string;
  no: string;
  topicsKept: string;
  size: string;
  searchable: string;
  processing: string;
  processingOn: string;
  processingOff: string;
  processingUnknown: string;
  lastSuccess: string;
  lastFailure: string;
  lastWrite: string;
  version: (version: string, ms: number) => string;
  unverified: string;
  tooOld: (version: string) => string;
  needServer: string;
  fetch: Record<string, string>;
  ago: (minutes: number) => string;
  families: Record<string, string>;
}

const MONTHS = [
  "Jan",
  "Feb",
  "Mar",
  "Apr",
  "May",
  "Jun",
  "Jul",
  "Aug",
  "Sep",
  "Oct",
  "Nov",
  "Dec",
];

const TEXTS: Record<Language, Texts> = {
  "zh-CN": {
    ask: "本会话还没有选定项目记忆。不要打断当前任务，在本轮回复的末尾问用户：是否把记忆纳入一个项目，用下面已有的，还是新建一个。",
    askNamed:
      "本会话还没有选定项目记忆。不要打断当前任务，在本轮回复的末尾问用户：是否把记忆纳入一个项目，纳入的话项目名用什么。",
    listed: (total, shown, more) =>
      more
        ? "已有项目（超过 " + total + " 个，列出最近用过的 " + shown + " 个）："
        : total > shown
          ? "已有项目（共 " + total + " 个，列出最近用过的 " + shown + " 个）："
          : "已有项目（共 " + total + " 个）：",
    current: "当前目录",
    lastUsed: (date) => "上次使用 " + date,
    answer:
      "用户回答后（说编号或名字都可以），在回复开头单独一行输出 <memory-project>项目名</memory-project>：",
    answerNamed:
      "用户回答后，在回复开头单独一行输出 <memory-project>项目名</memory-project>：",
    pick: "- 选已有项目时，项目名和列表里的写法完全一致；",
    create: "- 新建时用用户给的名字，只用小写字母、数字、点、下划线、短横线；",
    createNamed:
      "- 项目名用用户给的名字，只用小写字母、数字、点、下划线、短横线；",
    decline: "- 不纳入时输出 <memory-project>none</memory-project>。",
    format: "这一行不要放进代码块，也不要加引号或加粗。",
    similar: "用户给的名字和列表里某个很像时，先确认是不是那个。",
    once: "用户还没回答时不要输出这一行；问过一次就不要再问；没法直接问用户时（比如你是子代理），就不要问。",
    date: (year, month, day, withYear) =>
      (withYear ? year + " 年 " : "") + month + " 月 " + day + " 日",
    colon: "：",
    context: "以下是本项目的历史记忆，与用户当前指示冲突时以当前指示为准。",
    placeholders:
      "带 <PRIVATE_…>、<SECRET_…>、private.invalid 或 192.0.2.x 这类文档网段地址的内容是脱敏占位符，不是真实值，回答时如实说明。",
    profile: "项目画像",
    topics: (count) => "主题目录（最近更新的 " + count + " 个）",
    moreTopics: "还有更多主题没有列出。",
    loadTitle: "记忆装载",
    askTitle: "询问项目",
    writeTitle: "记忆写入",
    refresh: "刷新记忆",
    refreshConfirm:
      "重新装载这个会话的项目画像和主题目录。系统提示词会变，这个会话的提示词缓存要重写一次。",
    refreshQueued: "这个会话的下一个请求会重新装载记忆。",
    loaded: (profile, topics, all, bytes) =>
      (profile ? "画像 · " : "没有画像 · ") +
      "主题 " +
      topics +
      (all > topics ? "/" + all : "") +
      " 个 · " +
      kilobytes(bytes),
    noProject: "会话还没有项目",
    declined: "本会话不纳入记忆",
    residentOff: "设置里关掉了装载",
    empty: "项目还没有画像和主题",
    serverDown: (reason) => "服务器不可用：" + reason,
    askedWith: (shown, total) =>
      "列出 " +
      shown +
      " 个项目" +
      (total > shown ? "（共 " + total + " 个）" : ""),
    askedWithout: "没有列出项目",
    written: "已写入",
    writeFailed: (reason) => "写入失败：" + reason,
    project: "项目",
    source: { header: "请求头", session: "会话绑定", token: "令牌默认值" },
    profileKept: "画像",
    yes: "有",
    no: "没有",
    topicsKept: "主题",
    size: "大小",
    searchable: "可检索",
    processing: "自动整理",
    processingOn: "已打开",
    processingOff: "没打开，原子记忆不会生成",
    processingUnknown: "由外部执行，状态未知",
    lastSuccess: "最近整理成功",
    lastFailure: "最近整理失败",
    lastWrite: "最近写入",
    version: (version, ms) => "PowerContext " + version + " · " + ms + " ms",
    unverified: "这个版本没有验证过",
    tooOld: (version) => "PowerContext " + version + " 太旧，需要 1.3 或以上",
    needServer: "请先在设置里填写服务器地址",
    fetch: {
      timeout: "超时",
      network: "连不上",
      denied: "地址未授权",
      budget: "超出等待时间",
      too_large: "响应过大",
    },
    ago: (minutes) =>
      minutes < 1
        ? "刚刚"
        : minutes < 60
          ? minutes + " 分钟前"
          : minutes < 1440
            ? Math.floor(minutes / 60) + " 小时前"
            : Math.floor(minutes / 1440) + " 天前",
    families: {
      "atomic-memory": "原子记忆",
      "topic-memory": "主题记忆",
      experience: "经验",
      skill: "技能",
    },
  },
  en: {
    ask: "This session has no project memory selected yet. Do not interrupt the current task; at the end of this reply, ask the user whether to keep memory in a project: one listed below, or a new one.",
    askNamed:
      "This session has no project memory selected yet. Do not interrupt the current task; at the end of this reply, ask the user whether to keep memory in a project, and what to name it.",
    listed: (total, shown, more) =>
      more
        ? "Existing projects (more than " +
          total +
          "; the " +
          shown +
          " used most recently):"
        : total > shown
          ? "Existing projects (" +
            total +
            " in all; the " +
            shown +
            " used most recently):"
          : "Existing projects (" + total + "):",
    current: "current directory",
    lastUsed: (date) => "last used " + date,
    answer:
      "Once the user answers (a number or a name will do), put this on a line of its own at the start of your reply: <memory-project>name</memory-project>",
    answerNamed:
      "Once the user answers, put this on a line of its own at the start of your reply: <memory-project>name</memory-project>",
    pick: "- For an existing project, write the name exactly as listed.",
    create:
      "- For a new project, use the user's name, with only lowercase letters, digits, dots, underscores and hyphens.",
    createNamed:
      "- Use the user's name, with only lowercase letters, digits, dots, underscores and hyphens.",
    decline:
      "- If the user wants no project, write <memory-project>none</memory-project>.",
    format: "Do not put the line in a code block, quotes or bold.",
    similar:
      " If the user's name is close to a listed one, ask whether they mean that one.",
    once: "Do not write the line before the user answers; ask only once; if you cannot ask the user directly (for example as a subagent), do not ask.",
    date: (year, month, day, withYear) =>
      MONTHS[month - 1] + " " + day + (withYear ? ", " + year : ""),
    colon: ": ",
    context:
      "Below is this project's memory from earlier work. Where it conflicts with the user's current instructions, follow the current instructions.",
    placeholders:
      "Text with <PRIVATE_…>, <SECRET_…>, private.invalid or documentation addresses such as 192.0.2.x holds redaction placeholders, not real values; say so when it matters.",
    profile: "Project profile",
    topics: (count) => "Topics (the " + count + " updated most recently)",
    moreTopics: "More topics are not listed here.",
    loadTitle: "Memory load",
    askTitle: "Project question",
    writeTitle: "Memory write",
    refresh: "Refresh memory",
    refreshConfirm:
      "Loads this session's project profile and topics again. The system prompt changes, so the session's prompt cache is written once more.",
    refreshQueued: "The session's next request loads its memory again.",
    loaded: (profile, topics, all, bytes) =>
      (profile ? "profile · " : "no profile · ") +
      topics +
      (all > topics ? " of " + all : "") +
      " topics · " +
      kilobytes(bytes),
    noProject: "The session has no project yet",
    declined: "The session keeps no memory",
    residentOff: "Loading is off in the settings",
    empty: "The project has no profile or topics yet",
    serverDown: (reason) => "Server unavailable: " + reason,
    askedWith: (shown, total) =>
      "Listed " + shown + " projects" + (total > shown ? " of " + total : ""),
    askedWithout: "Listed no projects",
    written: "Written",
    writeFailed: (reason) => "Write failed: " + reason,
    project: "Project",
    source: {
      header: "request header",
      session: "session",
      token: "token default",
    },
    profileKept: "Profile",
    yes: "yes",
    no: "none",
    topicsKept: "Topics",
    size: "Size",
    searchable: "Searchable",
    processing: "Automatic processing",
    processingOn: "on",
    processingOff: "off: no atomic memory is made",
    processingUnknown: "runs elsewhere, state unknown",
    lastSuccess: "Last processed",
    lastFailure: "Last processing failure",
    lastWrite: "Last write",
    version: (version, ms) => "PowerContext " + version + " · " + ms + " ms",
    unverified: "this version has not been verified",
    tooOld: (version) =>
      "PowerContext " + version + " is too old; 1.3 or later is needed",
    needServer: "Enter the server address in the settings first",
    fetch: {
      timeout: "timed out",
      network: "unreachable",
      denied: "address not allowed",
      budget: "out of time",
      too_large: "response too large",
    },
    ago: (minutes) =>
      minutes < 1
        ? "just now"
        : minutes < 60
          ? minutes + " min ago"
          : minutes < 1440
            ? Math.floor(minutes / 60) + " h ago"
            : Math.floor(minutes / 1440) + " d ago",
    families: {
      "atomic-memory": "atomic memory",
      "topic-memory": "topics",
      experience: "experience",
      skill: "skills",
    },
  },
  ja: {
    ask: "このセッションにはまだプロジェクトの記憶が選ばれていません。今の作業は中断せず、この返信の最後に、記憶をプロジェクトに入れるか（下の既存のものか、新しく作るか）をユーザーに尋ねてください。",
    askNamed:
      "このセッションにはまだプロジェクトの記憶が選ばれていません。今の作業は中断せず、この返信の最後に、記憶をプロジェクトに入れるか、入れるならプロジェクト名を何にするかをユーザーに尋ねてください。",
    listed: (total, shown, more) =>
      more
        ? "既存のプロジェクト（" +
          total +
          " 件以上、最近使った " +
          shown +
          " 件）："
        : total > shown
          ? "既存のプロジェクト（全 " +
            total +
            " 件、最近使った " +
            shown +
            " 件）："
          : "既存のプロジェクト（全 " + total + " 件）：",
    current: "現在のディレクトリ",
    lastUsed: (date) => "最終使用 " + date,
    answer:
      "ユーザーが答えたら（番号でも名前でも構いません）、返信の先頭に次の 1 行だけを出力してください：<memory-project>プロジェクト名</memory-project>",
    answerNamed:
      "ユーザーが答えたら、返信の先頭に次の 1 行だけを出力してください：<memory-project>プロジェクト名</memory-project>",
    pick: "- 既存のプロジェクトなら、一覧と同じ表記で書きます。",
    create:
      "- 新しく作るなら、ユーザーの指定した名前を、小文字の英字・数字・ピリオド・アンダースコア・ハイフンだけで書きます。",
    createNamed:
      "- ユーザーの指定した名前を、小文字の英字・数字・ピリオド・アンダースコア・ハイフンだけで書きます。",
    decline:
      "- プロジェクトに入れない場合は <memory-project>none</memory-project> と書きます。",
    format:
      "この行はコードブロックに入れず、引用符や太字も付けないでください。",
    similar:
      "ユーザーの名前が一覧のどれかに近いときは、それのことか確認してください。",
    once: "ユーザーが答える前にこの行を出力しないでください。尋ねるのは一度だけです。ユーザーに直接尋ねられない場合（サブエージェントなど）は尋ねないでください。",
    date: (year, month, day, withYear) =>
      (withYear ? year + "年" : "") + month + "月" + day + "日",
    colon: "：",
    context:
      "以下はこのプロジェクトの過去の記憶です。ユーザーの現在の指示と食い違う場合は、現在の指示に従ってください。",
    placeholders:
      "<PRIVATE_…>、<SECRET_…>、private.invalid、192.0.2.x のような文書用アドレスを含む内容はマスク用のプレースホルダーで、実際の値ではありません。回答ではその旨を正直に伝えてください。",
    profile: "プロジェクトのプロフィール",
    topics: (count) => "トピック一覧（最近更新された " + count + " 件）",
    moreTopics: "ほかにもトピックがありますが、ここには載せていません。",
    loadTitle: "記憶の読み込み",
    askTitle: "プロジェクトの確認",
    writeTitle: "記憶の書き込み",
    refresh: "記憶を更新",
    refreshConfirm:
      "このセッションのプロフィールとトピック一覧を読み込み直します。システムプロンプトが変わるため、このセッションのプロンプトキャッシュはもう一度書き込まれます。",
    refreshQueued: "このセッションの次のリクエストで記憶を読み込み直します。",
    loaded: (profile, topics, all, bytes) =>
      (profile ? "プロフィール · " : "プロフィールなし · ") +
      "トピック " +
      topics +
      (all > topics ? "/" + all : "") +
      " 件 · " +
      kilobytes(bytes),
    noProject: "セッションにまだプロジェクトがありません",
    declined: "このセッションは記憶に入れません",
    residentOff: "設定で読み込みがオフです",
    empty: "プロジェクトにまだプロフィールもトピックもありません",
    serverDown: (reason) => "サーバーを利用できません：" + reason,
    askedWith: (shown, total) =>
      shown +
      " 件のプロジェクトを提示" +
      (total > shown ? "（全 " + total + " 件）" : ""),
    askedWithout: "プロジェクトは提示していません",
    written: "書き込みました",
    writeFailed: (reason) => "書き込みに失敗：" + reason,
    project: "プロジェクト",
    source: {
      header: "リクエストヘッダー",
      session: "セッション",
      token: "トークンの既定値",
    },
    profileKept: "プロフィール",
    yes: "あり",
    no: "なし",
    topicsKept: "トピック",
    size: "サイズ",
    searchable: "検索できる記憶",
    processing: "自動整理",
    processingOn: "オン",
    processingOff: "オフ：原子記憶は作られません",
    processingUnknown: "外部で実行、状態は不明",
    lastSuccess: "最後に整理できた時刻",
    lastFailure: "最後に整理に失敗した時刻",
    lastWrite: "最後の書き込み",
    version: (version, ms) => "PowerContext " + version + " · " + ms + " ms",
    unverified: "このバージョンは未検証です",
    tooOld: (version) =>
      "PowerContext " + version + " は古すぎます。1.3 以上が必要です",
    needServer: "先に設定でサーバーのアドレスを入力してください",
    fetch: {
      timeout: "タイムアウト",
      network: "接続できません",
      denied: "許可されていないアドレス",
      budget: "待ち時間切れ",
      too_large: "応答が大きすぎます",
    },
    ago: (minutes) =>
      minutes < 1
        ? "たった今"
        : minutes < 60
          ? minutes + " 分前"
          : minutes < 1440
            ? Math.floor(minutes / 60) + " 時間前"
            : Math.floor(minutes / 1440) + " 日前",
    families: {
      "atomic-memory": "原子記憶",
      "topic-memory": "トピック",
      experience: "経験",
      skill: "スキル",
    },
  },
};

// Requests made while a request waits are bounded by the host; their own
// limit only bounds one that hangs.
const FETCH_TIMEOUT_MS = 5000;
// A block of the system prompt holds at most 8 KiB; the directory stops a
// little short of that so the closing lines always fit.
const BLOCK_BYTES = 7600;
const TOPIC_SUMMARY_CHARS = 100;
const PROFILE_SUMMARY_CHARS = 2000;
// A project with more topics than one page lists a second page; topics past
// that are left to the search.
const TOPIC_PAGES = 2;
const VERSION_TTL_MS = 600000;

function config(): Config {
  return astrlink.config.get() as Config;
}

function texts(language: string | undefined): Texts {
  return TEXTS[language as Language] ?? TEXTS.en;
}

function kilobytes(bytes: number): string {
  return (bytes / 1024).toFixed(1) + " KB";
}

// utf8 counts the bytes text takes in UTF-8.
function utf8(text: string): number {
  let bytes = 0;
  for (const char of text) {
    const code = char.codePointAt(0) ?? 0;
    bytes += code < 0x80 ? 1 : code < 0x800 ? 2 : code < 0x10000 ? 3 : 4;
  }
  return bytes;
}

// clip keeps the first max characters of text on one line.
function clip(text: string, max: number, line = true): string {
  const flat = line
    ? text.replace(/\s+/g, " ").trim()
    : text
        .replace(/\r\n/g, "\n")
        .replace(/\n{3,}/g, "\n\n")
        .trim();
  const chars = Array.from(flat);
  return chars.length <= max ? flat : chars.slice(0, max).join("") + "…";
}

function request(
  cfg: Config,
  method: "GET" | "POST" | "PUT",
  path: string,
  json?: unknown,
  timeoutMs = FETCH_TIMEOUT_MS,
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

// The plugin was written against 1.3; later versions work as far as their
// API stays the same, which nobody has checked yet.
function verified(version: string): boolean {
  return version.startsWith("1.3.");
}

// The version the server reported in the last 10 minutes, if any.
function knownVersion(): string | null {
  const cached = astrlink.store.get("server") as {
    version: string;
    at: number;
  } | null;
  if (cached !== null && astrlink.clock.now() - cached.at < VERSION_TTL_MS)
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

// serverProblem says why the server cannot serve: no address, no answer, or
// a version older than 1.3. null when it can; the version it reports is kept
// for 10 minutes.
function serverProblem(cfg: Config, text: Texts): string | null {
  if (!cfg.serverUrl) return text.needServer;
  let version = knownVersion();
  if (version === null) {
    const response = astrlink.http.fetch(
      request(cfg, "GET", "/v1/server-info"),
    );
    if (response.status !== 200) return failure(text, response);
    noteVersion(response);
    version = knownVersion() ?? "";
  }
  return supported(version) ? null : text.tooOld(version);
}

// Why a request failed, as the plugin page says it.
function failure(text: Texts, response: AstrLinkPlugin.HttpResponse): string {
  if (response.error) return text.fetch[response.error] ?? response.error;
  const error = (
    response.json as {
      error?: {
        message?: string;
        details?: { errors?: Array<{ msg?: string }> };
      };
    } | null
  )?.error;
  const detail = error?.details?.errors?.[0]?.msg ?? error?.message ?? "";
  return "HTTP " + response.status + (detail ? " " + detail.slice(0, 80) : "");
}

// Project → scope. A project another computer or tool made on the same
// server is found by its title; a new one is made with an idempotency key of
// the name alone, so every computer that picks the name gets one scope.
function scopeFor(cfg: Config, project: string): string | null {
  const cached = astrlink.store.get("scope:" + project);
  if (typeof cached === "string") return cached;
  const found = astrlink.http.fetch(
    request(
      cfg,
      "GET",
      "/v1/scopes?query=" +
        encodeURIComponent(project) +
        "&query_field=title&limit=100",
    ),
  );
  let scope =
    found.status === 200
      ? ((found.json as Page<Scope>).items ?? []).find(
          (item) => item.title === project,
        )?.scope_id
      : undefined;
  if (scope === undefined) {
    const created = astrlink.http.fetch(
      request(cfg, "POST", "/v1/scopes", {
        title: project,
        summary: "项目 " + project + " 的记忆",
        idempotency_key: "astrlink:project:" + project,
      }),
    );
    if (created.status !== 201 && created.status !== 200) return null;
    scope = (created.json as { scope_id?: string }).scope_id;
  }
  if (typeof scope !== "string" || scope === "") return null;
  astrlink.store.set("scope:" + project, scope);
  return scope;
}

// Profiles are the server's to generate, on its own schedule, for the scopes
// whose policy allows it: the first time the plugin uses a scope it turns the
// policy on. A policy someone set before stays as it is.
function policyRequests(
  cfg: Config,
  scope: string,
): AstrLinkPlugin.HttpRequest[] {
  if (astrlink.store.get("policy:" + scope) !== null) return [];
  return [request(cfg, "GET", "/v1/scopes/" + scope + "/profile-policy")];
}

function settlePolicy(
  cfg: Config,
  scope: string,
  response: AstrLinkPlugin.HttpResponse,
): void {
  if (response.status === 200) {
    astrlink.store.set("policy:" + scope, true);
    return;
  }
  if (response.status !== 404) return;
  const created = astrlink.http.fetch(
    request(cfg, "PUT", "/v1/scopes/" + scope + "/profile-policy", {
      generation_enabled: true,
      activation_mode: "automatic",
      expected_version: 0,
    }),
  );
  // 409: another computer set it in the meantime.
  if (created.status === 200 || created.status === 409)
    astrlink.store.set("policy:" + scope, true);
}

function skipped(
  text: Texts,
  summary: string,
): AstrLinkPlugin.PrepareSessionResult {
  return { record: { title: text.loadTitle, status: "skipped", summary } };
}

// Computed when a session gets its project, and again only after a compacted
// history, a refresh, or a change of the settings: the same data renders the
// same bytes, so the prompt cache holds.
export function prepareSession(
  view: AstrLinkPlugin.SessionView,
): AstrLinkPlugin.PrepareSessionResult {
  const cfg = config();
  const text = texts(view.language);
  const project = view.meta.project;
  if (project === undefined) return skipped(text, text.noProject);
  if (project.value === "none") return skipped(text, text.declined);
  if (cfg.resident === false) return skipped(text, text.residentOff);
  const problem = serverProblem(cfg, text);
  if (problem !== null) return skipped(text, text.serverDown(problem));
  const scope = scopeFor(cfg, project.value);
  if (scope === null) return skipped(text, text.serverDown(text.fetch.network));
  const base = "/v1/scopes/" + scope + "/artifacts/";
  const policy = policyRequests(cfg, scope);
  const responses = astrlink.http.fetch([
    request(cfg, "GET", base + "profile?limit=1"),
    request(cfg, "GET", base + "topic-memory?limit=100"),
    ...policy,
  ]);
  const [profileResponse, topicResponse] = responses;
  if (policy.length > 0) settlePolicy(cfg, scope, responses[2]);
  if (profileResponse.status !== 200 && topicResponse.status !== 200)
    return skipped(text, text.serverDown(failure(text, profileResponse)));
  const profiles =
    profileResponse.status === 200
      ? ((profileResponse.json as Page<Listed>).items ?? [])
      : [];
  const topics: Listed[] = [];
  let page = topicResponse;
  for (let pages = 1; page.status === 200; pages++) {
    const listed = page.json as Page<Listed>;
    topics.push(...(listed.items ?? []));
    if (!listed.next_cursor || pages === TOPIC_PAGES) break;
    page = astrlink.http.fetch(
      request(
        cfg,
        "GET",
        base +
          "topic-memory?limit=100&cursor=" +
          encodeURIComponent(listed.next_cursor),
      ),
    );
  }
  const block = render(text, cfg, view.redacted === true, profiles, topics);
  if (block === null) return skipped(text, text.empty);
  const record: AstrLinkPlugin.PluginRecord = {
    title: text.loadTitle,
    status: "ok",
    summary: text.loaded(block.profile, block.topics, block.all, block.bytes),
    view: [
      {
        type: "kv",
        items: [
          {
            label: text.project,
            value:
              project.value +
              " · " +
              (text.source[project.source] ?? project.source),
          },
          {
            label: text.profileKept,
            value: block.profile ? text.yes : text.no,
          },
          {
            label: text.topicsKept,
            value:
              block.all > block.topics
                ? block.topics + " / " + block.all
                : String(block.topics),
          },
          { label: text.size, value: kilobytes(block.bytes) },
        ],
      },
    ],
    actions: [
      { id: "refresh", label: text.refresh, confirm: text.refreshConfirm },
    ],
  };
  return { system: block.block, record };
}

interface Rendered {
  block: AstrLinkPlugin.Block;
  profile: boolean;
  topics: number;
  all: number;
  bytes: number;
}

// render lays out the profile and the topics updated most recently, within
// the block's budget, in a fixed order: the same data gives the same bytes.
function render(
  text: Texts,
  cfg: Config,
  redacted: boolean,
  profiles: Listed[],
  topics: Listed[],
): Rendered | null {
  const open =
    "<project-context>\n" +
    text.context +
    (redacted ? "\n" + text.placeholders : "");
  const close = "</project-context>";
  const items: string[] = [];
  // Each item takes a line; the closing lines, the note on more topics
  // included, always keep their room.
  let bytes = utf8(open) + 1 + utf8(text.moreTopics) + 1 + utf8(close);
  const fits = (lines: string[]) =>
    bytes + lines.reduce((sum, line) => sum + utf8(line) + 1, 0) <= BLOCK_BYTES;
  const add = (line: string) => {
    items.push(line);
    bytes += utf8(line) + 1;
  };
  const line = (entry: Listed, max: number, flat: boolean) =>
    "- " +
    clip(entry.title ?? "", 100) +
    (entry.title && entry.summary ? text.colon : "") +
    clip(entry.summary ?? "", max, flat);
  const profileLines = profiles
    .filter((profile) => profile.title || profile.summary)
    .map((profile) => line(profile, PROFILE_SUMMARY_CHARS, false));
  for (const entry of profileLines) {
    const lines = items.length === 0 ? [text.profile, entry] : [entry];
    if (!fits(lines)) break;
    lines.forEach(add);
  }
  const profile = items.length > 0;
  const sorted = topics
    .filter((topic) => topic.title || topic.summary)
    .sort((a, b) => {
      const at = a.published_at ?? "";
      const bt = b.published_at ?? "";
      if (at !== bt) return at < bt ? 1 : -1;
      const an = a.title ?? "";
      const bn = b.title ?? "";
      return an < bn ? -1 : an > bn ? 1 : 0;
    });
  const wanted = Math.min(Math.max(cfg.topicCount ?? 20, 0), 50, sorted.length);
  // The heading names how many topics follow; it takes at most the room of
  // the count asked for.
  bytes += utf8(text.topics(wanted)) + 1;
  const topicLines: string[] = [];
  for (const topic of sorted.slice(0, wanted)) {
    const entry = line(topic, TOPIC_SUMMARY_CHARS, true);
    if (!fits([entry])) break;
    topicLines.push(entry);
    bytes += utf8(entry) + 1;
  }
  if (topicLines.length > 0)
    items.push(text.topics(topicLines.length), ...topicLines);
  if (items.length === 0) return null;
  const more = sorted.length > topicLines.length && wanted > 0;
  const closing = (more ? text.moreTopics + "\n" : "") + close;
  return {
    block: { open, items, close: closing },
    profile,
    topics: topicLines.length,
    all: sorted.length,
    bytes:
      utf8(open) +
      items.reduce((sum, item) => sum + 1 + utf8(item), 0) +
      1 +
      utf8(closing),
  };
}

interface Candidate {
  name: string;
  lastUsedAt: number;
}

interface Listing {
  shown: Candidate[];
  total: number;
  more: boolean;
  current: string | null;
}

function fold(name: string): string {
  return name.toLowerCase().replace(/[-_]/g, "");
}

// listProjects gathers the projects to offer: those this computer used, the
// most recent first, then the others the server has, by name. A project named
// like the working directory goes first.
function listProjects(
  cfg: Config,
  limit: number,
  workspace: string | undefined,
): Listing {
  let stats: AstrLinkPlugin.ValueStat[] = [];
  try {
    stats = astrlink.meta.values("project", 200);
  } catch {
    // Without request.read the plugin knows no local project.
  }
  const local: Candidate[] = stats
    .filter((stat) => stat.value !== "none" && PROJECT_NAME.test(stat.value))
    .map((stat) => ({ name: stat.value, lastUsedAt: stat.lastUsedAt }));
  const names = new Set(local.map((candidate) => candidate.name));
  const remote: Candidate[] = [];
  let more = false;
  if (cfg.serverUrl) {
    const known = knownVersion();
    const batch = [request(cfg, "GET", "/v1/scopes?limit=100")];
    if (known === null) batch.push(request(cfg, "GET", "/v1/server-info"));
    const [scopes, info] = astrlink.http.fetch(batch);
    const ready = known === null ? noteVersion(info) : supported(known);
    if (ready && scopes.status === 200) {
      const page = scopes.json as Page<Scope>;
      more = !!page.next_cursor;
      for (const item of page.items ?? []) {
        if (PROJECT_NAME.test(item.title) && !names.has(item.title)) {
          names.add(item.title);
          remote.push({ name: item.title, lastUsedAt: 0 });
        }
      }
      remote.sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
    }
  }
  const all = local.concat(remote);
  let current: string | null = null;
  if (workspace) {
    const at = all.findIndex(
      (candidate) => fold(candidate.name) === fold(workspace),
    );
    if (at >= 0) {
      current = all[at].name;
      all.unshift(...all.splice(at, 1));
    }
  }
  return { shown: all.slice(0, limit), total: all.length, more, current };
}

// date writes a moment as the user's calendar shows it, with the year only
// when it is not this year's.
function date(text: Texts, at: number): string {
  const local = new Date(at + astrlink.clock.offset(at) * 60000);
  const now = astrlink.clock.now();
  const today = new Date(now + astrlink.clock.offset(now) * 60000);
  return text.date(
    local.getUTCFullYear(),
    local.getUTCMonth() + 1,
    local.getUTCDate(),
    local.getUTCFullYear() !== today.getUTCFullYear(),
  );
}

// The first turn the plugin sees of a session without a project asks which
// project it belongs to, at the start of the turn's user message: never in
// the system prompt, which every later turn would carry. The host puts the
// same words back on later requests, and binds the session at once when the
// user's whole answer is one of the names offered.
export function prepareTurn(
  turn: AstrLinkPlugin.TurnView,
): AstrLinkPlugin.PrepareTurnResult | undefined {
  const cfg = config();
  if (
    turn.meta.project !== undefined ||
    turn.session.first !== true ||
    cfg.askProject === false
  )
    return undefined;
  const text = texts(turn.language);
  const limit = Math.min(Math.max(cfg.listProjects ?? 20, 0), 50);
  const listing: Listing =
    limit > 0
      ? listProjects(cfg, limit, turn.workspace?.name)
      : { shown: [], total: 0, more: false, current: null };
  const lines: string[] = [];
  if (listing.shown.length === 0) {
    lines.push(
      text.askNamed,
      text.answerNamed,
      text.createNamed,
      text.decline,
      text.format,
      text.once,
    );
  } else {
    lines.push(
      text.ask,
      text.listed(listing.total, listing.shown.length, listing.more),
    );
    listing.shown.forEach((candidate, index) => {
      const notes: string[] = [];
      if (candidate.name === listing.current) notes.push(text.current);
      if (candidate.lastUsedAt > 0)
        notes.push(text.lastUsed(date(text, candidate.lastUsedAt)));
      lines.push(
        index +
          1 +
          ". " +
          candidate.name +
          (notes.length > 0
            ? turn.language === "en"
              ? " (" + notes.join(", ") + ")"
              : "（" + notes.join("，") + "）"
            : ""),
      );
    });
    lines.push(
      text.answer,
      text.pick,
      text.create,
      text.decline,
      text.format + text.similar,
      text.once,
    );
  }
  const offered = listing.shown.map((candidate) => candidate.name);
  return {
    context: lines.join("\n"),
    choices: { project: offered },
    record: {
      title: text.askTitle,
      status: "ok",
      summary:
        offered.length > 0
          ? text.askedWith(offered.length, listing.total)
          : text.askedWithout,
    },
  };
}

// Once per turn, after the request: what the user typed, never what was
// injected. The host calls it after its privacy check, with the checked text,
// and for a session without a project only once the reply bound one. The
// server turns what it receives into memory on its own schedule.
export function observeTurn(
  turn: AstrLinkPlugin.TurnView,
): AstrLinkPlugin.ObserveTurnResult | undefined {
  const cfg = config();
  const text = texts(turn.language);
  const project = turn.meta.project;
  const typed = turn.user.typed.trim();
  if (
    project === undefined ||
    project.value === "none" ||
    typed.length < 4 ||
    typed.startsWith("/")
  )
    return undefined;
  const problem = serverProblem(cfg, text);
  if (problem !== null)
    return written(text, project.value, "failed", text.writeFailed(problem));
  const scope = scopeFor(cfg, project.value);
  if (scope === null)
    return written(
      text,
      project.value,
      "failed",
      text.writeFailed(text.fetch.network),
    );
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
          astrlink.util.sha256(typed),
        content: typed,
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
    return written(
      text,
      project.value,
      "failed",
      text.writeFailed(failure(text, response)),
    );
  return written(text, project.value, "ok", text.written);
}

// written is the capture's record, and what the overview says of the last one.
function written(
  text: Texts,
  project: string,
  status: "ok" | "failed",
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
      title: text.writeTitle,
      status,
      summary,
      view: [{ type: "kv", items: [{ label: text.project, value: project }] }],
    },
  };
}

// A click on a record's 「刷新记忆」: the session's next request computes its
// block again.
export function onAction(
  view: AstrLinkPlugin.ActionView,
): AstrLinkPlugin.ActionResult {
  if (view.action !== "refresh") return {};
  return { refresh: true, message: texts(view.language).refreshQueued };
}

// processingItems says whether the server turns records into memory on its
// own, and when it last did or failed to.
function processingItems(
  text: Texts,
  capabilities: AstrLinkPlugin.HttpResponse,
): Array<{ label: string; value: string }> {
  if (capabilities.status !== 200) return [];
  const extraction = (capabilities.json as { extraction?: Extraction | null })
    .extraction;
  if (!extraction) return [];
  const enabled = extraction.background?.automatic_processing_enabled;
  const items = [
    {
      label: text.processing,
      value:
        enabled === true
          ? text.processingOn
          : enabled === false
            ? text.processingOff
            : text.processingUnknown,
    },
  ];
  const success = extraction.observation?.last_success_at;
  if (success)
    items.push({ label: text.lastSuccess, value: moment(text, success) });
  const failed = extraction.observation?.last_failure;
  if (failed)
    items.push({
      label: text.lastFailure,
      value: moment(text, failed.occurred_at) + " · " + failed.code,
    });
  return items;
}

function moment(text: Texts, iso: string): string {
  const at = Date.parse(iso);
  if (isNaN(at)) return iso;
  const local = new Date(at + astrlink.clock.offset(at) * 60000);
  const hours = String(local.getUTCHours()).padStart(2, "0");
  const minutes = String(local.getUTCMinutes()).padStart(2, "0");
  return date(text, at) + " " + hours + ":" + minutes;
}

// The plugin page's own part of the overview: whether the server processes
// what the plugin writes, and the last write. The host shows the server, the
// bound projects and the credits itself.
export function describeStatus(
  view: AstrLinkPlugin.PageView,
): AstrLinkPlugin.ViewBlock[] {
  const cfg = config();
  const text = texts(view?.language);
  const items: Array<{ label: string; value: string; mono?: boolean }> = [];
  if (cfg.serverUrl)
    items.push(
      ...processingItems(
        text,
        astrlink.http.fetch(
          request(cfg, "GET", "/v1/capabilities", undefined, 3000),
        ),
      ),
    );
  const last = astrlink.store.get("last-write") as {
    at: number;
    project: string;
    summary: string;
  } | null;
  if (last !== null)
    items.push({
      label: text.lastWrite,
      value:
        last.project +
        " · " +
        last.summary +
        " · " +
        text.ago(Math.floor((astrlink.clock.now() - last.at) / 60000)),
    });
  return items.length > 0 ? [{ type: "kv", items }] : [];
}

export function testConnection(
  view: AstrLinkPlugin.PageView,
): AstrLinkPlugin.TestConnectionResult {
  const cfg = config();
  const text = texts(view?.language);
  if (!cfg.serverUrl) return { status: "error", summary: text.needServer };
  const [info, capabilities] = astrlink.http.fetch([
    request(cfg, "GET", "/v1/server-info", undefined, 3000),
    request(cfg, "GET", "/v1/capabilities", undefined, 3000),
  ]);
  if (info.status !== 200)
    return { status: "error", summary: failure(text, info) };
  const version = String(
    (info.json as { package_version?: string }).package_version ?? "",
  );
  if (!supported(version))
    return { status: "error", summary: text.tooOld(version) };
  noteVersion(info);
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
    status: "ok",
    summary:
      text.version(version, info.took_ms) +
      (verified(version) ? "" : " · " + text.unverified),
    view: [
      {
        type: "kv",
        items: [
          {
            label: text.searchable,
            value: searchable
              .map((family) => text.families[family])
              .join(" / "),
          },
          ...processingItems(text, capabilities),
        ],
      },
    ],
  };
}
