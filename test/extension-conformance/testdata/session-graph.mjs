// The Node fixture of the session graph conformance rows (session_graph_test.go). It runs the script of testfixture/sessiongraph (Go), testdata/session-graph-rust and the inline Python fixture with Pi's own SessionManager, so the report it makes is Pi's.
import { DefaultPackageManager, DefaultResourceLoader, ModelRuntime, ProjectTrustStore, SessionManager, SettingsManager, createAgentSession, hasTrustRequiringProjectResources } from "@earendil-works/pi-coding-agent";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
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

  const msgId = m.appendCustomMessageEntry("note-msg", "body", true, { k: 1 });
  const usage = m.appendUsage("test", "p", "m", {"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}});
  r.extra = { msgType: m.getEntry(msgId).type, msgCustomType: m.getEntry(msgId).customType, usageType: usage.type, usageKind: usage.kind };

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
  const listProgress = [];
  const listAllProgress = [];
  r.listAll = infos(await SessionManager.listAll(dir, (loaded, total) => listAllProgress.push([loaded, total])));
  infos(await SessionManager.list(cwd, dir, (loaded, total) => listProgress.push([loaded, total])));
  r.progress = { list: { calls: listProgress.length > 0, last: listProgress.at(-1) ?? null }, listAll: { calls: listAllProgress.length > 0, last: listAllProgress.at(-1) ?? null } };
  r.listFork = infos(await SessionManager.list(otherDir, forkDir));
  const found = SessionManager.findById(cwd, fixed, dir);
  r.find = { found: found === file, missing: SessionManager.findById(cwd, "nope", dir) === undefined };

  const compactionId = m.appendCompaction("sum", first, 100, { k: 1 });
  const compaction = m.getEntry(compactionId);
  const editId = m.appendContextEdit(first, { content: "edited" });
  const edit = m.getEntry(editId);
  const clearId = m.appendContextEdit(first, null);
  const switched = SessionManager.create(cwd, dir, { id: "reopen-1" });
  switched.setSessionFile(file);
  r.compaction = {
    type: compaction.type, summary: compaction.summary, firstKept: compaction.firstKeptEntryId === first, tokensBefore: compaction.tokensBefore, details: orNull(compaction.details), fromHook: orNull(compaction.fromHook),
    editType: edit.type, replacement: orNull(edit.replacement), target: edit.targetId === first, cleared: orNull(m.getEntry(clearId).replacement), leaf: m.getLeafId() === clearId,
    badTarget: failure(() => m.appendContextEdit("nope", null), "not found"), badReplacement: failure(() => m.appendContextEdit(first, { content: 5 }), "must be null or contain"),
    switched: { id: switched.getSessionId() === m.getSessionId(), file: switched.getSessionFile() === file, shape: shape(switched.getEntries()).join(",") === shape(m.getEntries()).join(","), count: switched.getEntries().length },
    badSwitch: failure(() => switched.setSessionFile(junk), "not a valid"),
  };

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

async function storageReport() {
  const store = { global: '{"theme":"dark","quietStartup":true}', project: undefined };
  const calls = [];
  const storage = {
    withLock(scope, fn) {
      calls.push(`${scope}:${store[scope] === undefined ? "none" : "text"}`);
      const next = fn(store[scope]);
      if (next !== undefined) {
        calls.push(`${scope}:write`);
        store[scope] = next;
      }
    },
  };
  const out = {};
  const manager = SettingsManager.fromStorage(storage);
  out.loaded = { theme: orNull(manager.getTheme()), quiet: manager.getQuietStartup(), calls: [...calls], errors: manager.drainErrors().length };
  calls.length = 0;
  manager.setTheme("light");
  await manager.flush();
  out.wrote = { calls: [...calls], global: JSON.parse(store.global), project: store.project === undefined, theme: manager.getTheme() };
  store.global = '{"theme":"solarized"}';
  calls.length = 0;
  await manager.reload();
  out.reloaded = { theme: manager.getTheme(), calls: [...calls] };
  calls.length = 0;
  manager.setProjectExtensionPaths(["./e"]);
  await manager.flush();
  out.projectWrote = { project: JSON.parse(store.project), calls: [...calls], paths: manager.getProjectSettings().extensions ?? null };
  calls.length = 0;
  const untrusted = SettingsManager.fromStorage(storage, { projectTrusted: false });
  out.untrusted = { calls: [...calls], theme: orNull(untrusted.getTheme()), trusted: untrusted.isProjectTrusted() };
  store.global = "{bad";
  const broken = SettingsManager.fromStorage(storage);
  out.broken = broken.drainErrors().map((entry) => ({ scope: entry.scope, noPath: entry.path === undefined, hasMessage: entry.error.message !== "" }));
  const failing = SettingsManager.fromStorage({ withLock() { throw new Error("disk full"); } });
  out.failing = failing.drainErrors().map((entry) => ({ scope: entry.scope, message: entry.error.message }));
  return out;
}

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
  s.removeModelThinkingLevel("p", "m");
  r.removed = { thinking: orNull(s.getModelThinkingLevel("p", "m")), trackingId: orNull(s.getTrackingId()), deviceId: s.getOrCreateDeviceId().length > 0 };
  await s.flush();
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
  r.storage = await storageReport();
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

import { basename } from "node:path";

const skillReport = (result) => ({
  skills: result.skills
    .map((skill) => ({
      name: skill.name, description: skill.description, disable: !!skill.disableModelInvocation, file: basename(skill.filePath), baseDir: basename(skill.baseDir),
      scope: skill.sourceInfo.scope, origin: skill.sourceInfo.origin, source: skill.sourceInfo.source,
    }))
    .sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0)),
  diagnostics: result.diagnostics.map((diagnostic) => `${diagnostic.type}: ${diagnostic.message}`),
});

