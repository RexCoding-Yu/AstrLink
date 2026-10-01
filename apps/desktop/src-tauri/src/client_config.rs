//! Writes AstrLink's gateway connection straight into a CLI client's
//! user-level config, and keeps the base URL current when the port changes.
//!
//! Only the keys listed in `~/.astrlink/client-configs.json` belong to
//! AstrLink. Each edit is made in place, keeping the rest of the file's keys,
//! comments, and layout. Verified against Claude Code 2.1.283 and Codex CLI
//! 0.159.2.

use std::{
    collections::BTreeMap,
    path::{Path, PathBuf},
    time::{SystemTime, UNIX_EPOCH},
};

use jsonc_parser::{
    cst::{CstInputValue, CstObject, CstRootNode},
    ParseOptions,
};
use reqwest::Url;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use toml_edit::{DocumentMut, InlineTable, Item, Table, TableLike, Value};

use crate::{
    control_session::astrlink_home,
    host_files::{self, WriteOptions},
    sidecar::{CoreManager, CorePhase},
};

const RECORDS_VERSION: u32 = 1;
const CODEX_PROVIDER: &str = "astrlink";
const CODEX_PROVIDER_PREFIX: &str = "model_providers.astrlink.";

#[derive(Clone, Copy, Debug, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum Client {
    Claude,
    Codex,
    Gemini,
    Opencode,
    Openclaw,
}

impl Client {
    pub fn name(self) -> &'static str {
        match self {
            Self::Claude => "claude",
            Self::Codex => "codex",
            Self::Gemini => "gemini",
            Self::Opencode => "opencode",
            Self::Openclaw => "openclaw",
        }
    }

    /// Clients AstrLink configures itself; the rest go through CC Switch.
    const WRITABLE: [Self; 2] = [Self::Claude, Self::Codex];

    fn writable(self) -> Result<Self, String> {
        if Self::WRITABLE.contains(&self) {
            Ok(self)
        } else {
            Err("this client can only be configured through CC Switch".into())
        }
    }

    fn detected(self, home: &Path) -> bool {
        match self {
            Self::Claude => home.join(".claude").is_dir() || home.join(".claude.json").is_file(),
            Self::Codex => home.join(".codex").is_dir(),
            Self::Gemini | Self::Opencode | Self::Openclaw => false,
        }
    }

    fn config_path(self, home: &Path) -> PathBuf {
        match self {
            Self::Codex => home.join(".codex").join("config.toml"),
            _ => home.join(".claude").join("settings.json"),
        }
    }

    /// Claude Code appends its own API path; Codex needs the OpenAI `/v1` root.
    fn base_url(self, origin: &str) -> String {
        match self {
            Self::Codex => format!("{origin}/v1"),
            _ => origin.to_string(),
        }
    }

    fn base_url_key(self) -> &'static str {
        match self {
            Self::Codex => "model_providers.astrlink.base_url",
            _ => "env.ANTHROPIC_BASE_URL",
        }
    }

    /// Keys that decide where requests go and how they authenticate. A model
    /// the client rewrote itself, like Codex's `/model`, is not among them.
    fn connection_keys(self) -> &'static [&'static str] {
        match self {
            Self::Codex => &[
                "model_provider",
                "model_providers.astrlink.base_url",
                "model_providers.astrlink.wire_api",
                "model_providers.astrlink.experimental_bearer_token",
            ],
            _ => &["env.ANTHROPIC_BASE_URL", "env.ANTHROPIC_AUTH_TOKEN"],
        }
    }
}

#[derive(Default, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Models {
    pub model: Option<String>,
    pub haiku_model: Option<String>,
    pub sonnet_model: Option<String>,
    pub opus_model: Option<String>,
    pub fable_model: Option<String>,
}

impl Models {
    /// The filled-in models, trimmed and named by their camelCase field.
    /// Claude Code takes every tier; other clients take one required model.
    pub fn fields(&self, client: Client) -> Result<Vec<(&'static str, String)>, String> {
        let mut fields = vec![("model", self.model.as_deref())];
        if client == Client::Claude {
            fields.extend([
                ("haikuModel", self.haiku_model.as_deref()),
                ("sonnetModel", self.sonnet_model.as_deref()),
                ("opusModel", self.opus_model.as_deref()),
                ("fableModel", self.fable_model.as_deref()),
            ]);
        }
        let mut values = Vec::new();
        for (key, value) in fields {
            let Some(value) = value.map(str::trim).filter(|value| !value.is_empty()) else {
                continue;
            };
            if value.chars().count() > 256 || value.chars().any(char::is_control) {
                return Err(format!("invalid {key}"));
            }
            values.push((key, value.to_string()));
        }
        if client != Client::Claude && values.is_empty() {
            return Err("model is required for this client".into());
        }
        Ok(values)
    }
}

/// `http://127.0.0.1:<port>` or `http://localhost:<port>` for the gateway's
/// root inference URL; Core announces localhost only while it serves `[::1]`.
pub fn local_origin(inference_url: &str) -> Result<String, String> {
    let url = Url::parse(inference_url).map_err(|_| "invalid inference URL")?;
    if url.scheme() != "http"
        || !matches!(url.host_str(), Some("127.0.0.1" | "localhost"))
        || !url.username().is_empty()
        || url.password().is_some()
        || url.path() != "/"
        || url.query().is_some()
        || url.fragment().is_some()
    {
        return Err("invalid local inference URL".into());
    }
    Ok(url.as_str().trim_end_matches('/').to_string())
}

/// Reveals a token for the gateway session the dialog was opened against,
/// so a restart in between cannot pair the token with a stale address.
pub async fn reveal_access_token(
    manager: &CoreManager,
    token_id: &str,
    inference_url: &str,
) -> Result<String, String> {
    let changed = || "gateway session changed; reopen the dialog".to_string();
    let before = manager.snapshot();
    if before.phase != CorePhase::Ready
        || before
            .ready
            .as_ref()
            .map(|ready| ready.client_inference_url.as_str())
            != Some(inference_url)
    {
        return Err(changed());
    }
    let secret = manager.reveal_access_token(token_id).await?;
    let after = manager.snapshot();
    if after.phase != CorePhase::Ready || before.pid != after.pid || before.ready != after.ready {
        return Err(changed());
    }
    secret["access_token"]
        .as_str()
        .filter(|token| !token.is_empty())
        .map(str::to_string)
        .ok_or_else(|| "access token is unavailable".to_string())
}

