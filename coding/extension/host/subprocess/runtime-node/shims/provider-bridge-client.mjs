// pig divergence (D74): the provider-bridge client. A process that imports pi-ai's builtin APIs but has no extension runtime (a detached helper an extension starts, a forked child or
// a worker thread that loads this module from the harness package; the runtime's loader hooks are not passed on to it) cannot reach the host over the extension connection. It starts `pig provider-bridge`, a pig process this one owns, and speaks JSON
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
    if (bridge === state) {
      bridge = undefined;
      codexStats.clear();
    }
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

// The Codex WebSocket helpers of pi-ai's api/openai-codex-responses act on the Go cache of THIS process's bridge child. close and reset are one-way lines to it (never starting one: with no child
// there is no cache to act on, as Pi's helpers on an empty map). Pi's getter is synchronous, so it reads codexStats, a replica the child pushes after every change, in the order of the stream's
// lines and before the stream's result line: exact once a stream's result has been read, and during a stream as of the last line read. The replica ends with the child whose cache it mirrors.
const codexStats = new Map();
let nextCodexLine = 1;

// replicaOf rebuilds Pi's stats object from the pushed one. Pi assigns members in the order the changes happen: the ten counters first, then, as each change first occurs, lastDeltaInputItems and
// lastPreviousResponseId (the request path assigns them `undefined` on a full-context request, so they are members), lastWebSocketError, websocketFallbackActive. A member once assigned stays.
function replicaOf(previous, pushed) {
  const stats = previous ?? {};
  for (const name of ["requests", "connectionsCreated", "connectionsReused", "cachedContextRequests", "storeTrueRequests", "fullContextRequests", "deltaRequests", "lastInputItems", "websocketFailures", "sseFallbacks"]) stats[name] = pushed[name];
  if (pushed.requests > 0) {
    stats.lastDeltaInputItems = pushed.lastDeltaInputItems;
    stats.lastPreviousResponseId = pushed.lastPreviousResponseId;
  }
  if (pushed.lastWebSocketError !== undefined) stats.lastWebSocketError = pushed.lastWebSocketError;
  if (pushed.websocketFallbackActive !== undefined) stats.websocketFallbackActive = pushed.websocketFallbackActive;
  return stats;
}

function applyCodexStats(reply) {
  if (reply.sessionId === undefined) codexStats.clear();
  else if (reply.stats === undefined) codexStats.delete(reply.sessionId);
  else codexStats.set(reply.sessionId, replicaOf(codexStats.get(reply.sessionId), reply.stats));
}

function dispatch(state, reply) {
  if (reply.type === "codexStats") return applyCodexStats(reply);
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

// writeCodexLine sends one codexWebSocket line to the live bridge child; with none it does nothing and starts nothing.
function writeCodexLine(op, sessionId) {
  if (!bridge || bridge.dead) return;
  bridge.child.stdin.write(`${JSON.stringify({ id: `codex-ws-${nextCodexLine++}`, type: "codexWebSocket", op, sessionId: sessionId || undefined })}\n`);
}

// closeCodexWebSocketSessions is closeOpenAICodexWebSocketSessions: the cached connections of sessionId, or of every session when it is absent or empty.
export function closeCodexWebSocketSessions(sessionId) {
  writeCodexLine("close", sessionId);
}

// resetCodexWebSocketDebugStats is resetOpenAICodexWebSocketDebugStats: the replica is cleared here, before the call returns, as Pi's map is; the child clears its counters and SSE-fallback flags
// when it reads the line and pushes the cleared state.
export function resetCodexWebSocketDebugStats(sessionId) {
  if (sessionId) codexStats.delete(sessionId);
  else codexStats.clear();
  writeCodexLine("reset", sessionId);
}

// getCodexWebSocketDebugStats is getOpenAICodexWebSocketDebugStats: a copy of the session's replica, or undefined.
export function getCodexWebSocketDebugStats(sessionId) {
  const stats = codexStats.get(sessionId);
  return stats ? { ...stats } : undefined;
}
