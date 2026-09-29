import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join, resolve, relative } from "node:path";
import { pathToFileURL } from "node:url";
import ts from "../interface-extractor/node_modules/typescript/lib/typescript.js";

const root = process.env.PI_PACKAGE_ROOT ?? resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
const pin = readFileSync("internal/coding/pigversion/pigversion.go", "utf8").match(/UpstreamVersion = "([^"]+)"/)[1];
assert.equal(JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version, pin);
const resolvePackage = (specifier) => import.meta.resolve(specifier, pathToFileURL(join(root, "package.json")).href);
const upstream = resolve(".upstream/current/packages/coding-agent");
const helpers = new Set(["test/suite/harness.ts", "test/utilities.ts", "test/model-runtime-test-utils.ts"]);
const cache = new Map();
// Transpile only Pi's test helpers. Resolve every production import to the exact
// published Pi package; no production function is copied, extracted, or stubbed.
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
const { createHarness, getMessageText } = await import(helper("test/suite/harness.ts"));
const { fauxAssistantMessage, fauxToolCall } = await import(resolvePackage("@earendil-works/pi-ai"));
const { Type } = await import(resolvePackage("typebox"));

// packages/coding-agent/test/suite/regressions/5998-blocked-tool-terminate.test.ts:16.
{
  const executions = [];
  const h = await createHarness({
    tools: [{ name: "echo", label: "Echo", description: "Echo text back", parameters: Type.Object({ text: Type.String() }),
      execute: async () => { executions.push("echo"); throw new Error("tool should have been blocked"); } }],
    extensionFactories: [(pi) => pi.on("tool_call", async () => ({ block: true, reason: "Blocked by terminating policy", terminate: true }))],
  });
  try {
    h.setResponses([fauxAssistantMessage([fauxToolCall("echo", { text: "hello" })], { stopReason: "toolUse" }), fauxAssistantMessage("should not run")]);
    await h.session.prompt("hi");
    assert.equal(h.getPendingResponseCount(), 1);
    assert.deepEqual(executions, []);
    const results = h.session.messages.filter(m => m.role === "toolResult");
    assert.equal(results.length, 1);
    assert(results[0].isError);
    const end = h.eventsOfType("tool_execution_end")[0];
    assert.equal(end.result.terminate, true);
    console.log("TOOL_ADMISSION blocked " + JSON.stringify([getMessageText(results[0]), results[0].isError, end.result.terminate, 2 - h.getPendingResponseCount()]));
  } finally { h.cleanup(); }
}
// packages/coding-agent/test/suite/regressions/8935-parallel-preflight-abort.test.ts:16.
{
  const executions = [], preflights = [], resultHooks = [];
  const h = await createHarness({
    tools: [{ name: "external_write", label: "External write", description: "Perform an external write", parameters: Type.Object({ value: Type.String() }),
      execute: async (_id, args) => { executions.push(args.value); return { content: [{ type: "text", text: args.value }], details: { value: args.value } }; } }],
    extensionFactories: [(pi) => {
      pi.on("tool_call", async (event, ctx) => { preflights.push(event.input.value); if (event.input.value === "second") ctx.abort(); });
      pi.on("tool_result", async event => { resultHooks.push(event.toolCallId); });
    }],
  });
  try {
    h.setResponses([fauxAssistantMessage([fauxToolCall("external_write", { value: "first" }), fauxToolCall("external_write", { value: "second" })], { stopReason: "toolUse" })]);
    await h.session.prompt("run both writes");
    assert.deepEqual(preflights, ["first", "second"]);
    assert.deepEqual(executions, []);
    assert.deepEqual(resultHooks, []);
    const starts = h.eventsOfType("tool_execution_start").map(e => e.toolCallId);
    const ends = h.eventsOfType("tool_execution_end");
    assert.equal(starts.length, preflights.length);
    assert.deepEqual(ends.map(e => e.toolCallId).sort(), [...starts].sort());
    assert(ends.every(e => e.isError));
    const results = h.session.messages.filter(m => m.role === "toolResult");
    assert.deepEqual(results.map(m => m.toolCallId), starts);
    const texts = results.map(getMessageText);
    assert.deepEqual(texts, ["Operation aborted", "Operation aborted"]);
    console.log("TOOL_ADMISSION abort " + JSON.stringify([preflights, executions, resultHooks, texts]));
  } finally { h.cleanup(); }
}
