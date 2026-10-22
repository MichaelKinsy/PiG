package subprocess_test

import (
	"os"
	"path/filepath"
	"testing"
)

// Pi core/sdk.ts:175-439 constructs an independent AgentSession and restores its
// manager's transcript; it does not prompt or mutate the enclosing Session.
func TestNodeCreateAgentSessionMatchesPi(t *testing.T) {
	t.Parallel()
	runPinnedComparison(t, []string{"dist", "index.js"}, "pi-coding-agent.mjs", `
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
const pi = await import(process.argv[1]);
const pig = await import(process.argv[2]);
async function scene(m) {
  const dir = mkdtempSync(join(tmpdir(), "pig-child-sdk-"));
  try {
    const manager = m.SessionManager.inMemory(dir);
    const settings = m.SettingsManager.inMemory({ defaultThinkingLevel: "high", compaction: { enabled: false } });
    const model = { provider: "child", id: "independent", name: "Independent", api: "openai-completions", baseUrl: "http://unused.invalid", reasoning: false, input: ["text"], contextWindow: 32768, maxTokens: 1024, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } };
    const runtime = await m.ModelRuntime.create({ authPath: join(dir, "auth.json"), modelsPath: join(dir, "models.json"), allowModelNetwork: false });
    const resources = { extensions: [], errors: [], runtime: m.createExtensionRuntime() };
    const resourceLoader = {
      getExtensions: () => resources, getSkills: () => ({ skills: [], diagnostics: [] }),
      getPrompts: () => ({ prompts: [], diagnostics: [] }), getThemes: () => ({ themes: [], diagnostics: [] }),
      getAgentsFiles: () => ({ agentsFiles: [] }), getSystemPrompt: () => "Independent instructions",
      getSystemPromptSource: () => undefined, getAppendSystemPrompt: () => ["Child appendix"],
      getAppendSystemPromptSources: () => [], extendResources() {}, async reload() {},
    };
    const created = await m.createAgentSession({ cwd: dir, model, modelRuntime: runtime, sessionManager: manager, settingsManager: settings, resourceLoader, tools: ["read", "write"], excludeTools: ["write"] });
    const { session } = created;
    try {
      assert.ok(session instanceof m.AgentSession);
      assert.equal(session.sessionManager, manager);
      assert.equal(session.settingsManager, settings);
      assert.equal(session.modelRuntime, runtime);
      assert.equal(created.extensionsResult, resources);
      assert.equal(session.thinkingLevel, "off");
      assert.deepEqual(session.getActiveToolNames(), ["read"]);
      assert.ok(session.systemPrompt.includes("Independent instructions"));
      assert.ok(session.systemPrompt.includes("Child appendix"));
      assert.deepEqual(manager.getEntries().map(e => e.type), ["model_change", "thinking_level_change"]);
      assert.deepEqual(session.messages, []);
      return { tools: session.getActiveToolNames(), thinking: session.thinkingLevel, entries: manager.getEntries().map(e => ({ type: e.type, modelId: e.modelId, thinkingLevel: e.thinkingLevel })) };
    } finally { session.dispose(); }
  } finally { rmSync(dir, { recursive: true, force: true }); }
}
const want = await scene(pi);
assert.deepEqual(await scene(pig), want);
`)
}

