// D82 W5 oracle for the pi-messages API (Pi 1.1.0, packages/ai/src/api/pi-messages.ts).
//
// Drives Pi's real pipeline for pi-messages against a loopback backend that serves the
// pi-messages SSE wire protocol under two delivery shapes:
//
//   buffered  headers and the whole body leave in one write, so the first body read finds
//             the bytes already in Node's stream buffer
//   pending   headers are flushed first and the whole body follows in one write after the
//             first body read is already pending
//   chunked   headers first, then one SSE record per write, every read pending
//   split     headers first, then 40-byte pieces, so records span reads
//   tail      headers first, the first two records in one write, the rest in one write later
//
// Fixtures end in a terminal event (`tool`, `mixed`), a terminal error event (`error`), or in end-of-body
// with no terminal event (`truncated`, Pi's "stream ended without a terminal event"), or in a record
// that is not JSON (`malformed`, `notjson`).
//
// Three consumer paths are recorded per fixture and delivery:
//
//   direct    for await over pi-ai's pi-messages stream() (no lazy layer)
//   runtime   for await over ModelRuntime.streamSimple() (lazyStream layers)
//   agent     pi-agent-core runAgentLoop() with its event sink
//
// and the real `pi --mode rpc --no-extensions` binary reports its first turn's assistant
// message_start/message_update/message_end records as `rpc`.
//
// `trace` is the tick order. Each entry is one synchronous observation: `read` is a
// ReadableStreamDefaultReader.read() call, `decode` the resolution of a body read (TextDecoder.decode
// runs synchronously after `await reader.read()`), `parse` is JSON.parse of one SSE record,
// `push` is EventStream.push, `deliver` is the consumer's for-await body, and `state` is the
// assistant-message snapshot the consumer sees at that tick. The instrumentation returns the
// original promises and values unchanged, so it adds no microtask.
//
// usage: node probe.mjs <out.json>     (env PI_PACKAGE_ROOT = the installed pi-coding-agent package)
import { createServer } from 'node:http';
import { once } from 'node:events';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createInterface } from 'node:readline';
import { writeFile, readFile, mkdtemp, mkdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const root = process.env.PI_PACKAGE_ROOT;
const scope = root + '/node_modules/@earendil-works/';
for (const [path, version] of [[root, '1.1.0'], [scope + 'pi-ai', '1.1.0'], [scope + 'pi-agent-core', '1.1.0']]) {
  assert.equal(JSON.parse(await readFile(path + '/package.json', 'utf8')).version, version);
}
const outPath = process.argv[2];
assert.ok(outPath, 'usage: node probe.mjs <out.json>');

const { runAgentLoop } = await import(scope + 'pi-agent-core/dist/index.js');
const { stream: piMessagesStream } = await import(scope + 'pi-ai/dist/api/pi-messages.js');
const { AssistantMessageEventStream } = await import(scope + 'pi-ai/dist/utils/event-stream.js');
const { ModelRuntime } = await import(root + '/dist/core/model-runtime.js');

const parseJSON = JSON.parse.bind(JSON);
const args = '{"path":"parity-read-target.txt"}';
const toolCall = { type: 'toolCall', id: 'strictread1', name: 'read', arguments: { path: 'parity-read-target.txt' } };
const usage = { input: 10, output: 3, cacheRead: 0, cacheWrite: 0, totalTokens: 13, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } };

