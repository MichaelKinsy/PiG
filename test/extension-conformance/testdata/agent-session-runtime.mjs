// The Node fixture of the agent_session_runtime row (agent_session_runtime_test.go): Pi's own createAgentSessionRuntime and AgentSessionRuntime over a factory that builds cwd-bound services and a session, through every replacement method. Its report is the expectation; testfixture/sessiongraph (Go), testdata/session-graph-rust and the inline Python fixture run the same script through their SDK's classes.
import { ModelRuntime, SessionManager, SettingsManager, createAgentSessionFromServices, createAgentSessionRuntime, createAgentSessionServices } from "@earendil-works/pi-coding-agent";
import { copyFileSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { basename, join } from "node:path";

const orNull = (value) => (value === undefined ? null : value);
const error = async (fn) => {
  try {
    await fn();
  } catch (e) {
    return "error: " + e.message;
  }
  return "no error";
};
const textOf = (message) => (typeof message?.content === "string" ? message.content : (message?.content ?? []).filter((block) => block.type === "text").map((block) => block.text).join(""));

export async function agentSessionRuntimeScript({ cwd, agentDir, root, runtimeProbe, mrAuth, mrModels, mrStore }) {
  const r = {};
  const rt = await ModelRuntime.create({ authPath: mrAuth, modelsPath: mrModels, modelsStorePath: mrStore });
  const settings = SettingsManager.inMemory({ compaction: { enabled: false }, retry: { enabled: false }, defaultProvider: "mr-local", defaultModel: "m1" });
  const quiet = { noExtensions: true, noSkills: true, noPromptTemplates: true, noThemes: true, noContextFiles: true };
  const sessionDir = join(root, "runtime-sessions");
  const logFile = join(cwd, "runtime-events.log");
  const cancelFile = join(cwd, "cancel-next");
  mkdirSync(cwd, { recursive: true });
  const calls = [];
  const factory = async (options) => {
    calls.push({ cwd: options.cwd, reason: options.sessionStartEvent?.reason ?? null, previous: options.sessionStartEvent?.previousSessionFile !== undefined, persisted: options.sessionManager.isPersisted() });
    const services = await createAgentSessionServices({
      cwd: options.cwd, agentDir: options.agentDir, settingsManager: settings, modelRuntime: rt, resourceLoaderOptions: { ...quiet, additionalExtensionPaths: [runtimeProbe] },
    });
    const { session, modelFallbackMessage } = await createAgentSessionFromServices({ services, sessionManager: options.sessionManager, sessionStartEvent: options.sessionStartEvent });
    return { session, services, diagnostics: services.diagnostics, modelFallbackMessage };
  };
  const manager = SessionManager.create(cwd, sessionDir);
  const firstFile = () => runtimeFile;
  const runtime = await createAgentSessionRuntime(factory, { cwd, agentDir, sessionManager: manager });
  let rebinds = 0;
  let invalidations = 0;
  runtime.setRebindSession(async (session) => {
    rebinds++;
    await session.bindExtensions({});
  });
  runtime.setBeforeSessionInvalidate(() => {
    invalidations++;
  });
  await runtime.session.bindExtensions({});
  const seen = new Set([manager.getSessionId()]);
  const isNew = () => {
    const id = runtime.session.sessionManager.getSessionId();
    const fresh = !seen.has(id);
    seen.add(id);
    return fresh;
  };
  const kinds = () => runtime.session.sessionManager.getEntries().map((entry) => entry.type);
  const userEntry = (text) => runtime.session.sessionManager.getEntries().find((entry) => entry.type === "message" && entry.message.role === "user" && textOf(entry.message) === text);
  const cancelling = async (fn) => {
    writeFileSync(cancelFile, "");
    try {
      return await fn();
    } finally {
      rmSync(cancelFile);
    }
  };
  const runtimeFile = manager.getSessionFile();
  r.initial = {
    cwd: runtime.cwd, diagnostics: runtime.diagnostics, fallback: orNull(runtime.modelFallbackMessage), sameLog: runtime.session.sessionManager.getSessionId() === manager.getSessionId(),
    servicesCwd: runtime.services.cwd, servicesAgentDir: runtime.services.agentDir === agentDir, calls: calls.length,
  };
  await runtime.session.prompt("one");
  r.prompted = { kinds: kinds(), persisted: existsSync(firstFile()) };

  // newSession: cancelled by a handler, then with a parent session and a setup that adds a context message.
  const cancelledNew = await cancelling(() => runtime.newSession());
  r.newCancelled = { result: cancelledNew, sameSession: !isNew(), calls: calls.length, rebinds, invalidations };
  const created = await runtime.newSession({
    parentSession: firstFile(),
    setup: async (sm) => {
      sm.appendCustomMessageEntry("setup-note", "from setup", true);
    },
  });
  r.newSession = {
    result: created, fresh: isNew(), parentIsFirst: runtime.session.sessionManager.getHeader()?.parentSession === firstFile(), kinds: kinds(), roles: runtime.session.messages.map((message) => message.role),
    calls: calls.slice(1), rebinds, invalidations,
  };
  await runtime.session.prompt("two");
  const secondFile = runtime.session.sessionManager.getSessionFile();

  // fork: cancelled, before a user message, at an assistant message, and the errors.
  const two = userEntry("two");
  r.forkCancelled = { result: await cancelling(() => runtime.fork(two.id)), sameSession: !isNew() };
  const forked = await runtime.fork(two.id);
  r.fork = {
    result: forked, fresh: isNew(), parentIsNotSecond: runtime.session.sessionManager.getSessionFile() !== secondFile, roles: runtime.session.messages.map((message) => message.role),
    kinds: kinds(), calls: calls.length - 2,
  };
  await runtime.session.prompt("three");
  const assistant = runtime.session.sessionManager.getEntries().find((entry) => entry.type === "message" && entry.message.role === "assistant");
  const forkedAt = await runtime.fork(assistant.id, { position: "at" });
  r.forkAt = { result: forkedAt, fresh: isNew(), roles: runtime.session.messages.map((message) => message.role) };
  r.forkErrors = {
    missing: await error(() => runtime.fork("no-such-entry")),
    notUser: await error(() => runtime.fork(runtime.session.sessionManager.getEntries().find((entry) => entry.type === "message" && entry.message.role === "assistant").id)),
  };

  // switchSession: cancelled, then back to the first session's file.
  r.switchCancelled = { result: await cancelling(() => runtime.switchSession(firstFile())), sameSession: !isNew() };
  const switched = await runtime.switchSession(firstFile());
  r.switch = { result: switched, backOnFirst: runtime.session.sessionManager.getSessionId() === manager.getSessionId(), file: basename(runtime.session.sessionManager.getSessionFile()) === basename(firstFile()), roles: runtime.session.messages.map((message) => message.role) };

  // A fork before the first message of a session starts an empty session whose parent is this one.
  const one = userEntry("one");
  const emptied = await runtime.fork(one.id);
  r.forkRoot = { result: emptied, fresh: isNew(), parentIsFirst: runtime.session.sessionManager.getHeader()?.parentSession === firstFile(), kinds: kinds() };

  // importFromJsonl: a file outside the session directory is copied in (and gets a free name), a missing file fails.
  const importDir = join(root, "import");
  mkdirSync(importDir, { recursive: true });
  const importPath = join(importDir, "imported.jsonl");
  copyFileSync(firstFile(), importPath);
  const imported = await runtime.importFromJsonl(importPath);
  const importedFile = runtime.session.sessionManager.getSessionFile();
  r.import = {
    result: imported, fresh: isNew(), inSessionDir: importedFile.startsWith(sessionDir), name: basename(importedFile), roles: runtime.session.messages.map((message) => message.role),
    sourceKept: existsSync(importPath), missing: await error(() => runtime.importFromJsonl(join(importDir, "missing.jsonl"))),
  };

  await runtime.dispose();
  r.counts = { rebinds, invalidations, factoryCalls: calls.length, reasons: calls.map((call) => call.reason + (call.previous ? "+previous" : "")) };
  r.events = readFileSync(logFile, "utf8").trimEnd().split("\n");
  return r;
}
