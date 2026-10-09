// Pi 1.1.0 DynamicBorder, ThemedText, IdleStatus and StatusIndicator over the corpus the Go test TestStatusProbeDump prints: one JSON line of results per probe.
import { spawnSync } from 'node:child_process';
const dump = spawnSync('go', ['test', './tui', '-run', '^TestStatusProbeDump$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 28 });
const line = dump.stdout?.split('\n').find(l => l.startsWith('status-probes:'));
if (dump.status !== 0 || !line) {
 process.stderr.write(dump.stdout ?? '');
 process.stderr.write(dump.stderr ?? '');
 throw new Error(`status probe dump exited ${dump.status}`);
}
const result = spawnSync('node', ['tui/testdata/status_components.mjs', '1.1.0'], { input: line.slice('status-probes:'.length), encoding: 'utf8', maxBuffer: 1 << 28 });
if (result.status !== 0) {
 process.stderr.write(result.stderr ?? '');
 throw new Error(`status components oracle exited ${result.status}`);
}
for (const probe of JSON.parse(result.stdout)) process.stdout.write(JSON.stringify(probe) + '\n');
