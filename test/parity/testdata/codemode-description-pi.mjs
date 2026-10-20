// Pi 1.1.0 extensions/codemode/tool.ts createCodemodeDescription over the corpus the Go test TestCodemodeDescriptionProbesDump prints: one JSON line [text, error] per probe, the docs path replaced (it names each binary's own docs directory: D22).
import { spawnSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';
import { resolve } from 'node:path';
const dump = spawnSync('./test/parity/testdata/prebuilt-go-test.sh', ['./coding/extension/builtin/codemode', '-run', '^TestCodemodeDescriptionProbesDump$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 28 });
const line = dump.stdout?.split('\n').find(l => l.startsWith('codemode-description-probes:'));
if (dump.status !== 0 || !line) {
 process.stderr.write(dump.stdout ?? '');
 process.stderr.write(dump.stderr ?? '');
 throw new Error(`codemode description probe dump exited ${dump.status}`);
}
const m = await import(pathToFileURL(resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/extensions/codemode/tool.js')).href);
// Go's encoder writes U+2028 and U+2029 as escapes; JSON.stringify writes them raw.
const enc = (value) => JSON.stringify(value).replace(/\u2028/g, '\\u2028').replace(/\u2029/g, '\\u2029');
for (const p of JSON.parse(line.slice('codemode-description-probes:'.length))) {
 let row;
 try {
  const options = {
   models: p.models,
   namespaces: new Map(Object.entries(p.namespaces)),
   deferred: new Set(p.deferred ?? []),
   guidelines: new Map(Object.entries(p.guidelines)),
   inlineBudget: p.budget ?? undefined,
  };
  row = [m.createCodemodeDescription(p.tools, options).replace(/Read \S*codemode\.md first/g, 'Read <docs> first'), ''];
 } catch (e) {
  row = ['', String(e && e.message)];
 }
 process.stdout.write(enc(row) + '\n');
}
