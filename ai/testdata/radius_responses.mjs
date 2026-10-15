// Run Pi's Radius OAuth response handling (packages/ai/src/auth/oauth/radius.ts) over raw HTTP responses from stdin: [{flow, status, raw}] -> [{ok} | {err, syntax}].
// The private functions are cut out of the source (each starts at "function <name>", "async function <name>" or "class <name>" and ends at the first "}" in column 0), stripped of types and evaluated with the file's constants; fetch answers every request with the probe's raw bytes and status (the device authorization request of the "poll" flow gets a fixed valid response), and Date.now is fixed.
// flow: "discovery" (loadRadiusOAuthDiscovery), "token" (requestOAuthToken), "device" (requestDeviceAuthorization), "poll" (the poll step of loginWithDeviceCode, run once; the answer also carries what notify received).
// `syntax` is true when response.json() threw a SyntaxError; its V8 text is compared like any other message.
// usage: node radius_responses.mjs <path to radius.ts>
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
const source = readFileSync(process.argv[2], "utf8");
const cut = (name) => {
	const match = new RegExp(`^(?:async function|function|class) ${name}[\\s\\S]*?\\n}\\n`, "m").exec(source);
	if (!match) throw new Error(`${name} not found`);
	return match[0];
};
const constants = source.match(/^const [A-Z_]+ = .*;$/gm).join("\n");
const names = ["loadRadiusOAuthDiscovery", "OAuthResponseError", "readOAuthResponseError", "requestOAuthToken", "requestDeviceAuthorization", "loginWithDeviceCode"];
const body = stripTypeScriptTypes(`${constants}\n${names.map(cut).join("\n")}`);
const fns = new Function("pollOAuthDeviceCodeFlow", `${body}; return { loadRadiusOAuthDiscovery, requestOAuthToken, requestDeviceAuthorization, loginWithDeviceCode };`)((options) => options.poll());
Date.now = () => 1_700_000_000_000;
const gateway = "https://gw.example";
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
const device = JSON.stringify({ device_code: "dc", user_code: "uc", verification_uri: "https://v", expires_in: 600, interval: 5 });
for (const probe of JSON.parse(input)) {
	globalThis.fetch = async (url) => {
		if (probe.flow === "poll" && String(url).endsWith("/v1/oauth/device")) return new Response(device, { status: 200 });
		return new Response(Buffer.from(probe.raw ?? "", "base64"), { status: probe.status });
	};
	const signal = new AbortController().signal;
	try {
		let value;
		if (probe.flow === "discovery") value = await fns.loadRadiusOAuthDiscovery(gateway, signal);
		else if (probe.flow === "token") value = await fns.requestOAuthToken(gateway, new URLSearchParams(), signal);
		else if (probe.flow === "device") value = await fns.requestDeviceAuthorization(gateway, signal);
		else {
			const notified = [];
			value = await fns.loginWithDeviceCode(gateway, { signal, notify: (info) => notified.push(info) });
			value = { poll: value };
		}
		results.push({ ok: value === undefined ? null : value });
	} catch (error) {
		results.push({ err: error?.message ?? String(error), syntax: error instanceof SyntaxError });
	}
}
process.stdout.write(JSON.stringify(results));
