// Pi 1.1.0 buildSystemPromptSections over the corpus the Go test TestSystemPromptProbesDump prints: one JSON line [ok, entries] or [error, message] per probe, without the product preamble and the docs section (PiG differs there by design: D22, D92).
import { spawnSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';
import { resolve } from 'node:path';
const dump = spawnSync('./test/parity/testdata/prebuilt-go-test.sh', ['./internal/codingagent/prompts', '-run', '^TestSystemPromptProbesDump$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 28 });
const line = dump.stdout?.split('\n').find(l => l.startsWith('system-prompt-probes:'));
if (dump.status !== 0 || !line) {
 process.stderr.write(dump.stdout ?? '');
 process.stderr.write(dump.stderr ?? '');
 throw new Error(`system prompt probe dump exited ${dump.status}`);
}
const { buildSystemPromptSections } = await import(pathToFileURL(resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/core/system-prompt.js')).href);
// Go's encoder writes U+2028 and U+2029 as escapes; JSON.stringify writes them raw.
const enc = (value) => JSON.stringify(value).replace(/\u2028/g, '\\u2028').replace(/\u2029/g, '\\u2029');
for (const probe of JSON.parse(line.slice('system-prompt-probes:'.length))) {
 const { customSections, ...rest } = probe;
 const options = { ...rest, customPrompt: probe.customPrompt || undefined, sections: Object.fromEntries(customSections ?? []), skills: (probe.skills ?? []).map((skill) => ({ ...skill, sourceInfo: { path: skill.filePath, source: 'local', scope: 'user', origin: 'top-level' } })) };
 let row;
 try {
  row = ['ok', Object.entries(buildSystemPromptSections(options)).filter(([name]) => !(name === 'docs' || (name === 'preamble' && !probe.customPrompt)))];
 } catch (error) {
  row = ['error', String(error.message)];
 }
 process.stdout.write(enc(row) + '\n');
}
