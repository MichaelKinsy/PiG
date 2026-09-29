import assert from "node:assert/strict";
import { createHarness, resolvePackage } from "./session-harness-pi.mjs";
const { fauxAssistantMessage } = await import(resolvePackage("@earendil-works/pi-ai"));
for (const count of [1, 128]) {
  const harness = await createHarness({extensionFactories: [pi => {
    pi.on("agent_settled", () => {
      for (let i = 0; i < count; i++) pi.sendMessage({customType:"after", content:"after turn", display:true});
    });
  }]});
  harness.setResponses([fauxAssistantMessage("done")]);
  const listeners = [], output = [];
  const key = event => event.type === "agent_settled" ? "settled" : event.message?.role === "custom" && event.type === "message_start" ? "custom:start" : event.message?.role === "custom" && event.type === "message_end" ? "custom:end" : undefined;
  harness.session.subscribe(event => { const value=key(event); if(value) listeners.push(value); });
  harness.session.subscribe(event => { const value=key(event); if(value) output.push(value); });
  try {
    await harness.session.prompt("hello");
    const expected = [];
    for (let i=0; i<count; i++) expected.push("custom:start", "custom:end");
    expected.push("settled");
    assert.deepEqual(listeners, expected);
    assert.deepEqual(output, expected);
    console.log("CUSTOM_SETTLEMENT "+JSON.stringify([count,listeners,output]));
  } finally { harness.cleanup(); }
}
