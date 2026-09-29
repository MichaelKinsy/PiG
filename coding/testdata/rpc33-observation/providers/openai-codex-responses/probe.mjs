// D82 W5 oracle for the openai-codex-responses SSE transport (Pi 0.87.1).
//
// Drives Pi's real pipeline twice:
//   inproc  Pi's exported `stream` (packages/ai/src/api/openai-codex-responses.ts) against a loopback server, with
//           the AssistantMessageEventStream `push` instrumented and a microtask counter chain, so every push and
//           every consumer delivery records its exact tick position and the partial state visible at that tick.
//   rpc     The real Pi CLI in `--mode rpc`; the value is the first assistant `message_start` the RPC writer emits.
//
// The counter chain adds only independent reactions to the FIFO microtask queue; it cannot reorder the modeled
// reactions. Every data event on a client socket starts a new `segment` (an external completion: response headers or
// body bytes arriving) and restarts the chain at tick 0. `tick` is the number of chain steps completed when the entry
// is logged, so within a segment the distance between two entries is exact. A chain step re-arms itself for `window`
// more steps after each observation, then stops so the event loop is never starved.
//
// usage: PI_PACKAGE_ROOT=<pi-coding-agent dir> node probe.mjs <pi.json> [runs]
import { createServer } from 'node:http';
import net from 'node:net';
import { once } from 'node:events';
import { spawn } from 'node:child_process';
import { readFile, writeFile, mkdtemp, mkdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createInterface } from 'node:readline';
import assert from 'node:assert/strict';

const root = process.env.PI_PACKAGE_ROOT;
const piAI = root + '/node_modules/@earendil-works/pi-ai/';
for (const [dir, version] of [[root, '0.87.1'], [piAI, '0.87.1']]) {
  assert.equal(JSON.parse(await readFile(dir + '/package.json', 'utf8')).version, version);
}
const { stream } = await import(piAI + 'dist/api/openai-codex-responses.js');
const { AssistantMessageEventStream } = await import(piAI + 'dist/utils/event-stream.js');

const outPath = process.argv[2];
const runs = Number(process.argv[3] ?? 8);
const args = '{"path":"parity-read-target.txt"}';
const jwt = 'h.' + Buffer.from(JSON.stringify({ 'https://api.openai.com/auth': { chatgpt_account_id: 'acct' } })).toString('base64') + '.s';
const frame = e => `event: ${e.type}\ndata: ${JSON.stringify(e)}\n\n`;

