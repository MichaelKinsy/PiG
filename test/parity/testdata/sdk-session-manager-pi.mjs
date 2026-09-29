// Run the four original SDK SessionManager test bodies against published Pi production modules.
import assert from "node:assert/strict";
import { readFileSync, existsSync, realpathSync } from "node:fs";
import { resolve, join } from "node:path";
import { pathToFileURL } from "node:url";
import ts from "../interface-extractor/node_modules/typescript/lib/typescript.js";
import { resolvePackage, root } from "./session-harness-pi.mjs";
const production = path => pathToFileURL(join(root, "dist", path)).href;
const state = { cases: [], before: [], after: [], session: null, options: null, outputs: [] };
globalThis.__pigSDKManager = state;
const moduleURL = code => "data:text/javascript;base64," + Buffer.from(code).toString("base64");
const framework = moduleURL(`import assert from "node:assert/strict";const state=globalThis.__pigSDKManager;
export function describe(_name,run){run();} export function beforeEach(run){state.before.push(run);} export function afterEach(run){state.after.push(run);} export function it(name,run){state.cases.push({name,run});}
export function expect(value){return {toBe:expected=>assert.strictEqual(value,expected),toEqual:expected=>assert.deepStrictEqual(value,expected),toBeTruthy:()=>assert.ok(value),toContain:expected=>assert.ok(value.includes(expected))};}
`);
const sdk = moduleURL(`import {createAgentSession as original} from ${JSON.stringify(production("core/sdk.js"))};
export async function createAgentSession(options){const result=await original(options);const state=globalThis.__pigSDKManager;state.options=options;state.session=result.session;state.outputs=[];const bash=result.session.agent.state.tools.find(t=>t.name==="bash");if(bash){const execute=bash.execute;bash.execute=async(...args)=>{const result=await execute(...args);state.outputs.push(result.content.filter(b=>b.type==="text").map(b=>b.text).join(""));return result;};}return result;}
`);
const path = resolve(".upstream/current/packages/coding-agent/test/sdk-session-manager.test.ts");
const source = readFileSync(path, "utf8");
let code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ESNext, module: ts.ModuleKind.ESNext } }).outputText;
code = code.replace(/from "([^"]+)"/g, (_, specifier) => `from ${JSON.stringify(specifier === "vitest" ? framework : specifier === "../src/core/sdk.ts" ? sdk : specifier === "../src/core/session-manager.ts" ? production("core/session-manager.js") : specifier.startsWith("node:") ? specifier : resolvePackage(specifier))}`);
await import(moduleURL(code));
assert.equal(state.cases.length, [...source.matchAll(/\bit\(/g)].length);
try {
  for (let i = 0; i < state.cases.length; i++) {
    for (const before of state.before) await before();
    try {
      await state.cases[i].run();
      const s = state.session, o = state.options, m = s.sessionManager;
      let record;
      if (i === 0) {
        const safe = `--${o.cwd.replace(/^[/\\]/, "").replace(/[/\\:]/g, "-")}--`;
        const dir = join(o.agentDir, "sessions", safe);
        record = [m.getSessionDir() === dir, m.getSessionFile().startsWith(dir + "/"), m.isPersisted(), existsSync(m.getSessionFile())];
      }
      if (i === 1) record = [m === o.sessionManager, m.isPersisted(), m.getSessionFile() === undefined];
      if (i === 2) {
        const cwd = o.sessionManager.getCwd();
        record = [m === o.sessionManager, s.systemPrompt.includes(`<cwd>\n${cwd}\n</cwd>`), realpathSync(state.outputs.at(-1).trim()) === realpathSync(cwd)];
      }
      if (i === 3) {
        const output = state.outputs.at(-1).trim().split("\n");
        record = [output[0] === s.sessionId, output[1] === s.sessionFile, ...output.slice(2)];
      }
      console.log("SDK_SESSION_MANAGER " + JSON.stringify([i + 1, record]));
    } catch (error) {
      throw new Error(state.cases[i].name, { cause: error });
    } finally {
      for (const after of state.after) await after();
    }
  }
} finally {
  delete globalThis.__pigSDKManager;
}
