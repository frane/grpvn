#!/usr/bin/env node
// npm launcher for grpvn. Downloads the release binary matching this
// package's version on first run — sha256-verified against the release's
// checksums.txt — caches it inside the package directory (so `npx grpvn`
// pays the download once per version), then execs it with args passed
// through untouched.
"use strict";
const { spawnSync } = require("node:child_process");
const crypto = require("node:crypto");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const version = require("../package.json").version;
const repo = "frane/grpvn";

function fail(msg) {
  console.error(`grpvn (npm launcher): ${msg}`);
  process.exit(1);
}

function triple() {
  const osName = { darwin: "darwin", linux: "linux", win32: "windows" }[process.platform];
  const arch = { arm64: "arm64", x64: "x86_64" }[process.arch];
  if (!osName || !arch) fail(`no prebuilt binary for ${process.platform}/${process.arch}; install via https://github.com/${repo}#install`);
  if (osName === "windows" && arch !== "x86_64") fail("windows builds are x86_64 only");
  return `${osName}_${arch}`;
}

const exe = process.platform === "win32" ? "grpvn.exe" : "grpvn";
const cacheDir = path.join(__dirname, "..", "dist");
const cached = path.join(cacheDir, exe);

async function fetchBytes(url) {
  const res = await fetch(url);
  if (!res.ok) fail(`download failed (${res.status}): ${url}`);
  return Buffer.from(await res.arrayBuffer());
}

async function download() {
  const t = triple();
  const ext = process.platform === "win32" ? "zip" : "tar.gz";
  const asset = `grpvn_${version}_${t}.${ext}`;
  const base = `https://github.com/${repo}/releases/download/v${version}`;
  console.error(`grpvn: fetching ${asset} (first run for ${version})`);
  const [archive, sums] = await Promise.all([
    fetchBytes(`${base}/${asset}`),
    fetchBytes(`${base}/checksums.txt`),
  ]);
  const line = sums.toString("utf8").split("\n").find((l) => l.includes(asset));
  if (!line) fail(`no checksum for ${asset}`);
  const want = line.trim().split(/\s+/)[0].toLowerCase();
  const got = crypto.createHash("sha256").update(archive).digest("hex");
  if (want !== got) fail(`sha256 mismatch for ${asset} (want ${want}, got ${got})`);

  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "grpvn-npm-"));
  try {
    const archivePath = path.join(tmp, asset);
    fs.writeFileSync(archivePath, archive);
    // bsdtar ships with macOS, Linux distros, and Windows 10+, and handles
    // both tar.gz and zip — no unzip dependency needed.
    const tar = spawnSync("tar", ["-xf", archivePath, "-C", tmp], { stdio: "inherit" });
    if (tar.status !== 0) fail("could not extract archive (is `tar` on PATH?)");
    const extracted = path.join(tmp, exe);
    if (!fs.existsSync(extracted)) fail(`${exe} missing from ${asset}`);
    fs.mkdirSync(cacheDir, { recursive: true });
    fs.copyFileSync(extracted, cached);
    fs.chmodSync(cached, 0o755);
  } finally {
    fs.rmSync(tmp, { recursive: true, force: true });
  }
}

async function main() {
  if (!fs.existsSync(cached)) await download();
  const res = spawnSync(cached, process.argv.slice(2), { stdio: "inherit" });
  if (res.error) fail(String(res.error));
  process.exit(res.status ?? 0);
}

main();
