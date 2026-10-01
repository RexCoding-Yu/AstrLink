//! Keep the local key that wraps AstrLink's data keys in the macOS login
//! keychain on signed builds (plan §5.2). The desktop reads the key before each
//! Core start and injects it on stdin, so no key file sits beside the database.
//! Everywhere else — Windows, Linux, ad-hoc signed and `make dev` builds — Core
//! keeps `<data-dir>/local.key` itself.

// The keychain flow runs only on macOS, but its tests run on every platform.
#![cfg_attr(not(target_os = "macos"), allow(dead_code))]

use std::{
    fs,
    io::{self, Read},
    path::{Path, PathBuf},
    sync::Mutex,
};

use serde::Serialize;
use sha2::{Digest, Sha256};
use zeroize::Zeroizing;

/// Keychain service of every local key entry.
pub const KEYCHAIN_SERVICE: &str = "com.astrlink.desktop.local-key";
/// Core's default key file inside the data directory.
const KEY_FILE_NAME: &str = "local.key";
/// Key file Core uses while the keychain cannot be read. It stays apart from
/// `local.key` so a key made during an outage is never mistaken for one that
/// arrived with a copied directory and moved into the keychain.
const FALLBACK_KEY_FILE_NAME: &str = "local.fallback.key";
const KEY_BYTES: usize = 32;
/// Bounds a key file read: 64 hex digits plus line endings, as Core allows.
const MAX_KEY_FILE_BYTES: u64 = 256;

/// Where Core's local key lives, shown as one status line in Settings.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum LocalKeyStorage {
    /// The login keychain; the desktop injects the key on stdin.
    Keychain,
    /// A key file Core manages, as on every build that does not use the
    /// keychain.
    File,
    /// A signed build that could not read or write the keychain and fell back
    /// to a key file.
    KeychainUnavailable,
}

/// Secret storage the key moves through, so tests never touch the real
/// keychain.
pub trait KeyVault {
    /// Returns the stored value, `None` when there is no entry.
    fn get(&self, account: &str) -> Result<Option<Zeroizing<Vec<u8>>>, String>;
    /// Creates or replaces the entry.
    fn set(&self, account: &str, value: &[u8]) -> Result<(), String>;
}

/// How the next Core start receives its local key.
pub struct ResolvedKey {
    pub storage: LocalKeyStorage,
    /// The key as 64 lowercase hex digits for Core's stdin.
    pub stdin_hex: Option<Zeroizing<String>>,
    /// An explicit key file for Core; `None` keeps Core's default.
    pub key_file: Option<PathBuf>,
}

impl ResolvedKey {
    fn core_default(storage: LocalKeyStorage) -> Self {
        Self {
            storage,
            stdin_hex: None,
            key_file: None,
        }
    }

    fn keychain(key: &[u8]) -> Self {
        Self {
            storage: LocalKeyStorage::Keychain,
            stdin_hex: Some(encode_hex(key)),
            key_file: None,
        }
    }

    /// Core keeps the fallback file an outage start gave it.
    fn fallback_file(path: PathBuf) -> Self {
        Self {
            storage: LocalKeyStorage::KeychainUnavailable,
            stdin_hex: None,
            key_file: Some(path),
        }
    }
}

/// Resolves the key for a Core start. Only signed macOS builds use the
/// keychain; resolutions are serialized so two starts never both generate.
pub fn resolve_for_start(data_directory: &Path) -> ResolvedKey {
    static RESOLVING: Mutex<()> = Mutex::new(());
    let _guard = RESOLVING
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner());
    #[cfg(target_os = "macos")]
    if keychain_enabled() {
        return resolve(
            &MacKeychain(KEYCHAIN_SERVICE),
            data_directory,
            &mut |line| eprintln!("{line}"),
        );
    }
    let _ = data_directory;
    ResolvedKey::core_default(LocalKeyStorage::File)
}

/// The keychain account of a data directory: the first 16 hex digits of the
/// SHA-256 of its absolute path, so several data directories on one Mac keep
/// separate entries.
pub fn keychain_account(data_directory: &Path) -> String {
    let digest = Sha256::digest(data_directory.as_os_str().as_encoded_bytes());
    digest[..8]
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}

