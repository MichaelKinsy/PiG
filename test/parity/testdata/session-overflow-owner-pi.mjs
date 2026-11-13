// An extension that owns compaction continues a turn after a provider overflow: the cases of coding/session_overflow_owner_test.go against the pinned Pi package
// (its dist through the upstream suite harness, not the command line). One OVERFLOW_OWNER_CASE line per case; coding/testdata/overflow-owner-pi.jsonl holds them.
import {createHarness, resolvePackage} from "./session-harness-pi.mjs";
const {fauxAssistantMessage} = await import(resolvePackage("@earendil-works/pi-ai"));
const off = {compaction: {enabled: false}}, on = {compaction: {enabled: true, keepRecentTokens: 1, reserveTokens: 0}};
const cases = [
 ["A turn_end retain-none, before_settle continue, compaction off", off, "turnEnd+settle", 1],
 ["A2 turn_end retain-none and continue only, compaction off", off, "turnEnd", 1],
 ["B before_settle omit, kept compaction, continue, compaction off", off, "settle", 1],
 ["F before_settle continue without entries, compaction off", off, "settleOnly", 1],
 ["C turn_end retain-none, before_settle continue, compaction on", on, "turnEnd+settle", 1],
 ["D session_before_compact supplies the summary, compaction on", on, "beforeCompact", 1],
 ["G session_before_compact cancels, before_settle kept compaction, continue, compaction on", on, "cancel+settle", 1],
 ["E turn_end retain-none, before_settle continue, overflow twice, compaction off", off, "turnEnd+settle", 2],
 ["H before_settle context_edit without a replacement member, compaction off", off, "missingReplacement", 1],
];
const count = (text, mark) => text.split(mark).length - 1;
for (const [name, settings, mode, overflows] of cases) {
 const record = {case: name, calls: 0, requests: [], entries: [], turnEnd: [], beforeSettle: [], beforeCompact: [], compactionStart: 0, compactionEnd: [], settled: 0, projection: [], errors: []};
 let h, handled = false, pending = false, failedId = "";
 const retainNone = mode === "turnEnd+settle" || mode === "turnEnd";
 const lastUser = () => h.sessionManager.getBranch().findLast(entry => entry.type === "message" && entry.message.role === "user").id;
 const factory = pi => {
  pi.on("turn_end", event => {
   record.turnEnd.push(`${event.outcome}:${event.context.canContinue}`);
   if (event.outcome !== "error") return;
   failedId = event.messageEntryId;
   if (!retainNone || handled) return;
   handled = true; pending = true;
   return {entries: [{type: "compaction", summary: "SUMMARY-MARK", firstKeptEntryId: null}], continue: true};
  });
  if (mode !== "turnEnd") pi.on("agent_before_settle", event => {
   record.beforeSettle.push(`${event.outcome}:${event.context.canContinue}`);
   if (mode === "turnEnd+settle") {
    if (!pending) return;
    pending = false;
    return {continue: true};
   }
   if (handled || event.outcome !== "error") return;
   handled = true;
   if (mode === "settleOnly") return {continue: true};
   if (mode === "missingReplacement") return {entries: [{type: "context_edit", targetId: failedId}], continue: true};
   const entries = [];
   if (mode === "settle") entries.push({type: "context_edit", targetId: failedId, replacement: null});
   entries.push({type: "compaction", summary: "SUMMARY-MARK", firstKeptEntryId: lastUser()});
   return {entries, continue: true};
  });
  if (mode === "beforeCompact" || mode === "cancel+settle") pi.on("session_before_compact", event => {
   record.beforeCompact.push(`${event.reason}:${event.willRetry}`);
   if (mode === "cancel+settle") return {cancel: true};
   return {compaction: {summary: "SUMMARY-MARK", firstKeptEntryId: event.preparation.firstKeptEntryId, tokensBefore: event.preparation.tokensBefore}};
  });
 };
 h = await createHarness({models: [{id: "faux-1", contextWindow: 1000, maxTokens: 100}], settings, extensionFactories: [factory]});
 try {
  h.session._extensionRunner.onError(error => record.errors.push(`${error.event}: ${error.error}`));
  // Each response is built 2 ms after its request arrives, so it is later than the entries appended before the request, as a provider response is: one
  // stamped before, or in the same millisecond as, a compaction counts as older than it (agent-session.ts assistantIsFromBeforeCompaction).
  const steps = [];
  for (let i = 0; i < overflows; i++) steps.push(() => fauxAssistantMessage("FAILED-MARK", {stopReason: "error", errorMessage: "prompt is too long"}));
  steps.push(() => fauxAssistantMessage("RECOVERED-MARK"), () => fauxAssistantMessage("EXTRA-MARK"));
  h.setResponses(steps.map(step => context => {
   const raw = JSON.stringify(context.messages);
   record.requests.push({roles: context.messages.map(message => message.role), prompt: count(raw, "PROMPT-MARK"), summary: count(raw, "SUMMARY-MARK"), failed: count(raw, "FAILED-MARK")});
   return new Promise(resolve => setTimeout(() => resolve(step()), 2));
  }));
  await h.session.prompt("PROMPT-MARK " + "x".repeat(5000));
  record.calls = h.faux.state.callCount;
  const labels = new Map();
  for (const entry of h.sessionManager.getEntries()) {
   let label = entry.type;
   if (entry.type === "message") label += ":" + entry.message.role + (entry.message.stopReason === "error" ? ":error" : "");
   else if (entry.type === "context_edit") label += ":" + (entry.replacement === null || entry.replacement === undefined ? "omit" : "replace") + ":" + (labels.get(entry.targetId) ?? "");
   else if (entry.type === "compaction") label += ":" + (entry.firstKeptEntryId === entry.id ? "self" : "kept:" + (labels.get(entry.firstKeptEntryId) ?? ""));
   labels.set(entry.id, label);
   record.entries.push(label);
  }
  record.compactionStart = h.eventsOfType("compaction_start").length;
  record.compactionEnd = h.eventsOfType("compaction_end").map(end => `${end.reason}:aborted=${end.aborted}:willRetry=${end.willRetry}:result=${end.result !== undefined}:${end.errorMessage ?? ""}`);
  record.settled = h.eventsOfType("agent_settled").length;
  record.projection = h.sessionManager.buildSessionProjection().messages.map(message => message.role);
  console.log("OVERFLOW_OWNER_CASE " + JSON.stringify(record));
 } finally { h.cleanup(); }
}
process.exit(0);
