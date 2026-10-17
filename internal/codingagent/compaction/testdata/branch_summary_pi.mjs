// Drive the installed Pi's branch-summarization entry functions over generated session trees, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const b = await import(pathToFileURL(root + "dist/core/compaction/branch-summarization.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const sets = (ops) => ({ read: [...ops.read].sort(), written: [...ops.written].sort(), edited: [...ops.edited].sort() });
process.stdout.write(JSON.stringify(JSON.parse(input).map(({ entries, budget, oldLeaf, target }) => {
  const byId = new Map(entries.map((e) => [e.id, e]));
  const session = {
    getEntry: (id) => byId.get(id),
    getBranch: (from) => { const path = []; let cur = from === undefined ? undefined : byId.get(from); while (cur) { path.unshift(cur); cur = cur.parentId === null ? undefined : byId.get(cur.parentId); } return path; },
  };
  const attempt = (fn) => { try { return { ok: fn() }; } catch (e) { return { err: String(e && e.message || e) }; } };
  return {
    collect: attempt(() => { const r = b.collectEntriesForBranchSummary(session, oldLeaf, target); return { ids: r.entries.map((e) => e.id), common: r.commonAncestorId }; }),
    prepare: attempt(() => { const r = b.prepareBranchEntries(entries, budget); return { messages: r.messages, fileOps: sets(r.fileOps), totalTokens: r.totalTokens }; }),
  };
})));
