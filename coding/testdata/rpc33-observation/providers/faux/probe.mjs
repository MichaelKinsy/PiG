// Drives Pi 0.87.1 against the faux provider (packages/ai/src/providers/faux.ts) for every fixture.
//  - `rpc`: the real CLI in `--mode rpc`; records the ordered interleaving of faux `stream.push` calls and RPC
//    serialization of assistant records (extension.mjs).
//  - `direct`: the real pi-ai faux core behind 0..2 `lazyStream` layers, consumed by `for await` (direct) or by
//    pi-agent-core's `runAgentLoop` (agent). Every delivered event is copied at its delivery tick together with the
//    number of `stream.push` calls made so far, which is the exact tick order the consumer chain adds to the producer.
// usage: node probe.mjs <out.json> [runs]. Requires PI_PACKAGE_ROOT.
import { spawn } from 'node:child_process';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createInterface } from 'node:readline';
import assert from 'node:assert/strict';
import { pathToFileURL } from 'node:url';
import { fauxResponses } from './responses.mjs';

const root = process.env.PI_PACKAGE_ROOT;
assert.ok(root, 'PI_PACKAGE_ROOT must name the pinned pi-coding-agent package');
assert.equal(JSON.parse(readFileSync(join(root, 'package.json'), 'utf8')).version, '0.87.1');
assert.equal(JSON.parse(readFileSync(join(root, 'node_modules/@earendil-works/pi-ai/package.json'), 'utf8')).version, '0.87.1');
const extension = new URL('./extension.mjs', import.meta.url).pathname;
const [out, runsArg] = process.argv.slice(2);
const runs = Number(runsArg ?? 1);

const read = { type: 'toolCall', id: 'fauxread1', name: 'read', arguments: { path: 'parity-read-target.txt' } };
// `buffered`: no tokensPerSecond, so every chunk waits on `queueMicrotask` (faux.ts:scheduleChunk). `pending`: a
// tokensPerSecond makes every chunk wait on a timer, the faux equivalent of an unresolved body read.
// tokenSize 1 fixes the chunk size (4 characters) so no Math.random draw changes the schedule.
export const fixtures = [];
for (const [wait, tokensPerSecond] of [['buffered', undefined], ['pending', 1e6]]) {
  for (const [shape, content, message, factory] of [
    ['text', [{ type: 'text', text: 'one two three four five' }], {}],
    ['thinking', [{ type: 'thinking', thinking: 'one two three' }, { type: 'text', text: 'answer text' }], {}],
    ['tool', [read], { stopReason: 'toolUse' }],
    ['mixed', [{ type: 'thinking', thinking: 'think it' }, { type: 'text', text: 'call it' }, read], { stopReason: 'toolUse', responseId: 'resp-faux' }],
    ['empty', [{ type: 'text', text: '' }], {}],
    ['factory', [{ type: 'text', text: 'one two three' }], {}, 'value'],
    ['factory-reject', [], {}, 'reject'],
    ['error', [{ type: 'text', text: 'partial words' }], { stopReason: 'error', errorMessage: 'scripted failure' }],
    ['pending-stop', [{ type: 'text', text: 'never done' }], { stopReason: 'pending' }],
  ]) fixtures.push({ name: `${wait}-${shape}`, tokensPerSecond, tokenSize: 1, content, message, ...(factory ? { factory } : {}) });
}
// No queued response: the stream errors after the onResponse await without a start event (faux.ts:stream).
fixtures.push({ name: 'buffered-noresponse', noResponse: true, tokenSize: 1, content: [], message: {} });

async function run(fixture) {
  const dir = mkdtempSync(join(tmpdir(), 'faux-probe-'));
  const agent = join(dir, 'agent');
  mkdirSync(agent);
  const log = join(dir, 'log.jsonl');
  writeFileSync(log, '');
  const child = spawn(process.execPath, [join(root, 'dist/bundle/cli.js'), '--mode', 'rpc', '--offline', '--no-extensions', '-e', extension, '--model', 'faux-probe/faux-1',
    '--no-context-files', '--no-skills', '--no-session', '--no-tools'], {
    cwd: dir,
    env: { ...process.env, HOME: dir, PI_CODING_AGENT_DIR: agent, FAUX_PROBE_LOG: log, FAUX_PROBE_FIXTURE: JSON.stringify(fixture) },
    stdio: ['pipe', 'pipe', 'inherit'],
  });
  const wire = [];
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`timeout ${fixture.name}`)), 30000);
    createInterface({ input: child.stdout }).on('line', (line) => {
      let event;
      try { event = JSON.parse(line); } catch { return; }
      wire.push(event.type);
      if (event.type === 'agent_settled') { clearTimeout(timer); resolve(); }
    });
    child.stdin.write(JSON.stringify({ id: 'p', type: 'prompt', message: 'hello' }) + '\n');
  });
  child.kill();
  const entries = readFileSync(log, 'utf8').trim().split('\n').filter(Boolean).map((line) => JSON.parse(line));
  rmSync(dir, { recursive: true, force: true });
  return entries;
}

