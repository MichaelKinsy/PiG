import assert from "node:assert/strict";
import {createHarness,resolvePiPackage} from "./pi-session-harness.mjs";
const {fauxAssistantMessage,fauxToolCall}=await import(resolvePiPackage("@earendil-works/pi-ai"));
let release;
const barrier=new Promise(resolve=>{release=resolve;});
const entered=[];
const h=await createHarness({tools:[{
 name:"cooperate",label:"Cooperate",description:"Wait for both calls",parameters:{type:"object",properties:{value:{type:"string"}},required:["value"]},
 execute:async(_id,args)=>{entered.push(args.value);if(entered.length===2)release();await barrier;return {content:[{type:"text",text:args.value}],details:{}};},
}]});
try{
 h.setResponses([fauxAssistantMessage([fauxToolCall("cooperate",{value:"first"}),fauxToolCall("cooperate",{value:"second"})],{stopReason:"toolUse"}),fauxAssistantMessage("done")]);
 await h.session.prompt("run both");
 assert.deepEqual([...entered].sort(),["first","second"]);
 const results=h.session.messages.filter(message=>message.role==="toolResult");
 assert(results.every(result=>!result.isError));
 const texts=results.map(result=>result.content.filter(block=>block.type==="text").map(block=>block.text).join("\n"));
 assert.deepEqual(texts,["first","second"]);
 console.log("PARALLEL_TOOLS "+JSON.stringify(texts));
}finally{h.cleanup();}
