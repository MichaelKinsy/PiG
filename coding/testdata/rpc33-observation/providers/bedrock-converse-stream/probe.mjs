// Pi 1.0.1 / AWS SDK client-bedrock-runtime 3.1127.0: exact partial-message observation and microtask order for the bedrock-converse-stream provider.
//
// Usage: node probe.mjs <pi.json> [inputs.json]
//   env PI_PACKAGE_ROOT  installed @earendil-works/pi-coding-agent 1.0.1 (its dependency tree provides pi-ai, pi-agent-core and the AWS SDK)
//   env PROBE_TICKS=0    disable the tick counter; the run must then produce the same records once tick fields are removed (this proves the counter does not perturb order)
//   env PROBE_SKIP_RPC=1 skip the real `pi --mode rpc` observations
//
// Tick model (same as the google-generative-ai oracle). A tick is one microtask job (a Promise reaction or a queueMicrotask callback), counted by an async_hooks `before` hook.
// The hook only increments a counter, so it neither creates Promises nor reorders jobs.
// An epoch is one macrotask that delivers bytes: an HTTP/1 client socket 'data' event, or an HTTP/2 stream 'response' event or body push (data or end).
// The tick resets at each epoch. Two triggers with no job between them belong to one epoch, so an HTTP/2 headers-and-data read is one epoch.
// A record made at (epoch, tick) ran after `tick` microtask jobs of that epoch. Epoch 0 is everything before the first byte arrives.
//
// Bedrock specifics. `http` is the transport: "h2" is Pi's default (NodeHttp2Handler over cleartext prior-knowledge HTTP/2 to the loopback server),
// "h1" is `AWS_BEDROCK_FORCE_HTTP1=1` (NodeHttpHandler). The SDK's ConverseStream deserializer reads the first event inside `client.send`,
// so a `pending` delivery still writes the response headers together with the first frame; a headers-only response would never reach `start`.
import { fork, spawn } from 'node:child_process';
import net from 'node:net';
import { createHook } from 'node:async_hooks';
import { EventEmitter } from 'node:events';
import { Readable } from 'node:stream';
import { crc32 } from 'node:zlib';
import assert from 'node:assert/strict';
import { writeFile, readFile, mkdtemp, rm, mkdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createInterface } from 'node:readline';

const root = process.env.PI_PACKAGE_ROOT;
const base = root + '/node_modules/@earendil-works/';
const versions = {piVersion: '1.0.1', awsSdkClientBedrockRuntime: '3.1127.0', smithyCore: '3.35.1', smithyNodeHttpHandler: '4.12.1'};
for (const [path, version] of [[root + '/package.json', '1.0.1'], [base + 'pi-ai/package.json', '1.0.1'], [base + 'pi-agent-core/package.json', '1.0.1'],
  [root + '/node_modules/@aws-sdk/client-bedrock-runtime/package.json', '3.1127.0'], [root + '/node_modules/@smithy/core/package.json', '3.35.1'], [root + '/node_modules/@smithy/node-http-handler/package.json', '4.12.1']]) {
  assert.equal(JSON.parse(await readFile(path, 'utf8')).version, version, path);
}
const ticksEnabled = process.env.PROBE_TICKS !== '0';
const API = 'bedrock-converse-stream';
const serverPath = new URL('./server.mjs', import.meta.url).pathname;
process.env.AWS_BEDROCK_SKIP_AUTH = '1';
for (const name of ['AWS_PROFILE', 'AWS_REGION', 'AWS_DEFAULT_REGION', 'AWS_BEARER_TOKEN_BEDROCK']) delete process.env[name];

