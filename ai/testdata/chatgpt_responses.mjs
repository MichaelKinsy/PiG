// Run Pi's Sign in with ChatGPT token handling (packages/ai/src/auth/oauth/openai-chatgpt.ts) over raw HTTP responses from stdin: [{flow, status, statusText, raw}] -> [{ok} | {err, syntax}].
// The private functions are cut out of the source (each starts at "function <name>" or "async function <name>" and ends at the first "}" in column 0), stripped of types and evaluated with the listed constants; fetch answers every request with the probe's raw bytes, status and status text, and Date.now is fixed.
// flow: "exchange" (exchangeAuthorizationCode), "refresh" (refreshAccessToken for a credential with client id "cid").
// `syntax` is true when response.json() threw a SyntaxError: the V8 text of that error is not compared.
// usage: node chatgpt_responses.mjs <path to openai-chatgpt.ts>
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
const source = readFileSync(process.argv[2], "utf8");
const cut = (name) => {
	const match = new RegExp(`^(?:async )?function ${name}[\\s\\S]*?\\n}\\n`, "m").exec(source);
	if (!match) throw new Error(`${name} not found`);
	return match[0];
};
const constants = ["TOKEN_URL", "RESOURCE", "CALLBACK_PORT", "CALLBACK_PATH", "REDIRECT_URI", "DIRECT_TOKEN_SCOPE", "EXPIRY_MARGIN_MS"].map((name) => new RegExp(`^const ${name} = .*;$`, "m").exec(source)[0]).join("\n");
const names = ["requestToken", "requireTokenString", "credentialFromTokenResponse", "exchangeAuthorizationCode", "refreshAccessToken"];
const body = stripTypeScriptTypes(`${constants}\n${names.map(cut).join("\n")}`);
const fns = new Function(`${body}; return { exchangeAuthorizationCode, refreshAccessToken };`)();
Date.now = () => 1_700_000_000_000;
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const probe of JSON.parse(input)) {
	globalThis.fetch = async () => new Response(Buffer.from(probe.raw ?? "", "base64"), { status: probe.status, statusText: probe.statusText });
	const signal = new AbortController().signal;
	try {
		const value = probe.flow === "exchange" ? await fns.exchangeAuthorizationCode("code", "verifier", "cid", signal) : await fns.refreshAccessToken({ type: "oauth", refresh: "old", clientId: "cid" }, signal);
		results.push({ ok: value });
	} catch (error) {
		results.push({ err: error?.message ?? String(error), syntax: error instanceof SyntaxError });
	}
}
process.stdout.write(JSON.stringify(results));
