// Executes the pinned TypeScript modules with local dependency adapters, never a live gateway.
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const ts = require(path.resolve('parity/interface-extractor/node_modules/typescript'));
function load(file, dependencies) {
  const source = fs.readFileSync(path.join('.upstream/current/packages/coding-agent/src/experimental', file), 'utf8');
  const js = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText;
  const exports = {};
  new Function('require', 'exports', js)((name) => {
    if (!(name in dependencies)) throw Error(`unexpected dependency ${name}`);
    return dependencies[name];
  }, exports);
  return exports;
}
const { RadiusRelayAuthResolver } = load('radius-auth.ts', {
  'node:fs/promises': require('node:fs/promises'),
  '@earendil-works/pi-ai/providers/radius-config': { normalizeRadiusGatewayUrl: v => (/^https?:\/\//i.test(v) ? v : `https://${v}`).replace(/\/+$/, '') },
  '../cli/auth-command.ts': { getAuthCredential: () => { throw Error('stored auth not selected by this probe'); } },
  '../core/model-runtime.ts': { ModelRuntime: { create: () => { throw Error('stored auth not selected by this probe'); } } },
  '../core/radius.ts': { getRadiusGatewayUrl: () => { throw Error('implicit gateway forbidden'); } },
  '../utils/paths.ts': { resolvePath: v => path.resolve(v) },
});
const relay = load('radius-relay.ts', {
  '@earendil-works/pi-protocol': { DEFAULT_MAX_FRAME_LENGTH: 16 * 1024 * 1024 },
  undici: { WebSocket: class { constructor() { throw Error('live socket forbidden'); } } },
});
const id = '00000000-0000-4000-8000-000000000002';
class Socket {
  listeners = new Map(); sent = []; protocol = ''; bufferedAmount = 0; OPEN = 1; readyState = 1;
  addEventListener(name, fn) { if (!this.listeners.has(name)) this.listeners.set(name, new Set()); this.listeners.get(name).add(fn); }
  removeEventListener(name, fn) { this.listeners.get(name)?.delete(fn); }
  emit(name, value) { for (const fn of [...(this.listeners.get(name) ?? [])]) fn(value); }
  send(data) { this.sent.push(data); }
  close(code, reason) { if (this.readyState === 3) return; this.readyState = 3; this.emit('close', {code, reason}); }
}
const tick = () => new Promise(resolve => setImmediate(resolve));
(async () => {
  const payload = Uint8Array.from([0, 1, 2, 255]);
  const frame = relay.encodeRelayDataFrame(id, payload);
  assert.equal(Buffer.from(frame).toString('hex'), '010100000000000040008000000000000002000102ff');
  assert.deepEqual([...new Uint8Array(relay.parseRelayDataFrame(frame).payload)], [...payload]);
  assert.equal(relay.parseRelayDataFrame(new ArrayBuffer(17)), undefined);
  const auth = new RadiusRelayAuthResolver({ type: 'token', token: '\ufeff secret\n' }, 'http://localhost');
  assert.deepEqual(await auth.resolve({required: true}), {gateway: 'http://localhost', token: 'secret'});
  const prior = process.env.PI_OFFLINE;
  process.env.PI_OFFLINE = '';
  assert.equal(await auth.resolve({required: false}), undefined);
  await assert.rejects(auth.resolve({required: true}), /unavailable in offline mode/);
  if (prior === undefined) delete process.env.PI_OFFLINE; else process.env.PI_OFFLINE = prior;
  const socket = new Socket();
  const statuses = []; let accepted; const received = []; let closed = 0;
  const host = new relay.RadiusRelayHost({serverId: id, auth, server: {accept(connection) { accepted = connection; return {onData: chunk => received.push([...chunk]), onClose: () => closed++, onError: error => {throw error;}}; }}, onStatus: s => statuses.push(s.status), webSocketFactory: options => {
    assert.equal(options.authorization, 'Bearer secret');
    assert.equal(options.url, `ws://localhost/v1/session-relays/${id}/connect`);
    socket.protocol = options.protocol;
    setImmediate(() => socket.emit('open', {})); return socket;
  }});
  host.start(); await tick(); await tick();
  assert.deepEqual(statuses, ['connecting', 'connected']);
  socket.emit('message', {data: JSON.stringify({version:1, type:'connection_open', connection_id:id})});
  socket.emit('message', {data: frame});
  assert.deepEqual(received, [[0,1,2,255]]);
  await accepted.close(Uint8Array.from([9]));
  assert.deepEqual([...new Uint8Array(relay.parseRelayDataFrame(socket.sent[0]).payload)], [9]);
  assert.equal(socket.sent[1], JSON.stringify({version:1,type:'connection_close',connection_id:id,code:1000}));
  assert.equal(closed, 0);
  await host.close();

  // #handleHostMessage/#openConnection enqueue controls without awaiting the writer's drain.
  const blocked = new Socket();
  const otherId = '00000000-0000-4000-8000-000000000003';
  const rejectedId = '00000000-0000-4000-8000-000000000004';
  const events = []; const connections = [];
  const backpressured = new relay.RadiusRelayHost({serverId: id, auth, server: {accept(connection) {
    if (connections.length === 1) { connections.push(connection); void connection.close(); return {}; }
    connections.push(connection); events.push('open');
    return {onData: chunk => events.push([...chunk]), onClose: () => events.push('close'), onError: error => {throw error;}};
  }}, webSocketFactory: options => {
    blocked.protocol = options.protocol;
    setImmediate(() => blocked.emit('open', {})); return blocked;
  }});
  backpressured.start(); await tick(); await tick();
  try {
    blocked.emit('message', {data: JSON.stringify({version:1, type:'connection_open', connection_id:id})});
    blocked.bufferedAmount = 1024 * 1024 + 1;
    blocked.emit('message', {data: JSON.stringify({version:1, type:'ping'})});
    await tick();
    assert.deepEqual(blocked.sent, [JSON.stringify({version:1, type:'pong'})]);
    blocked.emit('message', {data: relay.encodeRelayDataFrame(otherId, payload)});
    blocked.emit('message', {data: JSON.stringify({version:1, type:'connection_open', connection_id:rejectedId})});
    blocked.emit('message', {data: frame});
    blocked.emit('message', {data: JSON.stringify({version:1, type:'connection_close', connection_id:id})});
    blocked.emit('message', {data: JSON.stringify({version:1, type:'connection_open', connection_id:otherId})});
    assert.deepEqual(events, ['open', [0,1,2,255], 'close', 'open']);
    assert.equal(blocked.sent.length, 1);
    const after = connections[2].send(payload);
    blocked.bufferedAmount = 0;
    await after;
    assert.deepEqual(blocked.sent, [
      JSON.stringify({version:1, type:'pong'}),
      JSON.stringify({version:1, type:'connection_close', connection_id:otherId, code:1000}),
      JSON.stringify({version:1, type:'connection_close', connection_id:rejectedId, code:1012}),
      relay.encodeRelayDataFrame(otherId, payload),
    ]);
  } finally {
    blocked.bufferedAmount = 0;
    await backpressured.close();
  }
  console.log('pinned upstream Radius auth/envelope/host/final-chunk/backpressure: pass');
})().catch(error => { console.error(error); process.exitCode = 1; });
