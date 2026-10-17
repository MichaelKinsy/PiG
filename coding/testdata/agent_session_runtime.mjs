// Drive the installed Pi's AgentSessionRuntime over persisted session files: switchSession, newSession, importFromJsonl, the rebind and before-invalidate callbacks, and the sessions each leaves behind.
import { readFileSync, realpathSync, mkdtempSync, rmSync, mkdirSync, writeFileSync, readdirSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, basename } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { createAgentSessionRuntime, createAgentSessionServices, createAgentSessionFromServices, SessionManager } = await import(pathToFileURL(root + "dist/index.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.env.PI_OFFLINE = "1";
const results = [];
const base = mkdtempSync(join(tmpdir(), "agent-runtime-oracle-"));
try {
  let n = 0;
  for (const scenario of JSON.parse(input)) {
    const dir = join(base, String(n++));
    const cwd = join(dir, "cwd");
    const agentDir = join(dir, "agent");
    const sessionDir = join(dir, "sessions");
    mkdirSync(cwd, { recursive: true });
    mkdirSync(agentDir, { recursive: true });
    mkdirSync(sessionDir, { recursive: true });
    // Prepared session files: each is a persisted session with the given user messages.
    const files = scenario.files.map((spec, i) => {
      const sm = SessionManager.create(spec.cwd === "missing" ? join(dir, "gone") : cwd, sessionDir);
      for (const text of spec.messages) sm.appendMessage({ role: "user", content: text, timestamp: 1 });
      if (spec.messages.length === 0) sm.appendSessionInfo?.("empty");
      return sm.getSessionFile();
    });
    const external = join(dir, "external");
    mkdirSync(external);
    const importable = scenario.imports.map((spec, i) => {
      const sm = SessionManager.create(cwd, external);
      for (const text of spec.messages) sm.appendMessage({ role: "user", content: text, timestamp: 1 });
      return sm.getSessionFile();
    });
    const factory = async ({ cwd: c, agentDir: a, sessionManager, sessionStartEvent }) => {
      const services = await createAgentSessionServices({ cwd: c, agentDir: a });
      return { ...(await createAgentSessionFromServices({ services, sessionManager, sessionStartEvent, noTools: "all" })), services, diagnostics: services.diagnostics };
    };
    const calls = [];
    const runtime = await createAgentSessionRuntime(factory, { cwd, agentDir, sessionManager: SessionManager.create(cwd, sessionDir) });
    runtime.setRebindSession(async (session) => { calls.push("rebind"); });
    runtime.setBeforeSessionInvalidate(() => { calls.push("invalidate"); });
    const norm = (p) => (p ? p.replaceAll(dir, "<dir>").replaceAll(process.cwd(), "<proc>").replace(/\d{4}-\d\d-\d\dT[\d-]+Z_[0-9a-f-]+\.jsonl/g, "<session>.jsonl") : p);
    const state = () => {
      const sm = runtime.session.sessionManager;
      return { cwd: norm(runtime.cwd), file: norm(runtime.session.sessionFile ?? null) && (runtime.session.sessionFile.startsWith(sessionDir) ? "<sessions>/" + (files.includes(runtime.session.sessionFile) ? "f" + files.indexOf(runtime.session.sessionFile) : "new") : norm(runtime.session.sessionFile)), entries: sm.getEntries().map((e) => (e.type === "message" ? `message:${e.message.role}:${typeof e.message.content === "string" ? e.message.content : "?"}` : e.type)), header: (() => { const h = sm.getHeader?.(); return h ? { cwd: norm(h.cwd), parentSession: h.parentSession ? (files.includes(h.parentSession) ? "f" + files.indexOf(h.parentSession) : norm(h.parentSession)) : null } : null; })(), dirFiles: readdirSync(sessionDir).map((name) => { const f = files.findIndex((p) => basename(p) === name); if (f >= 0) return "f" + f; const i = importable.findIndex((p) => basename(p) === name); if (i >= 0) return "imp" + i; const m = importable.findIndex((p) => name.startsWith(basename(p).replace(/\.jsonl$/, "") + "-")); return m >= 0 ? "imp" + m + "-" + name.slice(basename(importable[m]).length - 5) : "other"; }).sort() };
    };
    const outs = [];
    try {
      const initial = state();
      for (const op of scenario.ops) {
        calls.length = 0;
        let result = null;
        try {
          switch (op.op) {
            case "switch": result = await runtime.switchSession(files[op.index % files.length]); break;
            case "switchMissing": result = await runtime.switchSession(join(sessionDir, "nope.jsonl")); break;
            case "new": result = await runtime.newSession(op.parent ? { parentSession: files[op.index % files.length] } : undefined); break;
            case "import": result = await runtime.importFromJsonl(importable[op.index % importable.length]); break;
            case "importStored": result = await runtime.importFromJsonl(files[op.index % files.length]); break;
            case "importMissing": result = await runtime.importFromJsonl(join(dir, "nope.jsonl")); break;
            case "rebindOff": runtime.setRebindSession(undefined); break;
            case "invalidateOff": runtime.setBeforeSessionInvalidate(undefined); break;
            default: throw new Error("op " + op.op);
          }
        } catch (error) { result = { thrown: norm(String(error && error.message)) }; }
        outs.push({ result, calls: [...calls], state: state() });
      }
      results.push({ initial, outs });
    } finally { await runtime.dispose(); }
  }
} finally { rmSync(base, { recursive: true, force: true }); }
process.stdout.write(JSON.stringify(results), () => process.exit(0));
