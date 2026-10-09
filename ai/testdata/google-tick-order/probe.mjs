// Microtask tick-order probe for Pi 0.99.1's Google Generative AI provider (D82 W6/W5).
//
// Runs the REAL provider (`pi-ai/dist/api/google-generative-ai.js`, source
// packages/ai/src/api/google-generative-ai.ts:59-330) over the REAL `@google/genai` 2.21.0 SDK
// (dist/node/index.mjs: tslib __asyncGenerator `processStreamResponse` 13780-13830 and
// `generateContentStreamInternal` 15768+), with only `globalThis.fetch` replaced. The replacement
// returns a `Response` over a Web `ReadableStream` whose chunks are enqueued in a controlled
// macrotask, so the body-read readiness (bytes already queued versus not yet arrived) is an input
// of each case and never a timing accident.
//
// Every observation is stamped (segment, tick). A segment starts in a macrotask (the initial call,
// the fetch-resolution callback, and each later delivery callback). A self-rescheduling
// microtask chain of CAP rounds starts first in every segment; `tick` is the number of chain steps
// that have run. The chain shares the microtask FIFO with the code under test, so a stamp is
// the exact FIFO round in which the observation ran; array order within one stamp is the exact order.
// The chain never reorders anything: it only occupies one queue slot per round.
//
// Three timelines per case:
//   sdk      the SDK alone: the tick at which `await generateContentStream()` resumes and at which each
//            chunk / the end reaches a `for await`. This is what Pi's `for await (const chunk of googleStream)`
//            (google-generative-ai.ts:107) sees.
//   provider every `AssistantMessageEventStream.push` by the provider, with a deep copy of `event.partial`
//            (or `message`) at that tick. This is the provider-side state at each push (start at :106).
//   consumer a plain `for await` over the returned stream (event-stream.ts:44-91), with the same deep copy
//            taken when the consumer's continuation resumes, plus `result()` settlement. This is the
//            observable D82 boundary: what a consumer sees of the shared `output` object at delivery.
//
// Usage: PI_PACKAGE_ROOT=<pi-coding-agent package dir> node probe.mjs [pi.json]
//        PI_PACKAGE_ROOT=... node probe.mjs --check pi.json
// The optional argument writes the result; stdout always receives it too. `--check` compares a fresh run with the
// checked-in golden (ignoring the Node version string) and exits 1 on any difference; it never rewrites the golden.
import assert from 'node:assert/strict';
import { readFile, writeFile } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';

const root = process.env.PI_PACKAGE_ROOT;
assert.ok(root, 'PI_PACKAGE_ROOT must name the installed @earendil-works/pi-coding-agent directory');
const scope = root + '/node_modules/@earendil-works/';
const genaiDir = root + '/node_modules/@google/genai';
for (const [dir, version] of [[root, '1.1.0'], [scope + 'pi-ai', '1.1.0'], [genaiDir, '2.21.0']]) {
  assert.equal(JSON.parse(await readFile(dir + '/package.json', 'utf8')).version, version, dir);
}
const load = path => import(pathToFileURL(path).href);
const { stream: piGoogleStream } = await load(scope + 'pi-ai/dist/api/google-generative-ai.js');
const { AssistantMessageEventStream } = await load(scope + 'pi-ai/dist/utils/event-stream.js');
const { GoogleGenAI } = await load(genaiDir + '/dist/node/index.mjs');

// ---- stamping -------------------------------------------------------------------------------
const CAP = 160;
let seg = -1;
let tick = 0;
let generation = 0;
let log = [];
function segment(label) {
  seg++;
  tick = 0;
  const mine = ++generation;
  let n = 0;
  const step = () => {
    if (mine !== generation || n >= CAP) return;
    n++;
    tick = n;
    queueMicrotask(step);
  };
  queueMicrotask(step);
  log.push({ seg, tick: 0, ev: 'segment', label });
}
function note(line, ev, detail) {
  assert.ok(tick < CAP - 8, `tick ${tick} too close to the chain cap`);
  line.push({ seg, tick, ev, ...detail });
}
const copy = value => JSON.parse(JSON.stringify(value));
const macrotask = () => new Promise(resolve => setTimeout(resolve, 0));

// ---- fixtures -------------------------------------------------------------------------------
const record = data => `data: ${JSON.stringify(data)}\r\n\r\n`;
const usage = { promptTokenCount: 10, candidatesTokenCount: 3, totalTokenCount: 13 };
const toolRecord = record({
  candidates: [{ content: { role: 'model', parts: [{ functionCall: { id: 'call-1', name: 'read', args: { path: 'parity-read-target.txt' } } }] }, finishReason: 'STOP' }],
  usageMetadata: usage, modelVersion: 'probe', responseId: 'g1',
});
const textRecords = [
  record({ candidates: [{ content: { role: 'model', parts: [{ text: 'one' }] } }], responseId: 'g2', modelVersion: 'probe' }),
  record({ candidates: [{ content: { role: 'model', parts: [{ text: ' two' }] }, finishReason: 'STOP' }], usageMetadata: usage }),
];
const thinkRecords = [
  record({ candidates: [{ content: { role: 'model', parts: [{ text: 'hmm', thought: true, thoughtSignature: 'c2ln' }] } }], responseId: 'g3' }),
  record({ candidates: [{ content: { role: 'model', parts: [{ text: 'done' }] }, finishReason: 'STOP' }], usageMetadata: usage }),
];
const split = (text, at) => [text.slice(0, at), text.slice(at)];

