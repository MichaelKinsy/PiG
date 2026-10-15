// Drive the installed Pi's AgentSession (no model call is made): model and thinking-level changes, model cycling over scoped models, queue and mode controls, session name, context usage, and the settings and session entries those leave behind.
import { readFileSync, realpathSync, mkdtempSync, rmSync, mkdirSync } from "node:fs";
import { readFileSync as readText } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { createAgentSession, ModelRuntime, SessionManager, SettingsManager } = await import(pathToFileURL(root + "dist/index.js"));
const { AuthStorage } = await import(pathToFileURL(root + "dist/core/auth-storage.js"));
const { InMemoryModelsStore } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-ai/dist/models-store.js"));
const templateless = (page) => {
  const open = "\n  <script>\n", close = "\n  </script>";
  const at = page.lastIndexOf(open);
  const end = at < 0 ? -1 : page.indexOf(close, at);
  return end < 0 ? page : page.slice(0, at + open.length) + "<TEMPLATE_JS>" + page.slice(end);
};
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const ref = (m) => (m ? `${m.provider}/${m.id}` : null);
const results = [];
const dir = mkdtempSync(join(tmpdir(), "agent-session-oracle-"));
try {
  let n = 0;
  for (const scenario of JSON.parse(input)) {
    const cwd = join(dir, String(n++));
    mkdirSync(cwd);
    const modelRuntime = await ModelRuntime.create({ credentials: AuthStorage.inMemory({ anthropic: { type: "api_key", key: "k1" }, openai: { type: "api_key", key: "k2" } }), modelsStore: new InMemoryModelsStore(), modelsPath: null, allowModelNetwork: false });
    const find = (r) => { const [p, ...rest] = r.split("/"); return modelRuntime.getModel(p, rest.join("/")); };
    const settingsManager = SettingsManager.create(cwd, cwd);
    if (scenario.enabled) settingsManager.setEnabledModels(scenario.enabled);
    const scopedModels = (scenario.scoped ?? []).map((s) => ({ model: find(s.model), ...(s.thinking ? { thinkingLevel: s.thinking } : {}) }));
    const { session } = await createAgentSession({ cwd, agentDir: cwd, modelRuntime, model: find(scenario.model), ...(scenario.thinking ? { thinkingLevel: scenario.thinking } : {}), noTools: "all", scopedModels, settingsManager, sessionManager: SessionManager.inMemory(cwd) });
    const outs = [];
    try {
      const snapshot = () => ({
        model: ref(session.model), thinking: session.thinkingLevel, supports: session.supportsThinking(), levels: session.getAvailableThinkingLevels(),
        steeringMode: session.steeringMode, followUpMode: session.followUpMode, name: session.sessionName ?? null,
        scoped: session.scopedModels.map((s) => `${ref(s.model)}@${s.thinkingLevel ?? ""}`),
        steering: [...session.getSteeringMessages()], followUp: [...session.getFollowUpMessages()], pending: session.pendingMessageCount,
        settings: { provider: settingsManager.getDefaultProvider() ?? null, model: settingsManager.getDefaultModel() ?? null, thinking: settingsManager.getDefaultThinkingLevel() ?? null, enabled: settingsManager.getEnabledModels() ?? null, autoCompaction: settingsManager.getCompactionEnabled?.() ?? null, autoRetry: settingsManager.getRetryEnabled?.() ?? null },
        entries: session.sessionManager.getEntries().map((e) => e.type === "model_change" ? `model_change:${e.provider}/${e.modelId}` : e.type === "thinking_level_change" ? `thinking:${e.thinkingLevel}` : e.type === "session_info" ? `session_info:${e.name}` : e.type === "custom_message" ? `custom_message:${e.customType}` : e.type === "message" ? `message:${e.message.role}` : e.type),
        messages: session.messages.map((m) => { const { timestamp, ...rest } = m; return JSON.parse(JSON.stringify(rest)); }),
        leaf: (() => { const i = session.sessionManager.getEntries().findIndex((e) => e.id === session.sessionManager.getLeafId()); return i < 0 ? null : i; })(),
        parents: session.sessionManager.getEntries().map((e) => (e.parentId === null || e.parentId === undefined ? null : session.sessionManager.getEntries().findIndex((p) => p.id === e.parentId))),
        labels: session.sessionManager.getEntries().map((e) => session.sessionManager.getLabel(e.id) ?? null),
        autoCompactionEnabled: session.autoCompactionEnabled, autoRetryEnabled: session.autoRetryEnabled,
      });
      const initial = snapshot();
      for (const op of scenario.ops) {
        let result = null;
        try {
          switch (op.op) {
            case "setThinking": session.setThinkingLevel(op.level, { persist: op.persist }); break;
            case "cycleThinking": result = session.cycleThinkingLevel({ persist: op.persist }) ?? null; break;
            case "cycleModel": { const r = await session.cycleModel(op.direction, { persist: op.persist }); result = r ? { model: ref(r.model), thinking: r.thinkingLevel, scoped: r.isScoped } : null; break; }
            case "setModel": await session.setModel(find(op.ref), { persist: op.persist }); break;
            case "steeringMode": session.setSteeringMode(op.mode); break;
            case "followUpMode": session.setFollowUpMode(op.mode); break;
            case "name": session.setSessionName(op.name); break;
            case "lastText": result = session.getLastAssistantText() ?? null; break;
            case "usage": { const u = session.getContextUsage(); result = u ? { tokens: u.tokens ?? null, window: u.contextWindow, percent: u.percent ?? null } : null; break; }
            case "steer": await session.steer(op.text); break;
            case "followUp": await session.followUp(op.text); break;
            case "clearQueue": result = session.clearQueue(); break;
            case "autoCompaction": session.setAutoCompactionEnabled(op.enabled); break;
            case "autoRetry": session.setAutoRetryEnabled(op.enabled); break;
            case "custom": await session.sendCustomMessage({ customType: op.customType, content: op.content, display: op.display, ...(op.details === undefined || op.details === null ? {} : { details: op.details }) }, op.options ?? undefined); break;
            case "bash": { const r = await session.executeBash(op.command, undefined, op.exclude ? { excludeFromContext: true } : undefined); result = { output: r.output, exitCode: r.exitCode ?? null, cancelled: r.cancelled, truncated: r.truncated }; break; }
            case "recordBash": session.recordBashResult(op.command, { output: op.output, exitCode: op.exitCode ?? undefined, cancelled: op.cancelled, truncated: op.truncated }, op.exclude ? { excludeFromContext: true } : undefined); break;
            case "abort": session.abortBash(); session.abortCompaction(); session.abortRetry(); session.abortBranchSummary(); await session.waitForIdle(); break;
            case "toolDef": result = session.getToolDefinition(op.tool) ? "defined" : null; break;
            case "handlers": result = session.hasExtensionHandlers(op.event); break;
            case "navigate": {
              const entries = session.sessionManager.getEntries();
              const target = entries.length ? entries[op.index % entries.length] : undefined;
              const r = await session.navigateTree(target ? target.id : "missing", { summarize: false, ...(op.label ? { label: op.label } : {}) });
              result = { cancelled: r.cancelled, aborted: r.aborted ?? null, editorText: r.editorText || null, // Pig's EditorText is a string, so an empty text and an absent one are the same; callers only test it for truthiness.
                 summary: r.summaryEntry ? true : null };
              break;
            }
            case "exportJsonl": {
              const path = join(cwd, "export.jsonl");
              const written = session.exportToJsonl(path);
              const ids = new Map();
              const lines = readText(written, "utf8").split("\n").filter(Boolean).map((l) => JSON.parse(l));
              lines.forEach((l, i) => { if (l.id) ids.set(l.id, i); });
              result = lines.map((l) => { const { id, parentId, timestamp, cwd: c, ...rest } = l; if (rest.message) { const { timestamp: t, ...m } = rest.message; rest.message = m; } return { ...rest, parent: parentId === null || parentId === undefined ? null : ids.get(parentId) ?? "?" }; });
              break;
            }
            case "exportHtml": {
              const path = await session.exportToHtml(join(cwd, "export.html"), op.ref ? { themeName: op.ref } : {});
              const html = readText(path, "utf8");
              const match = /<script id="session-data" type="application\/json">([^<]*)<\/script>/.exec(html);
              const data = JSON.parse(Buffer.from(match[1], "base64").toString("utf8"));
              const ids = new Map();
              data.entries.forEach((e, i) => ids.set(e.id, i));
              const { systemPrompt, tools, ...rest } = data;
              const { id: hid, timestamp: hts, cwd: hcwd, ...header } = rest.header ?? {};
              result = {
                html: templateless(html.replace(match[1], "<DATA>").replaceAll(cwd, "<cwd>")),
                data: { ...rest, header: rest.header ? header : null, leafId: rest.leafId === null ? null : ids.get(rest.leafId) ?? "?",
                  entries: rest.entries.map((e) => { const { id, parentId, timestamp, ...r } = e; if (r.message) { const { timestamp: t, ...m } = r.message; r.message = m; } return { ...r, parent: parentId === null || parentId === undefined ? null : ids.get(parentId) ?? "?" }; }) },
              };
              break;
            }
            case "setScoped": session.setScopedModels(op.scoped.map((x) => ({ model: find(x.model), ...(x.thinking ? { thinkingLevel: x.thinking } : {}) }))); break;
            case "refresh": session.refreshContext(); break;
            case "callable": result = [...session.getCallableToolNames()]; break;
            default: throw new Error("op " + op.op);
          }
        } catch (error) { result = { thrown: String(error && error.message) }; }
        outs.push({ result, state: snapshot() });
      }
      results.push({ initial, outs });
    } finally { session.dispose(); }
  }
} finally { rmSync(dir, { recursive: true, force: true }); }
process.stdout.write(JSON.stringify(results), () => process.exit(0));
