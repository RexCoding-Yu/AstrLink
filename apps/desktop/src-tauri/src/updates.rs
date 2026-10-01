//! Application updates belong to the desktop host, never to the inference gateway.
use std::{
    fs,
    path::PathBuf,
    sync::{Arc, Mutex, MutexGuard},
    time::{Duration, Instant},
};

use base64::{engine::general_purpose::STANDARD, Engine};
use minisign_verify::{PublicKey, Signature};
use semver::Version;
use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Emitter, Manager, State};
use tauri_plugin_updater::{Update, UpdaterExt};
use tokio::sync::{Mutex as AsyncMutex, Notify};

use crate::{i18n, preferences::PreferencesStore, sidecar::CoreManager};

const REPOSITORY: &str = "https://github.com/Calcium-Ion/AstrLink";
const RELEASE_API: &str = "https://api.github.com/repos/Calcium-Ion/AstrLink/releases";
const EVENT: &str = "app-update-status";
const PUBLIC_KEY: &str = env!("TAURI_UPDATER_PUBLIC_KEY");
const INTERVAL: Duration = Duration::from_secs(3 * 60 * 60);
const MAX_PACKAGE_BYTES: u64 = 2 * 1024 * 1024 * 1024;

#[derive(Clone, Copy, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum UpdateChannel {
    #[default]
    Stable,
    Preview,
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(default, deny_unknown_fields)]
pub struct UpdatePreferences {
    pub auto_check: bool,
    pub auto_download: bool,
    pub channel: UpdateChannel,
}
impl Default for UpdatePreferences {
    fn default() -> Self {
        Self {
            auto_check: true,
            auto_download: true,
            channel: UpdateChannel::Stable,
        }
    }
}

#[derive(Clone, Debug, Serialize)]
pub struct ReleaseInfo {
    version: String,
    notes: String,
    published_at: Option<String>,
    url: String,
}

#[derive(Clone, Debug, Serialize)]
pub struct UpdateSnapshot {
    revision: u64,
    current_version: String,
    latest_version: Option<String>,
    platform: String,
    arch: String,
    install_supported: bool,
    configured: bool,
    development: bool,
    preferences: UpdatePreferences,
    phase: String,
    release: Option<ReleaseInfo>,
    downloaded_bytes: u64,
    total_bytes: Option<u64>,
    last_checked_at: Option<String>,
    error_code: Option<String>,
    error_detail: Option<String>,
}

struct Inner {
    snapshot: UpdateSnapshot,
    generation: u64,
    pending: Option<Update>,
    retry_after: Option<Instant>,
    /// The last `(notice, version)` announced, so periodic checks do not repeat it.
    notified: Option<(&'static str, String)>,
}

pub struct UpdateManager {
    inner: Mutex<Inner>,
    operation: AsyncMutex<()>,
    pub lifecycle: Arc<AsyncMutex<()>>,
    cancelled: Notify,
    schedule: Notify,
    cache: PathBuf,
}

#[derive(Clone, Debug, Deserialize)]
struct ReleaseAsset {
    name: String,
    browser_download_url: String,
}
#[derive(Clone, Debug, Deserialize)]
struct GithubRelease {
    tag_name: String,
    draft: bool,
    prerelease: bool,
    body: Option<String>,
    published_at: Option<String>,
    assets: Vec<ReleaseAsset>,
}

fn version(tag: &str) -> Option<Version> {
    Version::parse(tag.strip_prefix('v').unwrap_or(tag)).ok()
}
fn select_release(releases: &[GithubRelease], channel: UpdateChannel) -> Option<&GithubRelease> {
    releases
        .iter()
        .filter(|r| !r.draft)
        .filter_map(|r| {
            let v = version(&r.tag_name)?;
            if channel == UpdateChannel::Stable && (r.prerelease || !v.pre.is_empty()) {
                return None;
            }
            Some((v, r))
        })
        .max_by(|a, b| a.0.cmp_precedence(&b.0))
        .map(|(_, r)| r)
}

/// The update state worth the operator's attention. `available` only counts
/// while it waits on them; with automatic download, `ready` follows shortly.
fn notice(phase: &str, auto_download: bool) -> Option<&'static str> {
    match phase {
        "available" if !auto_download => Some("available"),
        "manual" => Some("manual"),
        "ready" => Some("ready"),
        _ => None,
    }
}

