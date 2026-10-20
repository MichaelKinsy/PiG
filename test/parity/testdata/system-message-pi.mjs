// Pi 1.1.0 utils/text.ts getSystemMessageText and renderSystemMessageUpdate over the corpus the Go test TestSystemMessageDocsDump prints: one line per message, the two results as JSON strings separated by a comma.
import { spawnSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';
import { resolve } from 'node:path';
const dump = spawnSync('./test/parity/testdata/prebuilt-go-test.sh', ['./ai', '-run', '^TestSystemMessageDocsDump$', '-v', '-count=1'], { encoding: 'utf8', maxBuffer: 1 << 28 });
const line = dump.stdout?.split('\n').find(l => l.startsWith('system-message-probes:'));
if (dump.status !== 0 || !line) {
 process.stderr.write(dump.stdout ?? '');
 process.stderr.write(dump.stderr ?? '');
 throw new Error(`system message probe dump exited ${dump.status}`);
}
const mod = await import(pathToFileURL(resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/utils/text.js')).href);
// Go's encoder writes U+2028 and U+2029 as escapes; JSON.stringify writes them raw.
const enc = (text) => JSON.stringify(text).replace(/\u2028/g, '\\u2028').replace(/\u2029/g, '\\u2029');
for (const doc of JSON.parse(line.slice('system-message-probes:'.length))) {
 const message = JSON.parse(doc);
 process.stdout.write(enc(mod.getSystemMessageText(message)) + ',' + enc(mod.renderSystemMessageUpdate(message)) + '\n');
}
