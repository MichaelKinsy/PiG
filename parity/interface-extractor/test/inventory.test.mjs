import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { compareInventories, extractInventory, stableSourcePath, verifyPublishedSources } from "../src/inventory.mjs";

const VERSION = fs.readFileSync(new URL("../../../coding/pigversion/pigversion.go", import.meta.url), "utf8").match(/const UpstreamVersion = "([^"]+)"/)[1];

function write(root, relative, content) {
  const file = path.join(root, relative);
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, content);
}

function manifest(name, extra = {}) {
  return JSON.stringify({ name, version: VERSION, ...extra }, null, 2);
}

function buildFixture() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "pig-interface-inventory-"));
  const source = path.join(root, "source");
  const published = path.join(root, "published", "coding-agent");

  write(source, "packages/agent/package.json", manifest("@earendil-works/pi-agent-core", {
    exports: {
      ".": { types: "./dist/index.d.ts", import: "./dist/index.js" },
      "./node": { types: "./dist/node.d.ts", import: "./dist/node.js" },
    },
  }));
  write(source, "packages/agent/src/index.ts", `export class Session<T = string> {\n  #secret = "hidden";\n  constructor(readonly id: T) {}\n  send(value: T): Promise<void> { return Promise.resolve(); }\n}\n`);
  write(source, "packages/agent/src/node.ts", `export function nodeOnly(signal?: AbortSignal): void {}\n`);

  write(source, "packages/ai/package.json", manifest("@earendil-works/pi-ai", {
    exports: {
      ".": { types: "./dist/index.d.ts", import: "./dist/index.js" },
      "./compat": { types: "./dist/compat.d.ts", import: "./dist/compat.js" },
      "./providers/*": { types: "./dist/providers/*.d.ts", import: "./dist/providers/*.js" },
    },
  }));
  write(source, "packages/ai/src/index.ts", `export { parse } from "./public.js";\nexport type { Box } from "./public.js";\n`);
  write(source, "packages/ai/src/public.ts", `export interface Box<T extends string = string> {\n  readonly value?: T;\n  run(input: number, ...rest: string[]): Promise<T>;\n  on(event: "start", handler: () => void): void;\n  on(event: "end", handler: (code: number) => void): void;\n}\nexport function parse(value: string): number;\nexport function parse(value: number): string;\nexport function parse(value: string | number): number | string { return value; }\n`);
  write(source, "packages/ai/src/compat.ts", `export type Compat = { strict: boolean };\n`);
  write(source, "packages/ai/src/providers/demo.ts", `export const provider = { id: "demo" } as const;\n`);

  write(source, "packages/coding-agent/package.json", manifest("@earendil-works/pi-coding-agent", {
    exports: {
      ".": { types: "./dist/index.d.ts", import: "./dist/index.js" },
      "./rpc-entry": { import: "./dist/bundle/rpc-entry.js" },
    },
  }));
  write(source, "packages/coding-agent/src/index.ts", `export type { Command } from "./types.js";\nexport function main(options?: { cwd?: string }): Promise<void> { return Promise.resolve(); }\n`);
  write(source, "packages/coding-agent/src/types.ts", `export interface Command { type: "prompt"; message: string; }\n`);
  write(source, "packages/coding-agent/src/rpc-entry.ts", `export function runRpc(...args: string[]): Promise<number> { return Promise.resolve(args.length); }\n`);

  write(source, "packages/tui/package.json", manifest("@earendil-works/pi-tui", {
    types: "./dist/index.d.ts",
    main: "./dist/index.js",
  }));
  write(source, "packages/tui/src/index.ts", `export class SelectList {\n  selected = 0;\n  handleInput(data: string): boolean { return data.length > 0; }\n}\n`);

  const roots = {
    agent: path.join(published, "node_modules", "@earendil-works", "pi-agent-core"),
    ai: path.join(published, "node_modules", "@earendil-works", "pi-ai"),
    coding: published,
    tui: path.join(published, "node_modules", "@earendil-works", "pi-tui"),
  };
  write(roots.agent, "package.json", manifest("@earendil-works/pi-agent-core", {
    exports: {
      ".": { types: "./dist/index.d.ts", import: "./dist/index.js" },
      "./node": { types: "./dist/node.d.ts", import: "./dist/node.js" },
    },
  }));
  write(roots.agent, "dist/index.d.ts", `export declare class Session<T = string> {\n  #private;\n  readonly id: T;\n  constructor(id: T);\n  send(value: T): Promise<void>;\n}\n`);
  write(roots.agent, "dist/node.d.ts", `export declare function nodeOnly(signal?: AbortSignal): void;\n`);

  write(roots.ai, "package.json", manifest("@earendil-works/pi-ai", {
    exports: {
      ".": { types: "./dist/index.d.ts", import: "./dist/index.js" },
      "./compat": { types: "./dist/compat.d.ts", import: "./dist/compat.js" },
      "./providers/*": { types: "./dist/providers/*.d.ts", import: "./dist/providers/*.js" },
    },
  }));
  write(roots.ai, "dist/index.d.ts", `export { parse } from "./public.js";\nexport type { Box } from "./public.js";\n`);
  write(roots.ai, "dist/public.d.ts", `export interface Box<T extends string = string> {\n  readonly value?: T;\n  run(input: number, ...rest: string[]): Promise<T>;\n  on(event: "start", handler: () => void): void;\n  on(event: "end", handler: (code: number) => void): void;\n}\nexport declare function parse(value: string): number;\nexport declare function parse(value: number): string;\n`);
  write(roots.ai, "dist/public.d.ts.map", JSON.stringify({
    version: 3,
    file: "public.d.ts",
    sourceRoot: "",
    sources: ["../src/public.ts"],
    sourcesContent: [fs.readFileSync(path.join(source, "packages/ai/src/public.ts"), "utf8")],
    names: [],
    mappings: "",
  }));
  write(roots.ai, "dist/compat.d.ts", `export type Compat = { strict: boolean };\n`);
  write(roots.ai, "dist/providers/demo.d.ts", `export declare const provider: { readonly id: "demo" };\n`);

  write(roots.coding, "package.json", manifest("@earendil-works/pi-coding-agent", {
    exports: {
      ".": { types: "./dist/index.d.ts", import: "./dist/index.js" },
      "./rpc-entry": { import: "./dist/bundle/rpc-entry.js" },
    },
  }));
  write(roots.coding, "dist/index.d.ts", `export type { Command } from "./types.js";\nexport declare function main(options?: { cwd?: string }): Promise<void>;\n`);
  write(roots.coding, "dist/types.d.ts", `export interface Command { type: "prompt"; message: string; }\n`);
  write(roots.coding, "dist/rpc-entry.d.ts", `export declare function runRpc(...args: string[]): Promise<number>;\n`);
  write(roots.coding, "dist/bundle/rpc-entry.js", "export {};\n");

  write(roots.tui, "package.json", manifest("@earendil-works/pi-tui", {
    types: "./dist/index.d.ts",
    main: "./dist/index.js",
  }));
  write(roots.tui, "dist/index.d.ts", `export declare class SelectList {\n  selected: number;\n  handleInput(data: string): boolean;\n}\n`);

  return { root, source, published, publishedAI: roots.ai };
}

