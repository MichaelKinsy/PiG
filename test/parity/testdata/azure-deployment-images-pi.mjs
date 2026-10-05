import { createServer } from "node:http";
import { readFileSync } from "node:fs";
const root = new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/", import.meta.url);
if (JSON.parse(readFileSync(new URL("package.json", root), "utf8")).version !== "1.0.3") throw new Error("Expected Pi 1.0.3");
const { stream } = await import(new URL("dist/api/azure-openai-responses.js", root));
const { getModel, normalizeContext } = await import(new URL("dist/compat.js", root));
const image = readFileSync(new URL("../../../ai/testdata/upstream-red-circle.png", import.meta.url)).toString("base64");
const requests = [];
let images = 0;
const server = createServer(async (req,res) => {
 const chunks = []; for await (const chunk of req) chunks.push(chunk);
 const payload = JSON.parse(Buffer.concat(chunks).toString());
 requests.push(payload.model);
 for (const item of payload.input) if (item.type === "function_call_output" && Array.isArray(item.output)) images += item.output.filter(part => part.type === "input_image").length;
 res.writeHead(200, { "content-type": "text/event-stream" });
 const events = requests.length === 1 ? [
  { type: "response.output_item.added", output_index: 0, item: { type: "function_call", id: "fc_circle", call_id: "call_circle", name: "image", arguments: "" } },
  { type: "response.output_item.done", output_index: 0, item: { type: "function_call", id: "fc_circle", call_id: "call_circle", name: "image", arguments: "{}" } },
 ] : [];
 events.push({ type: "response.completed", response: { status: "completed" } });
 res.end(events.map(event => `data: ${JSON.stringify(event)}\n\n`).join(""));
});
await new Promise(resolve => server.listen(0,"127.0.0.1",resolve));
try {
 const model = { ...getModel("azure","gpt-4o-mini"), baseUrl: `http://127.0.0.1:${server.address().port}` };
 const options = { apiKey: "test", azureDeploymentName: "image-deployment" };
 const context = { messages: [{ role: "user", content: "Describe the image.", timestamp: 0 }], tools: [{ name: "image", description: "", parameters: { type: "object", properties: {} } }] };
 const first = await stream(model,normalizeContext(context),options).result();
 if (first.stopReason !== "toolUse") throw new Error(JSON.stringify(first));
 const call = first.content.find(block => block.type === "toolCall");
 context.messages.push(first, { role: "toolResult", toolCallId: call.id, toolName: call.name, content: [{ type: "text", text: "A red circle." }, { type: "image", mimeType: "image/png", data: image }], isError: false, timestamp: 0 });
 const last = await stream(model,normalizeContext(context),options).result();
 if (last.stopReason !== "stop") throw new Error(JSON.stringify(last));
 console.log(`logical=${first.model} api=${first.api} requests=${requests.join(",")} images=${images}`);
} finally { await new Promise(resolve => server.close(resolve)); }
