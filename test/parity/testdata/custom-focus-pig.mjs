// Run the real remote custom-UI host with its owner-loop handoff and modal input leases.
import { spawnSync } from 'node:child_process';
const result = spawnSync('go', ['test', './internal/codingagent', '-run', '^TestRemoteOverlayReclaimsInputAfterEditorReplacementUpstream$', '-v', '-count=1'], { encoding: 'utf8' });
if (result.status !== 0) {
 process.stderr.write(result.stdout ?? '');
 process.stderr.write(result.stderr ?? '');
 throw result.error ?? new Error(`custom focus probe exited ${result.status}`);
}
const observations = result.stdout.split('\n').flatMap(line => {
 const match = /custom-focus-observation:(.*)$/.exec(line);
 return match ? [match[1]] : [];
});
if (observations.length !== 1) throw new Error('custom focus probe must emit its one complete lifecycle observation');
console.log(observations[0]);
