fn main() {
    println!("cargo:rerun-if-env-changed=TAURI_UPDATER_PUBLIC_KEY");
    let update_key = std::env::var("TAURI_UPDATER_PUBLIC_KEY").unwrap_or_default();
    assert!(
        !update_key.contains(['\n', '\r']),
        "updater public key must be a single base64 line"
    );
    println!(
        "cargo:rustc-env=TAURI_UPDATER_PUBLIC_KEY={}",
        update_key.trim()
    );
    let attributes = tauri_build::Attributes::new();
    let attributes = if std::env::var("CARGO_CFG_TARGET_ENV").as_deref() == Ok("msvc") {
        let manifest =
            std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("windows-app-manifest.xml");
        // tauri-build links its manifest only into bins, leaving lib unit tests without v6 controls.
        println!("cargo:rerun-if-changed={}", manifest.display());
        println!("cargo:rustc-link-arg=/MANIFEST:EMBED");
        println!("cargo:rustc-link-arg=/MANIFESTINPUT:{}", manifest.display());
        attributes.windows_attributes(tauri_build::WindowsAttributes::new_without_app_manifest())
    } else {
        attributes
    };
    tauri_build::try_build(attributes).expect("failed to build Tauri application");
}
