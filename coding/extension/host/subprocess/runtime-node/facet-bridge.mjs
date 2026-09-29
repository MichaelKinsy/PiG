// Private selected-facet adapter over the existing Node extension transport.
import { getRuntime } from "./state.mjs";

const selected = new WeakMap();
const synchronousRequests = new WeakMap();

export async function selectFacetBridge(moduleURL) {
  const runtime = getRuntime();
  if (selected.has(runtime)) throw new Error("Facet bridge is already selected");
  const module = await import(moduleURL);
  if (typeof module.createFacetBridge !== "function") throw new Error("Facet bridge module has no factory");
  const driver = module.createFacetBridge({
    call: args => runtime.call("facet.host", args),
    callSync: args => callFacetHostSync(runtime, args),
  });
  if (!driver || typeof driver.request !== "function" || typeof driver.sync !== "function") {
    throw new Error("Facet bridge module has an invalid driver");
  }
  selected.set(runtime, driver);
}

export function hasFacetBridge(runtime) {
  return selected.has(runtime);
}

function driverFor(runtime) {
  const driver = selected.get(runtime);
  if (!driver) throw new Error("Facet bridge is not selected for this connection");
  return driver;
}

export function dispatchFacet(runtime, request, context) {
  return driverFor(runtime).request(request.args, context.signal);
}

function callFacetHostSync(runtime, args) {
  const requestId = synchronousRequests.get(runtime)?.at(-1);
  if (!requestId) return runtime.callSync("facet.host.sync", args);
  const connection = runtime.conn;
  if (!connection || connection.closed) throw new Error("Facet connection is closed");
  connection.requestState(requestId, "blocked", "host_call");
  try {
    return connection.callSync("facet.host.sync", args, requestId);
  } finally {
    if (!connection.closed) connection.requestState(requestId, "progress");
  }
}

export function dispatchFacetSync(runtime, request) {
  const driver = driverFor(runtime);
  const { requestId, args } = request.args ?? {};
  if (typeof requestId !== "string" || !requestId) throw new Error("Facet synchronous callback has no request identity");
  const stack = synchronousRequests.get(runtime) ?? [];
  synchronousRequests.set(runtime, stack);
  stack.push(requestId);
  try {
    const result = driver.sync(args);
    if (result && typeof result.then === "function") {
      // Like FacetKernel.#setupFacet, reject the synchronous contract violation on this operation and observe its eventual rejection.
      void Promise.resolve(result).catch(() => {});
      throw new Error("Facet synchronous callback must not return a Promise");
    }
    return result;
  } finally {
    stack.pop();
    if (stack.length === 0) synchronousRequests.delete(runtime);
  }
}