/// A token's list entry: its display name and non-secret hint.
pub async fn token_summary(
    manager: &CoreManager,
    token_id: &str,
) -> Result<(String, String), String> {
    let page = manager.list_access_tokens().await?;
    page["items"]
        .as_array()
        .into_iter()
        .flatten()
        .find(|item| item["id"].as_str() == Some(token_id))
        .and_then(|item| Some((item["name"].as_str()?, item["hint"].as_str()?)))
        .map(|(name, hint)| (name.to_string(), hint.to_string()))
        .ok_or_else(|| "access token not found".to_string())
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum ClientState {
    NotConfigured,
    Configured,
    /// Still AstrLink's, pointing at a port the gateway no longer uses.
    Outdated,
    /// A connection key changed since AstrLink wrote it.
    Modified,
    /// The config file cannot be parsed.
    Invalid,
}

#[derive(Debug, Serialize)]
pub struct ClientStatus {
    pub client: Client,
    pub detected: bool,
    pub paths: Vec<String>,
    pub state: ClientState,
    pub token_id: Option<String>,
}

#[derive(Debug, PartialEq, Eq, Serialize)]
#[serde(tag = "status", rename_all = "snake_case")]
pub enum ApplyOutcome {
    Applied,
    /// Nothing was written: these keys hold values AstrLink did not write.
    NeedsConfirmation {
        keys: Vec<String>,
    },
}

/// What AstrLink wrote for one client. Values are kept only as SHA-256
/// fingerprints, so the file holds neither the token nor anything replaced.
#[derive(Clone, Debug, PartialEq, Eq, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ClientRecord {
    pub token_id: String,
    pub token_sha256: String,
    pub base_url: String,
    pub keys: BTreeMap<String, String>,
    pub created_file: bool,
    pub written_at_unix: u64,
}

#[derive(Debug, Default, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Records {
    version: u32,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    claude: Option<ClientRecord>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    codex: Option<ClientRecord>,
}

impl Records {
    fn slot(&mut self, client: Client) -> &mut Option<ClientRecord> {
        match client {
            Client::Codex => &mut self.codex,
            _ => &mut self.claude,
        }
    }

    fn get(&self, client: Client) -> Option<&ClientRecord> {
        match client {
            Client::Claude => self.claude.as_ref(),
            Client::Codex => self.codex.as_ref(),
            _ => None,
        }
    }
}

fn records_path(home: &Path) -> PathBuf {
    astrlink_home(home).join("client-configs.json")
}

fn read_records(home: &Path) -> Result<Records, String> {
    let path = records_path(home);
    let Some(raw) = host_files::read_optional(&path)? else {
        return Ok(Records::default());
    };
    let records: Records = serde_json::from_str(&raw)
        .map_err(|_| format!("{} is not a valid record file", path.display()))?;
    if records.version != RECORDS_VERSION {
        return Err(format!("{} has an unknown version", path.display()));
    }
    Ok(records)
}

fn write_records(home: &Path, records: &mut Records) -> Result<(), String> {
    let path = records_path(home);
    if records.claude.is_none() && records.codex.is_none() {
        return host_files::remove_path(&path);
    }
    records.version = RECORDS_VERSION;
    let mut encoded = serde_json::to_string_pretty(records)
        .map_err(|error| format!("unable to encode client records: {error}"))?;
    encoded.push('\n');
    host_files::write_text(
        &path,
        &encoded,
        WriteOptions {
            secret: true,
            ..Default::default()
        },
    )
}

fn sha256_hex(value: &str) -> String {
    format!("{:x}", Sha256::digest(value.as_bytes()))
}

fn unix_now() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_secs())
        .unwrap_or(0)
}

/// A client config file opened for in-place editing. Keys are dotted paths
/// from the file's root; `None` in a lookup result is a non-string value.
enum Document {
    Claude {
        root: CstRootNode,
        object: CstObject,
    },
    Codex {
        document: DocumentMut,
        crlf: bool,
    },
}

/// Claude Code settings keys that choose the endpoint, credential, or model.
fn claude_group_env(name: &str) -> bool {
    name.starts_with("ANTHROPIC_")
        || matches!(
            name,
            "CLAUDE_CODE_OAUTH_TOKEN"
                | "CLAUDE_CODE_SUBAGENT_MODEL"
                | "CLAUDE_CODE_USE_BEDROCK"
                | "CLAUDE_CODE_USE_VERTEX"
                | "CLAUDE_CODE_USE_FOUNDRY"
        )
}

fn toml_string(item: &Item) -> Option<String> {
    item.as_str().map(str::to_string)
}

/// Replaces a string in place, keeping the comment and spacing around it.
fn set_toml_string(table: &mut dyn TableLike, key: &str, value: &str) {
    match table.get_mut(key) {
        Some(Item::Value(current)) => {
            let decor = current.decor().clone();
            *current = Value::from(value);
            *current.decor_mut() = decor;
        }
        Some(item) => *item = toml_edit::value(value),
        None => {
            table.insert(key, toml_edit::value(value));
        }
    }
}

/// Removes a root key but keeps the comment above it, handing it to the
/// neighbouring key: a file's header note often sits above `model`.
fn remove_toml_root_key(document: &mut DocumentMut, key: &str) {
    let root = document.as_table_mut();
    let Some(index) = root.iter().position(|(name, _)| name == key) else {
        return;
    };
    let comment = root
        .key(key)
        .and_then(|key| key.leaf_decor().prefix())
        .and_then(|prefix| prefix.as_str())
        .filter(|prefix| prefix.contains('#'))
        .map(str::to_string);
    root.remove(key);
    let Some(comment) = comment else {
        return;
    };
    let values = root
        .iter()
        .enumerate()
        .filter(|(_, (_, item))| item.is_value())
        .map(|(position, (name, _))| (position, name.to_string()))
        .collect::<Vec<_>>();
    let raw = |text: Option<&toml_edit::RawString>| {
        text.and_then(|text| text.as_str())
            .unwrap_or_default()
            .to_string()
    };
    if let Some((_, next)) = values.iter().find(|(position, _)| *position >= index) {
        if let Some(mut key) = root.key_mut(next) {
            let decor = key.leaf_decor_mut();
            let rest = raw(decor.prefix());
            decor.set_prefix(format!("{comment}{rest}"));
        }
    } else if let Some((_, previous)) = values.last() {
        if let Some(value) = root.get_mut(previous).and_then(Item::as_value_mut) {
            let decor = value.decor_mut();
            let suffix = raw(decor.suffix());
            decor.set_suffix(format!("{suffix}\n{}", comment.trim_end_matches('\n')));
        }
    } else {
        let decor = root.decor_mut();
        let rest = raw(decor.prefix());
        decor.set_prefix(format!("{comment}{rest}"));
    }
}

impl Document {
    fn parse(client: Client, existing: Option<&str>) -> Result<Self, String> {
        let text = existing.unwrap_or_default();
        match client {
            Client::Codex => Ok(Self::Codex {
                document: text.parse().map_err(|_| "is not valid TOML".to_string())?,
                crlf: text.contains("\r\n"),
            }),
            _ => {
                // Claude Code reads strict JSON; the CST parser also takes
                // comments, so validate strictly first.
                if !text.trim().is_empty() {
                    let value: serde_json::Value = serde_json::from_str(text).map_err(|error| {
                        format!(
                            "is not valid JSON (line {}, column {})",
                            error.line(),
                            error.column()
                        )
                    })?;
                    if !value.is_object() {
                        return Err("is not a JSON object".into());
                    }
                    if value.get("env").is_some_and(|env| !env.is_object()) {
                        return Err("has an env value that is not an object".into());
                    }
                }
                let root = CstRootNode::parse(text, &ParseOptions::default())
                    .map_err(|_| "is not valid JSON".to_string())?;
                let object = root.object_value_or_set();
                Ok(Self::Claude { root, object })
            }
        }
    }

