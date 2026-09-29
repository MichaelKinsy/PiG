// Runs PiG's binary in `--mode rpc` against the fixture server for every rpc case of pi.json and compares the assistant message_start, message_update (usage) and
// message_end events with the retained Pi observations, over N runs per case.
// Usage: node check.mjs <pig-binary> <runs> [concurrency] [shape/delivery ...]
// Exit 0 only when every run of every case equals Pi. Nothing but clocks and the generated tool-call id's clock and counter is normalized.
import { fork, spawn } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createInterface } from 'node:readline';

const here = new URL('.', import.meta.url).pathname;
const [bin, runsArg, concurrencyArg = '4', ...only] = process.argv.slice(2);
const runs = Number(runsArg);
const concurrency = Number(concurrencyArg);
const oracle = JSON.parse(readFileSync(here + 'pi.json', 'utf8'));
const inputs = JSON.parse(readFileSync(here + 'inputs.json', 'utf8'));
const API = 'google-generative-ai';

const sortKeys = value => Array.isArray(value) ? value.map(sortKeys) : value && typeof value === 'object' ? Object.fromEntries(Object.keys(value).sort().map(key => [key, sortKeys(value[key])])) : value;
const canonical = events => JSON.stringify(sortKeys(events)).replace(/"timestamp":\d{13}/g, '"timestamp":0').replace(/_\d{13}_\d+"/g, '_T_N"');

function parts(shape, delivery) {
  const list = inputs.bodies[shape];
  const all = list.join('');
  if (delivery !== 'split') return ['', all];
  if (list.length > 1) return [list[0], list.slice(1).join('')];
  const cut = Math.floor(all.length / 2);
  return [all.slice(0, cut), all.slice(cut)];
}

async function run(shape, delivery) {
  const [first, rest] = parts(shape, delivery);
  const server = fork(here + 'server.mjs', [delivery, JSON.stringify(first), JSON.stringify(rest), JSON.stringify(inputs.laterBody)], {stdio: ['ignore', 'inherit', 'inherit', 'ipc']});
  const port = await new Promise(resolve => server.once('message', message => resolve(message.port)));
  const dir = await mkdtemp(join(tmpdir(), 'g-check-'));
  const agent = join(dir, 'agent');
  await mkdir(agent);
  await writeFile(join(agent, 'models.json'), JSON.stringify({providers: {p: {api: API, baseUrl: `http://127.0.0.1:${port}/v1beta`, apiKey: 'k', models: [{id: 'strict', name: 'strict', reasoning: false, input: ['text'], cost: {input: 0, output: 0, cacheRead: 0, cacheWrite: 0}, contextWindow: 128000, maxTokens: 1000}]}}}));
  await writeFile(join(dir, 'target.txt'), 'x\n');
  const pig = spawn(bin, ['--mode', 'rpc', '--offline', '--no-extensions', '--model', 'p/strict', '--no-context-files', '--no-skills', '--session-dir', join(dir, 's')], {cwd: dir, env: {...process.env, HOME: dir, PI_CODING_AGENT_DIR: agent, PIG_CODING_AGENT_DIR: agent, PIG_HOME: join(dir, 'pighome')}, stdio: ['pipe', 'pipe', 'inherit']});
  const events = [];
  let settled;
  const done = new Promise(resolve => { settled = resolve; });
  createInterface({input: pig.stdout}).on('line', line => {
    let event; try { event = JSON.parse(line); } catch { return; }
    if (event.type !== 'response') events.push(event);
    if (event.type === 'message_start' && event.message?.role === 'assistant' && delivery !== 'buffered') server.send({release: true});
    if (event.type === 'agent_settled') settled();
  });
  pig.stdin.write(JSON.stringify({id: 'read', type: 'prompt', message: 'READ'}) + '\n');
  const timeout = setTimeout(settled, 20000);
  await done;
  clearTimeout(timeout);
  pig.kill(); server.kill();
  await rm(dir, {recursive: true, force: true});
  return events.filter(e => e.type === 'message_update' || /^message_(start|end)$/.test(e.type) && e.message?.role === 'assistant');
}

let failed = false;
for (const c of oracle.rpc) {
  const name = `${c.shape}/${c.delivery}`;
  if (only.length && !only.includes(name)) continue;
  const want = canonical(c.events);
  const counts = new Map();
  let next = 0;
  await Promise.all(Array.from({length: concurrency}, async () => {
    while (next < runs) {
      next++;
      const got = canonical(await run(c.shape, c.delivery));
      counts.set(got, (counts.get(got) ?? 0) + 1);
    }
  }));
  const matched = counts.get(want) ?? 0;
  console.log(`${name}: ${matched}/${runs} equal Pi; ${counts.size} distinct observation(s)`);
  if (matched !== runs) {
    failed = true;
    for (const [got, count] of counts) if (got !== want) console.log(`  ${count}x ${got}\n  want ${want}`);
  }
}
process.exit(failed ? 1 : 0);
