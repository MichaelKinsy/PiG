import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
const root = new URL('../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/', import.meta.url);
const load = rel => import(new URL('dist/' + rel, root));
const ai = await import(new URL('node_modules/@earendil-works/pi-ai/dist/index.js', root));
const { getModel } = await import(new URL('node_modules/@earendil-works/pi-ai/dist/compat.js', root));
const { getPromptCacheTtlMs } = await load('core/cache-warmer.js');
const model = {...getModel('anthropic','claude-opus-4-6'), promptCache:{short:300,long:3600}};
console.log(JSON.stringify([getPromptCacheTtlMs(model), getPromptCacheTtlMs(model,{cacheRetention:'long'}), getPromptCacheTtlMs(model,{cacheRetention:'none'}) ?? null]));
const { createAgentSession } = await load('core/sdk.js');
const { ModelRuntime } = await load('core/model-runtime.js');
const { AuthStorage } = await load('core/auth-storage.js');
const { SessionManager } = await load('core/session-manager.js');
const { SettingsManager } = await load('core/settings-manager.js');
const { DefaultResourceLoader } = await load('core/resource-loader.js');
const dir = mkdtempSync(join(tmpdir(),'cache-decision-'));
let session;
try {
 const settingsManager = SettingsManager.inMemory({cacheWarming:'idle'});
 const runtime = await ModelRuntime.create({credentials:AuthStorage.inMemory(), modelsPath:null});
 const custom = {...model,id:'cache-test',provider:'cache-test',api:'cache-test-api',baseUrl:'https://cache.invalid',promptCache:{short:11},cost:{input:10,output:50,cacheRead:0.25,cacheWrite:12.5}};
 let calls = 0;
 runtime.registerProvider('cache-test',{api:custom.api,baseUrl:custom.baseUrl,apiKey:'key',models:[custom],streamSimple:()=>{
  calls++;
  const s = ai.createAssistantMessageEventStream();
  s.end({role:'assistant',content:[{type:'text',text:'hello'}],provider:custom.provider,api:custom.api,model:custom.id,stopReason:'stop',timestamp:Date.now(),usage:{input:0,output:1,cacheRead:100000,cacheWrite:0,totalTokens:100001,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}}});
  return s;
 }});
 const resourceLoader = new DefaultResourceLoader({cwd:dir,agentDir:dir,settingsManager,extensionFactories:[pi=>{pi.on('cache_warming_decision',()=>({action:'stop'}));}]});
 await resourceLoader.reload();
 ({session}=await createAgentSession({cwd:dir,agentDir:dir,model:runtime.getModel('cache-test','cache-test'),modelRuntime:runtime,settingsManager,sessionManager:SessionManager.inMemory(dir),resourceLoader}));
 await session.bindExtensions({});
 await session.prompt('hi');
 const deadline=Date.now()+5000;
 while(session.cacheWarmingStatus?.reason!=='stopped by extension'){
  if(Date.now()>deadline) throw new Error(JSON.stringify(session.cacheWarmingStatus));
  await new Promise(resolve=>setTimeout(resolve,10));
 }
 console.log(JSON.stringify([session.cacheWarmingStatus.state,session.cacheWarmingStatus.reason,session.cacheWarmingStatus.extensionOverride,calls]));
} finally {session?.dispose();rmSync(dir,{recursive:true,force:true});}
