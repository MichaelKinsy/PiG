import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { setImmediate } from 'node:timers/promises';

const pin = readFileSync('internal/coding/pigversion/pigversion.go', 'utf8').match(/UpstreamVersion = "([^"]+)"/)[1];
const root = process.env.PI_PACKAGE_ROOT ?? resolve('extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent');
const agent = join(root, 'node_modules/@earendil-works/pi-agent-core');
for (const path of [root, agent]) {
  assert.equal(JSON.parse(readFileSync(join(path, 'package.json'), 'utf8')).version, pin, 'Pi oracle version');
}
const { Harness, MemoryStorage, defineTask } = await import(pathToFileURL(join(agent, 'dist/harness/pico3/index.js')));
const { BACKGROUND_CONTEXT: ctx } = await import(pathToFileURL(join(root, 'node_modules/@earendil-works/chord/dist/context/index.js')));

// harness.ts:366-389 snapshots unknown tasks before resume returns, even though persistence and scheduler startup finish asynchronously.
for (const name of ['known', 'new', 'unknown']) {
  const h = await Harness.open(new MemoryStorage(), { models: {} }, ctx);
  try {
    const root = await h.root(ctx);
    const release = h.hold();
    const makeKind = (label) => defineTask({
      name: 'resume-reload',
      async initial() { return { done: () => ({ status: 'completed', result: label }) }; },
      phases: {},
      async abort() { return () => null; },
    });
    const old = makeKind('old');
    const off = h.registerTaskKind(old);
    const create = () => root.commit(tx => tx.createTask(old, null, { conversationId: root.id, background: true }), ctx);
    let ref;
    if (name !== 'new') ref = await create();
    if (name === 'unknown') off();
    h.resume();
    if (name === 'unknown') h.registerTaskKind(makeKind('replacement'));
    if (name === 'new') ref = await create();
    off();
    // Drain microtasks while the kind is absent, not a timed grace period.
    await setImmediate();
    const held = (await h.getTask(ref.id, ctx)).status;
    assert.equal(held, name === 'unknown' ? 'terminal' : 'pending');
    if (name !== 'unknown') h.registerTaskKind(makeKind('replacement'));
    release();
    const task = await h.waitForTask(ref.id, ctx);
    assert.deepEqual(task.outcome, name === 'unknown' ? { status: 'orphaned' } : { status: 'completed', result: 'replacement' });
    console.log(`PICO3_RESUME ${name} ${held} ${task.outcome.status} ${task.outcome.result ?? '-'}`);
  } finally {
    await h.close(ctx);
  }
}
