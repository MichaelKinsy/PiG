// Pig's CancellableLoader.Dispose over the case of cancellable-loader-dispose-pi.mjs; the owning Go test emits the observation.
import { spawnSync } from 'node:child_process';
const result = spawnSync('go', ['test', './tui', '-run', '^TestCancellableLoaderDisposeParity$', '-v', '-count=1'], { encoding: 'utf8' });
if (result.status !== 0) {
 process.stderr.write(result.stdout ?? '');
 process.stderr.write(result.stderr ?? '');
 throw result.error ?? new Error(`cancellable loader probe exited ${result.status}`);
}
const observations = result.stdout.split('\n').flatMap(line => {
 const match = /cancellable-loader-observation:(.*)$/.exec(line);
 return match ? [match[1]] : [];
});
if (observations.length === 0) throw new Error('cancellable loader probe emitted no observations');
for (const observation of observations) process.stdout.write(observation + '\n');
