import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { createHash, generateKeyPairSync, sign } from "node:crypto";
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import {
  collectRelease,
  releaseVersion,
  signingEnvironment,
  stampVersion,
  stageUpdate,
  targets,
  verifyUpdateSignature,
} from "./release-updates.mjs";

function signingFixture(bytes: Buffer) {
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  const id = Buffer.from("0102030405060708", "hex");
  const key = Buffer.concat([
    Buffer.from("Ed"),
    id,
    publicKey.export({ type: "spki", format: "der" }).subarray(-32),
  ]);
  const signature = sign(
    null,
    createHash("blake2b512").update(bytes).digest(),
    privateKey,
  );
  const comment = "timestamp:1\tfile:test";
  const global = sign(
    null,
    Buffer.concat([signature, Buffer.from(comment)]),
    privateKey,
  );
  return {
    publicKey: Buffer.from(
      `untrusted comment: test key\n${key.toString("base64")}\n`,
    ).toString("base64"),
    signature: Buffer.from(
      `untrusted comment: test signature\n${Buffer.concat([Buffer.from("ED"), id, signature]).toString("base64")}\ntrusted comment: ${comment}\n${global.toString("base64")}\n`,
    ).toString("base64"),
  };
}
const temporary: string[] = [];
const directory = () => {
  const dir = mkdtempSync(path.join(os.tmpdir(), "astrlink-update-test-"));
  temporary.push(dir);
  return dir;
};
afterEach(() => {
  for (const dir of temporary.splice(0))
    rmSync(dir, { recursive: true, force: true });
});

