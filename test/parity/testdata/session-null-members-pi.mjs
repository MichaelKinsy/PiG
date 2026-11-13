// What the pinned Pi package writes to a session file for an optional entry member that is left out, null or a value, and what a reader gets after the file is
// loaded again: the rows of coding/session_entry_null_members_test.go against Pi (its dist through the upstream suite harness, not the command line).
// One NULL_MEMBERS line per row; coding/testdata/entry-null-members-pi.jsonl holds them. NULL_MEMBERS_JS=1 adds the rows only a JavaScript author can write
// (JSON.stringify's own rules, members whose type the declarations do not allow), as NULL_MEMBERS_JS lines.
import {createHarness, resolvePackage, production} from "./session-harness-pi.mjs";
import {existsSync, mkdtempSync, readFileSync, rmSync} from "node:fs";
import {tmpdir} from "node:os";
import {join} from "node:path";
const ai = await import(resolvePackage("@earendil-works/pi-ai"));
const {fauxAssistantMessage} = ai;
const {SessionManager} = await production("core/session-manager.js");
const jsRows = process.env.NULL_MEMBERS_JS === "1";
const dir = mkdtempSync(join(tmpdir(), "null-members-"));
const usage = {input: 1, output: 2, cacheRead: 0, cacheWrite: 0, totalTokens: 3, cost: {input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0}};
const rows = [];
// What a reader of the labels gets for the labelled entry after a row that sets a label.
let labelSeen;
const linesOf = file => (file && existsSync(file) ? readFileSync(file, "utf8").split("\n").filter(Boolean) : []);
// row records the lines an action adds to the session file. A row a Go caller cannot write is a JavaScript row.
async function row(name, file, action, js = false) {
 if (js && !jsRows) return;
 const from = linesOf(file()).length;
 let error = "";
 labelSeen = undefined;
 try { await action(); } catch (e) { error = e instanceof Error ? e.message : String(e); }
 rows.push({name, file: file(), from, to: linesOf(file()).length, error, js, label: labelSeen});
}
const norm = line => line
 .replace(/"(id|parentId|targetId|firstKeptEntryId|fromId)":"[0-9a-f]{8}"/g, '"$1":"#"')
 .replace(/"timestamp":"\d{4}-[^"]*Z"/g, '"timestamp":"T"')
 .replace(/"timestamp":\d+/g, '"timestamp":0')
 .replace(/"tokensBefore":\d+/g, '"tokensBefore":0')
 .replace(/"systemMessage":\{.*\}\}$/, '"systemMessage":{}}');
const memberNames = {custom: ["data"], custom_message: ["content", "display", "details"], compaction: ["details", "usage", "fromHook"], branch_summary: ["details", "usage", "fromHook"],
 label: ["label"], session_info: ["name"], context_edit: ["replacement"], usage: ["note"], model_change: ["provider", "modelId"], thinking_level_change: ["thinkingLevel"]};
const state = (object, key) => `${key}=${key in object ? (object[key] === null ? "null" : typeof object[key]) : "undefined"}`;
function read(entry) {
 if (!entry) return "missing";
 if (entry.type === "message") return `message:${entry.message.role}:` + (entry.message.role === "toolResult" ? state(entry.message, "details") : entry.message.role === "bashExecution" ? state(entry.message, "exitCode") : "");
 return `${entry.type}:` + (memberNames[entry.type] ?? []).map(key => state(entry, key)).join(",");
}

