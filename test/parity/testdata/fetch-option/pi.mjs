import { createRequire } from 'node:module';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
const root = process.env.PI_PACKAGE_ROOT ?? resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
const ai = join(root, 'node_modules/@earendil-works/pi-ai/dist');
const mod = path => import(pathToFileURL(join(ai, path)));
const { normalizeContext } = await mod('compat.js');
const original = globalThis.fetch;
const result = {};
try {
 for (const api of ['anthropic-messages', 'openai-completions', 'openai-responses', 'azure-openai-responses', 'mistral-conversations', 'openai-codex-responses', 'pi-messages', 'google-generative-ai', 'google-vertex', 'openrouter-images']) {
  let custom = 0, fallback = 0;
  globalThis.fetch = async () => { fallback++; throw new Error('ambient fetch must not be called'); };
  const fetch = async () => { custom++; return new Response('{"error":{"message":"upstream rejected request"}}', { status: 401, headers: { 'content-type': 'application/json' } }); };
  const model = { id: 'test-model', name: 'Test Model', api, provider: 'test-provider', baseUrl: 'https://upstream.test/v1', reasoning: false, input: ['text'], output: ['image'], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 10000, maxTokens: 1000 };
  let apiKey = 'test-key';
  if (api === 'openai-codex-responses') apiKey = `header.${Buffer.from(JSON.stringify({ 'https://api.openai.com/auth': { chatgpt_account_id: 'account' } })).toString('base64url')}.signature`;
  const adapter = await mod(`api/${api}.js`);
  const output = api === 'openrouter-images'
   ? await adapter.generateImages(model, { input: [{ type: 'text', text: 'draw' }] }, { apiKey, fetch, maxRetries: 0 })
   : await adapter.streamSimple(model, normalizeContext({ messages: [{ role: 'user', content: 'hello', timestamp: 1 }] }), { apiKey, fetch, maxRetries: 0, transport: 'sse' }).result();
  result[api] = { custom, fallback, rejected: output.errorMessage?.includes('Custom fetch is not supported') ? output.errorMessage : '' };
 }
} finally { globalThis.fetch = original; }
const require = createRequire(join(ai, 'api/openrouter-images.js'));
const { default: OpenAI } = await import(pathToFileURL(require.resolve('openai').replace(/\.js$/, '.mjs')));
const savedCreate = OpenAI.Chat.Completions.prototype.create;
const controller = new AbortController(); controller.abort();
let sameSignal = false;
try {
 OpenAI.Chat.Completions.prototype.create = function (_params, options) {
  sameSignal = options.signal === controller.signal;
  return { withResponse: async () => { throw new Error('Request aborted'); } };
 };
 const { generateImages } = await mod('api/openrouter-images.js');
 const aborted = await generateImages({ id: 'black-forest-labs/flux.2-pro', name: 'FLUX.2 Pro', api: 'openrouter-images', provider: 'openrouter', baseUrl: 'https://openrouter.ai/api/v1', input: ['text', 'image'], output: ['image'], cost: { input: 0.015, output: 0.03, cacheRead: 0, cacheWrite: 0 } }, { input: [{ type: 'text', text: 'Generate a dog' }] }, { apiKey: 'test', signal: controller.signal });
 result['openrouter-images-aborted'] = { error: aborted.errorMessage, reason: aborted.stopReason, signal: sameSignal };
} finally { OpenAI.Chat.Completions.prototype.create = savedCreate; }
let redirectCalls = 0;
const { stream: streamCompletions } = await mod('api/openai-completions.js');
const redirectResult = await streamCompletions({ id: 'test-model', name: 'Test Model', api: 'openai-completions', provider: 'openai', baseUrl: 'https://request.test/v1', reasoning: false, input: ['text'], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 10000, maxTokens: 1000 }, normalizeContext({ messages: [{ role: 'user', content: 'hello', timestamp: 1 }] }), { apiKey: 'test', maxRetries: 0, fetch: async () => { redirectCalls++; return new Response('manual redirect', { status: 302, headers: { location: 'https://redirect.test/landing' } }); } }).result();
if (redirectResult.stopReason !== 'error') throw new Error('manual redirect must remain an HTTP rejection');
result['openai-completions-manual-redirect'] = { custom: redirectCalls };
console.log(JSON.stringify(Object.fromEntries(Object.keys(result).sort().map(key => [key, result[key]]))));
