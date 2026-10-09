// Pi 1.1.0 Image component over the corpus the Go test TestImageRenderProbeDump prints: one JSON line of frames per probe, the kitty image id normalised.
import { spawnSync } from 'node:child_process';
const dump = spawnSync('go', ['test', './tui', '-run', '^TestImageRenderProbeDump$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 29 });
const line = dump.stdout?.split('\n').find(l => l.startsWith('image-probes:'));
if (dump.status !== 0 || !line) {
 process.stderr.write(dump.stdout ?? '');
 process.stderr.write(dump.stderr ?? '');
 throw new Error(`image probe dump exited ${dump.status}`);
}
const result = spawnSync('node', ['tui/testdata/image_render.mjs', '1.1.0'], { input: line.slice('image-probes:'.length), encoding: 'utf8', maxBuffer: 1 << 29 });
if (result.status !== 0) {
 process.stderr.write(result.stderr ?? '');
 throw new Error(`image render oracle exited ${result.status}`);
}
const normalize = (s) => s.replace(/([,;]i=)\d+/g, '$1ID');
for (const probe of JSON.parse(result.stdout)) process.stdout.write(JSON.stringify({ frames: probe.frames.map(f => f === null ? null : f.map(normalize)), id: null }) + '\n');
