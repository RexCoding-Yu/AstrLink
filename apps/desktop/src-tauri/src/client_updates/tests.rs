use super::*;
use std::{
    io::{Read, Write},
    net::TcpListener,
    sync::atomic::{AtomicU64, Ordering},
};

static NEXT: AtomicU64 = AtomicU64::new(0);
struct Fixture(PathBuf);
impl Fixture {
    fn new() -> Self {
        let path = std::env::temp_dir().join(format!(
            "astrlink-client-test-{}-{}",
            std::process::id(),
            NEXT.fetch_add(1, Ordering::SeqCst)
        ));
        fs::create_dir_all(&path).unwrap();
        Self(path)
    }
    fn write(&self, path: &str, contents: &str) -> PathBuf {
        let path = self.0.join(path);
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(&path, contents).unwrap();
        path
    }
    #[cfg(unix)]
    fn script(&self, path: &str, contents: &str) -> PathBuf {
        use std::os::unix::fs::PermissionsExt;
        let path = self.write(path, contents);
        fs::set_permissions(&path, fs::Permissions::from_mode(0o755)).unwrap();
        path
    }
    #[cfg(unix)]
    fn environment(&self) -> Environment {
        Environment {
            home: self.0.clone(),
            path: std::env::join_paths([
                self.0.join("bin"),
                PathBuf::from("/usr/bin"),
                PathBuf::from("/bin"),
            ])
            .unwrap(),
            use_system_proxy: false,
            proxy_env: None,
        }
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.0);
    }
}

#[test]
fn reads_version_output_and_compares_semver_without_downgrading() {
    assert_eq!(
        parse_version("codex-cli 0.153.4\n").unwrap(),
        Version::new(0, 153, 4)
    );
    assert_eq!(
        parse_version("2.1.280 (Claude Code)\n").unwrap(),
        Version::new(2, 1, 280)
    );
    assert!(parse_version("v2.10.0").unwrap() > parse_version("2.9.9").unwrap());
    assert!(parse_version("0.160.0-beta.1").unwrap() > parse_version("0.159.0").unwrap());
    assert!(parse_version("not installed").is_err());
}

#[test]
fn identifies_npm_and_brew_without_confusing_native_installations() {
    let fixture = Fixture::new();
    let npm = fixture.write(
        "node/lib/node_modules/@openai/codex/package.json",
        r#"{"name":"@openai/codex"}"#,
    );
    let binary = npm.parent().unwrap().join("bin/codex.js");
    assert_eq!(
        installation_method(ClientId::Codex, &binary, &fixture.0),
        InstallMethod::Npm {
            prefix: fixture.0.join("node")
        }
    );
    let homebrew = PathBuf::from("/opt/homebrew/Caskroom/claude-code@latest/2.1.280/claude");
    assert_eq!(
        installation_method(ClientId::Claude, &homebrew, &fixture.0),
        InstallMethod::Brew {
            root: "/opt/homebrew".into(),
            package: "claude-code@latest".into(),
            cask: true
        }
    );
    assert!(matches!(
        installation_method(
            ClientId::Codex,
            Path::new("/usr/local/Cellar/codex/1.0/bin/codex"),
            &fixture.0
        ),
        InstallMethod::Brew { cask: false, .. }
    ));
    assert_eq!(
        installation_method(
            ClientId::Claude,
            &fixture.0.join(".local/share/claude/versions/2.1.0"),
            &fixture.0
        ),
        InstallMethod::Native
    );
    assert_eq!(
        installation_method(ClientId::Codex, &fixture.0.join("random/codex"), &fixture.0),
        InstallMethod::Unknown
    );
}

#[test]
fn excludes_other_package_managers_and_project_node_modules() {
    let fixture = Fixture::new();
    let package = fixture.write(
        ".bun/install/global/node_modules/@openai/codex/package.json",
        r#"{"name":"@openai/codex"}"#,
    );
    assert_eq!(
        installation_method(
            ClientId::Codex,
            &package.parent().unwrap().join("bin/codex.js"),
            &fixture.0
        ),
        InstallMethod::Unknown
    );
    #[cfg(unix)]
    {
        let project = fixture.write(
            "project/node_modules/@openai/codex/package.json",
            r#"{"name":"@openai/codex"}"#,
        );
        assert_eq!(
            installation_method(
                ClientId::Codex,
                &project.parent().unwrap().join("bin/codex.js"),
                &fixture.0
            ),
            InstallMethod::Unknown
        );
    }
    assert!(find_programs(std::ffi::OsStr::new(".:relative"), "codex").is_empty());
}

