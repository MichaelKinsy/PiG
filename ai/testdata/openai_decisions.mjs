// Drive the installed pi-ai openai-decisions classifier against a scripted server, never a translated oracle: each probe names the status and body the server answers with.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { classify } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-ai/dist/api/openai-decisions.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const ORIGIN = "http://decisions.test";
const results = [];
for (const [index, probe] of JSON.parse(input).entries()) {
	const requests = [];
	const fetch = async (url, init) => {
		const parsed = new URL(String(url));
		requests.push({ path: parsed.pathname + parsed.search, authorization: init.headers.authorization ?? null, contentType: init.headers["content-type"] ?? null, body: init.body });
		return new Response(probe.body, { status: probe.status, headers: { "content-type": probe.contentType } });
	};
	const model = { id: `m${index}`, name: "M", api: "openai-decisions", provider: "p", baseUrl: ORIGIN + probe.baseSuffix, input: ["text", "image"], cost: probe.cost, contextWindow: 1000 };
	const questions = {};
	for (const q of probe.questions) {
		if (q.type === "choice") questions[q.id] = { type: "choice", instructions: q.instructions, criteria: Object.fromEntries((q.criteria ?? []).map((c) => [c.key, c.description])) };
		else if (q.type === "score") questions[q.id] = { type: "score", instructions: q.instructions, criteria: q.levels ?? [] };
		else questions[q.id] = { type: "bool", instructions: q.instructions, criteria: { true: q.yes, false: q.no } };
	}
	const context = { state: JSON.parse(probe.state), questions };
	if (probe.images > 0) context.images = Array.from({ length: probe.images }, (_, i) => ({ type: "image", data: `AAAA${i}`, mimeType: i % 2 ? "image/png" : "image/jpeg" }));
	const result = await classify(model, context, { apiKey: probe.apiKey || undefined, fetch });
	results.push({ requests, stopReason: result.stopReason, errorMessage: result.errorMessage ?? null, answersJson: JSON.stringify(result.answers), usage: result.usage ?? null });
}
process.stdout.write(JSON.stringify(results));