const axes = {
  api: API,
  shapes: ['text', 'thinking', 'tool', 'mixed'],
  // Failure and odd-frame shapes run only through the direct consumer over 0 layers: a modeled and an unmodeled exception, an error frame, a stream without a stop reason, an unknown stop reason, a corrupt frame, an event the SDK drops and an event with an empty payload.
  errorShapes: ['throttling', 'abrupt', 'weirdstop', 'badframe', 'unmodeled', 'errframe', 'unknownevent', 'emptybody'],
  // The same direct/0-layer cases without the `onPayload`/`onResponse` hooks, which change the SDK stack's reaction count.
  noHookShapes: ['text', 'tool'],
  deliveries: ['buffered', 'pending', 'split'],
  https: ['h2', 'h1'],
  consumers: ['direct', 'held-direct', 'result-only', 'cancel', 'agent', 'held-agent'],
  layers: [0, 2, 'runtime'],
};

// application/vnd.amazon.eventstream framing: prelude (total length, headers length, prelude CRC), headers, payload, message CRC.
function header(name, value) {
  const n = Buffer.from(name), v = Buffer.from(value);
  const out = Buffer.alloc(1 + n.length + 1 + 2 + v.length);
  let at = 0;
  out[at++] = n.length; n.copy(out, at); at += n.length;
  out[at++] = 7; out.writeUInt16BE(v.length, at); at += 2; v.copy(out, at);
  return out;
}
function frame(eventType, payload) {
  const headers = Buffer.concat([header(':event-type', eventType), header(':content-type', 'application/json'), header(':message-type', 'event')]);
  const body = Buffer.from(JSON.stringify(payload));
  const total = 12 + headers.length + body.length + 4;
  const out = Buffer.alloc(total);
  out.writeUInt32BE(total, 0);
  out.writeUInt32BE(headers.length, 4);
  out.writeUInt32BE(crc32(out.subarray(0, 8)), 8);
  headers.copy(out, 12);
  body.copy(out, 12 + headers.length);
  out.writeUInt32BE(crc32(out.subarray(0, total - 4)), total - 4);
  return out;
}
function exceptionFrame(type, payload) {
  const headers = Buffer.concat([header(':exception-type', type), header(':content-type', 'application/json'), header(':message-type', 'exception')]);
  const body = Buffer.from(JSON.stringify(payload));
  const total = 12 + headers.length + body.length + 4;
  const out = Buffer.alloc(total);
  out.writeUInt32BE(total, 0);
  out.writeUInt32BE(headers.length, 4);
  out.writeUInt32BE(crc32(out.subarray(0, 8)), 8);
  headers.copy(out, 12);
  body.copy(out, 12 + headers.length);
  out.writeUInt32BE(crc32(out.subarray(0, total - 4)), total - 4);
  return out;
}
function errorFrame(code, message) {
  const headers = Buffer.concat([header(':error-code', code), header(':error-message', message), header(':message-type', 'error')]);
  const total = 12 + headers.length + 4;
  const out = Buffer.alloc(total);
  out.writeUInt32BE(total, 0);
  out.writeUInt32BE(headers.length, 4);
  out.writeUInt32BE(crc32(out.subarray(0, 8)), 8);
  headers.copy(out, 12);
  out.writeUInt32BE(crc32(out.subarray(0, total - 4)), total - 4);
  return out;
}
function bareEvent(eventType, body) {
  const headers = Buffer.concat([header(':event-type', eventType), header(':message-type', 'event')]);
  const total = 12 + headers.length + body.length + 4;
  const out = Buffer.alloc(total);
  out.writeUInt32BE(total, 0);
  out.writeUInt32BE(headers.length, 4);
  out.writeUInt32BE(crc32(out.subarray(0, 8)), 8);
  headers.copy(out, 12);
  Buffer.from(body).copy(out, 12 + headers.length);
  out.writeUInt32BE(crc32(out.subarray(0, total - 4)), total - 4);
  return out;
}
function corrupt(buffer) {
  const out = Buffer.from(buffer);
  out[out.length - 1] ^= 1;
  return out;
}
const usage = {usage: {inputTokens: 10, outputTokens: 3, totalTokens: 13}, metrics: {latencyMs: 1}};
const start = () => frame('messageStart', {role: 'assistant'});
const stop = contentBlockIndex => frame('contentBlockStop', {contentBlockIndex});
const text = (contentBlockIndex, value) => frame('contentBlockDelta', {contentBlockIndex, delta: {text: value}});
const frames = {
  text: [start(), text(0, 'one'), text(0, ' two'), stop(0), frame('messageStop', {stopReason: 'end_turn'}), frame('metadata', usage)],
  thinking: [start(),
    frame('contentBlockDelta', {contentBlockIndex: 0, delta: {reasoningContent: {text: 'one'}}}),
    frame('contentBlockDelta', {contentBlockIndex: 0, delta: {reasoningContent: {text: ' two', signature: 'c2ln'}}}),
    stop(0), text(1, 'answer'), stop(1), frame('messageStop', {stopReason: 'end_turn'}), frame('metadata', usage)],
  // The RPC33 shape: one tool call whose arguments arrive in one delta.
  tool: [start(),
    frame('contentBlockStart', {contentBlockIndex: 0, start: {toolUse: {toolUseId: 'toolu_1', name: 'read'}}}),
    frame('contentBlockDelta', {contentBlockIndex: 0, delta: {toolUse: {input: '{"path":"target.txt"}'}}}),
    stop(0), frame('messageStop', {stopReason: 'tool_use'}), frame('metadata', usage)],
  mixed: [start(), text(0, 'looking'), stop(0),
    frame('contentBlockStart', {contentBlockIndex: 1, start: {toolUse: {toolUseId: 'call-b', name: 'read'}}}),
    frame('contentBlockDelta', {contentBlockIndex: 1, delta: {toolUse: {input: '{"path":"target.txt"}'}}}),
    stop(1), frame('messageStop', {stopReason: 'tool_use'}), frame('metadata', usage)],
};
frames.throttling = [start(), text(0, 'one'), exceptionFrame('throttlingException', {message: 'slow down'})];
frames.abrupt = [start(), text(0, 'one'), stop(0)];
frames.weirdstop = [start(), text(0, 'one'), stop(0), frame('messageStop', {stopReason: 'weird'})];
frames.badframe = [start(), corrupt(text(0, 'one'))];
frames.unmodeled = [start(), text(0, 'one'), exceptionFrame('fooException', {message: 'foo'})];
frames.errframe = [start(), text(0, 'one'), errorFrame('SomeError', 'it broke')];
// Events the SDK drops (`$unknown`) and an event with an empty payload still cost reactions on the way.
frames.unknownevent = [start(), frame('somethingNew', {x: 1}), text(0, 'one'), stop(0), frame('messageStop', {stopReason: 'end_turn'})];
frames.emptybody = [start(), bareEvent('contentBlockStop', ''), text(0, 'one'), stop(0), frame('messageStop', {stopReason: 'end_turn'})];
const laterFrames = [start(), text(0, 'ok'), stop(0), frame('messageStop', {stopReason: 'end_turn'}), frame('metadata', {usage: {inputTokens: 1, outputTokens: 1, totalTokens: 2}})];
const b64 = list => Buffer.concat(list).toString('base64');
const bodies = Object.fromEntries(Object.entries(frames).map(([shape, list]) => [shape, list.map(f => f.toString('base64'))]));
const laterBody = b64(laterFrames);

