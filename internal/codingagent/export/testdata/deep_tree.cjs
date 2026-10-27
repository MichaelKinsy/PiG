// Ports packages/coding-agent/test/export-html-deep-tree.test.ts (Pi #11225): the exported viewer builds, flattens and walks a
// single-child chain deeper than the call stack. argv[2] is the exported HTML; argv[3] the chain depth.
const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const { createContext, runInContext } = require("node:vm");

const html = readFileSync(process.argv[2], "utf8");
const depth = Number(process.argv[3]);
const sessionData = html.match(/<script id="session-data" type="application\/json">([^<]+)<\/script>/)?.[1];
const scripts = [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)].map((match) => match[1]);
const viewer = scripts.pop();
assert.ok(sessionData && viewer, "Missing exported session data or viewer script");
const context = createContext({
  atob,
  TextDecoder,
  URLSearchParams,
  document: { getElementById: () => ({ textContent: sessionData }), querySelector: () => null },
  window: { location: { search: "" } },
});
const api = runInContext(
  viewer.replace(
    "let treeNodeMap = null;",
    "let treeNodeMap = null; return { buildTree, buildActivePathIds, flattenTree, findNewestLeaf, getPath };",
  ),
  context,
);

const tree = api.buildTree();
assert.equal(tree.length, 1);
const leafId = `e${depth - 1}`;
const flat = api.flattenTree(tree, api.buildActivePathIds(leafId));
assert.equal(flat.length, depth);
assert.equal(flat[depth - 1].node.entry.id, leafId);
assert.equal(api.findNewestLeaf("e0"), leafId);
const path = api.getPath(leafId);
assert.equal(path.length, depth);
assert.equal(path[0].id, "e0");
assert.equal(path[depth - 1].id, leafId);
console.log("ok");
