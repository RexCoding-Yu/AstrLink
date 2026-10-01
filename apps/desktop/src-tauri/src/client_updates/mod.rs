mod process;
#[cfg(any(target_os = "macos", test))]
mod proxy;

use std::{
    cmp::Ordering,
    collections::HashSet,
    fs,
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
    time::Duration,
};

use process::{CommandSpec, Environment};
use semver::Version;
use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Emitter, Manager, State};

const EVENT: &str = "local-client-status";
const PROBE_TIMEOUT: Duration = Duration::from_secs(8);
const UPDATE_TIMEOUT: Duration = Duration::from_secs(10 * 60);
const CURSOR_LATEST_URL: &str =
    "https://api2.cursor.sh/aiserver.v1.DashboardService/GetCliDownloadUrl";
const PI_LATEST_URL: &str = "https://pi.dev/api/latest-version";

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq, Hash)]
#[serde(rename_all = "snake_case")]
pub enum ClientId {
    Codex,
    Claude,
    Cursor,
    Pi,
}

impl ClientId {
    const ALL: [Self; 4] = [Self::Codex, Self::Claude, Self::Cursor, Self::Pi];

    fn name(self) -> &'static str {
        match self {
            Self::Codex => "codex",
            Self::Claude => "claude",
            Self::Cursor => "cursor-agent",
            Self::Pi => "pi",
        }
    }
    /// npm packages that provide the command, current name first.
    fn packages(self) -> &'static [&'static str] {
        match self {
            Self::Codex => &["@openai/codex"],
            Self::Claude => &["@anthropic-ai/claude-code"],
            Self::Cursor => &[],
            Self::Pi => &[
                "@earendil-works/pi-coding-agent",
                "@mariozechner/pi-coding-agent",
            ],
        }
    }
    fn brew_packages(self) -> &'static [&'static str] {
        match self {
            Self::Codex => &["codex"],
            Self::Claude => &["claude-code", "claude-code@latest"],
            Self::Cursor => &["cursor-cli"],
            Self::Pi => &["pi-coding-agent"],
        }
    }
}

/// A version as the client prints it, with the key used to order releases.
#[derive(Clone, Debug, PartialEq, Eq)]
struct ClientVersion {
    order: Version,
    text: String,
}

impl ClientVersion {
    fn parse(id: ClientId, raw: &str) -> Option<Self> {
        let raw = raw.trim_matches(['(', ')']).trim_start_matches('v');
        if id != ClientId::Cursor {
            let order = Version::parse(raw).ok()?;
            return Some(Self {
                text: order.to_string(),
                order,
            });
        }
        // Cursor builds are dated: YYYY.MM.DD-<commit>. Only the date orders them.
        let (date, commit) = raw.split_once('-').unwrap_or((raw, ""));
        let bytes = date.as_bytes();
        if bytes.len() != 10
            || bytes[4] != b'.'
            || bytes[7] != b'.'
            || !bytes
                .iter()
                .enumerate()
                .all(|(i, b)| i == 4 || i == 7 || b.is_ascii_digit())
            || !commit.bytes().all(|b| b.is_ascii_alphanumeric())
        {
            return None;
        }
        Some(Self {
            order: Version::new(
                date[..4].parse().ok()?,
                date[5..7].parse().ok()?,
                date[8..].parse().ok()?,
            ),
            text: raw.into(),
        })
    }
    /// Like Cursor's updater, a different build from the same day counts as newer.
    fn newer_than(&self, other: &Self) -> bool {
        match self.order.cmp(&other.order) {
            Ordering::Greater => true,
            Ordering::Equal => self.text != other.text,
            Ordering::Less => false,
        }
    }
}

impl std::fmt::Display for ClientVersion {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.text)
    }
}

#[derive(Clone, Debug, Serialize)]
pub struct ClientStatus {
    id: ClientId,
    phase: String,
    current_version: Option<String>,
    latest_version: Option<String>,
    install_method: String,
    executable: Option<String>,
    other_installations: Vec<String>,
    can_update: bool,
    checked_at: Option<String>,
    error_code: Option<String>,
    error_detail: Option<String>,
}

