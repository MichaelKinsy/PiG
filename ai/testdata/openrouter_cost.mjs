// Drive the installed pi-ai openrouter-cost.js (isOpenRouterModel, applyOpenRouterReportedCost) and openrouter-api.js (openRouterCost) over JSON cases:
// [{kind: "model", provider, baseUrl} | {kind: "reported", reported: <JSON text>} | {kind: "cost", pricing: <JSON text>}].
// Numbers that JSON cannot carry (NaN, Infinity) are printed as their String() text.
import { readFileSync, realpathSync } from "node:fs";
import { fileURLToPath, pathToFileURL } from "node:url";
const root = realpathSync(fileURLToPath(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url))) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const dist = pathToFileURL(root + "node_modules/@earendil-works/pi-ai/dist/").href;
const { applyOpenRouterReportedCost, isOpenRouterModel } = await import(dist + "utils/openrouter-cost.js");
const { openRouterCost } = await import(dist + "providers/openrouter-api.js");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const finite = (key, value) => (typeof value === "number" && !Number.isFinite(value) ? String(value) : value);
const results = JSON.parse(input).map((probe) => {
	if (probe.kind === "model") return isOpenRouterModel({ provider: probe.provider, baseUrl: probe.baseUrl });
	if (probe.kind === "reported") {
		const usage = { input: 1, output: 1, cacheRead: 0, cacheWrite: 0, totalTokens: 2, cost: { input: 0.25, output: 0.75, cacheRead: 0, cacheWrite: 0, total: 1 } };
		applyOpenRouterReportedCost(usage, JSON.parse(probe.reported));
		return { total: usage.cost.total, fromProvider: usage.cost.totalFromProvider ?? null };
	}
	return openRouterCost(JSON.parse(probe.pricing));
});
process.stdout.write(JSON.stringify(results, finite));
