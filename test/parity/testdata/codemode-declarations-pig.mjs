// PiG's codemode declarations over the corpus of codemode/testdata/upstream-golden.json; the owning Go test emits one JSON line per case.
import { spawnSync } from 'node:child_process';
const result = spawnSync('go', ['test', './codemode', '-run', '^TestCodemodeDeclarationsParity$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 28 });
if (result.status !== 0) {
 process.stderr.write(result.stdout ?? '');
 process.stderr.write(result.stderr ?? '');
 throw result.error ?? new Error(`codemode declarations probe exited ${result.status}`);
}
const observations = result.stdout.split('\n').flatMap(line => {
 const match = /^codemode-observation:(.*)$/.exec(line);
 return match ? [match[1]] : [];
});
if (observations.length === 0) throw new Error('codemode declarations probe emitted no observations');
for (const observation of observations) process.stdout.write(observation + '\n');
