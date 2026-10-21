// The Node fixture of the session graph conformance rows (session_graph_test.go). It runs the script of testfixture/sessiongraph (Go), testdata/session-graph-rust and the inline Python fixture with Pi's own SessionManager, so the report it makes is Pi's.
import { SessionManager } from "@earendil-works/pi-coding-agent";
import { dirname, join } from "node:path";

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

export default function (pi) {
  pi.registerTool({
    name: "session_manager", label: "session_manager", description: "Runs the SessionManager script.", parameters: { type: "object" },
    execute: async (_id, params) => ({ content: [{ type: "text", text: JSON.stringify(await script(params)) }] }),
  });
}
