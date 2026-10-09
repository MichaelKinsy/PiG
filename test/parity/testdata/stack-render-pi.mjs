// Pi 1.1.0 HStack, VStack and ScrollView render over the corpus the Go test TestStackProbeDump prints: one JSON line of renders per probe.
import { spawnSync } from 'node:child_process';
const dump = spawnSync('go', ['test', './tui', '-run', '^TestStackProbeDump$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 28 });
const line = dump.stdout?.split('\n').find(l => l.startsWith('stack-probes:'));
if (dump.status !== 0 || !line) {
 process.stderr.write(dump.stdout ?? '');
 process.stderr.write(dump.stderr ?? '');
 throw new Error(`stack probe dump exited ${dump.status}`);
}
const result = spawnSync('node', ['tui/testdata/stack_render.mjs', '1.1.0'], { input: line.slice('stack-probes:'.length), encoding: 'utf8', maxBuffer: 1 << 28 });
if (result.status !== 0) {
 process.stderr.write(result.stderr ?? '');
 throw new Error(`stack render oracle exited ${result.status}`);
}
for (const renders of JSON.parse(result.stdout)) process.stdout.write(JSON.stringify(renders) + '\n');
