// Drive the installed pi-ai typesafe-system-one classifier against canned HTTP responses, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { classify } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-ai/dist/api/typesafe-system-one.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const probes = JSON.parse(input);
const numericQuestions = {
  b: { type: "bool", instructions: "B? \u2028 <&>", criteria: { true: "t", false: "f" } },
  10: { type: "choice", instructions: "Ten", criteria: { z: "Z", 2: "two", 10: "ten" } },
  2: { type: "score", instructions: "Two", criteria: [] },
  a: { type: "score", instructions: "A", criteria: ["só", "high \ud83d\ude00"] },
};
const questions = {
  pick: { type: "choice", instructions: "Pick", criteria: { a: "A", b: "B" } },
  rate: { type: "score", instructions: "Rate", criteria: ["low", "high"] },
  fine: { type: "bool", instructions: "Fine?", criteria: { true: "yes", false: "no" } },
};
const model = { id: "m", name: "M", api: "typesafe-system-one", provider: "p", baseUrl: "http://127.0.0.1:1/", input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000 };
const results = [];
for (const probe of probes) {
  let sent = null;
  const fetch = async (_url, init) => { sent = init.body; return new Response(probe.body, { status: probe.status, headers: { "content-type": "application/json" } }); };
  const result = await classify(model, { state: { a: 1, nested: [1, { b: null }] }, questions: probe.numeric ? numericQuestions : questions }, { apiKey: "k", fetch });
  results.push({ request: sent, stopReason: result.stopReason, errorMessage: result.errorMessage ?? null, answers: result.answers, answersJson: JSON.stringify(result.answers), usage: result.usage ? { input: result.usage.input, output: result.usage.output, totalTokens: result.usage.totalTokens } : null });
}
process.stdout.write(JSON.stringify(results));