// fixtures: name -> ordered wire events (PiMessagesEvent).
const fixtures = {
  // The RPC33 shape: one tool call, arguments in one delta.
  tool: [
    { type: 'start' },
    { type: 'toolcall_start', contentIndex: 0, id: 'strictread1', toolName: 'read' },
    { type: 'toolcall_delta', contentIndex: 0, delta: args },
    { type: 'toolcall_end', contentIndex: 0, toolCall },
    { type: 'done', reason: 'toolUse', usage, responseId: 'msg-strict' },
  ],
  // Every content kind, deltas that split an argument JSON document, terminal usage.
  mixed: [
    { type: 'start' },
    { type: 'thinking_start', contentIndex: 0 },
    { type: 'thinking_delta', contentIndex: 0, delta: 'one' },
    { type: 'thinking_delta', contentIndex: 0, delta: ' two' },
    { type: 'thinking_end', contentIndex: 0, content: 'one two', contentSignature: 'sig' },
    { type: 'text_start', contentIndex: 1 },
    { type: 'text_delta', contentIndex: 1, delta: 'ans' },
    { type: 'text_delta', contentIndex: 1, delta: 'wer' },
    { type: 'text_end', contentIndex: 1, content: 'answer' },
    { type: 'toolcall_start', contentIndex: 2, id: 'strictread1', toolName: 'read' },
    { type: 'toolcall_delta', contentIndex: 2, delta: '{"path":"parity-' },
    { type: 'toolcall_delta', contentIndex: 2, delta: 'read-target.txt"}' },
    { type: 'toolcall_end', contentIndex: 2, toolCall },
    { type: 'done', reason: 'toolUse', usage, responseId: 'msg-strict' },
  ],
};
fixtures.error = [
  { type: 'start' },
  { type: 'text_start', contentIndex: 0 },
  { type: 'text_delta', contentIndex: 0, delta: 'par' },
  { type: 'error', reason: 'error', usage, errorMessage: 'backend failed', responseId: 'msg-error' },
];
fixtures.truncated = [
  { type: 'start' },
  { type: 'text_start', contentIndex: 0 },
  { type: 'text_delta', contentIndex: 0, delta: 'par' },
];
// A record whose JSON.parse throws inside readPiMessagesEvents: Pi reports V8's SyntaxError message.
fixtures.malformed = [
  { type: 'start' },
  { type: 'text_start', contentIndex: 0 },
  { raw: '{"type":"text_delta","contentIndex":0,"delta":"x"' },
];
fixtures.notjson = [
  { type: 'start' },
  { type: 'text_start', contentIndex: 0 },
  { raw: 'oops' },
];
const terminal = { tool: 'toolUse', mixed: 'toolUse', error: 'error', truncated: 'error', malformed: 'error', notjson: 'error' };
const reply = [
  { type: 'start' },
  { type: 'text_start', contentIndex: 0 },
  { type: 'text_delta', contentIndex: 0, delta: 'ok' },
  { type: 'text_end', contentIndex: 0, content: 'ok' },
  { type: 'done', reason: 'stop', usage, responseId: 'msg-reply' },
];
const deliveries = ['buffered', 'pending', 'chunked', 'split', 'tail'];
const sse = event => `data: ${event.raw ?? JSON.stringify(event)}\n\n`;
const wire = events => events.map(sse);
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));

