// D82 W5 real-body tick oracle for mistral-conversations (Pi 1.0.3, packages/ai/src/api/mistral-conversations.ts).
//
// ticks.mjs scripts the response body, so every read is an already buffered chunk. This probe runs Pi's real stream() against a
// loopback server that writes headers and the whole body in one write (`buffered`), which is the case in which every microtask of
// the provider and the consumer runs with no I/O in between. A self-rescheduling microtask counts ticks from the first body read.
// Each mark is the tick of one synchronous observation: `push:TYPE` (EventStream.push), `deliver:TYPE` (the consumer's `for await`).
// The spin is capped at 200 ticks: a fixture that needs a macrotask (end of body after the last chunk) is not part of this oracle.
//
// usage: node realticks.mjs <out.json>     (env PI_PACKAGE_ROOT = the installed pi-coding-agent package)
import { createServer } from 'node:http';
import { once } from 'node:events';
import assert from 'node:assert/strict';
import { readFile, writeFile } from 'node:fs/promises';

const root = process.env.PI_PACKAGE_ROOT;
const scope = root + '/node_modules/@earendil-works/';
assert.equal(JSON.parse(await readFile(scope + 'pi-ai/package.json', 'utf8')).version, '1.0.3');
const { stream: mistralStream } = await import(scope + 'pi-ai/dist/api/mistral-conversations.js');
const { AssistantMessageEventStream } = await import(scope + 'pi-ai/dist/utils/event-stream.js');

const args = '{"path":"parity-read-target.txt"}';
const chunk = (delta, finish = null, extra = {}) => ({ id: 'chatcmpl-strict', model: 'strict', choices: [{ index: 0, delta, finish_reason: finish }], ...extra });
const usage = { prompt_tokens: 10, completion_tokens: 3, total_tokens: 13 };
const call = (index, id, name, argumentsText) => ({ index, id, type: 'function', function: { name, arguments: argumentsText } });

// fixtures: name -> ordered wire records. A `raw` record is written verbatim after `data: `; the string '[DONE]' is the terminator.
const fixtures = {
  // The RPC33 shape: one tool call, arguments in one delta, finish and usage in the same chunk.
  tool: [chunk({ tool_calls: [call(0, 'strictread1', 'read', args)] }, 'tool_calls', { usage }), { raw: '[DONE]' }],
  // Every content kind: array thinking, string and array text, empty deltas, a tool call whose arguments split across chunks, usage on its own chunk.
  mixed: [
    chunk({ role: 'assistant', content: '' }),
    chunk({ content: [{ type: 'thinking', thinking: [{ type: 'text', text: 'one' }] }] }),
    chunk({ content: [{ type: 'thinking', thinking: [{ type: 'text', text: ' two' }] }] }),
    chunk({ content: 'ans' }),
    chunk({ content: [{ type: 'text', text: 'wer' }] }),
    chunk({ tool_calls: [call(0, 'strictread1', 'read', '{"path":"parity-')] }),
    chunk({ tool_calls: [{ index: 0, function: { name: '', arguments: 'read-target.txt"}' } }] }),
    chunk({}, 'tool_calls'),
    { id: 'chatcmpl-strict', model: 'strict', choices: [], usage },
    { raw: '[DONE]' },
  ],
  // Two tool calls in one chunk, then a delta for the first.
  multi: [
    chunk({ content: 'go' }),
    chunk({ tool_calls: [call(0, 'callone1', 'read', '{"path":"a"'), call(1, 'calltwo22', 'read', '{"path":"b"}')] }),
    chunk({ tool_calls: [{ index: 0, function: { name: '', arguments: '}' } }] }),
    chunk({}, 'tool_calls', { usage }),
    { raw: '[DONE]' },
  ],
  // finish_reason "error" does not end the loop: the chunk that follows is still consumed before Pi throws.
  errorFinish: [chunk({ content: 'par' }), chunk({}, 'error'), chunk({ content: 'tial' }), { raw: '[DONE]' }],
  truncated: [chunk({ content: 'par' })],
  noDone: [chunk({ content: 'ok' }), chunk({}, 'stop', { usage })],
  // Pi's JSON.parse throws inside readMistralEvents and reports V8's SyntaxError message.
  malformed: [chunk({ content: 'par' }), { raw: '{"id":"x","choices":[' }],
  notjson: [chunk({ content: 'par' }), { raw: 'oops' }],
  nochoices: [chunk({ content: 'par' }), { raw: '{"id":"x"}' }],
};
const terminal = { tool: 'toolUse', mixed: 'toolUse', multi: 'toolUse', errorFinish: 'error', truncated: 'error', noDone: 'stop', malformed: 'error', notjson: 'error', nochoices: 'error' };
const reply = [chunk({ content: 'ok' }, 'stop', { usage }), { raw: '[DONE]' }];

const sse = event => `data: ${event.raw ?? JSON.stringify(event)}\n\n`;
const model = port => ({
  id: 'strict', name: 'strict', api: 'mistral-conversations', provider: 'p', baseUrl: `http://127.0.0.1:${port}`,
  reasoning: false, input: ['text'], contextWindow: 128000, maxTokens: 1000,
  cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
});

let ticks = 0;
let state = 'idle'; // idle -> armed (stream created) -> counting (first read)
const spin = () => { ticks++; if (state === 'counting' && ticks < 200) queueMicrotask(spin); };
const originalRead = ReadableStreamDefaultReader.prototype.read;
ReadableStreamDefaultReader.prototype.read = function read(...args) {
  const promise = originalRead.apply(this, args);
  if (state === 'armed') { state = 'counting'; ticks = 0; queueMicrotask(spin); }
  return promise;
};
let marks = [];
const originalPush = AssistantMessageEventStream.prototype.push;
AssistantMessageEventStream.prototype.push = function push(event) {
  if (state === 'counting') marks.push([`push:${event.type}`, ticks]);
  return originalPush.call(this, event);
};

const out = [];
for (const fixture of ['tool', 'mixed', 'multi', 'errorFinish', 'malformed', 'notjson', 'nochoices']) {
  const body = Buffer.from(fixtures[fixture].map(sse).join(''));
  const server = createServer(async (req, res) => {
    for await (const _ of req) {}
    res.writeHead(200, { 'content-type': 'text/event-stream', 'content-length': body.length });
    res.end(body);
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  marks = [];
  state = 'armed';
  const response = mistralStream(model(server.address().port), { messages: [{ role: 'user', content: [{ type: 'text', text: 'probe' }], timestamp: 1 }] }, { apiKey: 'k', maxRetries: 0 });
  for await (const event of response) if (state === 'counting') marks.push([`deliver:${event.type}`, ticks]);
  const result = await response.result();
  state = 'idle';
  server.closeAllConnections();
  await new Promise(resolve => server.close(resolve));
  assert.ok(marks.every(([, tick]) => tick < 200), `${fixture} left the microtask domain`);
  out.push({ fixture, body: body.toString(), marks, stopReason: result.stopReason, errorMessage: result.errorMessage ?? null });
}
await writeFile(process.argv[2], JSON.stringify({ piVersion: '1.0.3', node: process.version, cases: out }, null, 2) + '\n');