function extractPair(fixture) {
  return {
    source: extractInventory({ origin: "source", root: fixture.source, upstreamVersion: VERSION }),
    published: extractInventory({ origin: "published", root: fixture.published, upstreamVersion: VERSION }),
  };
}

for (const origin of ["source", "published"]) {
  test(`${origin} inventory rejects a different tracked package name at the expected root`, (t) => {
    const fixture = buildFixture();
    t.after(() => fs.rmSync(fixture.root, { recursive: true, force: true }));
    const root = fixture[origin];
    const file = origin === "source"
      ? path.join(root, "packages/coding-agent/package.json")
      : path.join(root, "package.json");
    const pkg = JSON.parse(fs.readFileSync(file, "utf8"));
    pkg.name = "@earendil-works/pi-tui";
    fs.writeFileSync(file, JSON.stringify(pkg));
    assert.throws(
      () => extractInventory({ origin, root, upstreamVersion: VERSION }),
      /package name mismatch.*coding-agent.*pi-tui/,
    );
  });

  test(`${origin} inventory requires the exact release version without semver coercion`, (t) => {
    const fixture = buildFixture();
    t.after(() => fs.rmSync(fixture.root, { recursive: true, force: true }));
    const root = fixture[origin];
    const file = origin === "source"
      ? path.join(root, "packages/coding-agent/package.json")
      : path.join(root, "package.json");
    const pkg = JSON.parse(fs.readFileSync(file, "utf8"));
    for (const version of [undefined, null, "", `v${VERSION}`, `^${VERSION}`, `${VERSION}-rc.1`, `${VERSION}+local`, `${VERSION} `]) {
      fs.writeFileSync(file, JSON.stringify({ ...pkg, version }));
      assert.throws(() => extractInventory({ origin, root, upstreamVersion: VERSION }), /version skew/);
    }
  });
}

test("normalizes compiler dependency source paths", () => {
  const root = path.join(os.tmpdir(), "pig-interface-root");
  assert.equal(stableSourcePath(root, path.join(root, "packages", "agent", "src", "index.ts")), "packages/agent/src/index.ts");
  assert.equal(stableSourcePath(root, path.join(root, "..", "deps", "node_modules", "typescript", "lib", "lib.es5.d.ts")), "node_modules/typescript/lib/lib.es5.d.ts");
  assert.throws(() => stableSourcePath(root, path.join(root, "..", "vendor", "typescript", "lib.d.ts")), /stable dependency path/);
});


