import { createHash, createPublicKey, verify } from "node:crypto";
import { execFileSync } from "node:child_process";
import {
  cpSync,
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  writeFileSync,
} from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const desktop = fileURLToPath(new URL("..", import.meta.url));
const repository = "Calcium-Ion/AstrLink";
export const targets = [
  "darwin-aarch64",
  "darwin-x86_64",
  "windows-x86_64",
  "linux-x86_64",
];
const readJSON = (file) => JSON.parse(readFileSync(file, "utf8"));
const writeJSON = (file, value) =>
  writeFileSync(file, JSON.stringify(value, null, 2) + "\n");
const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");
function requireValue(value, message) {
  if (!value) throw new Error(message);
  return value;
}

export function releaseVersion(tag) {
  const version = tag.replace(/^v/, "");
  const numeric = "(?:0|[1-9][0-9]*)";
  const id = "(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)";
  requireValue(
    new RegExp(
      `^${numeric}\\.${numeric}\\.${numeric}(?:-${id}(?:\\.${id})*)?(?:\\+[0-9A-Za-z-]+(?:\\.[0-9A-Za-z-]+)*)?$`,
    ).test(version),
    "Release tag must be a SemVer version, optionally prefixed with v",
  );
  return { version, prerelease: version.split("+")[0].includes("-") };
}

export function stampVersion(root, tag) {
  const { version } = releaseVersion(tag);
  for (const filename of ["package.json", "src-tauri/tauri.conf.json"]) {
    const file = path.join(root, filename),
      data = readJSON(file);
    data.version = version;
    writeJSON(file, data);
  }
  const cargo = path.join(root, "src-tauri/Cargo.toml");
  writeFileSync(
    cargo,
    readFileSync(cargo, "utf8").replace(
      /(\[package\][\s\S]*?\nversion = ")[^"]+("\r?\n)/,
      `$1${version}$2`,
    ),
  );
  const lock = path.join(root, "src-tauri/Cargo.lock");
  writeFileSync(
    lock,
    readFileSync(lock, "utf8").replace(
      /(name = "astrlink-desktop"\r?\nversion = ")[^"]+("\r?\n)/,
      `$1${version}$2`,
    ),
  );
}

/** Verify Tauri's base64-wrapped Minisign signatures, including the trusted comment. */
export function verifyUpdateSignature(bytes, signature, publicKey) {
  const lines = Buffer.from(signature.trim(), "base64")
    .toString("utf8")
    .trim()
    .split(/\r?\n/);
  const keyLines = Buffer.from(publicKey.trim(), "base64")
    .toString("utf8")
    .trim()
    .split(/\r?\n/);
  const key = Buffer.from(keyLines[1] ?? "", "base64"),
    sig = Buffer.from(lines[1] ?? "", "base64");
  requireValue(
    key.length === 42 &&
      sig.length === 74 &&
      lines[2]?.startsWith("trusted comment: "),
    "Invalid update signature encoding",
  );
  requireValue(
    key.subarray(2, 10).equals(sig.subarray(2, 10)),
    "Update signature key does not match embedded public key",
  );
  const algorithm = sig.subarray(0, 2).toString();
  requireValue(
    algorithm === "ED" || algorithm === "Ed",
    "Unsupported update signature algorithm",
  );
  const verifier = createPublicKey({
    key: Buffer.concat([
      Buffer.from("302a300506032b6570032100", "hex"),
      key.subarray(10),
    ]),
    format: "der",
    type: "spki",
  });
  const message =
    algorithm === "ED"
      ? createHash("blake2b512").update(bytes).digest()
      : bytes;
  requireValue(
    verify(null, message, verifier, sig.subarray(10)),
    "Update artifact signature mismatch",
  );
  requireValue(
    verify(
      null,
      Buffer.concat([sig.subarray(10), Buffer.from(lines[2].slice(17))]),
      verifier,
      Buffer.from(lines[3] ?? "", "base64"),
    ),
    "Update trusted comment signature mismatch",
  );
}

export function signingEnvironment(env = process.env) {
  requireValue(
    env.TAURI_UPDATER_PUBLIC_KEY?.trim(),
    "Missing Actions variable TAURI_UPDATER_PUBLIC_KEY",
  );
  requireValue(
    env.TAURI_SIGNING_PRIVATE_KEY?.trim(),
    "Missing Actions secret TAURI_SIGNING_PRIVATE_KEY",
  );
}

export function stageUpdate(root, target, tag, env = process.env) {
  signingEnvironment(env);
  requireValue(targets.includes(target), "Unsupported update target");
  const { version } = releaseVersion(tag);
  requireValue(
    readJSON(path.join(root, "src-tauri/tauri.conf.json")).version ===
      version && readJSON(path.join(root, "package.json")).version === version,
    "Build version does not match release tag",
  );
  const output = path.join(root, "package");
  mkdirSync(output, { recursive: true });
  let file;
  if (target.startsWith("darwin-")) {
    const arch = target === "darwin-aarch64" ? "arm64" : "x86_64";
    // Created by the macOS workflow only AFTER notarization and stapling.
    file = path.join(output, `AstrLink-macOS-${arch}.app.tar.gz`);
  } else {
    const windows = target.startsWith("windows-");
    const bundle = path.join(
      root,
      windows
        ? "src-tauri/target/x86_64-pc-windows-msvc/release/bundle/nsis"
        : "src-tauri/target/release/bundle/appimage",
    );
    const candidates = readdirSync(bundle).filter((f) =>
      f.endsWith(windows ? ".exe" : ".AppImage"),
    );
    requireValue(
      candidates.length === 1,
      "Expected exactly one update installer",
    );
    file = path.join(output, candidates[0]);
    cpSync(path.join(bundle, candidates[0]), file);
  }
  requireValue(existsSync(file), "Missing verified update bundle");
  execFileSync("bun", ["run", "tauri", "signer", "sign", file], {
    cwd: root,
    env: {
      ...env,
      TAURI_SIGNING_PRIVATE_KEY_PASSWORD:
        env.TAURI_SIGNING_PRIVATE_KEY_PASSWORD ?? "",
    },
    stdio: "pipe",
  });
  const signature = readFileSync(`${file}.sig`, "utf8").trim(),
    bytes = readFileSync(file);
  verifyUpdateSignature(bytes, signature, env.TAURI_UPDATER_PUBLIC_KEY);
  writeJSON(path.join(output, `${target}.json`), {
    tag,
    version,
    target,
    file: path.basename(file),
    signature,
    sha256: sha256(bytes),
  });
}

function filesIn(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) =>
    entry.isDirectory()
      ? filesIn(path.join(directory, entry.name))
      : [path.join(directory, entry.name)],
  );
}

