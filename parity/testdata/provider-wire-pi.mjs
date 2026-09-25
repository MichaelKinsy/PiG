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
const aiRoot = join(root, 'node_modules/@earendil-works/pi-ai/dist');
const mod = async (rel) => import(pathToFileURL(join(aiRoot, rel)).href);

const openaiCompletions = await mod('api/openai-completions.js');
const openaiResponses = await mod('api/openai-responses.js');
const openaiCodexResponses = await mod('api/openai-codex-responses.js');
const azureOpenAIResponses = await mod('api/azure-openai-responses.js');
const anthropic = await mod('api/anthropic-messages.js');
const google = await mod('api/google-generative-ai.js');
const googleVertex = await mod('api/google-vertex.js');
const mistral = await mod('api/mistral-conversations.js');
const bedrock = await mod('api/bedrock-converse-stream.js');
// The api-level stream functions take a normalized TranscriptContext (0.87.1):
// the prompt and tool shorthand must be folded into a leading system message,
// or the request carries neither.
const { normalizeContext } = await mod('utils/transcript.js');

process.env.AWS_BEDROCK_SKIP_AUTH = '1';
process.env.AWS_REGION = 'us-east-1';

// FAUX_USAGE_SSE reproduces a provider that attaches a usage object to
// content-bearing chunks (e.g. gemini-3.5-flash via github-copilot). The parser
// must record usage and keep processing; aborting on the first usage chunk
// drops the whole stream. Kept byte-identical with the pig harness fauxUsageSSE.
const FAUX_USAGE_SSE = [
  'data: {"choices":[{"delta":{"content":"VER"},"finish_reason":null}],"usage":{"prompt_tokens":5,"completion_tokens":1}}',
  '',
  'data: {"choices":[{"delta":{"content":"DICT"},"finish_reason":null}],"usage":{"prompt_tokens":5,"completion_tokens":2}}',
  '',
  'data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":3}}',
  '',
  'data: [DONE]',
  '',
].join('\n');

const UNKNOWN_FINISH_SSE = [
  'data: {"choices":[{"delta":{},"finish_reason":"vendor_custom"}]}',
  '',
  'data: [DONE]',
  '',
].join('\n');

const out = {};
out.openaiCompletions = await probeOpenAICompletions();
out.openaiCompletionsGrammar = await probeOpenAICompletionsGrammar();
out.openaiCompletions084 = await probeOpenAICompletions084();
out.openaiCompletionsResponse = await probeOpenAICompletionsResponse();
out.openaiCompletionsUnknownFinish = await probeOpenAICompletionsUnknownFinish();
out.openaiResponses = await probeOpenAIResponses();
out.openaiCodexResponses = await probeOpenAICodexResponses();
out.azureOpenAIResponses = await probeAzureOpenAIResponses();
out.openaiResponsesGrammar = await probeOpenAIResponsesGrammar();
out.anthropic = await probeAnthropic();
out.google = await probeGoogle();
out.googleToolIds = await probeGoogleToolIds();
out.googleThinkingSig = await probeGoogleThinkingSig();
out.googleVertex = await probeGoogleVertex();
out.mistral = await probeMistral();
out.bedrock = await probeBedrock();
console.log(stableStringify(out));

async function probeOpenAICompletionsGrammar() {
  const { server, cap, url } = await captureServer();
  try {
    const context = {
      systemPrompt: 'system prompt',
      messages: [
        { role: 'user', content: [{ type: 'text', text: 'hello provider' }] },
        { role: 'assistant', content: [{ type: 'text', text: 'prior answer' }] },
      ],
      tools: [{
        name: 'calc',
        description: 'calculator',
        parameters: { type: 'object', properties: { expr: { type: 'string' } }, required: ['expr'] },
        constrainedSampling: { type: 'grammar', variants: { openai_lark: 'start: NUMBER' } },
      }],
    };
    return await runProbe(openaiCompletions.stream, {
      id: 'grok-2', api: 'openai-completions', provider: 'xai', baseUrl: url,
      compat: { supportsStore: false, supportsReasoningEffort: true, supportsUsageInStreaming: true, maxTokensField: 'max_tokens', supportsOpenAIGrammarTools: true },
    }, cap, {}, context);
  } finally { await closeServer(server); }
}

