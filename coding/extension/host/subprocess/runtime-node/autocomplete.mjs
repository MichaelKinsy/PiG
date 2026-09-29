import { randomUUID } from "node:crypto";

// A packed Node cell can pass a captured provider directly between its members, without blocking one member's socket pump on another member's JavaScript callback.
const localProviders = new Map();
const currentLeases = new FinalizationRegistry(({ owner, id }) => {
  const runtime = owner.deref();
  if (!runtime?.conn || runtime.conn.closed) return;
  try { runtime.notify("ui.autocomplete.release", { id }); } catch { /* Connection close releases its captured providers. */ }
});

export class AutocompleteRuntime {
  constructor(runtime) {
    this.runtime = runtime;
    this.factories = new Map();
    this.invoked = new Set();
    this.providers = new Map();
  }

  add(factory) {
    if (typeof factory !== "function") throw new TypeError("Autocomplete provider factory must be a function");
    const factoryId = randomUUID();
    this.factories.set(factoryId, factory);
    try { this.runtime.callSync("ui.addAutocompleteProvider", { factoryId }); }
    finally { if (!this.invoked.has(factoryId)) this.factories.delete(factoryId); }
  }

  current(descriptor) {
    if (descriptor.localId && localProviders.has(descriptor.localId)) {
      const provider = localProviders.get(descriptor.localId);
      this.runtime.notify("ui.autocomplete.release", { id: descriptor.id });
      if (descriptor.triggerCharacters != null) provider.triggerCharacters = descriptor.triggerCharacters;
      return provider;
    }
    const lease = { id: descriptor.id, runtime: this.runtime };
    currentLeases.register(lease, { id: descriptor.id, owner: new WeakRef(this.runtime) });
    const invoke = (operation, args, sync) => lease.runtime[sync ? "callSync" : "call"]("ui.autocomplete.invoke", { id: lease.id, operation, ...args });
    const provider = {
      triggerCharacters: descriptor.triggerCharacters ?? [],
      getSuggestions: async (lines, cursorLine, cursorCol, options) => {
        if (options.signal.aborted) return null;
        const queryId = randomUUID();
        const abort = () => this.runtime.fireAndForget("ui.autocomplete.cancel", { queryId });
        options.signal.addEventListener("abort", abort, { once: true });
        try { return await invoke("getSuggestions", { queryId, lines, cursorLine, cursorCol, force: options.force ?? false }, false); }
        catch (error) { if (options.signal.aborted) return null; throw error; }
        finally { options.signal.removeEventListener("abort", abort); }
      },
      applyCompletion: (lines, cursorLine, cursorCol, item, prefix) => invoke("applyCompletion", { lines, cursorLine, cursorCol, item, prefix }, true),
    };
    if (descriptor.hasFileTrigger) provider.shouldTriggerFileCompletion = (lines, cursorLine, cursorCol) => invoke("shouldTriggerFileCompletion", { lines, cursorLine, cursorCol }, true);
    return provider;
  }

  sync(args) {
    if (args.operation === "changed") {
      this.runtime.editorHost.setAutocompleteProvider(args.current);
      return null;
    }
    if (args.operation === "wrap") {
      const factory = this.factories.get(args.factoryId);
      if (!factory) throw new Error("Autocomplete factory is no longer available");
      this.invoked.add(args.factoryId);
      const provider = factory(this.current(args.current));
      if (!provider || typeof provider.getSuggestions !== "function" || typeof provider.applyCompletion !== "function") throw new TypeError("Autocomplete factory must return a provider");
      const id = randomUUID();
      this.providers.set(id, provider);
      localProviders.set(id, provider);
      return { id, triggerCharacters: provider.triggerCharacters ?? [], hasFileTrigger: typeof provider.shouldTriggerFileCompletion === "function" };
    }
    const provider = this.providers.get(args.id);
    if (!provider) throw new Error("Autocomplete provider is no longer available");
    if (args.operation === "applyCompletion") return provider.applyCompletion(args.lines, args.cursorLine, args.cursorCol, args.item, args.prefix);
    if (args.operation === "shouldTriggerFileCompletion") return provider.shouldTriggerFileCompletion?.(args.lines, args.cursorLine, args.cursorCol) ?? true;
    throw new Error(`Unknown autocomplete operation: ${args.operation}`);
  }

  async suggest(args, signal) {
    const provider = this.providers.get(args.id);
    if (!provider) throw new Error("Autocomplete provider is no longer available");
    signal.throwIfAborted();
    return await provider.getSuggestions(args.lines, args.cursorLine, args.cursorCol, { signal, force: args.force });
  }

  release(id) {
    this.providers.delete(id);
    localProviders.delete(id);
  }

  dispose() {
    for (const id of this.providers.keys()) localProviders.delete(id);
    this.providers.clear();
    this.factories.clear();
    this.invoked.clear();
  }
}
