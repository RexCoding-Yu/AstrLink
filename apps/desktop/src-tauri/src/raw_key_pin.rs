//! Pin the raw public key a data directory uses, so a raw key replaced outside
//! the desktop is noticed. `astrlink-core raw-password` run while the app is
//! closed needs no proof to reset the key, and whoever sets a key's password
//! can read the raw content captured under it. The pin lives in the login
//! keychain on signed macOS builds and in an owner-only file in the config
//! directory everywhere else.

use std::{
    collections::BTreeMap,
    fs::{self, OpenOptions},
    io::{self, Write},
    path::{Path, PathBuf},
    sync::atomic::{AtomicU64, Ordering},
};

use serde_json::Value;

/// Keychain service of every raw key pin.
#[cfg(target_os = "macos")]
const KEYCHAIN_SERVICE: &str = "com.astrlink.desktop.raw-key-pin";
/// Pin file in the config directory when the keychain is not used.
const PIN_FILE_NAME: &str = "raw-key-pins.json";
/// Bounds a pin file read; each entry is under 100 bytes.
const MAX_PIN_FILE_BYTES: u64 = 1 << 20;
/// The pin of a data directory whose raw key has no password yet.
const NO_PASSWORD: &str = "none";

static TEMPORARY_SEQUENCE: AtomicU64 = AtomicU64::new(0);

/// What a raw sealing status says about the key, in pinned form.
#[derive(Clone, Debug, PartialEq, Eq)]
enum Observed {
    /// No raw password is set, so no one outside can read raw content yet.
    NoPassword,
    /// The fingerprint of a key that has a password.
    Key(String),
}

impl Observed {
    /// `None` when the status gives no usable verdict, such as from a Core
    /// without key fingerprints.
    fn from_status(status: &Value) -> Option<Self> {
        if !status.get("password_set")?.as_bool()? {
            return Some(Self::NoPassword);
        }
        let fingerprint = status.get("key_fingerprint")?.as_str()?;
        is_fingerprint(fingerprint).then(|| Self::Key(fingerprint.to_string()))
    }

    fn decode(pin: &str) -> Option<Self> {
        if pin == NO_PASSWORD {
            Some(Self::NoPassword)
        } else {
            is_fingerprint(pin).then(|| Self::Key(pin.to_string()))
        }
    }

    fn encode(&self) -> &str {
        match self {
            Self::NoPassword => NO_PASSWORD,
            Self::Key(fingerprint) => fingerprint,
        }
    }
}

fn is_fingerprint(value: &str) -> bool {
    value.len() == 64
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

/// Where pins are kept, so tests never touch the real keychain or config.
pub trait PinStore: Send + Sync {
    /// Returns the pin of `account`, `None` when there is none.
    fn load(&self, account: &str) -> Result<Option<String>, String>;
    /// Creates or replaces the pin of `account`.
    fn save(&self, account: &str, pin: &str) -> Result<(), String>;
    /// The file holding the pins, which agent guards keep agents out of;
    /// `None` for the keychain.
    fn file(&self) -> Option<&Path> {
        None
    }
}

/// What the desktop does with the status a password action returns.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum PinAction {
    /// Compare the status with the pin.
    Check,
    /// Record the key in the status as the pinned one.
    Pin,
}

impl PinAction {
    /// A desktop set or reset gives the key a password the operator just
    /// chose, so it becomes the pinned key. A change keeps the key and its
    /// fingerprint, so it only checks: it must not clear the warning for a key
    /// replaced outside the desktop, whose old password still opens a copy.
    pub fn after_password(action: &str) -> Self {
        match action {
            "set" | "reset" => Self::Pin,
            _ => Self::Check,
        }
    }
}

/// The pins of every data directory plus the lock that keeps a status read
/// and its check, or a password action and its pin, from interleaving.
pub struct RawKeyPins {
    store: Box<dyn PinStore>,
    lock: tokio::sync::Mutex<()>,
}

impl RawKeyPins {
    pub fn new(store: Box<dyn PinStore>) -> Self {
        Self {
            store,
            lock: tokio::sync::Mutex::new(()),
        }
    }

    /// The keychain on signed macOS builds, the config directory elsewhere.
    pub fn system(config_directory: &Path) -> Self {
        #[cfg(target_os = "macos")]
        if crate::kek_store::keychain_enabled() {
            return Self::new(Box::new(KeychainPins(crate::kek_store::MacKeychain(
                KEYCHAIN_SERVICE,
            ))));
        }
        Self::new(Box::new(FilePins::new(
            config_directory.join(PIN_FILE_NAME),
        )))
    }

    /// The pin file where the pins live in one; see [`PinStore::file`].
    pub fn file(&self) -> Option<&Path> {
        self.store.file()
    }