// Ports packages/coding-agent/test/sdk-session-manager.test.ts and
// packages/coding-agent/test/sdk-skills.test.ts with the same inputs/assertions
// against both the installed Pi SDK and the shipped Node SDK. Upstream's
// assertions are POSIX-spelled (Pi's CI runs them on Linux only); on win32
// they are spelled the way Pi itself behaves there: session files are joined
// with path.sep, the system prompt's <cwd> uses forward slashes
// (system-prompt.ts), and Git Bash's pwd -W prints a Windows path.
func TestNodeSDKSessionDefaultsAndSkillsMatchPi(t *testing.T) {
	t.Parallel()
	runPinnedComparison(t, []string{"dist", "index.js"}, "pi-coding-agent.mjs", `
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, sep } from "node:path";
const pi = await import(process.argv[1]);
const pig = await import(process.argv[2]);
const ai = await import(new URL("../node_modules/@earendil-works/pi-ai/dist/compat.js", process.argv[1]));
for (const m of [pi, pig]) {
 const root = mkdtempSync(join(tmpdir(), "sdk-defaults-"));
 const cwd = join(root, "project"), agentDir = join(root, "agent");
 mkdirSync(cwd); mkdirSync(agentDir);
 const model = ai.getModel("anthropic", "claude-sonnet-4-5");
 assert.ok(model);
 try {
  // uses agentDir for the default persisted session path
  let { session } = await m.createAgentSession({ cwd, agentDir, model });
  try {
   const safePath = "--" + cwd.replace(/^[/\\]/, "").replace(/[/\\:]/g, "-") + "--";
   const expected = join(agentDir, "sessions", safePath);
   assert.equal(session.sessionManager.getSessionDir(), expected);
   assert.ok(session.sessionManager.getSessionFile().startsWith(expected + sep));
  } finally { session.dispose(); }
  // keeps an explicit sessionManager override
  const manager = m.SessionManager.inMemory(cwd);
  ({ session } = await m.createAgentSession({ cwd, agentDir, model, sessionManager: manager }));
  try { assert.equal(session.sessionManager, manager); assert.equal(manager.isPersisted(), false); }
  finally { session.dispose(); }
  // derives cwd from an explicit sessionManager when cwd is omitted
  const sessionCwd = join(root, "session-project"); mkdirSync(sessionCwd);
  const other = m.SessionManager.inMemory(sessionCwd);
  ({ session } = await m.createAgentSession({ agentDir, model, sessionManager: other }));
  try {
   assert.equal(session.sessionManager, other);
   assert.ok(session.systemPrompt.includes("<cwd>\n" + sessionCwd.replace(/\\/g, "/") + "\n</cwd>"));
   const bash = session.agent.state.tools.find(t => t.name === "bash"); assert.ok(bash);
   const result = await bash.execute("test", { command: process.platform === "win32" ? "pwd -W" : "pwd" });
   const text = result.content.filter(c => c.type === "text").map(c => c.text).join("");
   assert.equal(realpathSync(text.trim()), realpathSync(sessionCwd));
  } finally { session.dispose(); }
  // exposes current session state to the built-in bash tool
  ({ session } = await m.createAgentSession({ cwd, agentDir, model, thinkingLevel: "high" }));
  try {
   assert.ok(session.sessionFile);
   assert.ok(session.systemPrompt.includes("You can inspect PI_* environment variables for current model and session details."));
   const bash = session.agent.state.tools.find(t => t.name === "bash"); assert.ok(bash);
   const result = await bash.execute("test", { command: "printf '%s\\n' \"$PI_SESSION_ID\" \"$PI_SESSION_FILE\" \"$PI_PROVIDER\" \"$PI_MODEL\" \"$PI_REASONING_LEVEL\"" });
   const text = result.content.filter(c => c.type === "text").map(c => c.text).join("");
   assert.deepEqual(text.trim().split("\n"), [session.sessionId, session.sessionFile, model.provider, model.id, session.thinkingLevel]);
  } finally { session.dispose(); }
  const skillDir = join(root, "skills/test-skill"); mkdirSync(skillDir, { recursive: true });
  writeFileSync(join(skillDir, "SKILL.md"), "---\nname: test-skill\ndescription: A test skill for SDK tests.\n---\n\n# Test Skill\n\nThis is a test skill.\n");
  ({ session } = await m.createAgentSession({ cwd: root, agentDir: root, sessionManager: m.SessionManager.inMemory() }));
  try { assert.ok(session.resourceLoader.getSkills().skills.length > 0); assert.ok(session.resourceLoader.getSkills().skills.some(s => s.name === "test-skill")); }
  finally { session.dispose(); }
  const custom = { name: "custom-skill", description: "A custom skill", filePath: "/fake/path/SKILL.md", baseDir: "/fake/path", sourceInfo: m.createSyntheticSourceInfo("/fake/path/SKILL.md", { source: "sdk" }), disableModelInvocation: false };
  for (const skills of [[], [custom]]) {
   const resourceLoader = { getExtensions: () => ({ extensions: [], errors: [], runtime: m.createExtensionRuntime() }), getSkills: () => ({ skills, diagnostics: [] }), getPrompts: () => ({ prompts: [], diagnostics: [] }), getThemes: () => ({ themes: [], diagnostics: [] }), getAgentsFiles: () => ({ agentsFiles: [] }), getSystemPrompt: () => undefined, getSystemPromptSource: () => undefined, getAppendSystemPrompt: () => [], getAppendSystemPromptSources: () => [], extendResources() {}, async reload() {} };
   ({ session } = await m.createAgentSession({ cwd: root, agentDir: root, sessionManager: m.SessionManager.inMemory(), resourceLoader }));
   try { assert.deepEqual(session.resourceLoader.getSkills().skills, skills); assert.deepEqual(session.resourceLoader.getSkills().diagnostics, []); }
   finally { session.dispose(); }
  }
 } finally { rmSync(root, { recursive: true, force: true }); }
}
`)
}

