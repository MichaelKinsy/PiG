// Bedrock ConverseStream event frames (application/vnd.amazon.eventstream) for the RPC33 `read` tool call, shared by the multi-provider fixture server.
import { crc32 } from 'node:zlib';

function header(name, value) {
  const n = Buffer.from(name), v = Buffer.from(value);
  const out = Buffer.alloc(1 + n.length + 1 + 2 + v.length);
  let at = 0;
  out[at++] = n.length; n.copy(out, at); at += n.length;
  out[at++] = 7; out.writeUInt16BE(v.length, at); at += 2; v.copy(out, at);
  return out;
}
export function frame(eventType, payload) {
  const headers = Buffer.concat([header(':event-type', eventType), header(':content-type', 'application/json'), header(':message-type', 'event')]);
  const body = Buffer.from(JSON.stringify(payload));
  const total = 12 + headers.length + body.length + 4;
  const out = Buffer.alloc(total);
  out.writeUInt32BE(total, 0);
  out.writeUInt32BE(headers.length, 4);
  out.writeUInt32BE(crc32(out.subarray(0, 8)), 8);
  headers.copy(out, 12);
  body.copy(out, 12 + headers.length);
  out.writeUInt32BE(crc32(out.subarray(0, total - 4)), total - 4);
  return out;
}
export const readToolCall = () => Buffer.concat([
  frame('messageStart', {role: 'assistant'}),
  frame('contentBlockStart', {contentBlockIndex: 0, start: {toolUse: {toolUseId: 'toolu_1', name: 'read'}}}),
  frame('contentBlockDelta', {contentBlockIndex: 0, delta: {toolUse: {input: '{"path":"parity-read-target.txt"}'}}}),
  frame('contentBlockStop', {contentBlockIndex: 0}),
  frame('messageStop', {stopReason: 'tool_use'}),
  frame('metadata', {usage: {inputTokens: 10, outputTokens: 3, totalTokens: 13}, metrics: {latencyMs: 1}}),
]);
export const textReply = () => Buffer.concat([
  frame('messageStart', {role: 'assistant'}),
  frame('contentBlockDelta', {contentBlockIndex: 0, delta: {text: 'ok'}}),
  frame('contentBlockStop', {contentBlockIndex: 0}),
  frame('messageStop', {stopReason: 'end_turn'}),
  frame('metadata', {usage: {inputTokens: 1, outputTokens: 1, totalTokens: 2}, metrics: {latencyMs: 1}}),
]);