/// The main window shows its own toast; a native notification covers the
/// times it cannot be seen: hidden to the tray, minimized, or behind other apps.
fn notify_unattended(app: &AppHandle, kind: &str, version: &str) {
    let attended = app.get_webview_window("main").is_some_and(|window| {
        window.is_visible().unwrap_or(false)
            && window.is_focused().unwrap_or(false)
            && !window.is_minimized().unwrap_or(false)
    });
    if attended {
        return;
    }
    let locale = app
        .state::<Arc<PreferencesStore>>()
        .snapshot()
        .values
        .locale;
    crate::notify_native(
        app,
        i18n::t(locale, &format!("about.phase.{kind}"), &[]),
        i18n::t(
            locale,
            &format!("about.{kind}Notification"),
            &[("version", version)],
        ),
    );
}

fn github_asset(url: &str, tag: &str) -> bool {
    let Ok(parsed) = reqwest::Url::parse(url) else {
        return false;
    };
    parsed.scheme() == "https"
        && parsed.host_str() == Some("github.com")
        && parsed.username().is_empty()
        && parsed.password().is_none()
        && parsed.port().is_none()
        && parsed
            .path()
            .replace("%2B", "+")
            .replace("%2b", "+")
            .starts_with(&format!("/Calcium-Ion/AstrLink/releases/download/{tag}/"))
}

pub fn verify_package(bytes: &[u8], signature: &str, key: &str) -> Result<(), String> {
    let decode = |encoded: &str| -> Result<String, String> {
        String::from_utf8(STANDARD.decode(encoded.trim()).map_err(|e| e.to_string())?)
            .map_err(|e| e.to_string())
    };
    PublicKey::decode(&decode(key)?)
        .map_err(|e| e.to_string())?
        .verify(
            bytes,
            &Signature::decode(&decode(signature)?).map_err(|e| e.to_string())?,
            true,
        )
        .map_err(|e| e.to_string())
}

async fn fetch_releases(
    client: &reqwest::Client,
    endpoint: &str,
    retry_after: &mut Option<Instant>,
) -> Result<Vec<GithubRelease>, (&'static str, String)> {
    let mut releases = Vec::new();
    // Page through all public releases; GitHub's date ordering is not SemVer ordering.
    for page in 1..=100 {
        let response = client
            .get(endpoint)
            .query(&[("per_page", 100), ("page", page)])
            .header("Accept", "application/vnd.github+json")
            .send()
            .await
            .map_err(|e| ("network", e.to_string()))?;
        if matches!(response.status().as_u16(), 403 | 429) {
            let seconds = response
                .headers()
                .get("retry-after")
                .and_then(|v| v.to_str().ok())
                .and_then(|v| v.parse::<u64>().ok())
                .or_else(|| {
                    response
                        .headers()
                        .get("x-ratelimit-reset")
                        .and_then(|v| v.to_str().ok())
                        .and_then(|v| v.parse::<i64>().ok())
                        .map(|v| (v - chrono::Utc::now().timestamp()).max(1) as u64)
                })
                .unwrap_or(3600);
            *retry_after = Some(Instant::now() + Duration::from_secs(seconds.min(86400)));
            return Err((
                "rate_limit",
                format!("GitHub HTTP {}; retry in {seconds}s", response.status()),
            ));
        }
        let mut batch: Vec<GithubRelease> = response
            .error_for_status()
            .map_err(|e| ("network", e.to_string()))?
            .json()
            .await
            .map_err(|e| ("manifest", e.to_string()))?;
        let done = batch.len() < 100;
        releases.append(&mut batch);
        if done {
            break;
        }
        if page == 100 {
            return Err((
                "manifest",
                "Too many releases to select a version safely".into(),
            ));
        }
    }
    Ok(releases)
}

