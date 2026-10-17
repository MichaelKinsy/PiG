// pig divergence (D74): the provider-bridge client. A process that imports pi-ai's builtin APIs but has no extension runtime (a detached helper an extension starts, a forked child or
// a worker thread that inherited the loader) cannot reach the host over the extension connection. It starts `pig provider-bridge`, a pig process this one owns, and speaks JSON
// Lines to it: {id, type: "stream", simple, model, request} and {id, type: "cancel"} out; {id, type: "event" | "result" | "error"} back, in provider order, then exactly one of
// result or error for each id. The bridge's lifetime is this process's: its stdin closes when this process exits and the bridge ends and aborts what is in flight. Nothing of the
// extension connection (socket, token) is shared. onPayload, onResponse and transformHeaders run in this process: the bridge sends {id, type: "callback", callbackId, callback, value} and waits for
// {id, type: "callbackResult", callbackId, result | failure}. fetch has no wire form: a request that has one ends with an error event on the same stream.
import { spawn } from "node:child_process";
import { AssistantMessageEventStream } from "./pi-dist/pi-ai/sdk-bundle/index.js";

const HARNESS_BINARY_ENV = "PIG_HARNESS_BINARY";
let bridge;
let nextId = 1;

function startBridge() {
  const binary = process.env[HARNESS_BINARY_ENV];
  if (!binary) throw new Error(`${HARNESS_BINARY_ENV} is not set: pi-ai cannot reach PiG's provider implementations from a process with no extension runtime`);
  const child = spawn(binary, ["provider-bridge"], { stdio: ["pipe", "pipe", "inherit"], env: process.env });
  const state = { child, streams: new Map(), buffer: "", dead: false };
  const finish = (message) => {
    if (state.dead) return;
    state.dead = true;
    if (bridge === state) bridge = undefined;
    for (const [id, entry] of state.streams) entry.fail(new Error(message));
    state.streams.clear();
  };
  child.on("error", (error) => finish(`provider bridge: ${error.message}`));
  child.on("exit", (code, signal) => finish(`provider bridge exited (${signal ?? code}) before the stream ended`));
  child.stdin.on("error", () => {});
  child.stdout.setEncoding("utf8");
  child.stdout.on("data", (chunk) => {
    state.buffer += chunk;
    for (let newline = state.buffer.indexOf("\n"); newline >= 0; newline = state.buffer.indexOf("\n")) {
      const line = state.buffer.slice(0, newline);
      state.buffer = state.buffer.slice(newline + 1);
      if (line) dispatch(state, JSON.parse(line));
    }
  });
  // The bridge never keeps this process alive on its own: only a stream in flight does.
  child.unref();
  child.stdin.unref?.();
  child.stdout.unref?.();
  return state;
}

function dispatch(state, reply) {
  const entry = state.streams.get(reply.id);
  if (!entry) return;
  if (reply.type === "event") entry.stream.push(reply.event);
  else if (reply.type === "result") entry.settle();
  else if (reply.type === "error") entry.fail(new Error(reply.error));
  else if (reply.type === "callback") runCallback(state, entry, reply);
}

// A callback runs here, in the process that holds it, and its answer returns on the same stream id. A throw answers as a failure that rejects the provider request.
async function runCallback(state, entry, reply) {
  const answer = { id: reply.id, type: "callbackResult", callbackId: reply.callbackId };
  try {
    const fn = entry.callbacks[reply.callback];
    if (typeof fn !== "function") throw new Error(`unknown model stream callback ${reply.callback}`);
    const result = await fn(reply.value, entry.model);
    answer.result = reply.callback === "onPayload" ? { defined: result !== undefined, value: result } : result;
  } catch (error) {
    answer.failure = error instanceof Error ? error.message : String(error);
  }
  if (!state.dead) state.child.stdin.write(`${JSON.stringify(answer)}\n`);
}

function retain(state) {
  state.child.ref();
  state.child.stdin.ref?.();
  state.child.stdout.ref?.();
}

function release(state) {
  if (state.streams.size > 0) return;
  state.child.unref();
  state.child.stdin.unref?.();
  state.child.stdout.unref?.();
}

// bridgeStream is the pi-ai builtin API's stream/streamSimple for a process with no extension runtime. failedStream builds the error stream the shim ends a request with that never started.
export function bridgeStream(model, context, options, simple, failedStream) {
  const { signal, onPayload, onResponse, transformHeaders, fetch, ...requestOptions } = options ?? {};
  // fetch replaces the provider's HTTP client with a function of this process; it has no wire form.
  if (typeof fetch === "function") return failedStream(model, "pi-ai: fetch cannot be used outside an extension runtime (PiG provider bridge)", options);
  const callbacks = { onPayload, onResponse, transformHeaders };
  let state;
  try {
    state = bridge && !bridge.dead ? bridge : (bridge = startBridge());
  } catch (error) {
    return failedStream(model, error.message, options);
  }
  const id = `bridge-${process.pid}-${nextId++}`;
  const stream = new AssistantMessageEventStream();
  let ended = false;
  const entry = {
    stream,
    callbacks,
    model,
    // A stream that reached its provider terminal event is complete; one that did not ends as an error on this same stream.
    settle: () => end(),
    fail: (error) => {
      if (!ended) {
        const reason = signal?.aborted ? "aborted" : "error";
        stream.push({ type: "error", reason, error: { role: "assistant", content: [], api: model?.api ?? "", provider: model?.provider, model: model?.id, usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } }, stopReason: reason, errorMessage: error.message, timestamp: Date.now() } });
      }
      end();
    },
  };
  const end = () => {
    if (ended) return;
    ended = true;
    state.streams.delete(id);
    signal?.removeEventListener?.("abort", onAbort);
    stream.end();
    release(state);
  };
  const onAbort = () => state.child.stdin.write(`${JSON.stringify({ id, type: "cancel" })}\n`);
  state.streams.set(id, entry);
  retain(state);
  signal?.addEventListener?.("abort", onAbort, { once: true });
  state.child.stdin.write(`${JSON.stringify({ id, type: "stream", simple, model, onPayload: typeof onPayload === "function", onResponse: typeof onResponse === "function", transformHeaders: typeof transformHeaders === "function", request: { ...context, ...requestOptions } })}\n`);
  return stream;
}
