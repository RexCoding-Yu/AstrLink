//! Clients in WSL distributions, checked from Windows. Their paths stay Linux
//! paths. Commands, file checks, and link resolution run in the distribution
//! through wsl.exe, because Windows cannot follow Linux links through the
//! `\\wsl$` share. Manifests and settings files are read through the share.
//! Manifests lie under resolved paths; a settings file that is itself a link
//! reads as missing, and its default applies.

use std::{
    collections::HashSet,
    ffi::OsString,
    path::{Component, Path, PathBuf},
    time::Duration,
};

use super::{
    process::{self, CommandSpec, Environment},
    usual_directories, ClientError, Program, PROBE_TIMEOUT,
};

/// Covers starting a stopped distribution, which may boot the WSL VM.
const START_TIMEOUT: Duration = Duration::from_secs(60);

// wsl.exe receives these scripts through a Windows command line. They contain
// no double quotes, so its unquoting cannot change them. An empty IFS and
// `set -f` keep unquoted paths whole.

/// Prints the user's home and PATH. The login shell's PATH comes last; an
/// interactive shell also sets up version managers such as nvm.
const PROBE: &str = "echo __ASTRLINK_PATH__; printenv PATH; \
    ${SHELL:-/bin/sh} -ilc 'echo __ASTRLINK_PATH__; printenv PATH' </dev/null; \
    echo __ASTRLINK_HOME__; printenv HOME";
/// Prints each executable file among the arguments, a tab, and the file it
/// resolves to.
const LOOKUP: &str = r"IFS=; set -f; for f do if [ -f $f ] && [ -x $f ]; then printf '%s\t%s\n' $f $(readlink -f $f); fi; done";
const EXECUTABLE: &str = "IFS=; set -f; [ -f $1 ] && [ -x $1 ]";
const FILE: &str = "IFS=; set -f; [ -f $1 ]";

#[derive(Clone, Debug)]
pub(super) struct Wsl {
    pub distribution: String,
    /// wsl.exe, which runs commands in the distribution.
    pub launcher: PathBuf,
    /// The distribution's root as Windows reads files in it.
    pub share: PathBuf,
    /// Where the distribution mounts Windows drives: `/mnt` by default.
    pub mount_root: String,
}

impl Wsl {
    /// Reaches a registered distribution through wsl.exe and its share.
    pub fn new(distribution: &str) -> Result<Self, ClientError> {
        Ok(Self {
            distribution: distribution.to_string(),
            launcher: crate::wsl::program().map_err(|error| ClientError::new("command", error))?,
            share: crate::wsl::share_root(distribution),
            mount_root: crate::wsl::mount_root(distribution),
        })
    }

    /// A Linux path as Windows reads it through the share.
    pub fn shared(&self, path: &Path) -> Option<PathBuf> {
        if !path.has_root() {
            return None;
        }
        let mut shared = self.share.clone();
        for component in path.components() {
            match component {
                Component::RootDir => {}
                Component::Normal(part) => shared.push(part),
                _ => return None,
            }
        }
        Some(shared)
    }
}

/// How Linux spells a path in the distribution. On Windows, joining one adds
/// backslashes.
pub(super) fn linux(path: &Path) -> String {
    let parts = path
        .components()
        .filter_map(|component| match component {
            Component::Normal(part) => Some(part.to_string_lossy()),
            Component::ParentDir => Some("..".into()),
            _ => None,
        })
        .collect::<Vec<_>>()
        .join("/");
    if path.has_root() {
        format!("/{parts}")
    } else {
        parts
    }
}

/// Runs `spec` in the distribution, from the user's home, with the
/// environment changes a local command gets.
pub(super) fn command(environment: &Environment, wsl: &Wsl, spec: &CommandSpec) -> CommandSpec {
    let mut args = [
        "--distribution",
        wsl.distribution.as_str(),
        "--cd",
        "~",
        "--exec",
        "env",
    ]
    .map(OsString::from)
    .to_vec();
    let (removed, set) = process::changes(environment);
    for name in removed {
        args.extend(["-u".into(), OsString::from(*name)]);
    }
    if !environment.path.is_empty() {
        args.push(format!("PATH={}", environment.path.to_string_lossy()).into());
    }
    args.extend(
        set.into_iter()
            .map(|(name, value)| OsString::from(format!("{name}={value}"))),
    );
    args.push(linux(&spec.program).into());
    args.extend(spec.args.iter().cloned());
    CommandSpec {
        program: wsl.launcher.clone(),
        args,
    }
}

