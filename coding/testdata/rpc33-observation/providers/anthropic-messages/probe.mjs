// Oracle for the anthropic-messages row of the D82 multi-provider observation matrix.
// Pins Pi 1.0.0 and drives its real pipeline (packages/ai/src/api/anthropic-messages.ts, utils/event-stream.ts,
// api/lazy.ts, coding-agent/src/core/model-runtime.ts) against a loopback server.
//
// Two measurements per fixture:
//   1. "trace": every EventStream.push (producer) and every consumer delivery, each stamped with (epoch, tick).
//      A tick is one microtask round of a self-rescheduling Promise chain that restarts at every macrotask
//      that delivers client-socket bytes ("epoch"). Equal (epoch, tick) plus the sequence number totally orders
//      the microtask interleaving. Each record carries the message state visible at that instant.
//   2. "rpc" (with --rpc <pi cli>): the first assistant message_start serialized by `pi --mode rpc`.
//
// Usage: node probe.mjs <out.json> [inputs.json]        (needs PI_PACKAGE_ROOT)
//        node probe.mjs --rpc <cli.js|pig-binary> <out.json>   (adds the "rpc" section)
import { createServer } from 'node:http';
import { createHook } from 'node:async_hooks';
import { once } from 'node:events';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { writeFile, readFile, mkdtemp, mkdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createInterface } from 'node:readline';

const root = process.env.PI_PACKAGE_ROOT;
const base = root + '/node_modules/@earendil-works/';
for (const [path, version] of [[root, '1.0.0'], [base + 'pi-ai', '1.0.0'], [base + 'pi-agent-core', '1.0.0']]) {
  assert.equal(JSON.parse(await readFile(path + '/package.json', 'utf8')).version, version);
}
const anthropicVersion = JSON.parse(await readFile(root + '/node_modules/@anthropic-ai/sdk/package.json', 'utf8')).version;

const rpcIndex = process.argv.indexOf('--rpc');
const rpcBinary = rpcIndex >= 0 ? process.argv[rpcIndex + 1] : undefined;
const args = process.argv.filter((_, i) => i > 1 && !(rpcIndex >= 0 && (i === rpcIndex || i === rpcIndex + 1)));
const [outPath, inputsPath] = args;

const { EventStream } = await import(base + 'pi-ai/dist/utils/event-stream.js');
const { stream: anthropicStream } = await import(base + 'pi-ai/dist/api/anthropic-messages.js');
const { lazyStream } = await import(base + 'pi-ai/dist/api/lazy.js');
const { ModelRuntime } = await import(root + '/dist/core/model-runtime.js');

