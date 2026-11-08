// Drive the installed pi-ai classifier image checks, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const dist = root + "node_modules/@earendil-works/pi-ai/dist/";
const { assertClassifierInputSupported } = await import(pathToFileURL(dist + "utils/model-operations.js"));
const typesafe = await import(pathToFileURL(dist + "api/typesafe-system-one.js"));
const cloudflare = await import(pathToFileURL(dist + "api/cloudflare-workers-ai-system-one.js"));
const llama = await import(pathToFileURL(dist + "api/llama-cpp-classify.js"));
const classifiers = { "typesafe-system-one": typesafe.classify, "cloudflare-workers-ai-system-one": cloudflare.classify, "llama-cpp-classify": llama.classify };
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const probes = JSON.parse(input);
const questions = { q: { type: "bool", instructions: "Is it fine?", criteria: { true: "yes", false: "no" } } };
const results = [];
for (const probe of probes) {
  const model = { id: "m", name: "M", api: probe.api, provider: "p", baseUrl: "http://127.0.0.1:1/", input: probe.input, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000 };
  const context = { state: { a: 1 }, questions };
  if (probe.images > 0) context.images = Array.from({ length: probe.images }, () => ({ type: "image", data: "AAAA", mimeType: "image/png" }));
  if (probe.kind === "assert") {
    try {
      assertClassifierInputSupported(model, context);
      results.push({ error: null });
    } catch (error) {
      results.push({ error: error.message });
    }
  } else {
    const result = await classifiers[probe.api](model, context, { apiKey: "k" });
    results.push({ stopReason: result.stopReason, errorMessage: result.errorMessage ?? null });
  }
}
process.stdout.write(JSON.stringify(results));
