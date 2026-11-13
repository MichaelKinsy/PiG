// The differences between the pinned Pi source build (the oracle in node_modules) and the published release the peer dependency names
// are a report: an author type-checks against the pin (pi-pin.d.ts, gated by pin-bundle.test.mjs), and the report says what a runtime of
// the published release lacks. The published declarations are testdata/published-<version>.declarations.json.gz, not the network.
import assert from "node:assert/strict";
import test from "node:test";
import { compare, describe, PACKAGES } from "./declarations.mjs";
import { installedTree, pinnedBuild, publishedTree } from "./trees.mjs";

test("the pinned source build's differences from the published release are reported", (t) => {
  const { tree } = pinnedBuild();
  const release = publishedTree();
  const rows = compare(release.tree, tree);
  for (const row of rows.public) t.diagnostic(`the pin differs from Pi ${release.version}: ${describe(row)}`);
  for (const row of rows.other) t.diagnostic(`not exported, differs: ${describe(row)}`);
  if (rows.public.length === 0) t.diagnostic(`the pin's exported declarations equal Pi ${release.version}`);
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

test("a one-line change of an exported declaration is a public difference that names it", () => {
  const rows = compare(fixture(), fixture({ "@earendil-works/pi-ai/dist/index.d.ts": "export * from './types.js';\nexport declare const LIMIT = 9;\n" }));
  assert.deepEqual(rows.public.map(describe), ["@earendil-works/pi-ai/dist/index.d.ts#LIMIT (const) changed"]);
});

test("a member of an exported class, a type an exported class mentions, and a private member are public differences", () => {
  const rows = compare(fixture(), fixture({
    "@earendil-works/pi-ai/dist/types.d.ts": "export interface Message { role: string; text?: number }\nexport interface Unused { a: number }\n",
    "@earendil-works/pi-coding-agent/dist/index.d.ts": "import type { Message } from '@earendil-works/pi-ai';\nexport declare class Session {\n  private _kept;\n  run(m: Message): void;\n}\n",
  }));
  assert.deepEqual(rows.public.map(describe), [
    "@earendil-works/pi-ai/dist/types.d.ts#Message (interface) changed: member text (property) changed",
    "@earendil-works/pi-coding-agent/dist/index.d.ts#Session (class) changed: member _kept (private property) added",
  ]);
});

test("a declaration of a file only one side has is added or removed", () => {
  const rows = compare(fixture(), fixture({ "@earendil-works/pi-ai/dist/types.d.ts": null, "@earendil-works/pi-ai/dist/index.d.ts": "export declare const LIMIT = 8;\n" }));
  assert.deepEqual(rows.public.map(describe), [
    "@earendil-works/pi-ai/dist/types.d.ts#Message (interface) removed",
    "@earendil-works/pi-ai/dist/types.d.ts#Unused (interface) removed",
  ]);
});

test("each `*` pattern of an exports map exposes its files", () => {
  const tree = fixture({
    "@earendil-works/pi-ai/package.json": JSON.stringify({ name: "@earendil-works/pi-ai", exports: { ".": { types: "./dist/index.d.ts" }, "./api/*": { types: "./dist/api/*.d.ts" }, "./utils/*": { types: "./dist/utils/*.d.ts" } } }),
    "@earendil-works/pi-ai/dist/api/chat.d.ts": "export declare const CHAT = 1;\n",
    "@earendil-works/pi-ai/dist/utils/text.d.ts": "export declare const TEXT = 1;\n",
  });
  const rows = compare(tree, new Map([...tree, ["@earendil-works/pi-ai/dist/api/chat.d.ts", "export declare const CHAT = 2;\n"], ["@earendil-works/pi-ai/dist/utils/text.d.ts", "export declare const TEXT = 2;\n"]]));
  assert.deepEqual(rows.public.map(describe), ["@earendil-works/pi-ai/dist/api/chat.d.ts#CHAT (const) changed", "@earendil-works/pi-ai/dist/utils/text.d.ts#TEXT (const) changed"]);
});

test("the pinned build differs from itself nowhere", () => {
  const { tree } = installedTree();
  assert.deepEqual(compare(tree, tree), { public: [], other: [] });
});
