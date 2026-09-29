import { randomUUID } from "node:crypto";

const streamMethods = new Set(["stream", "streamSimple", "fetchDeferred"]);

function providerFor(runtime, request) {
  const provider = runtime.nativeProviderCallbacks.get(request.tool);
  if (!provider) throw new Error("Provider object owner is no longer registered");
  return provider;
}

export function dispatchProviderObjectSync(runtime, request) {
  if (request.method === "provider_object_callback_sync") return dispatchProviderObjectCallback(runtime, request);
  const { method, params = {} } = request.args ?? {};
  if (method === "update") {
    const update = runtime.providerUpdates.get(params.token);
    if (!update) throw new Error("Provider publication is no longer active");
    update();
    return null;
  }
  const provider = providerFor(runtime, request);
  if (method === "getModels") return provider.getModels();
  if (method === "filterModels") {
    const models = params.models;
    const filtered = provider.filterModels(models, params.credential ?? undefined);
    return { models: filtered, indices: filtered.map(model => models.indexOf(model)) };
  }
  throw new Error(`Invalid synchronous Provider method: ${method}`);
}

export function dispatchProviderObjectCallback(runtime, request) {
  const record = runtime.providerObjectCallbacks.get(request.tool);
  if (!record) throw new Error("Provider callback is no longer active");
  const { method, params = {} } = request.args ?? {};
  const fn = record.callbacks[method];
  if (!fn) throw new Error(`Provider callback ${method} is absent`);
  return fn(params);
}

// pig additive (D19): teardown releases transport waits without resolving or ending the caller-owned stream.
function transportValue(value, signal) {
  return new Promise((resolve, reject) => {
    let target = {resolve, reject};
    const settle = (kind, value) => {
      if (!target) return;
      const pending = target;
      target = undefined;
      signal.removeEventListener("abort", abort);
      pending[kind](value);
    };
    const abort = () => settle("reject", signal.reason);
    signal.addEventListener("abort", abort, {once:true});
    Promise.resolve(value).then(value => settle("resolve", value), error => settle("reject", error));
    if (signal.aborted) abort();
  });
}

export async function dispatchProviderObject(runtime, id, request, ctx) {
  const provider = providerFor(runtime, request);
  const { method, params = {} } = request.args;
  const signal = ctx.signal;
  const invoke = (method, params) => runtime.call("provider.callback", { provider: request.tool, method, params });
  const interaction = {
    signal,
    prompt: prompt => invoke("prompt", { prompt }),
    notify: event => runtime.callSync("provider.callback", { provider: request.tool, method: "notify", params: { event } }),
  };
  const authInput = {
    signal,
    credential: params.credential ?? undefined,
    ctx: {
      env: async name => (await invoke("env", { name })) ?? undefined,
      fileExists: path => invoke("fileExists", { path }),
    },
  };
  switch (method) {
    case "auth.apiKey.check": return provider.auth.apiKey.check(authInput);
    case "auth.apiKey.resolve": return provider.auth.apiKey.resolve(authInput);
    case "auth.apiKey.login": return provider.auth.apiKey.login(interaction);
    case "auth.oauth.login": return provider.auth.oauth.login(interaction);
    case "auth.oauth.refresh": return provider.auth.oauth.refresh(params.credential, signal);
    case "auth.oauth.toAuth": return provider.auth.oauth.toAuth(params.credential);
    case "refreshModels":
      return provider.refreshModels({ ...params, signal, publish: async publication => {
        const { update, ...value } = publication;
        const token = randomUUID();
        if (update) runtime.providerUpdates.set(token, update);
        try { return await invoke("publish", { publication: value, token: update ? token : undefined }); }
        finally { runtime.providerUpdates.delete(token); }
      } });
    case "cancelDeferred": return provider.cancelDeferred(params.model, params.handle, { ...params.options, signal });
    default: {
      if (!streamMethods.has(method)) throw new Error(`Unknown Provider method ${method}`);
      const controller = new AbortController();
      const abort = () => controller.abort(signal.reason);
      signal.addEventListener("abort", abort, { once: true });
      if (signal.aborted || params.aborted) controller.abort();
      const options = { ...params.options, signal: controller.signal };
      for (const name of params.callbacks ?? []) {
        options[name] = (value, model) => invoke(name, { value, model });
      }
      try {
        const stream = provider[method](params.model, method === "fetchDeferred" ? params.handle : params.context, options);
        runtime.notify("tool_update", { request_id: id, result: { type: "provider_started" } });
        const iterator = stream[Symbol.asyncIterator]();
        while (true) {
          const next = await transportValue(iterator.next(), runtime.providerTransport.signal);
          if (next.done) break;
          try {
            runtime.notify("tool_update", { request_id: id, result: next.value });
          } catch (error) {
            // Preserve for-await's iterator cleanup and original delivery error without letting cleanup block runtime shutdown.
            try { await transportValue(iterator.return?.(), runtime.providerTransport.signal); }
            finally { throw error; }
          }
        }
        return null;
      } finally { signal.removeEventListener("abort", abort); }
    }
  }
}

