import assert from "node:assert/strict";
import { copyFile, mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import test from "node:test";

const validator = fileURLToPath(new URL("./check-upstream-pin.mjs", import.meta.url));
const version = "9.8.7";

async function fixture(t, { mismatch, source, legacyVersion, onlyLegacy = false, readme } = {}) {
  const root = await mkdtemp(join(tmpdir(), "pig-sdk-pin-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const sdk = join(root, "extensions", "sdk-ts");
  const script = join(sdk, "scripts", "check-upstream-pin.mjs");
  await mkdir(dirname(script), { recursive: true });
  await copyFile(validator, script);
  const versions = { declared: version, development: version, locked: version };
  if (mismatch) versions[mismatch] = "9.8.6";
  await writeFile(join(sdk, "package.json"), JSON.stringify({
    peerDependencies: { "@earendil-works/pi-coding-agent": versions.declared },
    devDependencies: { "@earendil-works/pi-coding-agent": versions.development },
  }));
  await writeFile(join(sdk, "package-lock.json"), JSON.stringify({
    packages: { "node_modules/@earendil-works/pi-coding-agent": { version: versions.locked } },
  }));
  await writeFile(join(sdk, "README.md"), readme ?? `Exact Pi ${version} declarations.\n\nnpm install --save-dev @earendil-works/pi-coding-agent@${version}\n`);
  if (!onlyLegacy) {
    const pin = join(root, "internal", "coding", "pigversion", "pigversion.go");
    await mkdir(dirname(pin), { recursive: true });
    await writeFile(pin, source ?? `package pigversion\nconst UpstreamVersion = "${version}"\n`);
  }
  if (legacyVersion || onlyLegacy) {
    const pin = join(root, "coding", "pigversion", "pigversion.go");
    await mkdir(dirname(pin), { recursive: true });
    await writeFile(pin, `package pigversion\nconst UpstreamVersion = "${legacyVersion ?? version}"\n`);
  }
  return () => spawnSync(process.execPath, [script], { cwd: tmpdir(), encoding: "utf8" });
}

// db10ac87a moves the source unchanged; validation follows the current layout, not cwd or an old source path.
test("reads the relocated Go pin without the old coding directory", async (t) => {
  const run = await fixture(t);
  const result = run();
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout, "");
});

test("uses the current pin even when a different legacy source exists", async (t) => {
  const run = await fixture(t, { legacyVersion: "1.0.0" });
  const result = run();
  assert.equal(result.status, 0, result.stderr);
});

for (const mismatch of ["declared", "development", "locked"]) {
  test(`rejects mismatched ${mismatch} Pi version`, async (t) => {
    const run = await fixture(t, { mismatch });
    const result = run();
    assert.notEqual(result.status, 0);
    assert.ok(result.stderr.includes(`${mismatch} Pi version "9.8.6" does not match ${version}`), result.stderr);
  });
}

test("rejects a Go source without the upstream version constant", async (t) => {
  const run = await fixture(t, { source: "package pigversion\n" });
  const result = run();
  assert.notEqual(result.status, 0);
  assert.ok(result.stderr.includes("cannot read coding.UpstreamVersion"), result.stderr);
});

test("does not fall back to a legacy-only repository layout", async (t) => {
  const run = await fixture(t, { onlyLegacy: true });
  const result = run();
  assert.notEqual(result.status, 0);
  assert.ok(result.stderr.includes("ENOENT"), result.stderr);
  assert.ok(result.stderr.includes(join("internal", "coding", "pigversion", "pigversion.go")), result.stderr);
});

test("rejects a README that names a different Pi version", async (t) => {
  const run = await fixture(t, { readme: `Exact Pi ${version} declarations.\n\nnpm install @earendil-works/pi-coding-agent@9.8.6\n` });
  const result = run();
  assert.notEqual(result.status, 0);
  assert.ok(result.stderr.includes(`README Pi version "9.8.6" does not match ${version}`), result.stderr);
});

test("rejects a README whose Pi version follows a line break", async (t) => {
  const run = await fixture(t, { readme: `Exact declarations from Pi\n9.8.6.\n\nnpm install @earendil-works/pi-coding-agent@${version}\n` });
  const result = run();
  assert.notEqual(result.status, 0);
  assert.ok(result.stderr.includes(`README Pi version "9.8.6" does not match ${version}`), result.stderr);
});

test("rejects a README that does not name the Pi version", async (t) => {
  const run = await fixture(t, { readme: "Declarations only.\n" });
  const result = run();
  assert.notEqual(result.status, 0);
  assert.ok(result.stderr.includes(`README does not name Pi version ${version}`), result.stderr);
});
