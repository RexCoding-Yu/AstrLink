//! A hidden window keeps its webview. On Windows the WebView2 processes
//! behind it hold 100–200 MB for as long as AstrLink sits in the tray, so a
//! window left hidden for `HIDDEN_LIFETIME` is destroyed, and showing it
//! again builds a new one.
//!
//! Destroying is asynchronous: the label stays taken until the window is
//! gone. A show that arrives meanwhile is recorded, and the caller of
//! `destroyed` builds the window then.

use std::collections::{HashMap, HashSet};
use std::sync::{Mutex, MutexGuard};
use std::time::Duration;

use tauri::{AppHandle, Manager};

/// Long enough that another look from the tray, or a window closed by
/// mistake, comes back as it was left.
const HIDDEN_LIFETIME: Duration = Duration::from_secs(5 * 60);

/// What showing a window takes.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Show {
    /// The window exists; show it.
    Existing,
    /// There is no window. Build one, then report it with `built`.
    Build,
    /// A destroy or build under way brings the window up when it ends.
    Pending,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Phase {
    /// Hidden; the timer armed with this generation destroys it.
    Hidden(u64),
    /// Destroy sent; `reopen` records a show that arrived since.
    Destroying { reopen: bool },
    /// A new window is being built.
    Building,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Expiry {
    /// Shown, destroyed or hidden again since the timer was armed.
    Stale,
    /// The window asked to be kept; look again later.
    Held,
    Destroy,
}

#[derive(Debug, Default)]
struct Phases {
    phases: HashMap<String, Phase>,
    held: HashSet<String>,
    generation: u64,
}

impl Phases {
    fn hidden(&mut self, label: &str) -> u64 {
        self.generation += 1;
        self.phases
            .insert(label.to_owned(), Phase::Hidden(self.generation));
        self.generation
    }

    fn show(&mut self, label: &str, exists: bool) -> Show {
        match self.phases.get_mut(label) {
            Some(Phase::Destroying { reopen }) => {
                *reopen = true;
                Show::Pending
            }
            Some(Phase::Building) => Show::Pending,
            Some(Phase::Hidden(_)) | None if exists => {
                self.phases.remove(label);
                Show::Existing
            }
            Some(Phase::Hidden(_)) | None => {
                self.phases.insert(label.to_owned(), Phase::Building);
                Show::Build
            }
        }
    }

    fn built(&mut self, label: &str) {
        if self.phases.get(label) == Some(&Phase::Building) {
            self.phases.remove(label);
        }
    }

    fn expire(&mut self, label: &str, generation: u64) -> Expiry {
        if self.phases.get(label) != Some(&Phase::Hidden(generation)) {
            return Expiry::Stale;
        }
        if self.held.contains(label) {
            return Expiry::Held;
        }
        self.phases
            .insert(label.to_owned(), Phase::Destroying { reopen: false });
        Expiry::Destroy
    }

    fn destroyed(&mut self, label: &str) -> bool {
        match self.phases.get(label) {
            Some(Phase::Destroying { reopen }) => {
                let reopen = *reopen;
                self.phases.remove(label);
                reopen
            }
            Some(Phase::Hidden(_)) => {
                self.phases.remove(label);
                false
            }
            Some(Phase::Building) | None => false,
        }
    }

    fn hold(&mut self, label: &str, held: bool) {
        if held {
            self.held.insert(label.to_owned());
        } else {
            self.held.remove(label);
        }
    }
}

/// Managed state behind the functions below.
#[derive(Default)]
pub struct WindowReclaim(Mutex<Phases>);

impl WindowReclaim {
    fn lock(&self) -> MutexGuard<'_, Phases> {
        self.0
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
    }
}

fn phases(app: &AppHandle) -> MutexGuard<'_, Phases> {
    app.state::<WindowReclaim>().inner().lock()
}

/// Starts the countdown for a window that was just hidden.
pub fn hidden(app: &AppHandle, label: &'static str) {
    let generation = phases(app).hidden(label);
    let app = app.clone();
    tauri::async_runtime::spawn(async move {
        loop {
            tokio::time::sleep(HIDDEN_LIFETIME).await;
            if expire(&app, label, generation) != Expiry::Held {
                return;
            }
        }
    });
}