impl UpdateManager {
    pub fn new(app: &AppHandle) -> Result<Self, String> {
        #[cfg(target_os = "linux")]
        let supported = app.env().appimage.is_some();
        #[cfg(not(target_os = "linux"))]
        let supported = cfg!(any(target_os = "macos", windows));
        Ok(Self {
            inner: Mutex::new(Inner {
                snapshot: UpdateSnapshot {
                    revision: 0,
                    current_version: app.package_info().version.to_string(),
                    latest_version: None,
                    platform: std::env::consts::OS.into(),
                    arch: std::env::consts::ARCH.into(),
                    install_supported: supported,
                    configured: !PUBLIC_KEY.is_empty(),
                    development: cfg!(debug_assertions),
                    preferences: app
                        .state::<Arc<PreferencesStore>>()
                        .snapshot()
                        .values
                        .updates,
                    phase: "idle".into(),
                    release: None,
                    downloaded_bytes: 0,
                    total_bytes: None,
                    last_checked_at: None,
                    error_code: None,
                    error_detail: None,
                },
                generation: 0,
                pending: None,
                retry_after: None,
                notified: None,
            }),
            operation: AsyncMutex::new(()),
            lifecycle: Arc::new(AsyncMutex::new(())),
            cancelled: Notify::new(),
            schedule: Notify::new(),
            cache: app
                .path()
                .app_cache_dir()
                .map_err(|e| e.to_string())?
                .join("updates"),
        })
    }
    fn lock(&self) -> MutexGuard<'_, Inner> {
        self.inner.lock().unwrap_or_else(|e| e.into_inner())
    }
    pub fn snapshot(&self) -> UpdateSnapshot {
        self.lock().snapshot.clone()
    }
    fn publish(&self, app: &AppHandle, change: impl FnOnce(&mut Inner)) {
        let (snapshot, announce) = {
            let mut inner = self.lock();
            change(&mut inner);
            inner.snapshot.revision += 1;
            let current = notice(
                &inner.snapshot.phase,
                inner.snapshot.preferences.auto_download,
            )
            .zip(inner.snapshot.release.as_ref().map(|r| r.version.clone()));
            let announce = current.filter(|n| inner.notified.as_ref() != Some(n));
            if announce.is_some() {
                inner.notified.clone_from(&announce);
            }
            (inner.snapshot.clone(), announce)
        };
        if let Err(error) = app.emit_to("main", EVENT, snapshot) {
            eprintln!("unable to publish update state: {error}");
        }
        if let Some((kind, version)) = announce {
            notify_unattended(app, kind, &version);
        }
    }
    fn fail(&self, app: &AppHandle, generation: u64, code: &str, detail: String) {
        self.publish(app, |i| {
            if i.generation != generation {
                return;
            }
            i.snapshot.phase = "error".into();
            i.snapshot.error_code = Some(code.into());
            i.snapshot.error_detail = Some(detail);
        });
    }
    fn cache_file(&self, update: &Update) -> PathBuf {
        use sha2::{Digest, Sha256};
        let key = format!("{}:{}:{}", update.version, update.target, update.signature);
        self.cache
            .join(format!("{:x}.update", Sha256::digest(key.as_bytes())))
    }
    fn cached(&self, update: &Update) -> Result<Vec<u8>, String> {
        let path = self.cache_file(update);
        if fs::metadata(&path).map_err(|e| e.to_string())?.len() > MAX_PACKAGE_BYTES {
            return Err("Update cache is too large".into());
        }
        let bytes = fs::read(&path).map_err(|e| e.to_string())?;
        verify_package(&bytes, &update.signature, PUBLIC_KEY)?;
        Ok(bytes)
    }
    fn clear_cache(&self) {
        // This directory is exclusively owned by the updater.
        if let Ok(entries) = fs::read_dir(&self.cache) {
            for entry in entries.flatten() {
                if matches!(
                    entry.path().extension().and_then(|e| e.to_str()),
                    Some("update" | "part")
                ) {
                    let _ = fs::remove_file(entry.path());
                }
            }
        }
    }
    pub fn preferences(
        &self,
        app: &AppHandle,
        input: UpdatePreferences,
    ) -> Result<UpdateSnapshot, String> {
        let mut inner = self.lock();
        if inner.snapshot.phase == "installing" {
            return Err("An update is being installed".into());
        }
        app.state::<Arc<PreferencesStore>>()
            .replace_updates(input.clone())?;
        let changed_channel = inner.snapshot.preferences.channel != input.channel;
        inner.snapshot.preferences = input;
        if changed_channel {
            inner.generation += 1;
            inner.pending = None;
            inner.snapshot.release = None;
            inner.snapshot.latest_version = None;
            inner.snapshot.phase = "idle".into();
            inner.snapshot.error_code = None;
            inner.snapshot.error_detail = None;
            inner.snapshot.downloaded_bytes = 0;
            inner.snapshot.total_bytes = None;
            self.cancelled.notify_waiters();
        }
        if changed_channel {
            self.clear_cache();
        }
        drop(inner);
        self.publish(app, |_| {});
        self.schedule.notify_one();
        Ok(self.snapshot())
    }
    pub fn start(self: &Arc<Self>, app: AppHandle) {
        let manager = Arc::clone(self);
        tauri::async_runtime::spawn(async move {
            tokio::time::sleep(Duration::from_secs(30)).await;
            loop {
                if !cfg!(debug_assertions) && manager.snapshot().preferences.auto_check {
                    // Wait for an invalidated task to release its operation before the next check.
                    {
                        let _guard = manager.operation.lock().await;
                    }
                    let _ = manager.check(&app).await;
                }
                tokio::select! { _ = tokio::time::sleep(INTERVAL) => {}, _ = manager.schedule.notified() => {} }
            }
        });
    }
    pub async fn check(&self, app: &AppHandle) -> Result<UpdateSnapshot, String> {
        let _operation = self
            .operation
            .try_lock()
            .map_err(|_| "An update operation is already running")?;
        let cancel = self.cancelled.notified();
        tokio::pin!(cancel);
        cancel.as_mut().enable();
        let generation = self.lock().generation;
        if self
            .lock()
            .retry_after
            .is_some_and(|until| until > Instant::now())
        {
            return Err("GitHub rate limit: retry later".into());
        }
        self.publish(app, |i| {
            i.pending = None;
            i.snapshot.phase = "checking".into();
            i.snapshot.error_code = None;
            i.snapshot.error_detail = None;
        });
        let result = tokio::select! { result = self.check_inner(app, generation) => result, _ = &mut cancel => return Ok(self.snapshot()) };
        if let Err((code, error)) = result {
            self.fail(app, generation, code, error);
        }
        if self.snapshot().phase == "available" && self.snapshot().preferences.auto_download {
            self.download_locked(app, generation).await;
        }
        Ok(self.snapshot())
    }
    async fn check_inner(
        &self,
        app: &AppHandle,
        generation: u64,
    ) -> Result<(), (&'static str, String)> {
        let prefs = app.state::<Arc<PreferencesStore>>().snapshot().values;
        let mut builder = reqwest::Client::builder()
            .user_agent("tauri-updater")
            .timeout(Duration::from_secs(30));
        if !prefs.use_system_proxy {
            builder = builder.no_proxy();
        }
        let client = builder.build().map_err(|e| ("network", e.to_string()))?;
        let mut retry_after = None;
        let releases = match fetch_releases(&client, RELEASE_API, &mut retry_after).await {
            Ok(releases) => releases,
            Err(error) => {
                self.lock().retry_after = retry_after;
                return Err(error);
            }
        };
        self.publish(app, |i| {
            if i.generation == generation {
                i.snapshot.last_checked_at = Some(chrono::Utc::now().to_rfc3339());
            }
        });
        let selected = select_release(&releases, prefs.updates.channel);
        let Some(release) = selected else {
            self.publish(app, |i| {
                if i.generation == generation {
                    i.pending = None;
                    i.snapshot.release = None;
                    i.snapshot.phase = "no_releases".into();
                    i.snapshot.latest_version = None;
                }
            });
            return Ok(());
        };
        let selected_version = version(&release.tag_name).expect("selected version is valid");
        self.publish(app, |i| {
            if i.generation == generation {
                i.snapshot.latest_version = Some(selected_version.to_string());
            }
        });
        if selected_version
            .cmp_precedence(&app.package_info().version)
            .is_le()
        {
            self.publish(app, |i| {
                if i.generation == generation {
                    i.pending = None;
                    i.snapshot.release = None;
                    i.snapshot.phase = "up_to_date".into();
                }
            });
            return Ok(());
        }
        let info = ReleaseInfo {
            version: selected_version.to_string(),
            notes: release.body.clone().unwrap_or_default(),
            published_at: release.published_at.clone(),
            url: format!("{REPOSITORY}/releases/tag/{}", release.tag_name),
        };
        self.publish(app, |i| {
            if i.generation == generation {
                i.snapshot.release = Some(info);
            }
        });
        let snapshot = self.snapshot();
        if snapshot.development || !snapshot.install_supported || !snapshot.configured {
            self.publish(app, |i| {
                if i.generation == generation {
                    i.snapshot.phase = "manual".into();
                }
            });
            return Ok(());
        }
        let manifest = release
            .assets
            .iter()
            .find(|a| {
                a.name == "latest.json" && github_asset(&a.browser_download_url, &release.tag_name)
            })
            .ok_or((
                "missing_artifact",
                "Release has no latest.json update manifest".into(),
            ))?;
        let mut updater = app
            .updater_builder()
            .pubkey(PUBLIC_KEY)
            .timeout(Duration::from_secs(30))
            .endpoints(vec![manifest
                .browser_download_url
                .parse::<reqwest::Url>()
                .map_err(|e| ("manifest", e.to_string()))?])
            .map_err(|e| ("manifest", e.to_string()))?;
        if !prefs.use_system_proxy {
            updater = updater.no_proxy();
        }
        let mut update = updater
            .build()
            .map_err(|e| ("manifest", e.to_string()))?
            .check()
            .await
            .map_err(|e| ("manifest", e.to_string()))?
            .ok_or((
                "manifest",
                "Release and update manifest versions disagree".into(),
            ))?;
        if version(&update.version) != Some(selected_version)
            || !github_asset(update.download_url.as_str(), &release.tag_name)
        {
            return Err((
                "manifest",
                "Update manifest does not match the selected GitHub release".into(),
            ));
        }
        update.timeout = Some(Duration::from_secs(30 * 60));
        let cached = self.cached(&update).ok();
        if cached.is_none() {
            let _ = fs::remove_file(self.cache_file(&update));
        }
        self.publish(app, |i| {
            if i.generation != generation {
                return;
            }
            i.snapshot.phase = if cached.is_some() {
                "ready"
            } else {
                "available"
            }
            .into();
            i.snapshot.downloaded_bytes = cached.as_ref().map_or(0, |b| b.len() as u64);
            i.snapshot.total_bytes = cached.as_ref().map(|b| b.len() as u64);
            i.pending = Some(update);
        });
        Ok(())
    }
    pub async fn download(&self, app: &AppHandle) -> Result<UpdateSnapshot, String> {
        let _operation = self
            .operation
            .try_lock()
            .map_err(|_| "An update operation is already running")?;
        let generation = self.lock().generation;
        self.download_locked(app, generation).await;
        Ok(self.snapshot())
    }
    async fn download_locked(&self, app: &AppHandle, generation: u64) {
        let cancel = self.cancelled.notified();
        tokio::pin!(cancel);
        cancel.as_mut().enable();
        let Some(mut update) = self.lock().pending.clone() else {
            return;
        };
        update.no_proxy = !app
            .state::<Arc<PreferencesStore>>()
            .snapshot()
            .values
            .use_system_proxy;
        if self.snapshot().phase == "ready" {
            return;
        }
        self.publish(app, |i| {
            if i.generation == generation {
                i.snapshot.phase = "downloading".into();
                i.snapshot.downloaded_bytes = 0;
                i.snapshot.total_bytes = None;
                i.snapshot.error_code = None;
                i.snapshot.error_detail = None;
            }
        });
        let mut bytes_downloaded = 0u64;
        let mut last_event = Instant::now();
        let task = update.download(
            |bytes, total| {
                bytes_downloaded += bytes as u64;
                if last_event.elapsed() < Duration::from_millis(150) {
                    return;
                }
                last_event = Instant::now();
                self.publish(app, |i| {
                    if i.generation == generation {
                        i.snapshot.downloaded_bytes = bytes_downloaded;
                        i.snapshot.total_bytes = total;
                    }
                });
            },
            || {},
        );
        let result = tokio::select! { result = task => result.map_err(|e| e.to_string()), _ = &mut cancel => return };
        match result {
            Ok(bytes) => {
                let result = (|| -> Result<(), String> {
                    if bytes.len() as u64 > MAX_PACKAGE_BYTES {
                        return Err("Update package is too large".into());
                    }
                    verify_package(&bytes, &update.signature, PUBLIC_KEY)?;
                    // Serialize cache commit with channel changes, so cancelled downloads never reappear.
                    let inner = self.lock();
                    if inner.generation != generation {
                        return Ok(());
                    }
                    fs::create_dir_all(&self.cache).map_err(|e| e.to_string())?;
                    self.clear_cache();
                    let path = self.cache_file(&update);
                    fs::write(path.with_extension("part"), &bytes).map_err(|e| e.to_string())?;
                    fs::rename(path.with_extension("part"), path).map_err(|e| e.to_string())?;
                    Ok(())
                })();
                match result {
                    Ok(()) => self.publish(app, |i| {
                        if i.generation == generation {
                            i.snapshot.phase = "ready".into();
                            i.snapshot.downloaded_bytes = bytes.len() as u64;
                            i.snapshot.total_bytes = Some(bytes.len() as u64);
                        }
                    }),
                    Err(error) => self.fail(app, generation, "verification", error),
                }
            }
            Err(error) => self.fail(app, generation, "download", error),
        }
    }
    pub async fn install(&self, app: &AppHandle) -> Result<UpdateSnapshot, String> {
        let _operation = self
            .operation
            .try_lock()
            .map_err(|_| "An update operation is already running")?;
        let _lifecycle = self
            .lifecycle
            .try_lock()
            .map_err(|_| "The gateway is changing state; retry shortly")?;
        let (generation, update) = {
            let mut inner = self.lock();
            if inner.snapshot.development || inner.snapshot.phase != "ready" {
                return Err("No verified update is ready to install".into());
            }
            let update = inner.pending.clone().ok_or("No pending update")?;
            inner.snapshot.phase = "installing".into();
            (inner.generation, update)
        };
        self.publish(app, |_| {});
        let bytes = match self.cached(&update) {
            Ok(bytes) => bytes,
            Err(error) => {
                let _ = fs::remove_file(self.cache_file(&update));
                self.fail(app, generation, "verification", error);
                return Ok(self.snapshot());
            }
        };
        let core = Arc::clone(app.state::<Arc<CoreManager>>().inner());
        let (guard, was_running) = match core.begin_update() {
            Ok(value) => value,
            Err(error) => {
                self.fail(app, generation, "shutdown", error);
                return Ok(self.snapshot());
            }
        };
        if let Err(error) = core.stop_and_wait().await {
            drop(guard);
            self.fail(app, generation, "shutdown", error);
            return Ok(self.snapshot());
        }
        let result = tauri::async_runtime::spawn_blocking(move || {
            update.install(bytes).map_err(|e| e.to_string())
        })
        .await;
        match result {
            Ok(Ok(())) => {
                self.clear_cache();
                app.restart();
            }
            error => {
                drop(guard);
                let mut detail = match error {
                    Ok(Err(e)) => e,
                    Err(e) => e.to_string(),
                    _ => unreachable!(),
                };
                if was_running {
                    if let Err(e) = core.start(app) {
                        detail.push_str(&format!("; gateway recovery failed: {e}"));
                    }
                }
                self.fail(app, generation, "install", detail);
                Ok(self.snapshot())
            }
        }
    }
}

