import assert from "node:assert/strict";
import { createHarness, fauxAssistantMessage, fauxToolCall, Type } from "./pi-session-harness.mjs";

// Exact scenario from Pi's 9340 regression; the subscriber abort precedes post-run policy.
for (let run = 0; run < 20; run++) {
const h = await createHarness({
  models: [{ id: "faux-1", contextWindow: 200, maxTokens: 50 }],
  settings: { compaction: { enabled: true, reserveTokens: 50, keepRecentTokens: 1 }, retry: { enabled: false } },
  extensionFactories: [pi => pi.on("session_before_compact", () => ({ cancel: true }))],
});
try {
  const model = h.getModel();
  h.sessionManager.appendMessage({ role: "user", content: [{ type: "text", text: "x".repeat(500) }], timestamp: 1 });
  h.sessionManager.appendMessage({ ...fauxAssistantMessage("y".repeat(200), { timestamp: 2 }), api: model.api, provider: model.provider, model: model.id, usage: { input: 100, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 100, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } } });
  h.session.agent.state.messages = h.sessionManager.buildSessionContext().messages;
  h.setResponses([fauxAssistantMessage("", { stopReason: "error", errorMessage: "Synthetic network failure" })]);
  h.session.subscribe(event => {
    if (event.type === "message_end" && event.message.role === "assistant") {
      h.session.abortCompaction();
      void h.session.abort();
    }
  });
  await h.session.prompt("z".repeat(1000));
  const starts = h.eventsOfType("compaction_start").length;
  assert.equal(starts, 0);
  console.log(`POST_ABORT compactions=${starts}`);
} finally { h.cleanup(); }

// Public listeners observe message_end before persistence and before tool effects.
const effects = [];
const other = await createHarness({ tools: [{ name: "effect", label: "effect", description: "record an effect", parameters: Type.Object({ value: Type.String() }), execute: async (_id, args) => { effects.push(args); return { content: [{ type: "text", text: "effect" }], details: {} }; } }] });
try {
  other.setResponses([fauxAssistantMessage(fauxToolCall("effect", { value: "blocked" }), { stopReason: "toolUse" }), fauxAssistantMessage("unexpected continuation")]);
  let observed = 0, alreadyPersisted = false;
  other.session.subscribe(event => {
    if (event.type === "message_end" && event.message.role === "assistant" && event.message.content.some(block => block.type === "toolCall")) {
      observed++;
      alreadyPersisted = other.sessionManager.getEntries().some(entry => entry.type === "message" && entry.message.role === "assistant");
      void other.session.abort();
    }
  });
  await other.session.prompt("attempt the effect");
  assert.equal(observed, 1);
  assert.equal(alreadyPersisted, false);
  assert.equal(effects.length, 0);
  console.log(`LISTENER_ORDER persisted=${alreadyPersisted} effects=${effects.length} callbacks=${observed}`);
} finally { other.cleanup(); }
}