    fn get(&self, path: &str) -> Option<Option<String>> {
        match self {
            Self::Claude { object, .. } => {
                let property = match path.split_once('.') {
                    Some(("env", name)) => object.object_value("env")?.get(name)?,
                    _ => object.get(path)?,
                };
                Some(
                    property
                        .value()
                        .and_then(|value| value.as_string_lit())
                        .and_then(|value| value.decoded_value().ok()),
                )
            }
            Self::Codex { document, .. } => {
                let Some(key) = path.strip_prefix(CODEX_PROVIDER_PREFIX) else {
                    return document.get(path).map(toml_string);
                };
                document
                    .get("model_providers")?
                    .as_table_like()?
                    .get(CODEX_PROVIDER)?
                    .as_table_like()?
                    .get(key)
                    .map(toml_string)
            }
        }
    }

    /// Every key in the group AstrLink takes over, with its current value.
    fn group(&self) -> Vec<(String, Option<String>)> {
        let mut keys = Vec::new();
        match self {
            Self::Claude { object, .. } => {
                if let Some(env) = object.object_value("env") {
                    for property in env.properties() {
                        let Some(name) = property.decoded_name() else {
                            continue;
                        };
                        if claude_group_env(&name) {
                            let path = format!("env.{name}");
                            let value = self.get(&path).flatten();
                            keys.push((path, value));
                        }
                    }
                }
                if object.get("apiKeyHelper").is_some() {
                    keys.push(("apiKeyHelper".into(), self.get("apiKeyHelper").flatten()));
                }
            }
            Self::Codex { document, .. } => {
                for key in ["model_provider", "model"] {
                    if let Some(item) = document.get(key) {
                        keys.push((key.into(), toml_string(item)));
                    }
                }
                let provider = document
                    .get("model_providers")
                    .and_then(Item::as_table_like)
                    .and_then(|providers| providers.get(CODEX_PROVIDER));
                match provider.map(|item| item.as_table_like()) {
                    Some(Some(table)) => {
                        for (key, item) in table.iter() {
                            keys.push((format!("{CODEX_PROVIDER_PREFIX}{key}"), toml_string(item)));
                        }
                    }
                    Some(None) => keys.push(("model_providers.astrlink".into(), None)),
                    None => {}
                }
            }
        }
        keys
    }

    fn set(&mut self, path: &str, value: &str) -> Result<(), String> {
        match self {
            Self::Claude { object, .. } => {
                let Some(("env", name)) = path.split_once('.') else {
                    return Err(format!("unable to set {path}"));
                };
                let env = object
                    .object_value_or_create("env")
                    .ok_or("has an env value that is not an object")?;
                match env.get(name) {
                    Some(property) => property.set_value(CstInputValue::String(value.into())),
                    None => {
                        env.append(name, CstInputValue::String(value.into()));
                    }
                }
            }
            Self::Codex { document, .. } => {
                let Some(key) = path.strip_prefix(CODEX_PROVIDER_PREFIX) else {
                    set_toml_string(document.as_table_mut(), path, value);
                    return Ok(());
                };
                let root = document.as_table_mut();
                if !root.contains_key("model_providers") {
                    let mut providers = Table::new();
                    providers.set_implicit(true);
                    root.insert("model_providers", Item::Table(providers));
                }
                let providers = &mut root["model_providers"];
                let inline = providers.is_inline_table();
                let providers = providers
                    .as_table_like_mut()
                    .ok_or("has a model_providers value that is not a table")?;
                if providers
                    .get(CODEX_PROVIDER)
                    .and_then(Item::as_table_like)
                    .is_none()
                {
                    let table = if inline {
                        Item::Value(Value::InlineTable(InlineTable::new()))
                    } else {
                        Item::Table(Table::new())
                    };
                    providers.insert(CODEX_PROVIDER, table);
                }
                let provider = providers
                    .get_mut(CODEX_PROVIDER)
                    .and_then(Item::as_table_like_mut)
                    .ok_or("unable to create model_providers.astrlink")?;
                set_toml_string(provider, key, value);
            }
        }
        Ok(())
    }

    fn remove(&mut self, path: &str) {
        match self {
            Self::Claude { object, .. } => {
                let property = match path.split_once('.') {
                    Some(("env", name)) => object.object_value("env").and_then(|env| env.get(name)),
                    _ => object.get(path),
                };
                if let Some(property) = property {
                    property.remove();
                }
            }
            Self::Codex { document, .. } => {
                let providers = document
                    .get_mut("model_providers")
                    .and_then(Item::as_table_like_mut);
                if path == "model_providers.astrlink" {
                    if let Some(providers) = providers {
                        providers.remove(CODEX_PROVIDER);
                    }
                } else if let Some(key) = path.strip_prefix(CODEX_PROVIDER_PREFIX) {
                    if let Some(provider) = providers
                        .and_then(|providers| providers.get_mut(CODEX_PROVIDER))
                        .and_then(Item::as_table_like_mut)
                    {
                        provider.remove(key);
                    }
                } else {
                    remove_toml_root_key(document, path);
                }
            }
        }
    }

    /// Drops the containers a removal left empty.
    fn tidy(&mut self) {
        match self {
            Self::Claude { object, .. } => {
                if object
                    .object_value("env")
                    .is_some_and(|env| env.properties().is_empty())
                {
                    self.remove("env");
                }
            }
            Self::Codex { document, .. } => {
                let empty = |item: Option<&Item>| {
                    item.and_then(Item::as_table_like)
                        .is_some_and(|table| table.is_empty())
                };
                if let Some(providers) = document
                    .get_mut("model_providers")
                    .and_then(Item::as_table_like_mut)
                {
                    if empty(providers.get(CODEX_PROVIDER)) {
                        providers.remove(CODEX_PROVIDER);
                    }
                }
                if empty(document.get("model_providers")) {
                    document.remove("model_providers");
                }
            }
        }
    }

    fn is_empty(&self) -> bool {
        match self {
            Self::Claude { object, .. } => object.properties().is_empty(),
            Self::Codex { document, .. } => document.to_string().trim().is_empty(),
        }
    }

    fn render(&self) -> String {
        match self {
            Self::Claude { root, .. } => {
                let mut text = root.to_string();
                if !text.ends_with('\n') {
                    text.push('\n');
                }
                text
            }
            // toml_edit writes LF; keep a CRLF file CRLF.
            Self::Codex { document, crlf } => {
                let text = document.to_string();
                if *crlf {
                    text.replace("\r\n", "\n").replace('\n', "\r\n")
                } else {
                    text
                }
            }
        }
    }
}