async function probeOpenAICompletions() {
  const { server, cap, url } = await captureServer();
  try {
    return await runProbe(openaiCompletions.stream, {
      id: 'grok-2', api: 'openai-completions', provider: 'xai', baseUrl: url,
      compat: { supportsStore: false, supportsReasoningEffort: true, supportsUsageInStreaming: true, maxTokensField: 'max_tokens' },
    }, cap, { reasoning: 'high' });
  } finally { await closeServer(server); }
}

async function probeOpenAICompletions084() {
  const { server, cap, url } = await captureServer();
  try {
    return await runProbe(openaiCompletions.streamSimple, {
      id: 'reasoning-model', api: 'openai-completions', provider: 'baseten', baseUrl: url,
      reasoning: true, maxTokens: 4096,
      samplingParams: { top_p: 0.7, min_p: 0.1 },
      compat: {
        supportsDeveloperRole: false,
        supportsStore: false,
        supportsReasoningEffort: true,
        supportsThinkingTokenBudget: true,
        maxTokensField: 'max_tokens',
        thinkingFormat: 'baseten',
        chatTemplateArgs: { enable_thinking: { $var: 'thinking.enabled' } },
      },
    }, cap, { reasoning: 'high', maxTokens: 4096, samplingParams: { top_p: 0.9 } });
  } finally { await closeServer(server); }
}

