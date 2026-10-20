// Pi 1.1.0 llama-cpp-classify.ts renderQuestion over the corpus the Go test TestLlamaCppRenderDocsDump prints: one JSON line [content, labels, keys, error] per request.
import { spawnSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';
import { resolve } from 'node:path';
const dump = spawnSync('./test/parity/testdata/prebuilt-go-test.sh', ['./ai', '-run', '^TestLlamaCppRenderDocsDump$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 28 });
const line = dump.stdout?.split('\n').find(l => l.startsWith('llama-render-probes:'));
if (dump.status !== 0 || !line) {
 process.stderr.write(dump.stdout ?? '');
 process.stderr.write(dump.stderr ?? '');
 throw new Error(`llama render probe dump exited ${dump.status}`);
}
const mod = await import(pathToFileURL(resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/api/llama-cpp-classify.js')).href);
// Go's encoder writes U+2028 and U+2029 as escapes; JSON.stringify writes them raw.
const enc = (value) => JSON.stringify(value).replace(/\u2028/g, '\\u2028').replace(/\u2029/g, '\\u2029');
for (const doc of JSON.parse(line.slice('llama-render-probes:'.length))) {
 const questions = {};
 for (const q of doc.questions) {
  if (q.type === 'choice') questions[q.id] = { type: 'choice', instructions: q.instructions, criteria: Object.fromEntries(q.choices) };
  else if (q.type === 'score') questions[q.id] = { type: 'score', instructions: q.instructions, criteria: q.levels };
  else questions[q.id] = { type: 'bool', instructions: q.instructions, criteria: { true: q.true, false: q.false } };
 }
 let row;
 try {
  const r = mod.renderQuestion({ state: doc.state, questions }, doc.ask);
  row = [r.content, r.labels, r.keys, ''];
 } catch (error) {
  row = ['', null, null, error.message];
 }
 process.stdout.write(enc(row) + '\n');
}