// An extension: pi.appendEntry, pi.sendMessage, pi.setLabel, boundary drafts, and the results of session_before_compact and session_before_tree.
let pi, drafts, compaction, summary;
const errors = [];
const h = await createHarness({settings: {compaction: {enabled: false, keepRecentTokens: 1, reserveTokens: 0}}, sessionManager: SessionManager.create(dir, join(dir, "extension")), extensionFactories: [api => {
 pi = api;
 pi.on("agent_before_settle", () => {
  if (!drafts) return;
  const entries = drafts;
  drafts = undefined;
  return {entries};
 });
 pi.on("session_before_compact", event => compaction && {compaction: {summary: "hook-compaction", firstKeptEntryId: event.preparation.firstKeptEntryId, tokensBefore: event.preparation.tokensBefore, ...compaction}});
 pi.on("session_before_tree", () => summary && {summary: {summary: "hook-branch", ...summary}});
 pi.registerTool({name: "nulltool", label: "nulltool", description: "returns details as asked", parameters: {type: "object", properties: {mode: {type: "string"}}},
  execute: async (_id, params) => ({content: [{type: "text", text: "r"}], ...(params.mode === "null" ? {details: null} : params.mode === "value" ? {details: {k: 1}} : {})})});
}]});
h.session._extensionRunner.onError(error => errors.push(`${error.event}: ${error.error}`));
h.setResponses(Array.from({length: 60}, () => () => fauxAssistantMessage("ok")));
const file = () => h.sessionManager.getSessionFile();
const prompt = () => h.session.prompt("PROMPT " + "x".repeat(2000));
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const idle = async () => { await sleep(40); for (let i = 0; i < 500 && h.session.isStreaming; i++) await sleep(10); await sleep(40); };
const lastUser = () => h.sessionManager.getBranch().findLast(entry => entry.type === "message" && entry.message.role === "user").id;
const draft = (name, entries, js = false) => row(name, file, async () => { drafts = entries; await prompt(); }, js);
await prompt();
await row("extension appendEntry data omitted", file, () => pi.appendEntry("extension-custom"));
await row("extension appendEntry data null", file, () => pi.appendEntry("extension-custom", null));
await row("extension appendEntry data value", file, () => pi.appendEntry("extension-custom", {k: 1}));
await row("extension sendMessage details omitted", file, async () => { pi.sendMessage({customType: "extension-message", content: "x", display: false}); await idle(); });
await row("extension sendMessage details null", file, async () => { pi.sendMessage({customType: "extension-message", content: "x", display: true, details: null}); await idle(); });
await row("extension sendMessage details value", file, async () => { pi.sendMessage({customType: "extension-message", content: "x", display: true, details: {k: 1}}); await idle(); });
await row("extension sendMessage details null, triggerTurn", file, async () => { pi.sendMessage({customType: "extension-message", content: "x", display: true, details: null}, {triggerTurn: true}); await idle(); });
await row("extension sendMessage details omitted, triggerTurn", file, async () => { pi.sendMessage({customType: "extension-message", content: "x", display: true}, {triggerTurn: true}); await idle(); });
await row("extension setSessionName value", file, () => pi.setSessionName("n"));
await draft("draft custom data omitted", [{type: "custom", customType: "draft-custom"}]);
await draft("draft custom data null", [{type: "custom", customType: "draft-custom", data: null}]);
await draft("draft custom data value", [{type: "custom", customType: "draft-custom", data: {k: 1}}]);
await draft("draft custom_message details omitted", [{type: "custom_message", customType: "draft-message", content: "x", display: false}]);
await draft("draft custom_message details null", [{type: "custom_message", customType: "draft-message", content: "x", display: false, details: null}]);
await draft("draft custom_message details value", [{type: "custom_message", customType: "draft-message", content: "x", display: true, details: {k: 1}}]);
await draft("draft compaction members omitted", [{type: "compaction", summary: "draft-compaction"}]);
await draft("draft compaction members null", [{type: "compaction", summary: "draft-compaction", firstKeptEntryId: null, details: null, usage: null}]);
await draft("draft compaction members value", [{type: "compaction", summary: "draft-compaction", firstKeptEntryId: null, details: {k: 1}, usage}]);
for (const [name, members] of [["omitted", {}], ["null", {details: null, usage: null}], ["value", {details: {k: 1}, usage}]]) {
 await row(`session_before_compact result members ${name}`, file, async () => { await prompt(); compaction = members; try { await h.session.compact(); } finally { compaction = undefined; } });
}
for (const [name, members] of [["omitted", {}], ["null", {details: null, usage: null}], ["value", {details: {k: 1}, usage}]]) {
 await row(`session_before_tree summary members ${name}`, file, async () => { await prompt(); summary = members; try { await h.session.navigateTree(lastUser(), {summarize: true}); } finally { summary = undefined; } });
}
// The session API: SessionManager's own append methods, as a program that holds the SessionManager calls them.
const sm = h.sessionManager;
const userId = lastUser();
await row("api custom data omitted", file, () => sm.appendCustomEntry("api-custom"));
await row("api custom data null", file, () => sm.appendCustomEntry("api-custom", null));
await row("api custom data value", file, () => sm.appendCustomEntry("api-custom", {k: 1}));
await row("api custom_message details omitted", file, () => sm.appendCustomMessageEntry("api-message", "x", false));
await row("api custom_message details null", file, () => sm.appendCustomMessageEntry("api-message", "x", true, null));
await row("api custom_message details value", file, () => sm.appendCustomMessageEntry("api-message", "x", true, {k: 1}));
await row("api compaction members omitted", file, () => sm.appendCompaction("api-compaction", userId, 10));
await row("api compaction details and usage null", file, () => sm.appendCompaction("api-compaction", userId, 10, null, undefined, null));
await row("api compaction members value", file, () => sm.appendCompaction("api-compaction", userId, 10, {k: 1}, true, usage));
await row("api compaction fromHook false", file, () => sm.appendCompaction("api-compaction", userId, 10, undefined, false));
await row("api compaction firstKeptEntryId null", file, () => sm.appendCompaction("api-compaction", null, 10));
await row("api branch_summary details and usage omitted", file, () => sm.branchWithSummary(sm.getLeafId(), "api-branch", undefined, false));
await row("api branch_summary details and usage null", file, () => sm.branchWithSummary(sm.getLeafId(), "api-branch", null, false, null));
await row("api branch_summary members value", file, () => sm.branchWithSummary(sm.getLeafId(), "api-branch", {k: 1}, true, usage));
await row("api label omitted", file, () => sm.appendLabelChange(userId));
await row("api label null", file, () => sm.appendLabelChange(userId, null));
await row("api label empty", file, () => sm.appendLabelChange(userId, ""));
await row("api label value", file, () => sm.appendLabelChange(userId, "x"));
await row("api usage note omitted", file, () => sm.appendUsage("k", "p", "m", usage));
await row("api usage note empty", file, () => sm.appendUsage("k", "p", "m", usage, ""));
await row("api usage note value", file, () => sm.appendUsage("k", "p", "m", usage, "n"));
await row("api session_info name empty", file, () => sm.appendSessionInfo(""));
await row("api session_info name value", file, () => sm.appendSessionInfo(" n\nm "));
await row("api branch_summary fromHook omitted", file, () => sm.branchWithSummary(sm.getLeafId(), "api-branch"));
await draft("draft custom_message display omitted", [{type: "custom_message", customType: "draft-message", content: "x"}]);
await draft("draft custom_message content omitted", [{type: "custom_message", customType: "draft-message", display: true}]);
await row("extension sendMessage display omitted", file, async () => { pi.sendMessage({customType: "extension-message", content: "x"}); await idle(); });
await row("extension sendMessage display omitted, triggerTurn", file, async () => { pi.sendMessage({customType: "extension-message", content: "x"}, {triggerTurn: true}); await idle(); });
// pi.setLabel without a label, with a null one, an empty one and a value, each after a label a reader can see cleared.
const setLabel = (name, ...label) => row(`extension setLabel ${name}`, file, () => {
 const id = lastUser();
 pi.setLabel(id, "mark");
 pi.setLabel(id, ...label);
 labelSeen = String(h.sessionManager.getLabel(id));
});
await setLabel("omitted");
await setLabel("null", null);
await setLabel("empty", "");
await setLabel("value", "x");
// A usage an extension wrote in another member order and with members Pi's Usage does not declare: Pi stores the author's object.
const usageReordered = {cost: {total: 0, input: 0, output: 0, cacheRead: 0, cacheWrite: 0}, totalTokens: 3, output: 2, input: 1, cacheRead: 0, cacheWrite: 0};
const usageAuthored = {...usageReordered, extra: 1};
await draft("draft compaction usage in another member order", [{type: "compaction", summary: "draft-compaction", usage: usageReordered}]);
await draft("draft compaction usage with extra members", [{type: "compaction", summary: "draft-compaction", usage: {...usage, extra: 1, cost: {...usage.cost, extra: 2}}}]);
await row("session_before_compact result usage as its author wrote it", file, async () => { await prompt(); compaction = {usage: usageAuthored}; try { await h.session.compact(); } finally { compaction = undefined; } });
await row("session_before_tree summary usage as its author wrote it", file, async () => { await prompt(); summary = {usage: usageAuthored}; try { await h.session.navigateTree(lastUser(), {summarize: true}); } finally { summary = undefined; } });
// A display written as null, which the declaration does not allow and nothing checks: Pi stores the null.
await draft("draft custom_message display null", [{type: "custom_message", customType: "draft-message", content: "x", display: null}]);
await row("extension sendMessage display null", file, async () => { pi.sendMessage({customType: "extension-message", content: "x", display: null}); await idle(); });
// A message sent without content or with a null one, which the declaration does not allow: sendCustomMessage reads `message.content ?? []`, so both write an empty list.
await row("extension sendMessage content omitted", file, async () => { pi.sendMessage({customType: "extension-message", display: true}); await idle(); });
await row("extension sendMessage content null", file, async () => { pi.sendMessage({customType: "extension-message", content: null, display: true}); await idle(); });
// JavaScript rows: values the declarations do not allow, and JSON.stringify's rules.
await row("js api compaction fromHook null", file, () => sm.appendCompaction("api-compaction", userId, 10, undefined, null), true);
await row("js api branch_summary fromHook null", file, () => sm.branchWithSummary(sm.getLeafId(), "api-branch", undefined, null), true);
await row("js api context_edit replacement null", file, () => sm.appendContextEdit(userId, null), true);
await row("js api context_edit replacement value", file, () => sm.appendContextEdit(userId, {content: "c"}), true);
await row("js api toolResult details omitted", file, () => sm.appendMessage({role: "toolResult", toolCallId: "c1", toolName: "t", content: [{type: "text", text: "r"}], isError: false, timestamp: 3}), true);
await row("js api toolResult details null", file, () => sm.appendMessage({role: "toolResult", toolCallId: "c1", toolName: "t", content: [{type: "text", text: "r"}], details: null, isError: false, timestamp: 3}), true);
await draft("js draft custom_message content null", [{type: "custom_message", customType: "draft-message", content: null, display: true}], true);
await draft("js draft custom data NaN", [{type: "custom", customType: "draft-custom", data: NaN}], true);
await row("js extension setSessionName null", file, () => pi.setSessionName(null), true);
await row("js extension setSessionName undefined", file, () => pi.setSessionName(undefined), true);
await row("js extension appendEntry data NaN", file, () => pi.appendEntry("extension-custom", NaN), true);
await row("js extension appendEntry data function", file, () => pi.appendEntry("extension-custom", () => 1), true);
if (jsRows && ai.fauxToolCall) {
 for (const mode of ["omitted", "null", "value"]) {
  await row(`js extension tool result details ${mode}`, file, async () => {
   h.setResponses([() => fauxAssistantMessage([ai.fauxToolCall("nulltool", {mode})], {stopReason: "toolUse"}), ...Array.from({length: 40}, () => () => fauxAssistantMessage("ok"))]);
   await prompt();
  }, true);
 }
} else if (jsRows) console.log("NULL_MEMBERS_JS no fauxToolCall export: " + Object.keys(ai).filter(key => key.startsWith("faux")).join(","));

