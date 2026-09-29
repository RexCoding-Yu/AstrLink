use std::collections::HashMap;

use super::ClientError;

// Match Core's macOS system-proxy policy. Command-line installers, unlike
// native HTTP clients, need explicit environment variables for curl/npm.
pub(super) fn mac_environment(raw: &str) -> Result<Vec<(String, String)>, ClientError> {
    let mut values = HashMap::new();
    let mut bypass = vec!["localhost".to_string(), "127.0.0.1".into(), "::1".into()];
    let mut depth = 0_i32;
    let mut exceptions = false;
    for line in raw.lines().map(str::trim) {
        if line.ends_with('{') {
            if depth == 1 {
                exceptions = line.starts_with("ExceptionsList :");
            }
            depth += 1;
        } else if line == "}" {
            depth -= 1;
            if depth < 2 {
                exceptions = false;
            }
        } else if let Some((key, value)) = line.split_once(" : ") {
            if depth == 1 {
                values.insert(key, value);
            } else if depth == 2 && exceptions {
                bypass.push(value.strip_prefix('*').unwrap_or(value).to_string());
            }
        }
    }
    if depth != 0 || !raw.trim_start().starts_with("<dictionary> {") {
        return Err(ClientError::new("proxy", "Invalid macOS proxy response"));
    }
    if values.get("ProxyAutoConfigEnable") == Some(&"1")
        || values.get("ProxyAutoDiscoveryEnable") == Some(&"1")
    {
        return Err(ClientError::new("proxy", "PAC/WPAD is not supported by command-line updates. Configure a manual system HTTP/HTTPS/SOCKS proxy."));
    }
    let address = |protocol: &str, scheme: &str| -> Result<String, ClientError> {
        if values.get(format!("{protocol}Enable").as_str()) != Some(&"1") {
            return Ok(String::new());
        }
        let host = values
            .get(format!("{protocol}Proxy").as_str())
            .copied()
            .unwrap_or_default();
        let port = values
            .get(format!("{protocol}Port").as_str())
            .and_then(|p| p.parse::<u16>().ok())
            .filter(|p| *p > 0)
            .ok_or_else(|| ClientError::new("proxy", "Invalid proxy port"))?;
        if host.is_empty()
            || host
                .chars()
                .any(|c| c.is_whitespace() || "/@?#".contains(c))
        {
            return Err(ClientError::new("proxy", "Invalid proxy host"));
        }
        let host = host.trim_matches(['[', ']']);
        let host = if host.contains(':') {
            format!("[{host}]")
        } else {
            host.to_string()
        };
        let url = format!("{scheme}://{host}:{port}");
        reqwest::Url::parse(&url).map_err(|e| ClientError::new("proxy", e))?;
        Ok(url)
    };
    let socks = address("SOCKS", "socks5h")?;
    let http = address("HTTP", "http")?;
    let https = address("HTTPS", "http")?;
    let http = if http.is_empty() { socks.clone() } else { http };
    let https = if https.is_empty() {
        socks.clone()
    } else {
        https
    };
    let bypass = bypass.join(",");
    Ok([
        ("HTTP_PROXY", http.clone()),
        ("http_proxy", http.clone()),
        ("HTTPS_PROXY", https.clone()),
        ("https_proxy", https.clone()),
        ("ALL_PROXY", socks.clone()),
        ("all_proxy", socks),
        ("NO_PROXY", bypass.clone()),
        ("no_proxy", bypass.clone()),
        ("npm_config_proxy", http),
        ("npm_config_https_proxy", https),
        ("npm_config_noproxy", bypass),
    ]
    .into_iter()
    .map(|(key, value)| (key.to_string(), value))
    .collect())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn exports_system_connect_proxy_and_ignores_scoped_dictionaries() {
        let raw = "<dictionary> {\n HTTPSProxy : 127.0.0.1\n HTTPSPort : 6152\n HTTPSEnable : 1\n ExceptionsList : <array> {\n 0 : *.local\n }\n __SCOPED__ : <dictionary> {\n HTTPSProxy : wrong.example\n HTTPSPort : 123\n }\n}";
        let env: HashMap<_, _> = mac_environment(raw).unwrap().into_iter().collect();
        assert_eq!(env["HTTPS_PROXY"], "http://127.0.0.1:6152");
        assert_eq!(env["https_proxy"], env["HTTPS_PROXY"]);
        assert_eq!(env["npm_config_https_proxy"], env["HTTPS_PROXY"]);
        assert_eq!(env["HTTP_PROXY"], "");
        assert!(env["NO_PROXY"].contains(".local"));
    }

    #[test]
    fn handles_disabled_proxies_socks_ipv6_and_invalid_configuration() {
        let empty: HashMap<_, _> = mac_environment("<dictionary> {\n}")
            .unwrap()
            .into_iter()
            .collect();
        assert_eq!(empty["HTTPS_PROXY"], "");
        let socks: HashMap<_, _> = mac_environment(
            "<dictionary> {\n SOCKSEnable : 1\n SOCKSProxy : ::1\n SOCKSPort : 1080\n}",
        )
        .unwrap()
        .into_iter()
        .collect();
        assert_eq!(socks["HTTPS_PROXY"], "socks5h://[::1]:1080");
        for raw in ["garbage", "<dictionary> {\n ProxyAutoConfigEnable : 1\n}", "<dictionary> {\n HTTPSEnable : 1\n HTTPSProxy : proxy@example.com\n HTTPSPort : 6152\n}", "<dictionary> {\n HTTPSEnable : 1\n HTTPSProxy : localhost\n HTTPSPort : 99999\n}"] {
            assert_eq!(mac_environment(raw).unwrap_err().code, "proxy");
        }
    }

    #[cfg(unix)]
    #[test]
    fn passes_proxy_to_installer_children_and_honors_direct_mode() {
        use crate::client_updates::process::{run, CommandSpec, Environment};
        use std::time::Duration;
        let proxy_env = mac_environment(
            "<dictionary> {\n HTTPSEnable : 1\n HTTPSProxy : 127.0.0.1\n HTTPSPort : 6152\n}",
        )
        .unwrap();
        let mut environment = Environment {
            home: std::env::temp_dir(),
            path: "/usr/bin:/bin".into(),
            use_system_proxy: true,
            proxy_env: Some(proxy_env),
        };
        let command = CommandSpec::new(
            "/bin/sh",
            &[
                "-c",
                "printf '%s|%s|%s' \"$HTTPS_PROXY\" \"$npm_config_https_proxy\" \"$NO_PROXY\"",
            ],
        );
        let result = run(&environment, &command, Duration::from_secs(3)).unwrap();
        assert!(result.starts_with("http://127.0.0.1:6152|http://127.0.0.1:6152|"));
        environment.use_system_proxy = false;
        assert_eq!(
            run(&environment, &command, Duration::from_secs(3)).unwrap(),
            "||*"
        );
    }
}
