// D82 W5 tick oracle for mistral-conversations (Pi 0.99.1, packages/ai/src/api/mistral-conversations.ts).
//
// Runs Pi's real stream() with options.fetch returning a Response over a scripted ReadableStream, so every read, await and yield
// is a microtask with no I/O in between. A self-rescheduling microtask counts ticks from the first body read. Every mark is the
// tick count at which one synchronous observation ran:
//
//   read:N     the N-th ReadableStreamDefaultReader.read() call
//   push:TYPE  EventStream.push (the provider's push of one event)
//   deliver:T  the consumer's `for await` over the returned stream
//
// The hooks return the original values and promises, so they add no microtask. `result` is the terminal message without its clock. The scenarios cover chunk splits, CRLF, multi-byte splits, BOM, invalid UTF-8, malformed JSON, rejected reads and
// a body that ends without a terminal event.
//
// usage: node ticks.mjs <out.json>     (env PI_PACKAGE_ROOT = the installed pi-coding-agent package)
import assert from 'node:assert/strict';
import { readFile, writeFile } from 'node:fs/promises';

const root = process.env.PI_PACKAGE_ROOT;
const scope = root + '/node_modules/@earendil-works/';
assert.equal(JSON.parse(await readFile(scope + 'pi-ai/package.json', 'utf8')).version, '0.99.2');
const { stream: mistralStream } = await import(scope + 'pi-ai/dist/api/mistral-conversations.js');
const { AssistantMessageEventStream } = await import(scope + 'pi-ai/dist/utils/event-stream.js');

const usage = { prompt_tokens: 10, completion_tokens: 3, total_tokens: 13 };
const chunk = (delta, finish = null, extra = {}) => ({ id: 'r1', choices: [{ index: 0, delta, finish_reason: finish }], ...extra });
const rec = event => `data: ${typeof event === 'string' ? event : JSON.stringify(event)}\n\n`;
const call = (index, id, name, args) => ({ index, id, type: 'function', function: { name, arguments: args } });
const records = [
  chunk({ role: 'assistant', content: '' }),
  chunk({ content: [{ type: 'thinking', thinking: [{ type: 'text', text: 'hm ✓' }] }] }),
  chunk({ content: 'héllo ✓ 😀' }),
  chunk({ content: [{ type: 'text', text: 'more' }] }),
  chunk({ tool_calls: [call(0, 'call-1', 'read', '{"path":') ] }),
  chunk({ tool_calls: [{ index: 0, function: { name: '', arguments: '"a.txt"}' } }, call(1, 'call-2', 'read', '{}')] }),
  chunk({}, 'tool_calls', { usage }),
  '[DONE]',
];
const enc = text => Buffer.from(text);
const all = enc(records.map(rec).join(''));
const split = (buffer, size) => { const out = []; for (let i = 0; i < buffer.length; i += size) out.push(buffer.subarray(i, i + size)); return out; };
const wireOf = list => list.map(rec).join('');
const emoji = all.indexOf(Buffer.from('😀'));
const sep = (list, separator) => enc(wireOf(list).replaceAll('\n\n', separator));

