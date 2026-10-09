// D82 W5 tick oracle for pi-messages (Pi 1.1.0, packages/ai/src/api/pi-messages.ts).
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
assert.equal(JSON.parse(await readFile(scope + 'pi-ai/package.json', 'utf8')).version, '1.1.0');
const { stream: piMessagesStream } = await import(scope + 'pi-ai/dist/api/pi-messages.js');
const { AssistantMessageEventStream } = await import(scope + 'pi-ai/dist/utils/event-stream.js');

const usage = { input: 1, output: 2, cacheRead: 0, cacheWrite: 0, totalTokens: 3, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } };
const rec = event => `data: ${JSON.stringify(event)}\n\n`;
const tool = { type: 'toolCall', id: 'call-1', name: 'read', arguments: { path: 'a.txt' } };
const records = [
  { type: 'start' },
  { type: 'text_start', contentIndex: 0 },
  { type: 'text_delta', contentIndex: 0, delta: 'héllo ✓ 😀' },
  { type: 'text_end', contentIndex: 0, content: 'héllo ✓ 😀' },
  { type: 'toolcall_start', contentIndex: 1, id: 'call-1', toolName: 'read' },
  { type: 'toolcall_delta', contentIndex: 1, delta: '{"path":"a.txt"}' },
  { type: 'toolcall_end', contentIndex: 1, toolCall: tool },
  { type: 'done', reason: 'toolUse', usage, responseId: 'r1' },
];
const enc = text => Buffer.from(text);
const all = enc(records.map(rec).join(''));
const split = (buffer, size) => { const out = []; for (let i = 0; i < buffer.length; i += size) out.push(buffer.subarray(i, i + size)); return out; };
const wireOf = list => list.map(rec).join('');
const emoji = all.indexOf(Buffer.from('😀'));

// name -> { chunks: Buffer[], end: 'close' | 'error' }
const scenarios = {
  oneChunk: { chunks: [all] },
  perRecord: { chunks: records.map(r => enc(rec(r))) },
  bytes7: { chunks: split(all, 7) },
  splitInsideEmoji: { chunks: [all.subarray(0, emoji + 2), all.subarray(emoji + 2)] },
  crlf: { chunks: [enc(wireOf(records).replace(/\n/g, '\r\n'))] },
  crlfSplit: { chunks: (() => { const text = enc(wireOf(records.slice(0, 3)).replace(/\n/g, '\r\n')); const cut = text.indexOf('\r\n\r\n') + 1; return [text.subarray(0, cut), text.subarray(cut)]; })() },
  bom: { chunks: [Buffer.concat([Buffer.from([0xef, 0xbb, 0xbf]), all])] },
  bomSplit: { chunks: [all.subarray(0, 0), Buffer.concat([Buffer.from([0xef, 0xbb]), Buffer.alloc(0)]), Buffer.concat([Buffer.from([0xbf]), all])] },
  invalidUTF8: { chunks: [enc(rec(records[0]) + rec(records[1])), Buffer.concat([enc('data: {"type":"text_delta","contentIndex":0,"delta":"a'), Buffer.from([0xff, 0xe2, 0x82]), enc('b"}\n\n')]), enc(rec(records[7]))] },
  truncatedUTF8: { chunks: [enc(rec(records[0]) + rec(records[1])), enc('data: {"type":"text_delta","contentIndex":0,"delta":"x"}\n\n'), Buffer.from([0xe2, 0x82])] },
  truncatedRecordUTF8: { chunks: [enc(wireOf(records.slice(0, 2)) + 'data: {"type":"text_delta","delta":"a'), Buffer.from([0xe2, 0x82])] },
  errorTerminal: { chunks: [enc(wireOf([records[0], records[1], { type: 'error', reason: 'error', usage, errorMessage: 'backend failed', responseId: 'r2' }]))] },
  noTerminal: { chunks: [enc(wireOf(records.slice(0, 3)))] },
  trailingRecord: { chunks: [enc(wireOf(records.slice(0, 2)) + rec(records[7]).slice(0, -2))] },
  malformed: { chunks: [enc(wireOf(records.slice(0, 2)) + 'data: {"type":"text_delta"\n\n')] },
  notJSON: { chunks: [enc(wireOf(records.slice(0, 2)) + 'data: oops\n\n')] },
  falsyRecords: { chunks: [enc(rec(records[0]) + 'data: null\n\ndata: 0\n\ndata: false\n\ndata: ""\n\ndata: [DONE]\n\ndata:\n\n: comment\n\n' + rec(records[1]) + rec(records[7]))] },
  eventLines: { chunks: [enc('event: x\nid: 1\ndata: {"type":"start"}\ndata: {"type":"done","reason":"stop","usage":' + JSON.stringify(usage) + '}\n\n' + rec(records[7]))] },
  readRejects: { chunks: [enc(wireOf(records.slice(0, 3)))], end: 'error' },
  rejectsFirst: { chunks: [], end: 'error' },
  emptyBody: { chunks: [] },
  doneWithoutStart: { chunks: [enc(rec(records[7]))] },
};

const model = { id: 'strict', name: 'strict', api: 'pi-messages', provider: 'p', baseUrl: 'http://127.0.0.1:1', reasoning: false, input: ['text'], contextWindow: 1, maxTokens: 1, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } };
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
  let index = 0;
  const body = new ReadableStream({
    pull(controller) {
      if (index < chunks.length) controller.enqueue(new Uint8Array(chunks[index++]));
      else if (end === 'error') controller.error(new Error('body failed'));
      else controller.close();
    },
  });
  const fetch = async () => new Response(body, { status: 200, headers: { 'content-type': 'text/event-stream' } });
  const response = piMessagesStream(model, { messages: [{ role: 'user', content: 'x', timestamp: 1 }] }, { apiKey: 'k', fetch });
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
await writeFile(process.argv[2], JSON.stringify({ piVersion: '1.1.0', node: process.version, scenarios: out }, null, 2) + '\n');