func TestNodeCodingAgentTypeOnlyExportsMatchPi(t *testing.T) {
	t.Parallel()
	runPinnedComparison(t, []string{"dist", "index.js"}, "pi-coding-agent.mjs", `
import assert from "node:assert/strict";
for (const url of process.argv.slice(1)) {
 const sdk = await import(url);
 for (const name of ["KeybindingsManager", "ExtensionAPI", "ExtensionContext", "AgentSessionEvent", "ResourceLoader", "SessionEntry"]) assert.equal(Object.hasOwn(sdk, name), false, name);
}
`)
}

// The sentinel scenario of test/extension-conformance (child_session_test.go) run through Pi's own
// createAgentSession and through the shipped Node SDK: a sentinel provider on the child's model runtime
// answers from the transcript, an extension-supplied custom tool serves the tool prompt, and the events
// and transcript roles must equal both each other and the golden every PiG SDK row asserts. The golden
// is Pi's observation, not PiG's.
func TestNodeCreateAgentSessionSentinelScenarioMatchesPi(t *testing.T) {
	t.Parallel()
	golden, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "test", "extension-conformance", "testdata", "child-session-want.json"))
	if err != nil {
		t.Fatal(err)
	}
	runPinnedComparison(t, []string{"dist", "index.js"}, "pi-coding-agent.mjs", `
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
const pi = await import(process.argv[1]);
const pig = await import(process.argv[2]);
const ai = await import(new URL("../node_modules/@earendil-works/pi-ai/dist/compat.js", process.argv[1]));
const golden = JSON.parse(`+"`"+string(golden)+"`"+`);
function stream(model, context) {
  let prompt = "", toolResult = "";
  for (const message of context.messages) {
    if (message.role === "user") prompt = typeof message.content === "string" ? message.content : message.content.filter(c => c.type === "text").map(c => c.text).join("");
    if (message.role === "toolResult") toolResult = message.content.filter(c => c.type === "text").map(c => c.text).join("");
  }
  let content, stopReason = "stop";
  if (prompt.startsWith("stream:") && toolResult === "") {
    const out = new ai.AssistantMessageEventStream();
    const usage = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } };
    const partial = text => ({ role: "assistant", content: text === undefined ? [] : [{ type: "text", text }], api: model.api, provider: model.provider, model: model.id, usage, stopReason: "stop", timestamp: Date.now() });
    queueMicrotask(() => {
      out.push({ type: "start", partial: partial() });
      out.push({ type: "text_start", contentIndex: 0, partial: partial("") });
      let text = "";
      for (let i = 0; i < Number(prompt.slice(7)); i++) { const delta = "chunk-" + String(i).padStart(3, "0") + " "; text += delta; out.push({ type: "text_delta", contentIndex: 0, delta, partial: partial(text) }); }
      out.push({ type: "text_end", contentIndex: 0, content: text, partial: partial(text) });
      out.push({ type: "done", reason: "stop", message: partial(text) });
    });
    return out;
  }
  if (toolResult !== "") content = [{ type: "text", text: "sentinel-final:" + toolResult }];
  else if (prompt.startsWith("tool:")) { stopReason = "toolUse"; content = [{ type: "toolCall", id: "call-1", name: "sentinel_tool", arguments: { x: prompt.slice(5) } }]; }
  else content = [{ type: "text", text: "sentinel-model:" + model.id + ":" + prompt }];
  const message = { role: "assistant", content, api: model.api, provider: model.provider, model: model.id, usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } }, stopReason, timestamp: Date.now() };
  const out = new ai.AssistantMessageEventStream();
  queueMicrotask(() => out.push({ type: "done", reason: stopReason, message }));
  return out;
}
async function scene(m) {
  const dir = mkdtempSync(join(tmpdir(), "pig-child-sentinel-"));
  try {
    const runtime = await m.ModelRuntime.create({ authPath: join(dir, "auth.json"), modelsPath: join(dir, "models.json"), allowModelNetwork: false });
    runtime.registerProvider("sentinel", { api: "openai-completions", baseUrl: "https://sentinel.invalid", apiKey: "sentinel-key", models: [{ id: "echo", name: "Echo", reasoning: false, input: ["text"], contextWindow: 32768, maxTokens: 1024, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } }], streamSimple: stream });
    const resources = { extensions: [], errors: [], runtime: m.createExtensionRuntime() };
    const resourceLoader = {
      getExtensions: () => resources, getSkills: () => ({ skills: [], diagnostics: [] }), getPrompts: () => ({ prompts: [], diagnostics: [] }), getThemes: () => ({ themes: [], diagnostics: [] }),
      getAgentsFiles: () => ({ agentsFiles: [] }), getSystemPrompt: () => undefined, getSystemPromptSource: () => undefined, getAppendSystemPrompt: () => ["Child appendix"],
      getAppendSystemPromptSources: () => [], extendResources() {}, async reload() {},
    };
    const sentinel = { name: "sentinel_tool", label: "sentinel", description: "uppercases x", parameters: { type: "object", properties: { x: { type: "string" } }, required: ["x"] }, execute: async (_id, params) => ({ content: [{ type: "text", text: params.x.toUpperCase() + "!" }], details: {} }) };
    const out = {};
    for (const [name, prompt] of [["plain", "hello"], ["stream", "stream:40"], ["tool", "tool:abc"]]) {
      const { session } = await m.createAgentSession({ cwd: dir, model: runtime.getModel("sentinel", "echo"), modelRuntime: runtime, sessionManager: m.SessionManager.inMemory(dir), settingsManager: m.SettingsManager.inMemory({ compaction: { enabled: false } }), resourceLoader, tools: ["sentinel_tool"], customTools: [sentinel] });
      try {
        const events = [];
        const text = m => typeof m?.content === "string" ? m.content : (m?.content ?? []).filter(c => c.type === "text").map(c => c.text).join("");
        let updates = 0, streamed = "", partial = "";
        session.subscribe(e => {
          events.push(e.type + (e.type.startsWith("message_") ? ":" + e.message.role : ""));
          if (e.type === "message_update") { updates++; streamed = text(e.message); partial = text(e.assistantMessageEvent.partial); }
        });
        await session.prompt(prompt);
        const roles = session.messages.map(x => x.role);
        const last = session.messages.at(-1);
        out[name] = { events, roles, final: text(last), lastText: session.getLastAssistantText(), updates, streamed, partial, system: { keys: Object.keys(session.messages[0]).sort(), role: session.messages[0].role, content: session.messages[0].content, sections: Object.keys(session.messages[0].sections).sort(), toolsAdded: session.messages[0].toolsAdded } };
      } finally { session.dispose(); }
    }
    return out;
  } finally { rmSync(dir, { recursive: true, force: true }); }
}
const want = await scene(pi);
console.log("PI-OBSERVED " + JSON.stringify(want));
assert.deepEqual(want["plain"].events, golden["plain-events"]);
assert.deepEqual(want["tool"].events, golden["tool-events"]);
assert.deepEqual(want["tool"].roles, golden["tool-roles"]);
assert.equal(want["tool"].final, "sentinel-final:ABC!");
const chunks = Array.from({ length: 40 }, (_, i) => "chunk-" + String(i).padStart(3, "0") + " ").join("");
assert.equal(want["stream"].updates, golden["stream-updates"]);
assert.equal(want["stream"].streamed, chunks);
assert.equal(want["stream"].partial, chunks);
assert.equal(want["stream"].final, chunks);
assert.equal(want["stream"].lastText, chunks.trim());
assert.deepEqual(want["plain"].system, golden["system"]);
assert.deepEqual(want["tool"].system, golden["system"]);
assert.deepEqual(await scene(pig), want);
`)
}

