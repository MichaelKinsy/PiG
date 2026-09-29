import {mkdtempSync,rmSync} from 'node:fs';import {tmpdir} from 'node:os';import {join} from 'node:path';
const core='../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/core/';
const {AuthStorage}=await import(core+'auth-storage.js');const {ModelRuntime}=await import(core+'model-runtime.js');const {SettingsManager}=await import(core+'settings-manager.js');const {SessionManager}=await import(core+'session-manager.js');const {DefaultResourceLoader}=await import(core+'resource-loader.js');const {createAgentSession}=await import(core+'sdk.js');
const {getModel}=await import('../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/compat.js');
const root=mkdtempSync(join(tmpdir(),'dynamic-provider-'));const rows=[];
try{for(const phase of ['top-level','session-start','command','native-top-level','native-command']){
 const dir=mkdtempSync(join(root,'case-'));const auth=AuthStorage.create(join(dir,'auth.json'));await auth.modify('anthropic',async()=>({type:'api_key',key:'test-key'}));
 const runtime=await ModelRuntime.create({credentials:auth,modelsPath:join(dir,'models.json')});const settings=SettingsManager.inMemory({});const model=getModel('anthropic','claude-sonnet-4-5');const baseUrl='http://localhost:8080/'+phase;
 const loader=new DefaultResourceLoader({cwd:dir,agentDir:dir,settingsManager:settings,extensionFactories:[pi=>{
  const register=()=>phase.startsWith('native-')?pi.registerProvider({id:'anthropic',name:'Native Anthropic',baseUrl,auth:{apiKey:{name:'Test API key',resolve:async()=>({auth:{apiKey:'test-key'},source:'test'})}},getModels:()=>[{...model,baseUrl}],stream:()=>{throw Error('unused')},streamSimple:()=>{throw Error('unused')}}):pi.registerProvider('anthropic',{baseUrl});
  if(phase.endsWith('top-level'))register();if(phase==='session-start')pi.on('session_start',register);pi.registerCommand('use-proxy',{description:'Use proxy',handler:async()=>register()});
 }]});await loader.reload();const {session}=await createAgentSession({cwd:dir,agentDir:dir,model,modelRuntime:runtime,settingsManager:settings,sessionManager:SessionManager.inMemory(),resourceLoader:loader});
 try{await session.bindExtensions({});if(phase.endsWith('command'))await session.prompt('/use-proxy');const active=session.model.baseUrl;let sent;session.agent.streamFunction=async m=>{sent=m.baseUrl;throw Error('stop')};await session.prompt('hello');rows.push([phase,active,sent]);}finally{session.dispose()}
}}finally{rmSync(root,{recursive:true,force:true})}console.log(JSON.stringify(rows));
