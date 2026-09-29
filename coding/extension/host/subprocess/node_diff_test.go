package subprocess_test

import "testing"

func TestVendoredCrossSpawnClosureMatchesPinnedPackages(t *testing.T) {
	runPinnedComparison(t, []string{"node_modules", "cross-spawn", "package.json"}, "cross-spawn/package.json", `
const assert = await import("node:assert/strict");
const fs = await import("node:fs");
const path = await import("node:path");
const { fileURLToPath } = await import("node:url");
const { createRequire } = await import("node:module");
function compareDirectory(want, got) {
  const entries = (dir) => fs.readdirSync(dir).filter((name) => name !== "node_modules").sort();
  assert.deepEqual(entries(got), entries(want));
  for (const name of entries(want)) {
    const a = path.join(want, name), b = path.join(got, name);
    if (fs.statSync(a).isDirectory()) compareDirectory(a, b);
    else assert.deepEqual(fs.readFileSync(b), fs.readFileSync(a), b);
  }
}
function comparePackage(want, got) {
  compareDirectory(path.dirname(want), path.dirname(got));
  const manifest = JSON.parse(fs.readFileSync(want, "utf8"));
  const require = createRequire(want);
  const dependencies = Object.keys(manifest.dependencies ?? {}).sort();
  const modules = path.join(path.dirname(got), "node_modules");
  assert.deepEqual(fs.existsSync(modules) ? fs.readdirSync(modules).sort() : [], dependencies);
  for (const name of dependencies) comparePackage(require.resolve(name + "/package.json"), path.join(modules, name, "package.json"));
}
comparePackage(fileURLToPath(process.argv[1]), fileURLToPath(process.argv[2]));
`)
}

// Pi core/tools/edit-diff.ts:373-516 preserves context, line numbers, CRLF and missing final newlines. These exports are synchronous utilities, not session-bound operations.
func TestPiDiffHelpersMatchThePinnedPackage(t *testing.T) {
	runPinnedComparison(t, []string{"dist", "core", "tools", "edit-diff.js"}, "pi-coding-agent.mjs", `
const [pi, pig] = await Promise.all([import(process.argv[1]), import(process.argv[2])]);
const assert = await import("node:assert/strict");
const long = Array.from({length: 120}, (_, i) => "line " + i).join("\n");
for (const [before, after] of [
  ["", ""], ["", "hello\n"], ["hello\n", ""], ["same", "same"],
  ["old", "new"], ["one\ntwo\n", "one\nTWO\n"],
  ["one\r\ntwo\r\n", "one\r\nTWO\r\n"], ["hello\n", "hello"],
  ["你好\n😀\n", "你好\né\n"],
  [long, long.replace("line 10\n", "changed ten\n").replace("line 99\n", "changed ninety-nine\n")],
]) {
  for (const context of [undefined, 0, 1, 4, 20]) {
    assert.deepEqual(pig.generateDiffString(before, after, context), pi.generateDiffString(before, after, context));
    assert.equal(pig.generateUnifiedPatch("space path/é.txt", before, after, context), pi.generateUnifiedPatch("space path/é.txt", before, after, context));
  }
}
`)
}