// name -> { chunks: Buffer[], end: 'close' | 'error' }
const scenarios = {
  oneChunk: { chunks: [all] },
  perRecord: { chunks: records.map(r => enc(rec(r))) },
  bytes7: { chunks: split(all, 7) },
  splitInsideEmoji: { chunks: [all.subarray(0, emoji + 2), all.subarray(emoji + 2)] },
  crlf: { chunks: [sep(records, '\r\n\r\n')] },
  crlfSplit: { chunks: (() => { const text = sep(records.slice(0, 3), '\r\n\r\n'); const cut = text.indexOf('\r\n\r\n') + 1; return [text.subarray(0, cut), text.subarray(cut)]; })() },
  crOnly: { chunks: [sep(records, '\r\r')] },
  mixedSeparators: { chunks: [sep(records, '\r\n\n')] },
  bom: { chunks: [Buffer.concat([Buffer.from([0xef, 0xbb, 0xbf]), all])] },
  bomSplit: { chunks: [Buffer.concat([Buffer.from([0xef, 0xbb])]), Buffer.concat([Buffer.from([0xbf]), all])] },
  invalidUTF8: { chunks: [enc(rec(records[0]) + rec(records[1])), Buffer.concat([enc('data: {"id":"r1","choices":[{"delta":{"content":"a'), Buffer.from([0xff, 0xe2, 0x82]), enc('b"}}]}\n\n')]), enc(rec(records[6]))] },
  truncatedUTF8: { chunks: [enc(rec(records[0]) + rec(records[2])), Buffer.from([0xe2, 0x82])] },
  finishError: { chunks: [enc(wireOf([records[2], chunk({}, 'error'), records[3], '[DONE]']))] },
  finishUnknown: { chunks: [enc(wireOf([records[2], chunk({}, 'weird'), '[DONE]']))] },
  finishLength: { chunks: [enc(wireOf([records[2], chunk({}, 'model_length', { usage: { prompt_tokens: 4, completion_tokens: 2, prompt_tokens_details: { cached_tokens: 3 } } }), '[DONE]']))] },
  noFinish: { chunks: [enc(wireOf(records.slice(0, 3)))] },
  noDone: { chunks: [enc(wireOf(records.slice(0, 7)))] },
  afterDone: { chunks: [enc(wireOf([records[2], chunk({}, 'stop'), '[DONE]', records[3]]))] },
  trailingRecord: { chunks: [enc(wireOf(records.slice(0, 2)) + rec(records[6]).slice(0, -2))] },
  trailingDone: { chunks: [enc(wireOf(records.slice(0, 2)) + 'data: [DONE]')] },
  malformed: { chunks: [enc(wireOf(records.slice(0, 2)) + 'data: {"id":"r1","choices":[\n\n')] },
  notJSON: { chunks: [enc(wireOf(records.slice(0, 2)) + 'data: oops\n\n')] },
  noChoices: { chunks: [enc(wireOf(records.slice(0, 2)) + 'data: {"id":"x"}\n\n')] },
  emptyChoices: { chunks: [enc(wireOf([records[2], { id: 'r2', choices: [], usage }, chunk({}, 'stop'), '[DONE]']))] },
  ignoredLines: { chunks: [enc('event: x\nid: 1\ndata:  {"id":"r1",\ndata: "choices":[{"delta":{"content":"hi"}}]}\n\n: comment\n\ndata:\n\n' + rec(chunk({}, 'stop')) + rec('[DONE]'))] },
  emptyText: { chunks: [enc(wireOf([chunk({ content: '' }), chunk({ content: [{ type: 'text' }] }), chunk({ content: [{ type: 'thinking', thinking: [] }] }), chunk({}, 'stop'), '[DONE]']))] },
  missingDelta: { chunks: [enc(wireOf([records[2], { id: 'r1', choices: [{}] }, records[3]]))] },
  nullChoice: { chunks: [enc(wireOf([records[2], { id: 'r1', choices: [null] }, chunk({}, 'stop'), '[DONE]']))] },
  noFunction: { chunks: [enc(wireOf([records[2], chunk({ tool_calls: [{ index: 0, id: 'x' }] }), records[3]]))] },
  nullContentItem: { chunks: [enc(wireOf([records[2], chunk({ content: [null] }), records[3]]))] },
  readRejects: { chunks: [enc(wireOf(records.slice(0, 3)))], end: 'error' },
  rejectsFirst: { chunks: [], end: 'error' },
  emptyBody: { chunks: [] },
};

const model = { id: 'strict', name: 'strict', api: 'mistral-conversations', provider: 'p', baseUrl: 'http://127.0.0.1:1', reasoning: false, input: ['text'], contextWindow: 1, maxTokens: 1, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } };
let marks = null;
let ticks = 0;
let spinning = false;
const spin = () => { ticks++; if (spinning) queueMicrotask(spin); };
const originalPush = AssistantMessageEventStream.prototype.push;
AssistantMessageEventStream.prototype.push = function push(event) {
  marks?.push([`push:${event.type}`, ticks]);
  return originalPush.call(this, event);
};
let reads = 0;
const originalRead = ReadableStreamDefaultReader.prototype.read;
ReadableStreamDefaultReader.prototype.read = function read(...args) {
  const promise = originalRead.apply(this, args);
  if (marks) {
    if (reads++ === 0) { ticks = 0; spinning = true; queueMicrotask(spin); }
    marks.push([`read:${reads}`, ticks]);
  }
  return promise;
};

async function run(name, { chunks, end }) {
  marks = [];
  reads = 0;
  ticks = 0; // The provider's start push precedes the first body read; the counter starts at the first read.
  let index = 0;
  const body = new ReadableStream({
    pull(controller) {
      if (index < chunks.length) controller.enqueue(new Uint8Array(chunks[index++]));
      else if (end === 'error') controller.error(new Error('body failed'));
      else controller.close();
    },
  });
  const fetch = async () => new Response(body, { status: 200, headers: { 'content-type': 'text/event-stream' } });
  const response = mistralStream(model, { messages: [{ role: 'user', content: 'x', timestamp: 1 }] }, { apiKey: 'k', fetch });
  for await (const event of response) marks.push([`deliver:${event.type}`, ticks]);
  spinning = false;
  const result = await response.result();
  const out = { name, chunks: chunks.map(c => Buffer.from(c).toString('base64')), end: end ?? 'close', marks, stopReason: result.stopReason, errorMessage: result.errorMessage ?? null, result: JSON.parse(JSON.stringify(result, (key, value) => (key === 'timestamp' ? undefined : value))) };
  marks = null;
  await new Promise(resolve => setTimeout(resolve, 5));
  return out;
}

const out = [];
for (const [name, scenario] of Object.entries(scenarios)) out.push(await run(name, scenario));
await writeFile(process.argv[2], JSON.stringify({ piVersion: '0.99.2', node: process.version, scenarios: out }, null, 2) + '\n');
