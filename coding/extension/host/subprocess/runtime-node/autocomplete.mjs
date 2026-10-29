import { randomUUID } from "node:crypto";
import { AsyncLocalStorage } from "node:async_hooks";

// A packed Node cell can pass a captured provider directly between its members, without blocking one member's socket pump on another member's JavaScript callback.
const localProviders = new Map();
const currentLeases = new FinalizationRegistry(({ owner, id }) => {
  const runtime = owner.deref();
  if (!runtime?.conn || runtime.conn.closed) return;
  try { runtime.notify("ui.autocomplete.release", { id }); } catch { /* Connection close releases its captured providers. */ }
});

// pig divergence (D213): the request a provider chain is serving. Pi's editor awaits the chain in the Promise jobs after the terminal read (editor.ts
// startAutocompleteRequest), so a chain that settles in that microtask checkpoint has opened its menu before the next read. The host holds
// the next read for such a chain and lets go when the runtime reports that the chain left the checkpoint (D213): at the end of this
// runtime's macrotask the chain is neither settled nor waiting for the host's answer to a captured provider's call. The store is shared by the
// members of a packed cell, whose providers call each other directly.
const settling = new AsyncLocalStorage();

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
        const request = settling.getStore();
        if (request) request.hostCalls++;
        try { return await invoke("getSuggestions", { queryId, lines, cursorLine, cursorCol, force: options.force ?? false }, false); }
        catch (error) { if (options.signal.aborted) return null; throw error; }
        finally {
          options.signal.removeEventListener("abort", abort);
          if (request) { request.hostCalls--; request.checkpoint(); }
        }
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

  async suggest(args, signal, requestId) {
    const provider = this.providers.get(args.id);
    if (!provider) throw new Error("Autocomplete provider is no longer available");
    signal.throwIfAborted();
    const request = {
      hostCalls: 0,
      settled: false,
      announced: false,
      // The chain awaits real I/O when a macrotask ends with it unsettled and not waiting for the host.
      checkpoint: () => setImmediate(() => {
        if (request.settled || request.announced || request.hostCalls > 0) return;
        request.announced = true;
        try { this.runtime.notify("ui.autocomplete.pending", { requestId }); } catch { /* A closed connection drops the request. */ }
      }),
    };
    return await settling.run(request, async () => {
      request.checkpoint();
      try { return await provider.getSuggestions(args.lines, args.cursorLine, args.cursorCol, { signal, force: args.force }); }
      finally { request.settled = true; }
    });
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
