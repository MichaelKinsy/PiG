import {join,resolve} from 'node:path';
import {pathToFileURL} from 'node:url';
const root=process.env.PI_PACKAGE_ROOT??resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
const ai=join(root,'node_modules/@earendil-works/pi-ai/dist');
const {createModels,createProvider}=await import(pathToFileURL(join(ai,'models.js')));
const {InMemoryModelsStore}=await import(pathToFileURL(join(ai,'models-store.js')));
const {lazyApi}=await import(pathToFileURL(join(ai,'api/lazy.js')));
const {fauxProvider,fauxAssistantMessage}=await import(pathToFileURL(join(ai,'providers/faux.js')));
const {AssistantMessageEventStream}=await import(pathToFileURL(join(ai,'utils/event-stream.js')));
const {normalizeContext}=await import(pathToFileURL(join(ai,'compat.js')));
const request={messages:[{role:'user',content:'hi',timestamp:0}]};
const model=(api,id,provider='mixed')=>({id,name:id,api,provider,baseUrl:'https://example.test/v1',reasoning:false,input:['text'],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:10000,maxTokens:1000});
const auth=()=>({apiKey:{name:'Test',resolve:async()=>({auth:{}})}});
const recorder=(label,calls)=>{const respond=model=>{calls?.push(`${label}:${model.id}`);const message=fauxAssistantMessage('ok'),stream=new AssistantMessageEventStream();stream.push({type:'start',partial:message});stream.push({type:'done',reason:'stop',message});stream.end(message);return stream;};return {stream:respond,streamSimple:respond};};
const output={},calls=[],models=createModels();
const mixed=createProvider({id:'mixed',auth:auth(),models:[model('api-a','model-a'),model('api-b','model-b')],api:{'api-a':recorder('a',calls),'api-b':recorder('b',calls)}});models.setProvider(mixed);
await models.completeSimple(model('api-a','model-a'),request);await models.completeSimple(model('api-b','model-b'),request);output.dispatch=calls;
output.missingAPI=(await mixed.streamSimple(model('api-ghost','model-x'),normalizeContext(request)).result()).errorMessage;
let loads=0;const streams=recorder('lazy');streams.fetchDeferred=model=>streams.streamSimple(model,normalizeContext(request));const lazy=lazyApi(async()=>{loads++;return streams;},{fetchDeferred:true});const before=loads;const loaded=await lazy.fetchDeferred(model('api-a','model-a'),{provider:'mixed',modelId:'model-a',api:'api-a',id:'response-1'}).result();output.lazy={before,loads,cancel:lazy.cancelDeferred!==undefined,reason:loaded.stopReason};
{
 let fetches=0,markStarted,release;const started=new Promise(resolve=>markStarted=resolve),blocked=new Promise(resolve=>release=resolve);
 const store=new InMemoryModelsStore(),collection=createModels({modelsStore:store});
 const provider=createProvider({id:'dynamic',auth:auth(),models:[],api:recorder('a'),fetchModels:async()=>{const current=++fetches;if(current===1){markStarted();await blocked;}return [model('api-a',`listed-${current}`)];}});collection.setProvider(provider);
 const first=collection.refresh({providers:['dynamic']});await started;const second=await collection.refresh({providers:['dynamic']});const old=await first;release();await new Promise(resolve=>setTimeout(resolve,0));
 output.refresh={firstAborted:old.aborted,secondAborted:second.aborted,errors:old.errors.size+second.errors.size,fetches,current:provider.getModels()[0].id,stored:(await store.read('dynamic')).models[0].id};
}
{
 const faux=fauxProvider({api:'faux',models:[{id:'test-model'}],deferred:{pendingFetches:1,pollAfterMs:25}}),collection=createModels();collection.setProvider(faux.provider);const model=faux.getModel();faux.setResponses([fauxAssistantMessage('ready')]);
 const stream=collection.streamSimple(model,request,{deferred:{window:'1h'}}),events=[];for await(const event of stream)events.push(event.type);const submission=await stream.result(),handle=submission.deferred;if(!handle)throw new Error('missing deferred handle');
 const pending=await collection.fetchDeferred(model,handle),ready=await collection.fetchDeferred(model,handle,{wait:0});
 output.deferred={events,submission:submission.stopReason,content:submission.content,provider:handle.provider,modelId:handle.modelId,pollAfterMs:handle.pollAfterMs,validId:!!handle.id,pending:pending.stopReason,sameHandle:JSON.stringify(pending.deferred)===JSON.stringify(handle),ready:ready.stopReason,readyContent:ready.content,positiveUsage:ready.usage.totalTokens>0,calls:faux.state.callCount,fetches:faux.state.deferredFetchCount};
 const failedFaux=fauxProvider({api:'faux',models:[{id:'test-model'}]});collection.setProvider(failedFaux.provider);const failedModel=failedFaux.getModel();failedFaux.setResponses([()=>Promise.reject(new Error('deferred failed')),fauxAssistantMessage('cancelled')]);
 const failedSubmission=await collection.completeSimple(failedModel,request,{deferred:true}),failed=await collection.fetchDeferred(failedModel,failedSubmission.deferred);
 const cancelledSubmission=await collection.completeSimple(failedModel,request,{deferred:true});await collection.cancelDeferred(failedModel,cancelledSubmission.deferred);const cancelled=await collection.fetchDeferred(failedModel,cancelledSubmission.deferred);
 output.failure={reason:failed.stopReason,message:failed.errorMessage,cancelReason:cancelled.stopReason,cancelMessage:cancelled.errorMessage===`Faux deferred response was cancelled: ${cancelledSubmission.deferred.id}`,recorded:JSON.stringify(failedFaux.state.cancelledDeferred)===JSON.stringify([cancelledSubmission.deferred])};
}
const sorted=value=>Array.isArray(value)?value.map(sorted):value&&typeof value==='object'?Object.fromEntries(Object.entries(value).sort(([a],[b])=>a.localeCompare(b)).map(([key,value])=>[key,sorted(value)])):value;
console.log(JSON.stringify(sorted(output)));
