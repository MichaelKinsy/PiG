// Pi 0.87.1 zen.test.ts:15-23 uses complete with the catalog model and only the Say hello. context.
// Capture native request parameters without allowing any provider network request.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { complete } from '../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/compat.js';
import { MODELS } from '../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/models.generated.js';

const packageURL = new URL('../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/package.json', import.meta.url);
assert.equal(JSON.parse(readFileSync(packageURL, 'utf8')).version, '1.0.0');
const requests = {};
for (const provider of ['opencode', 'opencode-go']) {
  for (const model of Object.values(MODELS[provider])) {
    let captured;
    let networkCalled = false;
    // Google rejects a per-request fetch option; replace the global transport without changing the original native options.
    globalThis.fetch = async () => { networkCalled = true; throw new Error('Network is forbidden in request capture'); };
    const result = await complete(model, { messages: [{ role: 'user', content: 'Say hello.', timestamp: 1 }] }, {
      apiKey: 'matrix-key',
      onPayload: payload => { captured = payload; throw new Error('Payload captured'); },
    });
    assert.equal(networkCalled, false, `${provider}/${model.id} reached network`);
    assert.notEqual(captured, undefined, `${provider}/${model.id} did not expose its request: ${result.errorMessage ?? result.stopReason}`);
    requests[`${provider}/${model.id}`] = captured;
  }
}
console.log(JSON.stringify(requests));
