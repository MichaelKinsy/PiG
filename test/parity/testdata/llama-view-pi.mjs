// Pi 1.1.0 LlamaView over the scenarios the Go test TestLlamaViewProbeDump prints: one JSON line of frames and settlements per scenario.
import { spawnSync } from 'node:child_process';
const dump = spawnSync('go', ['test', './internal/codingagent/llama', '-run', '^TestLlamaViewProbeDump$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 29 });
const line = dump.stdout?.split('\n').find(l => l.startsWith('llamaview-probes:'));
if (dump.status !== 0 || !line) {
 process.stderr.write(dump.stdout ?? '');
 process.stderr.write(dump.stderr ?? '');
 throw new Error(`llama view probe dump exited ${dump.status}`);
}
const result = spawnSync('node', ['internal/codingagent/llama/testdata/llama_ui.mjs', '1.1.0'], { input: line.slice('llamaview-probes:'.length), encoding: 'utf8', maxBuffer: 1 << 29 });
if (result.status !== 0) {
 process.stderr.write(result.stderr ?? '');
 throw new Error(`llama view oracle exited ${result.status}`);
}
// Go's JSON encoder spells U+2028 and U+2029 as escapes where JSON.stringify writes them raw; both are the same JSON text, so spell them as Go does.
// Pig's settlement object is a Go map, whose members JSON writes sorted: model before type.
const sorted = value => (value !== null && typeof value === 'object' && 'type' in value ? { model: value.model, type: value.type } : value);
for (const record of JSON.parse(result.stdout)) process.stdout.write(JSON.stringify({ ...record, settled: record.settled.map(sorted) }).replaceAll('\u2028', '\\u2028').replaceAll('\u2029', '\\u2029') + '\n');
