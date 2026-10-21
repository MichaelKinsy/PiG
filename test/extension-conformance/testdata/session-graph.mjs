// The Node fixture of the session graph conformance rows (session_graph_test.go). It runs the script of testfixture/sessiongraph (Go), testdata/session-graph-rust and the inline Python fixture with Pi's own SessionManager, so the report it makes is Pi's.
import { ProjectTrustStore, SessionManager, SettingsManager, hasTrustRequiringProjectResources } from "@earendil-works/pi-coding-agent";
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";

const readJson = (path) => JSON.parse(readFileSync(path, "utf-8"));

const shape = (entries) => {
  const index = new Map(entries.map((entry, i) => [entry.id, i]));
  return entries.map((entry) => `${entry.type}<${typeof entry.parentId === "string" && index.has(entry.parentId) ? index.get(entry.parentId) : -1}`);
};

const failure = (fn, contains) => {
  try {
    fn();
  } catch (error) {
    return String(error.message).includes(contains) ? `error: ${contains}` : `error: ${error.message}`;
  }
  return "no error";
};

const orNull = (value) => (value === undefined ? null : value);

const infos = (list) =>
  list
    .map((info) => ({
      id: info.id, messageCount: info.messageCount, name: orNull(info.name), firstMessage: info.firstMessage, cwd: info.cwd,
      createdRecent: info.created.getTime() > 1767225600000, modifiedMs: info.modified.getTime(), fileInDir: info.path !== "",
    }))
    .sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));

const assistant = {
  role: "assistant", content: [{ type: "text", text: "hi" }], api: "test", provider: "p", model: "m",
  usage: { input: 1, output: 1, cacheRead: 0, cacheWrite: 0, totalTokens: 2, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } },
  stopReason: "stop", timestamp: 2,
};

