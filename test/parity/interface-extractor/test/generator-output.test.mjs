import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

for (const [script, target] of [
  ["extract-cli.mjs", "interface-proposals"],
  ["extract-test-inventory.mjs", "test-inventory-generate"],
  ["extract-behavior-inputs.mjs", "behavior-input-inventory"],
]) {
  test(`${script} warns for implicit stdout and preserves explicit outputs`, (t) => {
    const root = fs.mkdtempSync(path.join(os.tmpdir(), "pig-generator-output-"));
    t.after(() => fs.rmSync(root, { recursive: true, force: true }));
    for (const [name, source] of [
      ["packages/coding-agent/src/cli/args.ts", "export function parseArgs(args: string[]) {}\nexport function printHelp() {}\n"],
      ["packages/coding-agent/src/package-manager-cli.ts", "export function handleConfigCommand(args: string[]) {}\nfunction parsePackageCommand(args: string[]) {}\nfunction printConfigCommandHelp() {}\nfunction printPackageCommandHelp(command: string) {}\n"],
      ["packages/tui/test/fixture.test.ts", 'test("fixture", () => {});\n'],
    ]) {
      const file = path.join(root, name);
      fs.mkdirSync(path.dirname(file), { recursive: true });
      fs.writeFileSync(file, source);
    }
    const run = (...args) => {
      const result = spawnSync(process.execPath, [
        fileURLToPath(new URL(`../src/${script}`, import.meta.url)),
        "--source-root", root, "--upstream-version", "fixture", ...args,
      ], { encoding: "utf8" });
      assert.equal(result.status, 0, result.stderr);
      return result;
    };
    const implicit = run();
    assert.match(implicit.stderr, /writing to stdout/);
    assert.ok(implicit.stderr.includes(`run: make generate (or: make ${target})`));
    assert.match(implicit.stderr, /--out -/);
    assert.equal(JSON.parse(implicit.stdout).upstreamVersion, "fixture");
    const explicit = run("--out", "-");
    assert.equal(explicit.stderr, "");
    assert.equal(explicit.stdout, implicit.stdout);
    const file = path.join(root, "inventory.json");
    const written = run("--out", file);
    assert.equal(written.stdout, "");
    assert.equal(written.stderr, "");
    assert.equal(fs.readFileSync(file, "utf8"), implicit.stdout);
  });
}
