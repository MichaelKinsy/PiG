// pi-pin.d.ts, the bundle of the pinned build's declarations: its exported declarations equal the pinned build's, every specifier the
// packages expose has a block, and the committed file is what the pinned build generates.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { blockSpecifiers, compare, describe, normalized, specifiers, unbundle } from "./declarations.mjs";
import { bundle, generate, jsonBodies, peerProblems, peers, TARGET } from "./pin-bundle.mjs";
import { pinnedBuild, published, sdk } from "./trees.mjs";

// equal lists what differs between a tree and a bundle read back as a tree.
function differences(tree, text) {
  const rows = compare(normalized(tree), normalized(unbundle(text, tree)));
  return [...rows.public, ...rows.other].map(describe);
}

// missing lists the specifiers a tree's packages expose that a bundle declares no block for.
function missing(tree, text) {
  const declared = new Set(blockSpecifiers(text));
  return [...specifiers(tree).keys()].filter((specifier) => !declared.has(specifier));
}

test("the bundle's declarations equal the pinned build's", () => {
  const { tree } = pinnedBuild();
  assert.deepEqual(differences(tree, readFileSync(TARGET, "utf8")), []);
});

test("every specifier the pinned build's packages expose has a block", () => {
  const { tree } = pinnedBuild();
  assert.deepEqual(missing(tree, readFileSync(TARGET, "utf8")), []);
});

test("pi-pin.d.ts is what the pinned build generates", { skip: published && "the installed Pi is the published release, not the pinned build" }, () => {
  assert.ok(generate().text === readFileSync(TARGET, "utf8"), "pi-pin.d.ts is not what the installed pinned build generates: run make vendor-sdk-ts-pin-declarations");
});

test("package.json declares as peer dependencies exactly the packages the bundle imports, at Pi's ranges, and the lock agrees", () => {
  const manifest = JSON.parse(readFileSync(join(sdk, "package.json"), "utf8"));
  assert.deepEqual(peerProblems(manifest.peerDependencies, generate().peers), []);
  const lock = JSON.parse(readFileSync(join(sdk, "package-lock.json"), "utf8"));
  assert.deepEqual(lock.packages[""].peerDependencies, manifest.peerDependencies, "package-lock.json does not record package.json's peerDependencies: run make vendor-sdk-ts-pin-declarations");
});

test("the lock holds every required peer of its packages, which npm does not add in this folder (.npmrc legacy-peer-deps)", () => {
  const { packages } = JSON.parse(readFileSync(join(sdk, "package-lock.json"), "utf8"));
  const missing = [];
  for (const [key, entry] of Object.entries(packages)) {
    if (key === "") continue;
    for (const name of Object.keys(entry.peerDependencies ?? {})) {
      if (entry.peerDependenciesMeta?.[name]?.optional) continue;
      let at = key;
      let found = false;
      while (!found && at !== "") {
        found = `${at}/node_modules/${name}` in packages;
        at = at.slice(0, Math.max(0, at.lastIndexOf("/node_modules/")));
      }
      if (!found && !(`node_modules/${name}` in packages)) missing.push(`${key} needs ${name}`);
    }
  }
  assert.deepEqual(missing, []);
});

test("a peer the bundle needs and lacks, a peer it does not import, and a range that is not Pi's are each a problem", () => {
  const packages = new Map([
    ["@earendil-works/pi-ai", { manifest: { dependencies: { openai: "7.19.0" } } }],
    ["@earendil-works/pi-tui", { manifest: { dependencies: { marked: "18.0.11" } } }],
  ]);
  const external = new Map([["openai/resources/chat/completions.js", ["@earendil-works/pi-ai"]], ["marked", ["@earendil-works/pi-tui"]], ["node:fs", ["@earendil-works/pi-tui"]], ["events", ["@earendil-works/pi-tui"]]]);
  const wanted = peers(external, packages);
  assert.deepEqual([...wanted], [["marked", "18.0.11"], ["openai", "7.19.0"]]);
  const pi = { "@earendil-works/pi-coding-agent": "9.9.9" };
  assert.deepEqual(peerProblems({ ...pi, marked: "18.0.11", openai: "7.19.0" }, wanted), []);
  assert.deepEqual(peerProblems({ ...pi, marked: "18.0.11" }, wanted), ["the bundle imports openai, which is no peer dependency"]);
  assert.deepEqual(peerProblems({ ...pi, marked: "18.0.11", openai: "7.19.0", typebox: "1.3.27" }, wanted), ["typebox is a peer dependency the bundle does not import"]);
  assert.deepEqual(peerProblems({ ...pi, marked: "^18.0.11", openai: "7.19.0" }, wanted), ["the peer dependency marked is ^18.0.11; Pi declares 18.0.11"]);
  assert.throws(() => peers(new Map([["zod", ["@earendil-works/pi-ai"]]]), packages), /@earendil-works\/pi-ai imports zod and declares no dependency on zod/);
});

