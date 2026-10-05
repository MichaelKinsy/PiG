// Job positions inside Pi's Bedrock pipeline, below the pi-ai provider: the real AWS SDK client (`client.send`) and its event stream, driven the way
// bedrock-converse-stream.ts:284-330 drives them. Go's bedrock_stream_pipeline.go takes its two SDK-stack constants from this output and
// bedrock_observation_test.go checks them.
//
// Usage: node chain.mjs <chain.json>
//   env PI_PACKAGE_ROOT installed @earendil-works/pi-coding-agent 1.0.3
//
// Records are (epoch, tick, what) with the probe.mjs tick model (async_hooks microtask jobs; an epoch is a byte-delivery macrotask). `hooks` installs Pi's deserialize
// middleware, which awaits `onResponse` (bedrock-converse-stream.ts:510-523), as when the caller passes `onResponse`; without it Pi awaits `options?.onResponse?.()`
// itself after send (bedrock-converse-stream.ts:289-296).
import { createRequire } from 'node:module';
import { fork } from 'node:child_process';
import { createHook } from 'node:async_hooks';
import { EventEmitter } from 'node:events';
import { Readable } from 'node:stream';
import net from 'node:net';
import { readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

const root = process.env.PI_PACKAGE_ROOT;
const require = createRequire(join(root, 'package.json'));
const { BedrockRuntimeClient, ConverseStreamCommand } = require('@aws-sdk/client-bedrock-runtime');
const { NodeHttpHandler } = require('@smithy/node-http-handler');
const here = new URL('.', import.meta.url).pathname;
const inputs = JSON.parse(readFileSync(join(here, 'inputs.json'), 'utf8'));
const frames = inputs.bodies.tool.map(b => Buffer.from(b, 'base64'));
const b64 = list => Buffer.concat(list).toString('base64');

let epoch = 0, tick = 0, serverPort = 0, records = [];
const jobTypes = new Set();
createHook({
  init(id, type) { if (type === 'PROMISE' || type === 'Microtask') jobTypes.add(id); },
  before(id) { if (jobTypes.has(id)) tick++; },
  destroy(id) { jobTypes.delete(id); },
}).enable();
const log = what => records.push({epoch, tick, what});
function startEpoch() { if (tick > 0 || epoch === 0) { epoch++; tick = 0; } }
const originalEmit = EventEmitter.prototype.emit;
EventEmitter.prototype.emit = function (name, ...args) {
  if (this instanceof net.Socket && name === 'data' && this.remotePort === serverPort) startEpoch();
  else if (name === 'response' && this.constructor.name === 'ClientHttp2Stream') startEpoch();
  return originalEmit.call(this, name, ...args);
};
const originalPush = Readable.prototype.push;
Readable.prototype.push = function (data, ...rest) {
  if (this.constructor.name === 'ClientHttp2Stream' && (data === null || data?.length > 0)) startEpoch();
  return originalPush.call(this, data, ...rest);
};
const originalIterator = Readable.prototype[Symbol.asyncIterator];
Readable.prototype[Symbol.asyncIterator] = function () { log(`Readable[Symbol.asyncIterator] on ${this.constructor.name}`); return originalIterator.call(this); };

async function run(delivery, http, hooks) {
  const [first, rest] = delivery === 'buffered' ? ['', b64(frames)] : [b64(frames.slice(0, 1)), b64(frames.slice(1))];
  const child = fork(join(here, 'server.mjs'), [delivery, http, JSON.stringify(first), JSON.stringify(rest), JSON.stringify('')], {stdio: ['ignore', 'inherit', 'inherit', 'ipc']});
  const {port} = await new Promise(resolve => child.once('message', resolve));
  serverPort = port; epoch = 0; tick = 0; records = [];
  const client = new BedrockRuntimeClient({region: 'us-east-1', endpoint: `http://127.0.0.1:${port}`, credentials: {accessKeyId: 'a', secretAccessKey: 'b'}, ...(http === 'h1' ? {requestHandler: new NodeHttpHandler()} : {})});
  if (hooks) {
    // bedrock-converse-stream.ts:510-523
    client.middlewareStack.add(next => async args => {
      const result = await next(args);
      log('middleware: next() returned');
      await (async () => {})();
      log('middleware: onResponse awaited');
      return result;
    }, {step: 'deserialize', name: 'pi-ai-response-headers'});
  }
  const response = await client.send(new ConverseStreamCommand({modelId: 'x', messages: [{role: 'user', content: [{text: 'a'}]}]}));
  if (!hooks) await undefined; // bedrock-converse-stream.ts:289-296: `await options?.onResponse?.(...)` with no callback
  log('send resolved');
  if (delivery !== 'buffered') child.send({release: true});
  for await (const item of response.stream) log(`item ${Object.keys(item)[0]}`);
  log('stream done');
  child.kill();
  return records;
}

const out = {piVersion: '1.0.3', awsSdkClientBedrockRuntime: '3.1127.0', smithyCore: '3.35.1', node: process.version, cases: {}};
for (const http of ['h1', 'h2']) for (const delivery of ['buffered', 'pending']) for (const hooks of [true, false]) {
  out.cases[`${http}/${delivery}/${hooks ? 'hooks' : 'nohooks'}`] = await run(delivery, http, hooks);
}
writeFileSync(process.argv[2], JSON.stringify(out, null, 1) + '\n');
process.exit(0);
