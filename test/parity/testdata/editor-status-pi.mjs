// Pi 1.1.0 CustomEditor with an embedded WorkingStatusIndicator over the corpus the Go test TestEditorStatusBorderProbeDump prints: one JSON line of rows per probe.
import { spawnSync } from 'node:child_process';
const dump = spawnSync('go', ['test', './tui', '-run', '^TestEditorStatusBorderProbeDump$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 28 });
const line = dump.stdout?.split('\n').find(l => l.startsWith('editorstatus-probes:'));
if (dump.status !== 0 || !line) {
 process.stderr.write(dump.stdout ?? '');
 process.stderr.write(dump.stderr ?? '');
 throw new Error(`editor status probe dump exited ${dump.status}`);
}
const result = spawnSync('node', ['tui/testdata/editor_status_border.mjs', '1.1.0'], { input: line.slice('editorstatus-probes:'.length), encoding: 'utf8', maxBuffer: 1 << 28 });
if (result.status !== 0) {
 process.stderr.write(result.stderr ?? '');
 throw new Error(`editor status oracle exited ${result.status}`);
}
for (const rows of JSON.parse(result.stdout)) process.stdout.write(JSON.stringify(rows) + '\n');
