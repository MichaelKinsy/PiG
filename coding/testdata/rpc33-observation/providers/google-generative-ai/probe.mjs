// Pi 1.0.0 / @google/genai 2.21.0: exact partial-message observation and microtask order for the google-generative-ai provider.
//
// Usage: node probe.mjs <pi.json> [inputs.json]
//   env PI_PACKAGE_ROOT  installed @earendil-works/pi-coding-agent 1.0.0 (its dependency tree provides pi-ai, pi-agent-core and @google/genai)
//   env PROBE_TICKS=0    disable the tick counter; the run must then produce the same records once tick fields are removed (this proves the counter does not perturb order)
//   env PROBE_SKIP_RPC=1 skip the real `pi --mode rpc` observations
//
// Tick model. A tick is one microtask job (a Promise reaction or a queueMicrotask callback), counted by an async_hooks `before` hook.
// The hook only increments a counter, so it neither creates Promises nor reorders jobs.
// Every client socket 'data' event starts an epoch (tick resets to 0) because each socket read is a macrotask: buffered delivery is one epoch
// for the headers and the whole body, pending delivery one per write. A record made at (epoch, tick) ran after `tick` microtask jobs of that epoch.
//
// Rounds. Jobs include unrelated background work (undici, Web Streams), so they order events but do not measure how many dependent steps separate them.
// `round` is the FIFO generation instead: a self-rescheduling queueMicrotask chain that starts at each socket 'data' event and occupies one queue slot per
// generation (`round` counts its steps). A second pass over every case measures it. The chain starves process.nextTick callbacks until it stops (ROUND_CAP),
// so a round is valid only while no nextTick-dependent step (end-of-body notification) has run; it is recorded for every event, and the pass asserts that
// its records equal the job pass record for record (same states, same order). Go replays rounds for events before the end of the body.
import { fork, spawn } from 'node:child_process';
import net from 'node:net';
import { createHook } from 'node:async_hooks';
import assert from 'node:assert/strict';
import { writeFile, readFile, mkdtemp, rm, mkdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createInterface } from 'node:readline';

const root = process.env.PI_PACKAGE_ROOT;
const base = root + '/node_modules/@earendil-works/';
for (const [path, version] of [[root + '/package.json', '1.0.0'], [base + 'pi-ai/package.json', '1.0.0'], [base + 'pi-agent-core/package.json', '1.0.0'], [root + '/node_modules/@google/genai/package.json', '2.21.0']]) {
  assert.equal(JSON.parse(await readFile(path, 'utf8')).version, version, path);
}
const ticksEnabled = process.env.PROBE_TICKS !== '0';
const ROUND_CAP = 160;
const API = 'google-generative-ai';
const serverPath = new URL('./server.mjs', import.meta.url).pathname;

const axes = {
  api: API,
  shapes: ['text', 'thinking', 'tool', 'mixed'],
  deliveries: ['buffered', 'pending', 'split'],
  consumers: ['direct', 'held-direct', 'result-only', 'cancel', 'agent', 'held-agent'],
  layers: [0, 1, 2, 'runtime'],
};

const chunk = value => 'data: ' + JSON.stringify(value) + '\r\n\r\n';
const usage = (extra = {}) => ({promptTokenCount: 10, candidatesTokenCount: 3, totalTokenCount: 13, ...extra});
const records = {
  text: [
    {candidates: [{content: {role: 'model', parts: [{text: 'one'}]}}], responseId: 'resp-g', modelVersion: 'probe'},
    {candidates: [{content: {role: 'model', parts: [{text: ' two'}]}, finishReason: 'STOP'}], usageMetadata: usage()},
  ],
  thinking: [
    {candidates: [{content: {role: 'model', parts: [{text: 'one', thought: true}]}}], responseId: 'resp-g'},
    {candidates: [{content: {role: 'model', parts: [{text: ' two', thought: true, thoughtSignature: 'c2ln'}]}}]},
    {candidates: [{content: {role: 'model', parts: [{text: 'answer'}]}, finishReason: 'STOP'}], usageMetadata: usage({thoughtsTokenCount: 2})},
  ],
  tool: [
    // The RPC33 shape: one record carries the call, the finish reason and the usage; the call has no id, so Pi generates `read_<ms>_<counter>`.
    {candidates: [{content: {role: 'model', parts: [{functionCall: {name: 'read', args: {path: 'target.txt'}}}]}, finishReason: 'STOP'}], usageMetadata: usage(), modelVersion: 'probe', responseId: 'g1'},
  ],
  mixed: [
    {candidates: [{content: {role: 'model', parts: [{text: 'looking'}]}}], responseId: 'resp-g'},
    {candidates: [{content: {role: 'model', parts: [{functionCall: {id: 'call-g', name: 'read', args: {path: 'target.txt'}}}]}, finishReason: 'STOP'}], usageMetadata: usage()},
  ],
};
const textReply = [{candidates: [{content: {role: 'model', parts: [{text: 'ok'}]}, finishReason: 'STOP'}]}];
const bodies = Object.fromEntries(Object.entries(records).map(([shape, list]) => [shape, list.map(chunk)]));
const laterBody = textReply.map(chunk).join('');

