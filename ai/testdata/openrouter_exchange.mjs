// Run Pi's OpenRouter key exchange (packages/ai/src/auth/oauth/openrouter.ts exchangeAuthorizationCode and errorDetail) over raw HTTP responses from stdin: [{status, body}] -> [{ok} | {err}].
// The private functions are cut out of the source (each starts at "function <name>" or "async function <name>" and ends at the first "}" in column 0), stripped of types and evaluated with the file's constants; fetch answers every request with the probe's raw status and body.
// usage: node openrouter_exchange.mjs <path to openrouter.ts>
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
const source = readFileSync(process.argv[2], "utf8");
const cut = (name) => {
	const match = new RegExp(`^(?:async )?function ${name}[\\s\\S]*?\\n}\\n`, "m").exec(source);
	if (!match) throw new Error(`${name} not found`);
	return match[0];
};
const constants = source.match(/^const [A-Z_]+ = .*;$/gm).join("\n");
const body = stripTypeScriptTypes(`${constants}\ntype JsonObject = Record<string, unknown>;\n${["errorDetail", "exchangeAuthorizationCode"].map(cut).join("\n")}`);
const { exchangeAuthorizationCode } = new Function(`${body}; return { exchangeAuthorizationCode };`)();
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const probe of JSON.parse(input)) {
	globalThis.fetch = async () => new Response(Buffer.from(probe.raw ?? "", "base64"), { status: probe.status });
	try {
		results.push({ ok: await exchangeAuthorizationCode("code", "verifier", new AbortController().signal) });
	} catch (error) {
		results.push({ err: error?.message ?? String(error) });
	}
}
process.stdout.write(JSON.stringify(results));
