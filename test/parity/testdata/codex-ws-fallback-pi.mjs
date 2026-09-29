import { createServer } from "node:http";
import { homedir } from "node:os";
import { pathToFileURL } from "node:url";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

const version = readFileSync("internal/coding/pigversion/pigversion.go", "utf8").match(/UpstreamVersion = "([^"]+)"/)[1];
const install = join(homedir(), ".local/share/mise/installs/npm-earendil-works-pi-coding-agent", version);
const root = process.env.PI_PACKAGE_ROOT ?? [
  "extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent",
  join(install, "node_modules/@earendil-works/pi-coding-agent"),
  join(install, "lib/node_modules/@earendil-works/pi-coding-agent"),
].find((path) => existsSync(join(path, "package.json")));
if (!root) throw new Error("Pinned Pi package not found; set PI_PACKAGE_ROOT");
if (JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version !== version) throw new Error("Pi version mismatch");
const aiRoot = join(root, "node_modules/@earendil-works/pi-ai/dist");
const { stream, getOpenAICodexWebSocketDebugStats, closeOpenAICodexWebSocketSessions } = await import(pathToFileURL(join(aiRoot, "api/openai-codex-responses.js")));
const { normalizeContext } = await import(pathToFileURL(join(aiRoot, "utils/transcript.js")));

let sse = 0;
const sockets = new Set();
const server = createServer((request, response) => {
  if (request.method !== "POST") throw new Error(`Unexpected method ${request.method}`);
  request.resume();
  sse++;
  response.writeHead(200, { "content-type": "text/event-stream" });
  response.end('data: {"type":"response.done","response":{"id":"resp_sse","status":"completed"}}\n\n');
});
// Never finish the upgrade. The provider owns the connect deadline; no server handler count is assumed because that deadline can expire before dispatch.
server.on("upgrade", (_request, socket) => {
  sockets.add(socket);
  socket.on("close", () => sockets.delete(socket));
});
await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
const model = {
  id: "gpt-5.2", name: "probe", api: "openai-codex-responses", provider: "openai-codex",
  baseUrl: `http://127.0.0.1:${server.address().port}`, reasoning: true, input: ["text"],
  cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 4096,
};
const token = `e30.${Buffer.from(JSON.stringify({ "https://api.openai.com/auth": { chatgpt_account_id: "acct_fallback" } })).toString("base64url")}.sig`;
try {
  for (let turn = 1; turn <= 2; turn++) {
    const events = [];
    const response = stream(model, normalizeContext({ messages: [{ role: "user", content: "hello", timestamp: 1 }] }), {
      apiKey: token, transport: "auto", sessionId: "fallback-probe", websocketConnectTimeoutMs: 10,
    });
    for await (const event of response) events.push(event.type);
    const result = await response.result();
    const stats = getOpenAICodexWebSocketDebugStats("fallback-probe");
    console.log(`turn=${turn} events=${events.join(",")} stop=${result.stopReason} response=${result.responseId} failures=${stats?.websocketFailures} fallbacks=${stats?.sseFallbacks} active=${stats?.websocketFallbackActive} sse=${sse} timeout=${stats?.lastWebSocketError?.includes("timeout")}`);
  }
} finally {
  closeOpenAICodexWebSocketSessions();
  for (const socket of sockets) socket.destroy();
  server.closeAllConnections();
  await new Promise((resolve) => server.close(resolve));
}
