// Pig's Container and MouseRegion over the same scripts; the owning Go test emits one JSON line of render frames per script.
import { spawnSync } from 'node:child_process';
const result = spawnSync('go', ['test', './tui', '-run', '^TestContainerRenderParity$', '-v', '-count=1'], { encoding: 'utf8' });
if (result.status !== 0) {
 process.stderr.write(result.stdout ?? '');
 process.stderr.write(result.stderr ?? '');
 throw result.error ?? new Error(`container render probe exited ${result.status}`);
}
const observations = result.stdout.split('\n').flatMap(line => {
 const match = /container-observation:(.*)$/.exec(line);
 return match ? [match[1]] : [];
});
if (observations.length === 0) throw new Error('container render probe emitted no observations');
for (const observation of observations) process.stdout.write(observation + '\n');