// parts(shape, delivery): the bytes that leave with the headers and the bytes withheld until release.
// Several records split at a record boundary; a single record splits mid-record.
function parts(shape, delivery) {
  const list = bodies[shape];
  const all = list.join('');
  if (delivery !== 'split') return ['', all];
  if (list.length > 1) return [list[0], list.slice(1).join('')];
  const cut = Math.floor(all.length / 2);
  return [all.slice(0, cut), all.slice(cut)];
}

// ---------------------------------------------------------------------------------------------------------------------
// Tick counter and Pi hooks

let epoch = 0, tick = 0, round = 0, seq = 0, mode = 'jobs', chain = 0, active = false, serverPort = 0, onFirstData = null;
const jobTypes = new Set();
const hook = createHook({
  init(id, type) { if (type === 'PROMISE' || type === 'Microtask') jobTypes.add(id); },
  before(id) { if (mode === 'jobs' && jobTypes.has(id)) tick++; },
  destroy(id) { jobTypes.delete(id); },
});
if (ticksEnabled) hook.enable();
// now() stamps one observation: `seq` is the total order across records and pushes; `tick` counts jobs, `round` counts generations (see the header).
const now = () => ticksEnabled ? {epoch, seq: seq++, ...(mode === 'jobs' ? {tick} : {round})} : {epoch, seq: seq++};
function startRounds() {
  const mine = ++chain;
  round = 0;
  const step = () => { if (chain === mine && round < ROUND_CAP) { round++; queueMicrotask(step); } };
  queueMicrotask(step);
}
const originalEmit = net.Socket.prototype.emit;
net.Socket.prototype.emit = function (name, ...args) {
  if (active && name === 'data' && serverPort && this.remotePort === serverPort) {
    epoch++; tick = 0;
    if (mode === 'rounds') startRounds();
    if (onFirstData) { const run = onFirstData; onFirstData = null; setImmediate(run); }
  }
  return originalEmit.call(this, name, ...args);
};

const { runAgentLoop } = await import(base + 'pi-agent-core/dist/index.js');
const { lazyStream } = await import(base + 'pi-ai/dist/api/lazy.js');
const { EventStream } = await import(base + 'pi-ai/dist/utils/event-stream.js');
const { ModelRuntime } = await import(root + '/dist/core/model-runtime.js');
const { stream: provider } = await import(base + 'pi-ai/dist/api/google-generative-ai.js');
const copy = value => value === undefined ? undefined : JSON.parse(JSON.stringify(value));

// Every assistant EventStream.push is recorded at its call: the provider push order and the tick at which each event entered the queue.
// Agent-loop events carry neither a partial nor an assistant terminal type.
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

// ---------------------------------------------------------------------------------------------------------------------
// In-process pipeline matrix

function launchServer(shape, delivery) {
  const [first, rest] = parts(shape, delivery);
  const child = fork(serverPath, [delivery, JSON.stringify(first), JSON.stringify(rest), JSON.stringify(laterBody)], {stdio: ['ignore', 'inherit', 'inherit', 'ipc']});
  return new Promise(resolve => child.once('message', message => resolve({child, port: message.port})));
}

async function probeOnce(shape, delivery, consumer, layers) {
  const {child, port} = await launchServer(shape, delivery);
  serverPort = port;
  epoch = 0; tick = 0; round = 0; seq = 0; chain++; onFirstData = null; active = true; pushes = []; streamIds = new WeakMap(); streamCount = 0;
  const release = () => child.send({release: true});
  const controller = new AbortController();
  const model = {id: 'probe', name: 'probe', api: API, provider: 'probe-provider', baseUrl: `http://127.0.0.1:${port}/v1beta`, reasoning: false, input: ['text'], contextWindow: 4096, maxTokens: 256, cost: {input: 0, output: 0, cacheRead: 0, cacheWrite: 0}};
  const message = {role: 'user', content: [{type: 'text', text: 'probe'}], timestamp: 1};
  const log = [];
  let runtime, runtimeDir, response, terminal;
  if (layers === 'runtime') {
    runtimeDir = await mkdtemp(join(tmpdir(), 'g-runtime-'));
    const modelsPath = join(runtimeDir, 'models.json');
    await writeFile(modelsPath, JSON.stringify({providers: {'probe-provider': {apiKey: 'test', baseUrl: model.baseUrl, api: API, models: [model]}}}));
    runtime = await ModelRuntime.create({modelsPath, authPath: join(runtimeDir, 'auth.json'), refreshOnCreate: false});
  }
  const streamFn = (m, c, o) => {
    if (runtime) return response = runtime.streamSimple(m, c, {...o, maxRetries: 0, signal: controller.signal});
    let make = () => provider(m, c, {...o, apiKey: 'test', maxRetries: 0, signal: controller.signal});
    for (let i = 0; i < layers; i++) { const inner = make; make = () => lazyStream(m, async () => inner()); }
    return response = make();
  };
  // Buffered delivery has nothing to release. Pending and split delivery release at the first start observation; result-only never observes one, so it releases after the headers.
  const releaseOnStart = delivery === 'buffered' ? () => {} : release;
  const isAgent = consumer.endsWith('agent');
  const held = consumer.startsWith('held');
  try {
    // A consumer that never observes start releases once the headers have been read.
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
    chain++;
    release();
    controller.abort();
    child.kill();
    if (runtimeDir) await rm(runtimeDir, {recursive: true, force: true});
  }
  return {api: API, shape, delivery, consumer, layers, epochs: epoch, records: log, pushes};
}