pub struct Connection<'a> {
    pub token_id: &'a str,
    pub token: &'a str,
    pub origin: &'a str,
    pub models: &'a [(&'static str, String)],
}

fn desired_keys(client: Client, connection: &Connection) -> Vec<(String, String)> {
    let base_url = client.base_url(connection.origin);
    let model = |field: &str| {
        connection
            .models
            .iter()
            .find(|(key, _)| *key == field)
            .map(|(_, value)| value.clone())
    };
    let mut keys = Vec::new();
    match client {
        Client::Codex => {
            keys.push(("model_provider".into(), CODEX_PROVIDER.into()));
            if let Some(model) = model("model") {
                keys.push(("model".into(), model));
            }
            // The display name is local UI; a reserved ID or the name
            // "OpenAI" would make Codex treat this as its built-in provider.
            for (key, value) in [
                ("name", "AstrLink"),
                ("base_url", base_url.as_str()),
                ("wire_api", "responses"),
                ("experimental_bearer_token", connection.token),
            ] {
                keys.push((format!("{CODEX_PROVIDER_PREFIX}{key}"), value.into()));
            }
        }
        _ => {
            keys.push(("env.ANTHROPIC_BASE_URL".into(), base_url));
            // AUTH_TOKEN, not API_KEY: an API key asks for approval in the
            // interactive client and conflicts with a login.
            keys.push(("env.ANTHROPIC_AUTH_TOKEN".into(), connection.token.into()));
            for (field, key) in [
                ("model", "ANTHROPIC_MODEL"),
                ("haikuModel", "ANTHROPIC_DEFAULT_HAIKU_MODEL"),
                ("sonnetModel", "ANTHROPIC_DEFAULT_SONNET_MODEL"),
                ("opusModel", "ANTHROPIC_DEFAULT_OPUS_MODEL"),
                ("fableModel", "ANTHROPIC_DEFAULT_FABLE_MODEL"),
            ] {
                if let Some(model) = model(field) {
                    keys.push((format!("env.{key}"), model));
                }
            }
        }
    }
    keys
}

fn written_by_us(record: Option<&ClientRecord>, path: &str, value: Option<&str>) -> bool {
    match (record.and_then(|record| record.keys.get(path)), value) {
        (Some(fingerprint), Some(value)) => *fingerprint == sha256_hex(value),
        _ => false,
    }
}

#[derive(Debug)]
pub struct WritePlan {
    pub contents: String,
    /// Keys holding values AstrLink did not write, which the plan replaces
    /// or removes. Names only; values never leave the file.
    pub conflicts: Vec<String>,
    pub record: ClientRecord,
}

pub fn plan_write(
    client: Client,
    existing: Option<&str>,
    connection: &Connection,
    previous: Option<&ClientRecord>,
) -> Result<WritePlan, String> {
    let mut document = Document::parse(client, existing)?;
    let desired = desired_keys(client, connection);
    let mut conflicts = Vec::new();
    for (path, value) in document.group() {
        let wanted = desired.iter().find(|(key, _)| *key == path);
        if wanted.is_some_and(|(_, wanted)| Some(wanted) == value.as_ref()) {
            continue;
        }
        if !written_by_us(previous, &path, value.as_deref()) {
            conflicts.push(path.clone());
        }
        if wanted.is_none() {
            document.remove(&path);
        }
    }
    for (path, value) in &desired {
        document.set(path, value)?;
    }
    Ok(WritePlan {
        contents: document.render(),
        conflicts,
        record: ClientRecord {
            token_id: connection.token_id.to_string(),
            token_sha256: sha256_hex(connection.token),
            base_url: client.base_url(connection.origin),
            keys: desired
                .iter()
                .map(|(path, value)| (path.clone(), sha256_hex(value)))
                .collect(),
            created_file: existing.is_none() || previous.is_some_and(|record| record.created_file),
            written_at_unix: unix_now(),
        },
    })
}

#[derive(Debug, PartialEq, Eq)]
pub enum Removal {
    Unchanged,
    Write(String),
    Delete,
}

pub fn plan_remove(
    client: Client,
    existing: Option<&str>,
    record: &ClientRecord,
) -> Result<Removal, String> {
    let Some(existing) = existing else {
        return Ok(Removal::Unchanged);
    };
    let mut document = Document::parse(client, Some(existing))?;
    let mut removed = false;
    for path in record.keys.keys() {
        let value = document.get(path).flatten();
        if written_by_us(Some(record), path, value.as_deref()) {
            document.remove(path);
            removed = true;
        }
    }
    if !removed {
        return Ok(Removal::Unchanged);
    }
    document.tidy();
    if record.created_file && document.is_empty() {
        return Ok(Removal::Delete);
    }
    Ok(Removal::Write(document.render()))
}

pub fn inspect(
    client: Client,
    existing: Option<&str>,
    record: Option<&ClientRecord>,
    origin: Option<&str>,
) -> ClientState {
    let Ok(document) = Document::parse(client, existing) else {
        return ClientState::Invalid;
    };
    let Some(record) = record else {
        return ClientState::NotConfigured;
    };
    if existing.is_none()
        || client.connection_keys().iter().any(|path| {
            let value = document.get(path).flatten();
            !written_by_us(Some(record), path, value.as_deref())
        })
    {
        return ClientState::Modified;
    }
    match origin {
        Some(origin) if record.base_url != client.base_url(origin) => ClientState::Outdated,
        _ => ClientState::Configured,
    }
}

/// Points an untouched config at the gateway's new port. The token stays
/// as written, so the sync never needs to reveal it.
pub fn plan_sync(
    client: Client,
    existing: Option<&str>,
    record: &ClientRecord,
    origin: &str,
) -> Result<Option<(String, ClientRecord)>, String> {
    if inspect(client, existing, Some(record), Some(origin)) != ClientState::Outdated {
        return Ok(None);
    }
    let mut document = Document::parse(client, existing)?;
    let base_url = client.base_url(origin);
    document.set(client.base_url_key(), &base_url)?;
    let mut next = record.clone();
    next.keys
        .insert(client.base_url_key().into(), sha256_hex(&base_url));
    next.base_url = base_url;
    next.written_at_unix = unix_now();
    Ok(Some((document.render(), next)))
}

/// The keys `apply` writes, as a standalone file for manual setup.
pub fn snippet(client: Client, connection: &Connection) -> Result<String, String> {
    Ok(plan_write(client.writable()?, None, connection, None)?.contents)
}

fn file_error(path: &Path, error: String) -> String {
    format!("{} {error}", path.display())
}

pub fn status(home: &Path, inference_url: Option<&str>) -> Result<Vec<ClientStatus>, String> {
    let origin = inference_url.map(local_origin).transpose()?;
    let _lock = host_files::lock();
    let records = read_records(home)?;
    let mut statuses = Vec::new();
    for client in Client::WRITABLE {
        let path = client.config_path(home);
        let existing = host_files::read_optional(&path)?;
        let record = records.get(client);
        let state = inspect(client, existing.as_deref(), record, origin.as_deref());
        statuses.push(ClientStatus {
            client,
            detected: client.detected(home),
            paths: vec![path.display().to_string()],
            state,
            token_id: record
                .filter(|_| state != ClientState::NotConfigured)
                .map(|record| record.token_id.clone()),
        });
    }
    Ok(statuses)
}