// parts(shape, delivery): the bytes that leave with the headers and the bytes withheld until release.
// pending: the first frame leaves with the headers. split: the first frame and half of the second leave with the headers.
function parts(shape, delivery) {
  const list = frames[shape];
  if (delivery === 'buffered') return ['', b64(list)];
  if (delivery === 'pending') return [b64(list.slice(0, 1)), b64(list.slice(1))];
  const cut = Math.floor(list[1].length / 2);
  return [b64([list[0], list[1].subarray(0, cut)]), b64([list[1].subarray(cut), ...list.slice(2)])];
}

// ---------------------------------------------------------------------------------------------------------------------
// Tick counter and Pi hooks

let epoch = 0, tick = 0, active = false, serverPort = 0, onFirstData = null;
const jobTypes = new Set();
const hook = createHook({
  init(id, type) { if (type === 'PROMISE' || type === 'Microtask') jobTypes.add(id); },
  before(id) { if (jobTypes.has(id)) tick++; },
  destroy(id) { jobTypes.delete(id); },
});
if (ticksEnabled) hook.enable();
const now = () => ticksEnabled ? {epoch, tick} : {epoch};
function startEpoch() {
  if (!active) return;
  if (tick > 0 || epoch === 0) { epoch++; tick = 0; }
  if (onFirstData) { const run = onFirstData; onFirstData = null; setImmediate(run); }
}
const originalEmit = EventEmitter.prototype.emit;
EventEmitter.prototype.emit = function (name, ...args) {
  if (active) {
    if (this instanceof net.Socket && name === 'data' && serverPort && this.remotePort === serverPort) startEpoch();
    else if (name === 'response' && this.constructor.name === 'ClientHttp2Stream') startEpoch();
  }
  return originalEmit.call(this, name, ...args);
};
const originalReadablePush = Readable.prototype.push;
Readable.prototype.push = function (data, ...rest) {
  if (active && this.constructor.name === 'ClientHttp2Stream' && (data === null || data?.length > 0)) startEpoch();
  return originalReadablePush.call(this, data, ...rest);
};