// Serves POST /messages. The first request of a pair gets the fixture; the second (the agent's
// follow-up after the tool result) gets `reply`, so an RPC run settles.
async function serve(records, delivery) {
  let count = 0;
  const server = createServer(async (req, res) => {
    for await (const _ of req) {} // request fully read before the response starts
    count++;
    const chunks = wire(count % 2 === 1 ? records : reply);
    if (delivery === 'buffered') {
      const body = Buffer.from(chunks.join(''));
      res.writeHead(200, { 'content-type': 'text/event-stream', 'content-length': body.length });
      res.end(body);
      return;
    }
    res.writeHead(200, { 'content-type': 'text/event-stream' });
    res.flushHeaders();
    await delay(150);
    if (res.destroyed) return;
    if (delivery === 'pending') {
      res.end(chunks.join(''));
      return;
    }
    let pieces = chunks;
    if (delivery === 'split') {
      const all = Buffer.from(chunks.join(''));
      pieces = [];
      for (let i = 0; i < all.length; i += 40) pieces.push(all.subarray(i, i + 40));
    } else if (delivery === 'tail') {
      pieces = [chunks.slice(0, 2).join(''), chunks.slice(2).join('')];
    }
    for (const piece of pieces) {
      if (res.destroyed) return;
      res.write(piece);
      await delay(delivery === 'tail' ? 150 : 25);
    }
    res.end();
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  return { server, port: server.address().port };
}

const model = port => ({
  id: 'strict', name: 'strict', api: 'pi-messages', provider: 'p', baseUrl: `http://127.0.0.1:${port}`,
  reasoning: false, input: ['text'], contextWindow: 128000, maxTokens: 1000,
  cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
});

// snapshot serializes a message the way an RPC listener would and drops the wall-clock fields.
function snapshot(value) {
  return parseJSON(JSON.stringify(value, (key, v) => (key === 'timestamp' ? undefined : v)));
}

// Non-intrusive instrumentation: every hook returns the original value.
let trace = null;
const label = new WeakMap();
let streams = 0;
const streamId = stream => { if (!label.has(stream)) label.set(stream, streams++); return label.get(stream); };
const originalPush = AssistantMessageEventStream.prototype.push;
AssistantMessageEventStream.prototype.push = function push(event) {
  trace?.push(`push:s${streamId(this)}:${event.type}`);
  return originalPush.call(this, event);
};
const originalRead = ReadableStreamDefaultReader.prototype.read;
ReadableStreamDefaultReader.prototype.read = function read(...a) {
  trace?.push('read');
  return originalRead.apply(this, a);
};
const originalDecode = TextDecoder.prototype.decode;
TextDecoder.prototype.decode = function decode(value, options) {
  if (trace) trace.push(value === undefined ? 'decode:end' : `decode:${value.byteLength}`);
  return originalDecode.call(this, value, options);
};
JSON.parse = function parse(text, reviver) {
  if (trace && typeof text === 'string') {
    const m = /^\{"type":"(\w+)"/.exec(text);
    if (m) trace.push(`parse:${m[1]}`);
  }
  return parseJSON(text, reviver);
};

// abort: undefined, 'timer' (60ms after the request, before the pending body arrives) or 'start' (in the consumer, on the start event).
async function runPath(path, fixture, records, delivery, abort) {
  const { server, port } = await serve(records, delivery);
  const m = model(port);
  const message = { role: 'user', content: [{ type: 'text', text: 'probe' }], timestamp: 1 };
  const controller = new AbortController();
  const states = [];
  let runtimeDir;
  trace = [];
  try {
    if (path === 'agent') {
      await runAgentLoop([message], { messages: [], tools: [] }, { model: m, convertToLlm: x => x, finishTurn: () => ({ action: 'end' }) }, async event => {
        trace.push(`deliver:${event.type}`);
        if (event.type.startsWith('message_') && event.message.role === 'assistant') states.push({ at: trace.length, event: event.type, message: snapshot(event.message) });
      }, controller.signal, (model, context, options) => piMessagesStream(model, context, { ...options, apiKey: 'k', maxRetries: 0, signal: controller.signal }));
    } else {
      let response;
      if (path === 'runtime') {
        runtimeDir = await mkdtemp(join(tmpdir(), 'pi-messages-runtime-'));
        const modelsPath = join(runtimeDir, 'models.json');
        await writeFile(modelsPath, JSON.stringify({ providers: { p: { api: 'pi-messages', baseUrl: m.baseUrl, apiKey: 'k', models: [m] } } }));
        const runtime = await ModelRuntime.create({ modelsPath, authPath: join(runtimeDir, 'auth.json'), refreshOnCreate: false });
        response = runtime.streamSimple(m, { messages: [message] }, { maxRetries: 0, signal: controller.signal });
      } else {
        response = piMessagesStream(m, { messages: [message] }, { apiKey: 'k', maxRetries: 0, signal: controller.signal });
      }
      if (abort === 'timer') setTimeout(() => controller.abort(), 60);
      for await (const event of response) {
        trace.push(`deliver:${event.type}`);
        if (abort === 'start' && event.type === 'start') controller.abort();
        states.push({ at: trace.length, event: event.type, message: snapshot(event.partial ?? event.message ?? event.error) });
      }
      const result = await response.result();
      assert.equal(result.stopReason, abort ? 'aborted' : terminal[fixture]);
    }
    // The trailing end-of-stream reads/decodes settle after the consumer loop ends.
    await delay(20);
    return { path, trace: trace.splice(0), states };
  } finally {
    trace = null;
    controller.abort();
    server.closeAllConnections();
    await new Promise(resolve => server.close(resolve));
    if (runtimeDir) await rm(runtimeDir, { recursive: true, force: true });
  }
}

// The real Pi binary in RPC mode: the serialized assistant messages of the first LLM turn.
async function runRPC(records, delivery) {
  const { server, port } = await serve(records, delivery);
  const dir = await mkdtemp(join(tmpdir(), 'pi-messages-rpc-'));
  const agent = join(dir, 'agent');
  await mkdir(agent);
  await writeFile(join(agent, 'models.json'), JSON.stringify({ providers: { p: { api: 'pi-messages', baseUrl: model(port).baseUrl, apiKey: 'k', models: [{ id: 'strict', name: 'strict', reasoning: false, input: ['text'], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 1000 }] } } }));
  await writeFile(join(dir, 'parity-read-target.txt'), 'x\n');
  const pi = spawn(process.execPath, [join(root, 'dist/bundle/cli.js'), '--mode', 'rpc', '--offline', '--no-extensions', '--model', 'p/strict', '--no-context-files', '--no-skills', '--session-dir', join(dir, 's')], { cwd: dir, env: { ...process.env, HOME: dir, PI_CODING_AGENT_DIR: agent }, stdio: ['pipe', 'pipe', 'inherit'] });
  const records1 = [];
  let assistantEnds = 0;
  await new Promise(resolve => {
    createInterface({ input: pi.stdout }).on('line', line => {
      let e;
      try { e = parseJSON(line); } catch { return; }
      // toJsonEvent (coding-agent/src/modes/json-event.ts) drops the message from message_update
      // and keeps its cumulative usage; message_start/message_end carry the message.
      if (assistantEnds === 0 && e.type === 'message_update') records1.push({ type: e.type, usage: e.usage, assistantMessageEvent: e.assistantMessageEvent });
      if (assistantEnds === 0 && e.message?.role === 'assistant' && e.type !== 'message_update' && e.type.startsWith('message_')) {
        records1.push({ type: e.type, message: snapshot(e.message) });
        if (e.type === 'message_end') assistantEnds++;
      }
      if (e.type === 'agent_settled') resolve();
    });
    pi.stdin.write(JSON.stringify({ id: 'read', type: 'prompt', message: 'READ' }) + '\n');
    setTimeout(resolve, 20000);
  });
  pi.kill();
  server.closeAllConnections();
  await new Promise(resolve => server.close(resolve));
  await rm(dir, { recursive: true, force: true });
  assert.ok(records1.length > 0 && records1.at(-1).type === 'message_end', 'no completed first assistant turn');
  return records1;
}

const cases = [];
for (const [fixture, records] of Object.entries(fixtures)) {
  for (const delivery of deliveries) {
    const entry = { fixture, delivery, paths: [], rpc: undefined };
    for (const path of ['direct', 'runtime', 'agent']) entry.paths.push(await runPath(path, fixture, records, delivery));
    entry.rpc = await runRPC(records, delivery);
    cases.push(entry);
    console.log(JSON.stringify({ fixture, delivery, start: entry.rpc[0].message, tracePaths: entry.paths.map(p => `${p.path}:${p.trace.length}`) }));
  }
}
// Cancellation, direct path: the abort reaches a read that is pending (headers seen, no body yet) or one issued after the start event.
const cancels = [];
for (const [name, fixture, delivery, abort] of [['pending-body', 'tool', 'pending', 'timer'], ['after-start', 'tool', 'chunked', 'start']]) {
  cancels.push({ name, fixture, delivery, abort, ...(await runPath('direct', fixture, fixtures[fixture], delivery, abort)) });
}
await writeFile(outPath, JSON.stringify({
  piVersion: '1.1.0',
  node: process.version,
  api: 'pi-messages',
  bodies: Object.fromEntries(Object.entries(fixtures).map(([name, records]) => [name, wire(records).join('')])),
  reply: wire(reply).join(''),
  cases,
  cancels,
}, null, 2) + '\n');