/// Applies the §5.2 flow against `vault`. It never deletes or overwrites a key
/// that might still open data: a failure at any step leaves the keychain and
/// the key files as they were and lets Core use a key file.
pub fn resolve(
    vault: &dyn KeyVault,
    data_directory: &Path,
    log: &mut dyn FnMut(&str),
) -> ResolvedKey {
    let account = keychain_account(data_directory);
    let key_file = data_directory.join(KEY_FILE_NAME);
    let fallback_file = data_directory.join(FALLBACK_KEY_FILE_NAME);
    let stored = match vault.get(&account) {
        Ok(Some(value)) => match parse_key(&value) {
            Some(key) => Some(key),
            None => {
                log("AstrLink local key: the keychain entry is not a valid key; using a key file");
                return keychain_unavailable(data_directory, &key_file);
            }
        },
        Ok(None) => None,
        Err(error) => {
            log(&format!(
                "AstrLink local key: the keychain cannot be read ({error}); using a key file"
            ));
            return keychain_unavailable(data_directory, &key_file);
        }
    };
    let file_key = match read_key_file(&key_file) {
        Ok(key) => key,
        Err(error) => {
            if stored.is_none() {
                // Core reports the unreadable file; nothing moves.
                log(&format!(
                    "AstrLink local key: {KEY_FILE_NAME} cannot be read ({error}); leaving it for Core"
                ));
                return ResolvedKey::core_default(LocalKeyStorage::File);
            }
            log(&format!(
                "AstrLink local key: ignoring {KEY_FILE_NAME} ({error}); using the keychain"
            ));
            None
        }
    };

    match (stored, file_key) {
        (Some(stored), None) => {
            settle_fallback(&stored, &fallback_file, log);
            ResolvedKey::keychain(&stored)
        }
        (Some(stored), Some(file)) if stored[..] == file[..] => {
            // A migration stopped after the keychain write; finish it.
            remove_key_file(&key_file, log);
            settle_fallback(&stored, &fallback_file, log);
            ResolvedKey::keychain(&stored)
        }
        (Some(stored), Some(_)) => {
            // A keychain hit is used as is (§5.2): a file anyone who can write
            // the data directory could have placed never replaces it. The
            // file stays for the user to recover a copied directory with.
            log(&format!(
                "AstrLink local key: {KEY_FILE_NAME} differs from the keychain entry; using the keychain and leaving the file"
            ));
            settle_fallback(&stored, &fallback_file, log);
            ResolvedKey::keychain(&stored)
        }
        (None, Some(file)) => {
            // Core used local.key over the fallback file whenever both were
            // there, so local.key holds the current key.
            let resolved = store_file_key(
                vault,
                &account,
                &file,
                &key_file,
                ResolvedKey::core_default(LocalKeyStorage::KeychainUnavailable),
                log,
            );
            if resolved.storage == LocalKeyStorage::Keychain {
                settle_fallback(&file, &fallback_file, log);
            }
            resolved
        }
        (None, None) => match read_key_file(&fallback_file) {
            // An outage start left its key in the fallback file and Core
            // wrapped the data keys under it: that key moves in, never a new
            // one that would orphan them.
            Ok(Some(fallback)) => store_file_key(
                vault,
                &account,
                &fallback,
                &fallback_file,
                ResolvedKey::fallback_file(fallback_file.clone()),
                log,
            ),
            Err(error) => {
                log(&format!(
                    "AstrLink local key: {FALLBACK_KEY_FILE_NAME} cannot be read ({error}); leaving it for Core"
                ));
                ResolvedKey::fallback_file(fallback_file)
            }
            Ok(None) => {
                let mut key = Zeroizing::new([0_u8; KEY_BYTES]);
                if let Err(error) = getrandom::getrandom(key.as_mut()) {
                    log(&format!(
                        "AstrLink local key: cannot generate a key ({error}); Core will create {KEY_FILE_NAME}"
                    ));
                    return ResolvedKey::core_default(LocalKeyStorage::KeychainUnavailable);
                }
                if let Err(error) = write_verified(vault, &account, key.as_ref()) {
                    log(&format!(
                        "AstrLink local key: cannot write the keychain ({error}); Core will create {KEY_FILE_NAME}"
                    ));
                    return ResolvedKey::core_default(LocalKeyStorage::KeychainUnavailable);
                }
                ResolvedKey::keychain(key.as_ref())
            }
        },
    }
}

