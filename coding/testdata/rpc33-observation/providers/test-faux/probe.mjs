// Drives Pi 1.0.2 against PiG's paired test-faux fixture (test/parity/testdata/test-faux-provider.ts) for every scenario.
//  - `rpc`: the real CLI in `--mode rpc` (recorder.mjs records push/serialization interleaving).
//  - `direct`: the fixture's stream function behind 0..2 pi-ai `lazyStream` layers, consumed by `for await` or by
//    pi-agent-core's `runAgentLoop`; every delivered event is copied at its delivery tick with the number of pushes so far.
//  - `ticks`: an independent microtask loop started in the stream-creating segment, sampling the push count.
// usage: node probe.mjs <out.json> [runs]. Requires PI_PACKAGE_ROOT.
import { spawn } from 'node:child_process';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createInterface } from 'node:readline';
import { pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';

const root = process.env.PI_PACKAGE_ROOT;
assert.ok(root, 'PI_PACKAGE_ROOT must name the pinned pi-coding-agent package');
assert.equal(JSON.parse(readFileSync(join(root, 'package.json'), 'utf8')).version, '1.0.2');
const here = new URL('.', import.meta.url).pathname;
const provider = join(here, '../../../../../test/parity/testdata/test-faux-provider.ts');
const recorder = join(here, 'recorder.mjs');
const [out, runsArg] = process.argv.slice(2);
const runs = Number(runsArg ?? 1);

export const scenarios = [
  { name: 'text', prompt: 'What is 20+22?' },
  { name: 'tool', prompt: 'Run: read parity-read-target.txt' },
  { name: 'parallel', prompt: 'Run: parallel reads' },
  { name: 'error', prompt: 'unhandled request for the fixture' },
  { name: 'live-stream', prompt: 'TUI_LIVE_STREAM' },
];

// The fixture finds Pi's package root from process.argv[1]; make it look like the CLI.
process.argv[1] = join(root, 'dist/cli.js');
const base = pathToFileURL(`${root}/node_modules/@earendil-works/`).href;
const { lazyStream } = await import(`${base}pi-ai/dist/api/lazy.js`);
const { runAgentLoop } = await import(`${base}pi-agent-core/dist/index.js`);
const copy = (value) => JSON.parse(JSON.stringify(value));

async function rpc(scenario) {
  const dir = mkdtempSync(join(tmpdir(), 'test-faux-probe-'));
  const agent = join(dir, 'agent');
  mkdirSync(agent);
  writeFileSync(join(dir, 'parity-read-target.txt'), 'hello\n');
  const log = join(dir, 'log.jsonl');
  writeFileSync(log, '');
  const child = spawn(process.execPath, [join(root, 'dist/bundle/cli.js'), '--mode', 'rpc', '--offline', '--no-extensions', '-e', recorder, '--model', 'test-faux/faux-1',
    '--no-context-files', '--no-skills', '--no-session'], {
    cwd: dir,
    env: { ...process.env, HOME: dir, PI_CODING_AGENT_DIR: agent, TESTFAUX_PROBE_LOG: log, TESTFAUX_PROVIDER: provider },
    stdio: ['pipe', 'pipe', 'inherit'],
  });
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`timeout ${scenario.name}`)), 60000);
    createInterface({ input: child.stdout }).on('line', (line) => {
      let event;
      try { event = JSON.parse(line); } catch { return; }
      if (event.type === 'agent_settled') { clearTimeout(timer); resolve(); }
    });
    child.stdin.write(JSON.stringify({ id: 'p', type: 'prompt', message: scenario.prompt }) + '\n');
  });
  child.kill();
  const entries = readFileSync(log, 'utf8').trim().split('\n').filter(Boolean).map((line) => JSON.parse(line));
  rmSync(dir, { recursive: true, force: true });
  return entries;
}

// The fixture module registers one provider; capture its stream function. Its tool-call counter is module state, so every
// observation imports a fresh instance (the query string defeats the module cache), as PiG's provider starts at 1.
let instances = 0;
async function streamFunction() {
  const fixture = await import(`${pathToFileURL(provider).href}?instance=${instances++}`);
  let captured, model;
  fixture.default({ registerProvider(_name, config) { captured = config.streamSimple; model = { ...config.models[0], api: config.api, provider: 'test-faux', baseUrl: config.baseUrl }; } });
  return { stream: captured, model };
}

async function direct(scenario, layers, mode) {
  const { stream: fixtureStream, model } = await streamFunction();
  const counter = { pushed: 0 };
  const source = (m, c, o) => {
    const inner = fixtureStream(m, c, o);
    const push = inner.push.bind(inner);
    inner.push = (event) => { counter.pushed++; return push(event); };
    return inner;
  };
  const records = [];
  const streamFn = (m, c, o) => {
    let make = () => source(m, c, o);
    for (let i = 0; i < layers; i++) {
      const inner = make;
      make = () => lazyStream(m, async () => inner());
    }
    return make();
  };
  const user = { role: 'user', content: [{ type: 'text', text: scenario.prompt }], timestamp: 1 };
  if (mode === 'agent') {
    await runAgentLoop([user], { messages: [], tools: [] }, { model, convertToLlm: (m) => m, finishTurn: () => ({ action: 'end' }) }, async (event) => {
      if ((event.type === 'message_start' || event.type === 'message_update' || event.type === 'message_end') && event.message.role === 'assistant') records.push({ pushed: counter.pushed, event: copy(event) });
    }, undefined, streamFn);
  } else {
    const stream = streamFn(model, { messages: [user] }, {});
    for await (const event of stream) records.push({ pushed: counter.pushed, event: copy(event) });
    records.push({ pushed: counter.pushed, result: copy(await stream.result()) });
  }
  return { layers, mode, records };
}

async function ticks(scenario) {
  const { stream: fixtureStream, model } = await streamFunction();
  let pushed = 0;
  const inner = fixtureStream(model, { messages: [{ role: 'user', content: [{ type: 'text', text: scenario.prompt }], timestamp: 1 }] }, {});
  const push = inner.push.bind(inner);
  inner.push = (event) => { pushed++; return push(event); };
  const seen = [];
  for (let i = 0; i < 40; i++) {
    await undefined;
    seen.push(pushed);
  }
  await inner.result();
  return { layers: 0, mode: 'ticks', records: seen };
}

const results = [];
for (const scenario of scenarios) {
  const rpcRuns = [];
  for (let i = 0; i < runs; i++) rpcRuns.push(await rpc(scenario));
  const directRuns = [];
  for (const layers of [0, 1, 2]) for (const mode of ['direct', 'agent']) directRuns.push(await direct(scenario, layers, mode));
  directRuns.push(await ticks(scenario));
  results.push({ scenario, rpc: rpcRuns, direct: directRuns });
}
writeFileSync(out, JSON.stringify({ pi: '1.0.2', node: process.version, results }, null, 1));
