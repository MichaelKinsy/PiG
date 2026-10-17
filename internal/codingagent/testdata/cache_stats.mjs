// Drive the installed Pi's collectCacheMisses over generated session entries, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { collectCacheMisses } = await import(pathToFileURL(root + "dist/core/cache-stats.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const { prices, sessions } = JSON.parse(input);
const models = { getModel: (provider, model) => (prices[`${provider}/${model}`] === undefined ? undefined : { cost: { cacheRead: prices[`${provider}/${model}`] } }) };
process.stdout.write(JSON.stringify(sessions.map((entries) => {
	const misses = collectCacheMisses(entries, models);
	return entries.map((entry) => {
		const miss = entry.type === "message" ? misses.get(entry.message) : undefined;
		return miss ? { missedTokens: miss.missedTokens, missedCost: miss.missedCost, idleMs: miss.idleMs, modelChanged: miss.modelChanged } : null;
	});
})));
