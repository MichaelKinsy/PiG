import {readFileSync} from "node:fs";
import {zstdDecompressSync} from "node:zlib";
const root=new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/",import.meta.url);
if(JSON.parse(readFileSync(new URL("package.json",root),"utf8")).version!=="0.87.1")throw new Error("Expected Pi 0.87.1");
const {stream}=await import(new URL("dist/api/openai-codex-responses.js",root));
const {normalizeContext}=await import(new URL("dist/utils/transcript.js",root));
const token=`aaa.${Buffer.from(JSON.stringify({"https://api.openai.com/auth":{chatgpt_account_id:"acc_test"}})).toString("base64")}.bbb`;
const model={id:"gpt-5.5",name:"Mapped Codex",provider:"openai-codex",api:"openai-codex-responses",baseUrl:"https://chatgpt.com/backend-api",reasoning:true,thinkingLevelMap:{minimal:"low",xhigh:"xhigh"},input:["text"],cost:{input:1,output:2,cacheRead:0,cacheWrite:0},contextWindow:400000,maxTokens:128000};
for(const tier of ["flex","priority"]){
 let payload;
 const result=await stream(model,normalizeContext({messages:[{role:"user",content:"Hi",timestamp:1}],tools:[{name:"optional",parameters:{type:"object",properties:{value:{type:"string"}},required:["value"]},constrainedSampling:false}]}),{apiKey:token,transport:"sse",reasoningEffort:"minimal",serviceTier:tier,fetch:async(_url,options)=>{payload=JSON.parse(zstdDecompressSync(options.body));return new Response('data: {"type":"response.completed","response":{"status":"completed","service_tier":"default","usage":{"input_tokens":1000000,"output_tokens":1000000,"total_tokens":2000000}}}\n\n',{headers:{"content-type":"text/event-stream"}})}}).result();
 console.log(JSON.stringify({tier,effort:payload.reasoning.effort,strict:payload.tools[0].strict,cost:result.usage.cost.total,stop:result.stopReason}));
}
// Node's AbortSignal timer is unref'ed; keep this standalone fake fetch process alive until it settles.
const hold=setInterval(()=>{},1000);
try {
const result=await stream(model,normalizeContext({messages:[]}),{apiKey:token,transport:"sse",timeoutMs:10,fetch:async(_url,options)=>await new Promise((_resolve,reject)=>options.signal.addEventListener("abort",()=>reject(options.signal.reason),{once:true}))}).result();
console.log(JSON.stringify({timeout:result.errorMessage,stop:result.stopReason}));
} finally { clearInterval(hold); }
