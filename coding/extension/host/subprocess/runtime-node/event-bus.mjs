// Ports packages/coding-agent/src/core/event-bus.ts
// pig additive (D19): the one pi.events bus shared by every Node realm. A process that is the Host's only Node realm keeps Pi's in-heap EventEmitter. With several realms, the Host holds the ordered listener registry and dispatches each listener's synchronous prefix in its own realm; payloads cross as xref references, so every listener sees the emitter's original object.
import { EventEmitter } from "node:events";

export class SharedBus {
  constructor(realm, routed) {
    this.realm = realm;
    this.routed = routed;
    this.emitter = new EventEmitter();
    this.handlers = new Map(); // handler id -> entry
    this.bySafe = new WeakMap(); // safeHandler -> entry
    this.nextId = 0;
  }

  on(runtime, channel, handler) {
    const safeHandler = async (data) => {
      try {
        await handler(data);
      } catch (err) {
        console.error(`Event handler error (${channel}):`, err);
      }
    };
    const entry = { id: String(++this.nextId), channel, safeHandler, runtime, subscribed: true, registered: false };
    this.handlers.set(entry.id, entry);
    this.bySafe.set(safeHandler, entry);
    if (this.routed) {
      try {
        runtime.busCall("events.on", { channel, handlerId: entry.id });
      } catch (error) {
        this.handlers.delete(entry.id);
        throw error;
      }
      entry.registered = true;
    } else {
      this.emitter.on(channel, safeHandler);
    }
    return () => this.off(entry);
  }

  off(entry) {
    if (!entry.subscribed) return;
    entry.subscribed = false;
    if (entry.registered) {
      // The Host keeps the handler callable for dispatch snapshots taken before removal and releases it afterward.
      entry.runtime.busCall("events.off", { handlerId: entry.id });
      return;
    }
    this.handlers.delete(entry.id);
    this.emitter.off(entry.channel, entry.safeHandler);
  }

  emit(runtime, channel, data) {
    if (!this.routed) {
      this.emitter.emit(channel, data);
      return;
    }
    const result = runtime.busCall("events.emit", { channel, data: this.realm.encode(data) });
    if (result?.emission) {
      queueMicrotask(() => {
        try { runtime.busCall("events.settle", { emission: result.emission }); }
        catch {}
      });
    }
    if (result?.unhandledError === true) {
      // EventEmitter's unhandled "error" rule, applied to the original payload.
      EventEmitter.prototype.emit.call(new EventEmitter(), "error", data);
    }
  }

  // Runs one listener's synchronous prefix for a Host dispatch. The returned Promise is the listener's own continuation; Pi does not await it.
  dispatch({ handlerId, channel, data }) {
    const value = this.realm.decode(data);
    const entry = this.handlers.get(handlerId);
    if (!entry || entry.channel !== channel) throw new Error(`Unknown event bus handler: ${handlerId}`);
    return entry.safeHandler(value);
  }

  release({ handlerId }) {
    const entry = this.handlers.get(handlerId);
    if (entry && !entry.subscribed) this.handlers.delete(handlerId);
  }

  // Hands the in-heap listeners to the Host registry, in EventEmitter order, when a second realm joins.
  migrate() {
    const listeners = [];
    for (const channel of this.emitter.eventNames()) {
      if (typeof channel !== "string") continue;
      for (const safeHandler of this.emitter.rawListeners(channel)) {
        const entry = this.bySafe.get(safeHandler);
        if (!entry) continue;
        entry.registered = true;
        listeners.push({ channel, handlerId: entry.id, runtime: entry.runtime });
      }
    }
    this.routed = true;
    this.emitter = new EventEmitter();
    return listeners;
  }

  // A closed connection's listeners leave the Host registry with it.
  disposeRuntime(runtime) {
    for (const [id, entry] of this.handlers) {
      if (entry.runtime === runtime && entry.registered) {
        entry.subscribed = false;
        this.handlers.delete(id);
      }
    }
  }

  stats() {
    return { handlers: this.handlers.size, routed: this.routed, local: this.emitter.eventNames().reduce((sum, name) => sum + this.emitter.listenerCount(name), 0) };
  }
}
