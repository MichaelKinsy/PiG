import { fileURLToPath, pathToFileURL } from "node:url";
import { readFileSync } from "node:fs";
import { join } from "node:path";

const root = fileURLToPath(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url));
if (JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version !== "0.87.1") throw new Error("Expected Pi 0.87.1");
const aiRoot = join(root, "node_modules/@earendil-works/pi-ai/dist");
const { stream } = await import(pathToFileURL(join(aiRoot, "api/azure-openai-responses.js")));
const { normalizeContext } = await import(pathToFileURL(join(aiRoot, "utils/transcript.js")));
const model = { id:"test-deployment", name:"Test Deployment", api:"azure-openai-responses", provider:"azure-openai-responses", baseUrl:"http://127.0.0.1:9/openai/v1", reasoning:false, input:["text"], cost:{input:0,output:0,cacheRead:0,cacheWrite:0}, contextWindow:10000,maxTokens:1000 };
const context = normalizeContext({ messages:[{role:"user",content:"Summarize this",timestamp:1}], tools:[{name:"read",description:"Read a file",parameters:{type:"object",properties:{path:{type:"string"}},required:["path"]}}] });
for (const choice of ["required", "none", {type:"function",name:"read"}]) {
 let payload;
 const result = await stream(model, context, { apiKey:"test-key", toolChoice:choice, fetch:async (_url, options) => {
  payload=JSON.parse(options.body);
  return new Response('data: {"type":"response.completed","response":{"status":"completed"}}\n\n', {status:200, headers:{"Content-Type":"text/event-stream"}});
 }}).result();
 console.log(`choice=${typeof payload.tool_choice === "object" ? `${payload.tool_choice.type}:${payload.tool_choice.name}` : payload.tool_choice} tools=${payload.tools.length}:${payload.tools[0].name} stop=${result.stopReason}`);
}
const invalid = await stream(model, context, {apiKey:"test-key",azureBaseUrl:"not-a-url"}).result();
console.log(`invalid-url=${invalid.stopReason === "error" && invalid.errorMessage.includes("Invalid Azure OpenAI base URL: not-a-url")}`);