const { runAgentLoop } = await import(base + 'pi-agent-core/dist/index.js');
const { lazyStream } = await import(base + 'pi-ai/dist/api/lazy.js');
const { EventStream } = await import(base + 'pi-ai/dist/utils/event-stream.js');
const { ModelRuntime } = await import(root + '/dist/core/model-runtime.js');
const { stream: provider } = await import(base + 'pi-ai/dist/api/bedrock-converse-stream.js');
const copy = value => value === undefined ? undefined : JSON.parse(JSON.stringify(value));

// Every assistant EventStream.push is recorded at its call: the provider push order and the tick at which each event entered the queue.
let pushes = [];
let streamIds = new WeakMap(), streamCount = 0;
const originalPush = EventStream.prototype.push;
EventStream.prototype.push = function (event) {
  if (active && (event?.partial || ['start', 'done', 'error'].includes(event?.type))) {
    if (!streamIds.has(this)) streamIds.set(this, streamCount++);
    pushes.push({stream: streamIds.get(this), ...now(), event: copy(event)});
  }
  return originalPush.call(this, event);
};

// sdk.ts:349-388 passes these two hooks on every Agent request; both are async no-ops when no extension handles the event.
const sdkHooks = {onPayload: async payload => payload, onResponse: async () => {}};

// ---------------------------------------------------------------------------------------------------------------------
// In-process pipeline matrix

function launchServer(shape, delivery, http) {
  const [first, rest] = parts(shape, delivery);
  const child = fork(serverPath, [delivery, http, JSON.stringify(first), JSON.stringify(rest), JSON.stringify(laterBody)], {stdio: ['ignore', 'inherit', 'inherit', 'ipc']});
  return new Promise(resolve => child.once('message', message => resolve({child, port: message.port})));
}

