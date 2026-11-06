// SPDX-License-Identifier: MIT
// Run: node --test automation/release/npm/launcher.test.js
"use strict";

const test = require("node:test");
const assert = require("node:assert");
const path = require("path");
const launcher = require("./launcher/bin/pig.js");

const TARGETS = [
  // Node on Termux reports "android" and needs the android binary, not linux-arm64.
  ["android", "arm64", "@pi-in-go/pig-android-arm64", "pig"],
  ["darwin", "arm64", "@pi-in-go/pig-darwin-arm64", "pig"],
  ["darwin", "x64", "@pi-in-go/pig-darwin-x64", "pig"],
  ["linux", "arm64", "@pi-in-go/pig-linux-arm64", "pig"],
  ["linux", "x64", "@pi-in-go/pig-linux-x64", "pig"],
  ["win32", "arm64", "@pi-in-go/pig-win32-arm64", "pig.exe"],
  ["win32", "x64", "@pi-in-go/pig-win32-x64", "pig.exe"],
];

for (const [platform, arch, pkg, bin] of TARGETS) {
  test(`resolves ${platform}/${arch}`, () => {
    assert.strictEqual(launcher.platformPackage(platform, arch), pkg);
    const root = path.join(path.sep, "prefix", "node_modules", pkg);
    const resolved = launcher.resolveBinary(platform, arch, (request) => {
      assert.strictEqual(request, `${pkg}/package.json`);
      return path.join(root, "package.json");
    });
    assert.strictEqual(resolved, path.join(root, bin));
  });
}

test("exactly seven platform packages", () => {
  assert.strictEqual(Object.keys(launcher.PLATFORM_PACKAGES).length, 7);
});

for (const [platform, arch] of [["freebsd", "x64"], ["android", "arm"], ["android", "x64"], ["linux", "ia32"], ["linux", "s390x"], ["aix", "ppc64"]]) {
  test(`unsupported ${platform}/${arch} explains alternatives`, () => {
    assert.strictEqual(launcher.platformPackage(platform, arch), null);
    assert.throws(
      () => launcher.resolveBinary(platform, arch, () => assert.fail("must not resolve")),
      (err) => /no npm package for/.test(err.message) && /go install/.test(err.message) && /install\.sh/.test(err.message),
    );
  });
}

test("missing optional platform package explains --no-optional", () => {
  assert.throws(
    () =>
      launcher.resolveBinary("linux", "x64", () => {
        const err = new Error("Cannot find module");
        err.code = "MODULE_NOT_FOUND";
        throw err;
      }),
    (err) => /@pi-in-go\/pig-linux-x64 is not installed/.test(err.message) && /--no-optional/.test(err.message),
  );
});

const REMOTE_HINT =
  "  ssh you@your-computer\n" +
  "  pig\n" +
  "Install PiG on that computer with:\n" +
  "  curl -fsSL https://pi-in-go.dev/install.sh | sh";

const IOS_MESSAGE =
  "PiG can't run on iPhone or iPad: iOS lets terminal apps run only code built into the app.\n" +
  "To use PiG from this device, connect to a computer that has PiG installed, then run pig there:\n" +
  REMOTE_HINT;

const ISH_MESSAGE =
  "PiG has no build for iSH, which emulates 32-bit x86 Linux.\n" +
  "To use PiG from this device, connect to a computer that has PiG installed, then run pig there:\n" +
  REMOTE_HINT;

test("ios explains that PiG does not run on iPhone or iPad", () => {
  assert.throws(
    () => launcher.resolveBinary("ios", "arm64", () => assert.fail("must not resolve"), () => assert.fail("must not probe")),
    (err) => err.message === IOS_MESSAGE,
  );
});

test("iSH explains that PiG does not support it", () => {
  const probed = [];
  assert.throws(
    () =>
      launcher.resolveBinary("linux", "ia32", () => assert.fail("must not resolve"), (p) => {
        probed.push(p);
        return true;
      }),
    (err) => err.message === ISH_MESSAGE,
  );
  assert.deepStrictEqual(probed, ["/proc/ish"]);
});

test("linux without /proc/ish is not iSH", () => {
  const root = path.join(path.sep, "prefix", "node_modules", "@pi-in-go", "pig-linux-x64");
  assert.strictEqual(
    launcher.resolveBinary("linux", "x64", () => path.join(root, "package.json"), () => false),
    path.join(root, "pig"),
  );
});

test("the launcher exits 1 with the iOS message on stderr", () => {
  const { spawnSync } = require("child_process");
  const fs = require("fs");
  const os = require("os");
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "pig-ios-"));
  try {
    const preload = path.join(dir, "ios.js");
    fs.writeFileSync(preload, 'Object.defineProperty(process, "platform", { value: "ios" });\n');
    const out = spawnSync(process.execPath, ["-r", preload, path.join(__dirname, "launcher", "bin", "pig.js")], { encoding: "utf8" });
    assert.strictEqual(out.status, 1);
    assert.strictEqual(out.stderr, `pig (npm launcher): ${IOS_MESSAGE}\n`);
    assert.strictEqual(out.stdout, "");
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
