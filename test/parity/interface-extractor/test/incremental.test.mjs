import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";
import { extractIncremental, mergeParts, packageKeys, thirdPartyFiles, thirdPartyUnchanged, workspaceClosures } from "../src/incremental.mjs";
import { PACKAGE_LIST } from "../src/inventory.mjs";
import { scratchDir } from "./scratch.mjs";

let MODULES;
const DEPENDS = { "coding-agent": ["codemode", "ai"], durable: ["ai"], ai: [] };

function fixture() {
  const root = scratchDir("pig-incremental-");
  const source = path.join(root, "source");
  const published = path.join(root, "published");
  for (const { key, name } of PACKAGE_LIST) {
    const dependencies = Object.fromEntries((DEPENDS[key] ?? []).map((dep) => [PACKAGE_LIST.find((entry) => entry.key === dep).name, "*"]));
    fs.mkdirSync(path.join(source, "packages", key, "src"), { recursive: true });
    fs.writeFileSync(path.join(source, "packages", key, "package.json"), JSON.stringify({ name, dependencies }));
    fs.writeFileSync(path.join(source, "packages", key, "src", "index.ts"), `export const ${key.replace(/\W/g, "_")} = 1;\n`);
    fs.mkdirSync(path.join(published, key), { recursive: true });
    fs.writeFileSync(path.join(published, key, "index.d.ts"), "export {};\n");
  }
  const publishedRoots = new Map(PACKAGE_LIST.map(({ key }) => [key, path.join(published, key)]));
  const modules = path.join(root, "node_modules");
  MODULES = modules;
  fs.mkdirSync(path.join(modules, "typebox"), { recursive: true });
  fs.writeFileSync(path.join(modules, "typebox/index.d.ts"), "export type T = 1;\n");
  fs.mkdirSync(path.join(modules, "@earendil-works/pi-ai/dist"), { recursive: true });
  fs.writeFileSync(path.join(modules, "@earendil-works/pi-ai/dist/index.d.ts"), "export type W = 1;\n");
  return { root, source, published, publishedRoots, modules, cacheDir: path.join(root, "cache") };
}

function part(key, ids) {
  return { packages: [{ key, name: key }], interfaces: ids.map((id) => ({ id: `pkg:${key}/.#${id}`, package: key })) };
}

function fakeExtract(calls) {
  return async (key) => {
    calls.push(key);
    return { source: part(key, ["B", "a", "A"]), published: part(key, ["B", "a", "A"]), footprint: [path.join(MODULES, "typebox/index.d.ts"), path.join(MODULES, "@earendil-works/pi-ai/dist/index.d.ts")] };
  };
}

const header = { upstreamVersion: "1.1.0+x", typescriptVersion: "5.9.3" };

function run(f, calls) {
  return extractIncremental({ sourceRoot: f.source, publishedRoot: f.published, publishedRoots: f.publishedRoots, header, cacheDir: f.cacheDir, jobs: 4, nodeModulesRoot: f.modules, fingerprint: "fp", extract: fakeExtract(calls) });
}

test("closures follow workspace dependencies transitively", () => {
  const f = fixture();
  const closures = workspaceClosures(f.source);
  assert.deepEqual(closures.get("coding-agent"), ["ai", "codemode", "coding-agent"]);
  assert.deepEqual(closures.get("durable"), ["ai", "durable"]);
  assert.deepEqual(closures.get("codemode"), ["codemode"]);
});

test("the first run extracts every package and a rerun extracts none", async () => {
  const f = fixture();
  const first = [];
  const one = await run(f, first);
  assert.equal(first.length, PACKAGE_LIST.length);
  assert.deepEqual(one.reused, []);
  const second = [];
  const two = await run(f, second);
  assert.deepEqual(second, []);
  assert.equal(two.reused.length, PACKAGE_LIST.length);
  assert.deepEqual(two.source, one.source);
  assert.deepEqual(two.published, one.published);
});

test("a changed package re-extracts itself and the packages that reach it, and nothing else", async () => {
  const f = fixture();
  await run(f, []);
  fs.writeFileSync(path.join(f.source, "packages/ai/src/index.ts"), "export const ai = 2;\n");
  const calls = [];
  const result = await run(f, calls);
  assert.deepEqual([...calls].sort(), ["ai", "coding-agent", "durable"]);
  assert.ok(result.reused.includes("codemode") && result.reused.includes("tui"));
  fs.writeFileSync(path.join(f.source, "packages/codemode/src/index.ts"), "export const codemode = 2;\n");
  const next = [];
  await run(f, next);
  assert.deepEqual([...next].sort(), ["codemode", "coding-agent"]);
});

test("a changed published tree or extractor fingerprint re-extracts", async () => {
  const f = fixture();
  await run(f, []);
  fs.writeFileSync(path.join(f.published, "tui", "index.d.ts"), "export declare const x: 1;\n");
  const calls = [];
  await run(f, calls);
  assert.deepEqual([...calls].sort(), ["tui"]);
  const fourth = [];
  await extractIncremental({ sourceRoot: f.source, publishedRoot: f.published, publishedRoots: f.publishedRoots, header, cacheDir: f.cacheDir, jobs: 2, nodeModulesRoot: f.modules, fingerprint: "other", extract: fakeExtract(fourth) });
  assert.equal(fourth.length, PACKAGE_LIST.length, "a changed extractor fingerprint invalidates every package");
});