// `deliveries[i]` = { at, bytes }. at 0: enqueued in the fetch-resolution macrotask before the Response
// resolves (a read finds the bytes queued). at N>0: enqueued in the N-th later macrotask (a read is pending).
// The last delivery closes the stream in the same callback, like a content-length response.
const cases = [
  { name: 'tool/buffered', deliveries: [{ at: 0, bytes: toolRecord }] },
  { name: 'tool/pending', deliveries: [{ at: 1, bytes: toolRecord }] },
  { name: 'tool/fragmented-buffered', deliveries: split(toolRecord, 40).map(bytes => ({ at: 0, bytes })) },
  { name: 'tool/fragmented-pending', deliveries: split(toolRecord, 40).map((bytes, i) => ({ at: i + 1, bytes })) },
  { name: 'text2/buffered-joined', deliveries: [{ at: 0, bytes: textRecords.join('') }] },
  { name: 'text2/buffered-split', deliveries: textRecords.map(bytes => ({ at: 0, bytes })) },
  { name: 'text2/pending-joined', deliveries: [{ at: 1, bytes: textRecords.join('') }] },
  { name: 'text2/pending-split', deliveries: textRecords.map((bytes, i) => ({ at: i + 1, bytes })) },
  { name: 'text2/buffered-then-pending', deliveries: textRecords.map((bytes, i) => ({ at: i, bytes })) },
  { name: 'thinking-text/buffered-split', deliveries: thinkRecords.map(bytes => ({ at: 0, bytes })) },
];

const model = { id: 'gemini-2.5-flash', name: 'probe', api: 'google-generative-ai', provider: 'google', baseUrl: 'http://probe.invalid/v1beta', reasoning: false, input: ['text'], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 };
const context = { messages: [{ role: 'user', content: 'go', timestamp: 0 }] };
const params = { model: model.id, contents: [{ role: 'user', parts: [{ text: 'go' }] }] };
const realFetch = globalThis.fetch;

// The fetch replacement: resolves from a macrotask (a network callback), like undici's headers arrival.
function installFetch(c) {
  globalThis.fetch = () => new Promise(resolve => {
    setTimeout(() => {
      segment('fetch-resolved');
      let controller;
      const body = new ReadableStream({ start(ctl) { controller = ctl; } });
      const encoder = new TextEncoder();
      const lastAt = Math.max(...c.deliveries.map(d => d.at));
      const flush = at => {
        for (const d of c.deliveries.filter(x => x.at === at)) controller.enqueue(encoder.encode(d.bytes));
        if (at === lastAt) controller.close();
      };
      flush(0);
      let n = 0;
      const next = () => {
        n++;
        if (n > lastAt) return;
        // Delivery N happens in the N-th macrotask after the response resolved.
        setTimeout(() => { segment(`delivery-${n}`); flush(n); next(); }, 0);
      };
      next();
      resolve(new Response(body, { status: 200, headers: { 'content-type': 'text/event-stream' } }));
    }, 0);
  });
}

async function runSdk(c) {
  const line = [];
  log = line;
  seg = -1;
  installFetch(c);
  segment('start');
  const client = new GoogleGenAI({ apiKey: 'k', httpOptions: { baseUrl: model.baseUrl, apiVersion: '' } });
  note(line, 'call');
  const sdkStream = await client.models.generateContentStream(params);
  note(line, 'sdk-resolved');
  let index = 0;
  for await (const chunk of sdkStream) {
    note(line, 'chunk', { index: index++, text: chunk.candidates?.[0]?.content?.parts?.[0]?.text ?? null, hasFunctionCall: !!chunk.candidates?.[0]?.content?.parts?.[0]?.functionCall, responseId: chunk.responseId ?? null });
  }
  note(line, 'sdk-end');
  return line;
}

async function runProvider(c) {
  const line = [];
  log = line;
  seg = -1;
  installFetch(c);
  const originalPush = AssistantMessageEventStream.prototype.push;
  AssistantMessageEventStream.prototype.push = function (event) {
    note(line, 'push', { type: event.type, contentIndex: event.contentIndex ?? null, snapshot: copy(event.partial ?? event.message ?? event.error) });
    return originalPush.call(this, event);
  };
  const consumer = [];
  try {
    segment('start');
    const stream = piGoogleStream(model, context, { apiKey: 'k' });
    note(consumer, 'returned');
    void stream.result().then(message => note(consumer, 'result', { stopReason: message.stopReason }));
    for await (const event of stream) {
      note(consumer, 'event', { type: event.type, contentIndex: event.contentIndex ?? null, snapshot: copy(event.partial ?? event.message ?? event.error) });
    }
    note(consumer, 'iterator-end');
    await macrotask();
  } finally {
    AssistantMessageEventStream.prototype.push = originalPush;
  }
  return { provider: line, consumer };
}

// The wall clock and generated IDs are the only nondeterministic values.
const scrub = value => JSON.parse(JSON.stringify(value).replace(/"timestamp":\d+/g, '"timestamp":0'));

const out = {
  pi: '1.1.0', genai: '2.21.0', node: process.version,
  cap: CAP,
  note: 'stamp = (seg, tick); see probe.mjs header. array order within a stamp is execution order.',
  cases: [],
};
for (const c of cases) {
  const sdk = await runSdk(c);
  await macrotask();
  const provider = await runProvider(c);
  out.cases.push({ name: c.name, deliveries: c.deliveries, sdk: scrub(sdk), provider: scrub(provider.provider), consumer: scrub(provider.consumer) });
}
globalThis.fetch = realFetch;
const text = JSON.stringify(out, null, 1) + '\n';
if (process.argv[2] === '--check') {
  const golden = JSON.parse(await readFile(process.argv[3], 'utf8'));
  golden.node = out.node;
  assert.deepEqual(out, golden);
  console.log(`tick order matches ${process.argv[3]} on Node ${process.version}`);
} else {
  if (process.argv[2]) await writeFile(process.argv[2], text);
  process.stdout.write(text);
}
