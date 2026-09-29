mod process;
#[cfg(any(target_os = "macos", test))]
mod proxy;

use std::{
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

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum ClientId {
    Codex,
    Claude,
}

impl ClientId {
    fn name(self) -> &'static str {
        match self {
            Self::Codex => "codex",
            Self::Claude => "claude",
        }
    }
    fn package(self) -> &'static str {
        match self {
            Self::Codex => "@openai/codex",
            Self::Claude => "@anthropic-ai/claude-code",
        }
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
            clients: [ClientId::Codex, ClientId::Claude]
                .map(ClientStatus::new)
                .to_vec(),
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
        #[cfg(windows)]
        let names = [
            format!("{name}.exe"),
            format!("{name}.cmd"),
            name.to_string(),
        ];
        #[cfg(not(windows))]
        let names = [name.to_string()];
        for name in names {
            let file = directory.join(name);
            if executable(&file) {
                if let Ok(resolved) = fs::canonicalize(&file) {
                    if seen.insert(resolved) {
                        found.push(file);
                    }
                }
            }
        }
    }
    found
}

fn installation_method(id: ClientId, resolved: &Path, home: &Path) -> InstallMethod {
    // Check package-manager ownership before native layouts. Never replace a
    // Homebrew or npm install with a second, unrelated installation.
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
            if (id == ClientId::Codex && package == "codex")
                || (id == ClientId::Claude
                    && ["claude-code", "claude-code@latest"].contains(&package.as_str()))
            {
                return InstallMethod::Brew {
                    root: root.into(),
                    package,
                    cask: ancestor.ends_with("Caskroom"),
                };
            }
        }
        if ancestor.ends_with(Path::new("node_modules").join(id.package())) {
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
                // pnpm/Bun/Yarn stores are not npm global installations.
                if resolved.components().any(|p| {
                    [".pnpm", "pnpm", ".bun", "yarn", ".yarn"]
                        .contains(&p.as_os_str().to_string_lossy().as_ref())
                }) {
                    return InstallMethod::Unknown;
                }
                let manifest = fs::read_to_string(ancestor.join("package.json"))
                    .ok()
                    .and_then(|s| serde_json::from_str::<serde_json::Value>(&s).ok());
                if manifest.as_ref().and_then(|v| v["name"].as_str()) == Some(id.package()) {
                    return InstallMethod::Npm {
                        prefix: prefix.into(),
                    };
                }
            }
        }
    }
    if id == ClientId::Claude && resolved.starts_with(home.join(".local/share/claude/versions")) {
        return InstallMethod::Native;
    }
    if id == ClientId::Codex {
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
    InstallMethod::Unknown
}

fn detect(environment: &Environment, id: ClientId) -> Option<Installation> {
    let mut paths = find_programs(&environment.path, id.name()).into_iter();
    let executable = paths.next()?;
    let mut resolved = fs::canonicalize(&executable).ok()?;
    if cfg!(windows) && executable.extension().is_some_and(|s| s == "cmd") {
        let entry = executable
            .parent()?
            .join("node_modules")
            .join(id.package())
            .join(match id {
                ClientId::Codex => "bin/codex.js",
                ClientId::Claude => "cli.js",
            });
        if entry.is_file() {
            resolved = fs::canonicalize(entry).ok()?;
        }
    }
    Some(Installation {
        method: installation_method(id, &resolved, &environment.home),
        executable,
        resolved,
        others: paths.map(|p| p.to_string_lossy().into_owned()).collect(),
    })
}

fn parse_version(raw: &str) -> Result<Version, ClientError> {
    raw.split_whitespace()
        .find_map(|token| {
            Version::parse(token.trim_matches(['(', ')']).trim_start_matches('v')).ok()
        })
        .ok_or_else(|| ClientError::new("version", "Could not read a semantic version"))
}

fn current_version(
    environment: &Environment,
    install: &Installation,
) -> Result<Version, ClientError> {
    // On Windows avoid passing a .cmd wrapper through cmd.exe; run the known
    // npm entry point through Node with a literal argument vector instead.
    let mut command = client_command(environment, install)?;
    command.args.push("--version".into());
    parse_version(&process::run(environment, &command, PROBE_TIMEOUT)?)
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
        if let InstallMethod::Npm { prefix } = &install.method {
            let node = node_program(environment, prefix)
                .ok_or_else(|| ClientError::new("unsupported", "Node.js is unavailable"))?;
            return Ok(CommandSpec {
                program: node,
                args: vec![install.resolved.as_os_str().into()],
            });
        }
        return Err(ClientError::new(
            "unsupported",
            "Unsupported command wrapper",
        ));
    }
    Ok(CommandSpec::new(&install.executable, &[]))
}