test("extracts package exports, re-exports, overloads, generics, members, and wildcard subpaths", () => {
  const fixture = buildFixture();
  const inventories = extractPair(fixture);
  const initialProblems = compareInventories(inventories.source, inventories.published);
  if (initialProblems.length) {
    const sourceProvider = inventories.source.interfaces.find((entry) => entry.id === "pkg:ai/providers/demo#provider");
    const publishedProvider = inventories.published.interfaces.find((entry) => entry.id === "pkg:ai/providers/demo#provider");
    assert.deepEqual(initialProblems, [], `${JSON.stringify({ sourceProvider, publishedProvider }, null, 2)}`);
  }

  const byID = new Map(inventories.source.interfaces.map((entry) => [entry.id, entry]));
  for (const entry of inventories.source.interfaces) {
    assert.ok(!entry.source.path.startsWith("../") && !path.isAbsolute(entry.source.path), `unstable source path: ${entry.source.path}`);
  }

  assert.ok(byID.has("pkg:agent/node#nodeOnly"));
  assert.ok(byID.has("pkg:ai/providers/demo#provider"), [...byID.keys()].filter((id) => id.includes("provider")).join("\n"));
  assert.equal(byID.get("pkg:ai/.#parse").shape.calls.length, 2);
  assert.equal(byID.get("pkg:ai/.#parse::call:0").parentId, "pkg:ai/.#parse");
  assert.equal(byID.get("pkg:ai/.#parse::call:1").role, "call-overload");

  const box = byID.get("pkg:ai/.#Box");
  assert.ok(byID.has("pkg:ai/.#Box::property:value"));
  assert.ok(byID.has("pkg:ai/.#Box::property:run::call:0"));
  assert.ok(byID.has("pkg:ai/.#Box::property:on::call:start"));
  assert.ok(byID.has("pkg:ai/.#Box::property:on::call:end"));
  assert.deepEqual(box.shape.typeParameters, [{ name: "T", constraint: "string", default: "string" }]);
  const valueProperty = box.shape.properties.find((property) => property.name === "value");
  assert.ok(valueProperty, JSON.stringify(box, null, 2));
  assert.equal(valueProperty.optional, true);
  const run = box.shape.properties.find((property) => property.name === "run");
  assert.equal(run.calls[0].parameters[1].rest, true);

  const session = byID.get("pkg:agent/.#Session");
  assert.equal(session.shape.constructs.length, 1);
  assert.ok(session.shape.properties.some((property) => property.name === "send"));
  assert.ok(byID.has("pkg:agent/.#Session::construct:0"));
  assert.ok(byID.has("pkg:agent/.#Session::property:send::call:0"));
});

test("source/published comparison fails when a published overload disappears", () => {
  const fixture = buildFixture();
  const declaration = path.join(fixture.publishedAI, "dist", "public.d.ts");
  fs.writeFileSync(declaration, fs.readFileSync(declaration, "utf8").replace("export declare function parse(value: number): string;\n", ""));
  const inventories = extractPair(fixture);
  const problems = compareInventories(inventories.source, inventories.published);
  assert.deepEqual(problems, [
    "pkg:ai/.#parse: source/published shape mismatch",
    "pkg:ai/.#parse::call:1: missing from published inventory",
  ]);
});

test("source/published comparison fails when a published interface member disappears", () => {
  const fixture = buildFixture();
  const declaration = path.join(fixture.publishedAI, "dist", "public.d.ts");
  fs.writeFileSync(declaration, fs.readFileSync(declaration, "utf8").replace("  run(input: number, ...rest: string[]): Promise<T>;\n", ""));
  const inventories = extractPair(fixture);
  const problems = compareInventories(inventories.source, inventories.published);
  assert.ok(problems.includes("pkg:ai/.#Box: source/published shape mismatch"), problems.join("\n"));
  assert.ok(problems.includes("pkg:ai/.#Box::property:run: missing from published inventory"), problems.join("\n"));
  assert.ok(problems.includes("pkg:ai/.#Box::property:run::call:0: missing from published inventory"), problems.join("\n"));
});

test("source/published comparison fails when a source export is absent from the package declaration", () => {
  const fixture = buildFixture();
  write(fixture.source, "packages/tui/src/index.ts", `export class SelectList { selected = 0; }\nexport const newPublicAction = "x";\n`);
  const inventories = extractPair(fixture);
  assert.ok(compareInventories(inventories.source, inventories.published).includes("pkg:tui/.#newPublicAction: missing from published inventory"));
});

test("published declaration maps prove byte-identical pinned source provenance", () => {
  const fixture = buildFixture();
  assert.deepEqual(
    verifyPublishedSources({ publishedRoot: fixture.published, sourceRoot: fixture.source, upstreamVersion: VERSION }),
    { checked: 1, problems: [] },
  );
});

test("published declaration provenance fails when the pinned source differs", () => {
  const fixture = buildFixture();
  write(fixture.source, "packages/ai/src/public.ts", "export const drift = true;\n");
  const result = verifyPublishedSources({ publishedRoot: fixture.published, sourceRoot: fixture.source, upstreamVersion: VERSION });
  assert.deepEqual(result.problems, ["@earendil-works/pi-ai: declaration source differs from mirror: src/public.ts"]);
});