impl ClientStatus {
    fn new(id: ClientId) -> Self {
        Self {
            id,
            phase: "idle".into(),
            current_version: None,
            latest_version: None,
            install_method: "unknown".into(),
            executable: None,
            other_installations: vec![],
            can_update: false,
            checked_at: None,
            error_code: None,
            error_detail: None,
        }
    }
    fn fail(&mut self, error: ClientError) {
        self.phase = "error".into();
        self.can_update = false;
        self.error_code = Some(error.code.into());
        self.error_detail = Some(error.detail);
    }
}

#[derive(Clone, Debug, Serialize)]
pub struct ClientSnapshot {
    revision: u64,
    busy: bool,
    clients: Vec<ClientStatus>,
}

impl Default for ClientSnapshot {
    fn default() -> Self {
        Self {
            revision: 0,
            busy: false,
            clients: ClientId::ALL.map(ClientStatus::new).to_vec(),
        }
    }
}

#[derive(Debug)]
struct ClientError {
    code: &'static str,
    detail: String,
}
impl ClientError {
    fn new(code: &'static str, detail: impl std::fmt::Display) -> Self {
        let detail = detail.to_string();
        // A successful final shell process can mask curl failures in a pipeline.
        let code = if ["command", "unchanged"].contains(&code)
            && [
                "curl: (5)",
                "curl: (6)",
                "curl: (7)",
                "curl: (28)",
                "curl: (35)",
                "curl: (60)",
                "ECONNREFUSED",
                "ENOTFOUND",
                "ETIMEDOUT",
            ]
            .iter()
            .any(|marker| detail.contains(marker))
        {
            "network"
        } else {
            code
        };
        let tail: String = detail.chars().rev().take(4096).collect();
        Self {
            code,
            detail: tail.chars().rev().collect(),
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
enum InstallMethod {
    Native,
    Npm {
        prefix: PathBuf,
    },
    /// A Bun, pnpm, or Yarn global installation.
    Package(&'static str),
    Brew {
        root: PathBuf,
        package: String,
        cask: bool,
    },
    Unknown,
}

impl InstallMethod {
    fn label(&self) -> &'static str {
        match self {
            Self::Native => "native",
            Self::Npm { .. } => "npm",
            Self::Package(manager) => manager,
            Self::Brew { .. } => "homebrew",
            Self::Unknown => "unknown",
        }
    }
}

#[derive(Clone)]
struct Installation {
    executable: PathBuf,
    resolved: PathBuf,
    method: InstallMethod,
    others: Vec<String>,
}

fn executable(path: &Path) -> bool {
    if !path.is_file() {
        return false;
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::metadata(path).is_ok_and(|m| m.permissions().mode() & 0o111 != 0)
    }
    #[cfg(not(unix))]
    true
}

fn find_programs(path: &std::ffi::OsStr, name: &str) -> Vec<PathBuf> {
    let mut found = Vec::new();
    let mut seen = HashSet::new();
    for directory in std::env::split_paths(path).filter(|p| p.is_absolute()) {
        // npm also writes an extensionless sh shim beside each .cmd. Windows
        // cannot launch it, and it is not a second installation.
        #[cfg(windows)]
        let names = [format!("{name}.exe"), format!("{name}.cmd")];
        #[cfg(not(windows))]
        let names = [name.to_string()];
        for name in names {
            let file = directory.join(name);
            if executable(&file) {
                if let Ok(resolved) = dunce::canonicalize(&file) {
                    if seen.insert(resolved) {
                        found.push(file);
                    }
                }
            }
        }
    }
    found
}

/// npm's Windows shims run their target as `"%dp0%\<path>"` (`"%~dp0\<path>"`
/// before npm 7), after the script's interpreter. Read the target instead of
/// guessing each package's entry point; package ownership is checked later.
fn cmd_shim_target(shim: &Path) -> Option<PathBuf> {
    let raw = fs::read_to_string(shim).ok()?;
    let (_, rest) = raw
        .rsplit_once("\"%dp0%\\")
        .or_else(|| raw.rsplit_once("\"%~dp0\\"))?;
    let (target, _) = rest.split_once('"')?;
    let mut path = shim.parent()?.to_path_buf();
    for part in target.split(['\\', '/']).filter(|p| !p.is_empty()) {
        // Global shims point into their own directory. A project's
        // node_modules\.bin shim climbs out of it and is not a global install.
        if part == ".." || part.contains(':') {
            return None;
        }
        path.push(part);
    }
    Some(path)
}

fn manifest_name(directory: &Path) -> Option<String> {
    let raw = fs::read_to_string(directory.join("package.json")).ok()?;
    let value: serde_json::Value = serde_json::from_str(&raw).ok()?;
    value["name"].as_str().map(str::to_owned)
}

fn installation_method(id: ClientId, resolved: &Path, home: &Path) -> InstallMethod {
    // Check package-manager ownership before native layouts. Never replace a
    // Homebrew or npm install with a second, unrelated installation. Homebrew
    // comes first because its formulae may wrap an npm layout.
    for ancestor in resolved.ancestors() {
        if ancestor
            .file_name()
            .is_some_and(|n| n == "Caskroom" || n == "Cellar")
        {
            let Some(root) = ancestor.parent() else {
                continue;
            };
            let Some(package) = resolved
                .strip_prefix(ancestor)
                .ok()
                .and_then(|p| p.components().next())
            else {
                continue;
            };
            let package = package.as_os_str().to_string_lossy().into_owned();
            if id.brew_packages().contains(&package.as_str()) {
                return InstallMethod::Brew {
                    root: root.into(),
                    package,
                    cask: ancestor.ends_with("Caskroom"),
                };
            }
        }
    }
    for ancestor in resolved.ancestors() {
        let Some(package) = id
            .packages()
            .iter()
            .find(|p| ancestor.ends_with(Path::new("node_modules").join(p)))
        else {
            continue;
        };
        if manifest_name(ancestor).as_deref() != Some(*package) {
            continue;
        }
        // pnpm/Bun/Yarn stores are not npm global installations. Only Pi's own
        // updater knows how to update them.
        let manager =
            resolved
                .components()
                .find_map(|p| match p.as_os_str().to_string_lossy().as_ref() {
                    ".pnpm" | "pnpm" => Some("pnpm"),
                    ".bun" => Some("bun"),
                    "yarn" | ".yarn" => Some("yarn"),
                    _ => None,
                });
        if let Some(manager) = manager {
            return if id == ClientId::Pi {
                InstallMethod::Package(manager)
            } else {
                InstallMethod::Unknown
            };
        }
        let prefix = ancestor
            .parent()
            .and_then(Path::parent)
            .and_then(Path::parent);
        if let Some(prefix) = prefix {
            if !cfg!(windows) && !prefix.ends_with("lib") {
                return InstallMethod::Unknown;
            }
            let prefix = if prefix.ends_with("lib") {
                prefix.parent().unwrap_or(prefix)
            } else {
                prefix
            };
            return InstallMethod::Npm {
                prefix: prefix.into(),
            };
        }
    }
    match id {
        ClientId::Claude if resolved.starts_with(home.join(".local/share/claude/versions")) => {
            return InstallMethod::Native;
        }
        ClientId::Cursor
            if resolved.starts_with(home.join(".local/share/cursor-agent/versions")) =>
        {
            return InstallMethod::Native;
        }
        ClientId::Codex => {
            for directory in resolved.ancestors().skip(1).take(3) {
                if let Ok(raw) = fs::read_to_string(directory.join("codex-package.json")) {
                    if let Ok(value) = serde_json::from_str::<serde_json::Value>(&raw) {
                        if value["variant"] == "codex"
                            && value["layoutVersion"] == 1
                            && value["entrypoint"] == "bin/codex"
                        {
                            return InstallMethod::Native;
                        }
                    }
                }
            }
        }
        ClientId::Pi => {
            // The managed installer links <agent>/bin/pi, a launcher that runs
            // the release selected under <agent>/install.
            let marker = resolved
                .parent()
                .and_then(Path::parent)
                .and_then(|agent| {
                    fs::read_to_string(agent.join("install/managed-install.json")).ok()
                })
                .and_then(|s| serde_json::from_str::<serde_json::Value>(&s).ok());
            if marker.is_some_and(|v| {
                v["kind"] == "pi-managed-install"
                    && v["schemaVersion"] == 1
                    && v["layout"] == "releases-v1"
            }) {
                return InstallMethod::Native;
            }
        }
        _ => {}
    }
    InstallMethod::Unknown
}

fn detect(environment: &Environment, id: ClientId) -> Option<Installation> {
    let mut paths = find_programs(&environment.path, id.name()).into_iter();
    let executable = paths.next()?;
    let mut resolved = dunce::canonicalize(&executable).ok()?;
    if cfg!(windows) && executable.extension().is_some_and(|s| s == "cmd") {
        if let Some(target) = cmd_shim_target(&executable)
            .filter(|t| t.is_file())
            .and_then(|t| dunce::canonicalize(t).ok())
        {
            resolved = target;
        }
    }
    let method = installation_method(id, &resolved, &environment.home);
    // `pi` is a generic command name. Only report a launcher or package that is
    // verifiably Pi; standalone binaries ship package.json beside themselves.
    if id == ClientId::Pi
        && method == InstallMethod::Unknown
        && !resolved
            .ancestors()
            .skip(1)
            .take(4)
            .filter_map(manifest_name)
            .any(|name| id.packages().contains(&name.as_str()))
    {
        return None;
    }
    Some(Installation {
        method,
        executable,
        resolved,
        others: paths.map(|p| p.to_string_lossy().into_owned()).collect(),
    })
}

fn parse_version(id: ClientId, raw: &str) -> Result<ClientVersion, ClientError> {
    raw.split_whitespace()
        .find_map(|token| ClientVersion::parse(id, token))
        .ok_or_else(|| ClientError::new("version", "Could not read a version"))
}

fn current_version(
    environment: &Environment,
    id: ClientId,
    install: &Installation,
) -> Result<ClientVersion, ClientError> {
    let mut command = client_command(environment, install)?;
    command.args.push("--version".into());
    parse_version(id, &process::run(environment, &command, PROBE_TIMEOUT)?)
}

fn node_program(environment: &Environment, prefix: &Path) -> Option<PathBuf> {
    let sibling = if cfg!(windows) {
        prefix.join("node.exe")
    } else {
        prefix.join("bin/node")
    };
    if executable(&sibling) {
        Some(sibling)
    } else {
        find_programs(&environment.path, "node").into_iter().next()
    }
}

fn client_command(
    environment: &Environment,
    install: &Installation,
) -> Result<CommandSpec, ClientError> {
    if cfg!(windows) && install.executable.extension().is_some_and(|s| s == "cmd") {
        return shim_command(environment, &install.executable, &install.resolved);
    }
    Ok(CommandSpec::new(&install.executable, &[]))
}

/// Runs a .cmd shim's target as the shim would, but with a literal argument
/// vector instead of a command line that cmd.exe parses again.
fn shim_command(
    environment: &Environment,
    shim: &Path,
    target: &Path,
) -> Result<CommandSpec, ClientError> {
    let extension = target
        .extension()
        .map(|s| s.to_string_lossy().to_ascii_lowercase());
    match extension.as_deref() {
        Some("exe") => Ok(CommandSpec::new(target, &[])),
        Some("js" | "cjs" | "mjs") => {
            // Like the shim, prefer a node.exe beside it, then PATH.
            let node = shim
                .parent()
                .and_then(|prefix| node_program(environment, prefix))
                .ok_or_else(|| ClientError::new("unsupported", "Node.js is unavailable"))?;
            Ok(CommandSpec {
                program: node,
                args: vec![target.as_os_str().into()],
            })
        }
        _ => Err(ClientError::new(
            "unsupported",
            "Unsupported command wrapper",
        )),
    }
}

/// Cursor's `update` command reads its release channel from cli-config.json.
fn cursor_channel(home: &Path) -> String {
    let channel = fs::read_to_string(home.join(".cursor/cli-config.json"))
        .ok()
        .and_then(|s| serde_json::from_str::<serde_json::Value>(&s).ok())
        .and_then(|v| v["channel"].as_str().map(str::to_owned));
    match channel.as_deref() {
        None | Some("prod-stable-internal") => "prod".into(),
        Some(channel) => channel.into(),
    }
}

fn update_command(
    environment: &Environment,
    id: ClientId,
    install: &Installation,
    latest: &ClientVersion,
) -> Result<CommandSpec, ClientError> {
    match &install.method {
        // Pi's updater handles its npm, Bun, pnpm, Yarn, and managed layouts,
        // including the move to the renamed package.
        InstallMethod::Native | InstallMethod::Npm { .. } | InstallMethod::Package(_)
            if id == ClientId::Pi =>
        {
            let mut command = client_command(environment, install)?;
            command.args.extend(["update".into(), "--self".into()]);
            Ok(command)
        }
        InstallMethod::Native => {
            if id == ClientId::Cursor && cursor_channel(&environment.home) == "static" {
                return Err(ClientError::new(
                    "unsupported",
                    "Updates are disabled for the static channel",
                ));
            }
            let mut command = client_command(environment, install)?;
            command.args.push("update".into());
            Ok(command)
        }
        InstallMethod::Brew {
            root,
            package,
            cask,
        } => {
            let brew = root.join("bin/brew");
            if !executable(&brew) {
                return Err(ClientError::new("unsupported", "Homebrew is unavailable"));
            }
            Ok(CommandSpec::new(
                brew,
                &[
                    "upgrade",
                    if *cask { "--cask" } else { "--formula" },
                    package,
                ],
            ))
        }
        InstallMethod::Npm { prefix } => {
            let npm = if cfg!(windows) {
                prefix.join("node_modules/npm/bin/npm-cli.js")
            } else {
                prefix.join("lib/node_modules/npm/bin/npm-cli.js")
            };
            let npm = if npm.is_file() {
                npm
            } else {
                find_programs(&environment.path, "npm")
                    .into_iter()
                    .filter_map(|p| {
                        // Node's Windows npm.cmd runs the npm-cli.js beside it.
                        if p.extension().is_some_and(|s| s == "cmd") {
                            p.parent()
                                .map(|d| d.join("node_modules/npm/bin/npm-cli.js"))
                        } else {
                            dunce::canonicalize(p).ok()
                        }
                    })
                    .find(|p| p.ends_with("npm/bin/npm-cli.js") && p.is_file())
                    .ok_or_else(|| ClientError::new("unsupported", "npm is unavailable"))?
            };
            let node = node_program(environment, prefix)
                .ok_or_else(|| ClientError::new("unsupported", "Node.js is unavailable"))?;
            Ok(CommandSpec {
                program: node,
                args: vec![
                    npm.into_os_string(),
                    "install".into(),
                    "--global".into(),
                    "--prefix".into(),
                    prefix.as_os_str().into(),
                    format!("{}@{latest}", id.packages()[0]).into(),
                ],
            })
        }
        InstallMethod::Package(_) | InstallMethod::Unknown => Err(ClientError::new(
            "unsupported",
            "Use the original installer or package manager",
        )),
    }
}

fn load_environment(home: PathBuf, use_system_proxy: bool) -> Result<Environment, ClientError> {
    let mut environment = Environment {
        home,
        path: std::env::var_os("PATH").unwrap_or_default(),
        use_system_proxy,
        proxy_env: None,
    };
    // GUI apps on macOS do not inherit a terminal's PATH (nvm/mise/asdf).
    // This fixed script only prints PATH; no frontend data becomes shell code.
    #[cfg(unix)]
    {
        let shell = std::env::var_os("SHELL")
            .map(PathBuf::from)
            .filter(|p| p.is_absolute() && executable(p))
            .unwrap_or_else(|| "/bin/sh".into());
        if let Ok(output) = process::run(
            &environment,
            &CommandSpec::new(
                shell,
                &["-ilc", "printf '\\n__ASTRLINK_PATH__%s\\n' \"$PATH\""],
            ),
            Duration::from_secs(4),
        ) {
            if let Some(value) = output
                .lines()
                .find_map(|l| l.strip_prefix("__ASTRLINK_PATH__"))
            {
                environment.path = value.into();
            }
        }
    }
    let mut paths: Vec<PathBuf> = std::env::split_paths(&environment.path)
        .filter(|p| p.is_absolute())
        .collect();
    for path in [
        environment.home.join(".local/bin"),
        environment.home.join(".npm-global/bin"),
        environment.home.join(".volta/bin"),
        PathBuf::from("/opt/homebrew/bin"),
        PathBuf::from("/usr/local/bin"),
        PathBuf::from("/usr/bin"),
        PathBuf::from("/bin"),
    ] {
        if path.is_absolute() && !paths.contains(&path) {
            paths.push(path);
        }
    }
    #[cfg(windows)]
    if let Some(appdata) = std::env::var_os("APPDATA") {
        paths.push(PathBuf::from(appdata).join("npm"));
    }
    environment.path = std::env::join_paths(paths).unwrap_or(environment.path);
    #[cfg(target_os = "macos")]
    if use_system_proxy {
        let output = process::run(
            &environment,
            &CommandSpec::new("/usr/sbin/scutil", &["--proxy"]),
            Duration::from_secs(3),
        )
        .map_err(|error| ClientError::new("proxy", error.detail))?;
        environment.proxy_env = Some(proxy::mac_environment(&output)?);
    }
    Ok(environment)
}

struct LatestSource {
    url: String,
    body: Option<serde_json::Value>,
}

fn latest_source(id: ClientId, install: &Installation, home: &Path) -> LatestSource {
    let url = match (&install.method, id) {
        (InstallMethod::Brew { package, cask, .. }, _) => format!(
            "https://formulae.brew.sh/api/{}/{package}.json",
            if *cask { "cask" } else { "formula" }
        ),
        (InstallMethod::Native, ClientId::Claude) => {
            let stable = fs::read_to_string(home.join(".claude/settings.json"))
                .ok()
                .and_then(|s| serde_json::from_str::<serde_json::Value>(&s).ok())
                .is_some_and(|v| v["autoUpdatesChannel"] == "stable");
            format!(
                "https://downloads.claude.ai/claude-code-releases/{}",
                if stable { "stable" } else { "latest" }
            )
        }
        // The same public endpoint `cursor-agent update` asks for its channel.
        (_, ClientId::Cursor) => {
            return LatestSource {
                url: CURSOR_LATEST_URL.into(),
                body: Some(serde_json::json!({ "channel": cursor_channel(home) })),
            }
        }
        (_, ClientId::Pi) => PI_LATEST_URL.into(),
        (_, ClientId::Codex | ClientId::Claude) => {
            format!("https://registry.npmjs.org/{}/latest", id.packages()[0])
        }
    };
    LatestSource { url, body: None }
}

fn parse_latest(
    body: &str,
    method: &InstallMethod,
    id: ClientId,
) -> Result<ClientVersion, ClientError> {
    let invalid = || ClientError::new("version", "Invalid latest version");
    if method == &InstallMethod::Native && id == ClientId::Claude {
        return ClientVersion::parse(id, body.trim()).ok_or_else(invalid);
    }
    let value: serde_json::Value =
        serde_json::from_str(body).map_err(|e| ClientError::new("version", e))?;
    let version = match method {
        InstallMethod::Brew { cask: false, .. } => value["versions"]["stable"].as_str(),
        _ => value["version"].as_str(),
    }
    .ok_or_else(|| ClientError::new("version", "Missing latest version"))?;
    let version = ClientVersion::parse(id, version).ok_or_else(invalid)?;
    if !version.order.pre.is_empty() {
        return Err(ClientError::new(
            "version",
            "Latest endpoint returned a prerelease",
        ));
    }
    Ok(version)
}

async fn fetch_latest(
    client: &reqwest::Client,
    source: &LatestSource,
    install: &Installation,
    id: ClientId,
) -> Result<ClientVersion, ClientError> {
    let request = match &source.body {
        Some(body) => client
            .post(&source.url)
            .header("connect-protocol-version", "1")
            .json(body),
        None => client.get(&source.url),
    };
    let mut response = request
        .send()
        .await
        .map_err(|e| ClientError::new("network", e))?;
    if response.status() == reqwest::StatusCode::TOO_MANY_REQUESTS {
        return Err(ClientError::new("rate_limit", "Version service rate limit"));
    }
    if !response.status().is_success() {
        return Err(ClientError::new("network", response.status()));
    }
    let mut bytes = Vec::new();
    while let Some(chunk) = response
        .chunk()
        .await
        .map_err(|e| ClientError::new("network", e))?
    {
        if bytes.len() + chunk.len() > 1024 * 1024 {
            return Err(ClientError::new("version", "Version response is too large"));
        }
        bytes.extend_from_slice(&chunk);
    }
    parse_latest(&String::from_utf8_lossy(&bytes), &install.method, id)
}

async fn inspect(
    environment: &Environment,
    client: &reqwest::Client,
    id: ClientId,
) -> (ClientStatus, Option<(Installation, ClientVersion)>) {
    let mut status = ClientStatus::new(id);
    status.checked_at = Some(chrono::Utc::now().to_rfc3339());
    let Some(install) = detect(environment, id) else {
        status.phase = "not_installed".into();
        return (status, None);
    };
    status.executable = Some(install.executable.to_string_lossy().into_owned());
    status.install_method = install.method.label().into();
    status.other_installations.clone_from(&install.others);
    let environment_copy = environment.clone();
    let install_copy = install.clone();
    let current = tauri::async_runtime::spawn_blocking(move || {
        current_version(&environment_copy, id, &install_copy)
    })
    .await
    .unwrap_or_else(|e| Err(ClientError::new("command", e)));
    let current = match current {
        Ok(v) => v,
        Err(e) => {
            status.fail(e);
            return (status, None);
        }
    };
    status.current_version = Some(current.to_string());
    let latest = match fetch_latest(
        client,
        &latest_source(id, &install, &environment.home),
        &install,
        id,
    )
    .await
    {
        Ok(v) => v,
        Err(e) => {
            status.fail(e);
            return (status, None);
        }
    };
    status.latest_version = Some(latest.to_string());
    let available = latest.newer_than(&current);
    status.phase = if available { "available" } else { "up_to_date" }.into();
    status.can_update = available && update_command(environment, id, &install, &latest).is_ok();
    if available && !status.can_update {
        status.phase = "manual".into();
    }
    (status, Some((install, latest)))
}

fn perform_update(
    environment: &Environment,
    id: ClientId,
    install: &Installation,
    latest: &ClientVersion,
) -> Result<ClientVersion, ClientError> {
    let fresh = detect(environment, id)
        .ok_or_else(|| ClientError::new("changed", "Client installation changed"))?;
    if fresh.executable != install.executable
        || fresh.resolved != install.resolved
        || fresh.method != install.method
    {
        return Err(ClientError::new("changed", "Client installation changed"));
    }
    let current = current_version(environment, id, &fresh)?;
    // Another updater may have replaced this file in place since the check.
    if !latest.newer_than(&current) {
        return Ok(current);
    }
    let command = update_command(environment, id, &fresh, latest)?;
    let output = process::run(environment, &command, UPDATE_TIMEOUT)?;
    let after = detect(environment, id)
        .ok_or_else(|| ClientError::new("command", "Client disappeared after update"))?;
    let version = current_version(environment, id, &after)?;
    if !version.newer_than(&current) {
        return Err(ClientError::new(
            "unchanged",
            format!(
                "The updater finished without installing a newer version.\n{}",
                output.trim()
            ),
        ));
    }
    Ok(version)
}

#[derive(Default)]
pub struct ClientUpdateManager {
    snapshot: Mutex<ClientSnapshot>,
    operation: Arc<tokio::sync::Mutex<()>>,
}

impl ClientUpdateManager {
    fn snapshot(&self) -> ClientSnapshot {
        self.snapshot
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .clone()
    }
    fn publish(&self, app: &AppHandle, action: impl FnOnce(&mut ClientSnapshot)) {
        let next = {
            let mut value = self.snapshot.lock().unwrap_or_else(|e| e.into_inner());
            action(&mut value);
            value.revision += 1;
            value.clone()
        };
        let _ = app.emit(EVENT, next);
    }
    fn start(
        self: &Arc<Self>,
        app: AppHandle,
        update: Vec<ClientId>,
    ) -> Result<ClientSnapshot, String> {
        let Ok(guard) = self.operation.clone().try_lock_owned() else {
            return Ok(self.snapshot());
        };
        let home = crate::control_session::user_home()?;
        let use_system_proxy = app
            .state::<Arc<crate::preferences::PreferencesStore>>()
            .snapshot()
            .values
            .use_system_proxy;
        let mut builder = reqwest::Client::builder().timeout(Duration::from_secs(20));
        if !use_system_proxy {
            builder = builder.no_proxy();
        }
        let client = builder.build().map_err(|e| e.to_string())?;
        self.publish(&app, |s| {
            s.busy = true;
            for status in &mut s.clients {
                if update.is_empty() || update.contains(&status.id) {
                    status.phase = if update.is_empty() {
                        "checking"
                    } else {
                        "updating"
                    }
                    .into();
                    status.can_update = false;
                    status.error_code = None;
                    status.error_detail = None;
                }
            }
        });
        let initial = self.snapshot();
        let manager = Arc::clone(self);
        // The host owns the task. Closing/navigating away from About only drops
        // a listener; it never cancels a package-manager update.
        tauri::async_runtime::spawn(async move {
            let _guard = guard;
            let environment = tauri::async_runtime::spawn_blocking(move || {
                load_environment(home, use_system_proxy)
            })
            .await
            .unwrap_or_else(|e| Err(ClientError::new("command", e)));
            match environment {
                Ok(environment) => {
                    for id in ClientId::ALL {
                        if !update.is_empty() && !update.contains(&id) {
                            continue;
                        }
                        let (mut status, plan) = inspect(&environment, &client, id).await;
                        if update.contains(&id) && status.can_update {
                            if let Some((install, latest)) = plan {
                                status.phase = "updating".into();
                                status.can_update = false;
                                manager.replace(&app, status.clone());
                                let env = environment.clone();
                                let result = tauri::async_runtime::spawn_blocking(move || {
                                    perform_update(&env, id, &install, &latest)
                                })
                                .await
                                .unwrap_or_else(|e| Err(ClientError::new("command", e)));
                                match result {
                                    Ok(version) => {
                                        status.current_version = Some(version.to_string());
                                        status.phase = "updated".into();
                                        status.checked_at = Some(chrono::Utc::now().to_rfc3339());
                                    }
                                    Err(error) => status.fail(error),
                                }
                            }
                        }
                        manager.replace(&app, status);
                    }
                }
                Err(error) => manager.publish(&app, |s| {
                    for item in &mut s.clients {
                        if ["checking", "updating"].contains(&item.phase.as_str()) {
                            item.fail(ClientError::new(error.code, &error.detail));
                        }
                    }
                }),
            }
            manager.publish(&app, |s| s.busy = false);
        });
        Ok(initial)
    }
    fn replace(&self, app: &AppHandle, status: ClientStatus) {
        self.publish(app, |s| {
            if let Some(item) = s.clients.iter_mut().find(|c| c.id == status.id) {
                *item = status;
            }
        });
    }
}

#[tauri::command]
pub fn local_client_status(manager: State<'_, Arc<ClientUpdateManager>>) -> ClientSnapshot {
    manager.snapshot()
}

#[tauri::command]
pub fn refresh_local_clients(
    app: AppHandle,
    manager: State<'_, Arc<ClientUpdateManager>>,
) -> Result<ClientSnapshot, String> {
    manager.start(app, vec![])
}

#[tauri::command]
pub fn update_local_clients(
    app: AppHandle,
    manager: State<'_, Arc<ClientUpdateManager>>,
    ids: Vec<ClientId>,
) -> Result<ClientSnapshot, String> {
    if ids.is_empty() || ids.iter().collect::<HashSet<_>>().len() != ids.len() {
        return Err("Invalid client selection".into());
    }
    manager.start(app, ids)
}

#[cfg(test)]
mod tests;