#[test]
fn parses_latest_sources_and_rejects_invalid_versions() {
    assert_eq!(
        parse_latest(
            r#"{"version":"0.158.0"}"#,
            &InstallMethod::Native,
            ClientId::Codex
        )
        .unwrap(),
        Version::new(0, 158, 0)
    );
    assert_eq!(
        parse_latest("2.1.283\n", &InstallMethod::Native, ClientId::Claude).unwrap(),
        Version::new(2, 1, 283)
    );
    let method = InstallMethod::Brew {
        root: "/opt/homebrew".into(),
        package: "codex".into(),
        cask: false,
    };
    assert_eq!(
        parse_latest(
            r#"{"versions":{"stable":"0.158.0"}}"#,
            &method,
            ClientId::Codex
        )
        .unwrap(),
        Version::new(0, 158, 0)
    );
    for invalid in [
        r#"{"version":"$(touch evil)"}"#,
        r#"{"version":"1.0.0-beta.1"}"#,
        "<html>error</html>",
    ] {
        assert!(parse_latest(invalid, &InstallMethod::Unknown, ClientId::Codex).is_err());
    }
}

#[test]
fn claude_version_check_respects_the_selected_native_channel() {
    let fixture = Fixture::new();
    let install = Installation {
        executable: "claude".into(),
        resolved: "claude".into(),
        method: InstallMethod::Native,
        others: vec![],
    };
    assert!(latest_url(ClientId::Claude, &install, &fixture.0).ends_with("/latest"));
    fixture.write(
        ".claude/settings.json",
        r#"{"autoUpdatesChannel":"stable"}"#,
    );
    assert!(latest_url(ClientId::Claude, &install, &fixture.0).ends_with("/stable"));
}

