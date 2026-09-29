import { createServer } from 'node:http';
import { readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
const root = process.env.PI_PACKAGE_ROOT ?? resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
const ai = join(root, 'node_modules/@earendil-works/pi-ai/dist');
const { normalizeContext } = await import(pathToFileURL(join(ai, 'compat.js')));
const results = [];
for (const api of ['openai-completions', 'openai-responses']) {
  const body = readFileSync(`test/parity/testdata/responseid/${api}.sse`);
  const server = createServer((req, res) => { req.resume(); res.setHeader('content-type', 'text/event-stream'); res.end(body); });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  try {
    const model = { id: 'requested', name: 'requested', api, provider: 'openai', baseUrl: `http://127.0.0.1:${server.address().port}`, reasoning: false, input: ['text'], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 4096 };
    const { stream } = await import(pathToFileURL(join(ai, `api/${api}.js`)));
    const response = await stream(model, normalizeContext({ systemPrompt: 'You are a helpful assistant. Be concise.', messages: [{ role: 'user', content: 'Reply with exactly: response id test', timestamp: 0 }] }), { apiKey: 'test' }).result();
    if (response.stopReason === 'error') throw new Error(response.errorMessage);
    results.push({ api, responseId: response.responseId ?? '', responseModel: response.responseModel ?? '' });
  } finally { await new Promise(resolve => server.close(resolve)); }
}
console.log(JSON.stringify(results));
