import assert from "node:assert/strict";
import {readFileSync, mkdtempSync, mkdirSync, rmSync} from "node:fs";
import {tmpdir} from "node:os";
import {join, resolve} from "node:path";
import {pathToFileURL} from "node:url";
const root=process.env.PI_PACKAGE_ROOT??resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
assert.equal(JSON.parse(readFileSync(join(root,"package.json"),"utf8")).version,"0.87.1");
const load=path=>import(pathToFileURL(join(root,"dist",path)));
const {createAgentSession}=await load("core/sdk.js");
const {DefaultResourceLoader}=await load("core/resource-loader.js");
const {SettingsManager}=await load("core/settings-manager.js");
const {SessionManager}=await load("core/session-manager.js");
const {createBashTool}=await load("core/tools/bash.js");
const {getModel}=await import(import.meta.resolve("@earendil-works/pi-ai/compat",pathToFileURL(join(root,"package.json")).href));
const model=getModel("anthropic","claude-sonnet-4-5");
const tool=(name,label,description,promptSnippet,promptGuidelines)=>({name,label,description,promptSnippet,promptGuidelines,parameters:{type:"object",properties:{}},execute:async()=>({content:[{type:"text",text:"ok"}],details:{}})});
const dynamic=guidelines=>tool("dynamic_tool","Dynamic Tool","Tool registered from session_start","Run dynamic test behavior",guidelines);
async function create(defaultTools,options={},factory){
 const cwd=mkdtempSync(join(tmpdir(),"registry-port-")),agentDir=join(cwd,"agent");mkdirSync(agentDir);
 const settingsManager=SettingsManager.inMemory(defaultTools===undefined?{}:{defaultTools});
 const resourceLoader=new DefaultResourceLoader({cwd,agentDir,settingsManager,noExtensions:true,noSkills:true,noPromptTemplates:true,noThemes:true,extensionFactories:factory?[factory]:[]});await resourceLoader.reload();
 const {session}=await createAgentSession({cwd,agentDir,model,settingsManager,sessionManager:SessionManager.create(cwd,join(agentDir,"sessions"),{id:"bash-env-test"}),resourceLoader,...options});
 return {session,cleanup:()=>{session.dispose();rmSync(cwd,{recursive:true,force:true});}};
}
function print(name,session){
 const all=session.getAllTools().map(tool=>tool.name).sort();
 const lines=session.systemPrompt.split("\n").filter(line=>/^\- (dynamic_tool|grep|powershell|read|bash|edit|write|find|ls):/.test(line));
 console.log("REGISTRY "+name+" "+JSON.stringify([all,session.getActiveToolNames(),lines]));
}
{
 const definition=tool("custom","Custom","Custom operation","\ufeffRun\n \u00a0custom  action\ufeff",["\ufeffkeep\ufeff","keep"," \u0085 "]);
 const h=await create(undefined,{noTools:"builtin",customTools:[definition]});
 try{
  const lines=h.session.systemPrompt.split("\n").filter(line=>line.startsWith("- custom:")||line==="- keep"||line==="- \u0085");
  assert.deepEqual(lines,["- custom: Run custom action","- keep","- \u0085"]);
  assert.deepEqual(h.session.getAllTools().find(info=>info.name==="custom").promptGuidelines,definition.promptGuidelines);
  console.log("REGISTRY whitespace "+JSON.stringify(lines));
 }finally{h.cleanup();}
}
for(const defaults of [["grep","find"],["read","powershell","edit","write"]]){
 const h=await create(defaults);try{assert.deepEqual(h.session.getActiveToolNames(),defaults);assert.deepEqual(h.session.getAllTools().map(tool=>tool.name).sort(),["bash","edit","find","grep","ls","powershell","read","write"]);print(defaults[0],h.session);}finally{h.cleanup();}
}
for(const allowed of [["read","dynamic_tool"],[]]){
 const h=await create(undefined,{tools:allowed},pi=>pi.on("session_start",()=>pi.registerTool(dynamic())));
 try{await h.session.bindExtensions({});assert.deepEqual(h.session.getActiveToolNames(),allowed);print("allow",h.session);}finally{h.cleanup();}
}
{
 const h=await create(undefined,{noTools:"builtin"},pi=>pi.on("session_start",()=>pi.registerTool(dynamic())));
 try{await h.session.bindExtensions({});assert.deepEqual(h.session.getActiveToolNames(),["dynamic_tool"]);print("no-builtin",h.session);}finally{h.cleanup();}
}
for(const allowed of [undefined,["read","bash","ask_question"]]){
 const h=await create(undefined,{tools:allowed,excludeTools:["read","ask_question"]},pi=>pi.on("session_start",()=>{pi.registerTool(tool("ask_question","Ask Question","Ask a question","Ask a question"));pi.registerTool(dynamic());}));
 try{await h.session.bindExtensions({});const all=h.session.getAllTools().map(tool=>tool.name);assert(!all.includes("read")&&!all.includes("ask_question"));print("exclude",h.session);}finally{h.cleanup();}
}
{
 let exposed,hidden;
 const h=await create(undefined,{thinkingLevel:"high"},pi=>{
  pi.registerTool(createBashTool(process.cwd(),{spawnHook:ctx=>{exposed=ctx.env;return ctx;}}));
  pi.registerTool({...createBashTool(process.cwd(),{exposeSessionEnvironment:false,spawnHook:ctx=>{hidden=ctx.env;return ctx;}}),name:"bash_without_session_env",label:"bash without session env"});
 });
 try{
  assert(h.session.systemPrompt.includes("You can inspect PI_* environment variables for current model and session details."));
  for(const name of ["bash","bash_without_session_env"]){const result=await h.session.agent.state.tools.find(tool=>tool.name===name).execute("bash-env",{command:"printf ok"});assert.equal(result.content[0].text,"ok");}
  for(const [key,value] of Object.entries({PI_SESSION_ID:h.session.sessionId,PI_SESSION_FILE:h.session.sessionFile,PI_PROVIDER:model.provider,PI_MODEL:model.id,PI_REASONING_LEVEL:h.session.thinkingLevel})){assert.equal(exposed[key],value);assert(!(key in hidden));}
  console.log("REGISTRY env validated");
 }finally{h.cleanup();}
}
{
 const guideline="Use dynamic_tool when the user asks for dynamic behavior tests.";
 const h=await create(undefined,{},pi=>pi.on("session_start",()=>pi.registerTool(dynamic([guideline]))));
 try{
  assert(!h.session.getAllTools().some(tool=>tool.name==="dynamic_tool"));await h.session.bindExtensions({});
  const info=h.session.getAllTools().find(tool=>tool.name==="dynamic_tool");assert.deepEqual(info.promptGuidelines,[guideline]);
  for(const [key,value] of Object.entries({path:"<inline:1>",source:"inline",scope:"temporary",origin:"top-level"}))assert.equal(info.sourceInfo[key],value);
  assert(h.session.systemPrompt.includes("- "+guideline));print("dynamic",h.session);
 }finally{h.cleanup();}
}
