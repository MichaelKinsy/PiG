// Compare the real interactive driver's input boundary, including its renderer focus restoration.
// The owning Go test exposes observations because handleKey is intentionally private, not a public test-only API.
import { spawnSync } from 'node:child_process';
const result = spawnSync('go', ['test', './internal/codingagent', '-run', '^TestInteractiveOverlayInputParity$', '-v', '-count=1'], { encoding: 'utf8' });
if (result.status !== 0) {
 process.stderr.write(result.stdout ?? '');
 process.stderr.write(result.stderr ?? '');
 throw result.error ?? new Error(`interactive input probe exited ${result.status}`);
}
const observations = result.stdout.split('\n').flatMap(line => {
 const match = /overlay-input-observation:(.*)$/.exec(line);
 return match ? [match[1]] : [];
});
if (observations.length === 0) throw new Error('interactive input probe emitted no observations');
for (const observation of observations) process.stdout.write(observation + '\n');