const promptReport = (result) => ({
  prompts: result.prompts
    .map((prompt) => ({
      name: prompt.name, description: prompt.description, argumentHint: prompt.argumentHint ?? "", content: prompt.content, file: basename(prompt.filePath), scope: prompt.sourceInfo.scope,
    }))
    .sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0)),
  diagnostics: result.diagnostics.map((diagnostic) => `${diagnostic.type}: ${diagnostic.message}`),
});

const resourceReport = (loader) => {
  const extensions = loader.getExtensions();
  const source = loader.getSystemPromptSource();
  return {
    extensions: extensions.extensions.length, extensionErrors: extensions.errors.length, themes: loader.getThemes().themes.length,
    skills: skillReport(loader.getSkills()), prompts: promptReport(loader.getPrompts()),
    agentsFiles: loader.getAgentsFiles().agentsFiles.map((file) => ({ path: file.path, content: file.content })),
    systemPrompt: orNull(loader.getSystemPrompt()), systemPromptSource: source === undefined ? null : source.path,
    appendSystemPrompt: loader.getAppendSystemPrompt(), appendSources: loader.getAppendSystemPromptSources().map((item) => item.path),
  };
};

async function loaderScript({ root, cwd, loaderAgentDir: agentDir }) {
  const extraSkills = join(root, "extra-skills");
  const extraPrompts = join(root, "extra-prompts");
  const r = {};
  const plain = new DefaultResourceLoader({ cwd, agentDir });
  await plain.reload();
  r.plain = resourceReport(plain);

  const overridden = new DefaultResourceLoader({
    cwd, agentDir, noSkills: true, additionalSkillPaths: [extraSkills], noPromptTemplates: true, additionalPromptTemplatePaths: [extraPrompts],
    noContextFiles: true, systemPrompt: "inline prompt", appendSystemPrompt: ["a1", "a2"],
    systemPromptOverride: (base) => (base === undefined ? undefined : base + "!"),
    appendSystemPromptOverride: (base) => [...base, "z"],
    skillsOverride: (base) => ({ skills: base.skills.filter((skill) => skill.name !== "beta"), diagnostics: base.diagnostics }),
    promptsOverride: (base) => ({ prompts: base.prompts.map((prompt) => ({ ...prompt, description: "overridden " + prompt.name })), diagnostics: base.diagnostics }),
    agentsFilesOverride: () => ({ agentsFiles: [{ path: "/virtual/AGENTS.md", content: "virtual" }] }),
  });
  await overridden.reload();
  r.overridden = resourceReport(overridden);

  plain.extendResources({ skillPaths: [{ path: join(extraSkills, "beta"), metadata: { source: "test", scope: "project", origin: "top-level" } }] });
  r.extended = skillReport(plain.getSkills());

  const settings = SettingsManager.inMemory({ skills: [extraSkills] });
  const viaSettings = new DefaultResourceLoader({ cwd, agentDir, settingsManager: settings });
  await viaSettings.reload();
  r.viaSettings = skillReport(viaSettings.getSkills());

  let calls = 0;
  const trusting = new DefaultResourceLoader({ cwd, agentDir });
  await trusting.reload({ resolveProjectTrust: () => { calls++; return true; } });
  r.trust = { calls, skills: trusting.getSkills().skills.length };
  const bootstrap = await trusting.loadProjectTrustExtensions();
  r.projectTrust = { extensions: bootstrap.extensions.length, errors: bootstrap.errors.length };
  return r;
}

