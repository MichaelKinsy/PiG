// Pi 1.1.0 oauth-page.ts over the corpus the Go test TestOAuthPageCorpusDump prints: one JSON line per page (the success page of each message, then its error page).
import { spawnSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';
import { resolve } from 'node:path';
const dump = spawnSync('go', ['test', './ai', '-run', '^TestOAuthPageCorpusDump$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 28 });
const line = dump.stdout?.split('\n').find(l => l.startsWith('oauth-page-probes:'));
if (dump.status !== 0 || !line) {
 process.stderr.write(dump.stdout ?? '');
 process.stderr.write(dump.stderr ?? '');
 throw new Error(`oauth page probe dump exited ${dump.status}`);
}
const page = await import(pathToFileURL(resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/utils/oauth-page.js')).href);
// Go's encoder writes U+2028 and U+2029 as escapes; JSON.stringify writes them raw.
const out = (text) => process.stdout.write(JSON.stringify(text).replace(/\u2028/g, '\\u2028').replace(/\u2029/g, '\\u2029') + '\n');
for (const c of JSON.parse(line.slice('oauth-page-probes:'.length))) {
 out(page.oauthSuccessHtml(c.message));
 out(page.oauthErrorHtml(c.message, c.details));
}
