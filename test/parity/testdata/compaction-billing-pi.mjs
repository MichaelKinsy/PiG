import assert from "node:assert/strict";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
const root = resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
const { InteractiveMode } = await import(pathToFileURL(root + "/dist/modes/interactive/interactive-mode.js"));
const { initTheme } = await import(pathToFileURL(root + "/dist/modes/interactive/theme/theme.js"));
const { Container } = await import(import.meta.resolve("@earendil-works/pi-tui", pathToFileURL(root + "/package.json").href));
initTheme("dark");
const usage = { input: 10, output: 20, cacheRead: 30, cacheWrite: 40, totalTokens: 100, cost: { input: .01, output: .02, cacheRead: .03, cacheWrite: .065, total: .125 } };
const state = { chatContainer: new Container(), settingsManager: { getShowCacheMissNotices: () => true } };
for (const kind of ["compaction", "branch_summary"]) InteractiveMode.prototype.addCompactionCostNotice.call(state, { type: "compaction_cost", kind, usage });
const rows = state.chatContainer.render(120);
assert(rows.some(row => row.includes("Compaction: 100 tokens billed (~$0.13)")));
assert(rows.some(row => row.includes("Branch summary: 100 tokens billed (~$0.13)")));
console.log("COMPACTION_BILLING " + JSON.stringify(rows));

// The compaction-end path renders every retained entry before the new summary,
// including entries appended after the persisted compaction boundary.
const order = [];
const latest = { type: "compaction", id: "latest", summary: "summary", firstKeptEntryId: "retained", tokensBefore: 123, usage };
const retained = { type: "message", id: "retained", message: { role: "user", content: "retained" } };
const tail = { type: "message", id: "tail", message: { role: "user", content: "tail" } };
const target = {
  isInitialized: true, footer: { invalidate() {} }, defaultEditor: {},
  statusContainer: { clear() {} }, chatContainer: { clear() { order.push("clear"); } },
  sessionManager: { buildContextEntries: () => [latest, retained, tail] },
  renderSessionEntries(entries) { order.push(...entries.map(entry => entry.id)); },
  addMessageToChat(message) { assert.equal(message.role, "compactionSummary"); assert.equal(message.tokensBefore, 123); order.push("summary"); },
  addCompactionCostNotice(notice) { assert.equal(notice.usage, usage); order.push("cost"); },
  clearStatusIndicator() {}, showError(error) { throw new Error(error); }, showStatus() {},
  async flushCompactionQueue(options) { assert.deepEqual(options, { willRetry: false }); order.push("flush"); },
  settingsManager: { getShowTerminalProgress: () => false },
  ui: { requestRender() {}, terminal: { setProgress() {} } },
};
await InteractiveMode.prototype.handleEvent.call(target, { type: "compaction_end", reason: "manual", result: { tokensBefore: 123, summary: "summary", usage }, aborted: false, willRetry: false });
assert.deepEqual(order, ["clear", "retained", "tail", "summary", "cost", "flush"]);
