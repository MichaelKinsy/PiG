// Pi 1.1.0 ToolExecutionComponent with the codemode renderers over the corpus the Go test TestCodemodeCardsProbeDump prints: one JSON line of rows per probe.
import { spawnSync } from 'node:child_process';
const dump = spawnSync('go', ['test', './internal/codingagent', '-run', '^TestCodemodeCardsProbeDump$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 28 });
const line = dump.stdout?.split('\n').find(l => l.startsWith('codemodecard-probes:'));
if (dump.status !== 0 || !line) {
 process.stderr.write(dump.stdout ?? '');
 process.stderr.write(dump.stderr ?? '');
 throw new Error(`codemode cards probe dump exited ${dump.status}`);
}
const result = spawnSync('node', ['internal/codingagent/testdata/codemode_cards.mjs', '1.1.0'], { input: line.slice('codemodecard-probes:'.length), encoding: 'utf8', maxBuffer: 1 << 28 });
if (result.status !== 0) {
 process.stderr.write(result.stderr ?? '');
 throw new Error(`codemode cards oracle exited ${result.status}`);
}
for (const rows of JSON.parse(result.stdout)) process.stdout.write(JSON.stringify(rows) + '\n');