export function collectRelease(
  input,
  output,
  tag,
  publicKey,
  notes = "",
  now = new Date(),
) {
  const { version } = releaseVersion(tag),
    files = filesIn(input);
  const platforms = {},
    assets = new Map();
  for (const target of targets) {
    const fragments = files.filter(
      (file) => path.basename(file) === `${target}.json`,
    );
    requireValue(
      fragments.length === 1,
      `Missing or duplicate platform: ${target}`,
    );
    const fragment = readJSON(fragments[0]);
    requireValue(
      fragment.version === version &&
        fragment.tag === tag &&
        fragment.target === target,
      `Version or target mismatch: ${target}`,
    );
    requireValue(
      typeof fragment.file === "string" &&
        path.basename(fragment.file) === fragment.file,
      "Invalid artifact filename",
    );
    const file = path.join(path.dirname(fragments[0]), fragment.file),
      bytes = readFileSync(file);
    requireValue(
      sha256(bytes) === fragment.sha256,
      `Artifact checksum mismatch: ${target}`,
    );
    requireValue(
      readFileSync(`${file}.sig`, "utf8").trim() === fragment.signature,
      `Signature sidecar mismatch: ${target}`,
    );
    verifyUpdateSignature(bytes, fragment.signature, publicKey);
    platforms[target] = {
      signature: fragment.signature,
      url: `https://github.com/${repository}/releases/download/${encodeURIComponent(tag)}/${encodeURIComponent(fragment.file)}`,
    };
  }
  // All validation precedes creating the publish directory or touching GitHub.
  for (const file of files.filter((file) =>
    /\.(dmg|deb|exe|AppImage|tar\.gz|sig)$/.test(file),
  )) {
    const name = path.basename(file);
    requireValue(!assets.has(name), `Duplicate release filename: ${name}`);
    assets.set(name, file);
  }
  mkdirSync(output, { recursive: true });
  for (const [name, file] of assets) cpSync(file, path.join(output, name));
  const manifest = { version, notes, pub_date: now.toISOString(), platforms };
  writeJSON(path.join(output, "latest.json"), manifest);
  writeFileSync(
    path.join(output, "SHA256SUMS"),
    [...assets.keys(), "latest.json"]
      .sort()
      .map(
        (name) => `${sha256(readFileSync(path.join(output, name)))}  ${name}\n`,
      )
      .join(""),
  );
  return manifest;
}

