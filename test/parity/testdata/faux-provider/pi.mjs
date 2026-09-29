import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
const root=process.env.PI_PACKAGE_ROOT??resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
const {registerFauxProvider,fauxAssistantMessage,fauxThinking,fauxText,fauxToolCall,stream,complete}=await import(pathToFileURL(join(root,'node_modules/@earendil-works/pi-ai/dist/compat.js')));
const provider=registerFauxProvider({tokenSize:{min:1,max:1}});
const canonicalModel=provider.getModel();
try{
 provider.setResponses([fauxAssistantMessage([fauxThinking('go'),fauxText('ok'),fauxToolCall('echo',{},{id:'tool-1'})],{stopReason:'toolUse'}),fauxAssistantMessage('ready')]);
 const context={messages:[{role:'user',content:'hi',timestamp:0}]};
 const firstStream=stream(provider.getModel(),context,{sessionId:'session-1',cacheRetention:'short'});const events=[];for await(const event of firstStream)events.push(event.type);const first=await firstStream.result();
 context.messages.push(first,{role:'toolResult',toolCallId:'tool-1',toolName:'echo',content:[{type:'text',text:'ok'}],isError:false,timestamp:0},{role:'user',content:'follow up',timestamp:0});
 const second=await complete(provider.getModel(),context,{sessionId:'session-1',cacheRetention:'short'});
 provider.setResponses([async()=>{throw new Error('boom')}]);const failedStream=stream(provider.getModel(),context);const errorEvents=[];for await(const event of failedStream)errorEvents.push(event.type);const failed=await failedStream.result();
 const paced=registerFauxProvider({tokensPerSecond:100,tokenSize:{min:3,max:3}});const abortEvents=[];
 try{paced.setResponses([fauxAssistantMessage('abcdefghijklmnopqrstuvwxyz')]);const controller=new AbortController();const aborting=stream(paced.getModel(),{messages:[{role:'user',content:'hi',timestamp:0}]},{signal:controller.signal});for await(const event of aborting){abortEvents.push(event.type);if(event.type==='text_delta')controller.abort();}}finally{paced.unregister()}
 const counters=u=>({cacheRead:u.cacheRead,cacheWrite:u.cacheWrite,input:u.input,output:u.output,totalTokens:u.totalTokens});
 console.log(JSON.stringify({abortEvents,calls:provider.state.callCount,canonicalIdentity:canonicalModel===provider.getModel(),canonicalProvider:canonicalModel.provider,error:failed.errorMessage,errorEvents,events,firstUsage:counters(first.usage),secondUsage:counters(second.usage)}));
}finally{provider.unregister()}
