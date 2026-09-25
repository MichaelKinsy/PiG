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
const {createRemoteServiceBinding, createServiceCatalogueCall, createServiceSubscribeCall, createServiceUnsubscribeCall} = await import(source('chord/src/index.ts'));
const {BACKGROUND_CONTEXT: ctx} = await import(source('chord/src/context/index.ts'));
const {SessionDirectory} = await import(source('coding-agent/src/experimental/services/sessions.ts'));
const clone = value => value === undefined ? undefined : JSON.parse(JSON.stringify(value));
const events = [], selections = [], values = [], failures = [];
let sessions = ['b', 'a'].map(sessionId => ({serverId: 'server', sessionId, createdAt: 12}));
let failDetach = false;
const services = await createExperimentalServerServices({
  async list() { events.push('list'); return clone(sessions); },
  async create(options) { events.push('create'); const created = {serverId: 'server', sessionId: options.id ?? 'generated', createdAt: 23}; sessions.push(created); return created; },
  async remove(id) { events.push(`remove:${id}`); sessions = sessions.filter(session => session.sessionId !== id); },
  async prepareSessionPlugins(id, paths) {
    selections.push({sessionId: id, packagePaths: paths ?? null});
    if (id === 'bad') throw new Error('prepare failed');
    return {packagePaths: paths ?? ['default'], presentationPlugins: {prepared: id}};
  },
  async reloadPresentationPlugins(paths) { return clone(paths); },
});
const presentation = {
  async attachSession(id) { events.push(`attach:${id}`); },
  async detachSession() { events.push('detach'); if (failDetach) throw new Error('detach failed'); },
  async prepareSessionRemoval(id) { events.push(`prepareRemoval:${id}`); },
};
const a = await services.host.attachClient(presentation, ctx), b = await services.host.attachClient(presentation, ctx);
const call = (attachment, serviceId, member, args = []) => attachment.invokeService(clone({serviceId, member, args}), () => {}, ctx).then(clone);
const record = async operation => { try { values.push((await operation()) ?? null); } catch (e) { failures.push(e.message); } };
const catalogue = await a.invokeService(createServiceCatalogueCall(), () => {}, ctx);
let id = 0;
const binding = createRemoteServiceBinding({services: [SessionDirectory], transport: {
  invoke: (call, context) => a.invokeService(clone(call), () => {}, context).then(clone),
  async subscribe(serviceId, mode, listener, context) {
    const subscriptionId = String(++id);
    let active = false;
    const pending = [];
    const publish = (_, update, context) => { if (active) listener(clone(update), context); else pending.push([clone(update), context]); };
    const snapshot = clone(await a.invokeService(createServiceSubscribeCall(subscriptionId, serviceId, mode), publish, context));
    return {snapshot, activate() { active = true; for (const [update, context] of pending) listener(update, context); pending.length = 0; }, close: () => a.invokeService(createServiceUnsubscribeCall(subscriptionId), publish, ctx)};
  },
}});
const directory = binding.use(SessionDirectory);
await binding.ready(ctx);
const snapshots = [];
const unsubscribe = directory.state.subscribe(value => snapshots.push(clone(value)));
let bUpdates = 0;
await b.invokeService(createServiceSubscribeCall('same', SessionDirectory.id, 'singleton'), () => { bUpdates++; }, ctx);
await record(() => call(b, 'pi.presentation-plugins', 'reload'));
await record(() => call(a, 'pi.presentation-plugins', 'prepareSession', [{sessionId: 'a', packagePaths: null}]));
await record(() => call(a, 'pi.presentation-plugins', 'reload'));
await record(() => call(b, 'pi.presentation-plugins', 'prepareSession', [{sessionId: 'b', packagePaths: []}]));
await record(() => call(b, 'pi.presentation-plugins', 'reload'));
await record(() => call(a, 'pi.presentation-plugins', 'prepareSession', [{sessionId: 'a', packagePaths: ['x', 'x', 'é']}]));
await record(() => call(a, 'pi.presentation-plugins', 'prepareSession', [{sessionId: 'bad', packagePaths: ['bad']}]));
await record(() => call(a, 'pi.presentation-plugins', 'reload'));
failDetach = true;
await record(() => call(a, 'pi.session-management', 'detach'));
await record(() => call(a, 'pi.presentation-plugins', 'reload'));
failDetach = false;
await record(() => call(a, 'pi.session-management', 'detach'));
await record(() => call(a, 'pi.presentation-plugins', 'reload'));
await record(() => call(b, 'pi.presentation-plugins', 'reload'));
await record(() => call(a, 'pi.session-management', 'create', [{id: ''}]));
await record(() => call(a, 'pi.session-management', 'attach', ['b']));
await record(() => call(a, 'pi.session-management', 'remove', ['a']));
await b.release(ctx);
await services.refresh();
await record(() => call(b, 'pi.presentation-plugins', 'reload'));
unsubscribe();
await binding.dispose(ctx);
await services.dispose();
await record(() => call(a, 'pi.presentation-plugins', 'reload'));
console.log(JSON.stringify({catalogue, values, failures, selections, snapshots, bUpdates, events}));