export function remoteProvider(runtime, declaration, EventStream) {
  const cached = runtime.remoteProviderObjects.get(declaration.id);
  if (cached?.handle === declaration.handle) return cached.provider;
  const lease = { handle: declaration.handle, token: randomUUID() };
  runtime.callSync("provider.retain", lease);
  const sync = (method, params) => runtime.callSync("provider.object", { handle: lease.handle, method, params });
  const invoke = (method, params = {}, callbacks = {}, signal) => {
    const callbackId = randomUUID();
    runtime.providerObjectCallbacks.set(callbackId, {callbacks, lease});
    const onAbort = () => { void runtime.call("cancelModelStream", { streamId: callbackId }).catch(() => {}); };
    signal?.addEventListener("abort", onAbort, { once: true });
    const operation = runtime.call("provider.object", { handle: lease.handle, method, params, callbackId });
    if (signal?.aborted) onAbort();
    return operation.then(value => value ?? undefined).finally(() => {
      signal?.removeEventListener("abort", onAbort);
      runtime.providerObjectCallbacks.delete(callbackId);
    });
  };
  const authInput = input => ({ env: ({ name }) => input.ctx.env(name), fileExists: ({ path }) => input.ctx.fileExists(path) });
  const interaction = input => ({ prompt: ({ prompt }) => input.prompt({ ...prompt, signal: input.signal }), notify: ({ event }) => input.notify(event) });
  const provider = { id: declaration.id, name: declaration.name, baseUrl: declaration.baseUrl, headers: declaration.headers, auth: {} };
  if (declaration.auth?.apiKey) provider.auth.apiKey = { ...declaration.auth.apiKey };
  if (declaration.auth?.oauth) provider.auth.oauth = { ...declaration.auth.oauth };
  const models = new Map();
  for (const method of declaration.methods ?? []) {
    if (method === "getModels") provider.getModels = () => sync(method, {}).map(model => {
      let value = models.get(model.id);
      if (!value) models.set(model.id, value = model);
      else { for (const key of Object.keys(value)) delete value[key]; Object.assign(value, model); }
      return value;
    });
    else if (method === "filterModels") provider.filterModels = (models, credential) => {
      const result = sync(method, { models, credential });
      return result.models.map((model, index) => {
        const input = models[result.indices[index]];
        if (!input) return model;
        Object.assign(input, model);
        return input;
      });
    };
    else if (method === "auth.apiKey.check" || method === "auth.apiKey.resolve") {
      provider.auth.apiKey[method.split(".").at(-1)] = input => invoke(method, { credential: input.credential }, authInput(input), input.signal);
    } else if (method.endsWith(".login")) {
      provider.auth[method.split(".")[1]].login = input => invoke(method, {}, interaction(input), input.signal);
    } else if (method === "auth.oauth.refresh") provider.auth.oauth.refresh = (credential, signal) => invoke(method, { credential }, {}, signal);
    else if (method === "auth.oauth.toAuth") provider.auth.oauth.toAuth = credential => invoke(method, { credential });
    else if (method === "refreshModels") provider.refreshModels = input => {
      const { publish, signal, ...params } = input;
      return invoke(method, params, { publish: ({ publication, token }) => publish({ ...publication, ...(token ? { update: () => sync("update", { token }) } : {}) }) }, signal);
    };
    else if (method === "cancelDeferred") provider.cancelDeferred = (model, handle, options = {}) => {
      const { signal, ...params } = options;
      return invoke(method, { model, handle, options: params }, {}, signal);
    };
    else if (streamMethods.has(method)) provider[method] = (model, context, options = {}) => {
      const streamId = randomUUID();
      const stream = new EventStream();
      runtime.modelStreams.set(streamId, stream);
      const { signal, ...data } = options;
      const callbacks = {};
      for (const name of ["onPayload", "onResponse", "transformHeaders"]) {
        if (typeof data[name] !== "function") continue;
        const fn = data[name];
        callbacks[name] = ({ value }) => fn(value, model);
        delete data[name];
      }
      runtime.providerObjectCallbacks.set(streamId, {callbacks, lease});
      const onAbort = () => { void runtime.call("cancelModelStream", { streamId }).catch(() => {}); };
      const cleanup = () => {
        signal?.removeEventListener("abort", onAbort);
        runtime.providerObjectCallbacks.delete(streamId);
        runtime.modelStreams.delete(streamId);
      };
      let operation;
      try {
        operation = runtime.conn.callWithStreamStart("provider.object", {
          handle: lease.handle, method, streamId, callbackId: streamId,
          params: { model, ...(method === "fetchDeferred" ? { handle: context } : { context }), options: data, callbacks: Object.keys(callbacks), aborted: signal?.aborted === true },
        }, runtime.activeRequest()?.id ?? "");
      } catch (error) { cleanup(); throw error; }
      signal?.addEventListener("abort", onAbort, { once: true });
      if (signal?.aborted) onAbort();
      const settle = error => {
        if (!stream.terminal && runtime.conn.queue.length > 0) { setImmediate(() => settle(error)); return; }
        if (!stream.terminal) stream.fail(error ?? new Error("Provider stream ended without a terminal event"), model);
        cleanup();
      };
      void operation.then(() => setImmediate(() => settle()), error => setImmediate(() => settle(error)));
      return stream;
    };
  }
  runtime.providerLeases.register(lease, { ...lease });
  runtime.remoteProviderObjects.set(declaration.id, {handle: lease.handle, provider});
  return provider;
}
