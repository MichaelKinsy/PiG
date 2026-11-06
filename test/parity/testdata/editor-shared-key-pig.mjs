// PiG Editor with one key bound to two actions over the probes of tui/editor_handle_input_contract_test.go; the owning Go test emits one JSON line per probe.
import { spawnSync } from 'node:child_process';
const result = spawnSync('./test/parity/testdata/prebuilt-go-test.sh', ['./tui', '-run', '^TestEditorSharedKeyProbesParity$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 29 });
if (result.status !== 0) {
 process.stderr.write(result.stdout ?? '');
 process.stderr.write(result.stderr ?? '');
 throw result.error ?? new Error(`editor probe exited ${result.status}`);
}
const observations = result.stdout.split('\n').flatMap(line => {
 const match = /^editor-observation:(.*)$/.exec(line);
 return match ? [match[1]] : [];
});
if (observations.length === 0) throw new Error('editor probe emitted no observations');
for (const observation of observations) process.stdout.write(observation + '\n');
