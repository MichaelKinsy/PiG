// Pi 1.1.0 ToolExecutionComponent with the built-in tool definitions over the corpus the Go test TestBuiltinToolCardsProbeDump prints: one JSON line of rows per probe.
import { spawnSync } from 'node:child_process';
const dump = spawnSync('go', ['test', './internal/codingagent', '-run', '^TestBuiltinToolCardsProbeDump$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 28 });
const line = dump.stdout?.split('\n').find(l => l.startsWith('toolcard-probes:'));
if (dump.status !== 0 || !line) {
 process.stderr.write(dump.stdout ?? '');
 process.stderr.write(dump.stderr ?? '');
 throw new Error(`tool cards probe dump exited ${dump.status}`);
}
const result = spawnSync('node', ['internal/codingagent/testdata/builtin_tool_cards.mjs', '1.1.0'], { input: line.slice('toolcard-probes:'.length), encoding: 'utf8', maxBuffer: 1 << 28 });
if (result.status !== 0) {
 process.stderr.write(result.stderr ?? '');
 throw new Error(`tool cards oracle exited ${result.status}`);
}
for (const rows of JSON.parse(result.stdout)) process.stdout.write(JSON.stringify(rows) + '\n');
