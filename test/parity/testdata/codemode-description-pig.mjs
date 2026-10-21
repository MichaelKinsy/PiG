// PiG's codemode tool description over the corpus of coding/extension/builtin/codemode/description_oracle_test.go; the owning Go test emits one JSON line per probe.
import { spawnSync } from 'node:child_process';
const result = spawnSync('./test/parity/testdata/prebuilt-go-test.sh', ['./coding/extension/builtin/codemode', '-run', '^TestCodemodeDescriptionProbesParity$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 28 });
if (result.status !== 0) {
 process.stderr.write(result.stdout ?? '');
 process.stderr.write(result.stderr ?? '');
 throw result.error ?? new Error(`codemode description probe exited ${result.status}`);
}
const observations = result.stdout.split('\n').flatMap(line => {
 const match = /^codemode-description-observation:(.*)$/.exec(line);
 return match ? [match[1]] : [];
});
if (observations.length === 0) throw new Error('codemode description probe emitted no observations');
for (const observation of observations) process.stdout.write(observation + '\n');
