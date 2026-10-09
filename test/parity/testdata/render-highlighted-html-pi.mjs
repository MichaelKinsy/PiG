// Pi 1.1.0 renderHighlightedHtml over the corpus the Go test TestRenderHighlightedHtmlProbeDump prints: one JSON line of the output per probe.
import { spawnSync } from 'node:child_process';
const dump = spawnSync('go', ['test', './tui', '-run', '^TestRenderHighlightedHtmlProbeDump$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 29 });
const line = dump.stdout?.split('\n').find(l => l.startsWith('hlhtml-probes:'));
if (dump.status !== 0 || !line) {
 process.stderr.write(dump.stdout ?? '');
 process.stderr.write(dump.stderr ?? '');
 throw new Error(`render highlighted html probe dump exited ${dump.status}`);
}
const result = spawnSync('node', ['tui/testdata/render_highlighted_html.mjs', '1.1.0'], { input: line.slice('hlhtml-probes:'.length), encoding: 'utf8', maxBuffer: 1 << 29 });
if (result.status !== 0) {
 process.stderr.write(result.stderr ?? '');
 throw new Error(`render highlighted html oracle exited ${result.status}`);
}
// Node writes a lone surrogate to a terminal as U+FFFD; Pig's renderer converts at that same boundary (terminalText), so compare the written form.
// Go's JSON encoder spells U+2028 and U+2029 as escapes where JSON.stringify writes them raw; both are the same JSON text, so spell them as Go does.
for (const probe of JSON.parse(result.stdout)) process.stdout.write(JSON.stringify(probe.out === undefined ? probe : { out: probe.out.toWellFormed() }).replaceAll('\u2028', '\\u2028').replaceAll('\u2029', '\\u2029') + '\n');
