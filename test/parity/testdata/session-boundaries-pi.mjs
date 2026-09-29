// Execute every original boundary body against published Pi 0.87.1; only test helpers are transpiled.
import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import {resolve} from "node:path";
import ts from "../interface-extractor/node_modules/typescript/lib/typescript.js";
import {createHarness as originalHarness, resolvePackage, getMessageText} from "./session-harness-pi.mjs";
const {fauxAssistantMessage,fauxToolCall}=await import(resolvePackage("@earendil-works/pi-ai"));
const {Type}=await import(resolvePackage("typebox"));
const source=readFileSync(resolve(".upstream/current/packages/coding-agent/test/suite/agent-session-boundaries.test.ts"),"utf8");
const ast=ts.createSourceFile("boundaries.ts",source,ts.ScriptTarget.Latest,true), cases=[];
function visit(node){
 if(ts.isCallExpression(node)){
  const expression=node.expression.getText(ast);
  const site=ast.getLineAndCharacterOfPosition(node.getStart(ast)).line+1;
  if(expression==="it")cases.push({site,name:node.arguments[0].text,body:node.arguments[1].getText(ast),args:[]});
  else if(ts.isCallExpression(node.expression)&&node.expression.expression.getText(ast)==="it.each"){
   const values=node.expression.arguments[0].expression.elements.map(e=>e.text);
   for(const value of values)cases.push({site,name:node.arguments[0].text.replace("%s",value),body:node.arguments[1].getText(ast),args:[value]});
  }
 }
 ts.forEachChild(node,visit);
}
visit(ast);
function deferred(){let resolve;const promise=new Promise(done=>{resolve=done;});return {promise,resolve};}
const matchSymbol=Symbol("matcher");
function matches(value,expected){
 if(expected?.[matchSymbol])return expected[matchSymbol](value);
 if(Array.isArray(expected))return Array.isArray(value)&&value.length===expected.length&&expected.every((v,i)=>matches(value[i],v));
 if(expected&&typeof expected==="object")return value&&typeof value==="object"&&Object.keys(value).length===Object.keys(expected).length&&Object.entries(expected).every(([k,v])=>matches(value[k],v));
 return Object.is(value,expected);
}
function expect(value,negate=false){
 const check=(result,message)=>assert.equal(Boolean(result),!negate,message);
 return {
  get not(){return expect(value,!negate);},
  toBe:expected=>check(Object.is(value,expected),`expected ${JSON.stringify(value)} ${negate?"not ":""}to be ${JSON.stringify(expected)}`),
  toEqual:expected=>check(matches(value,expected),`expected ${JSON.stringify(value)} to equal ${JSON.stringify(expected)}`),
  toHaveLength:expected=>check(value.length===expected,`length ${value.length}, expected ${expected}`),
  toContain:expected=>check(value?.includes(expected),`expected ${JSON.stringify(value)} ${negate?"not ":""}to contain ${JSON.stringify(expected)}`),
  toContainEqual:expected=>check(value.some(item=>matches(item,expected)),`expected matching entry in ${JSON.stringify(value)}`),
  toMatchObject:expected=>check(value&&Object.entries(expected).every(([k,v])=>matches(value[k],v)),`expected object ${JSON.stringify(expected)} in ${JSON.stringify(value)}`),
  toBeGreaterThan:expected=>check(value>expected,`${value} > ${expected}`),
  toBeLessThan:expected=>check(value<expected,`${value} < ${expected}`),
  toBeNull:()=>check(value===null,`expected null, got ${value}`),
  toBeDefined:()=>check(value!==undefined,"expected defined"),
  toHaveBeenCalled:()=>check(value.calls.length>0,`calls=${value.calls.length}`),
 };
}
expect.objectContaining=expected=>({[matchSymbol]:value=>value&&Object.entries(expected).every(([k,v])=>matches(value[k],v))});
expect.arrayContaining=expected=>({[matchSymbol]:value=>expected.every(item=>value.some(v=>matches(v,item)))});
const vi={spyOn(object,key){const original=object[key];function spy(...args){spy.calls.push(args);return original.apply(this,args);}spy.calls=[];object[key]=spy;return spy;}};
for(const test of cases){
 if(process.argv[2]&&String(test.site)!==process.argv[2])continue;
 const harnesses=[], diagnostics=[], requests=[];
 const createHarness=async(...args)=>{
  const h=await originalHarness(...args);
  if(test.site===439){
   h.session._extensionRunner.onError(error=>diagnostics.push(error.error));
   const setResponses=h.setResponses;
   h.setResponses=responses=>setResponses(responses.map(step=>(...args)=>{
    requests.push(args[0].messages.map(message=>message.role));
    return typeof step==="function"?step(...args):step;
   }));
  }
  return h;
 };
 const body=ts.transpileModule(`(${test.body})`,{compilerOptions:{target:ts.ScriptTarget.ESNext,module:ts.ModuleKind.ESNext}}).outputText;
 const run=new Function("createHarness","harnesses","fauxAssistantMessage","fauxToolCall","Type","expect","getMessageText","deferred","vi",`return ${body}`);
 try{
  await run(createHarness,harnesses,fauxAssistantMessage,fauxToolCall,Type,expect,getMessageText,deferred,vi)(...test.args);
  assert.equal(harnesses.length,1);
  if(test.site===439){
   const h=harnesses[0], entries=h.sessionManager.getEntries();
   const messages=entries.filter(entry=>entry.type==="message");
   const user=messages.find(entry=>entry.message.role==="user"), assistants=messages.filter(entry=>entry.message.role==="assistant"), tool=messages.find(entry=>entry.message.role==="toolResult");
   const edits=entries.filter(entry=>entry.type==="context_edit");
   assert.deepEqual(edits.map(entry=>entry.targetId),[user.id,assistants[0].id,tool.id,user.id,assistants[1].id]);
   assert(edits.every(entry=>entry.replacement===null));
   assert(h.sessionManager.buildSessionProjection().messages.every(message=>message.role==="system"));
   assert.deepEqual(requests[1],["system"]);
   const invalid="turn_end requested continuation without runnable model context";
   assert.deepEqual(diagnostics,[invalid,invalid]);
  }
  console.log("SESSION_BOUNDARY_CASE "+JSON.stringify([test.site,test.name,harnesses[0].faux.state.callCount]));
 }catch(error){throw new Error(`original boundary case ${test.site}: ${test.name}`,{cause:error});}
 finally{for(const h of harnesses)h.cleanup();}
}
if(!process.argv[2]){
 const {default:factory,started}=await import("./session-boundary-abort-extension.mjs");
 const h=await originalHarness({extensionFactories:[factory]}), diagnostics=[];
 h.session._extensionRunner.onError(error=>diagnostics.push(error.error));
 h.setResponses([fauxAssistantMessage("first"),fauxAssistantMessage("must not run")]);
 try{
  const prompt=h.session.prompt("start");
  await started;
  const abort=h.session.abort();
  await h.session.prompt("/release-boundary");
  await Promise.all([prompt,abort]);
  const entry=h.sessionManager.getEntries().find(entry=>entry.type==="custom_message"&&entry.customType==="committed-after-abort");
  assert.equal(h.faux.state.callCount,1);assert.equal(entry.content,"runnable draft after abort");assert.equal(entry.display,false);assert.deepEqual(diagnostics,[]);
  console.log("SESSION_BOUNDARY_BRIDGE "+JSON.stringify([h.faux.state.callCount,diagnostics,entry.content,entry.display]));
 }finally{h.cleanup();}
}
if(!process.argv[2]){
 const {default:factory}=await import("./session-boundary-extension.mjs");
 const h=await originalHarness({extensionFactories:[factory]}),diagnostics=[],requests=[];
 h.session._extensionRunner.onError(error=>diagnostics.push(error.error));
 h.setResponses([fauxAssistantMessage("first"),context=>{requests.push(JSON.stringify(context.messages));return fauxAssistantMessage("second");}]);
 try{
  await h.session.prompt("start");
  assert.equal(h.faux.state.callCount,2);assert.equal(requests.length,1);assert(requests[0].includes("context from real extension"));
  assert.deepEqual(diagnostics,["retained boundary error"]);
  const entry=h.sessionManager.getEntries().find(entry=>entry.type==="custom_message"&&entry.customType==="bridge-boundary");
  assert.equal(entry.content,"context from real extension");assert.equal(entry.display,false);assert.deepEqual(entry.details,{source:"boundary-probe"});
  console.log("SESSION_BOUNDARY_BRIDGE "+JSON.stringify([h.faux.state.callCount,diagnostics,entry.content,entry.display,entry.details]));
 }finally{h.cleanup();}
}
if(!process.argv[2]){
 let handled=false;
 const h=await originalHarness({settings:{compaction:{enabled:false},retry:{enabled:true,maxRetries:1,baseDelayMs:1}},extensionFactories:[pi=>pi.on("turn_end",event=>{
  if(handled)return;handled=true;
  return {entries:[{type:"context_edit",targetId:event.messageEntryId,replacement:null}]};
 })]});
 try{
  h.sessionManager.appendMessage({role:"user",content:"historical input",timestamp:Date.now()-5});
  h.sessionManager.appendMessage(fauxAssistantMessage("",{stopReason:"error",errorMessage:"overloaded_error",timestamp:Date.now()-4}));
  h.session.refreshContext();
  h.setResponses([fauxAssistantMessage("current done"),fauxAssistantMessage("must not retry history")]);
  await h.session.prompt("current input");
  assert.equal(h.faux.state.callCount,1);assert.deepEqual(h.eventsOfType("auto_retry_start"),[]);
  const retained=JSON.stringify(h.sessionManager.buildSessionProjection().messages).includes("overloaded_error");assert(retained);
  console.log("SESSION_BOUNDARY_BRIDGE "+JSON.stringify([h.faux.state.callCount,retained]));
 }finally{h.cleanup();}
}
