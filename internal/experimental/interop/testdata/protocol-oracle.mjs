// Differential oracle for packages/protocol: one JSON case per stdin line, one JSON result per stdout line.
import { createInterface } from 'node:readline';
import {
  ClientMessageDecoder, ServerMessageDecoder, decodeCbor, encodeCbor, encodeClientMessage, encodeServerMessage,
  isSupportedProtocolVersion, isServerId,
} from '@earendil-works/pi-protocol';

const hex = (bytes) => Buffer.from(bytes).toString('hex');
const unhex = (text) => new Uint8Array(Buffer.from(text, 'hex'));
const failure = (error) => ({ error: `${error.name}: ${error.message}` });

function decode(Decoder, encode, chunks, end) {
  const decoder = new Decoder();
  const steps = [];
  for (const chunk of chunks) {
    try { steps.push({ messages: decoder.push(unhex(chunk)).map((message) => hex(encode(message))) }); }
    catch (error) { steps.push(failure(error)); }
  }
  if (end) {
    try { decoder.end(); steps.push({ ended: true }); } catch (error) { steps.push(failure(error)); }
  }
  return { steps };
}

function run(c) {
  switch (c.op) {
    case 'decodeClient': return decode(ClientMessageDecoder, encodeClientMessage, c.chunks, c.end);
    case 'decodeServer': return decode(ServerMessageDecoder, encodeServerMessage, c.chunks, c.end);
    case 'encodeClient': try { return { hex: hex(encodeClientMessage(JSON.parse(c.json))) }; } catch (error) { return failure(error); }
    case 'encodeServer': try { return { hex: hex(encodeServerMessage(JSON.parse(c.json))) }; } catch (error) { return failure(error); }
    case 'cbor': try { return { hex: hex(encodeCbor(decodeCbor(unhex(c.hex)))) }; } catch (error) { return failure(error); }
    case 'version': return { value: isSupportedProtocolVersion(c.value) };
    case 'serverId': return { value: isServerId(c.value) };
    default: throw new Error(`unknown op ${c.op}`);
  }
}

for await (const line of createInterface({ input: process.stdin })) {
  if (line === '') continue;
  process.stdout.write(`${JSON.stringify(run(JSON.parse(line)))}\n`);
}
