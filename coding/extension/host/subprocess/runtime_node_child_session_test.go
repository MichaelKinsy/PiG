package subprocess_test

import "testing"

// Pi core/sdk.ts:175-439 constructs an independent AgentSession and restores its
// manager's transcript; it does not prompt or mutate the enclosing Session.
func TestNodeCreateAgentSessionMatchesPi(t *testing.T) {
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
	runPinnedComparison(t, []string{"dist", "index.js"}, "pi-coding-agent.mjs", `
import assert from "node:assert/strict";
for (const url of process.argv.slice(1)) {
 const sdk = await import(url);
 for (const name of ["KeybindingsManager", "ExtensionAPI", "ExtensionContext", "AgentSessionEvent", "ResourceLoader", "SessionEntry"]) assert.equal(Object.hasOwn(sdk, name), false, name);
}
`)
}
