// Run Pi's xAI OAuth response handling (packages/ai/src/auth/oauth/xai.ts) over raw HTTP responses from stdin: [{flow, status, body, previous}] -> [{ok} | {err}].
// The private functions are cut out of the source (each starts at "function <name>" or "async function <name>" and ends at the first "}" in column 0), stripped of types and evaluated with the file's constants; fetch answers every request with the probe's raw status and body, Date.now is fixed.
// flow: "device" (requestDeviceCode), "refresh" (refreshXaiToken with `previous`), "poll" (pollForTokens' poll step, run once).
// usage: node xai_responses.mjs <path to xai.ts>
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
const source = readFileSync(process.argv[2], "utf8");
const cut = (name) => {
	const match = new RegExp(`^(?:async )?function ${name}[\\s\\S]*?\\n}\\n`, "m").exec(source);
	if (!match) throw new Error(`${name} not found`);
	return match[0];
};
const constants = source.match(/^const [A-Z_]+ = .*;$/gm).join("\n");
const names = ["requiredString", "positiveNumber", "validateVerificationUri", "postForm", "requestFailure", "parseDeviceCode", "credentialsFromTokenResponse", "requestDeviceCode", "pollForTokens", "refreshXaiToken"];
const body = stripTypeScriptTypes(`${constants}\n${names.map(cut).join("\n")}`);
// pollOAuthDeviceCodeFlow runs poll once here: the poll step is what carries xAI's response handling.
const fns = new Function("pollOAuthDeviceCodeFlow", `${body}; return { requestDeviceCode, pollForTokens, refreshXaiToken };`)((options) => options.poll());
const realNow = Date.now;
Date.now = () => 1_700_000_000_000;
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const probe of JSON.parse(input)) {
	globalThis.fetch = async () => new Response(Buffer.from(probe.raw ?? "", "base64"), { status: probe.status });
	const signal = new AbortController().signal;
	try {
		let value;
		if (probe.flow === "device") value = await fns.requestDeviceCode(signal);
		else if (probe.flow === "refresh") value = await fns.refreshXaiToken(probe.previous, signal);
		else value = await fns.pollForTokens({ deviceCode: "dc", intervalSeconds: 1, expiresInSeconds: 60 }, signal);
		results.push({ ok: value === undefined ? null : value });
	} catch (error) {
		results.push({ err: error?.message ?? String(error) });
	}
}
Date.now = realNow;
process.stdout.write(JSON.stringify(results));
