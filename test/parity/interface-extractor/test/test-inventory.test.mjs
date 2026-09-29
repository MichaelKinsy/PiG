import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { extractTestInventory } from "../src/test-inventory.mjs";

// fixture writes upstream-shaped test files (including a nested workspace package
// and every call form the extractor must handle) and returns the source root.
function fixture() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "pig-test-inventory-"));
  const write = (relative, contents) => {
    const file = path.join(root, relative);
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.writeFileSync(file, contents);
  };

  write(
    "packages/coding-agent/test/sample.test.ts",
    `import { describe, it, test, expect } from "vitest";
describe("outer", () => {
  it("does a thing", () => { expect(1).toBe(1); });
  describe("inner", () => {
    test("nested test", () => {});
    it.skip("skipped case", () => {});
  });
});
it.each([1, 2])("each case %s", (n) => { expect(n).toBeDefined(); });
test("top-level test", () => {});
`,
  );
  // Nested workspace package: tests live under a deeper test/ directory.
  write(
    "packages/session-backends/sqlite-node/test/store.test.ts",
    `import { it } from "vitest";
it("persists rows", () => {});
`,
  );
  // Data-driven file: cases are generated at runtime with computed titles.
  write(
    "packages/coding-agent/test/conformance.test.ts",
    `import { describe, it } from "vitest";
const table = [{ name: "a" }, { name: "b" }];
describe("conformance", () => {
  for (const c of table) {
    it(c.name, () => {});
  }
});
`,
  );
  // A non-test source file must be ignored.
  write("packages/coding-agent/src/index.ts", `export const x = 1;\n`);
  return root;
}

test("enumerates every test file at any nesting depth", () => {
  const root = fixture();
  const inv = extractTestInventory({ sourceRoot: root, upstreamVersion: "9.9.9" });
  assert.equal(inv.fileCount, 3);
  const paths = inv.files.map((f) => f.path);
  assert.deepEqual(paths, [
    "packages/coding-agent/test/conformance.test.ts",
    "packages/coding-agent/test/sample.test.ts",
    "packages/session-backends/sqlite-node/test/store.test.ts",
  ]);
  assert.equal(inv.files[2].package, "session-backends");
});

test("flags dynamic it/test sites instead of reporting a false zero", () => {
  const root = fixture();
  const inv = extractTestInventory({ sourceRoot: root, upstreamVersion: "9.9.9" });
  const conformance = inv.files.find((f) => f.path.endsWith("conformance.test.ts"));
  assert.equal(conformance.caseCount, 0);
  assert.equal(conformance.dynamicCaseSites, 1);
  assert.equal(inv.dynamicCaseSiteCount, 1);
  // Static-only files omit the field entirely.
  const sample = inv.files.find((f) => f.path.endsWith("sample.test.ts"));
  assert.equal(sample.dynamicCaseSites, undefined);
});

test("folds describe context into case ids and captures kind/modifiers", () => {
  const root = fixture();
  const inv = extractTestInventory({ sourceRoot: root, upstreamVersion: "9.9.9" });
  const sample = inv.files.find((f) => f.path.endsWith("sample.test.ts"));
  const ids = sample.cases.map((c) => c.id);
  assert.deepEqual(ids, [
    "outer \u203a does a thing",
    "outer \u203a inner \u203a nested test",
    "outer \u203a inner \u203a skipped case",
    "each case %s",
    "top-level test",
  ]);
  const byId = Object.fromEntries(sample.cases.map((c) => [c.id, c]));
  assert.equal(byId["outer \u203a inner \u203a nested test"].kind, "test");
  assert.deepEqual(byId["outer \u203a inner \u203a skipped case"].modifiers, ["skip"]);
  assert.deepEqual(byId["each case %s"].modifiers, ["each"]);
  assert.equal(byId["each case %s"].kind, "it");
  // The it.each(table)(...) table application must not produce a phantom case.
  assert.equal(sample.caseCount, 5);
});

test("hashes file content and totals cases; output is deterministic", () => {
  const root = fixture();
  const a = extractTestInventory({ sourceRoot: root, upstreamVersion: "9.9.9" });
  const b = extractTestInventory({ sourceRoot: root, upstreamVersion: "9.9.9" });
  assert.deepEqual(a, b);
  assert.equal(a.caseCount, 6);
  for (const file of a.files) assert.match(file.sha256, /^sha256:[0-9a-f]{64}$/);
  assert.equal(a.upstreamVersion, "9.9.9");
  assert.equal(a.generator, "extract-test-inventory.mjs");
});
