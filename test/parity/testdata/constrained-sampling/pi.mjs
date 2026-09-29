import { createServer } from 'node:http';
import { readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
const root = process.env.PI_PACKAGE_ROOT ?? resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
const ai = join(root, 'node_modules/@earendil-works/pi-ai/dist');
const { normalizeContext } = await import(pathToFileURL(join(ai, 'compat.js')));
const { stream } = await import(pathToFileURL(join(ai, 'api/openai-responses.js')));
const body = readFileSync('test/parity/testdata/constrained-sampling/events.sse');
const server = createServer((req, res) => { req.resume(); res.setHeader('content-type', 'text/event-stream'); res.end(body); });
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
try {
 const model = { id: 'gpt-test', name: 'GPT Test', api: 'openai-responses', provider: 'openai', baseUrl: `http://127.0.0.1:${server.address().port}`, reasoning: false, input: ['text', 'image'], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 4096, compat: { supportsOpenAIGrammarTools: true } };
 const tools = [{ name: 'sample_tool', description: 'Sample tool', parameters: { type: 'object', properties: { payload: { type: 'string' } }, required: ['payload'], additionalProperties: false }, constrainedSampling: { type: 'grammar', variants: { openai_lark: 'start: /[a-z]+/' } } }];
 const events = stream(model, normalizeContext({ tools, messages: [{ role: 'user', content: 'hi', timestamp: 0 }] }), { apiKey: 'test' });
 const starts = [], partials = [];
 let deltas = '';
 // Match upstream captureToolCallEvents: observe publication, not the later live-partial value seen by an async consumer.
 const push = events.push.bind(events);
 events.push = event => {
  if (event.type === 'toolcall_start') starts.push(structuredClone(event.partial.content[event.contentIndex].arguments));
  if (event.type === 'toolcall_delta') { deltas += event.delta; partials.push(structuredClone(event.partial.content[event.contentIndex].arguments)); }
  return push(event);
 };
 const result = await events.result();
 if (result.stopReason === 'error') throw new Error(result.errorMessage);
 const call = result.content[0];
 const capture = async messages => {
  let input;
  await stream(model, normalizeContext({ tools, messages }), { apiKey: 'test', onPayload(payload) { input = payload.input; throw new Error('captured'); } }).result();
  if (!input) throw new Error('no input captured');
  return input;
 };
 const replay = await capture([result, { role: 'toolResult', toolCallId: call.id, toolName: call.name, content: [{ type: 'text', text: 'done' }], isError: false, timestamp: 0 }]);
 const foreignCall = { ...call, id: 'call 1|fc_native', namespace: 'functions' };
 const foreignReplay = await capture([{ ...result, api: 'openai-completions', provider: 'other', model: 'source', content: [foreignCall] }, { role: 'toolResult', toolCallId: foreignCall.id, toolName: call.name, content: [{ type: 'text', text: 'done' }], isError: false, timestamp: 0 }]);
 const sorted = value => Array.isArray(value) ? value.map(sorted) : value && typeof value === 'object' ? Object.fromEntries(Object.keys(value).sort().map(key => [key, sorted(value[key])])) : value;
 const orderedTool = JSON.parse(readFileSync('test/parity/testdata/constrained-sampling/ordered-tool.json', 'utf8'));
 let strictRequired;
 await stream({ ...model, compat: { supportsStrictMode: true } }, normalizeContext({ tools: [orderedTool], messages: [] }), { apiKey: 'test', onPayload(payload) { strictRequired = payload.tools[0].parameters.required; throw new Error('captured'); } }).result();
 console.log(JSON.stringify(sorted({ strictRequired, starts, partials, deltaArguments: JSON.parse(deltas), finalArguments: call.arguments, reason: result.stopReason, replay, foreignReplay })));
} finally { await new Promise(resolve => server.close(resolve)); }
