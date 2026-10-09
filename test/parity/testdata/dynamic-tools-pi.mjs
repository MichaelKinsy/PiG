import assert from "node:assert/strict";
import {readFileSync, mkdtempSync, mkdirSync, rmSync} from "node:fs";
import {tmpdir} from "node:os";
import {join, resolve} from "node:path";
import {pathToFileURL} from "node:url";
const root=process.env.PI_PACKAGE_ROOT??resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
assert.equal(JSON.parse(readFileSync(join(root,"package.json"),"utf8")).version,"1.1.0");
const load=path=>import(pathToFileURL(join(root,"dist",path)));
const {createAgentSession}=await load("core/sdk.js");
const {DefaultResourceLoader}=await load("core/resource-loader.js");
const {SettingsManager}=await load("core/settings-manager.js");
const {SessionManager}=await load("core/session-manager.js");
const cwd=mkdtempSync(join(tmpdir(),"dynamic-tools-pi-")),agentDir=join(cwd,"agent");mkdirSync(agentDir);
const settingsManager=SettingsManager.inMemory();
const resourceLoader=new DefaultResourceLoader({cwd,agentDir,settingsManager,noExtensions:true,noSkills:true,noPromptTemplates:true,noThemes:true,additionalExtensionPaths:[resolve(".upstream/current/packages/coding-agent/examples/extensions/dynamic-tools.ts")]});
await resourceLoader.reload();
assert.deepEqual(resourceLoader.getExtensions().errors,[]);
const {session}=await createAgentSession({cwd,agentDir,settingsManager,sessionManager:SessionManager.inMemory(cwd),resourceLoader,noTools:"builtin"});
try {
 assert.deepEqual(session.getActiveToolNames(),[]);
 await session.bindExtensions({});
 for(const name of ["echo_session","shout"]){
  if(name==="shout")await session.prompt("/add-echo-tool shout");
  const tool=session.agent.state.tools.find(t=>t.name===name);assert(tool,"missing dynamic tool "+name);
  const result=await tool.execute("dynamic",{message:"hello"});
  console.log(JSON.stringify({active:session.getActiveToolNames(),name,text:result.content[0].text}));
 }
} finally {session.dispose();rmSync(cwd,{recursive:true,force:true});}