test("files the TypeScript program cannot read do not invalidate a package", async () => {
  const f = fixture();
  fs.mkdirSync(path.join(f.source, "packages/ai/src/providers/data"), { recursive: true });
  fs.writeFileSync(path.join(f.source, "packages/ai/src/providers/data/openrouter.json"), "{}");
  fs.writeFileSync(path.join(f.source, "packages/ai/README.md"), "one");
  await run(f, []);
  fs.writeFileSync(path.join(f.source, "packages/ai/src/providers/data/openrouter.json"), '{"models":1}');
  fs.writeFileSync(path.join(f.source, "packages/ai/README.md"), "two");
  const calls = [];
  await run(f, calls);
  assert.deepEqual(calls, [], "model data and docs are not inputs of the extraction (the program has no resolveJsonModule)");
  fs.writeFileSync(path.join(f.source, "packages/ai/package.json"), JSON.stringify({ name: "@earendil-works/pi-ai", exports: {} }));
  const manifest = [];
  await run(f, manifest);
  assert.deepEqual([...manifest].sort(), ["ai", "coding-agent", "durable"], "a manifest is an input");
});

test("a changed third-party file the program read re-extracts, one it did not read does not, a workspace dist file is not third-party", async () => {
  const f = fixture();
  await run(f, []);
  fs.mkdirSync(path.join(f.modules, "unread"), { recursive: true });
  fs.writeFileSync(path.join(f.modules, "unread/index.d.ts"), "export type U = 2;\n");
  fs.writeFileSync(path.join(f.modules, "@earendil-works/pi-ai/dist/index.d.ts"), "export type W = 2;\n");
  const untouched = [];
  await run(f, untouched);
  assert.deepEqual(untouched, [], "neither an unread package nor a workspace dist file invalidates the third-party footprint");
  fs.writeFileSync(path.join(f.modules, "typebox/index.d.ts"), "export type T = 2;\n");
  const calls = [];
  await run(f, calls);
  assert.equal(calls.length, PACKAGE_LIST.length, "every package read typebox");
});

test("the third-party filter keeps nested installs of workspace packages and drops workspace files", () => {
  const f = fixture();
  fs.mkdirSync(path.join(f.modules, "@earendil-works/pi-coding-agent/node_modules/@aws-sdk/core"), { recursive: true });
  fs.writeFileSync(path.join(f.modules, "@earendil-works/pi-coding-agent/node_modules/@aws-sdk/core/index.d.ts"), "x");
  fs.writeFileSync(path.join(f.modules, "@earendil-works/pi-coding-agent/dist.d.ts"), "y");
  const files = [
    path.join(f.modules, "typebox/index.d.ts"),
    path.join(f.modules, "@earendil-works/pi-coding-agent/node_modules/@aws-sdk/core/index.d.ts"),
    path.join(f.modules, "@earendil-works/pi-coding-agent/dist.d.ts"),
    path.join(f.root, "source/packages/ai/src/index.ts"),
  ];
  const recorded = thirdPartyFiles(f.modules, files);
  assert.deepEqual(recorded.map(([name]) => name), ["@earendil-works/pi-coding-agent/node_modules/@aws-sdk/core/index.d.ts", "typebox/index.d.ts"]);
  assert.equal(thirdPartyUnchanged(f.modules, recorded), true);
  fs.writeFileSync(path.join(f.modules, "typebox/index.d.ts"), "changed");
  assert.equal(thirdPartyUnchanged(f.modules, recorded), false);
  fs.rmSync(path.join(f.modules, "typebox"), { recursive: true });
  assert.equal(thirdPartyUnchanged(f.modules, recorded), false, "a removed file is a change");
});

test("the merge orders packages by key and interfaces by ID like a whole-tree extraction", () => {
  const merged = mergeParts(header, [part("tui", ["x"]), part("ai", ["b", "a"])]);
  assert.deepEqual(merged.packages.map((entry) => entry.key), ["ai", "tui"]);
  assert.deepEqual(merged.interfaces.map((entry) => entry.id), ["pkg:ai/.#a", "pkg:ai/.#b", "pkg:tui/.#x"]);
});

test("a failed extraction fails the run and publishes nothing for that package", async () => {
  const f = fixture();
  await assert.rejects(extractIncremental({
    sourceRoot: f.source, publishedRoot: f.published, publishedRoots: f.publishedRoots, header, cacheDir: f.cacheDir, jobs: 2, nodeModulesRoot: f.modules, fingerprint: "fp",
    extract: async (key) => { if (key === "ai") throw new Error("boom"); return { source: part(key, ["A"]), published: part(key, ["A"]), footprint: [] }; },
  }), /boom/);
  const keys = packageKeys({ sourceRoot: f.source, publishedRoot: f.published, publishedRoots: f.publishedRoots, fingerprint: "fp" });
  assert.equal(fs.existsSync(path.join(f.cacheDir, keys.get("ai"))), false);
});

