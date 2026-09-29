import {mkdtempSync,rmSync,writeFileSync,existsSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {pathToFileURL} from 'node:url';
const root=process.env.PI_PACKAGE_ROOT;
const load=path=>import(pathToFileURL(join(root,path)));
const {AuthStorage}=await load('dist/core/auth-storage.js');
const {ModelRuntime}=await load('dist/core/model-runtime.js');
const {SettingsManager}=await load('dist/core/settings-manager.js');
const {SessionManager}=await load('dist/core/session-manager.js');
const {DefaultResourceLoader}=await load('dist/core/resource-loader.js');
const {createAgentSession}=await load('dist/core/sdk.js');
const {AssistantMessageEventStream}=await load('node_modules/@earendil-works/pi-ai/dist/utils/event-stream.js');
const dir=mkdtempSync(join(tmpdir(),'metadata-refresh-'));
try {
 const counter=join(dir,'called'),script=join(dir,'key.cjs');
 writeFileSync(script,`require("node:fs").appendFileSync(${JSON.stringify(counter)},"called\\n"); console.log("configured-key");`);
 const runtime=await ModelRuntime.create({credentials:AuthStorage.inMemory(),modelsPath:null,allowModelNetwork:false});
 runtime.registerProvider('metadata-refresh',{api:'openai-completions',baseUrl:'https://before.test',apiKey:'!node '+JSON.stringify(script),models:[{id:'model',name:'Model'}]});
 const settings=SettingsManager.inMemory({});
 const loader=new DefaultResourceLoader({cwd:dir,agentDir:dir,settingsManager:settings,extensionFactories:[pi=>pi.registerCommand('refresh-metadata',{description:'Refresh metadata',handler:async()=>pi.registerProvider('metadata-refresh',{baseUrl:'https://after.test'})})]});await loader.reload();
 const {session}=await createAgentSession({cwd:dir,agentDir:dir,model:runtime.getModel('metadata-refresh','model'),modelRuntime:runtime,settingsManager:settings,sessionManager:SessionManager.inMemory(),resourceLoader:loader});
 await session.bindExtensions({});
 await session.prompt('/refresh-metadata');
 const metadata=[session.model.baseUrl,existsSync(counter)];session.dispose();
 let calls=0;const dispatch=[];
 const model={id:'model',name:'Model',provider:'native-refresh',api:'openai-completions',baseUrl:'https://native.test',reasoning:false,input:['text'],contextWindow:128000,maxTokens:16384,cost:{input:0,output:0,cacheRead:0,cacheWrite:0}};
 const callback=method=>(_model,_context,options)=>{dispatch.push(method+':'+options.apiKey);const stream=new AssistantMessageEventStream();stream.end({role:'assistant',api:model.api,provider:model.provider,model:model.id,content:[{type:'text',text:'end-only'}],stopReason:'stop',timestamp:0,usage:{input:0,output:0,cacheRead:0,cacheWrite:0,totalTokens:0,cost:{input:0,output:0,cacheRead:0,cacheWrite:0}}});return stream};
 runtime.registerNativeProvider({id:model.provider,name:'Native',getModels:()=>[model],auth:{apiKey:{name:'Native',resolve:async()=>{calls++;return {auth:{apiKey:'native-key'},source:'native'}}}},stream:callback('stream'),streamSimple:callback('streamSimple')});
 const selected=runtime.getModel(model.provider,model.id),before=calls,results=[];
 for(const method of ['stream','streamSimple']){const result=await runtime[method](selected,{messages:[]},{}).result();results.push(result.stopReason+':'+result.content[0].text)}
 console.log(JSON.stringify([metadata,before,calls,dispatch,results]));
}finally{rmSync(dir,{recursive:true,force:true})}
