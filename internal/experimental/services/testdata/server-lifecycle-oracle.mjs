import assert from 'node:assert/strict';
import { registerHooks } from 'node:module';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
const root = resolve(process.argv[2]);
const source = path => pathToFileURL(resolve(root, '.upstream/current/packages', path)).href;
registerHooks({ resolve(specifier, context, next) {
  const paths = {'@earendil-works/chord': 'chord/src/index.ts', '@earendil-works/chord/context': 'chord/src/context/index.ts'};
  return next(paths[specifier] ? source(paths[specifier]) : specifier, context);
}});
globalThis.fetch = () => { throw new Error('network forbidden'); };
const {createExperimentalServerServices} = await import(source('coding-agent/src/experimental/services/server.ts'));
const {createServiceCatalogueCall, createServiceSubscribeCall} = await import(source('chord/src/index.ts'));
const {BACKGROUND_CONTEXT: ctx} = await import(source('chord/src/context/index.ts'));
const events = [];
const entered = Promise.withResolvers(), release = Promise.withResolvers();
const services = await createExperimentalServerServices({
  async list() { events.push('list'); return []; },
  async create() { events.push('create:start'); entered.resolve(); await release.promise; events.push('create:rejected'); throw new Error('cancel create'); },
  async remove(id) { events.push(`remove:${id}`); },
});
const presentation = {async attachSession() {}, async detachSession() {}, async prepareSessionRemoval(id) {events.push(`prepare:${id}`);}};
const a = await services.host.attachClient(presentation, ctx), b = await services.host.attachClient(presentation, ctx);
let updates = 0;
for (const attachment of [a, b]) await attachment.invokeService(createServiceSubscribeCall('same', 'pi.session-directory', 'singleton'), () => {updates++;}, ctx);
const first = a.invokeService({serviceId: 'pi.session-management', member: 'create', args: [{}]}, () => {}, ctx).catch(e => e.message);
await entered.promise;
const second = b.invokeService({serviceId: 'pi.session-management', member: 'remove', args: ['a']}, () => {}, ctx);
const refresh = services.refresh();
let disposed = false;
const disposing = services.dispose().then(() => { disposed = true; });
for (const attachment of [a, b]) await assert.rejects(attachment.invokeService(createServiceCatalogueCall(), () => {}, ctx), {message: 'Server service attachment is released'});
assert.equal(disposed, false);
assert.deepEqual(events, ['list', 'create:start']);
release.resolve();
assert.equal(await first, 'cancel create');
await Promise.all([second, refresh, disposing]);
assert.deepEqual(events, ['list', 'create:start', 'create:rejected', 'prepare:a', 'remove:a', 'list', 'list']);
assert.equal(updates, 0);
await services.dispose();
console.log(JSON.stringify({events, updates, disposed}));