const resolvedReport = (paths) => ({
  extensions: paths.extensions.length,
  skills: paths.skills.map((skill) => ({ file: basename(skill.path), enabled: skill.enabled, source: skill.metadata.source, scope: skill.metadata.scope, origin: skill.metadata.origin })),
  prompts: paths.prompts.length, themes: paths.themes.length,
});

async function packagesScript({ root, pmCwd: cwd, pmAgentDir: agentDir }) {
  const pkg = join(root, "pkgs", "localpkg");
  const r = {};
  const settings = SettingsManager.create(cwd, agentDir);
  const pm = new DefaultPackageManager({ cwd, agentDir, settingsManager: settings });
  r.initial = { list: pm.listConfiguredPackages().length, installed: orNull(pm.getInstalledPath(pkg, "user")) };
  const added = pm.addSourceToSettings(pkg);
  const again = pm.addSourceToSettings(pkg);
  r.added = { first: added, again, list: pm.listConfiguredPackages(), packages: settings.getPackages(), installed: orNull(pm.getInstalledPath(pkg, "user")) };
  r.resolved = resolvedReport(await pm.resolve());

  const events = [];
  pm.setProgressCallback((event) => events.push(event.type + ":" + event.action));
  await pm.installAndPersist(pkg, { local: true });
  await pm.remove(pkg, { local: true });
  r.removeOnly = { list: pm.listConfiguredPackages().length };
  r.installed = { list: pm.listConfiguredPackages(), events: [...events] };
  r.removed = {
    project: await pm.removeAndPersist(pkg, { local: true }), list: pm.listConfiguredPackages().length,
    settings: pm.removeSourceFromSettings(pkg), settingsAgain: pm.removeSourceFromSettings(pkg),
  };

  pm.setProgressCallback(undefined);
  r.temporary = resolvedReport(await pm.resolveExtensionSources([pkg], { temporary: true }));
  r.updates = (await pm.checkForAvailableUpdates()).length;

  const missing = [];
  pm.addSourceToSettings("npm:not-installed-pkg");
  const skipped = await pm.resolve(async (source) => { missing.push(source); return "skip"; });
  r.missing = { calls: missing, skills: skipped.skills.length };
  return r;
}

const plain = (value) => (value === undefined ? null : JSON.parse(JSON.stringify(value)));

const pickModel = (model) =>
  model === undefined
    ? null
    : { id: model.id, provider: model.provider, api: model.api, name: model.name, reasoning: !!model.reasoning, contextWindow: model.contextWindow, maxTokens: model.maxTokens };

const failed = async (fn) => {
  try {
    await fn();
  } catch (error) {
    return `error: ${error.message}`;
  }
  return "no error";
};

