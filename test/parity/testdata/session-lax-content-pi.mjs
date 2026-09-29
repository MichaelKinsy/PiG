// Pi 0.87.1 lax-message-content.test.ts:29,62,86,105,140,156. Production comes from the published package.
import assert from "node:assert/strict";
import { createHarness, production, resolvePackage } from "./session-harness-pi.mjs";
const { fauxAssistantMessage, fauxToolCall } = await import(resolvePackage("@earendil-works/pi-ai"));
const { Type } = await import(resolvePackage("typebox"));
const { sessionEntryToContextMessages } = await production("core/session-manager.js");
const result = [];
const roleContent = m => [m.role, m.content];
let harness = await createHarness({ extensionFactories: [pi => pi.registerTool({
  name: "web_search", label: "Web Search", description: "Custom tool that returns a result without content",
  parameters: Type.Object({}), execute: async () => ({details: {}}),
})] });
try {
  harness.setResponses([fauxAssistantMessage(fauxToolCall("web_search", {}), { stopReason: "toolUse" }), fauxAssistantMessage("done")]);
  await harness.session.prompt("search something");
  const messages = harness.session.messages.filter(m => m.role === "toolResult");
  assert.equal(messages.length, 1); assert.deepEqual(messages[0].content, []);
  assert.equal(harness.getPendingResponseCount(), 0);
  result.push([messages.map(roleContent), harness.getPendingResponseCount()]);
} finally { harness.cleanup(); }
harness = await createHarness({ extensionFactories: [pi => pi.on("message_end", async event => {
  if (event.message.role !== "assistant") return undefined;
  return {message: {...event.message, content: null}};
})] });
try {
  harness.setResponses([fauxAssistantMessage("hello")]);
  await harness.session.prompt("hi");
  const messages = harness.session.messages.filter(m => m.role === "assistant");
  assert.equal(messages.length, 1); assert.deepEqual(messages[0].content, []);
  result.push(messages.map(roleContent));
} finally { harness.cleanup(); }
harness = await createHarness();
try {
  await harness.session.sendCustomMessage({customType: "test", content: null, display: false, details: undefined});
  const messages = harness.session.messages.filter(m => m.role === "custom");
  assert.equal(messages.length, 1); assert.deepEqual(messages[0].content, []);
  result.push(messages.map(roleContent));
} finally { harness.cleanup(); }
const messageEntry = message => ({type: "message", id: "entry-1", parentId: null, timestamp: new Date().toISOString(), message});
const badMessages = [
  {role: "user", content: null, timestamp: Date.now()},
  {role: "assistant", content: null, api: "openai-completions", provider: "openai", model: "test-model", usage: {input:0,output:0,cacheRead:0,cacheWrite:0,totalTokens:0,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}}, stopReason:"stop", timestamp:Date.now()},
  {role: "toolResult", toolCallId: "call_1", toolName: "web_search", isError: false, timestamp: Date.now()},
];
result.push(badMessages.map(bad => {
  const [message] = sessionEntryToContextMessages(messageEntry(bad));
  assert.deepEqual(roleContent(message), [bad.role, []]); return roleContent(message);
}));
const [custom] = sessionEntryToContextMessages({type:"custom_message",id:"entry-1",parentId:null,timestamp:new Date().toISOString(),customType:"test",content:null,display:false,details:undefined});
assert.deepEqual(roleContent(custom), ["custom", []]); result.push(roleContent(custom));
const [valid] = sessionEntryToContextMessages(messageEntry({role:"user",content:"hello",timestamp:Date.now()}));
assert.deepEqual(roleContent(valid), ["user", "hello"]); result.push(roleContent(valid));
const { convertToLlm } = await production("core/messages.js");
const converted = convertToLlm([valid]);
assert.deepEqual(converted.map(roleContent), [["user", "hello"]]);
if (process.argv.includes("--ported")) {
  console.log("SESSION_LAX_EXTENSIONS " + JSON.stringify(result.slice(0, 3)));
  console.log("SESSION_LAX_ENTRIES " + JSON.stringify(result.slice(3)));
  console.log("SESSION_USER_STRING_CONTEXT " + JSON.stringify(roleContent(converted[0])));
} else {
  console.log("SESSION_LAX " + JSON.stringify(result));
}