const readArgs = '{"path":"parity-read-target.txt"}';
// Bodies are binary strings: one char per byte, so chunk boundaries can fall inside a UTF-8 sequence.
const binary = text => Buffer.from(text, 'utf8').toString('latin1');
const sse = (events, eol = '\n') => events.map(e => e.bytes ?? binary(e.raw ?? `event: ${e.type}${eol}data: ${JSON.stringify(e)}${eol}${eol}`)).join('');
const messageStart = (id, usage) => ({ type: 'message_start', message: { id, type: 'message', role: 'assistant', model: 'probe', content: [], stop_reason: null, usage } });
const stop = { type: 'message_stop' };
const records = {
  // The RPC33 shape: a tool call whose complete argument arrives in one delta.
  tool: [
    messageStart('msg_1', { input_tokens: 10, output_tokens: 1 }),
    { type: 'content_block_start', index: 0, content_block: { type: 'tool_use', id: 'toolu_1', name: 'read', input: {} } },
    { type: 'content_block_delta', index: 0, delta: { type: 'input_json_delta', partial_json: readArgs } },
    { type: 'content_block_stop', index: 0 },
    { type: 'message_delta', delta: { stop_reason: 'tool_use' }, usage: { output_tokens: 3 } },
    stop,
  ],
  text: [
    messageStart('msg_2', { input_tokens: 10, output_tokens: 1 }),
    { type: 'content_block_start', index: 0, content_block: { type: 'text', text: '' } },
    { type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text: 'one' } },
    { type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text: ' two' } },
    { type: 'content_block_stop', index: 0 },
    { type: 'message_delta', delta: { stop_reason: 'end_turn' }, usage: { output_tokens: 3 } },
    stop,
  ],
  thinking: [
    messageStart('msg_3', { input_tokens: 10, output_tokens: 1 }),
    { type: 'content_block_start', index: 0, content_block: { type: 'thinking', thinking: '', signature: '' } },
    { type: 'content_block_delta', index: 0, delta: { type: 'thinking_delta', thinking: 'hm' } },
    { type: 'content_block_delta', index: 0, delta: { type: 'signature_delta', signature: 'sig' } },
    { type: 'content_block_stop', index: 0 },
    { type: 'content_block_start', index: 1, content_block: { type: 'text', text: '' } },
    { type: 'content_block_delta', index: 1, delta: { type: 'text_delta', text: 'ok' } },
    { type: 'content_block_stop', index: 1 },
    { type: 'message_delta', delta: { stop_reason: 'end_turn' }, usage: { output_tokens: 4 } },
    stop,
  ],
};
const textStart = { type: 'content_block_start', index: 0, content_block: { type: 'text', text: '' } };
const textDelta = { type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text: 'one' } };
Object.assign(records, {
  // Comment/ping records that G skips with `continue`.
  ping: [messageStart('msg_4', { input_tokens: 10, output_tokens: 1 }), { type: 'ping' }, textStart, { type: 'ping' }, textDelta, { type: 'content_block_stop', index: 0 }, { type: 'message_delta', delta: { stop_reason: 'end_turn' }, usage: { output_tokens: 3 } }, stop],
  // Failures after the stream started.
  'error-event': [messageStart('msg_5', { input_tokens: 10, output_tokens: 1 }), textStart, textDelta, { type: 'error', error: { type: 'overloaded_error', message: 'Overloaded' } }],
  'bad-json': [messageStart('msg_6', { input_tokens: 10, output_tokens: 1 }), textStart, { raw: 'event: content_block_delta\ndata: {bad\n\n' }],
  truncated: [messageStart('msg_7', { input_tokens: 10, output_tokens: 1 }), textStart, textDelta],
  'bad-stop': [messageStart('msg_8', { input_tokens: 10, output_tokens: 1 }), textStart, textDelta, { type: 'message_delta', delta: { stop_reason: 'weird' }, usage: { output_tokens: 3 } }, stop],
  'no-stop': [messageStart('msg_9', { input_tokens: 10, output_tokens: 1 }), textStart, textDelta, { type: 'content_block_stop', index: 0 }, { type: 'message_delta', delta: {}, usage: { output_tokens: 3 } }, stop],
  // A block that never stops: its scratch index survives into the final message.
  unstopped: [messageStart('msg_10', { input_tokens: 10, output_tokens: 1 }), textStart, textDelta, { type: 'message_delta', delta: { stop_reason: 'end_turn' }, usage: { output_tokens: 3 } }, stop],
});
// Byte-level decoding: CRLF line endings, a BOM, multi-byte text and an ill-formed byte.
const utf8Delta = text => ({ type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text } });
records.crlf = records.text;
records.utf8 = [
  messageStart('msg_11', { input_tokens: 10, output_tokens: 1 }), textStart,
  utf8Delta('h\u00e9llo \u2713 \ud83d\ude00 end'),
  { bytes: 'event: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x\xffy\xe2\x82"}}\n\n' },
  { type: 'content_block_stop', index: 0 }, { type: 'message_delta', delta: { stop_reason: 'end_turn' }, usage: { output_tokens: 3 } }, stop,
];
records.bom = records.text;
const bodyOf = shape => shape === 'crlf' ? sse(records[shape], '\r\n') : shape === 'bom' ? '\xef\xbb\xbf' + sse(records[shape]) : sse(records[shape]);
// Text of one plain reply used for the second request of an RPC turn.
const replyBody = sse(records.text);

