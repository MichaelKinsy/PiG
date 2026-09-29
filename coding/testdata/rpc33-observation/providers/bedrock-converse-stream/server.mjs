// Fixture server for the Bedrock ConverseStream oracle. It runs in its own process so the probe process sees only client sockets.
// Protocol (Node IPC): the server sends {port} once listening; the parent sends {release:true} to write the withheld part.
//
// argv: delivery ("buffered" | "pending" | "split"), http ("h1" | "h2"), first, rest, later.
// first, rest and later are JSON strings holding base64 of application/vnd.amazon.eventstream bytes:
// first leaves in one write with the response headers; rest is withheld until release (buffered: written with first); later is the whole body of every later request.
import { createServer as createHttp1 } from 'node:http';
import { createServer as createHttp2 } from 'node:http2';

const [, , delivery, http, firstArg, restArg, laterArg] = process.argv;
const first = Buffer.from(JSON.parse(firstArg), 'base64'), rest = Buffer.from(JSON.parse(restArg), 'base64'), later = Buffer.from(JSON.parse(laterArg), 'base64');
let requests = 0;
let released = false;
const waiting = [];
process.on('message', message => {
  if (message?.release) {
    released = true;
    for (const resume of waiting.splice(0)) resume();
  }
});
const handler = async (req, res) => {
  for await (const _ of req) {} // Finish request ownership before responding.
  requests++;
  res.writeHead(200, {'content-type': 'application/vnd.amazon.eventstream', 'x-amzn-requestid': `req-${requests}`});
  if (requests > 1) { res.end(later); return; }
  if (delivery === 'buffered') { res.end(Buffer.concat([first, rest])); return; }
  // Headers and the first bytes leave in one write; the remainder waits for the release message.
  res.write(first);
  if (!released) await new Promise(resolve => waiting.push(resolve));
  if (res.destroyed) return;
  res.end(rest);
};
const server = http === 'h2' ? createHttp2(handler) : createHttp1(handler);
server.listen(0, '127.0.0.1', () => process.send({port: server.address().port}));
