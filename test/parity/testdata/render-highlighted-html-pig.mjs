// Pig's renderHighlightedHtml over the corpus of tui/render_tool_path_oracle_test.go; the owning Go test emits one JSON line of the output per probe.
import { spawnSync } from 'node:child_process';
const result = spawnSync('go', ['test', './tui', '-run', '^TestRenderHighlightedHtmlParity$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 29 });
if (result.status !== 0) {
 process.stderr.write(result.stdout ?? '');
 process.stderr.write(result.stderr ?? '');
 throw result.error ?? new Error(`render highlighted html probe exited ${result.status}`);
}
const observations = result.stdout.split('\n').flatMap(line => {
 const match = /^hlhtml-observation:(.*)$/.exec(line);
 return match ? [match[1]] : [];
});
if (observations.length === 0) throw new Error('render highlighted html probe emitted no observations');
for (const observation of observations) process.stdout.write(observation + '\n');
