// Ports packages/coding-agent/src/experimental/plugins/bundled.ts and the isolated execution of packages/chord/src/node/bundle-loader.ts.
import { createFacetBundleArtifactLoader, createFacetBundleLoader } from "./chord/node.js";
import { isJsonValue, replicatedState } from "./chord/index.js";
import { awaitWithContext, BACKGROUND_CONTEXT, withAbortSignal } from "./chord/context/index.js";
import { getReplicatedStateInternals } from "./chord/services/state-internals.js";
import { validateRemoteServiceImplementation } from "./chord/services/provider.js";

const pluginAPI = "@earendil-works/pi-coding-agent/experimental/plugin";

export function createFacetBridge(host) {
  let nextId = 0;
  let loaded;
  let closed = false;
  const values = new Map();
  const identities = new WeakMap();
  const symbols = new Map();
  const hostValues = new Map();
  const hostIdentities = new WeakMap();
  const subscriptions = new Set();
  const contexts = new WeakMap();
  const contextValues = new Map();
  const contextSignals = new Map();
  const contextAborts = new Map();

  function own(value) {
    const identitiesFor = typeof value === "symbol" ? symbols : identities;
    let id = identitiesFor.get(value);
    if (id === undefined || !values.has(id)) {
      id = String(++nextId);
      identitiesFor.set(value, id);
      values.set(id, value);
    }
    return id;
  }

  function valueFor(id) {
    if (!values.has(id)) throw new Error(`Facet reference ${id} is released`);
    return values.get(id);
  }

  function encode(value) {
    if (value === undefined) return { kind: "undefined" };
    if (typeof value === "bigint") return { kind: "bigint", value: String(value) };
    if (typeof value === "number" && (!Number.isFinite(value) || Object.is(value, -0))) {
      return { kind: "number", value: Object.is(value, -0) ? "-0" : String(value) };
    }
    if (value === null || ["string", "number", "boolean"].includes(typeof value)) return { kind: "json", value };
    const hostId = hostIdentities.get(value);
    if (hostId !== undefined) return { kind: "host", id: hostId };
    if (typeof value === "object" && value !== null && typeof value.value === "function" && typeof value.toString === "function" && "abortSignal" in value) {
      const id = own(value);
      if (!contextValues.has(id)) {
        const signal = value.abortSignal;
        const origin = contexts.get(value);
        host.callSync({ op: "context", id, origin, cancellable: signal !== undefined, cancelled: signal?.aborted === true });
        const abort = () => host.callSync({ op: "contextCancel", id });
        signal?.addEventListener("abort", abort, { once: true });
        contextValues.set(id, () => signal?.removeEventListener("abort", abort));
      }
      return { kind: "context", id };
    }
    return { kind: "node", id: own(value), callable: typeof value === "function", asynchronous: typeof value === "function" && value.constructor?.name === "AsyncFunction", promise: value instanceof Promise, symbol: typeof value === "symbol" };
  }

  function decode(value, signal) {
    switch (value.kind) {
      case "undefined": return undefined;
      case "json": return value.value;
      case "bigint": return BigInt(value.value);
      case "number": return Number(value.value);
      case "node": {
        const result = valueFor(value.id);
        if (value.transient) values.delete(value.id);
        return result;
      }
      case "context": {
        const original = value.nodeOrigin === undefined ? BACKGROUND_CONTEXT : valueFor(value.nodeOrigin);
        let combined = signal;
        if (value.cancellable && value.origin !== undefined) {
          let contextSignal;
          if (value.aborted || contextAborts.has(value.origin)) {
            // An aborted Context needs no controller: the host encoded it after it was done, or its abort notification already arrived.
            contextSignal = AbortSignal.abort(new Error(value.aborted ? value.abortReason : contextAborts.get(value.origin)));
          } else {
            let controller = contextSignals.get(value.origin);
            if (!controller) {
              controller = new AbortController();
              contextSignals.set(value.origin, controller);
            }
            contextSignal = controller.signal;
          }
          combined = combined === undefined ? contextSignal : AbortSignal.any([combined, contextSignal]);
        }
        const context = combined === undefined || !value.cancellable ? original : withAbortSignal(combined, original);
        if (value.origin !== undefined) contexts.set(context, value.origin);
        return context;
      }
      case "host": return hostValue(value);
      default: throw new Error(`Invalid facet value kind: ${value.kind}`);
    }
  }

  function hostValue(description) {
    const existing = hostValues.get(description.id);
    if (existing !== undefined) return existing;
    if (description.promise) {
      const promise = host.call({ op: "await", id: description.id }).then(value => decode(value));
      hostValues.set(description.id, promise);
      hostIdentities.set(promise, description.id);
      return promise;
    }
    const target = description.callable ? (..._args) => undefined : Object.create(null);
    const proxy = new Proxy(target, {
      get(_target, property) {
        if (property === Symbol.toStringTag) return description.tag;
        if (typeof property !== "string") return undefined;
        return decode(host.callSync({ op: "get", id: description.id, property }));
      },
      apply(_target, receiver, args) {
        const request = { op: "call", id: description.id, receiver: encode(receiver), args: args.map(encode) };
        return description.asynchronous ? host.call(request).then(value => decode(value)) : decode(host.callSync(request));
      },
    });
    hostValues.set(description.id, proxy);
    hostIdentities.set(proxy, description.id);
    return proxy;
  }

  function environment(id) {
    const call = (method, args) => decode(host.callSync({ op: "environment", id, method, args }));
    return {
      use: service => call("use", { service }),
      provide: (service, implementation) => call("provide", { service, implementation: encode(implementation) }),
      provideMany: service => call("provideMany", { service }),
      observe: (service, handler) => call("observe", { service, handler: encode(handler) }),
      replicatedState(initial) {
        call("assertRunning", { operation: "create replicated state" });
        return replicatedState(initial);
      },
      own: callback => call("own", { callback: encode(callback) }),
      onActivate: callback => call("onActivate", { callback: encode(callback) }),
      onDeactivate: callback => call("onDeactivate", { callback: encode(callback) }),
    };
  }

  function resolveExternal(specifier, options) {
    if (options.resolveExternal) {
      const result = host.callSync({ op: "resolveExternal", specifier });
      if (result.defined) return result.value;
    }
    if (options.pluginAPI && specifier === pluginAPI) return new URL("./plugin.mjs", import.meta.url).href;
    return undefined;
  }

  function invoke(args, signal) {
    const callback = valueFor(args.id);
    if (typeof callback !== "function") throw new TypeError("Facet reference is not callable");
    return Reflect.apply(callback, args.receiver === undefined ? undefined : decode(args.receiver, signal), (args.args ?? []).map(value => decode(value, signal)));
  }

  function encodeJSON(value) {
    if (value === undefined) return { kind: "undefined" };
    if (!isJsonValue(value)) throw new TypeError("Facet value is not strict JSON");
    return { kind: "json", value };
  }

  return {
    async request(args, signal) {
      if (closed) throw new Error("Facet generation is closed");
      switch (args.op) {
        case "load": {
          if (loaded !== undefined) throw new Error("Facet generation is already loaded");
          const resolver = specifier => resolveExternal(specifier, args);
          const loader = args.artifact === undefined
            ? createFacetBundleLoader({ manifestPath: args.manifestPath, entry: args.entry, verifyIntegrity: args.verifyIntegrity, resolveExternal: resolver })
            : createFacetBundleArtifactLoader({ artifact: args.artifact, temporaryDirectory: args.temporaryDirectory, resolveExternal: resolver });
          loaded = await loader.load();
          return loaded.facets.map(facet => ({ id: facet.id, reference: own(facet) }));
        }
        case "invoke": return encode(await invoke(args, signal));
        case "invokeJSON": return encodeJSON(await invoke(args, signal));
        // The request owns only this observer, not the retained producer Promise.
        case "await": {
          const settled = awaitWithContext(Promise.resolve(valueFor(args.id)), withAbortSignal(signal, BACKGROUND_CONTEXT));
          if (!args.observation) return encode(await settled);
          // A keyed observation discards its fulfilment value and releases its private reference when it settles or is cancelled.
          try { await settled; return { kind: "undefined" }; } finally { values.delete(args.id); }
        }
        case "releaseLoaded": await loaded?.dispose(); return;
        case "close": {
          closed = true;
          const errors = [];
          try { await loaded?.dispose(); } catch (error) { errors.push(error); }
          for (const unsubscribe of [...subscriptions].reverse()) {
            try { unsubscribe(); } catch (error) { errors.push(error); }
          }
          subscriptions.clear();
          for (const remove of contextValues.values()) remove();
          contextValues.clear();
          contextSignals.clear();
          contextAborts.clear();
          values.clear();
          symbols.clear();
          hostValues.clear();
          if (errors.length === 1) throw errors[0];
          if (errors.length > 1) throw new AggregateError(errors, "Failed to close isolated facet generation");
          return;
        }
        default: throw new Error(`Unknown facet operation: ${args.op}`);
      }
    },
    sync(args) {
      if (closed) throw new Error("Facet generation is closed");
      switch (args.op) {
        case "setup": {
          const facet = valueFor(args.id);
          const result = facet.setup(environment(args.environment));
          if (result && typeof result.then === "function") {
            void Promise.resolve(result).catch(() => {});
            throw new Error(`Facet ${facet.id} setup must be synchronous`);
          }
          return;
        }
        case "contextAbort": {
          contextAborts.set(args.id, args.reason);
          contextSignals.get(args.id)?.abort(new Error(args.reason));
          // The record recreates an aborted controller for a request still in flight.
          contextSignals.delete(args.id);
          return;
        }
        // No request that carries this Context is in flight, so no request can decode it as unaborted.
        case "contextForget": contextAborts.delete(args.id); return;
        case "command": return encode(Object.freeze(Object.fromEntries(Object.entries(args.properties).map(([name, value]) => [name, decode(value)]))));
        case "array": return { ...encode(Object.freeze(args.values.map(value => decode(value)))), transient: true };
        case "get": return encode(Reflect.get(valueFor(args.id), args.property));
        case "json": return encodeJSON(valueFor(args.id));
        case "invoke": {
          // The host owns a synchronously returned Promise and observes its settlement through "await"; a rejection before that observation is not unhandled.
          const result = invoke(args);
          if (args.observation && (typeof result === "object" || typeof result === "function") && result !== null) {
            // Pi observes Promise.resolve(handler(...)) (chord services/instances.ts #start). The observation owns a private reference that its "await" releases; the result may share an identity reference the host still uses.
            const settled = Promise.resolve(result);
            void settled.catch(() => {});
            const id = String(++nextId);
            values.set(id, settled);
            return { kind: "node", id, promise: true };
          }
          if (result instanceof Promise) void result.catch(() => {});
          return encode(result);
        }
        case "release": for (const id of args.ids) values.delete(id); return;
        case "describeService": {
          const implementation = valueFor(args.id);
          validateRemoteServiceImplementation(args.serviceId, implementation);
          return Object.keys(implementation).sort().map(name => {
            const value = Object.getOwnPropertyDescriptor(implementation, name).value;
            return typeof value === "function"
              ? { name, kind: "method", value: encode(value) }
              : { name, kind: "state", value: encode(value) };
          });
        }
        case "watchState": {
          const state = getReplicatedStateInternals(valueFor(args.id));
          if (!state) throw new TypeError("Facet reference is not replicated state");
          const remove = state.subscribe((ops, sequence, context) => host.callSync({ op: "state", id: args.target, ops, sequence, context: encode(context) }));
          let disposed = false;
          const unsubscribe = () => {
            if (disposed) return;
            disposed = true;
            subscriptions.delete(unsubscribe);
            remove();
          };
          subscriptions.add(unsubscribe);
          const { value, sequence } = state.snapshot();
          return { sequence, value, unsubscribe: encode(unsubscribe) };
        }
        default: throw new Error(`Unknown synchronous facet operation: ${args.op}`);
      }
    },
  };
}
