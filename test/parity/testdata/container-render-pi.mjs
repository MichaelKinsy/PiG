// Pi 1.1.0 Container.render and MouseRegion.render over the edit scripts in tui/testdata/container_scripts.json: one JSON line of render frames per script.
import { PI_PACKAGE_VERSION } from "./pi-version.mjs";
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
const scripts = readFileSync('tui/testdata/container_scripts.json', 'utf8');
const result = spawnSync('node', ['tui/testdata/container_render.mjs', PI_PACKAGE_VERSION], { input: scripts, encoding: 'utf8' });
if (result.status !== 0) {
 process.stderr.write(result.stderr ?? '');
 throw new Error(`container render probe exited ${result.status}`);
}
for (const frames of JSON.parse(result.stdout)) process.stdout.write(JSON.stringify(frames) + '\n');
