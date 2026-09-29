import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join, resolve, relative } from "node:path";
import { pathToFileURL } from "node:url";
import ts from "../interface-extractor/node_modules/typescript/lib/typescript.js";

const root = process.env.PI_PACKAGE_ROOT ?? resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
const pin = readFileSync("internal/coding/pigversion/pigversion.go", "utf8").match(/UpstreamVersion = "([^"]+)"/)[1];
assert.equal(JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version, pin);
export const resolvePiPackage = specifier => import.meta.resolve(specifier, pathToFileURL(join(root, "package.json")).href);
const upstream = resolve(".upstream/current/packages/coding-agent");
const helpers = new Set(["test/suite/harness.ts", "test/utilities.ts", "test/model-runtime-test-utils.ts"]);
const cache = new Map();

// Only test helpers are transpiled. Production imports resolve to the unchanged pinned Pi package.
function helper(path) {
  if (cache.has(path)) return cache.get(path);
  assert(helpers.has(path), `unexpected test helper ${path}`);
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
      target = resolvePiPackage(specifier);
    }
    return `from ${JSON.stringify(target)}`;
  });
  const url = "data:text/javascript;base64," + Buffer.from(code).toString("base64");
  cache.set(path, url);
  return url;
}

export const { createHarness, getMessageText, getUserTexts } = await import(helper("test/suite/harness.ts"));
export const { fauxAssistantMessage, fauxToolCall, createAssistantMessageEventStream } = await import(resolvePiPackage("@earendil-works/pi-ai"));
export const { Type } = await import(resolvePiPackage("typebox"));
