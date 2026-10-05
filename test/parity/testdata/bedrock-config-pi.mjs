import { fileURLToPath, pathToFileURL } from "node:url";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { createServer } from "node:http";

const root = fileURLToPath(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url));
if (JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version !== "1.0.2") throw new Error("Expected Pi 1.0.2");
const aiRoot = join(root, "node_modules/@earendil-works/pi-ai/dist");
const { stream } = await import(pathToFileURL(join(aiRoot, "api/bedrock-converse-stream.js")));
const { normalizeContext } = await import(pathToFileURL(join(aiRoot, "utils/transcript.js")));
const dir=mkdtempSync(join(tmpdir(),"bedrock-config-"));
writeFileSync(join(dir,"credentials"),"[explicit-profile]\naws_access_key_id = explicit-profile\naws_secret_access_key = profile-secret\n[scoped-profile]\naws_access_key_id = scoped-profile\naws_secret_access_key = profile-secret\n[ambient-profile]\naws_access_key_id = ambient-profile\naws_secret_access_key = profile-secret\n");
Object.assign(process.env,{AWS_CONFIG_FILE:join(dir,"config"),AWS_SHARED_CREDENTIALS_FILE:join(dir,"credentials"),AWS_PROFILE:"ambient-profile",AWS_ACCESS_KEY_ID:"AKIAEXAMPLE",AWS_SECRET_ACCESS_KEY:"secretexample",AWS_REGION:"us-east-2",AWS_EC2_METADATA_DISABLED:"true"});
for(const key of ["AWS_DEFAULT_PROFILE","AWS_SESSION_TOKEN","AWS_BEDROCK_SKIP_AUTH","AWS_BEARER_TOKEN_BEDROCK","AWS_ACCESS_KEY_ID","AWS_SECRET_ACCESS_KEY"]) delete process.env[key];
let authorization;
const server=createServer(async(req,res)=>{
 for await(const _chunk of req) { /* Drain request before closing the response. */ }
 authorization=req.headers.authorization;
 res.writeHead(200,{"content-type":"application/vnd.amazon.eventstream"});res.end();
});
await new Promise(resolve=>server.listen(0,"127.0.0.1",resolve));
try {
 const model={id:"us.anthropic.claude-opus-4-8",name:"Claude Opus 4.8",api:"bedrock-converse-stream",provider:"amazon-bedrock",baseUrl:`http://127.0.0.1:${server.address().port}`,reasoning:true,input:["text"],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:200000,maxTokens:32000};
 for(const [name,options] of [["explicit",{profile:"explicit-profile",region:"eu-west-1",env:{AWS_ACCESS_KEY_ID:"AKIAEXAMPLE",AWS_SECRET_ACCESS_KEY:"secretexample"}}],["scoped",{env:{AWS_PROFILE:"scoped-profile",AWS_ACCESS_KEY_ID:"AKIAEXAMPLE",AWS_SECRET_ACCESS_KEY:"secretexample"}}],["ambient",{}],["bearer",{apiKey:"bedrock-api-key"}]]) {
  if(name==="ambient") Object.assign(process.env,{AWS_ACCESS_KEY_ID:"AKIAEXAMPLE",AWS_SECRET_ACCESS_KEY:"secretexample"});
  authorization=undefined;
  await stream(model,normalizeContext({messages:[{role:"user",content:"hello",timestamp:1}]}),{cacheRetention:"none",...options,env:{AWS_BEDROCK_FORCE_HTTP1:"1",...options.env}}).result();
  if(!authorization) throw new Error("No HTTP request for "+name);
  const scope=/Credential=([^/]+)\/[^/]+\/([^/]+)\//.exec(authorization);
  console.log(`${name} auth=${scope ? `${scope[1]} region=${scope[2]}` : authorization}`);
 }
} finally {await new Promise(resolve=>server.close(resolve));rmSync(dir,{recursive:true,force:true});}
