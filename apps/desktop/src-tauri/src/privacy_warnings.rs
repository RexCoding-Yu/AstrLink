//! Native notifications for requests the privacy policy's warn action let
//! through. Core keeps the recent ones in an in-memory feed and marks those
//! that carried a value it had not warned about before, so a secret resent
//! with every call of a conversation notifies once. The tray's observer loop
//! polls the feed and raises at most one notification per poll.

use std::sync::Arc;

use serde::Deserialize;
use tauri::{AppHandle, Manager};

use crate::{
    i18n::{self, Locale},
    preferences::PreferencesStore,
    sidecar::CoreManager,
};

pub const PRIVACY_WARNINGS_PATH: &str = "/control/v1/privacy/warnings";

/// The kinds Core reports, in the order the desktop lists them. Must match
/// `PRIVACY_KIND_ORDER` in the frontend.
const KIND_ORDER: [&str; 10] = [
    "email",
    "phone",
    "account",
    "payment_card",
    "ip_address",
    "url",
    "common_secret",
    "private_address",
    "private_date",
    "private_person",
];
/// Stands in for a kind from a newer Core.
const UNKNOWN_KIND: &str = "unknown";

/// The part of Core's feed after the sequence the desktop asked for.
#[derive(Debug, Deserialize)]
pub struct WarningPage {
    epoch: String,
    #[serde(default)]
    events: Vec<WarningEvent>,
}

#[derive(Debug, Deserialize)]
struct WarningEvent {
    sequence: u64,
    #[serde(default)]
    kinds: Vec<WarningKind>,
    #[serde(default)]
    notifiable: bool,
}

#[derive(Debug, Deserialize)]
struct WarningKind {
    kind: String,
}

pub fn parse_page(body: &[u8]) -> Result<WarningPage, String> {
    serde_json::from_slice(body)
        .map_err(|error| format!("privacy warnings returned invalid JSON: {error}"))
}

/// How far the desktop has read Core's feed. The epoch names one Core
/// process, whose sequence starts over when it restarts.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct WarningCursor {
    epoch: Option<String>,
    sequence: u64,
}

impl WarningCursor {
    pub fn since(&self) -> u64 {
        self.sequence
    }
}

/// What one read of the feed decided.
#[derive(Debug, PartialEq, Eq)]
enum Step {
    /// Core restarted and its sequence began again; read it from the start.
    Restart,
    /// Move to `cursor` and notify about `kinds`, if any.
    Advance {
        cursor: WarningCursor,
        kinds: Vec<&'static str>,
    },
}

/// Folds every warning that carried a new value into one notification's kinds.
fn step(cursor: &WarningCursor, page: &WarningPage) -> Step {
    if cursor
        .epoch
        .as_ref()
        .is_some_and(|epoch| *epoch != page.epoch)
    {
        return Step::Restart;
    }
    let mut next = WarningCursor {
        epoch: Some(page.epoch.clone()),
        sequence: cursor.sequence,
    };
    let mut found = Vec::new();
    for event in &page.events {
        if event.sequence <= cursor.sequence {
            continue;
        }
        next.sequence = next.sequence.max(event.sequence);
        if event.notifiable {
            found.extend(event.kinds.iter().map(|kind| kind.kind.as_str()));
        }
    }
    Step::Advance {
        cursor: next,
        kinds: ordered_kinds(&found),
    }
}

/// Known kinds in display order, then one stand-in for any kind this build
/// does not know.
fn ordered_kinds(found: &[&str]) -> Vec<&'static str> {
    let mut kinds: Vec<&'static str> = KIND_ORDER
        .into_iter()
        .filter(|kind| found.contains(kind))
        .collect();
    if found.iter().any(|kind| !KIND_ORDER.contains(kind)) {
        kinds.push(UNKNOWN_KIND);
    }
    kinds
}

/// The notification names kinds only; a value never reaches the desktop.
fn notification_text(locale: Locale, kinds: &[&str]) -> (String, String) {
    let labels: Vec<String> = kinds
        .iter()
        .map(|kind| i18n::t(locale, &format!("privacy.{kind}"), &[]))
        .collect();
    let separator = i18n::t(locale, "host.privacyWarning.kindSeparator", &[]);
    let title = i18n::t(
        locale,
        "host.privacyWarning.title",
        &[("kinds", &labels.join(&separator))],
    );
    (title, i18n::t(locale, "host.privacyWarning.body", &[]))
}

