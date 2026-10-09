// Runs the interop client scenario with the pinned Pi 1.0.4 packages/client against a Unix socket server and prints the
// transcript as one JSON line. The Go client runs the identical steps (client_test.go); the transcripts must be equal.
import { Client } from '@earendil-works/pi-client';
import { createUnixTransportFactory } from '@earendil-works/pi-client/unix';

const [path, serverId, otherServerId] = process.argv.slice(2);
const transcript = [];
const client = new Client({ transportFactory: createUnixTransportFactory({ path }), serverId });

const record = (name, outcome) => transcript.push({ name, ...outcome });
const failure = (error) => ({ error: { name: error.constructor.name, code: error.code ?? null, message: error.message } });
async function step(name, run) {
  try { const value = await run(); record(name, value === undefined ? { resultUndefined: true } : { result: value }); } catch (error) { record(name, failure(error)); }
}
const waitFor = async (predicate) => { for (let i = 0; i < 2000 && !predicate(); i++) await new Promise((r) => setTimeout(r, 5)); };

const server = { serverId };
await step('connect', async () => { const hello = await client.connect(); return { version: hello.version, serverId: hello.serverId, connected: client.connected }; });
await step('catalogue-server', () => client.serviceCatalogue(server));
await step('attach-unknown', () => client.request(server, { serviceId: 'pi.session-management', member: 'attach', args: ['nope'] }));
await step('attach-bad-arguments', () => client.request(server, { serviceId: 'pi.session-management', member: 'attach', args: [1] }));
await step('server-call-unsupported', () => client.request(server, { serviceId: 'x', member: 'y', args: [] }));
await step('wrong-server', () => client.request({ serverId: otherServerId }, { serviceId: 'pi.session-management', member: 'detach', args: [] }));
await step('attach', async () => {
  const result = await client.request(server, { serviceId: 'pi.session-management', member: 'attach', args: ['session-1'] });
  await waitFor(() => client.attachment !== undefined);
  const attachment = client.attachment;
  return { result, attachment: attachment && { serverId: attachment.serverId, sessionId: attachment.sessionId, attachmentId: attachment.attachmentId.length > 0 } };
});
const session = client.attachment;
const rich = { serviceId: 'svc', member: 'echo', instance: { key: 'i-1', generation: 2 }, args: [null, true, false, 0, 1, -1, 1.5, -2.25, 9007199254740991, -9007199254740991, 1e-7, 'é😀\u0000\u001f', '', [], {}, { b: 1, a: 2, 10: 3, 2: 4, nested: [{ k: [1, [2, [3]]] }] }] };
await step('stream', async () => {
  const updates = [];
  const subscription = await client.subscribeService(session, 'interop.counter', 'singleton', (update) => { updates.push(update); });
  const snapshot = subscription.snapshot;
  subscription.start();
  for (const [member, args] of [['bump', [1]], ['bump', [2]], ['rename', ['é😀']], ['bump', [4]]]) {
    await client.request(session, { serviceId: 'interop.counter', member, args });
  }
  await waitFor(() => updates.length >= 4);
  await subscription.dispose();
  await client.request(session, { serviceId: 'interop.counter', member: 'bump', args: [100] });
  await new Promise((r) => setTimeout(r, 150));
  return { subscriptionId: subscription.id, snapshot, updates, afterDispose: updates.length };
});
await step('stream-unknown-service', () => client.subscribeService(session, 'interop.missing', 'singleton', () => {}));
await step('catalogue-session', () => client.serviceCatalogue(session));
await step('echo', () => client.request(session, rich));
await step('echo-unsafe-integer', () => client.request(session, { serviceId: 's', member: 'echo', args: [1e21] }));
await step('echo-empty-args', () => client.request(session, { serviceId: 's', member: 'echo', args: [] }));
await step('undefined-result', () => client.request(session, { serviceId: 's', member: 'undefined', args: [] }));
await step('null-result', () => client.request(session, { serviceId: 's', member: 'null', args: [] }));
await step('service-error', () => client.request(session, { serviceId: 's', member: 'fail', args: [] }));
await step('internal-error', () => client.request(session, { serviceId: 's', member: 'secret', args: [] }));
await step('stale-attachment', () => client.request({ ...session, attachmentId: 'bogus' }, { serviceId: 's', member: 'echo', args: [] }));
await step('unattached-session', () => client.request({ serverId, sessionId: 'session-2', attachmentId: 'bogus' }, { serviceId: 's', member: 'echo', args: [] }));
await step('large-result', async () => { const { echo, data } = await client.request(session, { serviceId: 's', member: 'big', args: [] }); return { echo: echo ?? null, length: data.length, tail: data.slice(-2) }; });
await step('large-argument', async () => { const text = `${'y'.repeat(2 << 20)}😀`; const result = await client.request(session, { serviceId: 's', member: 'echo', args: [text] }); return { length: result.echo.args[0].length, equal: result.echo.args[0] === text }; });
await step('pipelined', async () => {
  const results = await Promise.all(Array.from({ length: 50 }, (_, i) => client.request(session, { serviceId: 's', member: 'echo', args: [i] })));
  return results.map((r) => r.echo.args[0]);
});
await step('cancel', async () => {
  const controller = new AbortController();
  const pending = client.request(session, { serviceId: 's', member: 'hang', args: [] }, controller.signal);
  const settled = pending.then(() => 'resolved', (error) => `rejected:${error.name}`);
  controller.abort();
  const outcome = await settled;
  // The server records the cancellation before the next request is answered.
  const after = await client.request(session, { serviceId: 's', member: 'echo', args: ['after-cancel'] });
  return { outcome, after: after.echo.args[0] };
});
await step('detach', async () => {
  const result = await client.request(server, { serviceId: 'pi.session-management', member: 'detach', args: [] });
  await waitFor(() => client.attachment === undefined);
  return { result, attachment: client.attachment ?? null };
});
await step('after-detach', () => client.request(session, { serviceId: 's', member: 'echo', args: [] }));
await step('reattach', async () => {
  await client.request(server, { serviceId: 'pi.session-management', member: 'attach', args: ['session-2'] });
  await waitFor(() => client.attachment?.sessionId === 'session-2');
  return { sessionId: client.attachment.sessionId };
});
await step('disconnect', async () => { client.disconnect(); return { state: client.connectionState, attachment: client.attachment ?? null }; });
await step('request-while-disconnected', () => client.request(server, { serviceId: 'pi.session-management', member: 'detach', args: [] }));
await step('reconnect', async () => { const hello = await client.connect(); return { serverId: hello.serverId, attachment: client.attachment ?? null }; });
await step('dispose', async () => { await client.dispose(); return { disposed: true }; });
await step('request-disposed', () => client.request(server, { serviceId: 'x', member: 'y', args: [] }));
process.stdout.write(`${JSON.stringify(transcript)}\n`);
