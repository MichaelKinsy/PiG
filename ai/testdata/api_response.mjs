// Drive an installed pi-ai API module's stream over scripted SSE responses (through options.fetch) and report the events and the final message.
// argv: version, module file under dist/api, api name.
import { subscribe } from "node:diagnostics_channel";
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { STATUS_CODES, createServer, get } from "node:http";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const dist = root + "node_modules/@earendil-works/pi-ai/dist/";
const api = await import(pathToFileURL(dist + "api/" + process.argv[3]));
const { normalizeContext } = await import(pathToFileURL(dist + "utils/transcript.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
// Some adapters (Google's SDK) reject a custom fetch, so their requests go to a local server instead.
const useServer = process.argv[3].startsWith("google");
// A loopback port belongs to the host: a process polling a port that another server held before reaches whichever server holds it now (on the Linux lab host a
// Go client outside the tests sends GET /readyz to 127.0.0.1 ports), and taken for an attempt it would get a scripted response. The server answers
// only the requests fetch (undici) announces, with its socket, before writing them: pending counts them per connection ("client address>server port").
const pending = new Map();
if (useServer) {
  subscribe("undici:client:sendHeaders", ({ socket }) => {
    const key = `${socket.localAddress}:${socket.localPort}>${socket.remotePort}`;
    pending.set(key, (pending.get(key) ?? 0) + 1);
  });
}
// A held-open response (probe.abortAfterMs) is aborted once the client has read everything the server wrote and is waiting for more, not after a wall-clock
// delay: on a loaded host the abort timer can expire before the event loop has polled the socket, and the abort then lands before the client has read the prefix
// (undici's headers and body chunks arrive in the poll phase; Pi parses a chunk in microtasks, so an abort queued with setImmediate runs after the parse).
// held is the probe whose response is being read: remaining counts the body bytes not yet received, abort aborts its request.
let held = null;
function bodyRead() {
  if (held && held.remaining <= 0 && !held.aborting) {
    held.aborting = true;
    setImmediate(held.abort);
  }
}
if (useServer) {
  subscribe("undici:request:headers", bodyRead);
  subscribe("undici:request:bodyChunkReceived", ({ chunk }) => {
    if (held) held.remaining -= chunk.length;
    bodyRead();
  });
}
for (const probe of JSON.parse(input)) {
  let server = null;
  let request = null;
  let attempts = 0;
  if (useServer) {
    server = createServer((req, res) => {
      req.resume();
      const key = `${req.socket.remoteAddress}:${req.socket.remotePort}>${req.socket.localPort}`;
      if (!pending.get(key)) {
        res.writeHead(421, { "content-type": "text/plain" });
        res.end("not a request of this probe's client");
        return;
      }
      pending.set(key, pending.get(key) - 1);
      request = { path: req.url, method: req.method, headers: Object.fromEntries(Object.entries(req.headers).map(([name, value]) => [name.toLowerCase(), Array.isArray(value) ? value.join(", ") : value])) };
      const step = probe.sequence ? probe.sequence[Math.min(attempts, probe.sequence.length - 1)] : probe;
      attempts++;
      if (probe.abortAfterMs) {
        res.writeHead(200, { "content-type": "text/event-stream" });
        res.write(step.sse ?? "");
        return;
      }
      res.writeHead(step.status || 200, { "content-type": step.contentType || "text/event-stream", ...(step.responseHeaders || {}) });
      res.end(step.sse ?? "");
    });
    await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
    // strayAt "pi": a client other than the API module's requests this server before the probe runs, as a process polling a port that another server
    // held before does (on the Linux lab host a Go client outside the tests sends GET /readyz to 127.0.0.1 ports).
    if (probe.strayAt === "pi") {
      await new Promise((resolve, reject) => get({ host: "127.0.0.1", port: server.address().port, path: "/readyz", agent: false }, (res) => res.resume().on("end", resolve)).on("error", reject));
    }
  }
  const model = { id: probe.modelId || "m", name: "m", api: process.argv[4], provider: probe.provider, baseUrl: server ? `http://127.0.0.1:${server.address().port}` : "http://127.0.0.1:9", reasoning: probe.reasoning, input: ["text"], cost: { input: 1, output: 2, cacheRead: 0.5, cacheWrite: 3 }, contextWindow: 128000, maxTokens: 16384 };
  const fetchFn = async (input, init) => {
    const url = new URL(typeof input === "string" ? input : input.url ?? String(input));
    const headers = {};
    for (const [key, value] of new Headers(init?.headers ?? {})) headers[key.toLowerCase()] = value;
    request = { path: url.pathname + url.search, method: init?.method ?? "GET", headers };
    const step = probe.sequence ? probe.sequence[Math.min(attempts, probe.sequence.length - 1)] : probe;
    attempts++;
    if (probe.abortAfterMs) {
      // The body is delivered and then stays open, as a live connection would, until the request is aborted (a fetch rejects the read then).
      const body = new ReadableStream({
        start(controller) {
          controller.enqueue(new TextEncoder().encode(step.sse ?? ""));
          init?.signal?.addEventListener("abort", () => controller.error(init.signal.reason ?? new DOMException("This operation was aborted", "AbortError")), { once: true });
        },
      });
      return new Response(body, { status: 200, headers: { "content-type": "text/event-stream" } });
    }
    return new Response(step.sse ?? "", { status: step.status || 200, statusText: STATUS_CODES[step.status || 200], headers: { "content-type": step.contentType || "text/event-stream", ...(step.responseHeaders || {}) } });
  };
  const context = normalizeContext({ messages: [{ role: "user", content: "hi", timestamp: 1 }] });
  const events = [];
  let message;
  const abortController = new AbortController();
  if (probe.abortAfterMs) {
    // The Google adapters reject a custom fetch, so their held-open body comes from the server above and is aborted when it has been read (held).
    if (server) held = { remaining: Buffer.byteLength(probe.sse ?? ""), aborting: false, abort: () => abortController.abort() };
    else setTimeout(() => abortController.abort(), probe.abortAfterMs);
  }
  try {
    const stream = api.stream(model, context, { apiKey: probe.apiKey || "k", ...(server ? {} : { fetch: fetchFn }), ...(probe.abortAfterMs ? { signal: abortController.signal } : {}), ...(probe.options || {}), ...(probe.sessionId ? { sessionId: probe.sessionId } : {}), ...(probe.headers ? { headers: probe.headers } : {}) });
    for await (const event of stream) {
      const { partial, message: _m, error: _e, ...rest } = event;
      events.push(rest);
    }
    message = await stream.result();
  } catch (error) { message = { thrown: String(error) }; }
  const { timestamp, durationMs, ...finalMessage } = message;
  held = null;
  if (server) { server.closeAllConnections(); await new Promise((resolve) => server.close(resolve)); }
  results.push({ events, message: finalMessage, request, attempts });
}
process.stdout.write(JSON.stringify(results));
