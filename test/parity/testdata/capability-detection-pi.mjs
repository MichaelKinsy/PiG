// Pi 1.1.0 terminal-image.ts detectCapabilities over the environments the Go test TestColorDetectionProbesDump prints: one JSON line [images, trueColor, hyperlinks] per environment.
import { spawnSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';
import { resolve } from 'node:path';
const dump = spawnSync('go', ['test', './tui', '-run', '^TestColorDetectionProbesDump$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 28 });
const line = dump.stdout?.split('\n').find(l => l.startsWith('capability-probes:'));
if (dump.status !== 0 || !line) {
 process.stderr.write(dump.stdout ?? '');
 process.stderr.write(dump.stderr ?? '');
 throw new Error(`capability probe dump exited ${dump.status}`);
}
const { detectCapabilities, resetCapabilitiesCache } = await import(pathToFileURL(resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-tui/dist/terminal-image.js')).href);
for (const { env, platform, tmuxProbe } of JSON.parse(line.slice('capability-probes:'.length))) {
 process.env = { ...env };
 Object.defineProperty(process, 'platform', { value: platform });
 resetCapabilitiesCache();
 const caps = detectCapabilities(() => tmuxProbe === true);
 process.stdout.write(JSON.stringify([caps.images ?? null, caps.trueColor, caps.hyperlinks]) + '\n');
}