pub fn lifecycle_guard(
    app: &AppHandle,
) -> Result<Option<tokio::sync::OwnedMutexGuard<()>>, String> {
    app.try_state::<Arc<UpdateManager>>()
        .map(|manager| {
            Arc::clone(&manager.lifecycle)
                .try_lock_owned()
                .map_err(|_| "Application update or gateway operation in progress".into())
        })
        .transpose()
}

#[tauri::command]
pub fn app_update_status(manager: State<'_, Arc<UpdateManager>>) -> UpdateSnapshot {
    manager.snapshot()
}
#[tauri::command]
pub fn update_update_preferences(
    app: AppHandle,
    manager: State<'_, Arc<UpdateManager>>,
    input: UpdatePreferences,
) -> Result<UpdateSnapshot, String> {
    manager.preferences(&app, input)
}
#[tauri::command]
pub async fn check_app_update(
    app: AppHandle,
    manager: State<'_, Arc<UpdateManager>>,
) -> Result<UpdateSnapshot, String> {
    manager.check(&app).await
}
#[tauri::command]
pub async fn download_app_update(
    app: AppHandle,
    manager: State<'_, Arc<UpdateManager>>,
) -> Result<UpdateSnapshot, String> {
    manager.download(&app).await
}
#[tauri::command]
pub async fn install_app_update(
    app: AppHandle,
    manager: State<'_, Arc<UpdateManager>>,
) -> Result<UpdateSnapshot, String> {
    manager.install(&app).await
}

