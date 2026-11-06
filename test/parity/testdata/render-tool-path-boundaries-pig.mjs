// Pig's renderToolPath over the boundary corpus of tui/render_tool_path_contract_test.go; the owning Go test emits one JSON line of the rendering per probe.
import { spawnSync } from 'node:child_process';
const result = spawnSync('./test/parity/testdata/prebuilt-go-test.sh', ['./tui', '-run', '^TestRenderToolPathBoundaryParity$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 29 });
if (result.status !== 0) {
 process.stderr.write(result.stdout ?? '');
 process.stderr.write(result.stderr ?? '');
 throw result.error ?? new Error(`render tool path probe exited ${result.status}`);
}
const observations = result.stdout.split('\n').flatMap(line => {
 const match = /^toolpath-observation:(.*)$/.exec(line);
 return match ? [match[1]] : [];
});
if (observations.length === 0) throw new Error('render tool path probe emitted no observations');
for (const observation of observations) process.stdout.write(observation + '\n');
