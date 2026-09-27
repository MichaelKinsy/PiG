import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';

const pin = readFileSync('coding/pigversion/pigversion.go', 'utf8').match(/UpstreamVersion = "([^"]+)"/)[1];
const root = process.env.PI_PACKAGE_ROOT ?? resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
const chord = join(root, 'node_modules/@earendil-works/chord');
for (const path of [root, chord]) {
  assert.equal(JSON.parse(readFileSync(join(path, 'package.json'), 'utf8')).version, pin, 'Pi oracle version');
}
const {
  createRemoteServiceBinding, createRemoteServiceEndpoint,
  createServiceSubscribeCall, createServiceUnsubscribeCall,
  defineService, RemoteServiceProvider, replicatedState,
} = await import(pathToFileURL(join(chord, 'dist/index.js')));
const { BACKGROUND_CONTEXT: ctx } = await import(pathToFileURL(join(chord, 'dist/context/index.js')));
const copy = value => value === undefined ? undefined : JSON.parse(JSON.stringify(value));
const Counter = defineService('test.counter');
const expected = [];

// Pi packages/chord/src/services/consumer.ts:575-613 installs the snapshot before activating buffered updates.
// Run both observer schedules through the installed provider, endpoint and binding.
for (const installed of [false, true]) {
  const provider = new RemoteServiceProvider([Counter]);
  const state = replicatedState({ count: 0, log: [] });
  const implementation = {
    state,
    async add(amount, label, context) {
      state.change(context, draft => {
        draft.count += amount;
        draft.log.push(label);
      });
      return state.value.count;
    },
    async fail() { throw new Error('counter failure'); },
  };
  await implementation.add(1, 'before-subscribe', ctx);
  provider.provide(Counter, implementation);
  const endpoint = createRemoteServiceEndpoint(provider);
  const entered = Promise.withResolvers();
  const release = Promise.withResolvers();
  const errors = [];
  const binding = createRemoteServiceBinding({
    services: [Counter],
    onError: error => errors.push(error),
    transport: {
      invoke: async (call, context) => copy(await endpoint.invoke(copy(call), () => {}, context)),
      async subscribe(id, mode, listener, context) {
        // This test adapter copies JSON and buffers endpoint publications until activation, like PiG's JSON-copy transport.
        const buffered = [];
        let active = false;
        const snapshot = copy(await endpoint.invoke(createServiceSubscribeCall('s1', id, mode), (_id, update, updateContext) => {
          if (active) listener(copy(update), updateContext);
          else buffered.push([copy(update), updateContext]);
        }, context));
        entered.resolve();
        await release.promise;
        return {
          snapshot,
          activate() {
            for (const args of buffered) listener(...args);
            buffered.length = 0;
            active = true;
          },
          close: () => endpoint.invoke(createServiceUnsubscribeCall('s1'), () => {}, ctx),
        };
      },
    },
  });
  try {
    const service = binding.use(Counter);
    await entered.promise;
    if (installed) {
      release.resolve();
      await binding.ready(ctx);
    }
    assert.equal(service.state.value !== undefined, installed);
    const deliveries = [];
    service.state.subscribe((value, _context, info) => {
      deliveries.push({ Kind: info.kind, Sequence: info.sequence, Value: value });
    });
    assert.equal(await service.add(3, 'remote', ctx), 4);
    if (!installed) assert.deepEqual(deliveries, []);
    release.resolve();
    await binding.ready(ctx);
    assert.deepEqual(deliveries, [
      { Kind: 'hydrate', Sequence: 1, Value: { count: 1, log: ['before-subscribe'] } },
      { Kind: 'update', Sequence: 2, Value: { count: 4, log: ['before-subscribe', 'remote'] } },
    ]);
    assert.deepEqual(errors, []);
    expected.push(`CHORD_REPLICA installed=${installed} ${JSON.stringify(deliveries)}`);
  } finally {
    release.resolve();
    await binding.dispose(ctx);
    endpoint.dispose();
    provider.dispose();
  }
}

// Chord is outside PORT_MAP's package denominator, so its paired proof does not claim unrelated covers entries.
const output = execFileSync('go', ['test', '-count=1', '-run', '^TestSingletonReplicaHydratesThenReceivesOperationStream$', '-v', './internal/chord'], { encoding: 'utf8' });
const actual = output.split('\n').filter(line => line.startsWith('CHORD_REPLICA '));
assert.deepEqual(actual, expected);
console.log(expected.join('\n'));