async function modelRuntimeScript({ mrAuth, mrModels, mrStore, mrServer }) {
  const r = {};
  const rt = await ModelRuntime.create({ authPath: mrAuth, modelsPath: mrModels, modelsStorePath: mrStore });
  const local = rt.getModel("mr-local", "m1");
  r.catalog = {
    error: orNull(rt.getError()), models: rt.getModels("mr-local").map(pickModel), all: rt.getAllModels("mr-local").length, one: pickModel(local),
    missing: rt.getModel("mr-local", "nope") === undefined, builtinListed: rt.getModels().some((model) => model.provider === "anthropic"),
    ofType: rt.getModelsOfType("chat", "mr-local").map((model) => model.id), oneOfType: pickModel(rt.getModelOfType("chat", "mr-local", "m1")),
    imagesOfType: rt.getModelsOfType("image", "mr-local").length, physical: pickModel(rt.getPhysicalModel("mr-local", "m1")),
    physicalMissing: rt.getPhysicalModel("mr-local", "nope") === undefined,
  };
  r.auth = {
    configured: rt.hasConfiguredAuth("mr-local"), unconfigured: rt.hasConfiguredAuth("nope-prov"), oauth: rt.isUsingOAuth("mr-local"), subscription: rt.isUsingSubscription("mr-local"),
    status: rt.getProviderAuthStatus("mr-local"), storedStatus: rt.getProviderAuthStatus("stored-prov"), unknownStatus: rt.getProviderAuthStatus("nope-prov"),
    byProvider: plain(await rt.getAuth("mr-local")), byModel: plain(await rt.getAuth(local)), unconfiguredAuth: plain(await rt.getAuth("nope-prov")),
    overridden: plain(await rt.getAuth("mr-local", { apiKey: "override" })),
    check: plain(await rt.checkAuth("mr-local")), uncheckedProvider: plain(await rt.checkAuth("nope-prov")), compat: plain(rt.getCompatibilityRequestConfig(local)),
  };
  await rt.setRuntimeApiKey("rt-prov", "rk");
  const withKey = { status: rt.getProviderAuthStatus("rt-prov"), configured: rt.hasConfiguredAuth("rt-prov"), auth: plain(await rt.getAuth("rt-prov")) };
  await rt.removeRuntimeApiKey("rt-prov");
  r.runtimeKey = { withKey, removed: rt.getProviderAuthStatus("rt-prov"), configuredAfter: rt.hasConfiguredAuth("rt-prov") };
  r.credentials = (await rt.listCredentials()).map((credential) => ({ providerId: credential.providerId, type: credential.type })).sort((a, b) => (a.providerId < b.providerId ? -1 : 1));

  const registeredBefore = [...rt.getRegisteredProviderIds()];
  rt.registerProvider("reg-prov", {
    baseUrl: mrServer, apiKey: "regkey", api: "openai-completions", headers: { "x-reg": "1" },
    models: [{ id: "r1", name: "Reg One", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 4000, maxTokens: 500 }],
  });
  rt.registerProvider("reg-prov", { baseUrl: `${mrServer}/v2` });
  const config = rt.getRegisteredProviderConfig("reg-prov");
  r.registered = {
    before: registeredBefore, ids: [...rt.getRegisteredProviderIds()], models: rt.getModels("reg-prov").map(pickModel), baseUrl: config.baseUrl, apiKey: config.apiKey, api: config.api, headers: plain(config.headers), modelIds: config.models.map((model) => model.id),
    configured: rt.hasConfiguredAuth("reg-prov"), unregisteredConfig: rt.getRegisteredProviderConfig("nope-prov") === undefined,
    rejected: await failed(() => rt.registerProvider("bad-prov", { models: [{ id: "x" }] })),
  };
  rt.unregisterProvider("reg-prov");
  r.registered.after = { ids: [...rt.getRegisteredProviderIds()], models: rt.getModels("reg-prov").length, config: rt.getRegisteredProviderConfig("reg-prov") === undefined };

  const requests = [];
  rt.registerVirtualModel({
    provider: "mr-virtual", id: "v1", name: "Virtual One", thinkingLevels: ["off", "high"], contextWindow: 1000, maxTokens: 100, input: ["text"],
    route: async (request) => {
      requests.push({ model: request.model.id, thinkingLevel: request.thinkingLevel, reason: request.reason, messages: request.messages.length, state: orNull(request.state) });
      return { model: rt.getPhysicalModel("mr-local", "m1"), thinkingLevel: "off", state: { n: requests.length } };
    },
  });
  const virtual = rt.getModel("mr-virtual", "v1");
  const route = await rt.resolveModel(virtual, [{ role: "user", content: "hi", timestamp: 1 }], { reason: "user", thinkingLevel: "high" });
  const second = await rt.resolveModel(virtual, [{ role: "user", content: "hi", timestamp: 1 }], { reason: "continuation", thinkingLevel: "off", state: { n: 1 } });
  r.virtual = {
    model: pickModel(virtual), thinkingLevels: virtual.thinkingLevels ?? null, route: { model: pickModel(route.model), thinkingLevel: route.thinkingLevel, state: plain(route.state) }, second: plain(second.state), requests,
    physicalIsNull: rt.getPhysicalModel("mr-virtual", "v1") === undefined, unknown: await failed(() => rt.resolveModel(local, [], { reason: "user", thinkingLevel: "off" })),
  };
  rt.unregisterVirtualModel("mr-virtual", "v1");
  r.virtual.after = rt.getModel("mr-virtual", "v1") === undefined;

  const refreshed = await rt.refresh({ allowNetwork: false });
  r.refresh = { aborted: refreshed.aborted, errors: Object.fromEntries(refreshed.errors) };
  r.available = {
    ofProvider: (await rt.getAvailable("mr-local")).map((model) => model.id), all: (await rt.getAvailable()).filter((model) => model.provider === "mr-local").map((model) => model.id),
    snapshot: rt.getAvailableSnapshot().filter((model) => model.provider === "mr-local").map((model) => model.id),
    allOfProvider: (await rt.getAllAvailable("mr-local")).map((model) => model.id), ofType: (await rt.getAvailableOfType("chat", "mr-local")).map((model) => model.id),
    unavailableProvider: (await rt.getAvailable("nope-prov")).length,
  };

  const conversation = { systemPrompt: "Be brief.", messages: [{ role: "user", content: "say hello", timestamp: 1 }] };
  const events = [];
  const stream = rt.streamSimple(local, conversation, { maxTokens: 50 });
  for await (const event of stream) events.push(event.type);
  const message = await stream.result();
  const completed = await rt.completeSimple(local, conversation, { maxTokens: 50 });
  const full = await rt.complete(local, conversation, { maxTokens: 50 });
  r.stream = {
    events, stopReason: message.stopReason, text: message.content.map((block) => block.text ?? "").join(""), provider: message.provider, model: message.model, api: message.api,
    completed: completed.content.map((block) => block.text ?? "").join(""), full: full.stopReason,
    unknownProvider: (await rt.completeSimple({ ...local, provider: "nope-prov" }, conversation, {})).stopReason,
  };
  const classified = await rt.classify(local, { state: {}, questions: {} });
  const imaged = await rt.generateImages(local, { input: [{ type: "text", text: "a cat" }] });
  r.typed = { classifyStop: classified.stopReason, classifyError: classified.errorMessage ?? null, imagesStop: imaged.stopReason, imagesError: imaged.errorMessage ?? null };

  const seen = [];
  const events2 = [];
  const credential = await rt.login("openai", "api_key", { prompt: async (prompt) => { seen.push(prompt.type); return "sk-login"; }, notify: (event) => events2.push(event.type) });
  r.login = { credential: plain(credential), prompts: seen, events: events2, listed: (await rt.listCredentials()).map((entry) => entry.providerId).sort(), status: rt.getProviderAuthStatus("openai") };
  await rt.logout("openai");
  r.login.afterLogout = (await rt.listCredentials()).map((entry) => entry.providerId).sort();
  r.stored = readJson(mrAuth);
  return r;
}

