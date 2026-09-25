// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT
// Run: node --test automation/release/npm/launcher.test.js
"use strict";

const test = require("node:test");
const assert = require("node:assert");
const path = require("path");
const launcher = require("./launcher/bin/pig.js");

const SIX = [
  ["darwin", "arm64", "@pi-in-go/pig-darwin-arm64", "pig"],
  ["darwin", "x64", "@pi-in-go/pig-darwin-x64", "pig"],
  ["linux", "arm64", "@pi-in-go/pig-linux-arm64", "pig"],
  ["linux", "x64", "@pi-in-go/pig-linux-x64", "pig"],
  ["win32", "arm64", "@pi-in-go/pig-win32-arm64", "pig.exe"],
  ["win32", "x64", "@pi-in-go/pig-win32-x64", "pig.exe"],
];

for (const [platform, arch, pkg, bin] of SIX) {
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

test("exactly six platform packages", () => {
  assert.strictEqual(Object.keys(launcher.PLATFORM_PACKAGES).length, 6);
});

for (const [platform, arch] of [["freebsd", "x64"], ["linux", "ia32"], ["linux", "s390x"], ["aix", "ppc64"]]) {
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
