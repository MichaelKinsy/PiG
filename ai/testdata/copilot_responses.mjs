// Run Pi's GitHub Copilot OAuth response handling (packages/ai/src/auth/oauth/github-copilot.ts) over raw HTTP responses from stdin: [{flow, status, statusText, raw}] -> [{ok} | {err, syntax}].
// The private functions are cut out of the source (each starts at "function <name>" or "async function <name>" and ends at the first "}" in column 0), stripped of types and evaluated with the file's constants (lines 10-19); fetch answers every request with the probe's raw bytes, status and status text, and pollOAuthDeviceCodeFlow runs poll once.
// flow: "ids" (the ids of the GITHUB_COPILOT_MODELS catalog), "catalog" (fetchGitHubCopilotModels with probe.token, no retries; the answer is {availableModelIds, policyModelIds}), "device" (startDeviceFlow), "poll" (pollForGitHubAccessToken's poll step), "token" (refreshGitHubCopilotAccessToken).
// `syntax` is true when response.json() threw a SyntaxError: the V8 text of that error is not compared.
// usage: node copilot_responses.mjs <path to github-copilot.ts>
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
import { pathToFileURL } from "node:url";
const source = readFileSync(process.argv[2], "utf8");
const cut = (name) => {
	const match = new RegExp(`^(?:async )?function ${name}[\\s\\S]*?\\n}\\n`, "m").exec(source);
	if (!match) throw new Error(`${name} not found`);
	return match[0];
};
const constants = source.split("\n").slice(9, 19).join("\n");
// Resolve as a URL, not by string replacement, so Windows backslash paths work too: src/auth/oauth/github-copilot.ts -> src/providers/github-copilot.models.ts.
const { GITHUB_COPILOT_MODELS } = await import(new URL("../../providers/github-copilot.models.ts", pathToFileURL(process.argv[2])).href);
const names = ["getUrls", "fetchJson", "startDeviceFlow", "pollForGitHubAccessToken", "refreshGitHubCopilotAccessToken", "getBaseUrlFromToken", "getGitHubCopilotBaseUrl", "asRecord", "parseGitHubCopilotModelCatalog", "fetchWithRateLimitRetry", "fetchGitHubCopilotModels"];
const body = stripTypeScriptTypes(`${constants}\n${names.map(cut).join("\n")}`);
const fns = new Function("pollOAuthDeviceCodeFlow", "GITHUB_COPILOT_MODELS", "sleep", `${body}; return { startDeviceFlow, pollForGitHubAccessToken, refreshGitHubCopilotAccessToken, fetchGitHubCopilotModels };`)((options) => options.poll(), GITHUB_COPILOT_MODELS, async () => {});
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const probe of JSON.parse(input)) {
	globalThis.fetch = async () => new Response(Buffer.from(probe.raw ?? "", "base64"), { status: probe.status, statusText: probe.statusText });
	const signal = new AbortController().signal;
	try {
		let value;
		if (probe.flow === "ids") value = Object.keys(GITHUB_COPILOT_MODELS);
		else if (probe.flow === "catalog") value = await fns.fetchGitHubCopilotModels(probe.token ?? "", undefined, signal, { maxRetries: 0, maxElapsedMs: 0 });
		else if (probe.flow === "device") value = await fns.startDeviceFlow("github.com", signal);
		else if (probe.flow === "poll") value = await fns.pollForGitHubAccessToken("github.com", { device_code: "dc", interval: 5, expires_in: 900 }, signal);
		else value = await fns.refreshGitHubCopilotAccessToken("gh-token", undefined, signal);
		results.push({ ok: value === undefined ? null : value });
	} catch (error) {
		results.push({ err: error?.message ?? String(error), syntax: error instanceof SyntaxError });
	}
}
process.stdout.write(JSON.stringify(results));
