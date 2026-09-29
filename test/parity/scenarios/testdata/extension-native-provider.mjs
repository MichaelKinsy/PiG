// Pi 0.87.1 ModelRuntime.registerNativeProvider installs one callback-bearing
// provider in the catalog and dispatches through it after request auth.
import { createAssistantMessageEventStream } from "@earendil-works/pi-ai";
import { writeFileSync } from "node:fs";
// Object property insertion order belongs to the JSON encoders, not Provider behavior.
const sorted=value=>Array.isArray(value)?value.map(sorted):value&&typeof value==="object"?Object.fromEntries(Object.keys(value).sort().map(key=>[key,sorted(value[key])])):value;

export default function(pi) {
 let refreshed=false;
 const streamMethods=[];
 let cancelled=false;
 const model={id:"native-model",name:"Native Model",api:"openai-completions",provider:"parity-native",baseUrl:"http://127.0.0.1:9/v1",reasoning:false,input:["text"],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:1000,maxTokens:100};
 const stream=(m,ctx,options={})=>{
  if(options.apiKey!=="native-key") throw new Error("native stream did not receive resolved auth");
  if(options.headers?.["X-Native"]!=="yes") throw new Error("native stream did not receive auth headers");
  const user=ctx.messages.findLast(m=>m.role==="user")?.content;
  if(user==="throw")throw new Error("native stream failure");
  const s=createAssistantMessageEventStream();
  const text=ctx.messages.some(m=>m.role==="user")?"native answer":"no user";
  const message={role:"assistant",content:[],api:m.api,provider:m.provider,model:m.id,usage:{input:1,output:2,cacheRead:0,cacheWrite:0,totalTokens:3,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}},stopReason:"stop",timestamp:123};
  queueMicrotask(()=>{
   s.push({type:"start",partial:structuredClone(message)});
   if(user==="cancel"){
    options.signal.addEventListener("abort",()=>{cancelled=true;message.stopReason="aborted";message.errorMessage="native cancelled";s.push({type:"error",reason:"aborted",error:message})},{once:true});
    return;
   }
   message.content.push({type:"text",text:""});s.push({type:"text_start",contentIndex:0,partial:structuredClone(message)});
   message.content[0].text=text;s.push({type:"text_delta",contentIndex:0,delta:text,partial:structuredClone(message)});
   s.push({type:"text_end",contentIndex:0,content:text,partial:structuredClone(message)});
   s.push({type:"done",reason:"stop",message});
  });return s;
 };
 const provider={id:"parity-native",name:"Native",baseUrl:model.baseUrl,auth:{apiKey:{name:"API key",check:async()=>({type:"api_key",source:"native-check"}),resolve:async()=>({auth:{apiKey:"native-key",headers:{"X-Native":"yes"}}})}},getModels:()=>refreshed?[model,{...model,id:"refreshed-model"},{...model,id:"filtered-model"}]:[model],filterModels:models=>models.filter(model=>model.id!=="filtered-model"),refreshModels:async ctx=>{if(ctx.allowNetwork){if(ctx.force!==true)throw new Error("refresh force missing");await ctx.publish({persist:{models:[]},update:()=>{refreshed=true}})}},stream:(model,context,options)=>{streamMethods.push("api");return stream(model,context,options)},streamSimple:(model,context,options)=>{streamMethods.push("simple");return stream(model,context,options)}};
 pi.registerProvider(provider);
 pi.registerCommand("native-probe",{description:"Probe native registration",handler:async(args,ctx)=>{
  const r=ctx.modelRegistry;
  const before={ownerObject:r.getRegisteredNativeProvider("parity-native")===provider,found:!!r.find("parity-native","native-model"),catalog:r.getAll().some(m=>m.provider==="parity-native")};
  const refresh=await r.refresh({providers:["parity-native"],allowNetwork:true,force:true});
  const auth=await r.getProviderAuth("parity-native");
  const s=r.streamSimple(model,{messages:[{role:"user",content:"question",timestamp:1}]});
  const events=[];for await(const event of s) events.push(event.type);
  const answer=await s.result();
  const apiAnswer=await r.complete(model,{messages:[{role:"user",content:"api question",timestamp:1}]});
  const failure=await r.complete(model,{messages:[{role:"user",content:"throw",timestamp:1}]});
  const abort=new AbortController();
  const interrupted=r.streamSimple(model,{messages:[{role:"user",content:"cancel",timestamp:1}]},{signal:abort.signal});
  for await(const event of interrupted){if(event.type==="start")abort.abort()}
  const aborted=await interrupted.result();
  writeFileSync(args.trim(),JSON.stringify(sorted({before,refreshed:!!r.find("parity-native","refreshed-model"),refresh:{aborted:refresh.aborted,errors:[...refresh.errors.keys()]},auth,events,answer,apiAnswer,streamMethods,cancelled,failure:{reason:failure.stopReason,error:failure.errorMessage},aborted,available:r.getAvailable().filter(m=>m.provider==="parity-native").map(m=>m.id)}),null,1)+"\n");
 }});
}
