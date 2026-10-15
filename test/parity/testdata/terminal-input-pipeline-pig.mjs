// Pig's tui.HandleTerminalInput over the same cases as terminal-input-pipeline-pi.mjs; the owning Go test emits the observations.
import { spawnSync } from 'node:child_process';
const result = spawnSync('go', ['test', './tui', '-run', '^TestTerminalInputPipelineParity$', '-v', '-count=1'], { encoding: 'utf8' });
if (result.status !== 0) {
 process.stderr.write(result.stdout ?? '');
 process.stderr.write(result.stderr ?? '');
 throw result.error ?? new Error(`terminal input probe exited ${result.status}`);
}
const observations = result.stdout.split('\n').flatMap(line => {
 const match = /terminal-input-observation:(.*)$/.exec(line);
 return match ? [match[1]] : [];
});
if (observations.length === 0) throw new Error('terminal input probe emitted no observations');
for (const observation of observations) process.stdout.write(observation + '\n');