/// Moves a key file into the keychain and deletes the file once the entry
/// reads back. While the keychain refuses, Core keeps `if_refused`.
fn store_file_key(
    vault: &dyn KeyVault,
    account: &str,
    key: &[u8],
    key_file: &Path,
    if_refused: ResolvedKey,
    log: &mut dyn FnMut(&str),
) -> ResolvedKey {
    if let Err(error) = write_verified(vault, account, key) {
        log(&format!(
            "AstrLink local key: cannot move {} into the keychain ({error}); using the file",
            file_name(key_file)
        ));
        return if_refused;
    }
    remove_key_file(key_file, log);
    ResolvedKey::keychain(key)
}

/// Settles a fallback file beside the key the keychain holds. The same key
/// means a move stopped after the keychain write, so the file goes; another
/// key never replaces the entry and the file stays for recovery.
fn settle_fallback(key: &[u8], fallback_file: &Path, log: &mut dyn FnMut(&str)) {
    match read_key_file(fallback_file) {
        Ok(None) => {}
        Ok(Some(fallback)) if fallback[..] == key[..] => remove_key_file(fallback_file, log),
        Ok(Some(_)) => log(&format!(
            "AstrLink local key: {FALLBACK_KEY_FILE_NAME} differs from the keychain entry; using the keychain and leaving the file"
        )),
        Err(error) => log(&format!(
            "AstrLink local key: ignoring {FALLBACK_KEY_FILE_NAME} ({error}); using the keychain"
        )),
    }
}

fn write_verified(vault: &dyn KeyVault, account: &str, key: &[u8]) -> Result<(), String> {
    vault.set(account, encode_hex(key).as_bytes())?;
    match vault.get(account)?.and_then(|value| parse_key(&value)) {
        Some(stored) if stored[..] == key[..] => Ok(()),
        _ => Err("the entry did not read back".into()),
    }
}

/// While the keychain cannot be read, Core keeps using a key file. An existing
/// `local.key` is still the best guess; otherwise an existing data directory
/// gets the separate fallback file so the outage never replaces the entry. The
/// first start that finds no entry moves the fallback key in.
fn keychain_unavailable(data_directory: &Path, key_file: &Path) -> ResolvedKey {
    let mut resolved = ResolvedKey::core_default(LocalKeyStorage::KeychainUnavailable);
    if !key_file.exists() && data_directory.is_dir() {
        resolved.key_file = Some(data_directory.join(FALLBACK_KEY_FILE_NAME));
    }
    resolved
}

fn read_key_file(path: &Path) -> io::Result<Option<Zeroizing<Vec<u8>>>> {
    let file = match fs::File::open(path) {
        Ok(file) => file,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(error),
    };
    if !file.metadata()?.is_file() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "not a regular file",
        ));
    }
    let mut content = Zeroizing::new(Vec::new());
    file.take(MAX_KEY_FILE_BYTES + 1)
        .read_to_end(&mut content)?;
    if content.len() as u64 > MAX_KEY_FILE_BYTES {
        return Err(io::Error::new(io::ErrorKind::InvalidData, "file too large"));
    }
    parse_key(&content)
        .map(Some)
        .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidData, "not 64 hex digits"))
}

fn file_name(path: &Path) -> std::borrow::Cow<'_, str> {
    path.file_name().unwrap_or_default().to_string_lossy()
}

fn remove_key_file(path: &Path, log: &mut dyn FnMut(&str)) {
    if let Err(error) = fs::remove_file(path) {
        if error.kind() != io::ErrorKind::NotFound {
            log(&format!(
                "AstrLink local key: cannot delete {} after moving it into the keychain ({error})",
                file_name(path)
            ));
        }
        return;
    }
    // Windows cannot open a directory as a file to sync it.
    if !cfg!(unix) {
        return;
    }
    if let Some(directory) = path.parent() {
        if let Err(error) = fs::File::open(directory).and_then(|directory| directory.sync_all()) {
            log(&format!(
                "AstrLink local key: cannot sync the data directory ({error})"
            ));
        }
    }
}