/// The distribution's environment for client checks: its user's home, and
/// the login shell's PATH with the usual install directories added.
pub(super) fn environment(wsl: Wsl, use_system_proxy: bool) -> Result<Environment, ClientError> {
    let mut environment = Environment {
        home: PathBuf::new(),
        path: OsString::new(),
        use_system_proxy,
        proxy_env: None,
        wsl: Some(wsl),
    };
    let output = process::run(
        &environment,
        &CommandSpec::new("/bin/sh", &["-c", PROBE]),
        START_TIMEOUT,
    )?;
    let (home, path) = parse_probe(&output).ok_or_else(|| {
        ClientError::new(
            "command",
            format!("WSL did not report the user's home and PATH\n{output}"),
        )
    })?;
    environment.home = PathBuf::from(home);
    let mut directories = path.split(':').map(str::to_string).collect::<Vec<_>>();
    for directory in usual_directories(&environment.home) {
        let directory = linux(&directory);
        if !directories.contains(&directory) {
            directories.push(directory);
        }
    }
    environment.path = directories.join(":").into();
    Ok(environment)
}

/// The home and PATH from the probe's output. Shell start-up files may print
/// around them; the last PATH wins.
fn parse_probe(output: &str) -> Option<(&str, &str)> {
    let lines = output.lines().map(str::trim).collect::<Vec<_>>();
    let value = |marker: &str| {
        lines
            .windows(2)
            .rev()
            .find(|pair| pair[0] == marker && pair[1].starts_with('/'))
            .map(|pair| pair[1])
    };
    Some((value("__ASTRLINK_HOME__")?, value("__ASTRLINK_PATH__")?))
}

/// The programs named `name` on PATH, apart from those on Windows drives.
/// WSL appends the Windows PATH, and this computer's own check covers it.
pub(super) fn programs(environment: &Environment, wsl: &Wsl, name: &str) -> Vec<Program> {
    let candidates = candidates(&environment.path.to_string_lossy(), &wsl.mount_root, name);
    lookup(environment, wsl, name, &candidates)
}

/// `path` and the file it resolves to, if it is an executable file.
pub(super) fn program(environment: &Environment, wsl: &Wsl, path: &Path) -> Option<Program> {
    let path = linux(path);
    lookup(environment, wsl, &path, std::slice::from_ref(&path))
        .into_iter()
        .next()
}

/// The executable files among `candidates`, in order, each target once.
fn lookup(environment: &Environment, wsl: &Wsl, name: &str, candidates: &[String]) -> Vec<Program> {
    if candidates.is_empty() {
        return Vec::new();
    }
    let mut lookup = CommandSpec::new("/bin/sh", &["-c", LOOKUP, "sh"]);
    lookup.args.extend(candidates.iter().map(OsString::from));
    let output = match process::run(environment, &lookup, PROBE_TIMEOUT) {
        Ok(output) => output,
        Err(error) => {
            eprintln!(
                "unable to look up {name} in WSL {}: {}",
                wsl.distribution, error.detail
            );
            return Vec::new();
        }
    };
    let mut seen = HashSet::new();
    output
        .lines()
        .filter_map(|line| {
            let (path, resolved) = line.split_once('\t')?;
            (candidates.iter().any(|candidate| candidate == path)
                && resolved.starts_with('/')
                && seen.insert(resolved.to_string()))
            .then(|| Program {
                path: path.into(),
                resolved: resolved.into(),
            })
        })
        .collect()
}

/// The files named `name` in a Linux PATH, in order, apart from directories
/// on Windows drives.
fn candidates(path: &str, mount_root: &str, name: &str) -> Vec<String> {
    let mut seen = HashSet::new();
    path.split(':')
        .filter(|directory| directory.starts_with('/') && !on_windows_drive(directory, mount_root))
        .map(|directory| format!("{}/{name}", directory.trim_end_matches('/')))
        .filter(|file| seen.insert(file.clone()))
        .collect()
}

/// Whether a Linux directory lies on a Windows drive the distribution mounts.
fn on_windows_drive(directory: &str, mount_root: &str) -> bool {
    let Some(rest) = directory
        .strip_prefix(mount_root)
        .and_then(|rest| rest.strip_prefix('/'))
    else {
        return false;
    };
    let mut chars = rest.chars();
    chars.next().is_some_and(|c| c.is_ascii_alphabetic())
        && matches!(chars.next(), None | Some('/'))
}

