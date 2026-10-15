// Run Pi's OpenAI Codex OAuth response handling (packages/ai/src/auth/oauth/openai-codex.ts) over raw HTTP responses from stdin: [{flow, status, statusText, body, previous}] -> [{ok} | {err, syntax}].
// The private functions are cut out of the source (each starts at "function <name>" or "async function <name>" and ends at the first "}" in column 0), stripped of types and evaluated with the file's constants; fetch answers every request with the probe's raw status and body, Date.now is fixed, and pollOAuthDeviceCodeFlow runs poll once.
// flow: "exchange" (exchangeAuthorizationCode), "refresh" (refreshAccessToken with `previous`), "device" (startOpenAICodexDeviceAuth), "poll" (pollOpenAICodexDeviceAuth's poll step).
// `syntax` is true when response.json() threw: the V8 text of that error is not compared.
// usage: node codex_responses.mjs <path to openai-codex.ts>
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
const source = readFileSync(process.argv[2], "utf8");
const cut = (name) => {
	const match = new RegExp(`^(?:async )?function ${name}[\\s\\S]*?\\n}\\n`, "m").exec(source);
	if (!match) throw new Error(`${name} not found`);
	return match[0];
};
const constants = source.match(/^const [A-Z_]+ = .*;$/gm).join("\n");
const names = ["fetchWithLoginCancellation", "readTokenResponse", "exchangeAuthorizationCode", "refreshAccessToken", "startOpenAICodexDeviceAuth", "pollOpenAICodexDeviceAuth"];
const body = stripTypeScriptTypes(`${constants}\n${names.map(cut).join("\n")}`);
const fns = new Function("pollOAuthDeviceCodeFlow", `${body}; return { exchangeAuthorizationCode, refreshAccessToken, startOpenAICodexDeviceAuth, pollOpenAICodexDeviceAuth };`)((options) => options.poll());
Date.now = () => 1_700_000_000_000;
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const probe of JSON.parse(input)) {
	globalThis.fetch = async () => new Response(Buffer.from(probe.raw ?? "", "base64"), { status: probe.status, statusText: probe.statusText });
	const signal = new AbortController().signal;
	try {
		let value;
		if (probe.flow === "exchange") value = await fns.exchangeAuthorizationCode("code", "verifier", "http://localhost/cb", signal);
		else if (probe.flow === "refresh") value = await fns.refreshAccessToken(probe.previous, signal);
		else if (probe.flow === "device") value = await fns.startOpenAICodexDeviceAuth(signal);
		else value = await fns.pollOpenAICodexDeviceAuth({ deviceAuthId: "id", userCode: "uc", intervalSeconds: 1 }, signal);
		results.push({ ok: value === undefined ? null : value });
	} catch (error) {
		results.push({ err: error?.message ?? String(error), syntax: error instanceof SyntaxError });
	}
}
process.stdout.write(JSON.stringify(results));