// Delivery of the response body. Each entry is written in its own socket write; `wait` runs before the write.
// The first entry rides with the response headers unless it is the empty string.
const deliveries = {
  // Headers and the complete body in one write with content-length (the RPC33 fixture).
  buffered: body => ({ headers: 'length', chunks: [{ wait: 0, data: body }] }),
  // Headers alone; the complete body follows in one later write.
  pending: body => ({ headers: 'flush', chunks: [{ wait: 30, data: body }] }),
  // Headers plus the first two records; the rest follows later.
  'buffered-then-pending': body => {
    const cut = nthRecordEnd(body, 2);
    return { headers: 'chunked', chunks: [{ wait: 0, data: body.slice(0, cut) }, { wait: 30, data: body.slice(cut) }] };
  },
  // Headers alone; then one write per SSE record.
  'pending-per-record': body => ({ headers: 'flush', chunks: splitRecords(body).map(data => ({ wait: 15, data })) }),
  // Headers plus a first write that ends right after a carriage return: Pi's consumeLine ends a line there.
  'buffered-cr-split': body => {
    const cut = Math.max(body.indexOf('\r') + 1, nthRecordEnd(body, 1) - 2);
    return { headers: 'chunked', chunks: [{ wait: 0, data: body.slice(0, cut) }, { wait: 30, data: body.slice(cut) }] };
  },
  // Headers plus a first write that ends inside a multi-byte character (or, without one, inside a line).
  'buffered-byte-split': body => {
    const wide = [...body].findIndex(c => c.charCodeAt(0) >= 0x80);
    const cut = wide >= 0 ? wide + 2 : nthRecordEnd(body, 1) + 3;
    return { headers: 'chunked', chunks: [{ wait: 0, data: body.slice(0, cut) }, { wait: 30, data: body.slice(cut) }] };
  },
  // Headers plus a first write that stops in the middle of a data line.
  'buffered-mid-line': body => {
    const cut = nthRecordEnd(body, 1) + 20;
    return { headers: 'chunked', chunks: [{ wait: 0, data: body.slice(0, cut) }, { wait: 30, data: body.slice(cut) }] };
  },
};
function splitRecords(body) { return body.match(/[\s\S]*?(?:\r\n\r\n|\n\n)/g) ?? [body]; }
function nthRecordEnd(body, n) { return splitRecords(body).slice(0, n).join('').length; }

const shapes = Object.keys(records);
// The consumer aborts when it receives `start`; the server never sends the body.
const abortDeliveries = { held: () => ({ headers: 'flush', hold: true, chunks: [] }) };
const layerAxis = [0, 1, 2, 'runtime'];
const coreShapes = ['tool', 'text', 'thinking'];
const layersFor = shape => coreShapes.includes(shape) ? layerAxis : [0, 2];
const axes = { shapes, deliveries: Object.keys(deliveries), layers: layerAxis, layersFor: Object.fromEntries(shapes.map(shape => [shape, layersFor(shape)])), abort: { deliveries: Object.keys(abortDeliveries), shape: 'text' } };

const state = { port: 0, epoch: 0, tick: 0, seq: 0, generation: 0, log: undefined, nextStreamId: 0 };
const TICK_LIMIT = 600;
function newEpoch(reason, bytes) {
  state.epoch++;
  state.tick = 0;
  const generation = ++state.generation;
  if (state.log) state.log.push(stamp({ side: 'socket', reason, bytes }));
  Promise.resolve().then(function step() {
    if (generation !== state.generation) return;
    state.tick++;
    if (state.tick < TICK_LIMIT) Promise.resolve().then(step);
  });
}
function stamp(record) { return { seq: state.seq++, epoch: state.epoch, tick: state.tick, ...record }; }
// Clocks are the only nondeterministic field; the oracle pins them to 0.
const copy = value => value === undefined ? undefined : JSON.parse(JSON.stringify(value, (key, v) => key === 'timestamp' && typeof v === 'number' ? 0 : v));

// Every macrotask callback (timer, immediate, I/O completion) starts a new epoch. Promise jobs and
// process.nextTick callbacks do not: they belong to the macrotask that scheduled them.
const resourceTypes = new Map();
const microtaskTypes = new Set(['PROMISE', 'TickObject', 'Microtask']);
createHook({
  init(id, type) { if (!microtaskTypes.has(type)) resourceTypes.set(id, type); },
  before(id) { const type = resourceTypes.get(id); if (type && state.log) newEpoch(type); },
  destroy(id) { resourceTypes.delete(id); },
}).enable();
const originalPush = EventStream.prototype.push;
EventStream.prototype.push = function (event) {
  if (state.log) {
    this.probeId ??= ++state.nextStreamId;
    state.log.push(stamp({ side: 'push', stream: this.probeId, type: event.type, message: message(event) }));
  }
  return originalPush.call(this, event);
};
function message(event) {
  return copy(event.partial ?? event.message ?? event.error);
}

