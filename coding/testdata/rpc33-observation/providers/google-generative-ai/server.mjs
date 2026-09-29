// Fixture server for the Google Generative AI oracle. It runs in its own process so the probe process sees only client sockets.
// Protocol (Node IPC): the server sends {port} once listening; the parent sends {release:true} to write the withheld part.
import { createServer } from 'node:http';

const [, , deliveryArg, firstArg, restArg, laterArg] = process.argv;
// argv carries JSON: delivery ("buffered" | "pending" | "split"), first (bytes written with the headers), rest (bytes withheld until release), later (body for every request after the first).
const delivery = deliveryArg, first = JSON.parse(firstArg), rest = JSON.parse(restArg), later = JSON.parse(laterArg);
let requests = 0;
let released = false;
const waiting = [];
process.on('message', message => {
  if (message?.release) {
    released = true;
    for (const resume of waiting.splice(0)) resume();
  }
});
const server = createServer(async (req, res) => {
  for await (const _ of req) {} // Finish request ownership before responding.
  requests++;
  res.writeHead(200, {'content-type': 'text/event-stream'});
  if (requests > 1) { res.end(later); return; }
  if (delivery === 'buffered') { res.end(first + rest); return; }
  // Headers (and, for split, the first bytes) leave in one write; the remainder waits for the release message.
  if (first === '') res.flushHeaders(); else res.write(first);
  if (!released) await new Promise(resolve => waiting.push(resolve));
  if (res.destroyed) return;
  res.end(rest);
});
server.listen(0, '127.0.0.1', () => process.send({port: server.address().port}));
