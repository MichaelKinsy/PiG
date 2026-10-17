// Run Pi's Meta OAuth response handling (packages/ai/src/auth/oauth/meta.ts) over raw HTTP responses from stdin: [{flow, status, raw}] -> [{ok} | {err}].
// The private functions are cut out of the source (each starts at "function <name>" or "async function <name>" and ends at the first "}" in column 0), stripped of types and evaluated with the file's constants; fetch answers every request with the probe's raw bytes and status, and Date.now is fixed.
// flow: "device" (startDeviceAuthorization), "poll" (pollForIdentityToken's poll step, run once), "mint" (mintApiKey).
// usage: node meta_responses.mjs <path to meta.ts>
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
const source = readFileSync(process.argv[2], "utf8");
const cut = (name) => {
	const match = new RegExp(`^(?:async )?function ${name}[\\s\\S]*?\\n}\\n`, "m").exec(source);
	if (!match) throw new Error(`${name} not found`);
	return match[0];
};
const constants = source.match(/^const [A-Z_]+ = .*;$/gm).join("\n");
const names = ["requestSignal", "readJson", "errorDetail", "trustedHttpUrl", "positiveNumber", "startDeviceAuthorization", "pollForIdentityToken", "mintApiKey"];
const body = stripTypeScriptTypes(`${constants}\n${names.map(cut).join("\n")}`);
const fns = new Function("pollOAuthDeviceCodeFlow", `${body}; return { startDeviceAuthorization, pollForIdentityToken, mintApiKey };`)((options) => options.poll());
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
		if (probe.flow === "device") value = await fns.startDeviceAuthorization(signal);
		else if (probe.flow === "poll") value = await fns.pollForIdentityToken({ deviceCode: "dc" }, signal);
		else value = await fns.mintApiKey("tok", signal);
		results.push({ ok: value === undefined ? null : value });
	} catch (error) {
		results.push({ err: error?.message ?? String(error) });
	}
}
process.stdout.write(JSON.stringify(results));