function serverFor(delivery, body) {
  let requests = 0;
  const server = createServer(async (req, res) => {
    for await (const _ of req) { /* drain */ }
    requests++;
    const plan = requests === 1 ? (deliveries[delivery] ?? abortDeliveries[delivery])(body) : deliveries.buffered(replyBody);
    const total = plan.chunks.reduce((sum, c) => sum + c.data.length, 0);
    const headers = { 'content-type': 'text/event-stream' };
    if (plan.headers === 'length') headers['content-length'] = total;
    res.writeHead(200, headers);
    if (plan.headers === 'flush') res.flushHeaders();
    if (plan.hold) {
      await new Promise(resolve => res.once('close', resolve));
      return;
    }
    for (let i = 0; i < plan.chunks.length; i++) {
      const chunk = plan.chunks[i];
      if (chunk.wait) await new Promise(resolve => setTimeout(resolve, chunk.wait));
      if (res.destroyed) return;
      const bytes = Buffer.from(chunk.data, 'latin1');
      if (i === plan.chunks.length - 1) res.end(bytes); else res.write(bytes);
    }
  });
  return server;
}

// Renumbers epochs 1.. over the epochs that hold a record (server-side macrotasks are noise) and measures the
// first epoch's ticks from its first record (the provider's start push), which is the only place where the time
// spent in the request and header phases enters the trace.
function normalize(log) {
  const records = log.filter(r => r.side !== 'socket');
  const ordinals = new Map();
  for (const r of records) if (!ordinals.has(r.epoch)) ordinals.set(r.epoch, ordinals.size + 1);
  const anchor = records[0].tick;
  return records.map((r, i) => ({ seq: i, epoch: ordinals.get(r.epoch), tick: ordinals.get(r.epoch) === 1 ? r.tick - anchor : r.tick, side: r.side, ...(r.stream === undefined ? {} : { stream: r.stream }), ...(r.type === undefined ? {} : { type: r.type }), message: r.message }));
}

async function traceCase(shape, delivery, layers, abortAtStart = false) {
  const body = bodyOf(shape);
  const server = serverFor(delivery, body);
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  state.port = server.address().port;
  const model = { id: 'probe', name: 'probe', api: 'anthropic-messages', provider: 'probe-provider', baseUrl: `http://127.0.0.1:${state.port}`, reasoning: false, input: ['text'], contextWindow: 8192, maxTokens: 512, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } };
  const context = { messages: [{ role: 'user', content: [{ type: 'text', text: 'probe' }], timestamp: 1 }] };
  let runtime, runtimeDir;
  if (layers === 'runtime') {
    runtimeDir = await mkdtemp(join(tmpdir(), 'd82-anthropic-runtime-'));
    const modelsPath = join(runtimeDir, 'models.json');
    await writeFile(modelsPath, JSON.stringify({ providers: { 'probe-provider': { apiKey: 'test', baseUrl: model.baseUrl, api: 'anthropic-messages', models: [model] } } }));
    runtime = await ModelRuntime.create({ modelsPath, authPath: join(runtimeDir, 'auth.json'), refreshOnCreate: false });
  }
  const log = state.log = [];
  state.epoch = 0; state.tick = 0; state.seq = 0; state.nextStreamId = 0;
  newEpoch('start');
  const controller = new AbortController();
  let response;
  try {
    if (runtime) {
      response = runtime.streamSimple(model, context, { maxRetries: 0, signal: controller.signal });
    } else {
      let make = () => anthropicStream(model, context, { apiKey: 'test', maxRetries: 0, signal: controller.signal });
      for (let i = 0; i < layers; i++) { const inner = make; make = () => lazyStream(model, async () => inner()); }
      response = make();
    }
    for await (const event of response) {
      log.push(stamp({ side: 'deliver', type: event.type, message: message(event) }));
      if (abortAtStart && event.type === 'start') controller.abort();
    }
    const result = await response.result();
    log.push(stamp({ side: 'result', message: copy(result) }));
  } finally {
    controller.abort();
    server.closeAllConnections();
    await new Promise(resolve => server.close(resolve));
    state.port = 0;
    state.log = undefined;
    if (runtimeDir) await rm(runtimeDir, { recursive: true, force: true });
  }
  return { shape, delivery, layers, ...(abortAtStart ? { abortAtStart } : {}), trace: normalize(log) };
}

