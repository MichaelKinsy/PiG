import { createServer } from 'node:http';
import { readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
const root = process.env.PI_PACKAGE_ROOT ?? resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
const ai = join(root, 'node_modules/@earendil-works/pi-ai/dist');
const { normalizeContext } = await import(pathToFileURL(join(ai, 'compat.js')));
const cases = JSON.parse(readFileSync('test/parity/testdata/provider-error-body/cases.json', 'utf8'));
const output = {};
for (const api of ['openai-completions', 'openai-responses']) {
 output[api] = [];
 const { stream } = await import(pathToFileURL(join(ai, `api/${api}.js`)));
 for (const test of cases) {
  const server = createServer((req, res) => { req.resume(); res.statusCode = test.status; res.setHeader('content-type', 'application/json'); res.end(test.body); });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  try {
   const model = { id: 'test-model', name: 'Test Model', api, provider: 'openai', baseUrl: `http://127.0.0.1:${server.address().port}`, reasoning: false, input: ['text'], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 };
   const result = await stream(model, normalizeContext({ messages: [{ role: 'user', content: 'hi', timestamp: 0 }] }), { apiKey: 'test', maxRetries: 0 }).result();
   if (result.stopReason !== 'error') throw new Error('missing error');
   output[api].push(result.errorMessage);
  } finally { await new Promise(resolve => server.close(resolve)); }
 }
}
console.log(JSON.stringify(output));
