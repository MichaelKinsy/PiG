import assert from "node:assert/strict";
import {createHarness, resolvePiPackage} from "./pi-session-harness.mjs";
const {fauxAssistantMessage, fauxToolCall, getCurrentTools, getCurrentSystemPrompt} = await import(resolvePiPackage("@earendil-works/pi-ai"));
const {Type} = await import(resolvePiPackage("typebox"));
for(const override of [false,false,true]) {
  const harness = await createHarness({extensionFactories:[pi=>{
    if(override) pi.on("before_agent_start",async event=>({systemPrompt:event.systemPrompt+"\n\nkeep this run override"}));
    pi.registerTool({name:"switch_tools",label:"Switch Tools",description:"Switch the active extension tool set",promptSnippet:"Switch to the next extension tool",parameters:Type.Object({}),execute:async()=>{pi.setActiveTools(["after_switch"]);return {content:[{type:"text",text:"switched"}],details:{}};}});
    pi.registerTool({name:"after_switch",label:"After Switch",description:"Tool that should be available after switching",promptSnippet:"Run after the active tool set changes",parameters:Type.Object({}),execute:async()=>({content:[{type:"text",text:"after"}],details:{}})});
  }]});
  try {
    harness.session.setActiveToolsByName(["switch_tools"]);
    const names=[],prompts=[],sessionPrompts=[];
    const capture=context=>{names.push(getCurrentTools(context.messages).map(tool=>tool.name).sort());prompts.push(getCurrentSystemPrompt(context.messages));sessionPrompts.push(harness.session.systemPrompt);};
    harness.setResponses([
      context=>{capture(context);return fauxAssistantMessage(fauxToolCall("switch_tools",{}),{stopReason:"toolUse"});},
      context=>{capture(context);return fauxAssistantMessage("done");},
    ]);
    assert.deepEqual(harness.session.getActiveToolNames(),["switch_tools"]);
    await harness.session.prompt("start");
    assert.deepEqual(harness.session.getActiveToolNames(),["after_switch"]);
    assert.deepEqual(names,[["switch_tools"],["after_switch"]]);
    assert.equal(prompts.length,2);
    assert.deepEqual(prompts,sessionPrompts);
    if(override) assert(prompts.every(prompt=>prompt.includes("keep this run override")));
    else assert.notEqual(prompts[0],prompts[1]);
    const lines=prompts.map(prompt=>prompt.split("\n").filter(line=>line.startsWith("- switch_tools:")||line.startsWith("- after_switch:")));
    console.log("NEXT_TOOLS "+JSON.stringify([names,lines,true,override]));
  } finally {harness.cleanup();}
}
