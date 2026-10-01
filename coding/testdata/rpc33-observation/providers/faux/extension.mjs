// Pi 0.99.1 extension loaded into the real `pi --mode rpc` process. It registers packages/ai/src/providers/faux.ts
// (createFauxCore) as a provider through the same registerProvider/model-runtime path any provider uses, and it
// records the single-thread interleaving of `stream.push` calls with RPC serialization of assistant records.
// The recorder is synchronous (appendFileSync), so it adds no microtask or timer.
import { appendFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
import { fauxResponses } from './responses.mjs';

const root = process.env.PI_PACKAGE_ROOT;
const log = process.env.FAUX_PROBE_LOG;
const ai = await import(pathToFileURL(`${root}/node_modules/@earendil-works/pi-ai/dist/index.js`).href);
const fixture = JSON.parse(process.env.FAUX_PROBE_FIXTURE);

let pushes = 0;
const record = (entry) => appendFileSync(log, JSON.stringify(entry) + '\n');

// RPC serializes one JSON line per session event (rpc-mode.ts:356 `output(toJsonEvent(event))`). The wrapper records
// the moment an assistant message record is serialized, with its full serialized value.
const stringify = JSON.stringify;
JSON.stringify = function (value, ...rest) {
  const text = stringify.call(this, value, ...rest);
  const assistant = value && typeof value === 'object' && (
    ((value.type === 'message_start' || value.type === 'message_end') && value.message?.role === 'assistant') ||
    (value.type === 'message_update' && value.assistantMessageEvent !== undefined && value.usage !== undefined));
  if (assistant) {
    record({ kind: 'serialize', pushed: pushes, record: JSON.parse(text) });
  }
  return text;
};

const core = ai.createFauxCore({
  api: 'faux-probe',
  provider: 'faux-probe',
  tokensPerSecond: fixture.tokensPerSecond,
  tokenSize: { min: fixture.tokenSize, max: fixture.tokenSize },
});
core.setResponses(fauxResponses(ai, fixture));

function stream(model, context, options) {
  const inner = core.streamSimple(model, context, options);
  const push = inner.push.bind(inner);
  inner.push = (event) => {
    pushes++;
    record({ kind: 'push', pushed: pushes, type: event.type, content: event.partial ? event.partial.content.length : undefined });
    return push(event);
  };
  return inner;
}

export default function (pi) {
  pi.registerProvider('faux-probe', {
    baseUrl: 'http://localhost:0',
    apiKey: 'unused',
    api: 'faux-probe',
    models: [{ id: 'faux-1', name: 'Faux', reasoning: false, input: ['text'], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 16384 }],
    streamSimple: stream,
  });
}
