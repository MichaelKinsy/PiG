// Ports packages/ai/src/types.ts (ProviderRequestOptions.fetch).
// The transport retains the caller's fetch and exposes response readers only to the owning model stream.
export function modelFetchCallbacks(fetch, signal) {
  const responses = new Map();
  const controllers = new Set();
  let disposed = false;
  const close = async ({ id }) => {
    const entry = responses.get(id);
    if (!entry) return;
    responses.delete(id);
    entry.controller.abort();
    try {
      if (!entry.done) await entry.reader.cancel();
    } finally {
      entry.reader.releaseLock();
      controllers.delete(entry.controller);
    }
  };
  return {
    async fetch(value, _model, callbackSignal) {
      if (disposed) throw new Error("model fetch transport is closed");
      const controller = new AbortController();
      controllers.add(controller);
      const abort = () => controller.abort(signal?.reason ?? callbackSignal?.reason);
      signal?.addEventListener("abort", abort, { once: true });
      callbackSignal?.addEventListener("abort", abort, { once: true });
      if (signal?.aborted || callbackSignal?.aborted) abort();
      try {
        const headers = new Headers();
        for (const [key, values] of Object.entries(value.headers)) {
          for (const item of values) headers.append(key, item);
        }
        const response = await fetch(value.url, {
          method: value.method, headers, signal: controller.signal,
          ...(value.body === undefined ? {} : { body: Buffer.from(value.body, "base64") }),
        });
        if (disposed || controller.signal.aborted) {
          await response.body?.cancel();
          throw controller.signal.reason ?? new Error("model fetch transport is closed");
        }
        if (response.body) {
          responses.set(value.id, { reader: response.body.getReader(), pending: new Uint8Array(), done: false, controller });
        } else {
          controllers.delete(controller);
        }
        return { status: response.status, statusText: response.statusText, headers: [...response.headers], body: response.body !== null };
      } catch (error) {
        controllers.delete(controller);
        throw error;
      } finally {
        signal?.removeEventListener("abort", abort);
        callbackSignal?.removeEventListener("abort", abort);
      }
    },
    async fetchRead({ id, size }, _model, callbackSignal) {
      const entry = responses.get(id);
      if (!entry) throw new Error(`unknown model fetch response ${id}`);
      if (!Number.isSafeInteger(size) || size < 1 || size > 64 * 1024) throw new Error("invalid model fetch read size");
      const abort = () => entry.controller.abort(callbackSignal.reason);
      callbackSignal?.addEventListener("abort", abort, { once: true });
      try {
        if (callbackSignal?.aborted) abort();
        while (entry.pending.length === 0 && !entry.done) {
          const chunk = await entry.reader.read();
          entry.done = chunk.done;
          entry.pending = chunk.value ?? new Uint8Array();
        }
        const count = Math.min(size, entry.pending.length);
        const data = Buffer.from(entry.pending.subarray(0, count)).toString("base64");
        entry.pending = entry.pending.subarray(count);
        return { data, done: entry.done && entry.pending.length === 0 };
      } finally {
        callbackSignal?.removeEventListener("abort", abort);
      }
    },
    fetchClose: close,
    async disposeFetch() {
      disposed = true;
      for (const controller of controllers) controller.abort();
      await Promise.all([...responses.keys()].map(id => close({ id })));
    },
  };
}