await row("js api custom data undefined", file, () => sm.appendCustomEntry("api-custom", undefined), true);
await row("js api custom data NaN", file, () => sm.appendCustomEntry("api-custom", NaN), true);
await row("js api custom data Infinity", file, () => sm.appendCustomEntry("api-custom", -Infinity), true);
await row("js api custom data function", file, () => sm.appendCustomEntry("api-custom", () => 1), true);
await row("js api custom data symbol", file, () => sm.appendCustomEntry("api-custom", Symbol("s")), true);
await row("js api custom data array of undefined function NaN", file, () => sm.appendCustomEntry("api-custom", [undefined, () => 1, NaN, -0, null]), true);
await row("js api custom data object of undefined function NaN date toJSON", file, () => sm.appendCustomEntry("api-custom", {u: undefined, f() {}, n: NaN, d: new Date(0), j: {toJSON: () => null}, z: null}), true);
await row("js api custom data zero", file, () => sm.appendCustomEntry("api-custom", 0), true);
await row("js api custom data empty string", file, () => sm.appendCustomEntry("api-custom", ""), true);
await row("js api custom data false", file, () => sm.appendCustomEntry("api-custom", false), true);
await row("js api custom_message display undefined", file, () => sm.appendCustomMessageEntry("api-message", "x"), true);
await row("js api custom_message display null", file, () => sm.appendCustomMessageEntry("api-message", "x", null), true);
await row("js api custom_message content null", file, () => sm.appendCustomMessageEntry("api-message", null, true), true);
await row("js api custom_message content undefined", file, () => sm.appendCustomMessageEntry("api-message", undefined, true), true);
await row("js api compaction summary tokensBefore null", file, () => sm.appendCompaction(null, undefined, null), true);
await row("js api label undefined", file, () => sm.appendLabelChange(userId, undefined), true);
await row("js api context_edit replacement undefined", file, () => sm.appendContextEdit(userId, undefined), true);
await row("js api usage note null", file, () => sm.appendUsage("k", "p", "m", usage, null), true);
await row("js api session_info name null", file, () => sm.appendSessionInfo(null), true);
await row("js api session_info name undefined", file, () => sm.appendSessionInfo(undefined), true);
await row("js api model_change null", file, () => sm.appendModelChange(null, null), true);
await row("js api model_change undefined", file, () => sm.appendModelChange(undefined, undefined), true);
await row("js api thinking_level_change null", file, () => sm.appendThinkingLevelChange(null), true);
await row("js api thinking_level_change undefined", file, () => sm.appendThinkingLevelChange(undefined), true);
await row("js api bashExecution exitCode undefined", file, () => sm.appendMessage({role: "bashExecution", command: "c", output: "o", exitCode: undefined, cancelled: false, truncated: false, timestamp: 4}), true);
await row("js api bashExecution exitCode null", file, () => sm.appendMessage({role: "bashExecution", command: "c", output: "o", exitCode: null, cancelled: false, truncated: false, timestamp: 4}), true);
await row("js api custom data bigint", file, () => sm.appendCustomEntry("api-custom", 1n), true);

const reloaded = new Map();
for (const path of new Set(rows.map(entry => entry.file))) reloaded.set(path, new Map(SessionManager.open(path).getEntries().map(entry => [entry.id, entry])));
for (const {name, file: path, from, to, error, js, label} of rows) {
 const lines = [], reads = [];
 for (const line of linesOf(path).slice(from, to)) {
  const entry = JSON.parse(line);
  if (entry.type === "message" && (entry.message.role === "user" || entry.message.role === "assistant")) continue;
  lines.push(norm(line));
  reads.push(read(reloaded.get(path).get(entry.id)));
 }
 console.log((js ? "NULL_MEMBERS_JS " : "NULL_MEMBERS ") + JSON.stringify({row: name, error, lines, read: reads, ...(label === undefined ? {} : {label})}));
}
if (errors.length > 0) console.log("NULL_MEMBERS " + JSON.stringify({row: "extension errors", error: errors.join("; "), lines: [], read: []}));
h.cleanup();
rmSync(dir, {recursive: true, force: true});
process.exit(0);
