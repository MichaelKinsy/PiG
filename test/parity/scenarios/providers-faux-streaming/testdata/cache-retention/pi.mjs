import { readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const root = process.env.PI_PACKAGE_ROOT ?? resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
const ai = join(root, 'node_modules/@earendil-works/pi-ai/dist');
const mod = (path) => import(pathToFileURL(join(ai, path)).href);
const { getModel, normalizeContext } = await mod('compat.js');
const cases = JSON.parse(readFileSync('test/parity/scenarios/providers-faux-streaming/testdata/cache-retention/cases.json', 'utf8'));
const results = {};
for (const test of cases) {
  const provider = test.api === 'anthropic-messages' ? 'anthropic' : 'openai';
  const metadata = getModel(provider, test.model);
  const model = {
    ...(metadata ?? { id: test.model, name: test.model, provider, baseUrl: 'https://api.openai.com/v1', reasoning: false, input: ['text'], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 4096 }),
    api: test.api,
  };
  if (test.proxy) model.baseUrl = 'https://my-proxy.example.com/v1';
  const { stream } = await mod(`api/${test.api}.js`);
  let captured;
  await stream(model, normalizeContext({ systemPrompt: 'You are a helpful assistant.', messages: [{ role: 'user', content: 'Hello', timestamp: 0 }] }), {
    apiKey: 'fake-key', cacheRetention: test.retention, sessionId: test.session,
    env: { PI_CACHE_RETENTION: test.env ?? '' },
    onPayload(payload) { captured = payload; throw new Error('payload captured'); },
  }).result();
  if (!captured) throw new Error(`${test.name}: no payload`);
  results[test.name] = test.api === 'anthropic-messages'
    ? { system: captured.system[0].cache_control ?? null, user: captured.messages.at(-1).content.at(-1)?.cache_control ?? null }
    : Object.fromEntries(['prompt_cache_key', 'prompt_cache_retention', 'prompt_cache_options'].map(key => [key, captured[key] ?? null]));
}
function sorted(value) {
  if (Array.isArray(value)) return value.map(sorted);
  if (value && typeof value === 'object') return Object.fromEntries(Object.keys(value).sort().map(key => [key, sorted(value[key])]));
  return value;
}
console.log(JSON.stringify(sorted(results)));