async function script({ cwd, dir, otherDir, junk }) {
  const r = {};
  const fixed = "fixed-session-1";
  const m = SessionManager.create(cwd, dir, { id: fixed });
  const file = m.getSessionFile();
  r.created = {
    id: m.getSessionId(), persisted: m.isPersisted(), dir: m.getSessionDir(), fileSuffix: file.endsWith("_fixed-session-1.jsonl"), fileInDir: dirname(file) === dir,
    cwd: m.getCwd(), defaultDir: m.usesDefaultSessionDir(), entries: m.getEntries().length, leaf: orNull(m.getLeafId()),
  };
  const first = m.appendMessage({ role: "user", content: "hello", timestamp: 1 });
  m.appendMessage(assistant);
  m.appendSessionInfo("graph");
  m.appendLabelChange(first, "start");
  m.appendCustomEntry("note", { a: 1, b: 2 });
  m.appendThinkingLevelChange("high");
  const last = m.appendModelChange("p", "m");
  const entries = m.getEntries();
  const header = m.getHeader();
  const custom = entries.find((entry) => entry.type === "custom") ?? {};
  r.log = {
    name: orNull(m.getSessionName()), label: orNull(m.getLabel(first)), count: m.getEntries().length, shape: shape(entries), leafIsLast: m.getLeafId() === last,
    entryType: m.getEntry(first).type, missing: m.getEntry("nope") === undefined,
    headerType: header.type, headerVersion: header.version, headerId: header.id, headerCwd: header.cwd, customType: custom.customType, customData: custom.data,
  };
  const built = m.buildSessionContext();
  r.context = { messages: built.messages.length, thinkingLevel: built.thinkingLevel, model: built.model };
  const tree = m.getTree();
  r.tree = { roots: tree.length, firstChildren: tree[0].children.length };

  m.branch(first);
  const alt = m.appendMessage({ role: "user", content: "alt", timestamp: 3 });
  r.branch = { shape: shape(m.getBranch()), children: m.getChildren(first).length, leafIsAlt: m.getLeafId() === alt };
  m.resetLeaf();
  r.reset = { leaf: orNull(m.getLeafId()) };
  const summary = m.branchWithSummary(first, "summary text");
  const summaryEntry = m.getEntry(summary);
  r.summary = { type: summaryEntry.type, parentIsFirst: summaryEntry.parentId === first, text: summaryEntry.summary, fromId: summaryEntry.fromId === first };
  const branched = m.createBranchedSession(summary);
  r.branched = {
    isFile: branched !== undefined && branched.endsWith(".jsonl"), inDir: branched !== undefined && dirname(branched) === dir, newFile: branched !== undefined && branched !== file,
    idChanged: m.getSessionId() !== fixed, count: m.getEntries().length, shape: shape(m.getEntries()),
  };

  const opened = SessionManager.open(file);
  r.opened = {
    id: opened.getSessionId(), count: opened.getEntries().length, cwd: opened.getCwd(), headerCwd: opened.getHeader().cwd, dirIsFileDir: opened.getSessionDir() === dir,
    file: opened.getSessionFile() === file, name: orNull(opened.getSessionName()),
  };
  const overridden = SessionManager.open(file, undefined, otherDir);
  r.override = { cwd: overridden.getCwd(), id: overridden.getSessionId() };
  const recent = SessionManager.continueRecent(cwd, dir);
  const recentFile = recent.getSessionFile();
  r.recent = { inDir: recentFile !== undefined && dirname(recentFile) === dir, hasEntries: recent.getEntries().length > 0, cwd: recent.getCwd() };

  const mem = SessionManager.inMemory(cwd, { id: "mem-1" });
  const before = orNull(mem.getLeafId());
  mem.appendMessage({ role: "user", content: "x", timestamp: 1 });
  r.memory = {
    id: mem.getSessionId(), persisted: mem.isPersisted(), file: orNull(mem.getSessionFile()), dir: mem.getSessionDir(), defaultDir: mem.usesDefaultSessionDir(),
    leafBefore: before, count: mem.getEntries().length, cwd: mem.getCwd(),
  };
  const seeded = SessionManager.inMemory(cwd, undefined, [
    { type: "message", id: "e1", parentId: null, timestamp: "2026-01-01T00:00:00.000Z", message: { role: "user", content: "seed", timestamp: 1 } },
  ]);
  r.seeded = { count: seeded.getEntries().length, leaf: orNull(seeded.getLeafId()), hasId: seeded.getSessionId() !== "" };

  const forkDir = join(otherDir, "sessions");
  const forked = SessionManager.forkFrom(file, otherDir, forkDir, { id: "forked-1" });
  r.fork = {
    id: forked.getSessionId(), count: forked.getEntries().length, parent: forked.getHeader().parentSession === file, cwd: forked.getCwd() === otherDir,
    inDir: dirname(forked.getSessionFile()) === forkDir, shape: shape(forked.getEntries()),
  };

  r.list = infos(await SessionManager.list(cwd, dir));
  r.listAll = infos(await SessionManager.listAll(dir));
  r.listFork = infos(await SessionManager.list(otherDir, forkDir));
  const found = SessionManager.findById(cwd, fixed, dir);
  r.find = { found: found === file, missing: SessionManager.findById(cwd, "nope", dir) === undefined };

  r.errors = {
    badId: failure(() => SessionManager.create(cwd, dir, { id: "bad id!" }), "Session id must be"),
    notSession: failure(() => SessionManager.open(junk), "not a valid"),
    noFork: failure(() => SessionManager.forkFrom(join(dir, "missing.jsonl"), cwd, forkDir), "Cannot fork"),
  };
  return r;
}


const failureOf = (fn, contains) => {
  try {
    fn();
  } catch (error) {
    return String(error.message).includes(contains) ? `error: ${contains}` : `error: ${error.message}`;
  }
  return "no error";
};

