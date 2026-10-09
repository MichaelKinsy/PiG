// Pi 1.1.0 createToolHtmlRenderer over the scenarios the Go test TestToolHTMLRendererProbeDump prints: one JSON line of outputs per scenario.
import { spawnSync } from 'node:child_process';
const dump = spawnSync('go', ['test', './internal/codingagent/export', '-run', '^TestToolHTMLRendererProbeDump$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 29 });
const line = dump.stdout?.split('\n').find(l => l.startsWith('toolrenderer-probes:'));
if (dump.status !== 0 || !line) {
 process.stderr.write(dump.stdout ?? '');
 process.stderr.write(dump.stderr ?? '');
 throw new Error(`tool renderer probe dump exited ${dump.status}`);
}
const result = spawnSync('node', ['internal/codingagent/export/testdata/tool_renderer.mjs', '1.1.0'], { input: line.slice('toolrenderer-probes:'.length), encoding: 'utf8', maxBuffer: 1 << 29 });
if (result.status !== 0) {
 process.stderr.write(result.stderr ?? '');
 throw new Error(`tool renderer oracle exited ${result.status}`);
}
// Pig omits empty members and spells them html, collapsed, expanded; JSON.stringify writes U+2028 and U+2029 raw where Go's encoder escapes them.
const compact = out => Object.fromEntries(['html', 'collapsed', 'expanded'].filter(key => out[key]).map(key => [key, out[key]]));
for (const scenario of JSON.parse(result.stdout)) process.stdout.write(JSON.stringify(scenario.map(compact)).replaceAll('\u2028', '\\u2028').replaceAll('\u2029', '\\u2029') + '\n');
