import { registerHooks } from 'node:module';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const root = resolve(process.argv[2]);
const source = (path) => pathToFileURL(resolve(root, '.upstream/current/packages', path)).href;
registerHooks({
  resolve(specifier, context, nextResolve) {
    const replacements = {
      '@earendil-works/chord': 'chord/src/index.ts',
      '@earendil-works/chord/context': 'chord/src/context/index.ts',
      '@earendil-works/pi-ai': 'ai/src/models.ts',
    };
    if (replacements[specifier]) return nextResolve(source(replacements[specifier]), context);
    return nextResolve(specifier, context);
  },
});
const { createModelsService, createModelsServiceFacet } = await import(source('coding-agent/src/experimental/services/models-provider.ts'));
const { replicatedState } = await import(source('chord/src/api.ts'));
const { BACKGROUND_CONTEXT: ctx } = await import(source('chord/src/context/index.ts'));

const local = {provider: 'local', id: 'chosen', name: 'name-chosen', reasoning: true};
const other = {provider: 'other', id: 'chosen', name: 'name-chosen', reasoning: false};
let selected = local;
let thinking = 'high';
let failRefresh = false;
let warnings = true;
const persisted = [];
const lane = {
  async getModel() { return selected; },
  async setModel(ref) { selected = [local, other].find(m => m.provider === ref.provider && m.id === ref.modelId); },
  async getThinkingLevel() { return thinking; },
  async setThinkingLevel(level) { thinking = level; },
};
const modelRuntime = {
  getAvailableSnapshot() { return [other]; },
  getModel(provider, id) { return [local, other].find(m => m.provider === provider && m.id === id); },
  async refresh() {
    if (failRefresh) throw new Error('refresh failed');
    return {aborted: false, errors: warnings ? new Map([['local', new Error('unavailable')]]) : new Map()};
  },
};
const settings = {
  setDefaultModelAndProvider(provider, id) { persisted.push({provider, modelId: id}); },
  async flush() { persisted.push('flushed'); },
};
const snapshots = [];
let service;
let activate;
const facet = createModelsServiceFacet({lane, modelRuntime, settingsManager: settings});
facet.setup({
  replicatedState(initial) {
    const state = replicatedState(initial);
    state.subscribe(value => snapshots.push(structuredClone(value)));
    return state;
  },
  provide(token, value) { if (token.id !== 'pi.models') throw new Error(token.id); service = value; },
  onActivate(callback) { activate = callback; },
});
await activate();
const levels = await service.getThinkingLevels(ctx);
await service.cycleThinking(ctx);
await service.selectThinking('medium', ctx);
await service.refresh(ctx);
warnings = false;
await service.refresh(ctx);
await service.select({provider: 'other', modelId: 'chosen'}, ctx);
const failures = [];
for (const operation of [
  () => service.selectThinking('max', ctx),
  () => service.select({provider: 'missing', modelId: 'unknown'}, ctx),
  () => { failRefresh = true; return service.refresh(ctx); },
]) {
  try { await operation(); } catch (error) { failures.push(error.message); }
}
const empty = createModelsService({...lane, async getModel() { return undefined; }, async getThinkingLevel() { return 'off'; }}, undefined, undefined, replicatedState);
await empty.activate(ctx);
await empty.service.refresh(ctx);
console.log(JSON.stringify({facetId: facet.id, snapshots, levels, persisted, failures, empty: empty.service.state.value}));