// Fixture shapes. `frames` are the SSE records, in wire order.
function fixture(shape) {
  if (shape === 'invalid' || shape === 'apierror') {
    const item = { type: 'function_call', id: 'fc_1', call_id: 'call_1', name: 'read', arguments: '' };
    const head = [{ type: 'response.created', response: { id: 'resp_1' } }, { type: 'response.output_item.added', output_index: 0, item }];
    // invalid: malformed JSON (CodexProtocolError). apierror: an `error` record (CodexApiError).
    if (shape === 'invalid') return [...head.map(frame), 'data: {not json\n\n'];
    return [...head, { type: 'error', code: 'boom', message: 'broken' }].map(frame);
  }
  if (shape === 'tool') {
    const item = { type: 'function_call', id: 'fc_1', call_id: 'call_1', name: 'read', arguments: '' };
    const done = { ...item, arguments: args, status: 'completed' };
    return [
      { type: 'response.created', response: { id: 'resp_1' } },
      { type: 'response.output_item.added', output_index: 0, item },
      { type: 'response.function_call_arguments.delta', output_index: 0, delta: args },
      { type: 'response.output_item.done', output_index: 0, item: done },
      { type: 'response.completed', response: { id: 'resp_1', status: 'completed', output: [done], usage: { input_tokens: 10, output_tokens: 3, total_tokens: 13 } } },
    ].map(frame);
  }
  const item = { type: 'message', id: 'msg_1', role: 'assistant', status: 'completed', content: [] };
  const done = { ...item, content: [{ type: 'output_text', text: 'one two', annotations: [] }] };
  return [
    { type: 'response.created', response: { id: 'resp_1' } },
    { type: 'response.output_item.added', output_index: 0, item },
    { type: 'response.output_text.delta', output_index: 0, content_index: 0, delta: 'one' },
    { type: 'response.output_text.delta', output_index: 0, content_index: 0, delta: ' two' },
    { type: 'response.output_item.done', output_index: 0, item: done },
    { type: 'response.completed', response: { id: 'resp_1', status: 'completed', output: [done], usage: { input_tokens: 10, output_tokens: 3, total_tokens: 13 } } },
  ].map(frame);
}
// buffered: headers and the whole body in one write. pending: headers flushed, the whole body later in one write.
// chunked: headers flushed, then one write per SSE record with a macrotask gap.
const modes = ['buffered', 'pending', 'chunked'];
const shapes = ['tool', 'text', 'invalid', 'apierror'];
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
async function serve(mode, frames, secondFrames) {
  let count = 0;
  const server = createServer(async (req, res) => {
    for await (const _ of req) { /* drain the request before answering */ }
    const current = ++count % 2 === 1 ? frames : secondFrames;
    const buffered = mode === 'buffered' || count % 2 === 0;
    if (buffered) {
      const body = Buffer.from(current.join(''));
      res.writeHead(200, { 'content-type': 'text/event-stream', 'content-length': body.length });
      res.end(body);
      return;
    }
    res.writeHead(200, { 'content-type': 'text/event-stream' });
    res.flushHeaders();
    await sleep(60);
    if (mode === 'pending') { res.end(current.join('')); return; }
    for (const record of current) { res.write(record); await sleep(20); }
    res.end();
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  return { server, port: server.address().port };
}

// ---- microtask counter chain
const window = 1024;
let segment = 0, ticks = 0, allow = 0, running = false;
function startChain() {
  running = true;
  const step = () => Promise.resolve().then(() => { ticks++; if (ticks < allow) step(); else running = false; });
  step();
}
function external() {
  assert.equal(running, false, 'an external event interrupted a running chain');
  segment++; ticks = 0; allow = window; startChain();
}
function arm() {
  assert.ok(segment > 0, 'observation before the first external event');
  assert.ok(running, 'observation after the chain lapsed');
  allow = ticks + window;
}
const originalConnect = net.Socket.prototype.connect;
net.Socket.prototype.connect = function (...a) { this.d82Client = true; return originalConnect.apply(this, a); };
const originalEmit = net.Socket.prototype.emit;
net.Socket.prototype.emit = function (name, ...a) {
  if (name === 'data' && this.d82Client && log) { external(); log.push({ at: 'external', segment, tick: 0 }); }
  return originalEmit.call(this, name, ...a);
};
const view = m => m && ({
  content: JSON.parse(JSON.stringify(m.content)), stopReason: m.stopReason,
  responseId: m.responseId ?? null, usage: m.usage.totalTokens, errorMessage: m.errorMessage ?? null,
});
let log = null;
const originalPush = AssistantMessageEventStream.prototype.push;
AssistantMessageEventStream.prototype.push = function (event) {
  arm();
  log?.push({ at: 'push', type: event.type, segment, tick: ticks, state: view(event.partial ?? event.message ?? event.error) });
  return originalPush.call(this, event);
};

async function inproc(mode, shape) {
  log = []; segment = 0; ticks = 0; running = false;
  const { server, port } = await serve(mode, fixture(shape), fixture(shape));
  const model = { id: 'strict', name: 'strict', api: 'openai-codex-responses', provider: 'p', baseUrl: `http://127.0.0.1:${port}`, reasoning: false, input: ['text'], contextWindow: 128000, maxTokens: 1000, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } };
  const events = stream(model, { messages: [{ role: 'user', content: [{ type: 'text', text: 'READ' }], timestamp: 1 }] }, { apiKey: jwt, transport: 'sse', maxRetries: 0 });
  for await (const event of events) {
    arm();
    log.push({ at: 'deliver', type: event.type, segment, tick: ticks, state: view(event.partial ?? event.message ?? event.error) });
  }
  const entries = log;
  log = null;
  server.closeAllConnections();
  await new Promise(resolve => server.close(resolve));
  return entries;
}

async function rpc(mode, shape) {
  const { server, port } = await serve(mode, fixture(shape), fixture('text'));
  const dir = await mkdtemp(join(tmpdir(), 'd82-codex-'));
  const agent = join(dir, 'agent');
  await mkdir(agent);
  await writeFile(join(agent, 'models.json'), JSON.stringify({ providers: { p: { api: 'openai-codex-responses', baseUrl: `http://127.0.0.1:${port}`, apiKey: jwt, models: [{ id: 'strict', name: 'strict', reasoning: false, input: ['text'], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 1000 }] } } }));
  await writeFile(join(agent, 'settings.json'), JSON.stringify({ transport: 'sse' }));
  await writeFile(join(dir, 'parity-read-target.txt'), 'x\n');
  const child = spawn('node', [root + '/dist/bundle/cli.js', '--mode', 'rpc', '--offline', '--no-extensions', '--model', 'p/strict', '--no-context-files', '--no-skills', '--session-dir', join(dir, 's')], { cwd: dir, env: { ...process.env, HOME: dir, PI_CODING_AGENT_DIR: agent }, stdio: ['pipe', 'pipe', 'inherit'] });
  const starts = [];
  await new Promise(resolve => {
    createInterface({ input: child.stdout }).on('line', line => {
      let event; try { event = JSON.parse(line); } catch { return; }
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
  assert.ok(starts.length > 0, `no assistant message_start for ${mode}/${shape}`);
  return view(starts[0]);
}

async function repeat(fn) {
  const first = await fn();
  for (let i = 1; i < runs; i++) assert.deepEqual(await fn(), first, 'oracle is not deterministic');
  return first;
}

const result = { piVersion: '0.87.1', api: 'openai-codex-responses', node: process.version, runs, fixtures: {}, inproc: {}, rpc: {} };
for (const shape of shapes) result.fixtures[shape] = fixture(shape).join('');
for (const mode of modes) {
  for (const shape of shapes) {
    const key = `${mode}/${shape}`;
    result.inproc[key] = await repeat(() => inproc(mode, shape));
    console.log('inproc', key, result.inproc[key].length);
    if (shape === 'tool' && mode !== 'chunked') {
      result.rpc[key] = await repeat(() => rpc(mode, shape));
      console.log('rpc', key, JSON.stringify(result.rpc[key]));
    }
  }
}
await writeFile(outPath, JSON.stringify(result, null, 2) + '\n');
