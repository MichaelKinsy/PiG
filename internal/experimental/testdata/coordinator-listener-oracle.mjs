import { createServer } from 'node:net';
import { once } from 'node:events';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';

const { CoordinatorConnection } = await import(pathToFileURL(`${process.env.PIG_TEST_ROOT}/.upstream/current/packages/coding-agent/src/experimental/coordinator.ts`));
const [directory, cleanup] = process.argv.slice(2);
const path = join(directory, 'c');
const fake = createServer();
fake.listen(path);
await once(fake, 'listening');
const accepted = once(fake, 'connection');
const connection = new CoordinatorConnection({ controlPath: path, endpoint: 'unused', serverConnectionId: 's' });
let calls = 0;
const counts = [];
const listener = () => { calls++; };
const remove = [connection.onEvent(listener), connection.onEvent(listener)];
let finish;
const observed = new Promise(resolve => { finish = resolve; });
connection.onEvent(() => {
  counts.push(calls);
  calls = 0;
  remove[Number(cleanup)]();
  if (counts.length === 2) finish();
});
let socket;
try {
  const connected = connection.connect();
  [socket] = await accepted;
  let buffered = '';
  socket.setEncoding('utf8');
  socket.on('data', chunk => {
    buffered += chunk;
    if (!buffered.includes('\n')) return;
    const registration = JSON.parse(buffered.slice(0, buffered.indexOf('\n')));
    socket.write(JSON.stringify({ type: 'server_registered', serverConnectionId: registration.serverConnectionId, peers: [] }) + '\n');
  });
  await connected;
  socket.write('{"type":"peer_connected","peerId":"p"}\n{"type":"peer_disconnected","peerId":"p"}\n');
  await observed;
  console.log(JSON.stringify(counts));
} finally {
  connection.close();
  socket?.destroy();
  await new Promise(resolve => fake.close(resolve));
}