const readAll = (s) => ({
    getDefaultProvider: orNull(s.getDefaultProvider()),
    getDefaultModel: orNull(s.getDefaultModel()),
    getDefaultThinkingLevel: orNull(s.getDefaultThinkingLevel()),
    getTheme: orNull(s.getTheme()),
    getThemeSetting: orNull(s.getThemeSetting()),
    getSteeringMode: orNull(s.getSteeringMode()),
    getFollowUpMode: orNull(s.getFollowUpMode()),
    getTransport: orNull(s.getTransport()),
    getSessionShareDestination: orNull(s.getSessionShareDestination()),
    getCompactionEnabled: orNull(s.getCompactionEnabled()),
    getBranchSummarySkipPrompt: orNull(s.getBranchSummarySkipPrompt()),
    getRetryEnabled: orNull(s.getRetryEnabled()),
    getHttpIdleTimeoutMs: orNull(s.getHttpIdleTimeoutMs()),
    getCacheWarmingMode: orNull(s.getCacheWarmingMode()),
    getWebSocketConnectTimeoutMs: orNull(s.getWebSocketConnectTimeoutMs()),
    getHideThinkingBlock: orNull(s.getHideThinkingBlock()),
    getShowCacheMissNotices: orNull(s.getShowCacheMissNotices()),
    getExternalEditorCommand: orNull(s.getExternalEditorCommand()),
    getShellPath: orNull(s.getShellPath()),
    getQuietStartup: orNull(s.getQuietStartup()),
    getDefaultProjectTrust: orNull(s.getDefaultProjectTrust()),
    getShellCommandPrefix: orNull(s.getShellCommandPrefix()),
    getNpmCommand: orNull(s.getNpmCommand()),
    getCollapseChangelog: orNull(s.getCollapseChangelog()),
    getEnableInstallTelemetry: orNull(s.getEnableInstallTelemetry()),
    getEnableAnalytics: orNull(s.getEnableAnalytics()),
    getPackages: orNull(s.getPackages()),
    getExtensionPaths: orNull(s.getExtensionPaths()),
    getSkillPaths: orNull(s.getSkillPaths()),
    getPromptTemplatePaths: orNull(s.getPromptTemplatePaths()),
    getThemePaths: orNull(s.getThemePaths()),
    getEnableSkillCommands: orNull(s.getEnableSkillCommands()),
    getThinkingBudgets: orNull(s.getThinkingBudgets()),
    getTerminalCapabilityOverrides: orNull(s.getTerminalCapabilityOverrides()),
    getShowImages: orNull(s.getShowImages()),
    getImageWidthCells: orNull(s.getImageWidthCells()),
    getClearOnShrink: orNull(s.getClearOnShrink()),
    getShowTerminalProgress: orNull(s.getShowTerminalProgress()),
    getTuiMode: orNull(s.getTuiMode()),
    getFullscreenExitOutput: orNull(s.getFullscreenExitOutput()),
    getFullscreenScrollbar: orNull(s.getFullscreenScrollbar()),
    getFullscreenCopyOnSelect: orNull(s.getFullscreenCopyOnSelect()),
    getFullscreenWheelScrollLines: orNull(s.getFullscreenWheelScrollLines()),
    getImageAutoResize: orNull(s.getImageAutoResize()),
    getBlockImages: orNull(s.getBlockImages()),
    getEnabledModels: orNull(s.getEnabledModels()),
    getDefaultTools: orNull(s.getDefaultTools()),
    getDoubleEscapeAction: orNull(s.getDoubleEscapeAction()),
    getTreeFilterMode: orNull(s.getTreeFilterMode()),
    getShowHardwareCursor: orNull(s.getShowHardwareCursor()),
    getEditorPaddingX: orNull(s.getEditorPaddingX()),
    getOutputPad: orNull(s.getOutputPad()),
    getAutocompleteMaxVisible: orNull(s.getAutocompleteMaxVisible()),
    getCodeBlockIndent: orNull(s.getCodeBlockIndent()),
    getMermaidRenderingMode: orNull(s.getMermaidRenderingMode()),
    getWarnings: orNull(s.getWarnings()),
    getAllModelThinkingLevels: orNull(s.getAllModelThinkingLevels()),
    getBranchSummarySettings: orNull(s.getBranchSummarySettings()),
    getRetrySettings: orNull(s.getRetrySettings()),
    getProviderRetrySettings: orNull(s.getProviderRetrySettings()),
    getLastChangelogVersion: orNull(s.getLastChangelogVersion()),
    getSessionDir: orNull(s.getSessionDir()),
    getSettings: orNull(s.getSettings()),
    getGlobalSettings: orNull(s.getGlobalSettings()),
    getProjectSettings: orNull(s.getProjectSettings()),
    isProjectTrusted: orNull(s.isProjectTrusted()),
});

