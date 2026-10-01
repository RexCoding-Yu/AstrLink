//! Read-only check of whether a client that honours the system proxy reaches
//! the local gateway. Codex's Rust HTTP stack reads the same system settings
//! through the same matcher, and on Windows it treats bypass entries such as
//! `127.*` as domain names, so `127.0.0.1` can end up inside the proxy. This
//! check never changes proxy settings, the registry, or environment
//! variables; the UI only explains what the user can change.

use std::time::Duration;

use hyper_util::client::proxy::matcher::Matcher;
use reqwest::{header::HeaderMap, redirect, StatusCode};
use serde::Serialize;

use crate::client_config;

/// Mirrors `ingress.ReachabilityPath`: token-less and never recorded.
const REACHABILITY_PATH: &str = "/astrlink/reachability";
/// Mirrors `ingress.ReachabilityHeader`, which only the gateway sets.
const REACHABILITY_HEADER: &str = "x-astrlink-reachable";
const PROBE_TIMEOUT: Duration = Duration::from_secs(5);

/// How a system-proxy-aware client reaches one gateway address.
#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
#[serde(tag = "route", rename_all = "snake_case")]
pub enum Route {
    /// The system proxy does not apply; the client connects directly.
    Direct,
    /// Requests pass through the proxy and still reach the gateway.
    Proxied { proxy: String },
    /// Requests pass through the proxy and never reach the gateway.
    Blocked { proxy: String },
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct ProxyCheck {
    /// The address AstrLink writes into client configs.
    pub client: Route,
    /// `http://127.0.0.1:<port>` while the client address is localhost, for
    /// configs written by hand or by other tools before the switch.
    pub numeric: Option<Route>,
}

pub async fn check(inference_url: &str) -> Result<ProxyCheck, String> {
    let origin = client_config::local_origin(inference_url)?;
    let matcher = Matcher::from_system();
    let client = route(&matcher, &origin).await?;
    let numeric = match numeric_origin(&origin) {
        Some(numeric) => Some(route(&matcher, &numeric).await?),
        None => None,
    };
    Ok(ProxyCheck { client, numeric })
}

async fn route(matcher: &Matcher, origin: &str) -> Result<Route, String> {
    let uri: http::Uri = origin.parse().map_err(|_| "invalid inference URL")?;
    let Some(intercept) = matcher.intercept(&uri) else {
        return Ok(Route::Direct);
    };
    let proxy = proxy_authority(intercept.uri());
    Ok(if probe(origin).await {
        Route::Proxied { proxy }
    } else {
        Route::Blocked { proxy }
    })
}

fn numeric_origin(origin: &str) -> Option<String> {
    origin
        .strip_prefix("http://localhost:")
        .map(|port| format!("http://127.0.0.1:{port}"))
}

/// Host and port only: proxy credentials never leave this module.
fn proxy_authority(uri: &http::Uri) -> String {
    match (uri.host(), uri.port_u16()) {
        (Some(host), Some(port)) => format!("{host}:{port}"),
        (Some(host), None) => host.to_string(),
        _ => String::new(),
    }
}

/// Sends the probe the way Codex would: the default client honours the
/// system proxy. No token or identifying header is attached.
async fn probe(origin: &str) -> bool {
    let Ok(client) = reqwest::Client::builder()
        .timeout(PROBE_TIMEOUT)
        .redirect(redirect::Policy::none())
        .build()
    else {
        return false;
    };
    match client
        .get(format!("{origin}{REACHABILITY_PATH}"))
        .send()
        .await
    {
        Ok(response) => reached(response.status(), response.headers()),
        Err(_) => false,
    }
}

/// A proxy that cannot reach the gateway answers on its own (often an empty
/// 502), so only the gateway's marker counts.
fn reached(status: StatusCode, headers: &HeaderMap) -> bool {
    status == StatusCode::NO_CONTENT
        && headers
            .get(REACHABILITY_HEADER)
            .is_some_and(|value| value == "1")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn localhost_also_checks_the_numeric_address() {
        assert_eq!(
            numeric_origin("http://localhost:18317").as_deref(),
            Some("http://127.0.0.1:18317")
        );
        assert_eq!(numeric_origin("http://127.0.0.1:18317"), None);
    }

    #[test]
    fn proxy_authority_omits_credentials() {
        let matcher = Matcher::builder()
            .http("http://user:secret@127.0.0.1:7892")
            .build();
        let intercept = matcher
            .intercept(&http::Uri::from_static("http://127.0.0.1:18317"))
            .unwrap();
        assert_eq!(proxy_authority(intercept.uri()), "127.0.0.1:7892");
    }

    #[tokio::test]
    async fn bypassed_addresses_connect_directly_without_probing() {
        let matcher = Matcher::builder()
            .http("http://127.0.0.1:7892")
            .no("localhost")
            .build();
        assert_eq!(
            route(&matcher, "http://localhost:18317").await.unwrap(),
            Route::Direct
        );
    }

    #[test]
    fn only_the_gateway_marker_counts_as_reached() {
        let mut marked = HeaderMap::new();
        marked.insert(REACHABILITY_HEADER, "1".parse().unwrap());
        assert!(reached(StatusCode::NO_CONTENT, &marked));
        assert!(!reached(StatusCode::NO_CONTENT, &HeaderMap::new()));
        assert!(!reached(StatusCode::BAD_GATEWAY, &marked));
    }
}
