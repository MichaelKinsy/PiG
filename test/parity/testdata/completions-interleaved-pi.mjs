import { createServer } from "node:http";
import { readFileSync } from "node:fs";
const root=new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/",import.meta.url);
if(JSON.parse(readFileSync(new URL("package.json",root),"utf8")).version!=="0.87.1")throw new Error("Expected Pi 0.87.1");
const {stream,getModel}=await import(new URL("dist/compat.js",root));
for(const test of JSON.parse(readFileSync(new URL("./completions-interleaved.json",import.meta.url),"utf8"))){
 const server=createServer((_req,res)=>{res.writeHead(200,{"content-type":"text/event-stream"});for(const chunk of test.chunks)res.write(`data: ${JSON.stringify(chunk)}\n\n`);res.end();});
 await new Promise(resolve=>server.listen(0,"127.0.0.1",resolve));
 try{
  const model={...getModel("openai","gpt-4o-mini"),api:"openai-completions",compat:undefined,baseUrl:`http://127.0.0.1:${server.address().port}`};
  const response=stream(model,{messages:[{role:"user",content:"Use tools.",timestamp:0}]},{apiKey:"test"});const eventTypes=[];
  for await(const event of response)eventTypes.push(event.type);
  const result=await response.result();if(result.stopReason!=="toolUse")throw new Error(JSON.stringify(result));
  const contents=result.content.map(block=>block.type==="text"?`text:${block.text}`:block.type==="thinking"?`thinking:${block.thinking}:${block.thinkingSignature}`:`toolCall:${block.id}:${block.name}:${JSON.stringify(Object.fromEntries(Object.entries(block.arguments).sort(([a],[b])=>a.localeCompare(b))))}`);
  console.log(JSON.stringify({case:test.name,eventTypes,contents}));
 }finally{await new Promise(resolve=>server.close(resolve));}
}