// A session whose model cannot be used says so the way Pi does: the model-resolution and missing-credential messages of Pi's createAgentSession and AgentSession.prompt are the golden every PiG SDK row asserts (test/extension-conformance/testdata/child-session-want.json, "model-errors").
func TestNodeCreateAgentSessionModelErrorsMatchPi(t *testing.T) {
	t.Parallel()
	golden, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "test", "extension-conformance", "testdata", "child-session-want.json"))
	if err != nil {
		t.Fatal(err)
	}
	runPinnedComparison(t, []string{"dist", "index.js"}, "pi-coding-agent.mjs", `
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
const pi = await import(process.argv[1]);
const pig = await import(process.argv[2]);
const golden = JSON.parse(`+"`"+string(golden)+"`"+`);
async function scene(m) {
  const dir = mkdtempSync(join(tmpdir(), "pig-child-errors-"));
  try {
    const runtime = await m.ModelRuntime.create({ authPath: join(dir, "auth.json"), modelsPath: join(dir, "models.json"), allowModelNetwork: false });
    runtime.registerProvider("nokey", { api: "openai-completions", baseUrl: "https://nokey.invalid", models: [{ id: "locked", name: "Locked", reasoning: false, input: ["text"], contextWindow: 32768, maxTokens: 1024, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } }], streamSimple: () => { throw new Error("never streamed"); } });
    const resources = { extensions: [], errors: [], runtime: m.createExtensionRuntime() };
    const resourceLoader = {
      getExtensions: () => resources, getSkills: () => ({ skills: [], diagnostics: [] }), getPrompts: () => ({ prompts: [], diagnostics: [] }), getThemes: () => ({ themes: [], diagnostics: [] }),
      getAgentsFiles: () => ({ agentsFiles: [] }), getSystemPrompt: () => undefined, getSystemPromptSource: () => undefined, getAppendSystemPrompt: () => [],
      getAppendSystemPromptSources: () => [], extendResources() {}, async reload() {},
    };
    const out = {};
    // The docs paths are the installation's: the message keeps its first line and the names of the documents it points at (Pi joins them with the platform's separator).
    const norm = message => message == null ? null : { first: message.split("\n")[0], docs: message.split("\n").filter(line => line.trim().endsWith(".md")).map(line => line.trim().split(/[\\/]/).at(-1)) };
    const attempt = async (name, options) => {
      const { session, modelFallbackMessage } = await m.createAgentSession({ cwd: dir, modelRuntime: runtime, sessionManager: m.SessionManager.inMemory(dir), settingsManager: m.SettingsManager.inMemory({ compaction: { enabled: false } }), resourceLoader, ...options });
      try {
        let error = null;
        try { await session.prompt("hi"); } catch (e) { error = e.message; }
        out[name] = { fallback: norm(modelFallbackMessage), model: session.model?.id ?? null, promptError: norm(error) };
      } finally { session.dispose(); }
    };
    await attempt("no-key", { model: runtime.getModel("nokey", "locked") });
    await attempt("no-model", {});
    return out;
  } finally { rmSync(dir, { recursive: true, force: true }); }
}
const want = await scene(pi);
console.log("PI-OBSERVED " + JSON.stringify(want));
assert.deepEqual(want, golden["model-errors"]);
assert.deepEqual(await scene(pig), want);
`)
}

