// Drive the installed pi-ai api/*.lazy entry points, never a translated oracle: each case calls stream or streamSimple with a model whose api names another API and prints the final message's api, stop reason and error message.
import http from "node:http";
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const api = root + "node_modules/@earendil-works/pi-ai/dist/api/";
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const cases = JSON.parse(input);
const server = http.createServer((_request, response) => {
  response.statusCode = 500;
  response.end();
});
await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
const baseUrl = `http://127.0.0.1:${server.address().port}`;
delete process.env.GOOGLE_CLOUD_PROJECT;
delete process.env.GCLOUD_PROJECT;
const context = { messages: [{ role: "user", content: "hi", timestamp: 1 }] };
const results = [];
for (const test of cases) {
  const module = await import(pathToFileURL(api + test.module + ".lazy.js"));
  const streams = module[test.factory]();
  const model = { id: "model-x", name: "model-x", api: test.modelApi, provider: "p", baseUrl, input: ["text"], reasoning: false, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 };
  const options = test.apiKey ? { apiKey: test.apiKey } : {};
  if (test.aborted) options.signal = AbortSignal.abort();
  const message = await (test.simple ? streams.streamSimple : streams.stream)(model, context, options).result();
  results.push({ api: message.api, stopReason: message.stopReason, errorMessage: message.errorMessage });
}
server.close();
process.stdout.write(JSON.stringify(results));