pub async fn apply(
    manager: &CoreManager,
    home: PathBuf,
    token_id: String,
    client: Client,
    models: &Models,
    inference_url: String,
    replace: bool,
) -> Result<ApplyOutcome, String> {
    let client = client.writable()?;
    let origin = local_origin(&inference_url)?;
    let models = models.fields(client)?;
    if !client.detected(&home) {
        return Err("this client is not installed for the current user".into());
    }
    let token = reveal_access_token(manager, &token_id, &inference_url).await?;
    tauri::async_runtime::spawn_blocking(move || {
        write(
            &home,
            client,
            &Connection {
                token_id: &token_id,
                token: &token,
                origin: &origin,
                models: &models,
            },
            replace,
        )
    })
    .await
    .map_err(|error| error.to_string())?
}

pub fn write(
    home: &Path,
    client: Client,
    connection: &Connection,
    replace: bool,
) -> Result<ApplyOutcome, String> {
    let client = client.writable()?;
    let _lock = host_files::lock();
    let mut records = read_records(home)?;
    let path = client.config_path(home);
    let existing = host_files::read_optional(&path)?;
    let plan = plan_write(client, existing.as_deref(), connection, records.get(client))
        .map_err(|error| file_error(&path, error))?;
    if !plan.conflicts.is_empty() && !replace {
        return Ok(ApplyOutcome::NeedsConfirmation {
            keys: plan.conflicts,
        });
    }
    let previous = records.slot(client).replace(plan.record);
    write_records(home, &mut records)?;
    let written = host_files::write_text(
        &path,
        &plan.contents,
        WriteOptions {
            secret: true,
            expected: Some(existing.as_deref()),
        },
    );
    if let Err(error) = written {
        *records.slot(client) = previous;
        let _ = write_records(home, &mut records);
        return Err(error);
    }
    Ok(ApplyOutcome::Applied)
}

pub fn remove(home: &Path, client: Client) -> Result<(), String> {
    let client = client.writable()?;
    let _lock = host_files::lock();
    let mut records = read_records(home)?;
    let Some(record) = records.get(client).cloned() else {
        return Ok(());
    };
    let path = client.config_path(home);
    let existing = host_files::read_optional(&path)?;
    match plan_remove(client, existing.as_deref(), &record)
        .map_err(|error| file_error(&path, error))?
    {
        Removal::Unchanged => {}
        Removal::Write(contents) => host_files::write_text(
            &path,
            &contents,
            WriteOptions {
                expected: Some(existing.as_deref()),
                ..Default::default()
            },
        )?,
        Removal::Delete => {
            if host_files::read_optional(&path)? != existing {
                return Err(format!(
                    "unable to remove {}: the file changed; try again",
                    path.display()
                ));
            }
            host_files::remove_path(&path)?;
        }
    }
    *records.slot(client) = None;
    write_records(home, &mut records)
}