// Aborting a session mid-tool, prompting a session that is already running, and prompting one after dispose, through Pi's own createAgentSession: the observations are the golden of the PiG SDK rows' lifecycle fixtures ("lifecycle" in test/extension-conformance/testdata/child-session-want.json).
func TestNodeCreateAgentSessionLifecycleMatchesPi(t *testing.T) {
	t.Parallel()
	golden, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "test", "extension-conformance", "testdata", "child-session-want.json"))
	if err != nil {
		t.Fatal(err)
	}
	runPinnedComparison(t, []string{"dist", "index.js"}, "pi-coding-agent.mjs", `
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
const pi = await import(process.argv[1]);
const pig = await import(process.argv[2]);
const ai = await import(new URL("../node_modules/@earendil-works/pi-ai/dist/compat.js", process.argv[1]));
const golden = JSON.parse(`+"`"+string(golden)+"`"+`);
function stream(model, context) {
  const answered = context.messages.some(m => m.role === "toolResult");
  const stopReason = answered ? "stop" : "toolUse";
  const content = answered ? [{ type: "text", text: "done" }] : [{ type: "toolCall", id: "call-1", name: "sentinel_tool", arguments: { x: "hang" } }];
  const message = { role: "assistant", content, api: model.api, provider: model.provider, model: model.id, usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } }, stopReason, timestamp: Date.now() };
  const out = new ai.AssistantMessageEventStream();
  queueMicrotask(() => out.push({ type: "done", reason: stopReason, message }));
  return out;
}
async function scene(m) {
  const dir = mkdtempSync(join(tmpdir(), "pig-child-life-"));
  try {
    const runtime = await m.ModelRuntime.create({ authPath: join(dir, "auth.json"), modelsPath: join(dir, "models.json"), allowModelNetwork: false });
    runtime.registerProvider("sentinel", { api: "openai-completions", baseUrl: "https://sentinel.invalid", apiKey: "sentinel-key", models: [{ id: "echo", name: "Echo", reasoning: false, input: ["text"], contextWindow: 32768, maxTokens: 1024, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } }], streamSimple: stream });
    const resources = { extensions: [], errors: [], runtime: m.createExtensionRuntime() };
    const resourceLoader = {
      getExtensions: () => resources, getSkills: () => ({ skills: [], diagnostics: [] }), getPrompts: () => ({ prompts: [], diagnostics: [] }), getThemes: () => ({ themes: [], diagnostics: [] }),
      getAgentsFiles: () => ({ agentsFiles: [] }), getSystemPrompt: () => undefined, getSystemPromptSource: () => undefined, getAppendSystemPrompt: () => [],
      getAppendSystemPromptSources: () => [], extendResources() {}, async reload() {},
    };
    let started, toolSawAbort = false;
    const toolStarted = new Promise(resolve => { started = resolve; });
    const hang = { name: "sentinel_tool", label: "sentinel", description: "waits for its abort", parameters: { type: "object", properties: { x: { type: "string" } }, required: ["x"] }, execute: (_id, _params, signal) => new Promise((resolve, reject) => { started(); signal.addEventListener("abort", () => { toolSawAbort = true; reject(new Error("aborted")); }); }) };
    const { session } = await m.createAgentSession({ cwd: dir, model: runtime.getModel("sentinel", "echo"), modelRuntime: runtime, sessionManager: m.SessionManager.inMemory(dir), settingsManager: m.SettingsManager.inMemory({ compaction: { enabled: false } }), resourceLoader, tools: ["sentinel_tool"], customTools: [hang] });
    const events = [];
    session.subscribe(e => events.push(e.type + (e.type === "agent_settled" ? ":aborted=" + e.aborted : "")));
    const out = {};
    const running = session.prompt("go");
    await toolStarted;
    try { await session.prompt("again"); out.concurrentPrompt = "resolved"; } catch (e) { out.concurrentPrompt = e.message; }
    await session.abort();
    await running;
    out.toolSawAbort = toolSawAbort;
    out.idleAfterAbort = !session.isStreaming;
    out.tail = events.slice(-4);
    out.lastRole = session.messages.at(-1).role;
    session.dispose();
    try { await session.prompt("after dispose"); out.afterDispose = "resolved"; } catch (e) { out.afterDispose = e.message; }
    return out;
  } finally { rmSync(dir, { recursive: true, force: true }); }
}
const want = await scene(pi);
console.log("PI-OBSERVED " + JSON.stringify(want));
// Pi keeps a disposed session callable: prompt resolves and runs the agent, and the native SDKs' rows assert the same.
assert.deepEqual(want, golden["lifecycle"]);
assert.deepEqual(await scene(pig), want);
`)
}
