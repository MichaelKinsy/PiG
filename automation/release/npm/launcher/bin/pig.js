#!/usr/bin/env node
// SPDX-License-Identifier: MIT
//
// npm launcher for PiG. `pig` itself is a native Go binary shipped in one
// platform package per os/cpu pair (installed through optionalDependencies,
// the esbuild/biome pattern; no postinstall download). This script only finds
// that binary and runs it with the caller's stdio, arguments, exit status and
// signals.
"use strict";

const fs = require("fs");
const path = require("path");
const { spawn } = require("child_process");

const SCOPE = "@pi-in-go";

// process.platform/process.arch -> platform package. Keep in sync with
// TARGETS in automation/release/npm/pack_npm.py.
const PLATFORM_PACKAGES = {
  // Node on Termux reports "android". Termux needs the bionic-linked android binary, never linux-arm64.
  "android arm64": `${SCOPE}/pig-android-arm64`,
  "darwin arm64": `${SCOPE}/pig-darwin-arm64`,
  "darwin x64": `${SCOPE}/pig-darwin-x64`,
  "linux arm64": `${SCOPE}/pig-linux-arm64`,
  "linux x64": `${SCOPE}/pig-linux-x64`,
  "win32 arm64": `${SCOPE}/pig-win32-arm64`,
  "win32 x64": `${SCOPE}/pig-win32-x64`,
};

const OTHER_METHODS =
  "Other ways to install PiG:\n" +
  "  curl -fsSL https://pi-in-go.dev/install.sh | sh\n" +
  "  go install github.com/MichaelKinsy/PiG/cmd/pig@latest\n" +
  "  https://github.com/MichaelKinsy/PiG/releases";

const REMOTE_HINT =
  "To use PiG from this device, run it on a computer you can reach and connect to it:\n" +
  "  ssh you@your-computer pig\n" +
  "Install PiG on that computer with:\n" +
  "  curl -fsSL https://pi-in-go.dev/install.sh | sh";

// unsupportedDevice returns the message for an iPhone, an iPad (a-Shell's Node reports platform "ios") or iSH
// (Linux on emulated 32-bit x86, which exposes /proc/ish), and null elsewhere.
function unsupportedDevice(platform, exists) {
  if (platform === "ios") {
    return (
      "PiG can't run on iPhone or iPad. iOS lets terminal apps run only code built into the app, " +
      "so neither a PiG binary nor a WebAssembly build can reach a model provider.\n" +
      REMOTE_HINT
    );
  }
  if (platform === "linux" && exists("/proc/ish")) {
    return "PiG doesn't support iSH: iSH emulates 32-bit x86 Linux, and PiG doesn't run reliably there.\n" + REMOTE_HINT;
  }
  return null;
}

function platformPackage(platform, arch) {
  return PLATFORM_PACKAGES[`${platform} ${arch}`] || null;
}

function binaryName(platform) {
  return platform === "win32" ? "pig.exe" : "pig";
}

// resolveBinary returns the absolute path of the native pig binary, or throws
// an Error whose message tells the user what to do.
function resolveBinary(platform, arch, resolve, exists) {
  const device = unsupportedDevice(platform, exists || fs.existsSync);
  if (device) throw new Error(device);
  const pkg = platformPackage(platform, arch);
  if (!pkg) {
    throw new Error(
      `PiG has no npm package for ${platform}/${arch}. ` +
        `Supported: ${Object.keys(PLATFORM_PACKAGES).map((k) => k.replace(" ", "/")).join(", ")}.\n` +
        OTHER_METHODS,
    );
  }
  let manifest;
  try {
    manifest = (resolve || require.resolve)(`${pkg}/package.json`);
  } catch (_) {
    throw new Error(
      `The PiG platform package ${pkg} is not installed.\n` +
        "npm installs it automatically as an optional dependency of @pi-in-go/pig; it is missing\n" +
        "when optional dependencies were skipped (--no-optional, --omit=optional) or the\n" +
        "lockfile was created on another platform. Reinstall with optional dependencies:\n" +
        "  npm install -g @pi-in-go/pig\n" +
        OTHER_METHODS,
    );
  }
  return path.join(path.dirname(manifest), binaryName(platform));
}

const FORWARDED_SIGNALS = ["SIGINT", "SIGTERM", "SIGHUP", "SIGQUIT", "SIGBREAK"];

function main() {
  let binary;
  try {
    binary = resolveBinary(process.platform, process.arch);
  } catch (err) {
    process.stderr.write(`pig (npm launcher): ${err.message}\n`);
    process.exit(1);
  }

  const child = spawn(binary, process.argv.slice(2), { stdio: "inherit", windowsHide: false });
  const handlers = {};
  for (const signal of FORWARDED_SIGNALS) {
    handlers[signal] = () => {
      try {
        child.kill(signal);
      } catch (_) {
        // The child already exited or the signal is unsupported here.
      }
    };
    try {
      process.on(signal, handlers[signal]);
    } catch (_) {
      // Signal not supported on this platform.
    }
  }

  child.on("error", (err) => {
    process.stderr.write(`pig (npm launcher): cannot run ${binary}: ${err.message}\n${OTHER_METHODS}\n`);
    process.exit(1);
  });

  child.on("exit", (code, signal) => {
    for (const s of Object.keys(handlers)) process.removeListener(s, handlers[s]);
    if (signal) {
      // Die from the same signal so the caller sees the real termination cause.
      process.kill(process.pid, signal);
      return;
    }
    process.exit(code === null ? 1 : code);
  });
}

module.exports = { PLATFORM_PACKAGES, platformPackage, binaryName, resolveBinary };

if (require.main === module) {
  main();
}