/// Moves every untouched config to the gateway's current address.
pub fn sync(home: &Path, inference_url: &str) -> Result<(), String> {
    let origin = local_origin(inference_url)?;
    let _lock = host_files::lock();
    let mut records = read_records(home)?;
    let mut errors = Vec::new();
    for client in Client::WRITABLE {
        let Some(record) = records.get(client).cloned() else {
            continue;
        };
        let path = client.config_path(home);
        let result = (|| {
            let existing = host_files::read_optional(&path)?;
            let Some((contents, next)) = plan_sync(client, existing.as_deref(), &record, &origin)
                .map_err(|error| file_error(&path, error))?
            else {
                return Ok(());
            };
            *records.slot(client) = Some(next);
            write_records(home, &mut records)?;
            let written = host_files::write_text(
                &path,
                &contents,
                WriteOptions {
                    secret: true,
                    expected: Some(existing.as_deref()),
                },
            );
            if written.is_err() {
                *records.slot(client) = Some(record.clone());
                let _ = write_records(home, &mut records);
            }
            written
        })();
        if let Err(error) = result {
            errors.push(error);
        }
    }
    if errors.is_empty() {
        Ok(())
    } else {
        Err(errors.join("; "))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::{
        fs,
        sync::atomic::{AtomicU64, Ordering},
    };

    const TOKEN: &str = "astr_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA0";
    const OTHER_TOKEN: &str = "astr_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB4";
    const ORIGIN: &str = "http://127.0.0.1:18317";

    fn unique_home(name: &str) -> PathBuf {
        static SEQUENCE: AtomicU64 = AtomicU64::new(0);
        let path = std::env::temp_dir().join(format!(
            "astrlink-client-config-{name}-{}-{}",
            std::process::id(),
            SEQUENCE.fetch_add(1, Ordering::Relaxed)
        ));
        let _ = fs::remove_dir_all(&path);
        fs::create_dir_all(path.join(".claude")).unwrap();
        fs::create_dir_all(path.join(".codex")).unwrap();
        path
    }

    fn connection<'a>(models: &'a [(&'static str, String)]) -> Connection<'a> {
        Connection {
            token_id: "token_01",
            token: TOKEN,
            origin: ORIGIN,
            models,
        }
    }

    fn codex_models() -> Vec<(&'static str, String)> {
        vec![("model", "gpt-route".to_string())]
    }

    fn json(text: &str) -> serde_json::Value {
        serde_json::from_str(text).unwrap()
    }

    #[test]
    fn claude_settings_keep_other_keys_order_and_indent() {
        let existing = "{\n    \"theme\": \"dark\",\n    \"env\": {\n        \"EDITOR\": \"vim\"\n    },\n    \"permissions\": {\"allow\": []}\n}\n";
        let models = vec![
            ("model", "main-route".to_string()),
            ("fableModel", "fable-route".to_string()),
        ];
        let plan = plan_write(Client::Claude, Some(existing), &connection(&models), None).unwrap();
        assert!(plan.conflicts.is_empty());
        assert_eq!(
            plan.contents,
            "{\n    \"theme\": \"dark\",\n    \"env\": {\n        \"EDITOR\": \"vim\",\n        \"ANTHROPIC_BASE_URL\": \"http://127.0.0.1:18317\",\n        \"ANTHROPIC_AUTH_TOKEN\": \"astr_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA0\",\n        \"ANTHROPIC_MODEL\": \"main-route\",\n        \"ANTHROPIC_DEFAULT_FABLE_MODEL\": \"fable-route\"\n    },\n    \"permissions\": {\"allow\": []}\n}\n"
        );
        assert!(!plan.record.created_file);
        assert_eq!(plan.record.base_url, ORIGIN);
        assert_eq!(
            plan.record.keys.keys().collect::<Vec<_>>(),
            [
                "env.ANTHROPIC_AUTH_TOKEN",
                "env.ANTHROPIC_BASE_URL",
                "env.ANTHROPIC_DEFAULT_FABLE_MODEL",
                "env.ANTHROPIC_MODEL",
            ]
        );
        let removed = plan_remove(Client::Claude, Some(&plan.contents), &plan.record).unwrap();
        assert_eq!(removed, Removal::Write(existing.into()));
    }

    #[test]
    fn claude_settings_are_created_and_removed_whole() {
        for existing in [None, Some("")] {
            let plan = plan_write(Client::Claude, existing, &connection(&[]), None).unwrap();
            assert_eq!(
                json(&plan.contents),
                serde_json::json!({"env": {"ANTHROPIC_BASE_URL": ORIGIN, "ANTHROPIC_AUTH_TOKEN": TOKEN}})
            );
            assert!(plan.contents.ends_with("}\n"));
            assert_eq!(plan.record.created_file, existing.is_none());
            let removal = plan_remove(Client::Claude, Some(&plan.contents), &plan.record).unwrap();
            if existing.is_none() {
                assert_eq!(removal, Removal::Delete);
            } else {
                assert_eq!(removal, Removal::Write("{}\n".into()));
            }
        }
    }

    #[test]
    fn claude_conflicts_are_named_and_removed_only_when_replacing() {
        let existing = r#"{"apiKeyHelper": "secret-helper", "env": {"ANTHROPIC_API_KEY": "sk-secret", "ANTHROPIC_BASE_URL": "https://other.example", "CLAUDE_CODE_USE_BEDROCK": "1", "ANTHROPIC_DEFAULT_OPUS_MODEL": "old-opus", "HTTP_PROXY": "http://proxy"}}"#;
        let plan = plan_write(Client::Claude, Some(existing), &connection(&[]), None).unwrap();
        assert_eq!(
            plan.conflicts,
            [
                "env.ANTHROPIC_API_KEY",
                "env.ANTHROPIC_BASE_URL",
                "env.CLAUDE_CODE_USE_BEDROCK",
                "env.ANTHROPIC_DEFAULT_OPUS_MODEL",
                "apiKeyHelper",
            ]
        );
        let written = json(&plan.contents);
        assert_eq!(
            written,
            serde_json::json!({"env": {"ANTHROPIC_BASE_URL": ORIGIN, "HTTP_PROXY": "http://proxy", "ANTHROPIC_AUTH_TOKEN": TOKEN}})
        );
        let debug = format!("{plan:?}");
        for secret in ["sk-secret", "secret-helper", "other.example", TOKEN] {
            assert!(!plan.conflicts.join(",").contains(secret));
            assert!(!serde_json::to_string(&plan.record)
                .unwrap()
                .contains(secret));
        }
        assert!(debug.contains("ANTHROPIC_API_KEY"));
    }

    #[test]
    fn rewriting_replaces_our_own_values_without_asking() {
        let models = vec![("opusModel", "opus-route".to_string())];
        let first = plan_write(Client::Claude, None, &connection(&models), None).unwrap();
        let second = plan_write(
            Client::Claude,
            Some(&first.contents),
            &Connection {
                token_id: "token_02",
                token: OTHER_TOKEN,
                origin: "http://127.0.0.1:9000",
                models: &[],
            },
            Some(&first.record),
        )
        .unwrap();
        assert!(second.conflicts.is_empty());
        assert_eq!(
            json(&second.contents),
            serde_json::json!({"env": {"ANTHROPIC_BASE_URL": "http://127.0.0.1:9000", "ANTHROPIC_AUTH_TOKEN": OTHER_TOKEN}})
        );
        assert!(second.record.created_file);
        assert_eq!(second.record.token_id, "token_02");
    }

    #[test]
    fn removal_keeps_keys_changed_or_added_after_writing() {
        let models = vec![("model", "main-route".to_string())];
        let plan = plan_write(Client::Claude, None, &connection(&models), None).unwrap();
        let edited = plan
            .contents
            .replace("main-route", "user-route")
            .replace("\"env\": {", "\"env\": {\n    \"EDITOR\": \"vim\",");
        let Removal::Write(left) =
            plan_remove(Client::Claude, Some(&edited), &plan.record).unwrap()
        else {
            panic!("expected a rewrite");
        };
        assert_eq!(
            json(&left),
            serde_json::json!({"env": {"EDITOR": "vim", "ANTHROPIC_MODEL": "user-route"}})
        );
    }

    #[test]
    fn invalid_claude_settings_are_rejected() {
        for existing in [
            "{broken",
            "{/*c*/}",
            "[]",
            "{\"env\": \"x\"}",
            "{\"a\": 1,}",
        ] {
            let error =
                plan_write(Client::Claude, Some(existing), &connection(&[]), None).unwrap_err();
            assert!(!error.contains(TOKEN));
            assert_eq!(
                inspect(Client::Claude, Some(existing), None, Some(ORIGIN)),
                ClientState::Invalid
            );
        }
    }

    #[test]
    fn codex_config_keeps_comments_and_other_tables() {
        let existing = "# user settings\nmodel = \"gpt-5\" # mine\napproval_policy = \"never\"\n\n[profiles.fast]\nmodel = \"o3\"\n\n[model_providers.other]\nname = \"Other\"\n";
        let plan = plan_write(
            Client::Codex,
            Some(existing),
            &connection(&codex_models()),
            None,
        )
        .unwrap();
        assert_eq!(plan.conflicts, ["model"]);
        assert_eq!(
            plan.contents,
            "# user settings\nmodel = \"gpt-route\" # mine\napproval_policy = \"never\"\nmodel_provider = \"astrlink\"\n\n[profiles.fast]\nmodel = \"o3\"\n\n[model_providers.other]\nname = \"Other\"\n\n[model_providers.astrlink]\nname = \"AstrLink\"\nbase_url = \"http://127.0.0.1:18317/v1\"\nwire_api = \"responses\"\nexperimental_bearer_token = \"astr_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA0\"\n"
        );
        assert_eq!(plan.record.base_url, "http://127.0.0.1:18317/v1");
        let Removal::Write(removed) =
            plan_remove(Client::Codex, Some(&plan.contents), &plan.record).unwrap()
        else {
            panic!("expected a rewrite");
        };
        assert_eq!(
            removed,
            "# user settings\napproval_policy = \"never\"\n\n[profiles.fast]\nmodel = \"o3\"\n\n[model_providers.other]\nname = \"Other\"\n"
        );
    }

    #[test]
    fn codex_config_is_created_without_auth_settings() {
        let plan = plan_write(Client::Codex, None, &connection(&codex_models()), None).unwrap();
        assert_eq!(
            plan.contents,
            "model_provider = \"astrlink\"\nmodel = \"gpt-route\"\n\n[model_providers.astrlink]\nname = \"AstrLink\"\nbase_url = \"http://127.0.0.1:18317/v1\"\nwire_api = \"responses\"\nexperimental_bearer_token = \"astr_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA0\"\n"
        );
        for forbidden in ["env_key", "requires_openai_auth", "\"OpenAI\"", "openai"] {
            assert!(!plan.contents.contains(forbidden));
        }
        assert_eq!(
            plan_remove(Client::Codex, Some(&plan.contents), &plan.record).unwrap(),
            Removal::Delete
        );
    }

    #[test]
    fn codex_conflicts_cover_a_foreign_provider_table() {
        let existing = "model_provider = \"other\"\n\n[model_providers.astrlink]\nbase_url = \"https://secret.example/v1\"\nenv_key = \"SECRET_KEY\"\n";
        let plan = plan_write(
            Client::Codex,
            Some(existing),
            &connection(&codex_models()),
            None,
        )
        .unwrap();
        assert_eq!(
            plan.conflicts,
            [
                "model_provider",
                "model_providers.astrlink.base_url",
                "model_providers.astrlink.env_key",
            ]
        );
        assert!(!plan.contents.contains("env_key"));
        assert!(!plan.contents.contains("secret.example"));
        let plan = plan_write(
            Client::Codex,
            Some("model_providers = { astrlink = \"broken\" }\n"),
            &connection(&codex_models()),
            None,
        )
        .unwrap();
        assert_eq!(plan.conflicts, ["model_providers.astrlink"]);
        assert!(plan.contents.contains("experimental_bearer_token"));
    }

    #[test]
    fn codex_crlf_files_stay_crlf() {
        let existing = "# a\r\napproval_policy = \"never\"\r\n";
        let plan = plan_write(
            Client::Codex,
            Some(existing),
            &connection(&codex_models()),
            None,
        )
        .unwrap();
        assert!(plan
            .contents
            .starts_with("# a\r\napproval_policy = \"never\"\r\n"));
        assert!(!plan.contents.replace("\r\n", "").contains('\n'));
        let Removal::Write(removed) =
            plan_remove(Client::Codex, Some(&plan.contents), &plan.record).unwrap()
        else {
            panic!("expected a rewrite");
        };
        assert_eq!(removed, existing);
    }

    #[test]
    fn a_model_the_client_changed_does_not_block_sync() {
        let plan = plan_write(Client::Codex, None, &connection(&codex_models()), None).unwrap();
        let edited = plan.contents.replace("gpt-route", "picked-in-tui");
        assert_eq!(
            inspect(
                Client::Codex,
                Some(&edited),
                Some(&plan.record),
                Some(ORIGIN)
            ),
            ClientState::Configured
        );
        let (synced, record) = plan_sync(
            Client::Codex,
            Some(&edited),
            &plan.record,
            "http://127.0.0.1:9000",
        )
        .unwrap()
        .unwrap();
        assert_eq!(synced, edited.replace("127.0.0.1:18317", "127.0.0.1:9000"));
        assert_eq!(record.base_url, "http://127.0.0.1:9000/v1");
        assert_eq!(record.token_sha256, plan.record.token_sha256);
        assert_eq!(
            inspect(
                Client::Codex,
                Some(&synced),
                Some(&record),
                Some("http://127.0.0.1:9000")
            ),
            ClientState::Configured
        );
        // The model the user picked is theirs now and survives removal.
        let Removal::Write(removed) = plan_remove(Client::Codex, Some(&synced), &record).unwrap()
        else {
            panic!("expected a rewrite");
        };
        assert_eq!(removed, "model = \"picked-in-tui\"\n");
    }

    #[test]
    fn a_changed_connection_is_modified_and_never_synced() {
        let plan = plan_write(Client::Claude, None, &connection(&[]), None).unwrap();
        assert_eq!(
            inspect(
                Client::Claude,
                Some(&plan.contents),
                Some(&plan.record),
                Some(ORIGIN)
            ),
            ClientState::Configured
        );
        assert_eq!(
            inspect(
                Client::Claude,
                Some(&plan.contents),
                Some(&plan.record),
                Some("http://127.0.0.1:9000")
            ),
            ClientState::Outdated
        );
        let edited = plan.contents.replace(TOKEN, OTHER_TOKEN);
        assert_eq!(
            inspect(
                Client::Claude,
                Some(&edited),
                Some(&plan.record),
                Some("http://127.0.0.1:9000")
            ),
            ClientState::Modified
        );
        assert_eq!(
            plan_sync(
                Client::Claude,
                Some(&edited),
                &plan.record,
                "http://127.0.0.1:9000"
            )
            .unwrap(),
            None
        );
        assert_eq!(
            inspect(Client::Claude, None, Some(&plan.record), Some(ORIGIN)),
            ClientState::Modified
        );
        assert_eq!(
            inspect(Client::Claude, Some("{}"), None, Some(ORIGIN)),
            ClientState::NotConfigured
        );
    }

    #[test]
    fn a_synced_claude_config_changes_only_the_base_url() {
        let models = vec![("model", "main-route".to_string())];
        let plan = plan_write(Client::Claude, None, &connection(&models), None).unwrap();
        let (synced, record) = plan_sync(
            Client::Claude,
            Some(&plan.contents),
            &plan.record,
            "http://127.0.0.1:9000",
        )
        .unwrap()
        .unwrap();
        assert_eq!(
            synced,
            plan.contents
                .replace("http://127.0.0.1:18317", "http://127.0.0.1:9000")
        );
        assert_eq!(record.keys["env.ANTHROPIC_AUTH_TOKEN"], sha256_hex(TOKEN));
        assert!(plan_sync(
            Client::Claude,
            Some(&synced),
            &record,
            "http://127.0.0.1:9000"
        )
        .unwrap()
        .is_none());
    }

    #[test]
    fn only_claude_and_codex_are_written_directly() {
        let home = unique_home("unsupported");
        for client in [Client::Gemini, Client::Opencode, Client::Openclaw] {
            let error = write(&home, client, &connection(&codex_models()), true).unwrap_err();
            assert!(!error.contains(TOKEN));
            assert!(remove(&home, client).is_err());
            assert!(snippet(client, &connection(&codex_models())).is_err());
        }
        let clients = status(&home, Some("http://127.0.0.1:18317/"))
            .unwrap()
            .into_iter()
            .map(|status| status.client)
            .collect::<Vec<_>>();
        assert_eq!(clients, [Client::Claude, Client::Codex]);
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn apply_asks_before_replacing_and_writes_the_record_first() {
        let home = unique_home("apply");
        let settings = home.join(".claude/settings.json");
        let foreign = "{\n  \"env\": {\n    \"ANTHROPIC_API_KEY\": \"sk-secret\"\n  }\n}\n";
        fs::write(&settings, foreign).unwrap();
        let outcome = write(&home, Client::Claude, &connection(&[]), false).unwrap();
        assert_eq!(
            outcome,
            ApplyOutcome::NeedsConfirmation {
                keys: vec!["env.ANTHROPIC_API_KEY".into()]
            }
        );
        assert_eq!(fs::read_to_string(&settings).unwrap(), foreign);
        assert!(!records_path(&home).exists());

        let outcome = write(&home, Client::Claude, &connection(&[]), true).unwrap();
        assert_eq!(outcome, ApplyOutcome::Applied);
        let written = fs::read_to_string(&settings).unwrap();
        assert!(!written.contains("sk-secret"));
        let records = fs::read_to_string(records_path(&home)).unwrap();
        assert!(!records.contains(TOKEN));
        assert!(!records.contains("sk-secret"));
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            for path in [settings.clone(), records_path(&home)] {
                let mode = fs::metadata(path).unwrap().permissions().mode() & 0o777;
                assert_eq!(mode, 0o600);
            }
        }
        let statuses = status(&home, Some("http://127.0.0.1:18317/")).unwrap();
        assert_eq!(statuses[0].state, ClientState::Configured);
        assert_eq!(statuses[0].token_id.as_deref(), Some("token_01"));
        assert_eq!(statuses[1].state, ClientState::NotConfigured);

        remove(&home, Client::Claude).unwrap();
        assert_eq!(fs::read_to_string(&settings).unwrap(), "{}\n");
        assert!(!records_path(&home).exists());
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn codex_apply_never_touches_auth_json() {
        let home = unique_home("codex-auth");
        let auth = home.join(".codex/auth.json");
        let login = "{\"auth_mode\": \"chatgpt\", \"tokens\": {}}\n";
        fs::write(&auth, login).unwrap();
        write(&home, Client::Codex, &connection(&codex_models()), false).unwrap();
        sync(&home, "http://127.0.0.1:9000/").unwrap();
        let config = fs::read_to_string(home.join(".codex/config.toml")).unwrap();
        assert!(config.contains("base_url = \"http://127.0.0.1:9000/v1\""));
        remove(&home, Client::Codex).unwrap();
        assert!(!home.join(".codex/config.toml").exists());
        assert_eq!(fs::read_to_string(&auth).unwrap(), login);
        let entries = fs::read_dir(home.join(".codex"))
            .unwrap()
            .map(|entry| entry.unwrap().file_name())
            .collect::<Vec<_>>();
        assert_eq!(entries, ["auth.json"]);
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn sync_leaves_modified_and_unreadable_configs_alone() {
        let home = unique_home("sync");
        write(&home, Client::Claude, &connection(&[]), false).unwrap();
        write(&home, Client::Codex, &connection(&codex_models()), false).unwrap();
        let settings = home.join(".claude/settings.json");
        let edited = fs::read_to_string(&settings)
            .unwrap()
            .replace("127.0.0.1:18317", "proxy.example");
        fs::write(&settings, &edited).unwrap();
        let broken = "token = \"astr_x\n";
        fs::write(home.join(".codex/config.toml"), broken).unwrap();
        sync(&home, "http://127.0.0.1:9000/").unwrap();
        assert_eq!(fs::read_to_string(&settings).unwrap(), edited);
        assert_eq!(
            fs::read_to_string(home.join(".codex/config.toml")).unwrap(),
            broken
        );
        let statuses = status(&home, Some("http://127.0.0.1:9000/")).unwrap();
        assert_eq!(statuses[0].state, ClientState::Modified);
        assert_eq!(statuses[1].state, ClientState::Invalid);
        let error = remove(&home, Client::Codex).unwrap_err();
        assert!(error.contains("config.toml") && error.contains("TOML"));
        assert!(!error.contains("astr_x"));
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn comments_above_removed_codex_keys_stay() {
        let models = codex_models();
        for (existing, expected) in [
            ("# header\nmodel = \"old\"\n", "# header\n"),
            (
                "approval_policy = \"never\"\n# pick one\nmodel = \"old\"\n\n[tui]\nx = 1\n",
                "approval_policy = \"never\"\n# pick one\n\n[tui]\nx = 1\n",
            ),
        ] {
            let plan =
                plan_write(Client::Codex, Some(existing), &connection(&models), None).unwrap();
            let Removal::Write(removed) =
                plan_remove(Client::Codex, Some(&plan.contents), &plan.record).unwrap()
            else {
                panic!("expected a rewrite");
            };
            assert_eq!(removed, expected);
        }
    }

    #[cfg(unix)]
    #[test]
    fn a_symlinked_settings_file_is_written_at_its_target() {
        let home = unique_home("symlink");
        let target = home.join("dotfiles-settings.json");
        fs::write(&target, "{}\n").unwrap();
        std::os::unix::fs::symlink(&target, home.join(".claude/settings.json")).unwrap();
        write(&home, Client::Claude, &connection(&[]), false).unwrap();
        assert!(fs::symlink_metadata(home.join(".claude/settings.json"))
            .unwrap()
            .file_type()
            .is_symlink());
        assert!(fs::read_to_string(&target).unwrap().contains(TOKEN));
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn models_are_validated_and_fable_is_claude_only() {
        let models: Models = serde_json::from_value(serde_json::json!({
            "model": " main ", "fableModel": "fable-route"
        }))
        .unwrap();
        assert_eq!(
            models.fields(Client::Claude).unwrap(),
            [
                ("model", "main".to_string()),
                ("fableModel", "fable-route".to_string())
            ]
        );
        assert_eq!(
            models.fields(Client::Codex).unwrap(),
            [("model", "main".to_string())]
        );
        for key in [
            "model",
            "haikuModel",
            "sonnetModel",
            "opusModel",
            "fableModel",
        ] {
            for value in ["x".repeat(257), "invalid\nmodel".into()] {
                let models: Models =
                    serde_json::from_value(serde_json::json!({key: value})).unwrap();
                assert!(models.fields(Client::Claude).is_err());
            }
        }
        assert!(Models::default().fields(Client::Codex).is_err());
        assert!(serde_json::from_value::<Models>(serde_json::json!({"name": "x"})).is_err());
    }

    #[test]
    fn local_origin_accepts_only_the_loopback_gateway_root() {
        assert_eq!(
            local_origin("http://127.0.0.1:18317/").unwrap(),
            "http://127.0.0.1:18317"
        );
        assert_eq!(
            local_origin("http://localhost:18317").unwrap(),
            "http://localhost:18317"
        );
        for url in [
            "https://127.0.0.1:1",
            "http://[::1]:1",
            "http://localhost.example:1",
            "http://localhost:1/v1",
            "http://127.0.0.1:1/v1",
            "http://user@127.0.0.1:1",
            "http://127.0.0.1:1?x",
            "http://127.0.0.1:1#x",
        ] {
            assert!(local_origin(url).is_err());
        }
    }

    #[test]
    fn snippets_match_what_apply_writes() {
        let codex = snippet(Client::Codex, &connection(&codex_models())).unwrap();
        assert_eq!(
            codex,
            plan_write(Client::Codex, None, &connection(&codex_models()), None)
                .unwrap()
                .contents
        );
        let claude = snippet(Client::Claude, &connection(&[])).unwrap();
        assert_eq!(
            json(&claude),
            serde_json::json!({"env": {"ANTHROPIC_BASE_URL": ORIGIN, "ANTHROPIC_AUTH_TOKEN": TOKEN}})
        );
    }
}
