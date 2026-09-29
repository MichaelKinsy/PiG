import {mkdtempSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
const core = '../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/core/';
const {AuthStorage}=await import(core+'auth-storage.js');
const {ModelRuntime}=await import(core+'model-runtime.js');
const {SettingsManager}=await import(core+'settings-manager.js');
const {SessionManager}=await import(core+'session-manager.js');
const {DefaultResourceLoader}=await import(core+'resource-loader.js');
const {createAgentSession}=await import(core+'sdk.js');
import {createAssistantMessageEventStream} from '../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/utils/event-stream.js';
const directory=mkdtempSync(join(tmpdir(),'sdk-options-'));
let session;
try {
 const model={id:'capture-model',name:'Capture Model',api:'openai-completions',provider:'capture-provider',baseUrl:'https://capture.invalid/v1',reasoning:false,input:['text'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:128000,maxTokens:4096,headers:{'x-model':'model'}};
 const auth=AuthStorage.create(join(directory,'auth.json'));
 await auth.modify(model.provider,async()=>({type:'api_key',key:'test-api-key'}));
 const runtime=await ModelRuntime.create({credentials:auth,modelsPath:join(directory,'models.json')});
 let captured;
 runtime.registerProvider(model.provider,{api:model.api,baseUrl:model.baseUrl,headers:{'x-provider':'provider'},models:[model],streamSimple:(_model,_context,options)=>{
  captured=options;const stream=createAssistantMessageEventStream();stream.end({role:'assistant',api:model.api,provider:model.provider,model:model.id,content:[{type:'text',text:'ok'}],usage:{input:0,output:0,cacheRead:0,cacheWrite:0,totalTokens:0,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}},stopReason:'stop',timestamp:Date.now()});return stream;
 }});
 const settings=SettingsManager.inMemory({httpIdleTimeoutMs:1234,websocketConnectTimeoutMs:4321,retry:{provider:{maxRetries:2,maxRetryDelayMs:3000}}});
 const resourceLoader=new DefaultResourceLoader({cwd:directory,agentDir:directory,settingsManager:settings,extensionFactories:[pi=>pi.on('before_provider_headers',event=>{event.headers['x-hook']=event.headers['x-provider']+':'+event.headers['x-model'];})]});
 await resourceLoader.reload();
 ({session}=await createAgentSession({cwd:directory,agentDir:directory,model,modelRuntime:runtime,settingsManager:settings,sessionManager:SessionManager.inMemory(directory),resourceLoader}));
 await session.bindExtensions({});
 await session.prompt('test');
 console.log(JSON.stringify([captured.timeoutMs,captured.websocketConnectTimeoutMs,captured.maxRetries,captured.maxRetryDelayMs,captured.headers['x-provider'],captured.headers['x-model'],captured.headers['x-hook'],!('transformHeaders' in captured)]));
} finally {session?.dispose();rmSync(directory,{recursive:true,force:true});}
