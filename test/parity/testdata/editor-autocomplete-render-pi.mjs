// Pi 1.1.0 Editor with the coding-agent theme and a slash-command provider over the corpus the Go test TestEditorAutocompleteRenderProbeDump prints: one JSON line of the frames per probe.
import { spawnSync } from 'node:child_process';
const dump = spawnSync('go', ['test', './tui', '-run', '^TestEditorAutocompleteRenderProbeDump$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 29 });
const line = dump.stdout?.split('\n').find(l => l.startsWith('acrender-probes:'));
if (dump.status !== 0 || !line) {
 process.stderr.write(dump.stdout ?? '');
 process.stderr.write(dump.stderr ?? '');
 throw new Error(`editor autocomplete render probe dump exited ${dump.status}`);
}
const result = spawnSync('node', ['tui/testdata/editor_autocomplete_render.mjs', '1.1.0'], { input: line.slice('acrender-probes:'.length), encoding: 'utf8', maxBuffer: 1 << 29 });
if (result.status !== 0) {
 process.stderr.write(result.stderr ?? '');
 throw new Error(`editor autocomplete render oracle exited ${result.status}`);
}
for (const probe of JSON.parse(result.stdout)) process.stdout.write(JSON.stringify(probe) + '\n');
