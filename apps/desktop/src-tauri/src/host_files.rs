//! Edits to files other programs own: agent configs, skills, and client
//! connection settings in the user's home directory.

use std::{
    ffi::OsStr,
    fs::{self, OpenOptions},
    io::{self, Write},
    path::{Path, PathBuf},
    sync::{
        atomic::{AtomicU64, Ordering},
        Mutex, MutexGuard, PoisonError,
    },
};

static LOCK: Mutex<()> = Mutex::new(());
static TEMPORARY_SEQUENCE: AtomicU64 = AtomicU64::new(0);

/// Serializes this process's edits to host files. Agent installs and client
/// configuration both rewrite `~/.claude/settings.json`.
pub fn lock() -> MutexGuard<'static, ()> {
    LOCK.lock().unwrap_or_else(PoisonError::into_inner)
}

#[derive(Default)]
pub struct WriteOptions<'a> {
    /// Tightens the file to owner-only access after writing (Unix).
    #[cfg_attr(not(unix), allow(dead_code))]
    pub secret: bool,
    /// The contents the edit was computed from, `None` for a missing file.
    /// The write is abandoned when the file no longer holds them.
    pub expected: Option<Option<&'a str>>,
}

pub fn read_optional(path: &Path) -> Result<Option<String>, String> {
    match fs::read_to_string(path) {
        Ok(raw) => Ok(Some(raw)),
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(None),
        Err(error) => Err(format!("unable to read {}: {error}", path.display())),
    }
}

/// A unique sibling for staging a replacement of `name` in `parent`.
pub fn temporary_sibling(parent: &Path, name: &OsStr) -> PathBuf {
    parent.join(format!(
        ".{}.tmp-{}-{}",
        name.to_string_lossy(),
        std::process::id(),
        TEMPORARY_SEQUENCE.fetch_add(1, Ordering::Relaxed)
    ))
}

/// Replaces a user's file through a temporary sibling, so a crash or a full
/// disk leaves either the old contents or the new ones. A symlinked file is
/// replaced at its target, and an existing file keeps its permissions.
pub fn write_text(path: &Path, contents: &str, options: WriteOptions) -> Result<(), String> {
    let is_symlink = fs::symlink_metadata(path).is_ok_and(|metadata| metadata.is_symlink());
    let target = if is_symlink {
        fs::canonicalize(path)
            .map_err(|error| format!("unable to resolve {}: {error}", path.display()))?
    } else {
        path.to_path_buf()
    };
    let (Some(parent), Some(name)) = (target.parent(), target.file_name()) else {
        return Err(format!(
            "unable to write {}: no parent directory",
            path.display()
        ));
    };
    fs::create_dir_all(parent)
        .map_err(|error| format!("unable to create {}: {error}", parent.display()))?;
    let permissions = fs::metadata(&target)
        .ok()
        .map(|metadata| metadata.permissions());
    let temporary = temporary_sibling(parent, name);
    let result = (|| {
        let mut file = OpenOptions::new()
            .create_new(true)
            .write(true)
            .open(&temporary)?;
        file.write_all(contents.as_bytes())?;
        if let Some(permissions) = permissions {
            file.set_permissions(permissions)?;
        }
        #[cfg(unix)]
        if options.secret {
            use std::os::unix::fs::PermissionsExt;
            file.set_permissions(fs::Permissions::from_mode(0o600))?;
        }
        file.sync_all()?;
        drop(file);
        if let Some(expected) = options.expected {
            let current = match fs::read_to_string(&target) {
                Ok(raw) => Some(raw),
                Err(error) if error.kind() == io::ErrorKind::NotFound => None,
                Err(error) => return Err(error),
            };
            if current.as_deref() != expected {
                return Err(io::Error::other(
                    "the file changed while it was being edited; try again",
                ));
            }
        }
        crate::preferences::atomic_replace(&temporary, &target)
    })();
    if let Err(error) = result {
        let _ = fs::remove_file(&temporary);
        return Err(format!("unable to write {}: {error}", path.display()));
    }
    Ok(())
}

pub fn remove_path(path: &Path) -> Result<(), String> {
    match fs::symlink_metadata(path) {
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(()),
        Err(error) => Err(format!("unable to inspect {}: {error}", path.display())),
        Ok(metadata) if metadata.file_type().is_dir() && !metadata.file_type().is_symlink() => {
            fs::remove_dir_all(path)
                .map_err(|error| format!("unable to remove {}: {error}", path.display()))
        }
        Ok(_) => fs::remove_file(path)
            .or_else(|_| fs::remove_dir_all(path))
            .map_err(|error| format!("unable to remove {}: {error}", path.display())),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn unique_temp(name: &str) -> PathBuf {
        let path = std::env::temp_dir().join(format!(
            "astrlink-host-files-{name}-{}-{}",
            std::process::id(),
            TEMPORARY_SEQUENCE.fetch_add(1, Ordering::Relaxed)
        ));
        let _ = fs::remove_dir_all(&path);
        fs::create_dir_all(&path).unwrap();
        path
    }

    #[cfg(unix)]
    #[test]
    fn a_secret_file_is_owner_only_even_when_it_existed() {
        use std::os::unix::fs::PermissionsExt;
        let dir = unique_temp("secret");
        let path = dir.join("config.toml");
        fs::write(&path, "a = 1\n").unwrap();
        fs::set_permissions(&path, fs::Permissions::from_mode(0o644)).unwrap();
        write_text(
            &path,
            "a = 2\n",
            WriteOptions {
                secret: true,
                ..Default::default()
            },
        )
        .unwrap();
        let mode = fs::metadata(&path).unwrap().permissions().mode() & 0o777;
        assert_eq!(mode, 0o600);
        assert_eq!(fs::read_to_string(&path).unwrap(), "a = 2\n");
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn a_file_that_changed_after_reading_is_left_alone() {
        let dir = unique_temp("expected");
        let path = dir.join("settings.json");
        fs::write(&path, "{\"client\": true}\n").unwrap();
        let error = write_text(
            &path,
            "{}\n",
            WriteOptions {
                expected: Some(Some("{}\n")),
                ..Default::default()
            },
        )
        .unwrap_err();
        assert!(error.contains("changed"));
        assert_eq!(fs::read_to_string(&path).unwrap(), "{\"client\": true}\n");
        let error = write_text(
            &dir.join("missing.json"),
            "{}\n",
            WriteOptions {
                expected: Some(Some("{}\n")),
                ..Default::default()
            },
        )
        .unwrap_err();
        assert!(error.contains("changed"));
        assert!(!dir.join("missing.json").exists());
        write_text(
            &path,
            "{}\n",
            WriteOptions {
                expected: Some(Some("{\"client\": true}\n")),
                ..Default::default()
            },
        )
        .unwrap();
        assert_eq!(fs::read_to_string(&path).unwrap(), "{}\n");
        let leftovers = fs::read_dir(&dir)
            .unwrap()
            .filter(|entry| {
                entry
                    .as_ref()
                    .unwrap()
                    .file_name()
                    .to_string_lossy()
                    .contains(".tmp-")
            })
            .count();
        assert_eq!(leftovers, 0);
        fs::remove_dir_all(dir).unwrap();
    }
}
