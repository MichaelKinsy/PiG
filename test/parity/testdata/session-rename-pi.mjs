// Execute the original three tests against the published Pi 0.87.1 components.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import ts from "../interface-extractor/node_modules/typescript/lib/typescript.js";
const root = process.env.PI_PACKAGE_ROOT ?? resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
assert.equal(JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version, "1.0.0");
const packageURL = pathToFileURL(join(root, "package.json")).href;
const moduleURL = code => "data:text/javascript;base64," + Buffer.from(code).toString("base64");
const state = { cases: [], before: [], each: [] };
globalThis.__pigRename = state;
const framework = moduleURL(`import assert from "node:assert/strict";const s=globalThis.__pigRename;
export const describe=(_name,run)=>run(),beforeAll=run=>s.before.push(run),beforeEach=run=>s.each.push(run),it=(name,run)=>s.cases.push({name,run});
export const vi={fn:fn=>{const wrapped=(...args)=>{wrapped.calls.push(args);return fn(...args);};wrapped.calls=[];return wrapped;}};
export function expect(value){return {toContain:expected=>assert.ok(value.includes(expected)),not:{toContain:expected=>assert.ok(!value.includes(expected))},toHaveBeenCalledTimes:expected=>assert.equal(value.calls.length,expected),toHaveBeenCalledWith:(...args)=>assert.ok(value.calls.some(call=>{try{assert.deepStrictEqual(call,args);return true;}catch{return false;}}))};}
`);
const source = readFileSync(".upstream/current/packages/coding-agent/test/session-selector-rename.test.ts", "utf8");
const sites = source.split("\n").flatMap((line, i) => /\bit\(/.test(line) ? [i + 1] : []);
let code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ESNext, module: ts.ModuleKind.ESNext } }).outputText;
code = code.replace(/from "([^"]+)"/g, (_, specifier) => {
  const target = specifier === "vitest" ? framework : specifier.startsWith("../src/")
    ? pathToFileURL(join(root, "dist", specifier.slice(7).replace(/\.ts$/, ".js"))).href
    : import.meta.resolve(specifier, packageURL);
  return `from ${JSON.stringify(target)}`;
});
try {
  await import(moduleURL(code));
  assert.equal(state.cases.length, sites.length);
  for (const run of state.before) await run();
  for (const [index, test] of state.cases.entries()) {
    for (const run of state.each) await run();
    await test.run();
    console.log("SESSION_RENAME " + JSON.stringify([sites[index], test.name]));
  }
} finally { delete globalThis.__pigRename; }
await import("./session-rename-input-pi.mjs");