#[test]
fn serializes_operations_and_rejects_unknown_client_ids() {
    let manager = ClientUpdateManager::default();
    let guard = manager.operation.try_lock().unwrap();
    assert!(manager.operation.try_lock().is_err());
    drop(guard);
    assert!(manager.operation.try_lock().is_ok());
    assert!(serde_json::from_str::<ClientId>(r#""codex; rm -rf anything""#).is_err());
    assert_eq!(manager.snapshot().clients.len(), 2);
}

#[test]
fn reports_masked_curl_failures_as_network_errors_and_keeps_the_error_tail() {
    let output = format!(
        "{}Update ran successfully!\ncurl: (35) LibreSSL SSL_connect: SSL_ERROR_SYSCALL",
        "log\n".repeat(2000)
    );
    let error = ClientError::new("unchanged", output);
    assert_eq!(error.code, "network");
    assert!(error.detail.contains("SSL_ERROR_SYSCALL"));
    assert!(error.detail.chars().count() <= 4096);
}

#[cfg(target_os = "macos")]
#[test]
#[ignore = "Read-only live connectivity check; requires system proxy and network access"]
fn installed_system_proxy_reaches_codex_download_without_installing() {
    let environment = load_environment(crate::control_session::user_home().unwrap(), true).unwrap();
    let result = process::run(
        &environment,
        &CommandSpec::new(
            "/usr/bin/curl",
            &[
                "-fsSL",
                "--max-time",
                "20",
                "--output",
                "/dev/null",
                "--write-out",
                "%{http_code}",
                "https://chatgpt.com/codex/install.sh",
            ],
        ),
        Duration::from_secs(25),
    )
    .unwrap();
    assert_eq!(result.trim(), "200");
}

#[cfg(unix)]
#[test]
fn respects_path_order_and_deduplicates_symlinks() {
    use std::os::unix::fs::symlink;
    let fixture = Fixture::new();
    let first = fixture.script("first/codex", "#!/bin/sh\necho 'codex-cli 1.0.0'\n");
    fixture.script("second/codex", "#!/bin/sh\necho 'codex-cli 2.0.0'\n");
    fs::create_dir_all(fixture.0.join("alias")).unwrap();
    symlink(&first, fixture.0.join("alias/codex")).unwrap();
    let path = std::env::join_paths([
        fixture.0.join("first"),
        fixture.0.join("alias"),
        fixture.0.join("second"),
    ])
    .unwrap();
    let paths = find_programs(&path, "codex");
    assert_eq!(paths, [first, fixture.0.join("second/codex")]);
}

#[cfg(unix)]
fn fake_codex(fixture: &Fixture, update: &str) -> Installation {
    use std::os::unix::fs::symlink;
    fixture.write(
        "package/codex-package.json",
        r#"{"variant":"codex","layoutVersion":1,"entrypoint":"bin/codex"}"#,
    );
    let script = fixture.script(
        "package/bin/codex",
        &format!("#!/bin/sh\nif [ \"$1\" = '--version' ]; then cat version; else {update}; fi\n"),
    );
    fixture.write("version", "codex-cli 1.0.0\n");
    fs::create_dir_all(fixture.0.join("bin")).unwrap();
    symlink(script, fixture.0.join("bin/codex")).unwrap();
    detect(&fixture.environment(), ClientId::Codex).unwrap()
}

#[cfg(unix)]
#[test]
fn updates_an_isolated_native_client_and_checks_the_result() {
    let fixture = Fixture::new();
    let install = fake_codex(&fixture, "echo 'codex-cli 1.1.0' > version");
    assert_eq!(install.method, InstallMethod::Native);
    let result = perform_update(
        &fixture.environment(),
        ClientId::Codex,
        &install,
        &Version::new(1, 1, 0),
    )
    .unwrap();
    assert_eq!(result, Version::new(1, 1, 0));
    // Never downgrade even if the currently running check has stale metadata.
    let result = perform_update(
        &fixture.environment(),
        ClientId::Codex,
        &install,
        &Version::new(1, 0, 0),
    )
    .unwrap();
    assert_eq!(result, Version::new(1, 1, 0));
}

#[cfg(unix)]
#[test]
fn reports_update_failure_and_success_without_version_change() {
    for (script, code) in [
        ("echo permission-denied >&2; exit 1", "command"),
        ("echo download-failed >&2; exit 0", "unchanged"),
    ] {
        let fixture = Fixture::new();
        let install = fake_codex(&fixture, script);
        let error = perform_update(
            &fixture.environment(),
            ClientId::Codex,
            &install,
            &Version::new(1, 1, 0),
        )
        .unwrap_err();
        assert_eq!(error.code, code);
        if code == "unchanged" {
            assert!(error.detail.contains("download-failed"));
        }
        assert_eq!(
            current_version(&fixture.environment(), &install).unwrap(),
            Version::new(1, 0, 0)
        );
    }
}

#[cfg(unix)]
#[test]
fn refuses_to_update_when_the_selected_installation_changes() {
    let fixture = Fixture::new();
    let install = fake_codex(&fixture, "echo should-not-run > marker");
    fs::remove_file(&install.executable).unwrap();
    fixture.script("bin/codex", "#!/bin/sh\necho 'codex-cli 1.0.0'\n");
    assert_eq!(
        perform_update(
            &fixture.environment(),
            ClientId::Codex,
            &install,
            &Version::new(1, 1, 0)
        )
        .unwrap_err()
        .code,
        "changed"
    );
    assert!(!fixture.0.join("marker").exists());
}

#[cfg(unix)]
#[test]
fn npm_update_targets_the_detected_prefix_and_pins_the_version() {
    let fixture = Fixture::new();
    fixture.script("node/bin/node", "#!/bin/sh\nexit 0\n");
    fixture.write("node/lib/node_modules/npm/bin/npm-cli.js", "");
    let install = Installation {
        executable: fixture.0.join("node/bin/codex"),
        resolved: PathBuf::new(),
        method: InstallMethod::Npm {
            prefix: fixture.0.join("node"),
        },
        others: vec![],
    };
    let command = update_command(
        &fixture.environment(),
        ClientId::Codex,
        &install,
        &Version::new(0, 158, 0),
    )
    .unwrap();
    assert_eq!(command.program, fixture.0.join("node/bin/node"));
    assert_eq!(command.args[4], fixture.0.join("node").as_os_str());
    assert_eq!(command.args[5], "@openai/codex@0.158.0");
}

#[tokio::test]
async fn handles_http_failure_rate_limit_and_invalid_metadata() {
    for (status, body, code) in [
        ("429 Too Many Requests", "", "rate_limit"),
        ("503 Service Unavailable", "", "network"),
        ("200 OK", "not-json", "version"),
    ] {
        let server = TcpListener::bind("127.0.0.1:0").unwrap();
        let url = format!("http://{}/latest", server.local_addr().unwrap());
        let body = body.to_string();
        let worker = std::thread::spawn(move || {
            let (mut stream, _) = server.accept().unwrap();
            let mut request = [0; 2048];
            let _ = stream.read(&mut request);
            write!(
                stream,
                "HTTP/1.1 {status}\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
                body.len()
            )
            .unwrap();
        });
        let client = reqwest::Client::builder().no_proxy().build().unwrap();
        let install = Installation {
            executable: "codex".into(),
            resolved: "codex".into(),
            method: InstallMethod::Native,
            others: vec![],
        };
        assert_eq!(
            fetch_latest(&client, &url, &install, ClientId::Codex)
                .await
                .unwrap_err()
                .code,
            code
        );
        worker.join().unwrap();
    }
}
