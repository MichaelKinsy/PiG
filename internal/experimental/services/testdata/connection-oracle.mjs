// Probe the exact connection.ts bodies with local client/Chord dependencies. The imports alone are replaced; no network or installed runtime is needed.
import { readFile } from 'node:fs/promises';
import { stripTypeScriptTypes } from 'node:module';

const original = await readFile(new URL('../../../../.upstream/current/packages/coding-agent/src/experimental/services/connection.ts', import.meta.url), 'utf8');
const stripped = stripTypeScriptTypes(original, {mode: 'strip'});
let imports = 0;
const body = stripped.replace(/import\s+[\s\S]*?from\s+"@earendil-works\/(?:chord(?:\/context)?|pi-client)";/g, () => { imports++; return ''; });
if (imports !== 3) throw new Error(`unexpected runtime import count: ${imports}`);

const hostBindings = [];
function createRemoteServiceBinding(options) {
  const binding = {
    bounds: [], closed: 0,
    use: service => service.id,
    observe: () => () => {},
    async ready() {},
    async rebind(bound) { this.bounds.push(bound); },
    async dispose() { this.closed++; },
  };
  hostBindings.push(binding);
  return binding;
}
function replicatedState(initial) {
  const listeners = new Set();
  return {
    value: initial,
    replace(context, value) {
      if (JSON.stringify(this.value) === JSON.stringify(value)) return;
      this.value = value;
      for (const listener of listeners) listener(value);
    },
    subscribe(listener) { listeners.add(listener); listener(this.value); return () => listeners.delete(listener); },
  };
}
globalThis.connectionProbeDependencies = {createRemoteServiceBinding, replicatedState};
const prelude = `const {createRemoteServiceBinding, replicatedState} = globalThis.connectionProbeDependencies;
const BACKGROUND_CONTEXT = {};
const createClientServiceTransport = (client, target) => ({client, target});\n`;
const {createServerServiceSource, createSessionServiceSource} = await import(`data:text/javascript;base64,${Buffer.from(prelude + body).toString('base64')}`);
const context = {};
function client() {
  return {
    serverId: 'server', connectionState: 'connecting', attachment: undefined,
    get connected() { return this.connectionState === 'connected'; },
    onConnectionStateChange(listener) { this.connectionListener = listener; return () => { this.connectionListener = undefined; }; },
    onAttachmentChange(listener) { this.attachmentListener = listener; return () => { this.attachmentListener = undefined; }; },
    async serviceCatalogue() { return [{serviceId: 'pi.agent-controller', mode: 'singleton'}]; },
    connect(state, error) { this.connectionState = state; this.connectionListener?.({state, error}); },
    attach(target) { this.attachment = target; this.attachmentListener?.(target); },
  };
}
const options = {services: [{id: 'pi.agent-controller'}], assertAccess() {}, onError(error) { throw error; }};
const serverClient = client();
const server = createServerServiceSource(serverClient);
const active = server.open(options);
server.open(options);
await active.ready(context);
serverClient.connect('connected');
serverClient.connect('disconnected', new Error('wire closed'));
const disconnected = server.connection.value;
serverClient.connect('connecting');
const connecting = server.connection.value;
serverClient.connect('connected');
await server.dispose(context);

const sessionClient = client();
const session = createSessionServiceSource(sessionClient);
const sessionBinding = session.open(options);
const states = [];
session.attachment.subscribe(state => states.push(state));
await sessionBinding.ready(context);
sessionClient.attach({serverId: 'server', sessionId: 'session', attachmentId: 'a'});
await session.whenAttached('session', context);
const catalogue = await session.catalogue(context);
sessionClient.attach(undefined);
await session.whenDetached(context);
const cached = await session.catalogue(context);
await session.dispose(context);
// The changing wall clock is independently tested as ISO milliseconds; all other state and ownership output is compared exactly.
const {since, ...disconnectWithoutClock} = disconnected;
console.log(JSON.stringify({bounds: hostBindings.map(binding => binding.bounds), closed: hostBindings.map(binding => binding.closed), connecting, disconnected: disconnectWithoutClock, states, catalogue, cached, acceptsUnavailable: session.acceptsUnavailableServices, listenersRemoved: !serverClient.connectionListener && !sessionClient.attachmentListener}));
