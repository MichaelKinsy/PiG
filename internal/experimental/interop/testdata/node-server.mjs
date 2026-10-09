// A pinned Pi 1.0.4 packages/server instance on a Unix socket, hosting the interop echo Sessions.
// Usage: node-server.mjs <socket path> [handshakeTimeoutMs]. Writes one JSON event per line; closes when stdin ends.
import { createInterface } from 'node:readline';
import { RemoteServiceError, RemoteServiceProvider, createRemoteServiceEndpoint, defineService, replicatedState } from '@earendil-works/chord';
import { BACKGROUND_CONTEXT } from '@earendil-works/chord/context';
import { createUnixServer } from '@earendil-works/pi-server/unix';
import { TestServerHost } from '@earendil-works/pi-server/testing';

const [path, handshake, serverId] = process.argv.slice(2);
const SERVER_ID = serverId ?? '00000000-0000-4000-8000-000000000001';
const emit = (event) => process.stdout.write(`${JSON.stringify(event)}\n`);
// Long strings are logged by length only, so the log stays comparable and small.
const summarize = (value) => JSON.parse(JSON.stringify(value, (_key, item) => (typeof item === 'string' && item.length > 1000 ? `<${[...item].length} code points>` : item)));

const Counter = defineService('interop.counter');

// One counter service per attachment, as each Pi presentation owns its provider: replicated state plus two mutating methods.
function createCounterEndpoint() {
  const state = replicatedState({ count: 0, label: 'start' });
  const provider = new RemoteServiceProvider([{ service: Counter, mode: 'singleton' }]);
  provider.provide(Counter, {
    state,
    bump: (n) => { state.change(BACKGROUND_CONTEXT, (draft) => { draft.count += n; }); },
    rename: (label) => { state.change(BACKGROUND_CONTEXT, (draft) => { draft.label = label; }); },
  });
  return createRemoteServiceEndpoint(provider);
}

class EchoHandle {
  constructor(metadata) { this.metadata = metadata; }
  attachClient() {
    const counter = createCounterEndpoint();
    return {
      invokeService: async (call, publish, context) => {
        emit({ event: 'call', call: summarize(call) });
        if (call.serviceId === 'interop.counter' || call.serviceId.startsWith('$chord.')) return counter.invoke(call, publish, context);
        switch (call.member) {
          case 'echo': return { echo: call };
          case 'undefined': return undefined;
          case 'null': return null;
          case 'fail': throw new RemoteServiceError('service_member_not_found', 'no such member');
          case 'secret': throw new Error('secret detail');
          case 'big': return { data: `${'x'.repeat(1 << 20)}é` };
          case 'hang':
            if (!context.abortSignal.aborted) await new Promise((resolve) => context.abortSignal.addEventListener('abort', resolve, { once: true }));
            emit({ event: 'abort', member: call.member });
            throw new Error('aborted');
          default: throw new RemoteServiceError('service_member_not_found', `unknown member ${call.member}`);
        }
      },
      release: () => { counter.dispose(); emit({ event: 'release', session: this.metadata.id }); },
    };
  }
  async close() { emit({ event: 'close', session: this.metadata.id }); }
}

class EchoHost extends TestServerHost {
  async openSession(metadata, context) {
    await super.openSession(metadata, context);
    emit({ event: 'open', session: metadata.id });
    return new EchoHandle(metadata);
  }
}

const host = new EchoHost();
await host.seed('session-1');
await host.seed('session-2');
const server = createUnixServer(host, {
  path,
  serverId: SERVER_ID,
  handshakeTimeoutMs: handshake === undefined || handshake === '' ? undefined : Number(handshake),
  onError: (error) => emit({ event: 'error', message: error.message }),
});
await server.start();
emit({ event: 'ready' });
for await (const _ of createInterface({ input: process.stdin })) { /* wait for stdin to end */ }
await server.close();
emit({ event: 'closed' });
