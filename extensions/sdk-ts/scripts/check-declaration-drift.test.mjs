// The exported declarations of the pinned Pi source build (the oracle in node_modules) must equal the published release the peer
// dependency names. The published declarations are testdata/published-<version>.declarations.json.gz, checksum-verified, not the network.
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { existsSync, readdirSync, readFileSync, realpathSync, statSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { gunzipSync } from "node:zlib";
import { compare, describe, PACKAGES } from "./declarations.mjs";

const sdk = resolve(dirname(fileURLToPath(import.meta.url)), "..");

// Public differences the pin is allowed to have, as { key: "<package>/<path>#<declaration>", reason }. Each entry names one declaration
// and fails when that declaration no longer differs.
export const NAMED_DIFFERENCES = [];

function* declarations(directory) {
  for (const name of readdirSync(directory).sort()) {
    const path = join(directory, name);
    if (statSync(path).isDirectory()) yield* declarations(path);
    else if (name.endsWith(".d.ts")) yield path;
  }
}

function installedTree() {
  const modules = realpathSync(join(sdk, "node_modules"));
  const agent = join(modules, "@earendil-works", "pi-coding-agent");
  const tree = new Map();
  for (const name of PACKAGES) {
    const candidates = [join(modules, "@earendil-works", name), join(agent, "node_modules", "@earendil-works", name)];
    const directory = candidates.find((candidate) => existsSync(join(candidate, "package.json")));
    assert.ok(directory, `no installed @earendil-works/${name} under ${modules}`);
    const pkg = JSON.parse(readFileSync(join(directory, "package.json"), "utf8"));
    tree.set(`@earendil-works/${name}/package.json`, JSON.stringify({ name: pkg.name, version: pkg.version, type: pkg.type, types: pkg.types, exports: pkg.exports }));
    for (const path of declarations(join(directory, "dist"))) {
      tree.set(`@earendil-works/${name}/${path.slice(directory.length + 1).split("\\").join("/")}`, readFileSync(path, "utf8"));
    }
  }
  return { tree, modules };
}

// The effective pin's commit: the override `make` exports (PIG_UPSTREAM_COMMIT), else the committed pin.
function pinnedCommit() {
  if (process.env.PIG_UPSTREAM_COMMIT) return process.env.PIG_UPSTREAM_COMMIT;
  const source = readFileSync(resolve(sdk, "../../internal/coding/pigversion/pigversion.go"), "utf8");
  return source.match(/const UpstreamCommit = "([0-9a-f]{40})"/)?.[1];
}

function publishedTree() {
  const manifest = JSON.parse(readFileSync(join(sdk, "package.json"), "utf8"));
  const version = manifest.peerDependencies["@earendil-works/pi-coding-agent"];
  const file = join(sdk, "testdata", `published-${version}.declarations.json.gz`);
  assert.ok(existsSync(file), `no snapshot of Pi ${version}: run node scripts/freeze-published-declarations.mjs (make vendor-sdk-ts-declarations)`);
  const bytes = readFileSync(file);
  assert.equal(createHash("sha256").update(bytes).digest("hex"), readFileSync(`${file}.sha256`, "utf8").trim(), `${file} does not match its recorded sha256`);
  const snapshot = JSON.parse(gunzipSync(bytes).toString("utf8"));
  assert.equal(snapshot.version, version, `${file} holds Pi ${snapshot.version}`);
  return new Map(Object.entries(snapshot.files));
}

// failures applies the named differences: public rows not named, and names that match no public row.
export function failures(rows, named) {
  const keys = new Set(named.map((entry) => entry.key));
  const open = rows.public.filter((row) => !keys.has(row.key)).map(describe);
  const live = new Set(rows.public.map((row) => row.key));
  const stale = named.filter((entry) => !live.has(entry.key)).map((entry) => `${entry.key}: named as different (${entry.reason}) but equal`);
  return [...open, ...stale];
}

test("the pinned source build's exported declarations equal the published release", (t) => {
  const { tree, modules } = installedTree();
  const commit = pinnedCommit();
  const stamp = join(dirname(modules), ".pig-pi-oracle.json");
  if (process.env.PIG_PARITY_PI_PUBLISHED !== "1") {
    assert.ok(existsSync(stamp), `${modules} is not a pinned source build (no ${stamp}); run make parity-deps without PIG_PARITY_PI_PUBLISHED`);
    assert.equal(JSON.parse(readFileSync(stamp, "utf8")).commit, commit, `${modules} is not the build of the pinned commit ${commit}`);
  }
  const rows = compare(publishedTree(), tree);
  for (const row of rows.other) t.diagnostic(`not exported, differs: ${describe(row)}`);
  const found = failures(rows, NAMED_DIFFERENCES);
  assert.deepEqual(found, [], `the pinned build's declarations differ from the published release:\n${found.join("\n")}`);
});

// Fixtures: two tiny trees laid out as the real ones.
function fixture(overrides = {}) {
  const tree = new Map();
  const pkg = (name, extra = {}) => tree.set(`@earendil-works/${name}/package.json`, JSON.stringify({ name: `@earendil-works/${name}`, types: "./dist/index.d.ts", ...extra }));
  for (const name of PACKAGES) pkg(name);
  tree.set("@earendil-works/pi-ai/dist/index.d.ts", "export * from './types.js';\nexport declare const LIMIT = 8;\n");
  tree.set("@earendil-works/pi-ai/dist/types.d.ts", "export interface Message { role: string; text?: string }\nexport interface Unused { a: number }\n");
  tree.set("@earendil-works/pi-ai/dist/extra.d.ts", "export declare const HIDDEN = 1;\n");
  tree.set("@earendil-works/pi-coding-agent/dist/index.d.ts", "import type { Message } from '@earendil-works/pi-ai';\nexport declare class Session {\n  /** a comment */\n  run(m: Message): void;\n}\n");
  for (const name of ["pi-tui", "pi-agent-core"]) tree.set(`@earendil-works/${name}/dist/index.d.ts`, "export {};\n");
  for (const [path, text] of Object.entries(overrides)) {
    if (text === null) tree.delete(path);
    else tree.set(path, text);
  }
  return tree;
}

test("a comment, and a declaration nothing exported mentions, do not fail", () => {
  const rows = compare(fixture(), fixture({
    "@earendil-works/pi-coding-agent/dist/index.d.ts": "import type { Message } from '@earendil-works/pi-ai';\nexport declare class Session {\n  /** another comment */\n  run(m: Message): void;\n}\n",
    "@earendil-works/pi-ai/dist/extra.d.ts": "export declare const HIDDEN = 2;\n",
  }));
  assert.deepEqual(rows.public, []);
  assert.deepEqual(rows.other.map(describe), ["@earendil-works/pi-ai/dist/extra.d.ts#HIDDEN (const) changed"]);
});

test("a one-line change of an exported declaration fails and names it", () => {
  const rows = compare(fixture(), fixture({ "@earendil-works/pi-ai/dist/index.d.ts": "export * from './types.js';\nexport declare const LIMIT = 9;\n" }));
  assert.deepEqual(failures(rows, []), ["@earendil-works/pi-ai/dist/index.d.ts#LIMIT (const) changed"]);
});

test("a member of an exported class, a type an exported class mentions, and a private member are public differences", () => {
  const rows = compare(fixture(), fixture({
    "@earendil-works/pi-ai/dist/types.d.ts": "export interface Message { role: string; text?: number }\nexport interface Unused { a: number }\n",
    "@earendil-works/pi-coding-agent/dist/index.d.ts": "import type { Message } from '@earendil-works/pi-ai';\nexport declare class Session {\n  private _kept;\n  run(m: Message): void;\n}\n",
  }));
  assert.deepEqual(failures(rows, []), [
    "@earendil-works/pi-ai/dist/types.d.ts#Message (interface) changed: member text (property) changed",
    "@earendil-works/pi-coding-agent/dist/index.d.ts#Session (class) changed: member _kept (private property) added",
  ]);
});

test("a declaration of a file only one side has is added or removed", () => {
  const rows = compare(fixture(), fixture({ "@earendil-works/pi-ai/dist/types.d.ts": null, "@earendil-works/pi-ai/dist/index.d.ts": "export declare const LIMIT = 8;\n" }));
  assert.deepEqual(failures(rows, []), [
    "@earendil-works/pi-ai/dist/types.d.ts#Message (interface) removed",
    "@earendil-works/pi-ai/dist/types.d.ts#Unused (interface) removed",
  ]);
});

test("a named difference excuses exactly its declaration, and fails once it is equal", () => {
  const changed = compare(fixture(), fixture({ "@earendil-works/pi-ai/dist/index.d.ts": "export * from './types.js';\nexport declare const LIMIT = 9;\n" }));
  const named = [{ key: "@earendil-works/pi-ai/dist/index.d.ts#LIMIT", reason: "test" }];
  assert.deepEqual(failures(changed, named), []);
  assert.deepEqual(failures(changed, [{ key: "@earendil-works/pi-ai/dist/index.d.ts#OTHER", reason: "test" }]).length, 2);
  assert.equal(failures(compare(fixture(), fixture()), named).length, 1);
});

test("the pinned build differs from itself nowhere", () => {
  const { tree } = installedTree();
  assert.deepEqual(compare(tree, tree), { public: [], other: [] });
});