    pub async fn guard(&self) -> tokio::sync::MutexGuard<'_, ()> {
        self.lock.lock().await
    }

    /// Applies `action` to the key in `status` and returns whether it was
    /// replaced outside the desktop. Blocks on the keychain.
    pub fn apply(&self, account: &str, status: &Value, action: PinAction) -> bool {
        match action {
            PinAction::Check => self.replaced(account, status),
            PinAction::Pin => {
                self.pin(account, status);
                false
            }
        }
    }

    /// Whether the key in `status` differs from the pinned one. The first
    /// status seen for a data directory is adopted, so a key that predates
    /// pinning raises no warning. A status without a password keeps the pin:
    /// a key that loses its password, as with a wiped database, is not an
    /// attack, and a password set on it later is still compared.
    fn replaced(&self, account: &str, status: &Value) -> bool {
        let Some(observed) = Observed::from_status(status) else {
            return false;
        };
        match self.store.load(account) {
            Ok(None) => {
                self.save(account, &observed);
                false
            }
            Ok(Some(pin)) => match Observed::decode(&pin) {
                Some(pinned) => pinned != observed && observed != Observed::NoPassword,
                None => {
                    eprintln!("ignoring an invalid raw key pin");
                    false
                }
            },
            Err(error) => {
                eprintln!("unable to read the raw key pin: {error}");
                false
            }
        }
    }

    /// Records the key in `status` as the pinned one.
    fn pin(&self, account: &str, status: &Value) {
        if let Some(observed) = Observed::from_status(status) {
            self.save(account, &observed);
        }
    }

    fn save(&self, account: &str, observed: &Observed) {
        if let Err(error) = self.store.save(account, observed.encode()) {
            eprintln!("unable to save the raw key pin: {error}");
        }
    }
}

#[cfg(target_os = "macos")]
struct KeychainPins(crate::kek_store::MacKeychain);

#[cfg(target_os = "macos")]
impl PinStore for KeychainPins {
    fn load(&self, account: &str) -> Result<Option<String>, String> {
        use crate::kek_store::KeyVault;

        self.0
            .get(account)?
            .map(|value| {
                String::from_utf8(value.to_vec())
                    .map_err(|_| "the raw key pin is not UTF-8".to_string())
            })
            .transpose()
    }

    fn save(&self, account: &str, pin: &str) -> Result<(), String> {
        use crate::kek_store::KeyVault;

        self.0.set(account, pin.as_bytes())
    }
}

/// Pins as one owner-only JSON object from account to pin. Only the desktop
/// reads or writes it.
pub struct FilePins {
    path: PathBuf,
}

impl FilePins {
    pub fn new(path: PathBuf) -> Self {
        Self { path }
    }

    fn read(&self) -> Result<BTreeMap<String, String>, String> {
        let file = match fs::File::open(&self.path) {
            Ok(file) => file,
            Err(error) if error.kind() == io::ErrorKind::NotFound => {
                return Ok(BTreeMap::new());
            }
            Err(error) => return Err(format!("{}: {error}", self.path.display())),
        };
        let mut bytes = Vec::new();
        io::Read::read_to_end(&mut io::Read::take(file, MAX_PIN_FILE_BYTES), &mut bytes)
            .map_err(|error| format!("{}: {error}", self.path.display()))?;
        // An unreadable file is reported rather than replaced, so a damaged
        // pin never turns into a fresh adoption of whatever key is there.
        serde_json::from_slice(&bytes).map_err(|error| format!("{}: {error}", self.path.display()))
    }

    fn write(&self, pins: &BTreeMap<String, String>) -> Result<(), String> {
        let parent = self
            .path
            .parent()
            .ok_or_else(|| format!("{} has no parent directory", self.path.display()))?;
        fs::create_dir_all(parent).map_err(|error| format!("{}: {error}", parent.display()))?;
        let mut bytes = serde_json::to_vec_pretty(pins).map_err(|error| error.to_string())?;
        bytes.push(b'\n');
        let temporary = self.path.with_extension(format!(
            "tmp-{}-{}",
            std::process::id(),
            TEMPORARY_SEQUENCE.fetch_add(1, Ordering::Relaxed)
        ));
        let result = (|| {
            let mut options = OpenOptions::new();
            options.create_new(true).write(true);
            #[cfg(unix)]
            {
                use std::os::unix::fs::OpenOptionsExt;
                options.mode(0o600);
            }
            let mut file = options.open(&temporary)?;
            file.write_all(&bytes)?;
            file.sync_all()?;
            crate::preferences::atomic_replace(&temporary, &self.path)?;
            #[cfg(unix)]
            fs::File::open(parent)?.sync_all()?;
            Ok::<(), io::Error>(())
        })();
        if result.is_err() {
            let _ = fs::remove_file(&temporary);
        }
        result.map_err(|error| format!("{}: {error}", self.path.display()))
    }
}

