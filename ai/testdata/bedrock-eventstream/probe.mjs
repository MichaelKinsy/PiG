// Runs Pi's real Bedrock event-stream stack (AWS SDK client-bedrock-runtime 3.1127.0, @smithy/core 3.33.3) over byte sequences and prints, per case, what
// `for await (const item of response.stream)` yields or throws. bedrock_eventstream_test.go feeds the same bytes to PiG's framing and decoding and compares.
//
// Usage: PI_PACKAGE_ROOT=<installed pi-coding-agent 0.87.1> node probe.mjs > golden.json
import { createRequire } from 'node:module';
import { createServer } from 'node:http';
import { crc32 } from 'node:zlib';
import { join } from 'node:path';

const root = process.env.PI_PACKAGE_ROOT;
const require = createRequire(join(root, 'package.json'));
const { BedrockRuntimeClient, ConverseStreamCommand } = require('@aws-sdk/client-bedrock-runtime');
const { NodeHttpHandler } = require('@smithy/node-http-handler');

function header(name, type, value) {
  const n = Buffer.from(name);
  const v = Buffer.from(value);
  const out = Buffer.alloc(1 + n.length + 1 + (type === 7 ? 2 : 0) + v.length);
  let at = 0;
  out[at++] = n.length; n.copy(out, at); at += n.length;
  out[at++] = type;
  if (type === 7) { out.writeUInt16BE(v.length, at); at += 2; }
  v.copy(out, at);
  return out;
}
const str = (name, value) => header(name, 7, value);
function build(headers, payload, { badPrelude = false, badMessage = false } = {}) {
  const h = Buffer.concat(headers);
  const body = Buffer.isBuffer(payload) ? payload : Buffer.from(payload);
  const total = 12 + h.length + body.length + 4;
  const out = Buffer.alloc(total);
  out.writeUInt32BE(total, 0);
  out.writeUInt32BE(h.length, 4);
  out.writeUInt32BE((crc32(out.subarray(0, 8)) ^ (badPrelude ? 1 : 0)) >>> 0, 8);
  h.copy(out, 12);
  body.copy(out, 12 + h.length);
  out.writeUInt32BE((crc32(out.subarray(0, total - 4)) ^ (badMessage ? 1 : 0)) >>> 0, total - 4);
  return out;
}
const event = (type, payload, options) => build([str(':event-type', type), str(':content-type', 'application/json'), str(':message-type', 'event')], JSON.stringify(payload), options);
const raw = (type, body) => build([str(':event-type', type), str(':message-type', 'event')], body);
const exception = (type, payload) => build([str(':exception-type', type), str(':content-type', 'application/json'), str(':message-type', 'exception')], JSON.stringify(payload));
const errorFrame = (code, message) => build([str(':error-code', code), str(':error-message', message), str(':message-type', 'error')], '');
const start = event('messageStart', {role: 'assistant'});
const delta = (text, index = 0) => event('contentBlockDelta', {contentBlockIndex: index, delta: {text}});
const stop = event('messageStop', {stopReason: 'end_turn'});
const split = (buffer, ...cuts) => { const parts = []; let from = 0; for (const cut of cuts) { parts.push(buffer.subarray(from, cut)); from = cut; } parts.push(buffer.subarray(from)); return parts.filter(p => p.length); };
const bytewise = buffer => Array.from(buffer, (_, i) => buffer.subarray(i, i + 1));
const whole = Buffer.concat([start, delta('one'), delta(' two'), stop]);

