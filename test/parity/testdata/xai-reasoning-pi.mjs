import { getModel, stream } from '../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/compat.js';

let captured;
const fetch = async (_url, init) => {
 captured = JSON.parse(init.body);
 return new Response('data: {"type":"response.completed","response":{"id":"resp_xai_test","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}\n\n', { headers: { 'content-type': 'text/event-stream' } });
};
const rows = [];
for (const id of ['grok-4.5', 'grok-4.7', 'grok-4.3']) {
 for (const effort of ['', 'medium']) {
  const options = { apiKey: 'test-token', fetch };
  if (effort) options.reasoningEffort = effort;
  const result = await stream(getModel('xai', id), { messages: [{ role: 'user', content: 'hello', timestamp: 1 }] }, options).result();
  if (result.stopReason !== 'stop') throw new Error(result.errorMessage);
  const reasoning = captured.reasoning ? Object.fromEntries(Object.entries(captured.reasoning).sort(([a], [b]) => a.localeCompare(b))) : null;
  rows.push([id, effort, reasoning, captured.include, result.stopReason]);
 }
}
console.log(JSON.stringify(rows));
