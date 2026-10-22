//! Reminds the operator when the privacy model the policy runs has a newer
//! release, until the policy runs it. A configured gateway runs unattended, so
//! the update hint on the privacy page alone reaches almost nobody. Installing
//! and switching to the new version stay manual.
use std::{
    sync::{Arc, Mutex, MutexGuard},
    time::{Duration, Instant},
};

use semver::Version;
use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Emitter, Manager, State};
use tokio::sync::Notify;

use crate::{
    i18n,
    preferences::PreferencesStore,
    sidecar::{CoreManager, CorePhase},
};

const EVENT: &str = "privacy-model-update";
/// Core caches release lookups for an hour, so each check reaches the Hub.
const CHECK_INTERVAL: Duration = Duration::from_secs(3 * 60 * 60);
const RETRY_INTERVAL: Duration = Duration::from_secs(15 * 60);
/// The policy and installations are local reads. Re-reading them notices a
/// finished download, or a switch made outside the privacy page.
const EVALUATE_INTERVAL: Duration = Duration::from_secs(60);

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum UpdatePhase {
    /// The new version still has to be downloaded.
    Available,
    /// The new version is installed and waits for the policy to switch.
    Ready,
}

impl UpdatePhase {
    fn key(self) -> &'static str {
        match self {
            Self::Available => "available",
            Self::Ready => "ready",
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct PrivacyModelUpdate {
    pub catalog_id: String,
    pub name: String,
    pub version: String,
    pub phase: UpdatePhase,
}

#[derive(Default)]
pub struct PrivacyModelUpdates {
    inner: Mutex<Inner>,
    wake: Notify,
}

#[derive(Default)]
struct Inner {
    /// The newest release of each catalog model, from the last successful
    /// check.
    releases: Option<Vec<CatalogModel>>,
    next_check: Option<Instant>,
    update: Option<PrivacyModelUpdate>,
    notified: Option<PrivacyModelUpdate>,
}

#[derive(Deserialize)]
struct Catalog {
    items: Vec<CatalogModel>,
}

#[derive(Clone, Debug, Deserialize)]
struct CatalogModel {
    id: String,
    name: String,
    repo_id: String,
    revision: String,
    version: Option<String>,
    variants: Vec<CatalogVariant>,
}

#[derive(Clone, Debug, Deserialize)]
struct CatalogVariant {
    id: String,
    supported: bool,
}

#[derive(Deserialize)]
struct InstallationList {
    items: Vec<Installation>,
}

#[derive(Debug, Deserialize)]
struct Installation {
    id: String,
    source: String,
    catalog_id: Option<String>,
    repo_id: String,
    revision: String,
    variant_id: String,
    status: String,
}

#[derive(Deserialize)]
struct Policy {
    enabled: bool,
    detector: String,
    local_model_id: Option<String>,
}

impl PrivacyModelUpdates {
    fn lock(&self) -> MutexGuard<'_, Inner> {
        self.inner.lock().unwrap_or_else(|e| e.into_inner())
    }

    /// Re-reads the policy and installations without waiting for the tick.
    pub fn wake(&self) {
        self.wake.notify_one();
    }

    /// Also re-reads releases from Core, which answers from its own cache.
    pub fn recheck(&self) {
        self.lock().next_check = None;
        self.wake();
    }

    async fn tick(&self, app: &AppHandle, manager: &CoreManager) {
        let now = Instant::now();
        if self.lock().next_check.map_or(true, |at| at <= now) {
            let checked = tokio::try_join!(
                manager.get_privacy_model_catalog(),
                manager.get_privacy_model_releases(false),
            )
            .and_then(|(catalog, releases)| {
                Ok(latest_releases(
                    serde_json::from_value(catalog).map_err(|e| e.to_string())?,
                    serde_json::from_value(releases).map_err(|e| e.to_string())?,
                ))
            });
            let mut inner = self.lock();
            match checked {
                Ok(releases) => {
                    inner.releases = Some(releases);
                    inner.next_check = Some(now + CHECK_INTERVAL);
                }
                Err(error) => {
                    eprintln!("unable to check privacy model releases: {error}");
                    inner.next_check = Some(now + RETRY_INTERVAL);
                }
            }
        }
        let Some(releases) = self.lock().releases.clone() else {
            return;
        };
        let read = tokio::try_join!(
            manager.get_privacy_policy(),
            manager.list_privacy_model_installations(),
        )
        .and_then(|(record, installations)| {
            Ok((
                serde_json::from_value::<Policy>(record.policy).map_err(|e| e.to_string())?,
                serde_json::from_value::<InstallationList>(installations)
                    .map_err(|e| e.to_string())?,
            ))
        });
        match read {
            Ok((policy, installations)) => {
                self.publish(
                    app,
                    pending_update(&releases, &installations.items, &policy),
                );
            }
            // Core may be restarting; the next tick reads again.
            Err(error) => eprintln!("unable to read the privacy model in use: {error}"),
        }
    }

    fn publish(&self, app: &AppHandle, update: Option<PrivacyModelUpdate>) {
        let announce = {
            let mut inner = self.lock();
            if inner.update == update {
                return;
            }
            inner.update.clone_from(&update);
            let announce = update
                .clone()
                .filter(|next| inner.notified.as_ref() != Some(next));
            if announce.is_some() {
                inner.notified.clone_from(&announce);
            }
            announce
        };
        if let Err(error) = app.emit_to("main", EVENT, &update) {
            eprintln!("unable to publish privacy model update: {error}");
        }
        if let Some(update) = announce {
            notify_unattended(app, &update);
        }
    }
}

/// The main window shows its own toast and sidebar mark; a native
/// notification covers the times it cannot be seen.
fn notify_unattended(app: &AppHandle, update: &PrivacyModelUpdate) {
    if crate::updates::main_window_attended(app) {
        return;
    }
    let locale = app
        .state::<Arc<PreferencesStore>>()
        .snapshot()
        .values
        .locale;
    let key = update.phase.key();
    crate::notify_native(
        app,
        i18n::t(locale, &format!("safety.modelUpdate.{key}.title"), &[]),
        i18n::t(
            locale,
            &format!("safety.modelUpdate.{key}.notification"),
            &[("name", &update.name), ("version", &update.version)],
        ),
    );
}

/// Picks the newest release of each catalog model, matching the privacy page's
/// own update hint: a release that supersedes the pinned entry, otherwise the
/// pinned entry itself, which installs of an earlier pin still move to.
fn latest_releases(catalog: Catalog, releases: Catalog) -> Vec<CatalogModel> {
    let version = |model: &CatalogModel| {
        model
            .version
            .as_deref()
            .and_then(|version| Version::parse(version).ok())
    };
    catalog
        .items
        .into_iter()
        .filter_map(|pinned| {
            let release = releases
                .items
                .iter()
                .find(|release| release.id == pinned.id)?;
            let newer = release.repo_id == pinned.repo_id
                && release.revision != pinned.revision
                && version(release)
                    .zip(version(&pinned))
                    .is_some_and(|(release, pinned)| release > pinned);
            Some(if newer { release.clone() } else { pinned })
        })
        .collect()
}

/// Only the model the enabled policy runs earns a reminder. It moves to
/// `Ready` once the newer revision of that variant is installed and clears
/// when the policy switches to it.
fn pending_update(
    releases: &[CatalogModel],
    installations: &[Installation],
    policy: &Policy,
) -> Option<PrivacyModelUpdate> {
    if !policy.enabled || policy.detector != "local_model" {
        return None;
    }
    let local_model_id = policy.local_model_id.as_deref()?;
    let current = installations.iter().find(|installation| {
        installation.id == local_model_id && installation.source == "catalog"
    })?;
    let release = releases
        .iter()
        .find(|release| current.catalog_id.as_deref() == Some(release.id.as_str()))?;
    let supported = release
        .variants
        .iter()
        .any(|variant| variant.id == current.variant_id && variant.supported);
    if release.repo_id != current.repo_id || release.revision == current.revision || !supported {
        return None;
    }
    // A download in progress or failed still needs the operator to finish it.
    let ready = installations.iter().any(|installation| {
        installation.repo_id == release.repo_id
            && installation.revision == release.revision
            && installation.variant_id == current.variant_id
            && installation.status == "ready"
    });
    Some(PrivacyModelUpdate {
        catalog_id: release.id.clone(),
        name: release.name.clone(),
        version: release.version.clone()?,
        phase: if ready {
            UpdatePhase::Ready
        } else {
            UpdatePhase::Available
        },
    })
}

pub fn start(app: &AppHandle) {
    let (Some(manager), Some(updates)) = (
        app.try_state::<Arc<CoreManager>>(),
        app.try_state::<Arc<PrivacyModelUpdates>>(),
    ) else {
        return;
    };
    let manager = Arc::clone(manager.inner());
    let updates = Arc::clone(updates.inner());
    let app = app.clone();
    tauri::async_runtime::spawn(async move {
        loop {
            tokio::select! {
                _ = tokio::time::sleep(EVALUATE_INTERVAL) => {}
                _ = updates.wake.notified() => {}
            }
            if manager.view().phase == CorePhase::Ready {
                updates.tick(&app, &manager).await;
            }
        }
    });
}

#[tauri::command]
pub fn privacy_model_update_status(
    updates: State<'_, Arc<PrivacyModelUpdates>>,
) -> Option<PrivacyModelUpdate> {
    updates.lock().update.clone()
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    const PINNED: &str = "49e8b7b83d34fd75a86cf07698bd80972d444ff3";
    const RELEASE: &str = "11697fb46757a275ada41873c5e0e8f59bfd421d";
    const EARLIER_PIN: &str = "1111111111111111111111111111111111111111";

    fn model(revision: &str, version: &str) -> serde_json::Value {
        json!({
            "id": "astrlink-guard",
            "name": "AstrLink Guard",
            "repo_id": "QuantumNous/astrlink-guard",
            "revision": revision,
            "version": version,
            "variants": [
                { "id": "cpu-int8", "supported": true },
                { "id": "gpu-fp16", "supported": false },
            ],
        })
    }

    fn releases(release: serde_json::Value) -> Vec<CatalogModel> {
        latest_releases(
            serde_json::from_value(json!({ "items": [model(PINNED, "0.1.0")] })).unwrap(),
            serde_json::from_value(json!({ "items": [release] })).unwrap(),
        )
    }

    fn installation(id: &str, revision: &str, variant_id: &str) -> Installation {
        Installation {
            id: id.to_string(),
            source: "catalog".to_string(),
            catalog_id: Some("astrlink-guard".to_string()),
            repo_id: "QuantumNous/astrlink-guard".to_string(),
            revision: revision.to_string(),
            variant_id: variant_id.to_string(),
            status: "ready".to_string(),
        }
    }

    fn guard(phase: UpdatePhase) -> Option<PrivacyModelUpdate> {
        Some(PrivacyModelUpdate {
            catalog_id: "astrlink-guard".to_string(),
            name: "AstrLink Guard".to_string(),
            version: "0.1.1".to_string(),
            phase,
        })
    }

    fn policy(local_model_id: Option<&str>) -> Policy {
        Policy {
            enabled: true,
            detector: "local_model".to_string(),
            local_model_id: local_model_id.map(str::to_string),
        }
    }

    #[test]
    fn reminds_about_a_newer_release_of_the_model_in_use() {
        let installations = [installation("model_a", PINNED, "cpu-int8")];
        assert_eq!(
            pending_update(
                &releases(model(RELEASE, "0.1.1")),
                &installations,
                &policy(Some("model_a")),
            ),
            guard(UpdatePhase::Available),
        );
    }

    #[test]
    fn falls_back_to_the_pinned_entry_without_a_newer_release() {
        for release in [
            model(PINNED, "0.1.0"),
            model(RELEASE, "0.0.9"),
            model(RELEASE, "not-semver"),
        ] {
            let latest = releases(release);
            assert_eq!(latest.len(), 1);
            assert_eq!(latest[0].revision, PINNED);
            assert_eq!(
                pending_update(
                    &latest,
                    &[installation("model_a", PINNED, "cpu-int8")],
                    &policy(Some("model_a")),
                ),
                None,
            );
        }
    }

    #[test]
    fn moves_installs_of_an_earlier_pin_to_the_pinned_release() {
        let installations = [installation("model_a", EARLIER_PIN, "cpu-int8")];
        assert_eq!(
            pending_update(
                &releases(model(PINNED, "0.1.0")),
                &installations,
                &policy(Some("model_a")),
            ),
            Some(PrivacyModelUpdate {
                version: "0.1.0".to_string(),
                ..guard(UpdatePhase::Available).unwrap()
            }),
        );
    }

    #[test]
    fn stays_quiet_when_the_policy_does_not_run_that_model() {
        let releases = releases(model(RELEASE, "0.1.1"));
        let installations = [installation("model_a", PINNED, "cpu-int8")];
        let mut regex = policy(Some("model_a"));
        regex.detector = "regex".to_string();
        let mut disabled = policy(Some("model_a"));
        disabled.enabled = false;
        for policy in [regex, disabled, policy(None), policy(Some("model_other"))] {
            assert_eq!(pending_update(&releases, &installations, &policy), None);
        }
        let mut local = installation("model_a", PINNED, "cpu-int8");
        local.source = "local".to_string();
        assert_eq!(
            pending_update(&releases, &[local], &policy(Some("model_a"))),
            None,
        );
    }

    #[test]
    fn waits_for_the_switch_once_the_new_revision_is_ready() {
        let releases = releases(model(RELEASE, "0.1.1"));
        let mut downloading = installation("model_b", RELEASE, "cpu-int8");
        downloading.status = "downloading".to_string();
        let mut both = [installation("model_a", PINNED, "cpu-int8"), downloading];
        assert_eq!(
            pending_update(&releases, &both, &policy(Some("model_a"))),
            guard(UpdatePhase::Available),
        );
        both[1].status = "ready".to_string();
        assert_eq!(
            pending_update(&releases, &both, &policy(Some("model_a"))),
            guard(UpdatePhase::Ready),
        );
        // Another variant of the new revision does not replace this one.
        let other_variant = [
            installation("model_a", PINNED, "cpu-int8"),
            installation("model_c", RELEASE, "gpu-fp16"),
        ];
        assert_eq!(
            pending_update(&releases, &other_variant, &policy(Some("model_a"))),
            guard(UpdatePhase::Available),
        );
        assert_eq!(
            pending_update(&releases, &both, &policy(Some("model_b"))),
            None
        );
    }

    #[test]
    fn serializes_the_phase_for_the_shell() {
        assert_eq!(
            serde_json::to_value(guard(UpdatePhase::Ready)).unwrap(),
            json!({
                "catalog_id": "astrlink-guard",
                "name": "AstrLink Guard",
                "version": "0.1.1",
                "phase": "ready",
            }),
        );
    }

    #[test]
    fn skips_variants_the_release_cannot_run_here() {
        let installations = [installation("model_a", PINNED, "gpu-fp16")];
        assert_eq!(
            pending_update(
                &releases(model(RELEASE, "0.1.1")),
                &installations,
                &policy(Some("model_a")),
            ),
            None,
        );
    }
}
