// Pi 1.2.0 packages/codemode declarations (schemaToType, renderToolSignature, renderToolSample, renderDeclarations, renderToolOutputType): the oracle
// script regenerates its deterministic and seeded-random corpus with Pi, and this prints each result as one JSON line in the order PiG prints them.
import { spawnSync } from 'node:child_process';
const run = spawnSync('node', ['codemode/testdata/upstream-oracle.mjs'], { encoding: 'utf8', maxBuffer: 1 << 28 });
if (run.status !== 0) {
 process.stderr.write(run.stderr ?? '');
 throw new Error(`codemode oracle exited ${run.status}`);
}
const golden = JSON.parse(run.stdout);
// Go's encoder writes U+2028 and U+2029 as escapes; JSON.stringify writes them raw.
const enc = (value) => JSON.stringify(value).replace(/\u2028/g, '\\u2028').replace(/\u2029/g, '\\u2029');
const outcome = (o) => (o.error !== null && o.error !== undefined ? ['error', o.error] : ['ok', o.ok]);
const line = (kind, ...parts) => process.stdout.write(enc([kind, ...parts]) + '\n');
for (const c of golden.schemaToType) line('schemaToType', outcome(c));
for (const c of golden.signature) line('signature', outcome(c));
for (const c of golden.sample) line('sample', outcome(c));
for (const c of golden.declarations) line('declarations', outcome(c));
for (const c of golden.outputType) line('outputType', outcome(c.type));