async function sseResponseServer(body) {
  const server = createServer((req, res) => {
    let b = '';
    req.on('data', (chunk) => { b += chunk; });
    req.on('end', () => {
      res.writeHead(200, { 'content-type': 'text/event-stream' });
      res.end(body);
    });
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  return { server, url: `http://127.0.0.1:${server.address().port}` };
}

// collectResponse drives the upstream stream function against the faux SSE and
// aggregates it into the same normalized shape the pig harness produces.
async function probeOpenAICompletionsResponse() {
  const { server, url } = await sseResponseServer(FAUX_USAGE_SSE);
  try {
    const model = withModelDefaults({
      id: 'grok-2', api: 'openai-completions', provider: 'xai', baseUrl: url,
      compat: { supportsStore: false, supportsReasoningEffort: true, supportsUsageInStreaming: true, maxTokensField: 'max_tokens' },
    });
    const stream = openaiCompletions.stream(model, normalizeContext(sampleContext()), {
      apiKey: 'sk-test', maxTokens: 123, temperature: 0.2, sessionId: 'sess-provider-wire-response',
    });
    let text = '', reasoning = '', stop = '', usageIn = 0, usageOut = 0, error = '';
    for await (const ev of stream) {
      if (ev?.type === 'text_delta') text += ev.delta ?? '';
      else if (ev?.type === 'thinking_delta') reasoning += ev.delta ?? '';
      else if (ev?.type === 'done') {
        stop = ev.reason ?? '';
        const u = ev.message?.usage ?? {};
        usageIn = u.input ?? 0;
        usageOut = u.output ?? 0;
      } else if (ev?.type === 'error') {
        error = ev.error?.errorMessage ?? String(ev.error ?? '');
      }
    }
    const res = { text, reasoning, stop, usageIn, usageOut };
    if (error) res.error = error;
    return res;
  } finally {
    await closeServer(server);
  }
}

async function probeOpenAICompletionsUnknownFinish() {
  const { server, url } = await sseResponseServer(UNKNOWN_FINISH_SSE);
  try {
    const model = withModelDefaults({
      id: 'grok-2', api: 'openai-completions', provider: 'xai', baseUrl: url,
      compat: { supportsStore: false, supportsReasoningEffort: true, supportsUsageInStreaming: true, maxTokensField: 'max_tokens' },
    });
    const stream = openaiCompletions.stream(model, normalizeContext(sampleContext()), {
      apiKey: 'sk-test', maxTokens: 123, temperature: 0.2, sessionId: 'sess-provider-wire-unknown-finish',
    });
    let text = '', reasoning = '', stop = '', usageIn = 0, usageOut = 0, error = '';
    for await (const ev of stream) {
      if (ev?.type === 'text_delta') text += ev.delta ?? '';
      else if (ev?.type === 'thinking_delta') reasoning += ev.delta ?? '';
      else if (ev?.type === 'done') {
        stop = ev.reason ?? '';
        const u = ev.message?.usage ?? {};
        usageIn = u.input ?? 0;
        usageOut = u.output ?? 0;
      } else if (ev?.type === 'error') {
        error = ev.error?.errorMessage ?? String(ev.error ?? '');
      }
    }
    const res = { text, reasoning, stop, usageIn, usageOut };
    if (error) res.error = error;
    return res;
  } finally {
    await closeServer(server);
  }
}

async function probeOpenAIResponses() {
  const { server, cap, url } = await captureServer();
  try {
    return await runProbe(openaiResponses.stream, {
      id: 'gpt-5', api: 'openai-responses', provider: 'openai', baseUrl: url, reasoning: true,
    }, cap, { reasoningEffort: 'high' });
  } finally { await closeServer(server); }
}

async function probeOpenAIResponsesGrammar() {
  const { server, cap, url } = await captureServer();
  try {
    const context = {
      systemPrompt: 'system prompt',
      messages: [
        { role: 'user', content: [{ type: 'text', text: 'hello provider' }] },
        { role: 'assistant', content: [{ type: 'text', text: 'prior answer' }] },
      ],
      tools: [{
        name: 'calc',
        description: 'calculator',
        parameters: { type: 'object', properties: { expr: { type: 'string' } }, required: ['expr'] },
        constrainedSampling: { type: 'grammar', variants: { openai_lark: 'start: NUMBER' } },
      }],
    };
    return await runProbe(openaiResponses.stream, {
      id: 'gpt-5', api: 'openai-responses', provider: 'openai', baseUrl: url, reasoning: true,
      compat: { supportsOpenAIGrammarTools: true },
    }, cap, { reasoningEffort: 'high' }, context);
  } finally { await closeServer(server); }
}

async function probeOpenAICodexResponses() {
  const { server, cap, url } = await captureServer();
  try {
    return await runProbe(openaiCodexResponses.stream, {
      id: 'gpt-5.2', api: 'openai-codex-responses', provider: 'openai-codex', baseUrl: url, reasoning: true,
    }, cap, { transport: 'sse', reasoningEffort: 'high' });
  } finally { await closeServer(server); }
}

async function probeAzureOpenAIResponses() {
  const { server, cap, url } = await captureServer();
  process.env.AZURE_OPENAI_API_KEY = 'azure-test';
  try {
    return await runProbe(azureOpenAIResponses.stream, {
      id: 'gpt-4', api: 'azure-openai-responses', provider: 'azure-openai-responses',
    }, cap, { env: { AZURE_OPENAI_BASE_URL: url } });
  } finally { await closeServer(server); }
}

async function probeAnthropic() {
  const { server, cap, url } = await captureServer();
  try {
    return await runProbe(anthropic.stream, {
      id: 'claude-haiku-4-5', api: 'anthropic-messages', provider: 'anthropic', baseUrl: url, reasoning: true,
    }, cap, { thinkingEnabled: true });
  } finally { await closeServer(server); }
}

async function probeGoogle() {
  const { server, cap, url } = await captureServer();
  try {
    return await runProbe(google.stream, {
      id: 'gemini-1.5-flash', api: 'google-generative-ai', provider: 'google', baseUrl: `${url}/v1beta`,
    }, cap);
  } finally { await closeServer(server); }
}

async function probeGoogleVertex() {
  const { server, cap, url } = await captureServer();
  try {
    return await runProbe(googleVertex.stream, {
      id: 'gemini-1.5-flash', api: 'google-vertex', provider: 'google-vertex', baseUrl: `${url}/v1/publishers/google`,
    }, cap);
  } finally { await closeServer(server); }
}

// probeGoogleToolIds drives a requiresToolCallId model (gemini-3-pro) through a
// tool-call + tool-result turn so the payload exercises google-shared.ts:176/215
// id emission. The "|" in the id is normalized to "_" (google-shared.ts:101).
async function probeGoogleToolIds() {
  const { server, cap, url } = await captureServer();
  try {
    return await runProbe(google.stream, {
      id: 'gemini-3-pro', api: 'google-generative-ai', provider: 'google', baseUrl: `${url}/v1beta`,
    }, cap, {}, toolCallContext());
  } finally { await closeServer(server); }
}

function toolCallContext() {
  return {
    systemPrompt: 'system prompt',
    messages: [
      {
        role: 'assistant', provider: 'google', model: 'gemini-3-pro',
        content: [{ type: 'toolCall', id: 'call_abc|item_def', name: 'lookup', arguments: { q: 'x' } }],
      },
      {
        role: 'toolResult', toolCallId: 'call_abc|item_def', toolName: 'lookup',
        content: [{ type: 'text', text: 'found' }],
      },
    ],
    tools: [{
      name: 'lookup',
      description: 'look things up',
      parameters: { type: 'object', properties: { q: { type: 'string' } }, required: ['q'] },
    }],
  };
}

// probeGoogleThinkingSig drives a same-model (gemini-3-pro) assistant thinking
// block carrying a valid base64 signature so the payload exercises the outbound
// thought-signature resend (google-shared.ts:154-161).
async function probeGoogleThinkingSig() {
  const { server, cap, url } = await captureServer();
  try {
    return await runProbe(google.stream, {
      id: 'gemini-3-pro', api: 'google-generative-ai', provider: 'google', baseUrl: `${url}/v1beta`,
    }, cap, {}, thinkingSigContext());
  } finally { await closeServer(server); }
}

function thinkingSigContext() {
  return {
    systemPrompt: 'system prompt',
    messages: [
      {
        role: 'assistant', provider: 'google', api: 'google-generative-ai', model: 'gemini-3-pro',
        content: [
          { type: 'thinking', thinking: 'reasoning', thinkingSignature: 'c2lnbmF0dXJl' },
          { type: 'text', text: 'answer' },
        ],
      },
    ],
    tools: [],
  };
}

async function probeMistral() {
  const { server, cap, url } = await captureServer();
  try {
    return await runProbe(mistral.stream, {
      id: 'codestral-latest', api: 'mistral-conversations', provider: 'mistral', baseUrl: url, reasoning: true,
    }, cap);
  } finally { await closeServer(server); }
}

async function probeBedrock() {
  const { server, cap, url } = await captureServer();
  try {
    return await runProbe(bedrock.stream, {
      id: 'anthropic.claude-3-5-sonnet-20241022-v2:0', name: 'Claude 3.5 Sonnet', api: 'bedrock-converse-stream', provider: 'amazon-bedrock', baseUrl: url, reasoning: true,
    }, cap, { reasoning: 'high' });
  } finally {
    await closeServer(server);
  }
}

async function runProbe(streamFn, model, cap, extra = {}, context = sampleContext()) {
  model = withModelDefaults(model);
  let payload;
  const stream = streamFn(model, normalizeContext(context), {
    apiKey: providerKey(model.provider),
    maxTokens: 123,
    temperature: 0.2,
    sessionId: 'sess-provider-wire',
    onPayload: (p) => { payload = p; return undefined; },
    ...extra,
  });
  let terminal = '';
  for await (const event of stream) {
    if (event?.type === 'error') terminal = event.error?.errorMessage ?? event.errorMessage ?? String(event.error ?? '');
  }
  if (!cap.path && model.provider === 'amazon-bedrock') {
    cap.path = '/model/' + encodeURIComponent(model.id) + '/converse-stream';
  }
  return {
    provider: model.provider,
    path: cap.path,
    auth: model.provider === 'amazon-bedrock' ? 'authorization' : (cap.auth ?? authKind(cap.headers ?? {})),
    ...(model.provider === 'openai-codex' ? {
      accountHeader: cap.headers?.['chatgpt-account-id'] ?? null,
      sessionHeader: cap.headers?.['session-id'] ?? null,
    } : {}),
    payload: summarizePayload(model.provider, payload),
  };
}

function sampleContext() {
  return {
    systemPrompt: 'system prompt',
    messages: [
      { role: 'user', content: [{ type: 'text', text: 'hello provider' }] },
      { role: 'assistant', content: [{ type: 'text', text: 'prior answer' }] },
    ],
    tools: [{
      name: 'lookup',
      description: 'look things up',
      parameters: { type: 'object', properties: { q: { type: 'string' } }, required: ['q'] },
    }],
  };
}

function withModelDefaults(model) {
  return {
    name: model.name ?? model.id,
    input: model.input ?? ['text', 'image'],
    cost: model.cost ?? { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    contextWindow: model.contextWindow ?? 128000,
    maxTokens: model.maxTokens ?? 4096,
    headers: model.headers ?? {},
    reasoning: model.reasoning ?? false,
    ...model,
  };
}

function providerKey(provider) {
  if (provider === 'anthropic') return 'anth-test';
  if (provider === 'google') return 'gemini-test';
  if (provider === 'google-vertex') return 'vertex-test';
  if (provider === 'mistral') return 'mistral-test';
  if (provider === 'azure-openai-responses') return 'azure-test';
  if (provider === 'openai-codex') return makeCodexJWT('acct_probe');
  return 'sk-test';
}

function makeCodexJWT(accountId) {
  const header = Buffer.from(JSON.stringify({ alg: 'none', typ: 'JWT' })).toString('base64url');
  const payload = Buffer.from(JSON.stringify({ 'https://api.openai.com/auth': { chatgpt_account_id: accountId } })).toString('base64url');
  return `${header}.${payload}.sig`;
}

function summarizePayload(provider, payload) {
  if (!payload) return { kind: 'nil' };
  if (payload.modelId) {
    return {
      kind: 'bedrock-converse',
      model: payload.modelId,
      messages: payload.messages?.length ?? 0,
      system: payload.system?.length ?? 0,
      tools: Boolean(payload.toolConfig),
      inference: Boolean(payload.inferenceConfig),
      additional: Boolean(payload.additionalModelRequestFields),
    };
  }
  const m = JSON.parse(JSON.stringify(payload));
  const modelValue = provider === 'google' || provider === 'google-vertex' ? null : (m.model ?? null);
  const out = {
    kind: 'json',
    keys: normalizedKeys(provider, m),
    model: modelValue,
    tools: normalizedToolCount(provider, m),
    maxFields: normalizedMaxFields(provider, m),
    reasoning: Boolean(m.reasoning || m.reasoning_effort || m.reasoningEffort || m.thinking || m.prompt_mode),
    stream: m.stream ?? null,
    store: m.store ?? null,
    sessionKey: Boolean(m.prompt_cache_key || m.prompt_cache_retention),
  };
  if (m.top_p !== undefined) out.topP = m.top_p;
  if (m.min_p !== undefined) out.minP = m.min_p;
  if (m.thinking_token_budget !== undefined) out.thinkingTokenBudget = m.thinking_token_budget;
  if (m.chat_template_args !== undefined) out.chatTemplateArgs = m.chat_template_args;
  const roles = messageRoles(m.messages);
  if (roles.length) out.messageRoles = roles;
  const input = inputRoles(m.input);
  if (input.length) out.inputRoles = input;
  const content = geminiRoles(m.contents);
  if (content.length) out.contentRoles = content;
  const toolCallIds = geminiToolCallIDs(m.contents);
  if (toolCallIds.length) out.toolCallIds = toolCallIds;
  const thoughtSignatures = geminiThoughtSignatures(m.contents);
  if (thoughtSignatures.length) out.thoughtSignatures = thoughtSignatures;
  if (m.system !== undefined) out.system = true;
  if (m.systemInstruction !== undefined || m.config?.systemInstruction !== undefined) out.systemInstruction = true;
  const responsesShape = responsesToolShape(m);
  if (responsesShape) out.responsesToolShape = responsesShape;
  const completionsShape = completionsToolShape(m);
  if (completionsShape) out.completionsToolShape = completionsShape;
  return out;
}

function normalizedKeys(provider, m) {
  if (provider === 'google' || provider === 'google-vertex') return ['contents', 'generation', 'system', 'tools'];
  if (provider === 'mistral') return ['max_tokens', 'messages', 'model', 'reasoning', 'stream', 'temperature', 'tools'];
  return sortedKeys(m);
}

function responsesToolShape(m) {
  if (!('input' in m)) return undefined;
  const tools = m.tools;
  if (!Array.isArray(tools) || tools.length === 0) return undefined;
  return tools.map((t) => {
    const shape = { type: t.type };
    shape.strict = ('strict' in t) ? t.strict : 'absent';
    if (t.format && typeof t.format === 'object') shape.format = { type: t.format.type, syntax: t.format.syntax };
    return shape;
  });
}

// completionsToolShape mirrors the Go driver: completions nests strict under
// function and the grammar format under custom.format.grammar.
function completionsToolShape(m) {
  if ('input' in m) return undefined;
  const tools = m.tools;
  if (!Array.isArray(tools) || tools.length === 0) return undefined;
  const out = [];
  for (const t of tools) {
    const hasFn = t.function && typeof t.function === 'object';
    const hasCustom = t.custom && typeof t.custom === 'object';
    if (!hasFn && !hasCustom) continue; // not an openai-completions-shaped tool
    const shape = { type: t.type };
    if (hasFn) shape.strict = ('strict' in t.function) ? t.function.strict : 'absent';
    if (hasCustom && t.custom.format && typeof t.custom.format === 'object') {
      const f = t.custom.format;
      shape.format = { type: f.type };
      if (f.grammar && typeof f.grammar === 'object') shape.format.syntax = f.grammar.syntax;
    }
    out.push(shape);
  }
  return out.length ? out : undefined;
}

function normalizedToolCount(provider, m) {
  if (provider === 'google' || provider === 'google-vertex') return Array.isArray(m.config?.tools) ? m.config.tools.length : 0;
  return Array.isArray(m.tools) ? m.tools.length : 0;
}

function normalizedMaxFields(provider, m) {
  const fields = presentKeys(m, 'max_tokens', 'max_completion_tokens', 'max_output_tokens');
  if (Object.prototype.hasOwnProperty.call(m, 'maxTokens')) fields.push('max_tokens');
  if (Object.prototype.hasOwnProperty.call(m, 'maxOutputTokens')) fields.push('max_output_tokens');
  if (Object.prototype.hasOwnProperty.call(m.config ?? {}, 'maxOutputTokens')) fields.push('max_output_tokens');
  if (Object.prototype.hasOwnProperty.call(m.generationConfig ?? {}, 'maxOutputTokens')) fields.push('max_output_tokens');
  return [...new Set(fields)].sort();
}

function messageRoles(v) {
  if (!Array.isArray(v)) return [];
  return v.map((x) => x?.role ?? '');
}

function inputRoles(v) {
  if (!Array.isArray(v)) return [];
  return v.map((x) => x?.type ?? x?.role ?? '');
}

function geminiRoles(v) {
  if (!Array.isArray(v)) return [];
  return v.map((x) => x?.role ?? '');
}

// geminiThoughtSignatures collects every part's thoughtSignature from the gemini
// contents so the wire comparison asserts the resent thinking/text signatures
// (google-shared.ts:148/161/178).
function geminiThoughtSignatures(v) {
  if (!Array.isArray(v)) return [];
  const sigs = [];
  for (const content of v) {
    for (const part of content?.parts ?? []) {
      if (part.thoughtSignature) sigs.push(part.thoughtSignature);
    }
  }
  return sigs;
}

// geminiToolCallIDs collects functionCall.id / functionResponse.id from the
// gemini contents so the wire comparison asserts the emitted tool-call ids
// (google-shared.ts:176/215), not just role structure.
function geminiToolCallIDs(v) {
  if (!Array.isArray(v)) return [];
  const ids = [];
  for (const content of v) {
    for (const part of content?.parts ?? []) {
      if (part.functionCall) ids.push('call:' + (part.functionCall.id ?? ''));
      if (part.functionResponse) ids.push('resp:' + (part.functionResponse.id ?? ''));
    }
  }
  return ids;
}

function sortedKeys(m) {
  return Object.keys(m).filter((k) => m[k] !== undefined && m[k] !== null).sort();
}

function presentKeys(m, ...names) {
  return names.filter((name) => Object.prototype.hasOwnProperty.call(m, name));
}

async function captureServer() {
  const cap = {};
  const server = createServer((req, res) => {
    let body = '';
    req.on('data', (chunk) => { body += chunk; });
    req.on('end', () => {
      cap.path = req.url;
      cap.headers = req.headers;
      cap.body = body;
      res.writeHead(418, { 'content-type': 'application/json' });
      res.end(JSON.stringify({ error: { message: 'probe stop' } }));
    });
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  return { server, cap, url: `http://127.0.0.1:${server.address().port}` };
}

async function closeServer(server) {
  await new Promise((resolve) => server.close(resolve));
}

function authKind(headers) {
  const h = Object.fromEntries(Object.entries(headers).map(([k, v]) => [k.toLowerCase(), v]));
  if (h.authorization) return 'authorization';
  if (h['x-api-key']) return 'x-api-key';
  if (h['api-key']) return 'api-key';
  if (h['x-goog-api-key']) return 'x-goog-api-key';
  if (h['cf-aig-authorization']) return 'cf-aig-authorization';
  return 'none';
}

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
