import {fileURLToPath,pathToFileURL} from "node:url";
import {readFileSync} from "node:fs";
import {join} from "node:path";
import {createServer} from "node:http";
const root=fileURLToPath(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/",import.meta.url));
if(JSON.parse(readFileSync(join(root,"package.json"),"utf8")).version!=="1.0.1")throw new Error("Expected Pi 1.0.1");
const aiRoot=join(root,"node_modules/@earendil-works/pi-ai/dist");
const {getModel,normalizeContext}=await import(pathToFileURL(join(aiRoot,"compat.js")));
const {stream}=await import(pathToFileURL(join(aiRoot,"api/bedrock-converse-stream.js")));
const canonical=value=>Array.isArray(value)?value.map(canonical):value&&typeof value==="object"?Object.fromEntries(Object.keys(value).sort().map(key=>[key,canonical(value[key])])):value;
let payload;
const server=createServer(async(req,res)=>{const chunks=[];for await(const chunk of req)chunks.push(chunk);payload=JSON.parse(Buffer.concat(chunks).toString());res.writeHead(200,{"content-type":"application/vnd.amazon.eventstream"});res.end();});
await new Promise(resolve=>server.listen(0,"127.0.0.1",resolve));
try{
 const baseModel=getModel("amazon-bedrock","us.anthropic.claude-sonnet-4-5-20250929-v1:0");
 for(const row of JSON.parse(readFileSync(new URL("./bedrock-cache-cases.json",import.meta.url),"utf8"))){
  process.env.AWS_BEDROCK_FORCE_CACHE=row.ambientForce;
  const model={...baseModel,id:row.model,name:"",baseUrl:`http://127.0.0.1:${server.address().port}`};
  payload=undefined;
  await stream(model,normalizeContext({systemPrompt:"system",messages:[{role:"user",content:"hello",timestamp:1}]}),{cacheRetention:row.option||undefined,env:{PI_CACHE_RETENTION:row.env,AWS_BEDROCK_FORCE_CACHE:row.force,AWS_BEDROCK_FORCE_HTTP1:"1",AWS_BEDROCK_SKIP_AUTH:"1",AWS_REGION:"us-east-1"}}).result();
  if(!payload)throw new Error("missing request");console.log(JSON.stringify(canonical([row.name,payload.system,payload.messages])));
 }
}finally{await new Promise(resolve=>server.close(resolve));}
