// Node 24.19.0 undici/Web Streams latencies that the Google pipeline depends on, measured in rounds (see probe.mjs: a round is one FIFO generation,
// counted by a queueMicrotask chain that starts at each client socket 'data' event).
// Usage: node undici.mjs   -> JSON on stdout.
//
//   fetchResolvedRound  round (after the headers' socket data event) in which the `fetch()` promise settled
//   firstRead           rounds until the first `reader.read()` settles: from the read call when the chunk arrived with the headers (buffered, split),
//                       or from the socket data event that carries it (pending)
//   json                rounds for `new Response(text).json()` (a string body) to settle, measured by `then`
// The end-of-body read is not measured here: undici delivers it from a process.nextTick callback, which the round chain starves; the Go model treats it as the
// first external completion after the microtask queue drains (probe.mjs `done` pushes follow every consumer delivery in every case).
import { fork } from 'node:child_process';
import net from 'node:net';

const serverPath = new URL('./server.mjs', import.meta.url).pathname;
const body = 'data: {"a":1}\r\n\r\n';
let round = 0, chain = 0, active = false, port = 0, epoch = 0;
const CAP = 60;
const emit = net.Socket.prototype.emit;
net.Socket.prototype.emit = function (name, ...args) {
  if (active && name === 'data' && this.remotePort === port) {
    epoch++;
    const mine = ++chain;
    round = 0;
    const step = () => { if (chain === mine && round < CAP) { round++; queueMicrotask(step); } };
    queueMicrotask(step);
  }
  return emit.call(this, name, ...args);
};
const measure = async (label, run) => { const start = round; const startEpoch = epoch; await run(); return {label, rounds: epoch === startEpoch ? round - start : null, epochs: epoch - startEpoch}; };

// settled(promise) resolves with the round in which the promise settled: a `then` reaction runs one round after settlement.
const settledRound = promise => promise.then(() => round - 1);

async function fetchCase(delivery) {
  const [first, rest] = delivery === 'split' ? [body.slice(0, 8), body.slice(8)] : ['', body];
  const child = fork(serverPath, [delivery, JSON.stringify(first), JSON.stringify(rest), '""'], {stdio: ['ignore', 'inherit', 'inherit', 'ipc']});
  port = await new Promise(resolve => child.once('message', m => resolve(m.port)));
  active = true; epoch = 0; round = 0; chain++;
  const out = {delivery};
  const response = await fetch(`http://127.0.0.1:${port}/x`, {method: 'POST', body: '{}'});
  out.fetchResolvedRound = round - 1; // the continuation of `await fetch` runs one round after the promise settles
  const reader = response.body.getReader();
  const issued = round, issuedEpoch = epoch;
  const read = settledRound(reader.read());
  if (delivery === 'pending') child.send({release: true});
  const settled = await read;
  // A read that settles in the epoch it was issued in is measured from the issue round; one that waits for a later socket event, from that event (round 0).
  out.firstRead = epoch === issuedEpoch ? {from: 'issue', rounds: settled - issued} : {from: 'socket-data', rounds: settled};
  active = false; chain++; child.kill();
  return out;
}

async function jsonCase() {
  active = true; epoch = 0; round = 0; chain++;
  // A synthetic epoch: start the chain without a socket.
  const mine = ++chain;
  const step = () => { if (chain === mine && round < CAP) { round++; queueMicrotask(step); } };
  queueMicrotask(step);
  await Promise.resolve();
  const response = new Response('{"a":1}');
  const start = round;
  const rounds = (await settledRound(response.json())) - start;
  active = false; chain++;
  return {json: rounds};
}

const results = [];
for (const delivery of ['buffered', 'pending', 'split']) results.push(await fetchCase(delivery));
results.push(await jsonCase());
console.log(JSON.stringify({node: process.version, results}, null, 1));
process.exit(0);