#[cfg(test)]
mod tests {
    use super::*;
    fn release(tag: &str, prerelease: bool) -> GithubRelease {
        GithubRelease {
            tag_name: tag.into(),
            draft: false,
            prerelease,
            body: None,
            published_at: None,
            assets: vec![],
        }
    }
    #[test]
    fn notices_skip_states_that_resolve_on_their_own() {
        assert_eq!(notice("available", false), Some("available"));
        assert_eq!(notice("available", true), None);
        assert_eq!(notice("manual", true), Some("manual"));
        assert_eq!(notice("ready", true), Some("ready"));
        for phase in ["idle", "checking", "up_to_date", "downloading", "error"] {
            assert_eq!(notice(phase, false), None, "{phase}");
        }
        // Native notifications read the same catalogs; a missing key would
        // surface raw, and zh-CN would silently fall back to English.
        for kind in ["available", "manual", "ready"] {
            let title = format!("about.phase.{kind}");
            let body = format!("about.{kind}Notification");
            let vars = [("version", "1.1.0")];
            let en = i18n::t(i18n::Locale::En, &body, &vars);
            let zh = i18n::t(i18n::Locale::ZhCN, &body, &vars);
            assert!(en.contains("1.1.0") && zh.contains("1.1.0"), "{body}");
            assert_ne!(en, zh, "{body}");
            assert_ne!(
                i18n::t(i18n::Locale::En, &title, &[]),
                i18n::t(i18n::Locale::ZhCN, &title, &[]),
                "{title}"
            );
        }
    }
    #[test]
    fn cached_artifacts_are_verified_before_installation() {
        let fixture: serde_json::Value =
            serde_json::from_str(include_str!("../testdata/update-signature.json")).unwrap();
        let message = fixture["message"].as_str().unwrap().as_bytes();
        let signature = fixture["signature"].as_str().unwrap();
        let key = fixture["key"].as_str().unwrap();
        verify_package(message, signature, key)
            .expect("Node-signed Minisign fixture verifies independently in Rust");
        let mut corrupted = message.to_vec();
        corrupted[0] ^= 1;
        assert!(verify_package(&corrupted, signature, key).is_err());
        let decoded = String::from_utf8(STANDARD.decode(signature).unwrap())
            .unwrap()
            .replace("timestamp:1", "timestamp:2");
        assert!(verify_package(message, &STANDARD.encode(decoded), key).is_err());
    }