async function rpcStart(shape, delivery) {
  const body = bodyOf(shape);
  const server = serverFor(delivery, body);
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  const port = server.address().port;
  const dir = await mkdtemp(join(tmpdir(), 'd82-anthropic-rpc-'));
  const agent = join(dir, 'agent');
  await mkdir(agent);
  await writeFile(join(agent, 'models.json'), JSON.stringify({ providers: { p: { api: 'anthropic-messages', baseUrl: `http://127.0.0.1:${port}`, apiKey: 'k', models: [{ id: 'probe', name: 'probe', reasoning: false, input: ['text'], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 1000 }] } } }));
  await writeFile(join(dir, 'parity-read-target.txt'), 'x\n');
  const isNodeScript = rpcBinary.endsWith('.js');
  const child = spawn(isNodeScript ? process.execPath : rpcBinary, [...(isNodeScript ? [rpcBinary] : []), '--mode', 'rpc', '--offline', '--no-extensions', '--model', 'p/probe', '--no-context-files', '--no-skills', '--session-dir', join(dir, 's')], { cwd: dir, env: { ...process.env, HOME: dir, PI_CODING_AGENT_DIR: agent, PIG_CODING_AGENT_DIR: agent, PIG_HOME: join(dir, 'pighome') }, stdio: ['pipe', 'pipe', 'inherit'] });
  const starts = [];
  await new Promise(resolve => {
    createInterface({ input: child.stdout }).on('line', line => {
      let event;
      try { event = JSON.parse(line); } catch { return; }
      if (event.type === 'message_start' && event.message?.role === 'assistant') starts.push(event.message);
      if (event.type === 'agent_settled') resolve();
    });
    child.stdin.write(JSON.stringify({ id: 'read', type: 'prompt', message: 'READ' }) + '\n');
    setTimeout(resolve, 20000);
  });
  child.kill();
  server.closeAllConnections();
  await new Promise(resolve => server.close(resolve));
  await rm(dir, { recursive: true, force: true });
  assert.ok(starts[0], 'no assistant message_start');
  return { shape, delivery, start: copy(starts[0]) };
}

// A chunk is stored as text when its bytes are valid UTF-8, otherwise as base64.
function persistPlan(plan) {
  return { ...plan, chunks: plan.chunks.map(({ wait, data }) => {
    const bytes = Buffer.from(data, 'latin1');
    const text = bytes.toString('utf8');
    return Buffer.from(text, 'utf8').equals(bytes) ? { wait, data: text } : { wait, base64: bytes.toString('base64') };
  }) };
}
if (inputsPath) {
  await writeFile(inputsPath, JSON.stringify({ piVersion: '1.0.0', anthropicSdkVersion: anthropicVersion, nodeVersion: process.version, axes, bodies: Object.fromEntries(shapes.map(shape => [shape, Buffer.from(bodyOf(shape), 'latin1').toString('utf8')])), replyBody: Buffer.from(replyBody, 'latin1').toString('utf8'), plans: Object.fromEntries(shapes.map(shape => [shape, Object.fromEntries(Object.keys(deliveries).map(name => [name, persistPlan(deliveries[name](bodyOf(shape)))]))])) }, null, 2) + '\n');
}
const output = { piVersion: '1.0.0', anthropicSdkVersion: anthropicVersion, nodeVersion: process.version, axes };
output.cases = [];
if (!process.env.PROBE_RPC_ONLY) for (const shape of shapes) for (const layers of layersFor(shape)) for (const delivery of Object.keys(deliveries)) {
  output.cases.push(await traceCase(shape, delivery, layers));
}
if (!process.env.PROBE_RPC_ONLY) for (const layers of layerAxis) for (const delivery of Object.keys(abortDeliveries)) {
  output.cases.push(await traceCase('text', delivery, layers, true));
}
if (rpcBinary) {
  output.rpc = [];
  for (const delivery of Object.keys(deliveries)) output.rpc.push(await rpcStart('tool', delivery));
}
await writeFile(outPath, JSON.stringify(output, null, 2) + '\n');
