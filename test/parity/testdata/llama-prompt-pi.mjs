// Pi llama-cpp-classify.ts classify, and through it renderPrompt, against a fake llama-server per probe the Go test TestLlamaCppRenderPromptDump prints, with the
// fake server program it prints: one JSON line [/apply-template request body, first /completion prompt, error message] per probe.
import { spawnSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';
import { resolve } from 'node:path';
const dump = spawnSync('./test/parity/testdata/prebuilt-go-test.sh', ['./ai', '-run', '^TestLlamaCppRenderPromptDump$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 28 });
const lines = dump.stdout?.split('\n') ?? [];
const probes = lines.find(l => l.startsWith('llama-prompt-probes:'));
const oracle = lines.find(l => l.startsWith('llama-prompt-oracle:'));
if (dump.status !== 0 || !probes || !oracle) {
 process.stderr.write(dump.stdout ?? '');
 process.stderr.write(dump.stderr ?? '');
 throw new Error(`llama prompt probe dump exited ${dump.status}`);
}
const { classify } = await import(pathToFileURL(resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/api/llama-cpp-classify.js')).href);
const AsyncFunction = (async () => {}).constructor;
const run = new AsyncFunction('input', 'classify', JSON.parse(oracle.slice('llama-prompt-oracle:'.length)) + '\nreturn rows;');
for (const row of await run(JSON.parse(probes.slice('llama-prompt-probes:'.length)), classify)) process.stdout.write(row + '\n');
