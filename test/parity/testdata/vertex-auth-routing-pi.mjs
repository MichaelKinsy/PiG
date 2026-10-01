import {createServer} from "node:http";
import {readFileSync} from "node:fs";
import {createRequire} from "node:module";
const root = new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/", import.meta.url);
if(JSON.parse(readFileSync(new URL("package.json",root),"utf8")).version!=="0.99.2")throw new Error("Expected Pi 0.99.2");
const fromAi=createRequire(new URL("dist/api/google-vertex.js",root));
const fromGoogle=createRequire(fromAi.resolve("@google/genai"));
const {GoogleAuth}=fromGoogle("google-auth-library");
// Replace only the ADC token source; Google's actual URL/auth-mode logic runs.
GoogleAuth.prototype.getRequestHeaders=async()=>new Headers({Authorization:"Bearer adc-fixture"});
const {stream}=await import(new URL("dist/api/google-vertex.js",root));
const {getModel,normalizeContext}=await import(new URL("dist/compat.js",root));
const cases=JSON.parse(readFileSync(new URL("vertex-auth-routing.json",import.meta.url),"utf8"));
for(const test of cases){
 process.env.GOOGLE_CLOUD_API_KEY=test.envKey??"";
 let captured;
 const server=createServer((_req,res)=>{
  captured={case:test.name,path:_req.url.split("?")[0],key:_req.headers["x-goog-api-key"]??"",auth:_req.headers.authorization??"",userAgent:test.userAgent?(_req.headers["user-agent"]??""):""};
  res.writeHead(200,{"content-type":"text/event-stream"});res.end('data: {"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}\n\n');
 });
 await new Promise(resolve=>server.listen(0,"127.0.0.1",resolve));
 try{
  const model={...getModel("google-vertex","gemini-3-flash-preview"),baseUrl:`http://127.0.0.1:${server.address().port}${test.path??""}`};
  const result=await stream(model,normalizeContext({messages:[{role:"user",content:"hello",timestamp:0}]}),{apiKey:test.key,project:"test-project",location:"us-central1",headers:test.userAgent?{"User-Agent":test.userAgent}:undefined}).result();
  if(result.stopReason!=="stop")throw new Error(JSON.stringify(result));
  console.log(JSON.stringify({...captured,api:result.api}));
 }finally{await new Promise(resolve=>server.close(resolve));}
}