async function settingsScript({ cwd, agentDir, configDir, brokenCwd }) {
  const r = {};
  const s = SettingsManager.create(cwd, agentDir);
  r.initial = readAll(s);
  r.modelBudget = {
    reserve: s.getCompactionReserveTokens({ provider: "p", id: "m" }), reserveNone: s.getCompactionReserveTokens(), keep: s.getCompactionKeepRecentTokens(),
    settings: s.getCompactionSettings({ provider: "p", id: "m" }), thinking: orNull(s.getModelThinkingLevel("p", "m")),
  };
  s.setDefaultProvider("openai");
  s.setDefaultModel("gpt");
  s.setDefaultThinkingLevel("high");
  s.setTheme("light");
  s.setSteeringMode("one-at-a-time");
  s.setFollowUpMode("one-at-a-time");
  s.setCompactionEnabled(true);
  s.setRetryEnabled(false);
  s.setPackages(["npm:a", {"source": "npm:b", "extensions": ["x"]}]);
  s.setExtensionPaths(["a", "b"]);
  s.setSkillPaths(["s1"]);
  s.setPromptTemplatePaths(["p1"]);
  s.setThemePaths(["t1"]);
  s.setEnabledModels(["m1", "m2"]);
  s.setQuietStartup("header");
  s.setShellPath("/bin/sh");
  s.setShellCommandPrefix("set -e");
  s.setNpmCommand(["pnpm", "dlx"]);
  s.setModelThinkingLevel("p", "m", "low");
  s.setHttpIdleTimeoutMs(5000);
  s.setImageWidthCells(40);
  s.setOutputPad(0);
  s.setTuiMode("fullscreen");
  s.setFullscreenWheelScrollLines(3);
  s.setWarnings({"anthropicExtraUsage": true});
  s.setDefaultProjectTrust("never");
  s.setLastChangelogVersion("9.9.9");
  s.setEnableAnalytics(false);
  s.setTransport("sse");
  s.setCacheWarmingMode("idle");
  s.setHideThinkingBlock(true);
  s.setShowImages(false);
  s.setClearOnShrink(true);
  s.setDoubleEscapeAction("tree");
  s.setTreeFilterMode("user-only");
  s.setMermaidRenderingMode("off");
  s.setEditorPaddingX(2);
  s.setAutocompleteMaxVisible(7);
  s.setCollapseChangelog(true);
  s.setBlockImages(true);
  s.setSessionShareDestination("gist");
  s.setFullscreenExitOutput("resume-hint");
  s.setFullscreenScrollbar("hidden");
  s.setFullscreenCopyOnSelect(false);
  s.setShowHardwareCursor(true);
  s.setImageAutoResize(false);
  s.setShowTerminalProgress(true);
  s.setEnableSkillCommands(false);
  s.setEnableInstallTelemetry(false);
  s.setShowCacheMissNotices(false);
  s.setDefaultModelAndProvider("anthropic", "opus");
  s.setProjectPackages(["npm:proj"]);
  s.setProjectExtensionPaths(["pe"]);
  s.setProjectSkillPaths(["ps"]);
  s.setProjectPromptTemplatePaths(["pp"]);
  s.setProjectThemePaths(["pt"]);
  await s.flush();
  r.afterSet = readAll(s);
  r.modelThinking = orNull(s.getModelThinkingLevel("p", "m"));
  r.files = { global: readJson(join(agentDir, "settings.json")), project: readJson(join(cwd, configDir, "settings.json")) };

  const persisted = SettingsManager.create(cwd, agentDir);
  r.persisted = readAll(persisted);
  writeFileSync(join(agentDir, "settings.json"), '{"defaultProvider":"external"}');
  await persisted.reload();
  r.reload = { provider: orNull(persisted.getDefaultProvider()), model: orNull(persisted.getDefaultModel()), merged: persisted.getSettings() };
  persisted.setShellPath(undefined);
  persisted.setShellCommandPrefix(undefined);
  persisted.setNpmCommand(undefined);
  persisted.setEnabledModels(undefined);
  persisted.setDefaultModel("kept");
  await persisted.flush();
  r.cleared = {
    shellPath: orNull(persisted.getShellPath()), prefix: orNull(persisted.getShellCommandPrefix()), npm: orNull(persisted.getNpmCommand()), enabled: orNull(persisted.getEnabledModels()),
    file: readJson(join(agentDir, "settings.json")),
  };

  const memory = SettingsManager.inMemory({ defaultProvider: "mem", compaction: { enabled: false }, retry: { maxRetries: 9 } });
  r.memory = {
    provider: orNull(memory.getDefaultProvider()), compaction: memory.getCompactionSettings(), retry: memory.getRetrySettings(), merged: memory.getSettings(), trusted: memory.isProjectTrusted(),
  };
  memory.setDefaultModel("mm");
  memory.applyOverrides({ defaultModel: "ov" });
  r.memoryAfter = { model: orNull(memory.getDefaultModel()), global: memory.getGlobalSettings(), merged: memory.getSettings() };
  const untrusted = SettingsManager.inMemory({}, { projectTrusted: false });
  r.untrustedMemory = { trusted: untrusted.isProjectTrusted(), write: failureOf(() => untrusted.setProjectPackages(["x"]), "refusing to write project settings") };

  const broken = SettingsManager.create(brokenCwd, agentDir);
  const drained = broken.drainErrors();
  const entry = { count: drained.length };
  if (drained.length > 0) {
    entry.scope = drained[0].scope;
    entry.pathSuffix = typeof drained[0].path === "string" && drained[0].path.endsWith(join(configDir, "settings.json"));
    entry.hasMessage = !!drained[0].error && drained[0].error.message !== "";
  }
  entry.again = broken.drainErrors().length;
  r.errors = entry;

  const distrust = SettingsManager.create(cwd, agentDir, { projectTrusted: false });
  r.distrusted = {
    trusted: distrust.isProjectTrusted(), project: distrust.getProjectSettings(), merged: distrust.getSettings(),
    write: failureOf(() => distrust.setProjectExtensionPaths(["z"]), "refusing to write project settings"),
  };
  distrust.setProjectTrusted(true);
  r.retrusted = { trusted: distrust.isProjectTrusted(), projectModel: distrust.getProjectSettings().defaultModel };
  return r;
}

