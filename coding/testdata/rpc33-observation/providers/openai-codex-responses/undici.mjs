// Microtask latencies of Node's fetch() response body that the Codex SSE pipeline awaits (Node 24.19.0 and 26.7.0
// print the same values). `t` counts microtask generations of a counter chain restarted by each client-socket data event.
//   fetchResolution  socket data carrying the response headers -> `await fetch()` resumes
//   bufferedRead     reader.read() called while the first body bytes are already buffered -> `await` resumes
//   pendingRead      socket data event that completes a pending reader.read() -> `await` resumes
//   cancelOpen       `await reader.cancel()` on a stream that has not reached EOF
//   cancelClosed     `await reader.cancel()` on a stream that already delivered EOF
//
// usage: node undici.mjs <undici.json>
import { createServer } from 'node:http';
import net from 'node:net';
import { once } from 'node:events';
import { writeFile } from 'node:fs/promises';
import assert from 'node:assert/strict';

let ticks = 0, running = false, segment = 0;
const window = 256;
function external() {
  segment++; ticks = 0; running = true;
  const step = () => Promise.resolve().then(() => { ticks++; if (ticks < window) step(); else running = false; });
  step();
}
const connect = net.Socket.prototype.connect;
net.Socket.prototype.connect = function (...args) { this.d82Client = true; return connect.apply(this, args); };
const emit = net.Socket.prototype.emit;
net.Socket.prototype.emit = function (name, ...args) {
  if (name === 'data' && this.d82Client) external();
  return emit.call(this, name, ...args);
};

const body = Buffer.from('data: {"a":1}\n\n');
async function serve(pending) {
  const server = createServer(async (req, res) => {
    for await (const _ of req) { /* drain */ }
    if (!pending) { res.writeHead(200, { 'content-length': body.length }); res.end(body); return; }
    res.writeHead(200, { 'content-type': 'text/event-stream' });
    res.flushHeaders();
    await new Promise(resolve => setTimeout(resolve, 50));
    res.end(body);
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  return server;
}
const request = server => fetch(`http://127.0.0.1:${server.address().port}/`, { method: 'POST', body: 'x' });

const result = { node: process.version };
{
  const server = await serve(false);
  const response = await request(server);
  result.fetchResolution = ticks;
  const reader = response.body.getReader();
  const before = ticks;
  await reader.read();
  result.bufferedRead = ticks - before;
  const cancelStart = ticks;
  await reader.cancel();
  result.cancelOpen = ticks - cancelStart;
  server.closeAllConnections(); server.close();
}
{
  const server = await serve(true);
  const response = await request(server);
  const reader = response.body.getReader();
  const read = reader.read();
  const segmentBefore = segment;
  await read;
  assert.equal(segment, segmentBefore + 1, 'the pending read completed in the body data event');
  result.pendingRead = ticks;
  const eof = await reader.read();
  assert.equal(eof.done, true);
  external(); // The end-of-body notification is not a socket data event; restart the chain for the cancel measurement.
  const before = ticks;
  await reader.cancel();
  result.cancelClosed = ticks - before;
  server.closeAllConnections(); server.close();
}
await writeFile(process.argv[2], JSON.stringify(result, null, 2) + '\n');
console.log(JSON.stringify(result));
