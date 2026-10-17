// Pig's built-in tool cards over the corpus of internal/codingagent/builtin_tool_cards_oracle_test.go; the owning Go test emits one JSON line of rows per probe.
import { spawnSync } from 'node:child_process';
const result = spawnSync('go', ['test', './internal/codingagent', '-run', '^TestBuiltinToolCardsParity$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 28 });
if (result.status !== 0) {
 process.stderr.write(result.stdout ?? '');
 process.stderr.write(result.stderr ?? '');
 throw result.error ?? new Error(`tool cards probe exited ${result.status}`);
}
const observations = result.stdout.split('\n').flatMap(line => {
 const match = /^toolcard-observation:(.*)$/.exec(line);
 return match ? [match[1]] : [];
});
if (observations.length === 0) throw new Error('tool cards probe emitted no observations');
for (const observation of observations) process.stdout.write(observation + '\n');
