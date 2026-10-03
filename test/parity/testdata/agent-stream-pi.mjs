import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';

const root = resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
assert.equal(JSON.parse(readFileSync(join(root, 'package.json'), 'utf8')).version, '1.0.0');
const parent = pathToFileURL(join(root, 'package.json')).href;
const { Agent, setDefaultStreamFn } = await import(import.meta.resolve('@earendil-works/pi-agent-core', parent));
const { EventStream } = await import(import.meta.resolve('@earendil-works/pi-ai', parent));
const model = { id: 'mock', name: 'mock', provider: 'openai', api: 'openai-responses', baseUrl: '', reasoning: false, input: [], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 0, maxTokens: 0 };
function reply() {
  const message = { role: 'assistant', content: [{ type: 'text', text: 'fallback' }], api: model.api, provider: model.provider, model: model.id, usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } }, stopReason: 'stop', timestamp: 1 };
  const stream = new EventStream(e => e.type === 'done', e => e.message);
  queueMicrotask(() => { stream.push({ type: 'start', partial: { ...message, content: [] } }); stream.push({ type: 'done', reason: 'stop', message }); });
  return stream;
}
// Pi packages/agent/test/agent.test.ts:107 and agent-loop.test.ts:87.
let calls = 0;
setDefaultStreamFn(() => { calls++; return reply(); });
try {
  const agent = new Agent({});
  await agent.prompt('Hello');
  assert.equal(calls, 1);
  console.log('AGENT_STREAM default ' + calls);
} finally { setDefaultStreamFn(undefined); }
// A StreamFn consumes a model descriptor, not a constructed provider runtime.
for (const failed of [false, true]) {
  const agent = new Agent({ initialState: { model }, streamFn: () => { if (failed) throw new Error('provider exploded'); return reply(); } });
  await agent.prompt('Hello');
  const last = agent.state.messages.at(-1);
  assert.equal(last.stopReason, failed ? 'error' : 'stop');
  console.log('AGENT_STREAM descriptor ' + JSON.stringify([last.stopReason, failed ? last.errorMessage : last.content[0].text]));
}
let convertedText;
const custom = new Agent({ initialState: { model, messages: [{ role: 'custom', text: 'Hook content', timestamp: 1 }] }, convertToLlm: messages => messages.map(message => ({ role: 'user', content: message.text, timestamp: message.timestamp })), streamFn: (_model, context) => { convertedText = context.messages[0].content; return reply(); } });
await custom.continue();
console.log('AGENT_STREAM converter ' + JSON.stringify(convertedText));
