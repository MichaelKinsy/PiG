import {fileURLToPath,pathToFileURL} from "node:url";
import {readFileSync} from "node:fs";
import {join} from "node:path";
import {createServer} from "node:http";
import {crc32} from "node:zlib";
const root=fileURLToPath(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/",import.meta.url));
if(JSON.parse(readFileSync(join(root,"package.json"),"utf8")).version!=="0.99.2")throw new Error("Expected Pi 0.99.2");
const aiRoot=join(root,"node_modules/@earendil-works/pi-ai/dist");
const {getModel,normalizeContext}=await import(pathToFileURL(join(aiRoot,"compat.js")));
const {stream}=await import(pathToFileURL(join(aiRoot,"api/bedrock-converse-stream.js")));
function frame(fields,payload){
 const headers=Buffer.concat(Object.entries(fields).map(([name,value])=>{const n=Buffer.from(name),v=Buffer.from(value),length=Buffer.alloc(2);length.writeUInt16BE(v.length);return Buffer.concat([Buffer.from([n.length]),n,Buffer.from([7]),length,v]);}));
 const body=Buffer.from(JSON.stringify(payload)),prelude=Buffer.alloc(12);prelude.writeUInt32BE(16+headers.length+body.length);prelude.writeUInt32BE(headers.length,4);prelude.writeUInt32BE(crc32(prelude.subarray(0,8)),8);const bytes=Buffer.concat([prelude,headers,body]),crc=Buffer.alloc(4);crc.writeUInt32BE(crc32(bytes));return Buffer.concat([bytes,crc]);
}
const canonical=value=>Array.isArray(value)?value.map(canonical):value&&typeof value==="object"?Object.fromEntries(Object.keys(value).sort().map(key=>[key,canonical(value[key])])):value;
let fixture;
const server=createServer(async(req,res)=>{
 for await(const _chunk of req){}
 res.setHeader("x-amzn-requestid","11111111-2222-3333-4444-555555555555");
 if(fixture.status){res.writeHead(fixture.status,{"content-type":"application/json","x-amzn-errortype":fixture.code});res.end(JSON.stringify({message:fixture.message}));return;}
 res.writeHead(200,{"content-type":"application/vnd.amazon.eventstream"});
 const fields={":message-type":fixture.kind,":content-type":"application/json"};
 if(fixture.kind==="exception")fields[":exception-type"]=fixture.code;else{fields[":error-code"]=fixture.code;fields[":error-message"]=fixture.message;}
 res.end(Buffer.concat([frame({":message-type":"event",":event-type":"messageStart",":content-type":"application/json"},{role:"assistant"}),frame(fields,{message:fixture.message})]));
});
await new Promise(resolve=>server.listen(0,"127.0.0.1",resolve));
try{
 const model={...getModel("amazon-bedrock","us.anthropic.claude-opus-4-8"),baseUrl:`http://127.0.0.1:${server.address().port}`};
 for(const value of JSON.parse(readFileSync(new URL("./bedrock-error-cases.json",import.meta.url),"utf8"))){
  fixture=value;const controller=new AbortController();if(fixture.abort)controller.abort();
  const result=await stream(model,normalizeContext({messages:[{role:"user",content:"hello",timestamp:1}]}),{signal:controller.signal,cacheRetention:"none",env:{AWS_BEDROCK_SKIP_AUTH:"1",AWS_BEDROCK_FORCE_HTTP1:"1",AWS_REGION:"us-east-1"}}).result();
  console.log(JSON.stringify(canonical([fixture.name,result.stopReason,result.errorMessage,result.diagnostics?.find(d=>d.type==="bedrock_response_failure")?.details??null])));
 }
}finally{await new Promise(resolve=>server.close(resolve));}
