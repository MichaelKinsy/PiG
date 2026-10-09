// Pi 1.1.0 Editor scroll borders over tui/testdata/editor_scroll_probes.json: one JSON line of rendered rows per probe.
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
const probes = readFileSync('tui/testdata/editor_scroll_probes.json', 'utf8');
const result = spawnSync('node', ['tui/testdata/editor_scroll_borders.mjs', '1.1.0'], { input: probes, encoding: 'utf8', maxBuffer: 1 << 28 });
if (result.status !== 0) {
 process.stderr.write(result.stderr ?? '');
 throw new Error(`editor scroll oracle exited ${result.status}`);
}
for (const rows of JSON.parse(result.stdout)) process.stdout.write(JSON.stringify(rows) + '\n');
