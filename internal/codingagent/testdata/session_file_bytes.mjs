// Drive the installed Pi's SessionManager over scripted appends and report the bytes of the session file, never a translated oracle.
import { mkdtempSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { readFileSync as read, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(read(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { SessionManager } = await import(pathToFileURL(root + "dist/core/session-manager.js"));
const { serializeSessionBranch } = await import(pathToFileURL(root + "dist/core/session-export.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const ops of JSON.parse(input)) {
  const dir = mkdtempSync(join(tmpdir(), "pi-bytes-"));
  const manager = SessionManager.create("/work/project", dir, { id: "fixed-id" });
  const ids = [];
  const errors = [];
  for (const [index, op] of ops.entries()) {
    let id;
    try {
    switch (op.op) {
      case "message": id = manager.appendMessage(op.message); break;
      case "thinking": id = manager.appendThinkingLevelChange(op.level); break;
      case "model": id = manager.appendModelChange(op.provider, op.modelId); break;
      case "custom": id = manager.appendCustomEntry(op.customType, op.data); break;
      case "customMessage": id = manager.appendCustomMessageEntry(op.customType, op.content, op.display, op.details); break;
      case "info": id = manager.appendSessionInfo(op.name); break;
      case "compaction": id = manager.appendCompaction(op.summary, ids[op.first % ids.length], op.tokensBefore, op.details, op.fromHook); break;
      case "label": id = manager.appendLabelChange(ids[op.target % ids.length], op.label ?? undefined); break;
      case "usage": id = manager.appendUsage(op.kind, op.provider, op.model, op.usage, op.note || undefined).id; break;
      case "branch": manager.branch(ids[op.target % ids.length]); break;
      case "reset": manager.resetLeaf(); break;
      case "branchSummary": id = manager.branchWithSummary(op.target === null ? null : ids[op.target % ids.length], op.summary, op.details, op.fromHook, op.usage ?? undefined); break;
      case "contextEdit": id = manager.appendContextEdit(ids[op.target % ids.length], op.replacement); break;
    }
    } catch (error) { errors.push(index); continue; }
    if (id !== undefined) ids.push(id);
  }
  const file = manager.getSessionFile();
  let text = null;
  try { text = readFileSync(file, "utf8"); } catch {}
  results.push({ text, errors, exported: serializeSessionBranch(manager) });
}
process.stdout.write(JSON.stringify(results));
