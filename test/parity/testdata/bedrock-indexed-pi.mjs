import { fileURLToPath, pathToFileURL } from "node:url";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { createServer } from "node:http";
import { crc32 } from "node:zlib";

const root = fileURLToPath(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url));
if (JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version !== "1.0.0") throw new Error("Expected Pi 1.0.0");
const aiRoot = join(root, "node_modules/@earendil-works/pi-ai/dist");
const { stream } = await import(pathToFileURL(join(aiRoot, "api/bedrock-converse-stream.js")));
const { normalizeContext } = await import(pathToFileURL(join(aiRoot, "utils/transcript.js")));
const canonical = value => Array.isArray(value) ? value.map(canonical) : value && typeof value === "object" ? Object.fromEntries(Object.keys(value).sort().map(key => [key, canonical(value[key])])) : value;

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

for (const row of JSON.parse(readFileSync(new URL("./bedrock-indexed-events.json", import.meta.url), "utf8"))) {
 const response = Buffer.concat(row.events.map(([kind, payload]) => frame(kind, payload)));
 const server = createServer(async (req, res) => {
  for await (const _chunk of req) {}
  res.writeHead(200, {"content-type":"application/vnd.amazon.eventstream"}); res.end(response);
 });
 await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
 try {
  const model = {id:"global.openai.gpt-5.6-terra",name:"GPT-5.6 Terra (Global)",api:"bedrock-converse-stream",provider:"amazon-bedrock",baseUrl:`http://127.0.0.1:${server.address().port}`,reasoning:true,input:["text"],cost:{input:1.25,output:10,cacheRead:.125,cacheWrite:0},contextWindow:400000,maxTokens:128000};
  const events = stream(model, normalizeContext({messages:[{role:"user",content:"hello",timestamp:1}]}), {cacheRetention:"none",env:{AWS_BEDROCK_FORCE_HTTP1:"1",AWS_BEDROCK_SKIP_AUTH:"1",AWS_REGION:"us-east-1"}});
  const trace = [];
  for await (const event of events) trace.push(event.contentIndex === undefined ? event.type : `${event.type}:${event.contentIndex}`);
  const result = await events.result();
  console.log(JSON.stringify(canonical([row.name,result.stopReason,result.content,trace])));
 } finally { await new Promise(resolve => server.close(resolve)); }
}