async function probe(shape, delivery, http, consumer, layers, hooks = true) {
  const {child, port} = await launchServer(shape, delivery, http);
  serverPort = port;
  if (http === 'h1') process.env.AWS_BEDROCK_FORCE_HTTP1 = '1'; else delete process.env.AWS_BEDROCK_FORCE_HTTP1;
  epoch = 0; tick = 0; onFirstData = null; active = true; pushes = []; streamIds = new WeakMap(); streamCount = 0;
  const release = () => child.send({release: true});
  const controller = new AbortController();
  const model = {id: 'probe', name: 'probe', api: API, provider: 'probe-provider', baseUrl: `http://127.0.0.1:${port}`, reasoning: false, input: ['text'], contextWindow: 4096, maxTokens: 256, cost: {input: 0, output: 0, cacheRead: 0, cacheWrite: 0}};
  const message = {role: 'user', content: [{type: 'text', text: 'probe'}], timestamp: 1};
  const log = [];
  let runtime, runtimeDir, response, terminal;
  if (layers === 'runtime') {
    runtimeDir = await mkdtemp(join(tmpdir(), 'b-runtime-'));
    const modelsPath = join(runtimeDir, 'models.json');
    await writeFile(modelsPath, JSON.stringify({providers: {'probe-provider': {apiKey: 'test', baseUrl: model.baseUrl, api: API, models: [model]}}}));
    runtime = await ModelRuntime.create({modelsPath, authPath: join(runtimeDir, 'auth.json'), refreshOnCreate: false});
  }
  const streamFn = (m, c, o) => {
    const callbacks = hooks ? sdkHooks : {};
    if (runtime) return response = runtime.streamSimple(m, c, {...o, ...callbacks, maxRetries: 0, signal: controller.signal});
    let make = () => provider(m, c, {...o, ...callbacks, apiKey: 'test', maxRetries: 0, signal: controller.signal});
    for (let i = 0; i < layers; i++) { const inner = make; make = () => lazyStream(m, async () => inner()); }
    return response = make();
  };
  // Buffered delivery has nothing to release. Pending and split delivery release at the first start observation; result-only never observes one, so it releases after the first bytes.
  const releaseOnStart = delivery === 'buffered' ? () => {} : release;
  const isAgent = consumer.endsWith('agent');
  const held = consumer.startsWith('held');
  try {
    if (consumer === 'result-only' && delivery !== 'buffered') onFirstData = release;
    if (isAgent) {
      await runAgentLoop([message], {messages: [], tools: []}, {model, convertToLlm: m => m, finishTurn: () => ({action: 'end'})}, async event => {
        log.push({at: 'entry', ...now(), event: copy(event)});
        if (event.type === 'message_start' && event.message.role === 'assistant') {
          releaseOnStart();
          if (held) {
            await response.result();
            log.push({at: 'after-result', ...now(), event: copy(event)});
          }
        }
      }, controller.signal, streamFn);
    } else {
      response = streamFn(model, {messages: [message]}, {});
      if (consumer === 'result-only') {
        const result = await response.result();
        log.push({at: 'result-before-iteration', ...now(), result: copy(result)});
      }
      for await (const event of response) {
        if (event.type === 'done') terminal = event.message;
        if (event.type === 'error') terminal = event.error;
        log.push({at: 'entry', ...now(), event: copy(event)});
        if (event.type === 'start') {
          if (consumer === 'cancel') controller.abort();
          releaseOnStart();
          if (held) {
            await response.result();
            log.push({at: 'after-result', ...now(), event: copy(event)});
          }
        }
      }
      const result = await response.result();
      assert.equal(result, terminal);
      log.push({at: 'result', ...now(), result: copy(result)});
    }
  } finally {
    active = false;
    release();
    controller.abort();
    child.kill();
    if (runtimeDir) await rm(runtimeDir, {recursive: true, force: true});
  }
  return {api: API, shape, delivery, http, consumer, layers, hooks, epochs: epoch, records: log, pushes};
}

// ---------------------------------------------------------------------------------------------------------------------
// Real `pi --mode rpc` observations

