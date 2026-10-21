// Pi 1.1.0 Editor under rebound keybindings over the probes the Go test TestEditorRebindingProbesDump prints: one JSON line per probe, the editor's text, expanded text, cursor, lines, callbacks and rendering after every op.
import { PI_PACKAGE_VERSION } from "./pi-version.mjs";
import { spawnSync } from 'node:child_process';
const dump = spawnSync('./test/parity/testdata/prebuilt-go-test.sh', ['./tui', '-run', '^TestEditorRebindingProbesDump$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 28 });
const line = dump.stdout?.split('\n').find(l => l.startsWith('editor-probes:'));
if (dump.status !== 0 || !line) {
 process.stderr.write(dump.stdout ?? '');
 process.stderr.write(dump.stderr ?? '');
 throw new Error(`editor probe dump exited ${dump.status}`);
}
const result = spawnSync('node', ['tui/testdata/editor_bindings.mjs', PI_PACKAGE_VERSION], { input: line.slice('editor-probes:'.length), encoding: 'utf8', maxBuffer: 1 << 29 });
if (result.status !== 0) {
 process.stderr.write(result.stderr ?? '');
 throw new Error(`editor oracle exited ${result.status}`);
}
// Go's encoder writes U+2028 and U+2029 as escapes; JSON.stringify writes them raw.
for (const log of JSON.parse(result.stdout)) process.stdout.write(JSON.stringify(log).replace(/\u2028/g, '\\u2028').replace(/\u2029/g, '\\u2029') + '\n');