describe("release updates", () => {
  it("round-trips real Tauri CLI signatures through all four platform manifests", () => {
    const root = directory();
    mkdirSync(path.join(root, "src-tauri"));
    const cli = fileURLToPath(
      new URL("../node_modules/@tauri-apps/cli/tauri.js", import.meta.url),
    );
    const keyPath = path.join(root, "test.key");
    execFileSync(
      "bun",
      [
        cli,
        "signer",
        "generate",
        "--ci",
        "--password",
        "",
        "--write-keys",
        keyPath,
      ],
      { stdio: "pipe" },
    );
    const env = {
      ...process.env,
      TAURI_SIGNING_PRIVATE_KEY: readFileSync(keyPath, "utf8").trim(),
      TAURI_UPDATER_PUBLIC_KEY: readFileSync(`${keyPath}.pub`, "utf8").trim(),
      TAURI_SIGNING_PRIVATE_KEY_PASSWORD: "",
    };
    writeFileSync(
      path.join(root, "package.json"),
      JSON.stringify({ version: "1.0.0", scripts: { tauri: `bun "${cli}"` } }),
    );
    writeFileSync(
      path.join(root, "src-tauri/tauri.conf.json"),
      JSON.stringify({ version: "1.0.0" }),
    );
    const packages = path.join(root, "package");
    mkdirSync(packages);
    for (const arch of ["arm64", "x86_64"])
      writeFileSync(
        path.join(packages, `AstrLink-macOS-${arch}.app.tar.gz`),
        `test-only archive ${arch}`,
      );
    for (const [folder, file] of [
      ["x86_64-pc-windows-msvc/release/bundle/nsis", "test-setup.exe"],
      ["release/bundle/appimage", "test.AppImage"],
    ]) {
      const destination = path.join(root, "src-tauri/target", folder);
      mkdirSync(destination, { recursive: true });
      writeFileSync(
        path.join(destination, file),
        `test-only installer ${file}`,
      );
    }
    for (const target of targets) stageUpdate(root, target, "v1.0.0", env);
    const manifest = collectRelease(
      packages,
      path.join(root, "release"),
      "v1.0.0",
      env.TAURI_UPDATER_PUBLIC_KEY,
    );
    expect(Object.keys(manifest.platforms)).toEqual(targets);
  });

  it("validates release tags and identifies preview channels", () => {
    expect(releaseVersion("v1.2.3")).toEqual({
      version: "1.2.3",
      prerelease: false,
    });
    expect(releaseVersion("1.2.3-rc.2+build.4").prerelease).toBe(true);
    for (const tag of [
      "latest",
      "v01.2.3",
      "1.2",
      "1.2.3-01",
      "1.2.3+",
      "1.2.3/evil",
    ])
      expect(() => releaseVersion(tag)).toThrow();
  });
  it("stamps all desktop versions without changing dependencies", () => {
    const dir = directory();
    mkdirSync(path.join(dir, "src-tauri"));
    writeFileSync(
      path.join(dir, "package.json"),
      '{"version":"0.1.0","name":"desktop"}',
    );
    writeFileSync(
      path.join(dir, "src-tauri/tauri.conf.json"),
      '{"version":"0.1.0"}',
    );
    writeFileSync(
      path.join(dir, "src-tauri/Cargo.toml"),
      '[package]\nname = "astrlink-desktop"\nversion = "0.1.0"\n[dependencies]\nother = "1"\n',
    );
    writeFileSync(
      path.join(dir, "src-tauri/Cargo.lock"),
      '[[package]]\nname = "astrlink-desktop"\nversion = "0.1.0"\n[[package]]\nname = "other"\nversion = "1.0.0"\n',
    );
    // Git checkouts on Windows may contain CRLF.
    for (const file of ["src-tauri/Cargo.toml", "src-tauri/Cargo.lock"]) {
      const target = path.join(dir, file);
      writeFileSync(
        target,
        readFileSync(target, "utf8").replaceAll("\n", "\r\n"),
      );
    }
    stampVersion(dir, "v2.0.0-beta.1");
    expect(
      JSON.parse(readFileSync(path.join(dir, "package.json"), "utf8")).version,
    ).toBe("2.0.0-beta.1");
    expect(
      readFileSync(path.join(dir, "src-tauri/Cargo.lock"), "utf8"),
    ).toContain('name = "other"\r\nversion = "1.0.0"');
    expect(
      readFileSync(path.join(dir, "src-tauri/Cargo.toml"), "utf8"),
    ).toContain('version = "2.0.0-beta.1"');
  });
  it("verifies both artifact bytes and the trusted comment", () => {
    const bytes = Buffer.from("a signed test artifact"),
      keys = signingFixture(bytes);
    expect(() =>
      verifyUpdateSignature(bytes, keys.signature, keys.publicKey),
    ).not.toThrow();
    expect(() =>
      verifyUpdateSignature(
        Buffer.from("tampered"),
        keys.signature,
        keys.publicKey,
      ),
    ).toThrow("signature mismatch");
    const changed = Buffer.from(
      Buffer.from(keys.signature, "base64")
        .toString()
        .replace("timestamp:1", "timestamp:2"),
    ).toString("base64");
    expect(() => verifyUpdateSignature(bytes, changed, keys.publicKey)).toThrow(
      "trusted comment",
    );
    expect(() =>
      verifyUpdateSignature(
        bytes,
        keys.signature,
        signingFixture(bytes).publicKey,
      ),
    ).toThrow();
  });
  it("requires signing configuration only for update releases", () => {
    expect(() => signingEnvironment({})).toThrow("PUBLIC_KEY");
    expect(() =>
      signingEnvironment({ TAURI_UPDATER_PUBLIC_KEY: "public" }),
    ).toThrow("PRIVATE_KEY");
  });
  it("publishes a manifest only after every platform is present and verified", () => {
    const input = directory(),
      output = path.join(directory(), "release"),
      bytes = Buffer.from("signed installer");
    const keys = signingFixture(bytes);
    function platform(target: string) {
      const dir = path.join(input, target);
      mkdirSync(dir);
      const filename = `${target}.exe`;
      writeFileSync(path.join(dir, filename), bytes);
      writeFileSync(path.join(dir, `${filename}.sig`), keys.signature);
      writeFileSync(
        path.join(dir, `${target}.json`),
        JSON.stringify({
          target,
          tag: "v1.0.0",
          version: "1.0.0",
          file: filename,
          signature: keys.signature,
          sha256: createHash("sha256").update(bytes).digest("hex"),
        }),
      );
    }
    for (const target of targets.slice(0, -1)) platform(target);
    expect(() =>
      collectRelease(input, output, "v1.0.0", keys.publicKey),
    ).toThrow("Missing or duplicate");
    expect(existsSync(output)).toBe(false);
    platform(targets.at(-1)!);
    const manifest = collectRelease(
      input,
      output,
      "v1.0.0",
      keys.publicKey,
      "Release notes",
    );
    expect(Object.keys(manifest.platforms)).toEqual(targets);
    expect(manifest.platforms[targets[0]].url).toContain(
      "/releases/download/v1.0.0/",
    );
    expect(() =>
      collectRelease(
        input,
        path.join(directory(), "bad"),
        "v2.0.0",
        keys.publicKey,
      ),
    ).toThrow("Version");
    writeFileSync(
      path.join(input, targets[0], `${targets[0]}.exe`),
      "corrupted",
    );
    expect(() =>
      collectRelease(
        input,
        path.join(directory(), "bad"),
        "v1.0.0",
        keys.publicKey,
      ),
    ).toThrow("checksum");
  });
});
