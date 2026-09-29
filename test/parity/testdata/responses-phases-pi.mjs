import { readFileSync } from "node:fs";
const root = new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/", import.meta.url);
if (JSON.parse(readFileSync(new URL("package.json", root), "utf8")).version !== "0.87.1") throw new Error("Expected Pi 0.87.1");
const { processResponsesStream } = await import(new URL("dist/api/openai-responses-shared.js", root));
const { AssistantMessageEventStream } = await import(new URL("dist/utils/event-stream.js", root));
for (const [first, last, status] of [["commentary", "commentary", "completed"], ["final_answer", "final_answer", "completed"], ["commentary", "final_answer", "completed"], ["final_answer", "final_answer", "incomplete"]]) {
 const cost = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 };
 const model = { id: "gpt-5-mini", provider: "openai", api: "openai-responses", cost };
 const output = { role: "assistant", content: [], api: model.api, provider: model.provider, model: model.id, stopReason: "pending", usage: { ...cost, totalTokens: 0, cost }, timestamp: 0 };
 const events = [
  { type: "response.output_item.added", output_index: 0, item: { type: "message", id: "msg_phase", content: [], phase: first } },
  { type: "response.output_item.done", output_index: 0, item: { type: "message", id: "msg_phase", content: [{ type: "output_text", text: "answer" }], phase: last } },
  { type: `response.${status}`, response: { id: "resp_phase", status, incomplete_details: { reason: "max_output_tokens" } } },
 ];
 const stream = new AssistantMessageEventStream();
 let text = `${first}/${last}/${status}`;
 const push = stream.push.bind(stream);
 stream.push = event => {
  if (event.type === "text_start") text += ` start:${event.partial.stopReason}`;
  if (event.type === "text_end") text += ` end:${event.partial.stopReason}`;
  push(event);
 };
 await processResponsesStream((async function*(){yield* events;})(), output, stream, model);
 console.log(`${text} terminal:${output.stopReason}`);
}