impl PinStore for FilePins {
    fn load(&self, account: &str) -> Result<Option<String>, String> {
        self.read().map(|mut pins| pins.remove(account))
    }

    fn save(&self, account: &str, pin: &str) -> Result<(), String> {
        let mut pins = self.read()?;
        pins.insert(account.to_string(), pin.to_string());
        self.write(&pins)
    }

    fn file(&self) -> Option<&Path> {
        Some(&self.path)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    use std::sync::{Arc, Mutex};

    const ACCOUNT: &str = "0123456789abcdef";
    const KEY_A: &str = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
    const KEY_B: &str = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb";

    #[derive(Clone, Default)]
    struct FakePins {
        entries: Arc<Mutex<BTreeMap<String, String>>>,
        fail_load: bool,
    }

    impl FakePins {
        fn value(&self) -> Option<String> {
            self.entries.lock().unwrap().get(ACCOUNT).cloned()
        }
    }

    impl PinStore for FakePins {
        fn load(&self, account: &str) -> Result<Option<String>, String> {
            if self.fail_load {
                return Err("OSStatus -25308".into());
            }
            Ok(self.entries.lock().unwrap().get(account).cloned())
        }

        fn save(&self, account: &str, pin: &str) -> Result<(), String> {
            self.entries
                .lock()
                .unwrap()
                .insert(account.to_string(), pin.to_string());
            Ok(())
        }
    }

    fn pins() -> (RawKeyPins, FakePins) {
        let store = FakePins::default();
        (RawKeyPins::new(Box::new(store.clone())), store)
    }

    fn no_password() -> Value {
        json!({ "configured": true, "password_set": false, "key_fingerprint": KEY_A })
    }

    fn keyed(fingerprint: &str) -> Value {
        json!({ "configured": true, "password_set": true, "key_fingerprint": fingerprint })
    }

    fn check(pins: &RawKeyPins, status: &Value) -> bool {
        pins.apply(ACCOUNT, status, PinAction::Check)
    }

    #[test]
    fn a_key_that_predates_pinning_is_adopted_once() {
        let (pins, store) = pins();
        assert!(!check(&pins, &keyed(KEY_A)));
        assert_eq!(store.value().as_deref(), Some(KEY_A));
        assert!(!check(&pins, &keyed(KEY_A)));
        assert!(check(&pins, &keyed(KEY_B)));
        // The warning stays until the operator acts on it.
        assert_eq!(store.value().as_deref(), Some(KEY_A));
        assert!(check(&pins, &keyed(KEY_B)));
    }

    #[test]
    fn a_password_set_outside_the_desktop_is_a_replacement() {
        let (pins, store) = pins();
        assert!(!check(&pins, &no_password()));
        assert_eq!(store.value().as_deref(), Some(NO_PASSWORD));
        // The key is the same one; its new password is not the operator's.
        assert!(check(&pins, &keyed(KEY_A)));
    }

    #[test]
    fn desktop_set_and_reset_repin_while_a_change_only_checks() {
        assert_eq!(PinAction::after_password("set"), PinAction::Pin);
        assert_eq!(PinAction::after_password("reset"), PinAction::Pin);
        assert_eq!(PinAction::after_password("change"), PinAction::Check);

        let (pins, store) = pins();
        assert!(!check(&pins, &no_password()));
        assert!(!pins.apply(ACCOUNT, &keyed(KEY_A), PinAction::after_password("set")));
        assert!(!check(&pins, &keyed(KEY_A)));
        assert!(!pins.apply(ACCOUNT, &keyed(KEY_A), PinAction::after_password("change")));
        assert!(!pins.apply(ACCOUNT, &keyed(KEY_B), PinAction::after_password("reset")));
        assert_eq!(store.value().as_deref(), Some(KEY_B));
        assert!(!check(&pins, &keyed(KEY_B)));
    }

    #[test]
    fn a_change_does_not_clear_a_replacement() {
        let (pins, _) = pins();
        assert!(!check(&pins, &keyed(KEY_A)));
        assert!(pins.apply(ACCOUNT, &keyed(KEY_B), PinAction::after_password("change")));
        assert!(check(&pins, &keyed(KEY_B)));
    }

    #[test]
    fn acknowledging_a_replaced_key_repins_it() {
        let (pins, store) = pins();
        assert!(!check(&pins, &keyed(KEY_A)));
        assert!(check(&pins, &keyed(KEY_B)));
        assert!(!pins.apply(ACCOUNT, &keyed(KEY_B), PinAction::Pin));
        assert_eq!(store.value().as_deref(), Some(KEY_B));
        assert!(!check(&pins, &keyed(KEY_B)));
    }

    #[test]
    fn a_status_without_a_password_keeps_the_pin() {
        let (pins, store) = pins();
        assert!(!check(&pins, &keyed(KEY_A)));
        assert!(!check(&pins, &no_password()));
        assert_eq!(store.value().as_deref(), Some(KEY_A));
        assert!(check(&pins, &keyed(KEY_B)));
    }

    #[test]
    fn statuses_without_a_verdict_leave_the_pin_alone() {
        let (pins, store) = pins();
        for status in [
            json!({ "configured": true, "password_set": true }),
            json!({ "configured": true, "password_set": true, "key_fingerprint": "" }),
            json!({ "configured": true, "password_set": true, "key_fingerprint": KEY_A.to_uppercase() }),
            json!({ "configured": true, "password_set": true, "key_fingerprint": &KEY_A[1..] }),
            json!({ "configured": true, "password_set": "yes" }),
            json!({ "configured": true }),
        ] {
            assert!(!check(&pins, &status), "{status}");
            assert!(!pins.apply(ACCOUNT, &status, PinAction::Pin), "{status}");
        }
        assert_eq!(store.value(), None);
    }

    #[test]
    fn an_unreadable_or_invalid_pin_raises_no_warning() {
        let store = FakePins {
            fail_load: true,
            ..FakePins::default()
        };
        let pins = RawKeyPins::new(Box::new(store.clone()));
        assert!(!check(&pins, &keyed(KEY_B)));
        assert_eq!(store.value(), None);

        let (pins, store) = self::pins();
        store.save(ACCOUNT, "not-a-pin").unwrap();
        assert!(!check(&pins, &keyed(KEY_B)));
        assert_eq!(store.value().as_deref(), Some("not-a-pin"));
    }

    #[test]
    fn data_directories_keep_separate_pins() {
        let (pins, _) = pins();
        assert!(!pins.apply("1111111111111111", &keyed(KEY_A), PinAction::Check));
        assert!(!pins.apply("2222222222222222", &keyed(KEY_B), PinAction::Check));
        assert!(pins.apply("1111111111111111", &keyed(KEY_B), PinAction::Check));
    }

    struct TempDir(PathBuf);

    impl TempDir {
        fn new() -> Self {
            let mut random = [0_u8; 8];
            getrandom::getrandom(&mut random).unwrap();
            let suffix: String = random.iter().map(|byte| format!("{byte:02x}")).collect();
            let path = std::env::temp_dir().join(format!("astrlink-raw-key-pin-{suffix}"));
            fs::create_dir(&path).unwrap();
            Self(path)
        }
    }

    impl Drop for TempDir {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }

    #[test]
    fn the_pin_file_is_owner_only_and_keeps_other_accounts() {
        let root = TempDir::new();
        let path = root.0.join("config").join(PIN_FILE_NAME);
        let store = FilePins::new(path.clone());
        assert_eq!(store.load(ACCOUNT).unwrap(), None);
        store.save(ACCOUNT, KEY_A).unwrap();
        store.save("fedcba9876543210", NO_PASSWORD).unwrap();
        store.save(ACCOUNT, KEY_B).unwrap();
        assert_eq!(store.load(ACCOUNT).unwrap().as_deref(), Some(KEY_B));
        assert_eq!(
            store.load("fedcba9876543210").unwrap().as_deref(),
            Some(NO_PASSWORD)
        );
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let mode = fs::metadata(&path).unwrap().permissions().mode();
            assert_eq!(mode & 0o777, 0o600);
        }
        let leftovers: Vec<_> = fs::read_dir(path.parent().unwrap())
            .unwrap()
            .map(|entry| entry.unwrap().file_name())
            .collect();
        assert_eq!(leftovers, vec![std::ffi::OsString::from(PIN_FILE_NAME)]);

        let pins = RawKeyPins::new(Box::new(FilePins::new(path.clone())));
        assert_eq!(pins.file(), Some(path.as_path()));
        assert!(!check(&pins, &keyed(KEY_B)));
        assert!(check(&pins, &keyed(KEY_A)));
    }

    #[test]
    fn a_damaged_pin_file_is_neither_trusted_nor_replaced() {
        let root = TempDir::new();
        let path = root.0.join(PIN_FILE_NAME);
        fs::write(&path, b"{not json").unwrap();
        let store = FilePins::new(path.clone());
        assert!(store.load(ACCOUNT).is_err());
        assert!(store.save(ACCOUNT, KEY_A).is_err());
        let pins = RawKeyPins::new(Box::new(FilePins::new(path.clone())));
        assert!(!check(&pins, &keyed(KEY_A)));
        assert_eq!(fs::read(&path).unwrap(), b"{not json");
    }
}