const trustEntry = (entry) => (entry === null || entry === undefined ? null : { path: entry.path, decision: entry.decision });

async function trustScript({ root, cwd, agentDir, otherDir }) {
  const sub = join(cwd, "sub");
  const anc = join(root, "anc");
  const ancProject = join(root, "anc", "proj");
  const bare = join(root, "bare");
  const r = {};
  const store = new ProjectTrustStore(agentDir);
  r.initial = { get: store.get(cwd), entry: trustEntry(store.getEntry(cwd)) };
  store.set(cwd, true);
  store.set(otherDir, false);
  r.saved = { get: store.get(cwd), entry: trustEntry(store.getEntry(sub)), otherGet: store.get(otherDir), file: readJson(join(agentDir, "trust.json")) };
  store.setMany([{ path: cwd, decision: null }, { path: anc, decision: true }]);
  r.many = { cwd: store.get(cwd), anc: store.get(ancProject), entryAnc: trustEntry(store.getEntry(ancProject)), file: readJson(join(agentDir, "trust.json")) };
  store.set(sub, false);
  r.nested = { sub: store.get(sub), parent: store.get(cwd), file: readJson(join(agentDir, "trust.json")) };
  const failureOfGet = (dir, contains) => failureOf(() => new ProjectTrustStore(join(root, dir)).get(cwd), contains);
  r.errors = {
    notObject: failureOfGet("agent-array", "Invalid trust store"), unreadable: failureOfGet("agent-bad-json", "Failed to read trust store"),
    badValue: failureOfGet("agent-bad-value", "must be true, false, or null"),
  };
  r.resources = { config: hasTrustRequiringProjectResources(cwd), bare: hasTrustRequiringProjectResources(bare), ancestor: hasTrustRequiringProjectResources(ancProject) };
  return r;
}

export default function (pi) {
  pi.registerTool({
    name: "session_manager", label: "session_manager", description: "Runs the SessionManager script.", parameters: { type: "object" },
    execute: async (_id, params) => ({ content: [{ type: "text", text: JSON.stringify(await script(params)) }] }),
  });
  pi.registerTool({
    name: "settings_manager", label: "settings_manager", description: "Runs the SettingsManager script.", parameters: { type: "object" },
    execute: async (_id, params) => ({ content: [{ type: "text", text: JSON.stringify(await settingsScript(params)) }] }),
  });
  pi.registerTool({
    name: "project_trust_store", label: "project_trust_store", description: "Runs the ProjectTrustStore script.", parameters: { type: "object" },
    execute: async (_id, params) => ({ content: [{ type: "text", text: JSON.stringify(await trustScript(params)) }] }),
  });
}