fn update_command(
    environment: &Environment,
    id: ClientId,
    install: &Installation,
    latest: &Version,
) -> Result<CommandSpec, ClientError> {
    match &install.method {
        InstallMethod::Native => {
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
                    .filter_map(|p| fs::canonicalize(p).ok())
                    .find(|p| p.ends_with("npm/bin/npm-cli.js"))
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
                    format!("{}@{latest}", id.package()).into(),
                ],
            })
        }
        InstallMethod::Unknown => Err(ClientError::new(
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

fn latest_url(id: ClientId, install: &Installation, home: &Path) -> String {
    match &install.method {
        InstallMethod::Brew { package, cask, .. } => format!(
            "https://formulae.brew.sh/api/{}/{package}.json",
            if *cask { "cask" } else { "formula" }
        ),
        InstallMethod::Native if id == ClientId::Claude => {
            let stable = fs::read_to_string(home.join(".claude/settings.json"))
                .ok()
                .and_then(|s| serde_json::from_str::<serde_json::Value>(&s).ok())
                .is_some_and(|v| v["autoUpdatesChannel"] == "stable");
            format!(
                "https://downloads.claude.ai/claude-code-releases/{}",
                if stable { "stable" } else { "latest" }
            )
        }
        _ => format!("https://registry.npmjs.org/{}/latest", id.package()),
    }
}

fn parse_latest(body: &str, method: &InstallMethod, id: ClientId) -> Result<Version, ClientError> {
    if method == &InstallMethod::Native && id == ClientId::Claude {
        return Version::parse(body.trim()).map_err(|e| ClientError::new("version", e));
    }
    let value: serde_json::Value =
        serde_json::from_str(body).map_err(|e| ClientError::new("version", e))?;
    let version = match method {
        InstallMethod::Brew { cask: false, .. } => value["versions"]["stable"].as_str(),
        _ => value["version"].as_str(),
    }
    .ok_or_else(|| ClientError::new("version", "Missing latest version"))?;
    let version = Version::parse(version).map_err(|e| ClientError::new("version", e))?;
    if !version.pre.is_empty() {
        return Err(ClientError::new(
            "version",
            "Latest endpoint returned a prerelease",
        ));
    }
    Ok(version)
}

async fn fetch_latest(
    client: &reqwest::Client,
    url: &str,
    install: &Installation,
    id: ClientId,
) -> Result<Version, ClientError> {
    let mut response = client
        .get(url)
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
) -> (ClientStatus, Option<(Installation, Version)>) {
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
        current_version(&environment_copy, &install_copy)
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
        &latest_url(id, &install, &environment.home),
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
    status.phase = if latest > current {
        "available"
    } else {
        "up_to_date"
    }
    .into();
    status.can_update =
        latest > current && update_command(environment, id, &install, &latest).is_ok();
    if latest > current && !status.can_update {
        status.phase = "manual".into();
    }
    (status, Some((install, latest)))
}

fn perform_update(
    environment: &Environment,
    id: ClientId,
    install: &Installation,
    latest: &Version,
) -> Result<Version, ClientError> {
    let fresh = detect(environment, id)
        .ok_or_else(|| ClientError::new("changed", "Client installation changed"))?;
    if fresh.executable != install.executable
        || fresh.resolved != install.resolved
        || fresh.method != install.method
    {
        return Err(ClientError::new("changed", "Client installation changed"));
    }
    let current = current_version(environment, &fresh)?;
    // Another updater may have replaced this file in place since the check.
    if current >= *latest {
        return Ok(current);
    }
    let command = update_command(environment, id, &fresh, latest)?;
    let output = process::run(environment, &command, UPDATE_TIMEOUT)?;
    let after = detect(environment, id)
        .ok_or_else(|| ClientError::new("command", "Client disappeared after update"))?;
    let version = current_version(environment, &after)?;
    if version <= current {
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
                    for id in [ClientId::Codex, ClientId::Claude] {
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
    if ids.is_empty() || ids.len() > 2 || (ids.len() == 2 && ids[0] == ids[1]) {
        return Err("Invalid client selection".into());
    }
    manager.start(app, ids)
}

#[cfg(test)]
mod tests;
