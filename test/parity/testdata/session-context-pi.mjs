import assert from "node:assert/strict";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
const { SessionManager, buildSessionContext } = await import(pathToFileURL(resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/core/session-manager.js")));
const session = SessionManager.inMemory("/project");
session.appendModelChange("openai", "gpt-5");
const selected = session.appendMessage({ role: "assistant", content: [{ type: "text", text: "selected answer" }], api: "openai-completions", provider: "openai", model: "gpt-4o", usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } }, stopReason: "stop", timestamp: 1 });
session.appendModelChange("openai", "gpt-5");
session.appendThinkingLevelChange("high");
session.branch(selected);
const context = session.buildSessionContext();
assert.deepEqual(context.model, { provider: "openai", modelId: "gpt-4o" });
assert.equal(context.thinkingLevel, "off");
console.log(`SESSION_CONTEXT model=${context.model.modelId} thinking=${context.thinkingLevel}`);
const metadata=buildSessionContext([
 {type:"model_change",id:"model",parentId:null,timestamp:"2025-01-01T00:00:00Z",provider:"openai",modelId:"chosen",message:"not a message"},
 {type:"thinking_level_change",id:"thinking",parentId:"model",timestamp:"2025-01-01T00:00:01Z",thinkingLevel:"high",provider:{ignored:true}},
]);
assert.deepEqual(metadata.model,{provider:"openai",modelId:"chosen"});
assert.equal(metadata.thinkingLevel,"high");
console.log(`SESSION_METADATA model=${metadata.model.provider}/${metadata.model.modelId} thinking=${metadata.thinkingLevel}`);