// A tiny build laid out as the real one: a mapped package with a subpath, a private file, an augmentation and a JSON import; a package
// with two `*` patterns; a package without an exports map.
function fixture(overrides = {}) {
  const tree = new Map();
  const pkg = (name, fields) => tree.set(`@earendil-works/${name}/package.json`, JSON.stringify({ name: `@earendil-works/${name}`, version: "9.9.9", type: "module", ...fields }));
  pkg("pi-coding-agent", { exports: { ".": { types: "./dist/index.d.ts", import: "./dist/index.js" }, "./hooks": { import: { types: "./dist/hooks.d.ts" } }, "./rpc-entry": { import: "./dist/rpc.js" } } });
  pkg("pi-ai", { exports: { ".": { types: "./dist/index.d.ts" }, "./api/*": { types: "./dist/api/*.d.ts" }, "./utils/*": { types: "./dist/utils/*.d.ts" } } });
  pkg("pi-tui", { types: "./dist/index.d.ts" });
  pkg("pi-agent-core", { exports: { ".": { types: "./dist/index.d.ts" } } });
  const files = {
    "pi-coding-agent/dist/index.d.ts": 'export { Session } from "./core/session.ts";\nexport type { Message } from "@earendil-works/pi-ai";\nexport declare const VERSION: string;\n',
    "pi-coding-agent/dist/core/session.d.ts": [
      'import type { Message } from "@earendil-works/pi-ai";',
      'import type { TSchema } from "typebox";',
      'import type { Component } from "@earendil-works/pi-tui";',
      'import values from "./data/models.json";',
      "/** A session. */",
      "export declare class Session {",
      "    private _restored;",
      '    run(message: Message, schema: TSchema): import("./helper.js").Helper;',
      "    view: Component;",
      "    models: typeof values;",
      "}",
      "declare class Hidden {",
      "}",
      'declare module "@earendil-works/pi-agent-core" {',
      "    interface Extras {",
      "        session: Session;",
      "    }",
      "}",
      "export {};",
      "//# sourceMappingURL=session.d.ts.map",
      "",
    ].join("\n"),
    "pi-coding-agent/dist/core/helper.d.ts": "export interface Helper {\n    h: number;\n}\n",
    "pi-coding-agent/dist/core/data/models.json": '{"chat:one":{"id":"one","cost":1.5,"input":["text"],"level":null}}',
    "pi-coding-agent/dist/hooks.d.ts": "export declare function hook(): void;\n",
    "pi-coding-agent/dist/unreached.d.ts": "export declare const UNREACHED = 1;\n",
    "pi-ai/dist/index.d.ts": "export interface Message {\n    role: string;\n}\n",
    "pi-ai/dist/api/chat.d.ts": "export declare const chat: number;\n",
    "pi-ai/dist/utils/text.d.ts": "export declare const text: number;\n",
    "pi-tui/dist/index.d.ts": 'export * from "./component.js";\n',
    "pi-tui/dist/component.d.ts": "export interface Component {\n    render(): string[];\n}\n",
    "pi-agent-core/dist/index.d.ts": "export interface Extras {\n}\n",
  };
  for (const [path, text] of Object.entries({ ...files, ...overrides })) {
    if (text === null) tree.delete(`@earendil-works/${path}`);
    else tree.set(`@earendil-works/${path}`, text);
  }
  return tree;
}

const SESSION = "pi-pin:@earendil-works/pi-coding-agent/dist/core/session";

// block cuts one block out of a bundle.
function block(text, name) {
  const start = text.indexOf(`declare module ${JSON.stringify(name)} {\n`);
  assert.notEqual(start, -1, `no block ${name}`);
  const next = text.indexOf("\ndeclare module ", start + 1);
  return text.slice(start, next === -1 ? text.length : next + 1);
}

test("a bundle has a block per exposed specifier, per file of a package without a map, and per private file reached", () => {
  const { text } = bundle(fixture(), "// header");
  assert.deepEqual(blockSpecifiers(text), [
    "@earendil-works/pi-agent-core",
    "@earendil-works/pi-ai",
    "@earendil-works/pi-ai/api/chat",
    "@earendil-works/pi-ai/utils/text",
    "@earendil-works/pi-coding-agent",
    "@earendil-works/pi-coding-agent/hooks",
    "@earendil-works/pi-tui",
    "@earendil-works/pi-tui/dist/component.js",
    "pi-pin:@earendil-works/pi-coding-agent/dist/core/data/models.json",
    "pi-pin:@earendil-works/pi-coding-agent/dist/core/helper",
    SESSION,
  ]);
  assert.ok(text.startsWith("// header\ndeclare module "));
  assert.deepEqual(missing(fixture(), text), []);
});

