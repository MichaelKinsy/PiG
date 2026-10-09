// Run Pi's private normalizeDomain, getBaseUrlFromToken and getGitHubCopilotBaseUrl from auth/oauth/github-copilot.ts over probes from stdin:
// [{input, token, enterprise}] -> [{domain, baseFromToken, baseUrl}] with an absent value as "". The functions are not exported, so each is cut out of
// the file (it starts at "function <name>" and ends at the first "}" in column 0), stripped of types, and evaluated together.
// usage: node github_copilot_helpers.mjs <path to github-copilot.ts>
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
const source = readFileSync(process.argv[2], "utf8");
const cut = (name) => {
	const match = new RegExp(`^function ${name}[\\s\\S]*?\\n}\\n`, "m").exec(source);
	if (!match) throw new Error(`${name} not found`);
	return stripTypeScriptTypes(match[0]);
};
const names = ["normalizeDomain", "getBaseUrlFromToken", "getGitHubCopilotBaseUrl"];
const fns = new Function(`${names.map(cut).join("\n")}; return { ${names.join(", ")} };`)();
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((probe) => ({
			domain: fns.normalizeDomain(probe.input) || "",
			baseFromToken: fns.getBaseUrlFromToken(probe.token) || "",
			baseUrl: fns.getGitHubCopilotBaseUrl(probe.token || undefined, probe.enterprise || undefined),
		})),
	),
);
