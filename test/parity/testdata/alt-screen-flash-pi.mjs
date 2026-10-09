// Pi 1.1.0 AltScreenFlashContainer over the corpus the Go test TestAltScreenFlashProbeDump prints: one JSON line of the steps per probe.
import { spawnSync } from 'node:child_process';
const dump = spawnSync('go', ['test', './tui', '-run', '^TestAltScreenFlashProbeDump$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 29 });
const line = dump.stdout?.split('\n').find(l => l.startsWith('flash-probes:'));
if (dump.status !== 0 || !line) {
 process.stderr.write(dump.stdout ?? '');
 process.stderr.write(dump.stderr ?? '');
 throw new Error(`alt screen flash probe dump exited ${dump.status}`);
}
const result = spawnSync('node', ['tui/testdata/alt_screen_flash.mjs', '1.1.0'], { input: line.slice('flash-probes:'.length), encoding: 'utf8', maxBuffer: 1 << 29 });
if (result.status !== 0) {
 process.stderr.write(result.stderr ?? '');
 throw new Error(`alt screen flash oracle exited ${result.status}`);
}
for (const probe of JSON.parse(result.stdout)) process.stdout.write(JSON.stringify(probe) + '\n');