async function rpcProbe(shape, delivery, http) {
  const {child, port} = await launchServer(shape, delivery, http);
  const dir = await mkdtemp(join(tmpdir(), 'b-rpc-'));
  const agent = join(dir, 'agent');
  await mkdir(agent);
  await writeFile(join(agent, 'models.json'), JSON.stringify({providers: {p: {api: API, baseUrl: `http://127.0.0.1:${port}`, apiKey: 'k', models: [{id: 'strict', name: 'strict', reasoning: false, input: ['text'], cost: {input: 0, output: 0, cacheRead: 0, cacheWrite: 0}, contextWindow: 128000, maxTokens: 1000}]}}}));
  await writeFile(join(dir, 'target.txt'), 'x\n');
  const env = {...process.env, HOME: dir, PI_CODING_AGENT_DIR: agent};
  if (http === 'h1') env.AWS_BEDROCK_FORCE_HTTP1 = '1'; else delete env.AWS_BEDROCK_FORCE_HTTP1;
  const pi = spawn(process.execPath, [root + '/dist/cli.js', '--mode', 'rpc', '--offline', '--no-extensions', '--model', 'p/strict', '--no-context-files', '--no-skills', '--session-dir', join(dir, 's')], {cwd: dir, env, stdio: ['pipe', 'pipe', 'inherit']});
  const events = [];
  let settled;
  const done = new Promise(resolve => { settled = resolve; });
  createInterface({input: pi.stdout}).on('line', line => {
    let event; try { event = JSON.parse(line); } catch { return; }
    if (event.type !== 'response') events.push(event);
    if (event.type === 'message_start' && event.message?.role === 'assistant' && delivery !== 'buffered') child.send({release: true});
    if (event.type === 'agent_settled') settled();
  });
  pi.stdin.write(JSON.stringify({id: 'read', type: 'prompt', message: 'READ'}) + '\n');
  const timeout = setTimeout(settled, 20000);
  await done;
  clearTimeout(timeout);
  pi.kill(); child.kill();
  await rm(dir, {recursive: true, force: true});
  // The RPC wire drops `message` from message_update and keeps a cumulative `usage`; the other message events carry the whole message.
  const assistant = events.filter(e => e.type === 'message_update' || /^message_(start|end)$/.test(e.type) && e.message?.role === 'assistant');
  return {api: API, shape, delivery, http, events: copy(assistant)};
}

const outputs = [];
for (const http of axes.https) {
  for (const layers of axes.layers) {
    for (const shape of axes.shapes) {
      for (const delivery of axes.deliveries) {
        for (const consumer of axes.consumers) {
          const output = await probe(shape, delivery, http, consumer, layers);
          outputs.push(output);
          console.log(`${http}/${layers}/${shape}/${delivery}/${consumer}: ${output.records.length} records, ${output.pushes.length} pushes`);
        }
      }
    }
  }
  for (const [shapes, hooks] of [[axes.errorShapes, true], [axes.noHookShapes, false]]) {
    for (const shape of shapes) {
      for (const delivery of axes.deliveries) {
        const output = await probe(shape, delivery, http, 'direct', 0, hooks);
        outputs.push(output);
        console.log(`${http}/0/${shape}/${delivery}/direct hooks=${hooks}: ${output.records.length} records, ${output.pushes.length} pushes`);
      }
    }
  }
}
const rpc = [];
if (process.env.PROBE_SKIP_RPC !== '1') {
  for (const http of axes.https) for (const shape of axes.shapes) for (const delivery of axes.deliveries) {
    rpc.push(await rpcProbe(shape, delivery, http));
    console.log(`rpc/${http}/${shape}/${delivery}: ${rpc.at(-1).events.length} assistant events`);
  }
}
if (process.argv[3]) {
  await writeFile(process.argv[3], JSON.stringify({...versions, axes, bodies, laterBody}, null, 2) + '\n');
}
// One case per line keeps diffs reviewable.
const lines = outputs.map(o => '  ' + JSON.stringify(o));
const rpcLines = rpc.map(o => '  ' + JSON.stringify(o));
await writeFile(process.argv[2], `{"piVersion":"1.0.1","awsSdkClientBedrockRuntime":"3.1127.0","smithyCore":"3.35.1","smithyNodeHttpHandler":"4.12.1","cases":[\n${lines.join(',\n')}\n],"rpc":[\n${rpcLines.join(',\n')}\n]}\n`);
process.exit(0);
