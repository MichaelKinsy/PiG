// Pi 1.5.0 openai-responses-shared.ts renderIncrementalToolsNotice and renderToolRemovalNotice through the complete() request path: every case of
// responses-tool-notices.json is sent under four transports (additional_tools with the developer and the system instruction role, synthetic tool search,
// system messages folded into the prompt) and the request's input is printed with its keys sorted, one line per probe.
import { PI_PACKAGE_VERSION } from "./pi-version.mjs";
import {readFileSync} from "node:fs";
const root=new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/",import.meta.url);
if(JSON.parse(readFileSync(new URL("package.json",root),"utf8")).version!==PI_PACKAGE_VERSION)throw new Error(`Expected Pi ${PI_PACKAGE_VERSION}`);
const {complete}=await import(new URL("dist/compat.js",root));
const cases=JSON.parse(readFileSync(new URL("./responses-tool-notices.json",import.meta.url),"utf8"));
const transports=[
 {name:"additional_tools",reasoning:true,compat:{supportsMidConvoSystemMessages:true,supportsAdditionalTools:true}},
 {name:"additional_tools-system-role",reasoning:false,compat:{supportsMidConvoSystemMessages:true,supportsAdditionalTools:true}},
 {name:"tool-search",reasoning:true,compat:{supportsMidConvoSystemMessages:true,supportsToolSearch:true}},
 {name:"folded",reasoning:true,compat:{}},
];
function canonical(value){if(Array.isArray(value))return value.map(canonical);if(value&&typeof value==="object")return Object.fromEntries(Object.keys(value).sort().map(key=>[key,canonical(value[key])]));return value;}
for(const test of cases){for(const transport of transports){
 const model={id:"gpt-test",name:"gpt-test",api:"openai-responses",provider:"openai",baseUrl:"https://example.test/v1",reasoning:transport.reasoning,input:["text"],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:128000,maxTokens:4096,compat:transport.compat};
 let payload;
 const result=await complete(model,{messages:test.messages},{apiKey:"test",fetch:async(_url,options)=>{payload=JSON.parse(options.body);return new Response(`data: ${JSON.stringify({type:"response.completed",response:{status:"completed"}})}\n\n`,{headers:{"content-type":"text/event-stream"}});}});
 if(result.stopReason==="error")throw new Error(result.errorMessage);
 console.log(JSON.stringify({case:test.name,transport:transport.name,input:canonical(payload.input)}));
}}
