// Run the original runtime regression assertions against published Pi 0.87.1.
import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import {dirname, join, relative, resolve} from "node:path";
import {pathToFileURL} from "node:url";
import ts from "../interface-extractor/node_modules/typescript/lib/typescript.js";
import {root, resolvePackage} from "./session-harness-pi.mjs";

const files = {
  runtime: "suite/agent-session-runtime.test.ts",
  replaced: "suite/regressions/2860-replaced-session-context.test.ts",
  notify: "suite/regressions/5943-session-start-notify.test.ts",
  rebind: "suite/regressions/startup-session-rebind-duplicate-subscription.test.ts",
};
const family = process.argv[2] ?? "runtime";
assert.ok(Object.hasOwn(files, family), `unknown suite: ${family}`);
const selectedSite = process.argv[3] === undefined ? undefined : Number(process.argv[3]);
const state = {cases: [], after: [], observations: []};
globalThis.__pigRuntimeOriginalTests = state;
const moduleURL = code => "data:text/javascript;base64," + Buffer.from(code).toString("base64");
// The resource-order record also observes whether indexOf found the heading; -1 must not pass only because it is less than the chat index.
const framework = moduleURL(`
import assert from "node:assert/strict";
const state=globalThis.__pigRuntimeOriginalTests;
export function describe(_name, run) { run(); }
export function it(name, run) { state.cases.push({name, run}); }
export function afterEach(run) { state.after.push(run); }
export const vi = {fn(impl=()=>{}) { const calls=[]; const fn=(...args)=>{calls.push(args);return impl(...args);};fn.mock={calls};return fn; }};
export function expect(value) {
  const test=(invert=false)=>({
    get not(){return test(!invert);},
    get rejects(){return {async toThrow(message){await assert.rejects(value, error=>error.message.includes(message));}};},
    toBe(expected){state.observations.push(value);if(invert)assert.notStrictEqual(value,expected);else assert.strictEqual(value,expected);},
    toEqual(expected){if(invert)assert.notDeepStrictEqual(value,expected);else assert.deepStrictEqual(value,expected);},
    toContain(expected){state.observations.push(value.includes(expected));assert.equal(value.includes(expected),!invert);},
    toBeDefined(){assert.equal(value!==undefined,!invert);},
    toBeUndefined(){assert.equal(value===undefined,!invert);},
    toBeTruthy(){assert.equal(!!value,!invert);},
    toBeLessThan(expected){state.observations.push(value>=0,value<expected);assert.equal(value<expected,!invert);},
    toHaveBeenCalled(){assert.equal(value.mock.calls.length>0,!invert);},
    toHaveBeenCalledTimes(expected){assert.equal(value.mock.calls.length,expected);},
  });return test();
}
`);
const upstream = resolve(".upstream/current/packages/coding-agent");
const file = join(upstream, "test", files[family]);
const source = readFileSync(file, "utf8");
const sites = source.split("\n").flatMap((line, index) => /\bit\(/.test(line) ? [index + 1] : []);
let code = ts.transpileModule(source, {compilerOptions: {target: ts.ScriptTarget.ESNext, module: ts.ModuleKind.ESNext}}).outputText;
code = code.replace(/from "([^"]+)"/g, (_, specifier) => {
  let target = specifier;
  if (specifier === "vitest") target = framework;
  else if (specifier.endsWith("harness.ts")) target = new URL("./session-harness-pi.mjs", import.meta.url).href;
  else if (specifier.startsWith(".")) {
    const dependency = relative(upstream, resolve(dirname(file), specifier));
    assert.ok(dependency.startsWith("src/"), dependency);
    target = pathToFileURL(join(root, "dist", dependency.slice(4).replace(/\.ts$/, ".js"))).href;
  } else if (!specifier.startsWith("node:")) target = resolvePackage(specifier);
  return `from ${JSON.stringify(target)}`;
});
try {
  await import(moduleURL(code + `\n//# sourceURL=${file}\n`));
  assert.equal(state.cases.length, sites.length);
  if (selectedSite !== undefined) assert.ok(sites.includes(selectedSite), `no original case at ${selectedSite}`);
  for (const [index, test] of state.cases.entries()) {
    if (selectedSite !== undefined && selectedSite !== sites[index]) continue;
    state.observations = [];
    try {
      await test.run();
      if (selectedSite !== undefined) console.log("RUNTIME_OBSERVATION " + JSON.stringify([family, sites[index], state.observations]));
      else console.log("RUNTIME_ORIGINAL " + JSON.stringify([family, sites[index], test.name]));
    } catch (error) {
      throw new Error(`${files[family]}:${sites[index]} ${test.name}`, {cause: error});
    } finally {
      for (const cleanup of state.after) await cleanup();
    }
  }
} finally {
  delete globalThis.__pigRuntimeOriginalTests;
}
