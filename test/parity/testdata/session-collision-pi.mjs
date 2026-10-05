import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import { syncBuiltinESMExports } from 'node:module';
import { readFileSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';

const root = process.env.PI_PACKAGE_ROOT || resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
assert.equal(JSON.parse(readFileSync(join(root, 'package.json'), 'utf8')).version, '1.0.3');
const { SessionManager } = await import(pathToFileURL(join(root, 'dist/core/session-manager.js')).href);
const session = SessionManager.inMemory(process.cwd());
const original = crypto.randomUUID;
const ids = ['01020304', '01020304', '05060708'];
let calls = 0;
crypto.randomUUID = () => {
  assert.ok(calls < ids.length, 'unexpected extra entropy request');
  return `${ids[calls++]}-0000-4000-8000-000000000000`;
};
syncBuiltinESMExports();
try {
  const append = text => session.appendMessage({ role: 'user', content: [{ type: 'text', text }], timestamp: 1 });
  const first = append('seed'), second = append('next');
  const entries = session.getEntries();
  const observation = { ids: [first, second], parents: entries.map(entry => entry.parentId), seed: session.getEntry(first).message.content[0].text, calls };
  console.log('SESSION_COLLISION ' + JSON.stringify(observation));
  assert.deepEqual(observation, { ids: ['01020304', '05060708'], parents: [null, '01020304'], seed: 'seed', calls: 3 });
} finally {
  crypto.randomUUID = original;
  syncBuiltinESMExports();
}
