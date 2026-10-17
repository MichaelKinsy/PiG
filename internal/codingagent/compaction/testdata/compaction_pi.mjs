// Drive the installed Pi's compaction and session projection functions over generated session paths, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const c = await import(pathToFileURL(root + "dist/core/compaction/compaction.js"));
const s = await import(pathToFileURL(root + "dist/core/session-manager.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const attempt = (fn) => { try { const v = fn(); return { ok: v === undefined ? null : v }; } catch (error) { return { err: String(error && error.message || error) }; } };
const sets = (ops) => ({ read: [...ops.read].sort(), written: [...ops.written].sort(), edited: [...ops.edited].sort() });
const results = JSON.parse(input).map(({ entries, keep, reserve, window }) => {
  const settings = { enabled: true, reserveTokens: reserve, keepRecentTokens: keep };
  const context = attempt(() => s.buildSessionContext(entries));
  const prep = attempt(() => {
    const p = c.prepareCompaction(entries, settings);
    return p ? { ...p, fileOps: sets(p.fileOps), settings: undefined } : null;
  });
  return {
    context,
    prep,
    cut: attempt(() => c.findCutPoint(entries, 0, entries.length, keep)),
    turnStarts: entries.map((_, i) => attempt(() => c.findTurnStartIndex(entries, i, 0))),
    lastUsage: attempt(() => c.getLastAssistantUsage(entries) ?? null),
    estimate: context.ok ? attempt(() => c.estimateContextTokens(context.ok.messages)) : null,
    tokens: context.ok ? context.ok.messages.map((m) => attempt(() => c.estimateTokens(m))) : [],
    should: attempt(() => c.shouldCompact(window, 100000, settings)),
  };
});
process.stdout.write(JSON.stringify(results));
