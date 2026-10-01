use std::{fs, path::Path, time::UNIX_EPOCH};

use serde::Serialize;

const MAX_REPORTED_BACKUPS: usize = 100;

/// A file in the data directory whose name matches `*.bak*`. Such copies are
/// made by hand, outside AstrLink, and can hold every secret and captured body
/// in plain text. Settings points them out; AstrLink never deletes them.
#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
pub struct DataBackupFile {
    pub name: String,
    pub size_bytes: u64,
    pub modified_unix: Option<u64>,
}

/// Lists backup-looking regular files directly inside `data_directory`,
/// newest first. Symlinks and directories are skipped, and an unreadable
/// directory reports nothing rather than failing the settings snapshot.
pub fn scan_backup_files(data_directory: &Path) -> Vec<DataBackupFile> {
    let Ok(entries) = fs::read_dir(data_directory) else {
        return Vec::new();
    };
    let mut files: Vec<DataBackupFile> = entries
        .filter_map(Result::ok)
        .filter_map(|entry| {
            let name = entry.file_name().into_string().ok()?;
            if !is_backup_name(&name) {
                return None;
            }
            // DirEntry::metadata does not follow symlinks.
            let metadata = entry.metadata().ok()?;
            if !metadata.is_file() {
                return None;
            }
            let modified_unix = metadata
                .modified()
                .ok()
                .and_then(|time| time.duration_since(UNIX_EPOCH).ok())
                .map(|elapsed| elapsed.as_secs());
            Some(DataBackupFile {
                name,
                size_bytes: metadata.len(),
                modified_unix,
            })
        })
        .collect();
    files.sort_by(|left, right| {
        right
            .modified_unix
            .cmp(&left.modified_unix)
            .then_with(|| left.name.cmp(&right.name))
    });
    files.truncate(MAX_REPORTED_BACKUPS);
    files
}

fn is_backup_name(name: &str) -> bool {
    name.to_ascii_lowercase().contains(".bak")
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::PathBuf;

    fn temporary_directory(name: &str) -> PathBuf {
        let path = std::env::temp_dir().join(format!(
            "astrlink-data-hygiene-{name}-{}",
            std::process::id()
        ));
        let _ = fs::remove_dir_all(&path);
        fs::create_dir_all(&path).unwrap();
        path
    }

    #[test]
    fn reports_backup_files_without_touching_them() {
        let directory = temporary_directory("scan");
        fs::write(directory.join("astrlink.db"), b"live").unwrap();
        fs::write(directory.join("astrlink.db-wal"), b"wal").unwrap();
        fs::write(directory.join("desktop-preferences.json"), b"{}").unwrap();
        fs::write(
            directory.join("astrlink.db.bak-20260924-migration-34-repair"),
            b"0123456789",
        )
        .unwrap();
        fs::write(directory.join("notes.BAK"), b"xy").unwrap();
        fs::create_dir_all(directory.join("old.bak.d")).unwrap();
        #[cfg(unix)]
        std::os::unix::fs::symlink(directory.join("astrlink.db"), directory.join("link.db.bak"))
            .unwrap();

        let files = scan_backup_files(&directory);
        let mut names: Vec<&str> = files.iter().map(|file| file.name.as_str()).collect();
        names.sort_unstable();
        assert_eq!(
            names,
            ["astrlink.db.bak-20260924-migration-34-repair", "notes.BAK"]
        );
        let repair = files
            .iter()
            .find(|file| file.name.starts_with("astrlink.db.bak"))
            .unwrap();
        assert_eq!(repair.size_bytes, 10);
        assert!(repair.modified_unix.is_some());
        assert!(directory
            .join("astrlink.db.bak-20260924-migration-34-repair")
            .is_file());
        fs::remove_dir_all(directory).unwrap();
    }

    #[test]
    fn a_missing_directory_reports_nothing() {
        let directory = temporary_directory("missing").join("absent");
        assert!(scan_backup_files(&directory).is_empty());
    }
}