// createAgentSession over the objects the extension built: Pi's own run is the expectation (child_handles). One run of the script per SDK; the Go, Python and Rust scripts build the same objects through their SDK and must report the same.
async function childHandlesScript({ mrAuth, mrModels, mrStore, cwd, dir, agentDir, probeExt }) {
  const r = {};
  const rt = await ModelRuntime.create({ authPath: mrAuth, modelsPath: mrModels, modelsStorePath: mrStore });
  const settings = SettingsManager.inMemory({ compaction: { enabled: false }, retry: { enabled: false }, defaultProvider: "mr-local", defaultModel: "m1", defaultThinkingLevel: "high" });
  const quiet = { cwd, agentDir, settingsManager: settings, noExtensions: true, noSkills: true, noPromptTemplates: true, noThemes: true, noContextFiles: true };
  const loader = new DefaultResourceLoader({ ...quiet, additionalExtensionPaths: [probeExt], appendSystemPrompt: ["Handle appendix"] });
  await loader.reload();
  const textOf = (message) => (typeof message?.content === "string" ? message.content : (message?.content ?? []).filter((block) => block.type === "text").map((block) => block.text).join(""));
  const entryKinds = (manager) => manager.getEntries().map((entry) => entry.type + (entry.type === "message" ? ":" + entry.message.role : ""));
  const observe = async (options, manager, bind) => {
    const { session, modelFallbackMessage, extensionsResult } = await createAgentSession({ sessionManager: manager, settingsManager: settings, modelRuntime: rt, ...options });
    const events = [];
    session.subscribe((event) => events.push(event.type + (event.type.startsWith("message_") ? ":" + event.message.role : "")));
    if (bind) await session.bindExtensions({});
    const created = {
      model: { provider: session.model?.provider, id: session.model?.id }, thinkingLevel: session.thinkingLevel, activeTools: session.getActiveToolNames(), fallback: orNull(modelFallbackMessage),
      extensionErrors: extensionsResult.errors.length, appendix: session.systemPrompt.includes("Handle appendix"), sessionId: session.sessionId === manager.getSessionId(),
      sessionFile: orNull(session.sessionFile) === orNull(manager.getSessionFile()), persisted: session.sessionFile !== undefined,
    };
    await session.prompt("hello");
    return { session, report: { created, events: [...events], final: orNull(session.getLastAssistantText()), roles: session.messages.map((message) => message.role), entries: entryKinds(manager), text: textOf(session.messages.at(-1)) } };
  };
  const memory = SessionManager.inMemory(cwd);
  const first = await observe({ resourceLoader: loader, scopedModels: [{ model: rt.getModel("mr-local", "m1"), thinkingLevel: "off" }] }, memory, true);
  r.memory = { ...first.report, settings: { defaultModel: settings.getDefaultModel(), compaction: settings.getCompactionEnabled() } };
  const file = SessionManager.create(cwd, dir);
  const second = await observe({ resourceLoader: new DefaultResourceLoader(quiet) }, file, false);
  r.file = { ...second.report, onDisk: existsSync(file.getSessionFile()), samePath: second.session.sessionFile === file.getSessionFile() };
  // The extension lets go of every object it built; the sessions still run over them.
  await first.session.prompt("again");
  r.afterDrop = { text: textOf(first.session.messages.at(-1)), entries: entryKinds(memory).length, fileEntries: entryKinds(file).length };
  first.session.dispose();
  second.session.dispose();
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
  pi.registerTool({
    name: "resource_loader", label: "resource_loader", description: "Runs the DefaultResourceLoader script.", parameters: { type: "object" },
    execute: async (_id, params) => ({ content: [{ type: "text", text: JSON.stringify(await loaderScript(params)) }] }),
  });
  pi.registerTool({
    name: "model_runtime", label: "model_runtime", description: "Runs the ModelRuntime script.", parameters: { type: "object" },
    execute: async (_id, params) => ({ content: [{ type: "text", text: JSON.stringify(await modelRuntimeScript(params)) }] }),
  });
  pi.registerTool({
    name: "package_manager", label: "package_manager", description: "Runs the DefaultPackageManager script.", parameters: { type: "object" },
    execute: async (_id, params) => ({ content: [{ type: "text", text: JSON.stringify(await packagesScript(params)) }] }),
  });
  pi.registerTool({
    name: "child_handles", label: "child_handles", description: "Runs the createAgentSession handle options script.", parameters: { type: "object" },
    execute: async (_id, params) => ({ content: [{ type: "text", text: JSON.stringify(await childHandlesScript(params)) }] }),
  });
}