/// Whether `path` in the distribution is a file, and with `executable` set,
/// one the user may run. Links resolve in the distribution.
pub(super) fn is_file(environment: &Environment, path: &Path, executable: bool) -> bool {
    let test = if executable { EXECUTABLE } else { FILE };
    let path = linux(path);
    process::run(
        environment,
        &CommandSpec::new("/bin/sh", &["-c", test, "sh", &path]),
        PROBE_TIMEOUT,
    )
    .is_ok()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn lookups_skip_windows_drives_and_repeated_directories() {
        let path = "/home/ada/.local/bin:/usr/bin/:relative::/mnt/c/Users/ada/AppData/Roaming/npm:\
                    /mnt/d:/mnt/wsl/bin:/mntc/bin:/usr/bin";
        assert_eq!(
            candidates(path, "/mnt", "codex"),
            [
                "/home/ada/.local/bin/codex",
                "/usr/bin/codex",
                "/mnt/wsl/bin/codex",
                "/mntc/bin/codex",
            ]
        );
        // Drives mount at the root when wsl.conf sets `root = /`.
        assert_eq!(
            candidates("/c/tools:/usr/bin:/", "", "pi"),
            ["/usr/bin/pi", "/pi"]
        );
    }

    #[test]
    fn probe_output_survives_start_up_noise() {
        let output = "__ASTRLINK_PATH__\n/usr/bin:/bin\nWelcome to Ubuntu\n__ASTRLINK_PATH__\n\
                      /home/ada/.nvm/versions/node/v22.0.0/bin:/usr/bin\n__ASTRLINK_HOME__\n/home/ada\n\
                      bash: no job control in this shell";
        assert_eq!(
            parse_probe(output),
            Some((
                "/home/ada",
                "/home/ada/.nvm/versions/node/v22.0.0/bin:/usr/bin"
            ))
        );
        // A login shell that exits early leaves the plain PATH.
        assert_eq!(
            parse_probe(
                "__ASTRLINK_PATH__\n/usr/bin\n__ASTRLINK_PATH__\n__ASTRLINK_HOME__\n/root\n"
            ),
            Some(("/root", "/usr/bin"))
        );
        assert_eq!(parse_probe("__ASTRLINK_PATH__\n/usr/bin\n"), None);
    }

    #[test]
    fn linux_paths_keep_forward_slashes_after_joins() {
        let prefix = Path::new("/home/ada/.nvm/versions/node/v22.0.0");
        assert_eq!(
            linux(&prefix.join("lib/node_modules/npm/bin/npm-cli.js")),
            "/home/ada/.nvm/versions/node/v22.0.0/lib/node_modules/npm/bin/npm-cli.js"
        );
        assert_eq!(linux(Path::new("/")), "/");
        let wsl = Wsl {
            distribution: "Ubuntu".into(),
            launcher: "wsl.exe".into(),
            share: std::env::temp_dir(),
            mount_root: "/mnt".into(),
        };
        assert_eq!(
            wsl.shared(&Path::new("/home/ada").join(".claude/settings.json")),
            Some(std::env::temp_dir().join("home/ada/.claude/settings.json"))
        );
        assert_eq!(wsl.shared(Path::new("/home/../etc/shadow")), None);
        assert_eq!(wsl.shared(Path::new("home/ada")), None);
    }

    #[test]
    fn commands_run_through_wsl_from_the_home_with_the_local_environment() {
        let wsl = Wsl {
            distribution: "Ubuntu-24.04".into(),
            launcher: "wsl.exe".into(),
            share: "share".into(),
            mount_root: "/mnt".into(),
        };
        let environment = Environment {
            home: "/home/ada".into(),
            path: "/home/ada/.local/bin:/mnt/c/Program Files/nodejs".into(),
            use_system_proxy: false,
            proxy_env: None,
            wsl: Some(wsl.clone()),
        };
        let spec = CommandSpec::new(
            Path::new("/home/ada").join(".local/bin/claude"),
            &["update"],
        );
        let wrapped = command(&environment, &wsl, &spec);
        assert_eq!(wrapped.program, PathBuf::from("wsl.exe"));
        let args = wrapped
            .args
            .iter()
            .map(|arg| arg.to_str().unwrap())
            .collect::<Vec<_>>();
        assert_eq!(
            args[..8],
            [
                "--distribution",
                "Ubuntu-24.04",
                "--cd",
                "~",
                "--exec",
                "env",
                "-u",
                "HTTP_PROXY"
            ]
        );
        let assignments = args.iter().position(|arg| arg.contains('=')).unwrap();
        assert_eq!(
            args[assignments..],
            [
                "PATH=/home/ada/.local/bin:/mnt/c/Program Files/nodejs",
                "NO_COLOR=1",
                "CI=1",
                "DISABLE_AUTOUPDATER=1",
                "HOMEBREW_NO_INSTALL_CLEANUP=1",
                "NO_PROXY=*",
                "no_proxy=*",
                "npm_config_proxy=",
                "npm_config_https_proxy=",
                "/home/ada/.local/bin/claude",
                "update",
            ]
        );
        // With the system proxy, the distribution keeps its own proxy settings.
        let system = Environment {
            use_system_proxy: true,
            path: OsString::new(),
            ..environment
        };
        let args = command(&system, &wsl, &spec).args;
        assert!(!args
            .iter()
            .any(|arg| arg == "-u" || arg.to_string_lossy().starts_with("PATH=")));
    }
}