    fn response_server(
        status: &str,
        headers: &str,
        body: &str,
    ) -> (String, std::thread::JoinHandle<()>) {
        use std::io::{Read, Write};
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let url = format!("http://{}", listener.local_addr().unwrap());
        let response = format!("HTTP/1.1 {status}\r\nContent-Length: {}\r\nContent-Type: application/json\r\nConnection: close\r\n{headers}\r\n{body}", body.len());
        let handle = std::thread::spawn(move || {
            let (mut socket, _) = listener.accept().unwrap();
            socket
                .set_read_timeout(Some(Duration::from_secs(3)))
                .unwrap();
            let mut request = [0; 4096];
            let _ = socket.read(&mut request).unwrap();
            socket.write_all(response.as_bytes()).unwrap();
        });
        (url, handle)
    }

    #[tokio::test]
    async fn github_failures_are_distinct_from_no_releases_and_rate_limits_back_off() {
        let client = reqwest::Client::builder()
            .no_proxy()
            .timeout(Duration::from_secs(3))
            .build()
            .unwrap();
        for (status, body, expected) in [
            ("200 OK", "[]", None),
            ("500 Internal Server Error", "{}", Some("network")),
            ("200 OK", "invalid", Some("manifest")),
            ("429 Too Many Requests", "{}", Some("rate_limit")),
            ("403 Forbidden", "{}", Some("rate_limit")),
        ] {
            let (url, server) = response_server(status, "Retry-After: 60\r\n", body);
            let mut retry = None;
            let result = fetch_releases(&client, &url, &mut retry).await;
            server.join().unwrap();
            match expected {
                Some(code) => assert_eq!(result.unwrap_err().0, code),
                None => assert!(result.unwrap().is_empty()),
            }
            if expected == Some("rate_limit") {
                assert!(retry.unwrap() > Instant::now());
            }
        }
        let mut retry = None;
        assert_eq!(
            fetch_releases(&client, "http://127.0.0.1:0", &mut retry)
                .await
                .unwrap_err()
                .0,
            "network"
        );
    }