/// Decodes 64 hex digits, ignoring surrounding whitespace as Core does.
fn parse_key(text: &[u8]) -> Option<Zeroizing<Vec<u8>>> {
    let start = text
        .iter()
        .position(|byte| !byte.is_ascii_whitespace())
        .unwrap_or(text.len());
    let end = text
        .iter()
        .rposition(|byte| !byte.is_ascii_whitespace())
        .map_or(start, |last| last + 1);
    let text = &text[start..end];
    if text.len() != KEY_BYTES * 2 {
        return None;
    }
    let mut key = Zeroizing::new(vec![0_u8; KEY_BYTES]);
    for (index, pair) in text.chunks_exact(2).enumerate() {
        key[index] = (hex_digit(pair[0])? << 4) | hex_digit(pair[1])?;
    }
    Some(key)
}

fn hex_digit(character: u8) -> Option<u8> {
    match character {
        b'0'..=b'9' => Some(character - b'0'),
        b'a'..=b'f' => Some(character - b'a' + 10),
        b'A'..=b'F' => Some(character - b'A' + 10),
        _ => None,
    }
}

fn encode_hex(key: &[u8]) -> Zeroizing<String> {
    const DIGITS: &[u8; 16] = b"0123456789abcdef";
    let mut encoded = Zeroizing::new(String::with_capacity(key.len() * 2));
    for byte in key {
        encoded.push(DIGITS[usize::from(byte >> 4)] as char);
        encoded.push(DIGITS[usize::from(byte & 0x0f)] as char);
    }
    encoded
}

/// Signed builds running from their bundle keep the key in the keychain. Debug
/// and `make dev` builds, and ad-hoc signatures, keep the key file so a rebuild
/// never raises a keychain prompt.
#[cfg(target_os = "macos")]
pub(crate) fn keychain_enabled() -> bool {
    use security_framework::os::macos::code_signing::{Flags, SecCode, SecRequirement};

    if cfg!(debug_assertions) || cfg!(dev) {
        return false;
    }
    let Ok(executable) = std::env::current_exe() else {
        return false;
    };
    if crate::macos_app::bundle_for_executable(&executable).is_none() {
        return false;
    }
    // An ad-hoc signature has no certificate chain to anchor.
    let Ok(requirement) = "anchor apple generic".parse::<SecRequirement>() else {
        return false;
    };
    SecCode::for_self(Flags::NONE)
        .and_then(|code| code.check_validity(Flags::NONE, &requirement))
        .is_ok()
}