// The job pass and the round pass observe the same case twice. Their records must agree in every field except the clock, or the round pass perturbed order.
// Wall-clock values (assistant timestamps and the `_<ms>_<counter>` of generated tool-call ids) differ between the two passes by construction.
const strip = ({tick: _t, round: _r, ...rest}) => JSON.stringify(rest).replace(/"timestamp":\d{13}/g, '"timestamp":0').replace(/_\d{13}_\d+/g, '_T_N');
async function probe(shape, delivery, consumer, layers) {
  if (!ticksEnabled) return probeOnce(shape, delivery, consumer, layers);
  mode = 'jobs';
  const jobs = await probeOnce(shape, delivery, consumer, layers);
  mode = 'rounds';
  const rounds = await probeOnce(shape, delivery, consumer, layers);
  mode = 'jobs';
  const name = `${layers}/${shape}/${delivery}/${consumer}`;
  assert.deepEqual(rounds.records.map(strip), jobs.records.map(strip), name + ' records');
  assert.deepEqual(rounds.pushes.map(strip), jobs.pushes.map(strip), name + ' pushes');
  // Timing-sensitive pending releases (result-only) are the only way two passes may differ; the assertions above would fail on them.
  const merge = (a, b) => a.map((entry, i) => ({...entry, round: b[i].round}));
  return {...jobs, records: merge(jobs.records, rounds.records), pushes: merge(jobs.pushes, rounds.pushes)};
}

// ---------------------------------------------------------------------------------------------------------------------
// Real `pi --mode rpc` observations

async function rpcProbe(shape, delivery) {
  const {child, port} = await launchServer(shape, delivery);
  const dir = await mkdtemp(join(tmpdir(), 'g-rpc-'));
  const agent = join(dir, 'agent');
  await mkdir(agent);
  await writeFile(join(agent, 'models.json'), JSON.stringify({providers: {p: {api: API, baseUrl: `http://127.0.0.1:${port}/v1beta`, apiKey: 'k', models: [{id: 'strict', name: 'strict', reasoning: false, input: ['text'], cost: {input: 0, output: 0, cacheRead: 0, cacheWrite: 0}, contextWindow: 128000, maxTokens: 1000}]}}}));
  await writeFile(join(dir, 'target.txt'), 'x\n');
  const pi = spawn(process.execPath, [root + '/dist/cli.js', '--mode', 'rpc', '--offline', '--no-extensions', '--model', 'p/strict', '--no-context-files', '--no-skills', '--session-dir', join(dir, 's')], {cwd: dir, env: {...process.env, HOME: dir, PI_CODING_AGENT_DIR: agent}, stdio: ['pipe', 'pipe', 'inherit']});
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
  return {api: API, shape, delivery, events: copy(assistant)};
}

const outputs = [];
for (const layers of axes.layers) {
  for (const shape of axes.shapes) {
    for (const delivery of axes.deliveries) {
      for (const consumer of axes.consumers) {
        const output = await probe(shape, delivery, consumer, layers);
        outputs.push(output);
        console.log(`${layers}/${shape}/${delivery}/${consumer}: ${output.records.length} records, ${output.pushes.length} pushes`);
      }
    }
  }
}
const rpc = [];
if (process.env.PROBE_SKIP_RPC !== '1') {
  for (const shape of axes.shapes) for (const delivery of axes.deliveries) {
    rpc.push(await rpcProbe(shape, delivery));
    console.log(`rpc/${shape}/${delivery}: ${rpc.at(-1).events.length} assistant events`);
  }
}
if (process.argv[3]) {
  await writeFile(process.argv[3], JSON.stringify({piVersion: '1.0.0', genaiVersion: '2.21.0', axes, bodies, laterBody}, null, 2) + '\n');
}
// Generated tool-call ids are `read_<ms>_<counter>` (google-generative-ai.ts:199). The clock and the process-wide counter are not part of the contract, so the oracle
// stores `read_T_N`; assistant timestamps stay raw (the Go comparator canonicalizes them as the OpenAI oracle's does).
const canonicalId = value => JSON.stringify(value).replace(/_\d{13}_\d+"/g, '_T_N"');
// One case per line keeps diffs reviewable.
const lines = outputs.map(o => '  ' + canonicalId(o));
const rpcLines = rpc.map(o => '  ' + canonicalId(o));
await writeFile(process.argv[2], `{"piVersion":"1.0.0","genaiVersion":"2.21.0","cases":[\n${lines.join(',\n')}\n],"rpc":[\n${rpcLines.join(',\n')}\n]}\n`);
process.exit(0);
