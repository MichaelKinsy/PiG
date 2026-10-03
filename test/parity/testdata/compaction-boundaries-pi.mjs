import assert from "node:assert/strict";
import { createHarness, fauxAssistantMessage, createAssistantMessageEventStream } from "./pi-session-harness.mjs";
function seed(h) {
 h.settingsManager.applyOverrides({ compaction: { keepRecentTokens: 1 } });
 const model = h.getModel(), now = Date.now();
 h.sessionManager.appendMessage({ role: "user", content: [{ type: "text", text: "message to compact" }], timestamp: now - 1000 });
 h.sessionManager.appendMessage({ ...fauxAssistantMessage("assistant response to compact", { timestamp: now - 500 }), api: model.api, provider: model.provider, model: model.id, usage: { input: 100, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 100, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } } });
 h.session.agent.state.messages = h.sessionManager.buildSessionContext().messages;
}
const queued = await createHarness({ settings: { compaction: { keepRecentTokens: 1 } }, extensionFactories: [pi => pi.on("session_before_compact", event => ({ compaction: { summary: "manual compacted", firstKeptEntryId: event.preparation.firstKeptEntryId, tokensBefore: event.preparation.tokensBefore, details: { source: "extension" } } }))] });
try {
 seed(queued); queued.setResponses([fauxAssistantMessage("queued response")]);
 let promise, idle;
 queued.session.subscribe(event => {
  if (event.type === "compaction_end" && event.reason === "manual" && event.result) {
   idle = !queued.session.isCompacting;
   promise = queued.session.prompt("queued after compaction");
  }
 });
 await queued.session.compact();
 const beforeReturn = promise !== undefined;
 assert(beforeReturn); assert(idle);
 await promise;
 const text = queued.session.getLastAssistantText(); assert.equal(text, "queued response");
 console.log(`COMPACTION_BOUNDARY queue beforeReturn=${beforeReturn} idle=${idle} text=${JSON.stringify(text)}`);
} finally { queued.cleanup(); }
for (const name of ["no-model", "no-auth"]) {
 const h = await createHarness({ withConfiguredAuth: name !== "no-auth" });
 // agent-session-compaction.test.ts:282-288: auth is resolved only when Pi summarizes itself, after checking there is something to compact.
 if (name === "no-auth") seed(h);
 const previousPackageDir=process.env.PI_PACKAGE_DIR;
 try {
  process.env.PI_PACKAGE_DIR=process.cwd();
  if (name === "no-model") h.session.agent.state.model = undefined;
  await assert.rejects(h.session.compact(), error => {
   assert(error.message.includes(name === "no-model" ? "No model selected" : "No API key found for faux."));
   console.log(`COMPACTION_BOUNDARY ${name} ${JSON.stringify(error.message)}`);
   return true;
  });
 } finally { if(previousPackageDir===undefined)delete process.env.PI_PACKAGE_DIR;else process.env.PI_PACKAGE_DIR=previousPackageDir;h.cleanup(); }
}
const bearer = await createHarness({ withConfiguredAuth: false });
try {
 let resolved = 0;
 const model = bearer.getModel();
 bearer.session.modelRuntime.registerNativeProvider({ id: model.provider, name: "Faux bearer provider", auth: { apiKey: { name: "Faux bearer token", resolve: async () => { resolved++; return { auth: { headers: { Authorization: "Bearer ambient-token" } }, source: "ambient bearer token" }; } } }, getModels: () => bearer.models, stream: () => createAssistantMessageEventStream(), streamSimple: () => createAssistantMessageEventStream() });
 seed(bearer);
 bearer.setResponses([(_context, options) => {
  assert.equal(options?.apiKey, undefined);
  assert.deepEqual(options?.headers, { Authorization: "Bearer ambient-token" });
  return fauxAssistantMessage("summary with bearer auth");
 }]);
 const result = await bearer.session.compact();
 assert(result.summary.includes("summary with bearer auth"));
 assert.equal(bearer.faux.state.callCount, 1);
 console.log(`COMPACTION_BOUNDARY bearer requests=${bearer.faux.state.callCount} resolved=${resolved}`);
} finally { bearer.cleanup(); }
const failing = await createHarness();
try {
 seed(failing);
 failing.session.agent.streamFunction = () => { throw new Error("summary generator blew up"); };
 assert.equal(await failing.session._runAutoCompaction("threshold", false), false);
 const message = failing.eventsOfType("compaction_end").at(-1).errorMessage;
 assert.equal(message, "Auto-compaction failed: summary generator blew up");
 console.log(`COMPACTION_BOUNDARY exception ${JSON.stringify(message)}`);
} finally { failing.cleanup(); }
const custom = await createHarness({ withConfiguredAuth:false });
try {
 seed(custom);
 let requests=0;
 custom.session.agent.streamFunction=(model)=>{
  requests++;
  const stream=createAssistantMessageEventStream();
  queueMicrotask(()=>stream.push({type:"done",reason:"stop",message:{...fauxAssistantMessage("custom provider summary"),api:model.api,provider:model.provider,model:model.id}}));
  return stream;
 };
 const result=await custom.session.compact();
 assert(result.summary.includes("custom provider summary"));
 assert.equal(requests,1);
 console.log(`COMPACTION_BOUNDARY custom requests=${requests}`);
} finally { custom.cleanup(); }
