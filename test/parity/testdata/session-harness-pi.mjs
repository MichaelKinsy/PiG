// Import only upstream test setup helpers; all production imports resolve to the pinned published Pi package.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join, resolve, relative } from "node:path";
import { pathToFileURL } from "node:url";
import ts from "../interface-extractor/node_modules/typescript/lib/typescript.js";

export const root = process.env.PI_PACKAGE_ROOT ?? resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
assert.equal(JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version, "1.1.0");
export const resolvePackage = specifier => import.meta.resolve(specifier, pathToFileURL(join(root, "package.json")).href);
export const production = path => import(pathToFileURL(join(root, "dist", path)));
const upstream = resolve(".upstream/current/packages/coding-agent");
const helpers = new Set(["test/suite/harness.ts", "test/utilities.ts", "test/model-runtime-test-utils.ts"]);
const cache = new Map();
function helper(path) {
  if (cache.has(path)) return cache.get(path);
  assert(helpers.has(path), `unexpected test dependency ${path}`);
  let code = ts.transpileModule(readFileSync(join(upstream, path), "utf8"), {
    compilerOptions: { target: ts.ScriptTarget.ESNext, module: ts.ModuleKind.ESNext },
  }).outputText;
  code = code.replace(/from "([^"]+)"/g, (_, specifier) => {
    let target = specifier;
    if (specifier.startsWith(".")) {
      const dependency = relative(upstream, resolve(upstream, dirname(path), specifier));
      target = dependency.startsWith("src/")
        ? pathToFileURL(join(root, "dist", dependency.slice(4).replace(/\.ts$/, ".js"))).href
        : helper(dependency);
    } else if (!specifier.startsWith("node:")) {
      target = resolvePackage(specifier);
    }
    return `from ${JSON.stringify(target)}`;
  });
  const url = "data:text/javascript;base64," + Buffer.from(code).toString("base64");
  cache.set(path, url);
  return url;
}
export const { createHarness, createTestUiContext, getMessageText, getUserTexts, getAssistantTexts } = await import(helper("test/suite/harness.ts"));
export const utilities = await import(helper("test/utilities.ts"));
export const modelTestUtils = await import(helper("test/model-runtime-test-utils.ts"));
