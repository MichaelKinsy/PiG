// Job trace for one case: every microtask job of the epoch(s) between the first socket read and `done`, with the stack that created the Promise the job settles.
// Usage: node trace.mjs <shape> <delivery>   (direct provider, no lazyStream layers; PI_PACKAGE_ROOT as for probe.mjs)
// A job is attributed to the Promise it resolves: a `then` reaction is attributed to the derived promise, an `await` to its throwaway promise.
// The output is a reading aid for transcribing awaits; probe.mjs remains the oracle.
import { fork } from 'node:child_process';
import net from 'node:net';
import { createHook } from 'node:async_hooks';

const root = process.env.PI_PACKAGE_ROOT;
const base = root + '/node_modules/@earendil-works/';
const [shape = 'tool', delivery = 'buffered'] = process.argv.slice(2);
const chunk = value => 'data: ' + JSON.stringify(value) + '\r\n\r\n';
const usage = {promptTokenCount: 10, candidatesTokenCount: 3, totalTokenCount: 13};
const list = shape === 'tool'
  ? [{candidates: [{content: {role: 'model', parts: [{functionCall: {name: 'read', args: {path: 'target.txt'}}}]}, finishReason: 'STOP'}], usageMetadata: usage, responseId: 'g1'}]
  : [{candidates: [{content: {role: 'model', parts: [{text: 'one'}]}}], responseId: 'resp-g'}, {candidates: [{content: {role: 'model', parts: [{text: ' two'}]}, finishReason: 'STOP'}], usageMetadata: usage}];
const all = list.map(chunk).join('');
const [first, rest] = delivery === 'split' && list.length > 1 ? [chunk(list[0]), list.slice(1).map(chunk).join('')] : ['', all];
const server = new URL('./server.mjs', import.meta.url).pathname;
const child = fork(server, [delivery, JSON.stringify(first), JSON.stringify(rest), JSON.stringify('')], {stdio: ['ignore', 'inherit', 'inherit', 'ipc']});
const port = await new Promise(resolve => child.once('message', m => resolve(m.port)));

let tick = 0, epoch = 0, active = false;
const info = new Map();
const events = [];
const hook = createHook({
  init(id, type, trigger) {
    if (!active || (type !== 'PROMISE' && type !== 'Microtask')) return;
    const holder = {}; Error.stackTraceLimit = 12; Error.captureStackTrace(holder);
    const frames = holder.stack.split('\n').slice(2).map(l => l.trim().replace(/^at /, '')).filter(l => !l.includes('async_hooks') && !l.includes('probe') && !l.includes('trace.mjs'));
    info.set(id, {type, trigger, frames: frames.slice(0, 4)});
  },
  before(id) { if (!info.has(id)) return; tick++; events.push({epoch, tick, id, ...info.get(id)}); },
});
hook.enable();
const emit = net.Socket.prototype.emit;
net.Socket.prototype.emit = function (name, ...args) { if (active && name === 'data' && this.remotePort === port) { epoch++; tick = 0; } return emit.call(this, name, ...args); };

const { EventStream } = await import(base + 'pi-ai/dist/utils/event-stream.js');
const { stream } = await import(base + 'pi-ai/dist/api/google-generative-ai.js');
const push = EventStream.prototype.push;
EventStream.prototype.push = function (event) { if (active) events.push({epoch, tick, push: event.type}); return push.call(this, event); };
const model = {id: 'probe', name: 'probe', api: 'google-generative-ai', provider: 'p', baseUrl: `http://127.0.0.1:${port}/v1beta`, reasoning: false, input: ['text'], contextWindow: 4096, maxTokens: 256, cost: {input: 0, output: 0, cacheRead: 0, cacheWrite: 0}};
active = true;
const response = stream(model, {messages: [{role: 'user', content: [{type: 'text', text: 'probe'}], timestamp: 1}]}, {apiKey: 'test', maxRetries: 0});
for await (const event of response) {
  events.push({epoch, tick, deliver: event.type});
  if (event.type === 'start' && delivery !== 'buffered') child.send({release: true});
}
active = false;
child.kill();
for (const e of events) {
  if (e.push) console.log(`e${e.epoch} t${e.tick} PUSH ${e.push}`);
  else if (e.deliver) console.log(`e${e.epoch} t${e.tick} DELIVER ${e.deliver}`);
  else console.log(`e${e.epoch} t${e.tick} #${e.id}<-${e.trigger} ${e.type} ${e.frames.join(' | ')}`);
}
process.exit(0);
