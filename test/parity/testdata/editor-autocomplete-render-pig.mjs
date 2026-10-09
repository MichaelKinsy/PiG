// Pig's Editor over the corpus of tui/editor_autocomplete_render_oracle_test.go; the owning Go test emits one JSON line of the frames per probe.
import { spawnSync } from 'node:child_process';
const result = spawnSync('go', ['test', './tui', '-run', '^TestEditorAutocompleteRenderParity$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 29 });
if (result.status !== 0) {
 process.stderr.write(result.stdout ?? '');
 process.stderr.write(result.stderr ?? '');
 throw result.error ?? new Error(`editor autocomplete render probe exited ${result.status}`);
}
const observations = result.stdout.split('\n').flatMap(line => {
 const match = /^acrender-observation:(.*)$/.exec(line);
 return match ? [match[1]] : [];
});
if (observations.length === 0) throw new Error('editor autocomplete render probe emitted no observations');
for (const observation of observations) process.stdout.write(observation + '\n');
