#!/usr/bin/env node
import { createServer } from 'node:http';
import { homedir } from 'node:os';
import { pathToFileURL } from 'node:url';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

function version() {
  const m = readFileSync('coding/pigversion/pigversion.go', 'utf8').match(/UpstreamVersion = "([^"]+)"/);
  if (!m) throw new Error('cannot read UpstreamVersion');
  return m[1];
}
const root = process.env.PI_PACKAGE_ROOT ?? join(homedir(), '.local/share/mise/installs/npm-earendil-works-pi-coding-agent', version(), 'lib/node_modules/@earendil-works/pi-coding-agent');
const ai = join(root, 'node_modules/@earendil-works/pi-ai/dist');
const mod = async (rel) => import(pathToFileURL(join(ai, rel)).href);

process.env.HTTPS_PROXY = 'http://proxy.local:8080';
process.env.NO_PROXY = '127.0.0.1,skip.example';

const providersAll = await mod('providers/all.js');
const compat = await mod('compat.js');
const cloudflareAuth = await mod('providers/cloudflare-auth.js');
const cloudflareStream = await mod('providers/cloudflare-stream.js');
const imageModels = await mod('image-models.js');
const images = await mod('images.js');
const promptCache = await mod('api/openai-prompt-cache.js');
const cloudflare = await mod('api/cloudflare.js');
const sessionResources = await mod('session-resources.js');
const diagnostics = await mod('utils/diagnostics.js');
const proxy = await mod('utils/node-http-proxy.js');


const server = createServer((req, res) => {
  let body = '';
  req.on('data', (chunk) => { body += chunk; });
  req.on('end', () => {
    const parsed = JSON.parse(body || '{}');
    res.setHeader('content-type', 'application/json');
    res.end(JSON.stringify({
      id: 'img-resp-1',
      choices: [{ message: { content: parsed.modalities.join(','), images: [{ image_url: { url: 'data:image/png;base64,aGVsbG8=' } }] } }],
      usage: { prompt_tokens: 10, completion_tokens: 3, prompt_tokens_details: { cached_tokens: 4, cache_write_tokens: 1 } },
    }));
  });
});
await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
const port = server.address().port;
process.env.OPENROUTER_API_KEY = 'sk-test';
const model = { ...imageModels.getImageModel('openrouter', 'google/gemini-2.5-flash-image'), baseUrl: `http://127.0.0.1:${port}` };
const generated = await images.generateImages(model, { input: [{ type: 'text', text: 'draw' }] }, { apiKey: 'sk-test' });
await new Promise((resolve) => server.close(resolve));

process.env.CLOUDFLARE_ACCOUNT_ID = 'acct';
process.env.CLOUDFLARE_GATEWAY_ID = 'gate';
const cleanupOrder = [];
const unregisterA = sessionResources.registerSessionResourceCleanup((id) => cleanupOrder.push(`a:${id}`));
sessionResources.registerSessionResourceCleanup((id) => cleanupOrder.push(`b:${id}`));
sessionResources.cleanupSessionResources('sess');
unregisterA();
sessionResources.cleanupSessionResources('sess2');

const diag = diagnostics.createAssistantMessageDiagnostic('transport', new Error('boom'), { phase: 'connect' });

const cloudflareResolved = await cloudflareAuth.cloudflareAIGatewayAuth().resolve({
  model: { baseUrl: cloudflare.CLOUDFLARE_AI_GATEWAY_COMPAT_BASE_URL },
  ctx: { env: (name) => process.env[name] },
  credential: {
    type: 'api_key',
    key: 'cf-key',
    env: { CLOUDFLARE_ACCOUNT_ID: 'acct', CLOUDFLARE_GATEWAY_ID: 'gate' },
  },
});

const catalogProviders = providersAll.getBuiltinProviders().sort();
const runtimeProviderIDs = providersAll.builtinModels().getProviders().map((provider) => provider.id).sort();
const compatProviderIDs = compat.getProviders().sort();
const compatImageProviderIDs = compat.getImageProviders().sort();
const runtimeImageProviderIDs = providersAll.builtinImagesModels().getProviders().map((provider) => provider.id).sort();
const catalog = {
  providers: catalogProviders,
  totalModels: catalogProviders.reduce((sum, provider) => sum + providersAll.getBuiltinModels(provider).length, 0),
  byProvider: Object.fromEntries(catalogProviders.map((provider) => {
    const models = providersAll.getBuiltinModels(provider);
    const apiSet = new Set(models.map((model) => model.api));
    const imageInputCount = models.filter((model) => model.input?.includes('image')).length;
    const reasoningCount = models.filter((model) => model.reasoning).length;
    return [provider, {
      count: models.length,
      first: models[0]?.id ?? null,
      last: models.at(-1)?.id ?? null,
      apis: Array.from(apiSet).sort(),
      imageInputCount,
      reasoningCount,
    }];
  })),
  compatImageProviderIDs,
  compatProviderIDs,
  runtimeProviderIDs,
  runtimeImageProviderIDs,
};

const out = {
  catalog,
  imageProviders: imageModels.getImageProviders(),
  imageModelCount: imageModels.getImageModels('openrouter').length,
  imageModel: {
    id: model.id,
    api: model.api,
    provider: model.provider,
    output: model.output,
  },
  generated: {
    api: generated.api,
    provider: generated.provider,
    model: generated.model,
    output: generated.output.map((x) => x.type === 'image' ? { type: x.type, mimeType: x.mimeType, data: x.data } : x),
    responseId: generated.responseId,
    stopReason: generated.stopReason,
    usage: generated.usage,
  },
  promptCache: promptCache.clampOpenAIPromptCacheKey('🙂'.repeat(70)),
  cloudflareAuth: {
    baseUrl: cloudflareResolved.auth.baseUrl,
    env: cloudflareResolved.env,
    headers: cloudflareResolved.auth.headers,
    source: cloudflareResolved.source,
  },
  cloudflareURL: cloudflareStream.resolveCloudflareModel(
    { baseUrl: cloudflare.CLOUDFLARE_AI_GATEWAY_COMPAT_BASE_URL },
    cloudflareResolved.env,
  ).baseUrl,
  cloudflareUnresolvedURL: cloudflareStream.resolveCloudflareModel(
    { baseUrl: cloudflare.CLOUDFLARE_AI_GATEWAY_COMPAT_BASE_URL },
    undefined,
  ).baseUrl,
  cleanupOrder,
  diagnostic: { type: diag.type, error: { name: diag.error?.name, message: diag.error?.message }, details: diag.details },
  proxyURL: String(proxy.resolveHttpProxyUrlForTarget('https://example.com/path')),
  noProxyURL: proxy.resolveHttpProxyUrlForTarget('https://skip.example/path') ?? null,
};
console.log(stableStringify(out));

function stableStringify(value) {
  return JSON.stringify(sortValue(value), null, 2);
}

function sortValue(value) {
  if (Array.isArray(value)) return value.map(sortValue);
  if (value && typeof value === 'object') {
    const out = {};
    for (const key of Object.keys(value).sort()) out[key] = sortValue(value[key]);
    return out;
  }
  return value;
}