/// Reads what Core warned about since the last poll and notifies once for the
/// warnings that carried a new value. A failed read keeps the cursor: Core may
/// be restarting or predate the feed, and the next poll tries again.
pub async fn poll(app: &AppHandle, manager: &CoreManager, cursor: &mut WarningCursor) {
    // A restarted Core takes a second read, from the start of its feed.
    for _ in 0..2 {
        let Ok(page) = manager.privacy_warnings(cursor.since()).await else {
            return;
        };
        match step(cursor, &page) {
            Step::Restart => *cursor = WarningCursor::default(),
            Step::Advance {
                cursor: next,
                kinds,
            } => {
                *cursor = next;
                if !kinds.is_empty() {
                    let locale = app
                        .try_state::<Arc<PreferencesStore>>()
                        .map(|store| store.snapshot().values.locale)
                        .unwrap_or_default();
                    let (title, body) = notification_text(locale, &kinds);
                    crate::notify_native(app, title, body);
                }
                return;
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn page(epoch: &str, events: serde_json::Value) -> WarningPage {
        parse_page(
            serde_json::json!({ "epoch": epoch, "events": events })
                .to_string()
                .as_bytes(),
        )
        .unwrap()
    }

    fn event(sequence: u64, notifiable: bool, kinds: &[&str]) -> serde_json::Value {
        serde_json::json!({
            "sequence": sequence,
            "at": "2026-10-12T10:00:00Z",
            "request_id": format!("req_{sequence}"),
            "kinds": kinds
                .iter()
                .map(|kind| serde_json::json!({ "kind": kind, "count": 1 }))
                .collect::<Vec<_>>(),
            "notifiable": notifiable,
        })
    }

    fn advanced(epoch: &str, sequence: u64) -> WarningCursor {
        WarningCursor {
            epoch: Some(epoch.to_string()),
            sequence,
        }
    }

    #[test]
    fn a_burst_becomes_one_notification_naming_every_new_kind() {
        let burst = page(
            "a",
            serde_json::json!([
                event(1, true, &["common_secret"]),
                // The same secret resent with the history: already notified.
                event(2, false, &["common_secret"]),
                event(3, true, &["email", "common_secret"]),
            ]),
        );
        assert_eq!(
            step(&WarningCursor::default(), &burst),
            Step::Advance {
                cursor: advanced("a", 3),
                kinds: vec!["email", "common_secret"],
            }
        );
    }

    #[test]
    fn warnings_without_a_new_value_only_move_the_cursor() {
        let resent = page(
            "a",
            serde_json::json!([event(4, false, &["email"]), event(5, false, &["email"])]),
        );
        assert_eq!(
            step(&advanced("a", 3), &resent),
            Step::Advance {
                cursor: advanced("a", 5),
                kinds: vec![],
            }
        );
        assert_eq!(
            step(&advanced("a", 5), &page("a", serde_json::json!([]))),
            Step::Advance {
                cursor: advanced("a", 5),
                kinds: vec![],
            }
        );
    }

    #[test]
    fn a_restarted_core_is_read_again_from_its_first_warning() {
        // The new Core numbers from 1 again, so its first warnings sit below
        // the old cursor and would be lost if the cursor were kept.
        let restarted = page("b", serde_json::json!([event(1, true, &["phone"])]));
        assert_eq!(step(&advanced("a", 9), &restarted), Step::Restart);
        assert_eq!(
            step(&WarningCursor::default(), &restarted),
            Step::Advance {
                cursor: advanced("b", 1),
                kinds: vec!["phone"],
            }
        );
    }

    #[test]
    fn warnings_already_read_are_not_shown_again() {
        let repeated = page(
            "a",
            serde_json::json!([event(2, true, &["email"]), event(3, true, &["url"])]),
        );
        assert_eq!(
            step(&advanced("a", 2), &repeated),
            Step::Advance {
                cursor: advanced("a", 3),
                kinds: vec!["url"],
            }
        );
    }

    #[test]
    fn kinds_from_a_newer_core_read_as_one_other_kind() {
        let newer = page(
            "a",
            serde_json::json!([event(1, true, &["passport", "email", "tax_id"])]),
        );
        assert_eq!(
            step(&WarningCursor::default(), &newer),
            Step::Advance {
                cursor: advanced("a", 1),
                kinds: vec!["email", "unknown"],
            }
        );
    }

    #[test]
    fn the_notification_names_kinds_and_the_safer_actions() {
        let (title, body) = notification_text(Locale::ZhCN, &["email", "common_secret"]);
        assert_eq!(title, "已按原文发给模型：邮箱、常见密钥");
        assert!(body.contains("「警告并继续」"), "{body}");
        assert!(body.contains("「脱敏后继续」"), "{body}");
        assert!(body.contains("「阻止请求」"), "{body}");
        let (title, body) = notification_text(Locale::En, &["common_secret"]);
        assert_eq!(title, "Sent to the model as is: Common secrets");
        assert!(body.contains("“Redact and continue”"), "{body}");
        assert!(body.contains("“Block the request”"), "{body}");
        let (title, _) = notification_text(Locale::En, &["unknown"]);
        assert_eq!(title, "Sent to the model as is: Other");
    }

    #[test]
    fn a_page_without_events_parses_as_empty() {
        let page = parse_page(br#"{"epoch":"a"}"#).unwrap();
        assert!(page.events.is_empty());
        assert!(parse_page(b"not json").is_err());
    }
}