/// The login keychain entries of one service.
#[cfg(target_os = "macos")]
pub(crate) struct MacKeychain(pub(crate) &'static str);

#[cfg(target_os = "macos")]
impl KeyVault for MacKeychain {
    fn get(&self, account: &str) -> Result<Option<Zeroizing<Vec<u8>>>, String> {
        use security_framework::passwords::{generic_password, PasswordOptions};

        // errSecItemNotFound
        const ITEM_NOT_FOUND: i32 = -25300;
        match generic_password(PasswordOptions::new_generic_password(self.0, account)) {
            Ok(value) => Ok(Some(Zeroizing::new(value))),
            Err(error) if error.code() == ITEM_NOT_FOUND => Ok(None),
            Err(error) => Err(format!("OSStatus {}", error.code())),
        }
    }

    fn set(&self, account: &str, value: &[u8]) -> Result<(), String> {
        security_framework::passwords::set_generic_password(self.0, account, value)
            .map_err(|error| format!("OSStatus {}", error.code()))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::{
        cell::{Cell, RefCell},
        collections::BTreeMap,
    };

    const FILE_KEY_HEX: &str = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90";
    const STORED_KEY_HEX: &str = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0";

    #[derive(Default)]
    struct FakeVault {
        entries: RefCell<BTreeMap<String, Vec<u8>>>,
        fail_get: bool,
        fail_set: bool,
        /// Accepts writes without keeping them, like a keychain that drops them.
        drop_writes: bool,
        sets: Cell<usize>,
    }

    impl FakeVault {
        fn with(account: &str, value: &str) -> Self {
            let vault = Self::default();
            vault
                .entries
                .borrow_mut()
                .insert(account.to_string(), value.as_bytes().to_vec());
            vault
        }

        fn value(&self, account: &str) -> Option<String> {
            self.entries
                .borrow()
                .get(account)
                .map(|value| String::from_utf8(value.clone()).unwrap())
        }
    }

    impl KeyVault for FakeVault {
        fn get(&self, account: &str) -> Result<Option<Zeroizing<Vec<u8>>>, String> {
            if self.fail_get {
                return Err("OSStatus -25308".into());
            }
            Ok(self
                .entries
                .borrow()
                .get(account)
                .map(|value| Zeroizing::new(value.clone())))
        }

        fn set(&self, account: &str, value: &[u8]) -> Result<(), String> {
            self.sets.set(self.sets.get() + 1);
            if self.fail_set {
                return Err("OSStatus -128".into());
            }
            if !self.drop_writes {
                self.entries
                    .borrow_mut()
                    .insert(account.to_string(), value.to_vec());
            }
            Ok(())
        }
    }

    struct Case {
        _root: TempDir,
        data: PathBuf,
        logs: Vec<String>,
    }

    impl Case {
        fn new() -> Self {
            let root = TempDir::new();
            let data = root.0.join("data");
            fs::create_dir(&data).unwrap();
            Self {
                _root: root,
                data,
                logs: Vec::new(),
            }
        }

        fn account(&self) -> String {
            keychain_account(&self.data)
        }

        fn key_file(&self) -> PathBuf {
            self.data.join(KEY_FILE_NAME)
        }

        fn write_key_file(&self, content: &str) {
            fs::write(self.key_file(), content).unwrap();
        }

        fn fallback_file(&self) -> PathBuf {
            self.data.join(FALLBACK_KEY_FILE_NAME)
        }

        fn write_fallback_file(&self, content: &str) {
            fs::write(self.fallback_file(), content).unwrap();
        }

        fn resolve(&mut self, vault: &FakeVault) -> ResolvedKey {
            let logs = &mut self.logs;
            resolve(vault, &self.data, &mut |line| logs.push(line.to_string()))
        }

        fn assert_logs_hold_no_key(&self) {
            for line in &self.logs {
                for key in [FILE_KEY_HEX, STORED_KEY_HEX] {
                    assert!(
                        !line.to_ascii_lowercase().contains(&key[..16]),
                        "log line carries key material: {line}"
                    );
                }
            }
        }
    }

    /// A temporary directory, removed on drop.
    struct TempDir(PathBuf);

    impl TempDir {
        fn new() -> Self {
            let mut random = [0_u8; 8];
            getrandom::getrandom(&mut random).unwrap();
            let path = std::env::temp_dir().join(format!(
                "astrlink-kek-store-{}",
                encode_hex(&random).as_str()
            ));
            fs::create_dir(&path).unwrap();
            Self(path)
        }
    }

    impl Drop for TempDir {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }

    fn stdin_hex(resolved: &ResolvedKey) -> Option<&str> {
        resolved.stdin_hex.as_ref().map(|hex| hex.as_str())
    }

    #[test]
    fn keychain_hit_injects_the_stored_key() {
        let mut case = Case::new();
        let vault = FakeVault::with(&case.account(), STORED_KEY_HEX);
        let resolved = case.resolve(&vault);
        assert_eq!(resolved.storage, LocalKeyStorage::Keychain);
        assert_eq!(stdin_hex(&resolved), Some(STORED_KEY_HEX));
        assert_eq!(resolved.key_file, None);
        assert_eq!(vault.sets.get(), 0);
        assert!(!case.key_file().exists());
        assert!(case.logs.is_empty(), "{:?}", case.logs);
    }

    #[test]
    fn key_file_moves_into_the_keychain_and_is_deleted() {
        let mut case = Case::new();
        case.write_key_file(&format!("{}\n", FILE_KEY_HEX.to_ascii_uppercase()));
        let vault = FakeVault::default();
        let resolved = case.resolve(&vault);
        assert_eq!(resolved.storage, LocalKeyStorage::Keychain);
        assert_eq!(stdin_hex(&resolved), Some(FILE_KEY_HEX));
        assert_eq!(vault.value(&case.account()).as_deref(), Some(FILE_KEY_HEX));
        assert!(!case.key_file().exists(), "local.key survived the move");
        assert!(case.logs.is_empty(), "{:?}", case.logs);
    }

    #[test]
    fn interrupted_move_finishes_by_deleting_the_matching_file() {
        let mut case = Case::new();
        case.write_key_file(FILE_KEY_HEX);
        let vault = FakeVault::with(&case.account(), FILE_KEY_HEX);
        let resolved = case.resolve(&vault);
        assert_eq!(resolved.storage, LocalKeyStorage::Keychain);
        assert_eq!(stdin_hex(&resolved), Some(FILE_KEY_HEX));
        assert_eq!(vault.sets.get(), 0);
        assert!(!case.key_file().exists());
    }

    #[test]
    fn first_start_generates_a_keychain_key_without_a_file() {
        let mut case = Case::new();
        let vault = FakeVault::default();
        let resolved = case.resolve(&vault);
        assert_eq!(resolved.storage, LocalKeyStorage::Keychain);
        let stored = vault.value(&case.account()).expect("no keychain entry");
        assert_eq!(stored.len(), 64);
        assert!(stored
            .bytes()
            .all(|byte| byte.is_ascii_hexdigit() && !byte.is_ascii_uppercase()));
        assert_eq!(stdin_hex(&resolved), Some(stored.as_str()));
        assert!(!case.key_file().exists());

        // The next start reads the same key back.
        let again = case.resolve(&vault);
        assert_eq!(stdin_hex(&again), Some(stored.as_str()));
    }

    #[test]
    fn failed_keychain_write_lets_core_create_the_file() {
        let mut case = Case::new();
        let vault = FakeVault {
            fail_set: true,
            ..FakeVault::default()
        };
        let resolved = case.resolve(&vault);
        assert_eq!(resolved.storage, LocalKeyStorage::KeychainUnavailable);
        assert_eq!(stdin_hex(&resolved), None);
        assert_eq!(resolved.key_file, None, "Core's default local.key");
        assert!(!case.key_file().exists(), "the desktop wrote a key file");
        assert_eq!(case.logs.len(), 1);
        case.assert_logs_hold_no_key();

        // Once the keychain works again the file Core made moves in.
        case.write_key_file(FILE_KEY_HEX);
        let vault = FakeVault::default();
        let resolved = case.resolve(&vault);
        assert_eq!(resolved.storage, LocalKeyStorage::Keychain);
        assert_eq!(stdin_hex(&resolved), Some(FILE_KEY_HEX));
        assert!(!case.key_file().exists());
    }

    #[test]
    fn failed_or_dropped_write_keeps_the_key_file() {
        for vault in [
            FakeVault {
                fail_set: true,
                ..FakeVault::default()
            },
            FakeVault {
                drop_writes: true,
                ..FakeVault::default()
            },
        ] {
            let mut case = Case::new();
            case.write_key_file(FILE_KEY_HEX);
            let resolved = case.resolve(&vault);
            assert_eq!(resolved.storage, LocalKeyStorage::KeychainUnavailable);
            assert_eq!(stdin_hex(&resolved), None);
            assert_eq!(resolved.key_file, None);
            assert_eq!(fs::read_to_string(case.key_file()).unwrap(), FILE_KEY_HEX);
            case.assert_logs_hold_no_key();
        }
    }

    #[test]
    fn unreadable_keychain_uses_a_separate_fallback_file() {
        let mut case = Case::new();
        let vault = FakeVault {
            fail_get: true,
            ..FakeVault::with(&case.account(), STORED_KEY_HEX)
        };
        let resolved = case.resolve(&vault);
        assert_eq!(resolved.storage, LocalKeyStorage::KeychainUnavailable);
        assert_eq!(stdin_hex(&resolved), None);
        assert_eq!(
            resolved.key_file,
            Some(case.data.join(FALLBACK_KEY_FILE_NAME))
        );
        assert_eq!(vault.sets.get(), 0);
        case.assert_logs_hold_no_key();

        // An existing local.key is still used as Core's default.
        case.write_key_file(FILE_KEY_HEX);
        let resolved = case.resolve(&vault);
        assert_eq!(resolved.key_file, None);
        assert_eq!(fs::read_to_string(case.key_file()).unwrap(), FILE_KEY_HEX);

        // Before the data directory exists there is no data to protect.
        let missing = case.data.join("not-yet");
        let resolved = resolve(&vault, &missing, &mut |_| {});
        assert_eq!(resolved.key_file, None);
    }

    #[test]
    fn an_outage_key_moves_into_the_keychain_once_it_recovers() {
        // The keychain cannot be read and has no entry: Core gets the
        // fallback file and creates its key there.
        let mut case = Case::new();
        let outage = FakeVault {
            fail_get: true,
            ..FakeVault::default()
        };
        let resolved = case.resolve(&outage);
        assert_eq!(resolved.key_file, Some(case.fallback_file()));
        case.write_fallback_file(FILE_KEY_HEX);
        case.logs.clear();

        // The keychain works again: the fallback key moves in, no new key.
        let vault = FakeVault::default();
        let resolved = case.resolve(&vault);
        assert_eq!(resolved.storage, LocalKeyStorage::Keychain);
        assert_eq!(stdin_hex(&resolved), Some(FILE_KEY_HEX));
        assert_eq!(resolved.key_file, None);
        assert_eq!(vault.sets.get(), 1);
        assert_eq!(vault.value(&case.account()).as_deref(), Some(FILE_KEY_HEX));
        assert!(
            !case.fallback_file().exists(),
            "the fallback file survived the move"
        );
        assert!(!case.key_file().exists());
        assert!(case.logs.is_empty(), "{:?}", case.logs);

        let again = case.resolve(&vault);
        assert_eq!(stdin_hex(&again), Some(FILE_KEY_HEX));
        assert_eq!(vault.sets.get(), 1);
    }

    #[test]
    fn a_refused_move_keeps_core_on_the_fallback_file() {
        for vault in [
            FakeVault {
                fail_set: true,
                ..FakeVault::default()
            },
            FakeVault {
                drop_writes: true,
                ..FakeVault::default()
            },
        ] {
            let mut case = Case::new();
            case.write_fallback_file(FILE_KEY_HEX);
            let resolved = case.resolve(&vault);
            assert_eq!(resolved.storage, LocalKeyStorage::KeychainUnavailable);
            assert_eq!(stdin_hex(&resolved), None);
            assert_eq!(resolved.key_file, Some(case.fallback_file()));
            assert_eq!(
                fs::read_to_string(case.fallback_file()).unwrap(),
                FILE_KEY_HEX
            );
            assert!(
                !case.key_file().exists(),
                "Core was sent to a new local.key"
            );
            case.assert_logs_hold_no_key();
        }

        // A damaged fallback file is left for Core to report; nothing moves.
        let mut case = Case::new();
        case.write_fallback_file(&FILE_KEY_HEX[..40]);
        let vault = FakeVault::default();
        let resolved = case.resolve(&vault);
        assert_eq!(resolved.key_file, Some(case.fallback_file()));
        assert_eq!(vault.sets.get(), 0);
        assert!(case.fallback_file().exists());
    }

    #[test]
    fn the_keychain_entry_wins_over_a_fallback_file() {
        // The same key: a move stopped after the keychain write.
        let mut case = Case::new();
        case.write_fallback_file(STORED_KEY_HEX);
        let vault = FakeVault::with(&case.account(), STORED_KEY_HEX);
        let resolved = case.resolve(&vault);
        assert_eq!(stdin_hex(&resolved), Some(STORED_KEY_HEX));
        assert!(!case.fallback_file().exists());
        assert!(case.logs.is_empty(), "{:?}", case.logs);

        // Another key never replaces the entry; the file stays.
        let mut case = Case::new();
        case.write_fallback_file(FILE_KEY_HEX);
        let vault = FakeVault::with(&case.account(), STORED_KEY_HEX);
        let resolved = case.resolve(&vault);
        assert_eq!(resolved.storage, LocalKeyStorage::Keychain);
        assert_eq!(stdin_hex(&resolved), Some(STORED_KEY_HEX));
        assert_eq!(vault.sets.get(), 0);
        assert_eq!(
            fs::read_to_string(case.fallback_file()).unwrap(),
            FILE_KEY_HEX
        );
        assert_eq!(case.logs.len(), 1, "{:?}", case.logs);
        case.assert_logs_hold_no_key();

        // With local.key and the fallback file both there, Core has been
        // using local.key: it moves in and the other file stays.
        let mut case = Case::new();
        case.write_key_file(FILE_KEY_HEX);
        case.write_fallback_file(STORED_KEY_HEX);
        let vault = FakeVault::default();
        let resolved = case.resolve(&vault);
        assert_eq!(stdin_hex(&resolved), Some(FILE_KEY_HEX));
        assert!(!case.key_file().exists());
        assert_eq!(
            fs::read_to_string(case.fallback_file()).unwrap(),
            STORED_KEY_HEX
        );
        case.assert_logs_hold_no_key();
    }

    #[test]
    fn a_different_key_file_never_replaces_the_entry() {
        let mut case = Case::new();
        case.write_key_file(FILE_KEY_HEX);
        let account = case.account();
        let vault = FakeVault::with(&account, STORED_KEY_HEX);
        let resolved = case.resolve(&vault);
        assert_eq!(resolved.storage, LocalKeyStorage::Keychain);
        assert_eq!(stdin_hex(&resolved), Some(STORED_KEY_HEX));
        assert_eq!(resolved.key_file, None);
        assert_eq!(vault.sets.get(), 0);
        assert_eq!(vault.value(&account).as_deref(), Some(STORED_KEY_HEX));
        assert_eq!(vault.entries.borrow().len(), 1);
        assert_eq!(fs::read_to_string(case.key_file()).unwrap(), FILE_KEY_HEX);
        assert_eq!(case.logs.len(), 1, "{:?}", case.logs);
        case.assert_logs_hold_no_key();
    }

    #[test]
    fn invalid_entries_and_files_are_never_overwritten() {
        // A damaged keychain entry: use a file, leave the entry.
        let mut case = Case::new();
        let account = case.account();
        let vault = FakeVault::with(&account, "not a key");
        let resolved = case.resolve(&vault);
        assert_eq!(resolved.storage, LocalKeyStorage::KeychainUnavailable);
        assert_eq!(vault.sets.get(), 0);
        assert_eq!(vault.value(&account).as_deref(), Some("not a key"));

        // A damaged key file and no entry: Core reports it, nothing moves.
        let mut case = Case::new();
        case.write_key_file(&FILE_KEY_HEX[..40]);
        let vault = FakeVault::default();
        let resolved = case.resolve(&vault);
        assert_eq!(resolved.storage, LocalKeyStorage::File);
        assert_eq!(stdin_hex(&resolved), None);
        assert_eq!(vault.sets.get(), 0);
        assert_eq!(
            fs::read_to_string(case.key_file()).unwrap(),
            &FILE_KEY_HEX[..40]
        );

        // A damaged key file beside a good entry: the entry wins, the file stays.
        let mut case = Case::new();
        case.write_key_file("zz");
        let vault = FakeVault::with(&case.account(), STORED_KEY_HEX);
        let resolved = case.resolve(&vault);
        assert_eq!(stdin_hex(&resolved), Some(STORED_KEY_HEX));
        assert!(case.key_file().exists());
        case.assert_logs_hold_no_key();
    }

    #[test]
    fn keychain_account_is_stable_short_and_per_directory() {
        let first = keychain_account(Path::new("/Users/example/Library/Application Support/a"));
        assert_eq!(first.len(), 16);
        assert!(first
            .bytes()
            .all(|byte| byte.is_ascii_hexdigit() && !byte.is_ascii_uppercase()));
        assert_eq!(
            first,
            keychain_account(Path::new("/Users/example/Library/Application Support/a"))
        );
        assert_ne!(
            first,
            keychain_account(Path::new("/Users/example/Library/Application Support/b"))
        );
        // SHA-256("/") begins 8a5edab282632443.
        assert_eq!(keychain_account(Path::new("/")), "8a5edab282632443");
    }

    #[test]
    fn debug_builds_never_use_the_keychain() {
        let case = Case::new();
        let resolved = resolve_for_start(&case.data);
        assert_eq!(resolved.storage, LocalKeyStorage::File);
        assert_eq!(stdin_hex(&resolved), None);
        assert_eq!(resolved.key_file, None);
        #[cfg(target_os = "macos")]
        assert!(!keychain_enabled());
    }

    #[test]
    fn storage_serializes_as_snake_case() {
        assert_eq!(
            serde_json::to_string(&LocalKeyStorage::KeychainUnavailable).unwrap(),
            "\"keychain_unavailable\""
        );
        assert_eq!(
            serde_json::to_string(&LocalKeyStorage::Keychain).unwrap(),
            "\"keychain\""
        );
    }

    #[test]
    fn hex_round_trips_and_rejects_bad_digits() {
        let key = parse_key(format!("  {FILE_KEY_HEX}\r\n").as_bytes()).unwrap();
        assert_eq!(encode_hex(&key).as_str(), FILE_KEY_HEX);
        assert!(parse_key(&[b'g'; 64]).is_none());
        assert!(parse_key(&FILE_KEY_HEX.as_bytes()[..62]).is_none());
    }
}