test("a block is its file without `declare` and the source map comment, its specifiers the block names", () => {
  const { text, external } = bundle(fixture(), "// header");
  assert.equal(block(text, SESSION), [
    `declare module "${SESSION}" {`,
    'import type { Message } from "@earendil-works/pi-ai";',
    'import type { TSchema } from "typebox";',
    'import type { Component } from "@earendil-works/pi-tui";',
    'import values from "pi-pin:@earendil-works/pi-coding-agent/dist/core/data/models.json";',
    "/** A session. */",
    "export class Session {",
    "    private _restored;",
    '    run(message: Message, schema: TSchema): import("pi-pin:@earendil-works/pi-coding-agent/dist/core/helper").Helper;',
    "    view: Component;",
    "    models: typeof values;",
    "}",
    "class Hidden {",
    "}",
    'module "@earendil-works/pi-agent-core" {',
    "    interface Extras {",
    "        session: Session;",
    "    }",
    "}",
    "export {};",
    "}",
    "",
  ].join("\n"));
  assert.equal(block(text, "@earendil-works/pi-tui"), 'declare module "@earendil-works/pi-tui" {\nexport * from "@earendil-works/pi-tui/dist/component.js";\n}\n');
  assert.deepEqual([...external], [["typebox", ["@earendil-works/pi-coding-agent"]]]);
});

test("a JSON file is a block that declares the type the compiler gives the JSON module", () => {
  const bodies = jsonBodies(new Map([["a.json", '{"chat:one":{"id":"one","cost":1.5,"input":["text"],"level":null}}']]));
  assert.equal(bodies.get("a.json").replace(/\s+/g, " "), 'const value: { "chat:one": { id: string; cost: number; input: string[]; level: null; }; }; export default value;');
});

test("a bundle read back equals its build, and a removed block, a changed member and a dropped subpath do not", () => {
  const tree = fixture();
  const { text } = bundle(tree, "// header");
  assert.deepEqual(differences(tree, text), []);
  assert.deepEqual(differences(tree, text.replace(block(text, "pi-pin:@earendil-works/pi-coding-agent/dist/core/helper"), "")), [
    "@earendil-works/pi-coding-agent/dist/core/helper.d.ts#Helper (interface) removed",
  ]);
  assert.deepEqual(differences(tree, text.replace("    view: Component;", "    view: Component | undefined;")), [
    "@earendil-works/pi-coding-agent/dist/core/session.d.ts#Session (class) changed: member view (property) changed",
  ]);
  const dropped = text.replace(block(text, "@earendil-works/pi-ai/utils/text"), "");
  assert.deepEqual(missing(tree, dropped), ["@earendil-works/pi-ai/utils/text"]);
  assert.deepEqual(differences(tree, dropped), ["@earendil-works/pi-ai/dist/utils/text.d.ts#text (const) removed"]);
});

test("a build the bundle cannot carry fails the generation and says why", () => {
  const fails = (overrides, message) => assert.throws(() => bundle(fixture(overrides), "// header"), message);
  fails({ "pi-coding-agent/dist/core/helper.d.ts": null }, /session\.d\.ts imports \.\/helper\.js, which names no declaration file of the build/);
  fails({ "pi-coding-agent/dist/hooks.d.ts": 'export type { Message } from "@earendil-works/pi-ai/dist/index.js";\n' }, /imports @earendil-works\/pi-ai\/dist\/index\.js, which names no declaration file the packages expose/);
  fails({ "pi-coding-agent/dist/hooks.d.ts": "declare const hook: number;\nexport = hook;\n" }, /hooks\.d\.ts has an export assignment/);
  fails({ "pi-coding-agent/dist/hooks.d.ts": '/// <reference types="node" />\nexport declare const hook: number;\n' }, /hooks\.d\.ts has a reference directive/);
  fails({ "pi-coding-agent/dist/hooks.d.ts": "declare const hook: number;\n" }, /hooks\.d\.ts is a script/);
  fails({ "pi-coding-agent/dist/hooks.d.ts": 'import { a } from "./core/data/models.json";\nexport declare const hook: typeof a;\n' }, /other than as a default import/);
  fails({ "pi-ai/dist/api/chat.d.ts": 'export * from "../index.js";\n', "pi-ai/package.json": JSON.stringify({ name: "@earendil-works/pi-ai", exports: { ".": { types: "./dist/index.d.ts" }, "./again": { types: "./dist/index.d.ts" } } }) }, /@earendil-works\/pi-ai and @earendil-works\/pi-ai\/again expose one file/);
});
