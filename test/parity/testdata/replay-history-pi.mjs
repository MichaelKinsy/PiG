import {readFileSync} from "node:fs";
const root=new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/",import.meta.url);
if(JSON.parse(readFileSync(new URL("package.json",root),"utf8")).version!=="1.0.3")throw new Error("Expected Pi 1.0.3");
const {complete}=await import(new URL("dist/compat.js",root));
const cases=JSON.parse(readFileSync(new URL("./replay-history.json",import.meta.url),"utf8"));
function canonical(value){if(Array.isArray(value))return value.map(canonical);if(value&&typeof value==="object")return Object.fromEntries(Object.keys(value).sort().map(key=>[key,canonical(value[key])]));return value;}
for(const api of ["openai-completions","openai-responses"]){for(const test of cases){
 const model={id:test.model,name:test.model,api,provider:api==="openai-completions"?"moonshotai":"openai",baseUrl:"https://example.test/v1",reasoning:true,input:["text","image"],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:128000,maxTokens:4096,compat:{supportsMidConvoSystemMessages:true,supportsMidConvoToolAdditions:true,supportsAdditionalTools:true}};
 let payload;
 const result=await complete(model,{messages:test.messages},{apiKey:"test",fetch:async(_url,options)=>{payload=JSON.parse(options.body);const data=api==="openai-completions"?{choices:[{delta:{},finish_reason:"stop"}]}:{type:"response.completed",response:{status:"completed"}};return new Response(`data: ${JSON.stringify(data)}\n\n`,{headers:{"content-type":"text/event-stream"}});}});
 if(result.stopReason==="error")throw new Error(result.errorMessage);
 console.log(JSON.stringify({api,case:test.name,input:canonical(payload.input??payload.messages)}));
}}
