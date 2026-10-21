// PiG's terminal capability detection over the environments of tui/color_detection_pi_test.go; the owning Go test emits one JSON line per environment.
import { spawnSync } from 'node:child_process';
const result = spawnSync('./test/parity/testdata/prebuilt-go-test.sh', ['./tui', '-run', '^TestColorDetectionProbesParity$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 28 });
if (result.status !== 0) {
 process.stderr.write(result.stdout ?? '');
 process.stderr.write(result.stderr ?? '');
 throw result.error ?? new Error(`capability probe exited ${result.status}`);
}
const observations = result.stdout.split('\n').flatMap(line => {
 const match = /^capability-observation:(.*)$/.exec(line);
 return match ? [match[1]] : [];
});
if (observations.length === 0) throw new Error('capability probe emitted no observations');
for (const observation of observations) process.stdout.write(observation + '\n');
