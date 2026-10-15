// Run Pi's Kimi Code OAuth response handling (packages/ai/src/auth/oauth/kimi-coding.ts) over raw HTTP responses from stdin: [{flow, status, body, previous}] -> [{ok} | {err}].
// The private functions are cut out of the source (each starts at "function <name>" or "async function <name>" and ends at the first "}" in column 0), stripped of types and evaluated with the file's constants; fetch answers every request with the probe's raw status and body, sleep does not wait, and Date.now is fixed.
// flow: "device" (startDeviceAuthorization), "refresh" (refreshToken with `previous`, retries included), "poll" (pollForToken's poll step, run once).
// usage: node kimi_responses.mjs <path to kimi-coding.ts>
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
const source = readFileSync(process.argv[2], "utf8");
const cut = (name) => {
	const match = new RegExp(`^(?:async )?function ${name}[\\s\\S]*?\\n}\\n`, "m").exec(source);
	if (!match) throw new Error(`${name} not found`);
	return match[0];
};
const constants = source.match(/^const [A-Z_]+ = .*;$/gm).join("\n");
const names = ["requestSignal", "formUrlEncode", "readJson", "trustedHttpUrl", "startDeviceAuthorization", "parseTokenResponse", "pollForToken", "isRetryableRefreshFailure", "refreshToken"];
const body = stripTypeScriptTypes(`${constants}\n${names.map(cut).join("\n")}`);
// pollOAuthDeviceCodeFlow runs poll once here: the poll step is what carries Kimi's response handling.
const fns = new Function("pollOAuthDeviceCodeFlow", "sleep", `${body}; return { startDeviceAuthorization, pollForToken, refreshToken };`)((options) => options.poll(), async () => {});
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
		if (probe.flow === "device") value = await fns.startDeviceAuthorization("https://auth.example", signal);
		else if (probe.flow === "refresh") value = await fns.refreshToken("https://auth.example", probe.previous, signal);
		else value = await fns.pollForToken("https://auth.example", { deviceCode: "dc", intervalSeconds: 1, expiresInSeconds: 60 }, signal);
		results.push({ ok: value === undefined ? null : value });
	} catch (error) {
		results.push({ err: error?.message ?? String(error) });
	}
}
process.stdout.write(JSON.stringify(results));