const cases = {
  'one frame': [Buffer.concat([start])],
  'frames in one chunk': [whole],
  'frame per chunk': [start, delta('one'), stop],
  'split in the prelude length': [...split(Buffer.concat([start, delta('a')]), 2), stop],
  'split after four bytes': split(Buffer.concat([start, delta('a'), stop]), 4),
  'split at the prelude end': split(Buffer.concat([start, delta('a'), stop]), 8, 12),
  'split in the headers': split(Buffer.concat([start, delta('a'), stop]), 30, 60),
  'split in the payload and checksum': split(Buffer.concat([start, delta('a'), stop]), start.length - 3, start.length + 20, start.length + delta('a').length - 2),
  'one byte per chunk': bytewise(Buffer.concat([start, delta('a'), stop])),
  'unknown event dropped': [start, event('somethingNew', {x: 1}), delta('a'), stop],
  'unknown event first': [event('somethingNew', {x: 1}), start, stop],
  'empty payload': [start, raw('contentBlockStop', ''), stop],
  'tool use start and delta': [start, event('contentBlockStart', {contentBlockIndex: 1, start: {toolUse: {toolUseId: 't1', name: 'read'}}}), event('contentBlockDelta', {contentBlockIndex: 1, delta: {toolUse: {input: '{"a":1}'}}}), stop],
  'reasoning members': [start, event('contentBlockDelta', {contentBlockIndex: 0, delta: {reasoningContent: {text: 'why', signature: 'sig'}}}), event('contentBlockDelta', {contentBlockIndex: 0, delta: {reasoningContent: {redactedContent: 'AQID'}}}), stop],
  'metadata usage': [start, stop, event('metadata', {usage: {inputTokens: 1, outputTokens: 2, totalTokens: 3, cacheReadInputTokens: 4, cacheWriteInputTokens: 5, cacheDetails: [{ttl: '1h', inputTokens: 5}]}, metrics: {latencyMs: 9}})],
  'throttling exception': [start, exception('throttlingException', {message: 'slow down'})],
  'validation exception': [start, exception('validationException', {message: 'bad input'})],
  'internal server exception': [start, exception('internalServerException', {message: 'boom'})],
  'service unavailable exception': [start, exception('serviceUnavailableException', {message: 'later'})],
  'model stream error exception': [start, exception('modelStreamErrorException', {message: 'stream', originalStatusCode: 500, originalMessage: 'orig'})],
  'exception without message': [start, exception('throttlingException', {})],
  'unmodeled exception': [start, exception('fooException', {message: 'foo'})],
  'exception first': [exception('throttlingException', {message: 'first'})],
  'error frame': [start, errorFrame('SomeError', 'it broke')],
  'error frame without message': [start, errorFrame('SomeError', '')],
  'bad prelude checksum': [start, event('messageStop', {stopReason: 'x'}, {badPrelude: true})],
  'bad message checksum': [start, event('messageStop', {stopReason: 'x'}, {badMessage: true})],
  'length below the minimum': [start, Buffer.from([0, 0, 0, 12, 0, 0, 0, 0, 0, 0, 0, 0])],
  'length below four': [start, Buffer.from([0, 0, 0, 2, 9, 9, 9, 9])],
  'length three': [start, Buffer.from([0, 0, 0, 3, 9, 9, 9, 9])],
  'length zero': [start, Buffer.from([0, 0, 0, 0, 9, 9, 9, 9])],
  'length four': [start, Buffer.from([0, 0, 0, 4, 9, 9, 9, 9])],
  'truncated frame': [start, delta('a').subarray(0, 20)],
  'partial length prefix at the end': [start, Buffer.from([0, 0])],
  'malformed json': [start, raw('contentBlockDelta', 'not json')],
  'invalid base64': [start, event('contentBlockDelta', {contentBlockIndex: 0, delta: {reasoningContent: {redactedContent: '!!!!'}}})],
  'unrecognized message type': [start, build([str(':event-type', 'x'), str(':message-type', 'weird')], '{}')],
  'missing message type': [start, build([str(':event-type', 'x')], '{}')],
  'unrecognized header tag': [start, build([Buffer.from([1, 0x61, 0x63])], '{}')],
  'no frames': [],
};

async function run(chunks) {
  const server = createServer(async (req, res) => {
    for await (const _ of req) {}
    res.writeHead(200, {'content-type': 'application/vnd.amazon.eventstream'});
    for (const chunk of chunks) { res.write(chunk); await new Promise(r => setTimeout(r, 15)); }
    res.end();
  });
  await new Promise(r => server.listen(0, '127.0.0.1', r));
  const client = new BedrockRuntimeClient({region: 'us-east-1', endpoint: `http://127.0.0.1:${server.address().port}`, credentials: {accessKeyId: 'a', secretAccessKey: 'b'}, requestHandler: new NodeHttpHandler(), maxAttempts: 1});
  const results = [];
  try {
    const response = await client.send(new ConverseStreamCommand({modelId: 'x', messages: [{role: 'user', content: [{text: 'a'}]}]}));
    for await (const item of response.stream) results.push({item: JSON.parse(JSON.stringify(item))});
  } catch (error) {
    results.push({error: {name: error.name, message: error.message, isError: error instanceof Error}});
  }
  client.destroy();
  server.closeAllConnections();
  await new Promise(r => server.close(r));
  return results;
}

const out = {node: process.version, cases: {}};
for (const [name, chunks] of Object.entries(cases)) out.cases[name] = {chunks: chunks.map(c => c.toString('hex')), results: await run(chunks)};
console.log(JSON.stringify(out, null, 1));
process.exit(0);