function gh(args) {
  return execFileSync("gh", args, {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  });
}
export function publishRelease(input, output, tag, env = process.env) {
  requireValue(
    env.GITHUB_REPOSITORY === repository,
    "Release publishing is restricted to the project repository",
  );
  const { prerelease } = releaseVersion(tag);
  const notes = JSON.parse(
    gh([
      "api",
      `repos/${repository}/releases/generate-notes`,
      "-f",
      `tag_name=${tag}`,
      "-f",
      `target_commitish=${env.GITHUB_SHA}`,
    ]),
  ).body;
  collectRelease(
    input,
    output,
    tag,
    requireValue(env.TAURI_UPDATER_PUBLIC_KEY, "Missing update public key"),
    notes,
  );
  const notesFile = path.join(output, "release-notes.txt");
  writeFileSync(notesFile, notes);
  const releases = JSON.parse(
    gh(["api", `repos/${repository}/releases`, "--paginate", "--slurp"]),
  ).flat();
  const existing = releases.find((release) => release.tag_name === tag);
  requireValue(
    !existing || existing.draft,
    "Refusing to overwrite an already published release",
  );
  if (!existing)
    gh([
      "release",
      "create",
      tag,
      "--repo",
      repository,
      "--draft",
      "--verify-tag",
      "--title",
      tag,
      "--notes-file",
      notesFile,
    ]);
  // A temporary draft keeps incomplete uploads invisible. No human approval is required.
  const assets = readdirSync(output)
    .filter((name) => name !== "release-notes.txt")
    .map((name) => path.join(output, name));
  gh(["release", "upload", tag, "--repo", repository, "--clobber", ...assets]);
  const remote = JSON.parse(
    gh(["release", "view", tag, "--repo", repository, "--json", "assets"]),
  );
  for (const asset of assets)
    requireValue(
      remote.assets.some(
        (a) =>
          a.name === path.basename(asset) &&
          a.size === readFileSync(asset).length,
      ),
      `Release upload incomplete: ${path.basename(asset)}`,
    );
  gh([
    "release",
    "edit",
    tag,
    "--repo",
    repository,
    "--draft=false",
    `--prerelease=${prerelease}`,
    `--latest=${!prerelease}`,
    "--notes-file",
    notesFile,
  ]);
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const [command, ...args] = process.argv.slice(2);
  try {
    if (command === "version") {
      const data = releaseVersion(args[0]);
      if (process.env.GITHUB_OUTPUT)
        writeFileSync(
          process.env.GITHUB_OUTPUT,
          `version=${data.version}\nprerelease=${data.prerelease}\n`,
          { flag: "a" },
        );
    } else if (command === "stamp") stampVersion(desktop, args[0]);
    else if (command === "preflight") signingEnvironment();
    else if (command === "stage") stageUpdate(desktop, args[0], args[1]);
    else if (command === "publish") publishRelease(args[0], args[1], args[2]);
    else
      throw new Error(
        "Expected version, stamp, preflight, stage or publish command",
      );
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
