// Drive the installed pi-ai bedrock-converse-stream API over scripted AWS event-stream responses (a local server) and report the stream events and the final message.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { createServer } from "node:http";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const dist = root + "node_modules/@earendil-works/pi-ai/dist/";
const lazy = await import(pathToFileURL(dist + "api/bedrock-converse-stream.lazy.js"));
const { normalizeContext } = await import(pathToFileURL(dist + "utils/transcript.js"));
process.env.AWS_BEDROCK_SKIP_AUTH = "1";
process.env.AWS_REGION = "us-east-1";
process.env.AWS_BEDROCK_FORCE_HTTP1 = "1";
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const probe of JSON.parse(input)) {
  const body = Buffer.from(probe.body, "base64");
  const server = createServer((req, res) => {
    req.resume();
    req.on("end", () => {
      res.writeHead(probe.status || 200, { "content-type": probe.status ? "application/json" : "application/vnd.amazon.eventstream" });
      res.end(probe.status ? probe.errorBody : body);
    });
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const model = { id: probe.modelId, name: "m", api: "bedrock-converse-stream", provider: "amazon-bedrock", baseUrl: `http://127.0.0.1:${server.address().port}`, reasoning: probe.reasoning, input: ["text"], cost: { input: 1, output: 2, cacheRead: 0.5, cacheWrite: 3 }, contextWindow: 200000, maxTokens: 16384 };
  const events = [];
  let message;
  try {
    const stream = lazy.bedrockConverseStreamApi().stream(model, normalizeContext({ messages: [{ role: "user", content: "hi", timestamp: 1 }] }), { cacheRetention: "none" });
    for await (const event of stream) {
      const { partial, message: _m, error: _e, ...rest } = event;
      events.push(rest);
    }
    message = await stream.result();
  } catch (error) { message = { thrown: String(error) }; }
  const { timestamp, durationMs, ...finalMessage } = message;
  server.closeAllConnections();
  await new Promise((resolve) => server.close(resolve));
  results.push({ events, message: finalMessage });
}
process.stdout.write(JSON.stringify(results));