/// How to bring `label` up. The caller acts on the answer; `Build` must
/// be followed by `built`.
pub fn show(app: &AppHandle, label: &str) -> Show {
    let exists = app.get_webview_window(label).is_some();
    phases(app).show(label, exists)
}

/// Ends a build `show` asked for, whether or not it succeeded.
pub fn built(app: &AppHandle, label: &str) {
    phases(app).built(label);
}

/// Records that `label` is gone. Returns whether a show arrived while it
/// was being destroyed; the caller then shows it again.
pub fn destroyed(app: &AppHandle, label: &str) -> bool {
    phases(app).destroyed(label)
}

/// Keeps `label` while it holds something it cannot rebuild, such as
/// unsaved edits.
pub fn hold(app: &AppHandle, label: &str, held: bool) {
    phases(app).hold(label, held);
}

fn expire(app: &AppHandle, label: &str, generation: u64) -> Expiry {
    let Some(window) = app.get_webview_window(label) else {
        return Expiry::Stale;
    };
    // A window brought back some other way stays.
    if window.is_visible().unwrap_or(true) {
        return Expiry::Stale;
    }
    let expiry = phases(app).expire(label, generation);
    if expiry == Expiry::Destroy {
        if let Err(error) = window.destroy() {
            eprintln!("unable to release the hidden {label} window: {error}");
            phases(app).destroyed(label);
        }
    }
    expiry
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_window_shown_before_its_time_is_kept() {
        let mut phases = Phases::default();
        let generation = phases.hidden("main");
        assert_eq!(phases.show("main", true), Show::Existing);
        assert_eq!(phases.expire("main", generation), Expiry::Stale);
    }

    #[test]
    fn only_the_latest_hide_counts() {
        let mut phases = Phases::default();
        let first = phases.hidden("main");
        assert_eq!(phases.show("main", true), Show::Existing);
        let second = phases.hidden("main");
        assert_eq!(phases.expire("main", first), Expiry::Stale);
        assert_eq!(phases.expire("main", second), Expiry::Destroy);
    }

    #[test]
    fn a_released_window_is_built_again() {
        let mut phases = Phases::default();
        let generation = phases.hidden("main");
        assert_eq!(phases.expire("main", generation), Expiry::Destroy);
        assert!(!phases.destroyed("main"));
        assert_eq!(phases.show("main", false), Show::Build);
        // Another request while the build runs leaves it to that build.
        assert_eq!(phases.show("main", false), Show::Pending);
        phases.built("main");
        assert_eq!(phases.show("main", true), Show::Existing);
    }

    #[test]
    fn a_show_during_the_destroy_reopens_once_it_is_gone() {
        let mut phases = Phases::default();
        let generation = phases.hidden("tray-popover");
        assert_eq!(phases.expire("tray-popover", generation), Expiry::Destroy);
        // The label is still taken here: building now would fail.
        assert_eq!(phases.show("tray-popover", true), Show::Pending);
        assert!(phases.destroyed("tray-popover"));
        assert_eq!(phases.show("tray-popover", false), Show::Build);
    }

    #[test]
    fn a_held_window_waits_until_released() {
        let mut phases = Phases::default();
        phases.hold("main", true);
        let generation = phases.hidden("main");
        assert_eq!(phases.expire("main", generation), Expiry::Held);
        phases.hold("main", false);
        assert_eq!(phases.expire("main", generation), Expiry::Destroy);
    }

    #[test]
    fn windows_are_tracked_apart() {
        let mut phases = Phases::default();
        let main = phases.hidden("main");
        phases.hold("tray-popover", true);
        let popover = phases.hidden("tray-popover");
        assert_eq!(phases.expire("main", main), Expiry::Destroy);
        assert_eq!(phases.expire("tray-popover", popover), Expiry::Held);
        assert_eq!(phases.show("tray-popover", true), Show::Existing);
        assert_eq!(phases.show("main", true), Show::Pending);
    }

    #[test]
    fn a_window_gone_another_way_is_built_on_the_next_show() {
        let mut phases = Phases::default();
        phases.hidden("main");
        assert!(!phases.destroyed("main"));
        assert_eq!(phases.show("main", false), Show::Build);
    }
}
