//! `make dev` runs an isolated AstrLink beside the one started with
//! `make start` or installed. Its identifier, set by `tauri.dev.conf.json`,
//! gives it separate data and config directories and its own single-instance
//! lock. This module picks the rest: the default inference port, the link
//! scheme and the name of its login item. Client configs, agent tools and
//! the agent locator stay in the user's home, shared with the other
//! AstrLink.

use std::sync::atomic::{AtomicBool, Ordering};

pub const ISOLATED_IDENTIFIER: &str = "com.astrlink.desktop.isolated";
pub const ISOLATED_NAME: &str = "AstrLink Dev";
pub const ISOLATED_LINK_SCHEME: &str = "astrlink-isolated";
pub const LINK_SCHEME: &str = "astrlink";
const ISOLATED_INFERENCE_PORT: u16 = 18318;

static ISOLATED: AtomicBool = AtomicBool::new(false);

/// Applies the identity the build was configured with. Plugins read the link
/// schemes and the product name, which names the login item, when the app is
/// built.
pub fn configure<R: tauri::Runtime>(mut context: tauri::Context<R>) -> tauri::Context<R> {
    if context.config().identifier == ISOLATED_IDENTIFIER {
        ISOLATED.store(true, Ordering::Relaxed);
        isolate(context.config_mut());
        context.package_info_mut().name = ISOLATED_NAME.to_string();
    }
    context
}

/// Registers the isolated link scheme in place of the shared one.
fn isolate(config: &mut tauri::Config) {
    if let Some(schemes) = config
        .plugins
        .0
        .get_mut("deep-link")
        .and_then(|deep_link| deep_link.pointer_mut("/desktop/schemes"))
    {
        *schemes = serde_json::json!([ISOLATED_LINK_SCHEME]);
    }
}

pub fn is_isolated() -> bool {
    ISOLATED.load(Ordering::Relaxed)
}

/// The name this instance goes by, as its login item and app bundle show it.
pub fn product_name() -> &'static str {
    if is_isolated() {
        ISOLATED_NAME
    } else {
        "AstrLink"
    }
}

pub fn default_inference_port() -> u16 {
    if is_isolated() {
        ISOLATED_INFERENCE_PORT
    } else {
        crate::preferences::DEFAULT_INFERENCE_PORT
    }
}

pub fn link_scheme() -> &'static str {
    if is_isolated() {
        ISOLATED_LINK_SCHEME
    } else {
        LINK_SCHEME
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_dev_config_selects_the_isolated_identifier() {
        let config: serde_json::Value =
            serde_json::from_str(include_str!("../tauri.dev.conf.json")).unwrap();
        assert_eq!(config["identifier"], ISOLATED_IDENTIFIER);
    }

    #[test]
    fn the_shared_instance_goes_by_the_configured_product_name() {
        let config: serde_json::Value =
            serde_json::from_str(include_str!("../tauri.conf.json")).unwrap();
        assert_eq!(config["productName"], product_name());
    }

    #[test]
    fn the_isolated_identity_registers_its_own_link_scheme() {
        let mut config: tauri::Config =
            serde_json::from_str(include_str!("../tauri.conf.json")).unwrap();
        assert_eq!(
            config.plugins.0["deep-link"]["desktop"]["schemes"],
            serde_json::json!([LINK_SCHEME])
        );
        isolate(&mut config);
        assert_eq!(
            config.plugins.0["deep-link"]["desktop"]["schemes"],
            serde_json::json!([ISOLATED_LINK_SCHEME])
        );
    }
}
