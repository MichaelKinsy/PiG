import { fileURLToPath, pathToFileURL } from "node:url";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { createServer } from "node:http";
import { crc32 } from "node:zlib";

const root = fileURLToPath(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url));
if (JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version !== "1.1.0") throw new Error("Expected Pi 1.1.0");
const aiRoot = join(root, "node_modules/@earendil-works/pi-ai/dist");
const { stream } = await import(pathToFileURL(join(aiRoot, "api/bedrock-converse-stream.js")));
const { normalizeContext } = await import(pathToFileURL(join(aiRoot, "utils/transcript.js")));

// AWS event-stream framing: total/header lengths, prelude CRC, headers, body, message CRC.
function frame(kind, payload) {
 const headers = Buffer.concat(Object.entries({":message-type":"event",":event-type":kind,":content-type":"application/json"}).map(([name,value]) => {
  const n=Buffer.from(name), v=Buffer.from(value), length=Buffer.alloc(2); length.writeUInt16BE(v.length);
  return Buffer.concat([Buffer.from([n.length]),n,Buffer.from([7]),length,v]);
 }));
 const body=Buffer.from(JSON.stringify(payload)), prelude=Buffer.alloc(12);
 prelude.writeUInt32BE(16+headers.length+body.length); prelude.writeUInt32BE(headers.length,4); prelude.writeUInt32BE(crc32(prelude.subarray(0,8)),8);
 const message=Buffer.concat([prelude,headers,body]), crc=Buffer.alloc(4); crc.writeUInt32BE(crc32(message));
 return Buffer.concat([message,crc]);
}
const events=JSON.parse(readFileSync(new URL("./bedrock-redacted-events.json",import.meta.url),"utf8"));
const response=Buffer.concat(events.map(([kind,body])=>frame(kind,body)));
const requests=[];
const server=createServer(async (req,res)=>{
 const chunks=[]; for await(const chunk of req) chunks.push(chunk);
 requests.push(JSON.parse(Buffer.concat(chunks).toString()));
 res.writeHead(200,{"content-type":"application/vnd.amazon.eventstream"});
 res.end(requests.length===1 ? response : Buffer.concat([frame("messageStart",{role:"assistant"}),frame("messageStop",{stopReason:"guardrail_intervened"})]));
});
await new Promise(resolve=>server.listen(0,"127.0.0.1",resolve));
try {
 const model={id:"global.openai.gpt-5.6-terra",name:"GPT-5.6 Terra (Global)",api:"bedrock-converse-stream",provider:"amazon-bedrock",baseUrl:`http://127.0.0.1:${server.address().port}`,reasoning:true,input:["text"],cost:{input:1.25,output:10,cacheRead:.125,cacheWrite:0},contextWindow:400000,maxTokens:128000};
 const options={cacheRetention:"none",env:{AWS_BEDROCK_FORCE_HTTP1:"1",AWS_BEDROCK_SKIP_AUTH:"1",AWS_REGION:"us-east-1"}};
 const user={role:"user",content:"hello",timestamp:1};
 const first=await stream(model,normalizeContext({messages:[user]}),options).result();
 if(first.stopReason!=="stop") throw new Error(first.errorMessage);
 const thinking=first.content[0];
 console.log(`first=${first.stopReason} thinking=${thinking?.redacted===true}:${thinking?.thinkingSignature} text=${thinking?.thinking}`);
 const second=await stream(model,normalizeContext({messages:[user,first,{role:"user",content:"continue",timestamp:2}]}),options).result();
 const replay=requests[1].messages.find(m=>m.role==="assistant");
 console.log(`replay=${replay?.content[0]?.reasoningContent?.redactedContent}`);
 console.log(`second=${second.stopReason} raw=${second.rawStopReason} error=${second.errorMessage}`);
} finally { await new Promise(resolve=>server.close(resolve)); }