const base = pathToFileURL(`${root}/node_modules/@earendil-works/`).href;
const ai = await import(`${base}pi-ai/dist/index.js`);
const { lazyStream } = await import(`${base}pi-ai/dist/api/lazy.js`);
const { runAgentLoop } = await import(`${base}pi-agent-core/dist/index.js`);
const copy = (value) => JSON.parse(JSON.stringify(value));

function fauxCore(fixture) {
  const core = ai.createFauxCore({ api: 'faux-probe', provider: 'faux-probe', tokensPerSecond: fixture.tokensPerSecond, tokenSize: { min: fixture.tokenSize, max: fixture.tokenSize } });
  core.setResponses(fauxResponses(ai, fixture));
  return core;
}

// One direct/agent observation. `counter.pushed` counts faux `stream.push` calls at each delivery.
async function direct(fixture, layers, mode) {
  const core = fauxCore(fixture);
  const counter = { pushed: 0 };
  const source = (model, context, options) => {
    const inner = core.streamSimple(model, context, options);
    const push = inner.push.bind(inner);
    inner.push = (event) => { counter.pushed++; return push(event); };
    return inner;
  };
  const model = core.getModel();
  const records = [];
  const streamFn = (m, c, o) => {
    let make = () => source(m, c, o);
    for (let i = 0; i < layers; i++) {
      const inner = make;
      make = () => lazyStream(m, async () => inner());
    }
    return make();
  };
  const user = { role: 'user', content: [{ type: 'text', text: 'probe' }], timestamp: 1 };
  if (mode === 'agent') {
    await runAgentLoop([user], { messages: [], tools: [] }, { model, convertToLlm: (m) => m, finishTurn: () => ({ action: 'end' }) }, async (event) => {
      if (event.type === 'message_start' || event.type === 'message_update' || event.type === 'message_end') {
        if (event.message.role === 'assistant') records.push({ pushed: counter.pushed, event: copy(event) });
      }
    }, undefined, streamFn);
  } else {
    const stream = streamFn(model, { messages: [user] }, {});
    for await (const event of stream) records.push({ pushed: counter.pushed, event: copy(event) });
    records.push({ pushed: counter.pushed, result: copy(await stream.result()) });
  }
  return { layers, mode, records };
}

// `ticks`: an independent async loop started in the same synchronous segment as stream creation records how many
// pushes the faux body has made after each microtask. It measures the producer's own microtask schedule (start
// offset, chunk cadence) without any consumer.
async function ticks(fixture) {
  const core = fauxCore(fixture);
  let pushed = 0;
  const inner = core.streamSimple(core.getModel(), { messages: [{ role: 'user', content: [{ type: 'text', text: 'probe' }], timestamp: 1 }] }, {});
  const push = inner.push.bind(inner);
  inner.push = (event) => { pushed++; return push(event); };
  const seen = [];
  for (let i = 0; i < 80; i++) {
    await undefined;
    seen.push(pushed);
  }
  return { layers: 0, mode: 'ticks', records: seen };
}

// `deferred-ticks`: submit (options.deferred), a pending fetchDeferred, then the final fetchDeferred, each phase measured
// like `ticks` (faux.ts:stream deferred branch and faux.ts:fetchDeferred).
async function deferredTicks() {
  const core = ai.createFauxCore({ api: 'faux-probe', provider: 'faux-probe', tokenSize: { min: 1, max: 1 }, deferred: { pendingFetches: 1, pollAfterMs: 5 } });
  core.setResponses([ai.fauxAssistantMessage([ai.fauxText('one two three')], { timestamp: 1 })]);
  const model = core.getModel();
  let pushed = 0;
  const count = (stream) => {
    const push = stream.push.bind(stream);
    stream.push = (event) => { pushed++; return push(event); };
    return stream;
  };
  const phase = async (create) => {
    pushed = 0;
    const stream = count(create());
    const seen = [];
    for (let i = 0; i < 40; i++) { await undefined; seen.push(pushed); }
    return { seen, result: await stream.result() };
  };
  const context = { messages: [{ role: 'user', content: [{ type: 'text', text: 'probe' }], timestamp: 1 }] };
  const submit = await phase(() => core.streamSimple(model, context, { deferred: true }));
  const handle = submit.result.deferred;
  const pending = await phase(() => core.fetchDeferred(model, handle, {}));
  const final = await phase(() => core.fetchDeferred(model, handle, {}));
  const missing = await phase(() => core.fetchDeferred(model, { ...handle, id: 'unknown' }, {}));
  return { layers: 0, mode: 'deferred-ticks', records: [submit.seen, pending.seen, final.seen, missing.seen], results: [submit, pending, final, missing].map((x) => copy(x.result)) };
}

const results = [];
for (const fixture of fixtures) {
  const rpc = [];
  for (let i = 0; i < runs; i++) rpc.push(await run(fixture));
  const directRuns = [];
  for (const layers of [0, 1, 2]) for (const mode of ['direct', 'agent']) directRuns.push(await direct(fixture, layers, mode));
  directRuns.push(await ticks(fixture));
  results.push({ fixture, rpc, direct: directRuns });
}
writeFileSync(out, JSON.stringify({ pi: '0.87.1', node: process.version, results, deferred: await deferredTicks() }, null, 1));