    #[test]
    fn channels_select_by_semver_not_dates_and_never_accept_drafts() {
        let mut draft = release("9.0.0", false);
        draft.draft = true;
        let releases = vec![
            release("v1.9.0", false),
            release("1.10.0", false),
            release("2.0.0-beta.2", false),
            release("2.0.0-beta.10", true),
            release("invalid", false),
            draft,
        ];
        assert_eq!(
            select_release(&releases, UpdateChannel::Stable)
                .unwrap()
                .tag_name,
            "1.10.0"
        );
        assert_eq!(
            select_release(&releases, UpdateChannel::Preview)
                .unwrap()
                .tag_name,
            "2.0.0-beta.10"
        );
        assert!(version("v1.10.0")
            .unwrap()
            .cmp_precedence(&version("2.0.0").unwrap())
            .is_lt());
        assert!(select_release(&[], UpdateChannel::Stable).is_none());
    }
    #[test]
    fn asset_urls_must_belong_to_selected_release() {
        assert!(github_asset(
            "https://github.com/Calcium-Ion/AstrLink/releases/download/v1.0.0/latest.json",
            "v1.0.0"
        ));
        for url in [
            "http://github.com/Calcium-Ion/AstrLink/releases/download/v1.0.0/a",
            "https://example.org/a",
            "https://github.com/Calcium-Ion/AstrLink/releases/download/v0.1.0/a",
            "https://github.com.evil/Calcium-Ion/AstrLink/releases/download/v1.0.0/a",
        ] {
            assert!(!github_asset(url, "v1.0.0"));
        }
    }
    #[test]
    fn invalid_signatures_and_keys_fail_closed() {
        assert!(verify_package(b"tampered", "invalid", "invalid").is_err());
        assert!(verify_package(b"", "", "").is_err());
    }
    #[test]
    fn preferences_migrate_defaults_and_reject_unknown_channels() {
        assert_eq!(
            serde_json::from_str::<UpdatePreferences>("{}").unwrap(),
            UpdatePreferences::default()
        );
        assert!(serde_json::from_str::<UpdatePreferences>(r#"{"channel":"nightly"}"#).is_err());
    }
}
